package middlewares_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/middlewares"
)

func TestRequestIdMiddleware_NoHeader_GeneratesUUID(t *testing.T) {
	t.Parallel()

	var capturedID string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedID = app.GetRequestId(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	w := httptest.NewRecorder()

	middlewares.RequestIdMiddleware(next).ServeHTTP(w, r)

	require.NotEmpty(t, capturedID)
	_, err := uuid.Parse(capturedID)
	assert.NoError(t, err, "generated request ID must be a valid UUID")
}

// A caller's own UUID is honoured — tracing across the tiers is why the header
// exists — and lowercased, because the security stream's column is a UUID and
// Postgres renders it lowercase: a capitalised spelling would join to nothing.
func TestRequestIdMiddleware_ExistingHeader_Reused(t *testing.T) {
	t.Parallel()

	const existingID = "0f8fad5b-d9cb-469f-a165-70867728950e"
	for _, sent := range []string{existingID, strings.ToUpper(existingID)} {
		var capturedID string
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedID = app.GetRequestId(r.Context())
			w.WriteHeader(http.StatusOK)
		})

		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
		r.Header.Set("X-Request-Id", sent)
		w := httptest.NewRecorder()

		middlewares.RequestIdMiddleware(next).ServeHTTP(w, r)

		assert.Equal(t, existingID, capturedID, "sent %q", sent)
	}
}

// THE DOOR THIS MIDDLEWARE IS. Whatever it binds is written into audit_log,
// folded into the hash chain, and exported to a bucket that keeps it for ten
// years and lets nobody delete from it. A header of any other shape therefore
// costs the caller its correlation and nothing else: a fresh id is minted, and
// not one byte of what was sent survives anywhere.
func TestRequestIdMiddleware_HostileHeader_IsReplacedByAFreshUUID(t *testing.T) {
	t.Parallel()

	hostile := []string{
		"'; DROP TABLE audit_log; -- jane@example.com",
		"my-custom-request-id-123",
		"trace-abc",
		"0f8fad5b-d9cb-469f-a165-70867728950",  // one short
		"0f8fad5bd9cb469fa16570867728950e",     // unhyphenated
		"0f8fad5b-d9cb-469f-a165-7086772895ZZ", // not hex
		"0f8fad5b-d9cb-469f-a165-70867728950e ",
		strings.Repeat("a", 8192),
		"<script>alert(1)</script>",
	}

	for _, sent := range hostile {
		var capturedID string
		next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			capturedID = app.GetRequestId(r.Context())
			w.WriteHeader(http.StatusOK)
		})

		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
		r.Header.Set("X-Request-Id", sent)

		middlewares.RequestIdMiddleware(next).ServeHTTP(httptest.NewRecorder(), r)

		require.NotEqual(t, sent, capturedID, "caller text must not become the request id")
		require.NotContains(t, sent, capturedID, "no fragment of the caller's header may survive")
		_, err := uuid.Parse(capturedID)
		assert.NoError(t, err, "a refused header must be replaced by a minted UUID, not left empty")
	}
}

func TestRequestIdMiddleware_EachRequest_UniqueID(t *testing.T) {
	t.Parallel()

	ids := make([]string, 0, 3)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// index stored via query param for simplicity
		ids = append(ids, app.GetRequestId(r.Context()))
		w.WriteHeader(http.StatusOK)
	})

	mw := middlewares.RequestIdMiddleware(next)

	for i := 0; i < 3; i++ {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
		w := httptest.NewRecorder()
		mw.ServeHTTP(w, r)
	}

	seen := map[string]struct{}{}
	for _, id := range ids {
		if id == "" {
			continue
		}
		_, duplicate := seen[id]
		assert.False(t, duplicate, "duplicate request ID: %s", id)
		seen[id] = struct{}{}
	}
}

func TestRequestIdMiddleware_StoredInContext(t *testing.T) {
	t.Parallel()

	const sent = "3f2504e0-4f89-41d3-9a0c-0305e82c3301"
	var ctxID string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctxID = app.GetRequestId(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	r.Header.Set("X-Request-Id", sent)

	middlewares.RequestIdMiddleware(next).ServeHTTP(httptest.NewRecorder(), r)

	assert.Equal(t, sent, ctxID)
}
