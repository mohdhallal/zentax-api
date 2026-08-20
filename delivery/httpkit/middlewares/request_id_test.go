package middlewares_test

import (
	"net/http"
	"net/http/httptest"
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

func TestRequestIdMiddleware_ExistingHeader_Reused(t *testing.T) {
	t.Parallel()

	existingID := "my-custom-request-id-123"
	var capturedID string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedID = app.GetRequestId(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	r.Header.Set("X-Request-Id", existingID)
	w := httptest.NewRecorder()

	middlewares.RequestIdMiddleware(next).ServeHTTP(w, r)

	assert.Equal(t, existingID, capturedID)
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

	var ctxID string
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctxID = app.GetRequestId(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	r.Header.Set("X-Request-Id", "trace-abc")

	middlewares.RequestIdMiddleware(next).ServeHTTP(httptest.NewRecorder(), r)

	assert.Equal(t, "trace-abc", ctxID)
}
