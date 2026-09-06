package middlewares_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/delivery/httpkit/middlewares"
)

const (
	apiHost      = "api.zentax.test"
	sameOrigin   = "https://api.zentax.test"
	otherOrigin  = "https://evil.example"
	sessionCooky = "zentax_session=abc123"
	apiToken     = "Bearer ztx_live_abc123"
)

// The full decision table of CrossOriginGuard: method × Origin × credential.
// The defect this replaces let a foreign page POST /auth/logout with the
// victim's cookie, because the check only ran for routes that declared a tenant.
func TestCrossOriginGuard_DecisionTable(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		method string
		origin string
		authz  string
		cookie string
		want   int
	}{
		// Reads never change state, whoever asks and from wherever.
		{"GET, no origin, no credential", http.MethodGet, "", "", "", http.StatusOK},
		{"GET, same origin, cookie", http.MethodGet, sameOrigin, "", sessionCooky, http.StatusOK},
		{"GET, foreign origin, cookie", http.MethodGet, otherOrigin, "", sessionCooky, http.StatusOK},
		{"GET, foreign origin, bearer", http.MethodGet, otherOrigin, apiToken, "", http.StatusOK},

		// No Origin: server-to-server clients, curl, the Express adapter.
		{"POST, no origin, no credential", http.MethodPost, "", "", "", http.StatusOK},
		{"POST, no origin, cookie", http.MethodPost, "", "", sessionCooky, http.StatusOK},

		// Same origin: the product's own browser app.
		{"POST, same origin, cookie", http.MethodPost, sameOrigin, "", sessionCooky, http.StatusOK},
		{"PUT, same origin, cookie", http.MethodPut, sameOrigin, "", sessionCooky, http.StatusOK},

		// Foreign origin + ambient (or absent) credentials: CSRF, refused —
		// including before authentication, so an unauthenticated probe is
		// refused identically.
		{"POST, foreign origin, cookie", http.MethodPost, otherOrigin, "", sessionCooky, http.StatusForbidden},
		{"POST, foreign origin, no credential", http.MethodPost, otherOrigin, "", "", http.StatusForbidden},
		{"PUT, foreign origin, cookie", http.MethodPut, otherOrigin, "", sessionCooky, http.StatusForbidden},
		{"PATCH, foreign origin, cookie", http.MethodPatch, otherOrigin, "", sessionCooky, http.StatusForbidden},
		{"DELETE, foreign origin, cookie", http.MethodDelete, otherOrigin, "", sessionCooky, http.StatusForbidden},
		{"POST, unparseable origin", http.MethodPost, "://nonsense", "", sessionCooky, http.StatusForbidden},
		{"POST, origin null (sandboxed frame)", http.MethodPost, "null", "", sessionCooky, http.StatusForbidden},

		// A bearer API token is not ambient authority: a browser never attaches
		// it for a foreign page, so a cross-origin service-account call is fine.
		{"POST, foreign origin, bearer", http.MethodPost, otherOrigin, apiToken, "", http.StatusOK},
		{"DELETE, foreign origin, bearer", http.MethodDelete, otherOrigin, apiToken, "", http.StatusOK},
		// Anything that is not a ZenTax API token falls through to cookie auth,
		// so it is checked like a cookie request.
		{"POST, foreign origin, foreign bearer", http.MethodPost, otherOrigin, "Bearer other", "", http.StatusForbidden},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			reached := false
			guard := middlewares.CrossOriginGuard()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reached = true
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequestWithContext(t.Context(), tc.method, "https://"+apiHost+"/auth/logout", http.NoBody)
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.authz != "" {
				req.Header.Set("Authorization", tc.authz)
			}
			if tc.cookie != "" {
				req.Header.Set("Cookie", tc.cookie)
			}

			rr := httptest.NewRecorder()
			guard.ServeHTTP(rr, req)

			assert.Equal(t, tc.want, rr.Code)
			assert.Equal(t, tc.want == http.StatusOK, reached, "handler reached")
		})
	}
}

func TestCrossOriginGuard_RejectionBodyIsTheForbiddenEnvelope(t *testing.T) {
	t.Parallel()

	guard := middlewares.CrossOriginGuard()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("handler must not run")
	}))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "https://"+apiHost+"/auth/login",
		strings.NewReader(`{"email":"a@b.com","password":"x"}`))
	req.Header.Set("Origin", otherOrigin)

	rr := httptest.NewRecorder()
	guard.ServeHTTP(rr, req)

	require.Equal(t, http.StatusForbidden, rr.Code)
	assert.Contains(t, rr.Body.String(), "FORBIDDEN")
	assert.Contains(t, rr.Body.String(), "cross-origin request rejected")
}

// A request whose Origin differs only in port is still cross-origin.
func TestCrossOriginGuard_PortIsPartOfTheOrigin(t *testing.T) {
	t.Parallel()

	guard := middlewares.CrossOriginGuard()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "http://localhost:3000/auth/login", http.NoBody)
	req.Header.Set("Origin", "http://localhost:5000")

	rr := httptest.NewRecorder()
	guard.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusForbidden, rr.Code)
}
