package apiclient

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLogin_CapturesSessionCookie(t *testing.T) {
	t.Parallel()
	var loginBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/auth/login":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&loginBody))
			assert.Empty(t, r.Header.Get("Origin"))
			http.SetCookie(w, &http.Cookie{Name: SessionCookieName, Value: "sess-1", Path: "/"})
			writeEnvelope(t, w, http.StatusOK, map[string]any{
				"status": true,
				"data": map[string]any{
					"mfaRequired": false,
					"user": map[string]any{
						"id": "u-1", "tenantId": "t-1", "email": "admin@acme.test",
						"name": "Group Head of Tax", "status": "active", "mfaEnabled": false,
					},
				},
			})
		case "/auth/me":
			assert.Equal(t, SessionCookieName+"=sess-1", r.Header.Get("Cookie"))
			writeEnvelope(t, w, http.StatusOK, map[string]any{
				"status": true,
				"data": map[string]any{
					"id": "u-1", "tenantId": "t-1", "email": "admin@acme.test", "name": "Group Head of Tax",
					"status": "active", "mfaEnabled": false,
					"tenant": map[string]any{
						"id": "t-1", "slug": "acme", "name": "Acme Group", "timezone": "Europe/Berlin",
					},
				},
			})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	client := New(srv.URL)
	session, err := client.Login(t.Context(), "admin@acme.test", "demo-password-1")
	require.NoError(t, err)

	assert.Equal(t, "sess-1", session.Token)
	assert.Equal(t, "sess-1", session.SessionToken())
	assert.False(t, session.MFARequired)
	assert.Equal(t, "u-1", session.User.ID)
	assert.Equal(t, "admin@acme.test", session.User.Email)
	assert.Equal(t, map[string]string{"email": "admin@acme.test", "password": "demo-password-1"}, loginBody)
	assert.Empty(t, client.SessionToken(), "logging in does not bind the parent client")

	me, err := session.Me(t.Context())
	require.NoError(t, err)
	assert.Equal(t, "u-1", me.ID)
	assert.Equal(t, "acme", me.Tenant.Slug)
	assert.Equal(t, "Europe/Berlin", me.Tenant.Timezone)
}

func TestLogin_WrongPasswordIsAnAPIError(t *testing.T) {
	t.Parallel()
	client, _ := newStub(t, func(w http.ResponseWriter, _ *http.Request) {
		writeEnvelope(t, w, http.StatusUnauthorized, map[string]any{
			"status": false,
			"error":  map[string]any{"code": "UNAUTHORIZED", "message": "invalid credentials"},
		})
	})
	_, err := client.Login(t.Context(), "admin@acme.test", "nope")
	require.Error(t, err)
	assert.True(t, IsUnauthorized(err))
	assert.True(t, IsCode(err, "UNAUTHORIZED"))
}

func TestLogin_MissingCookieIsAnError(t *testing.T) {
	t.Parallel()
	client, _ := newStub(t, func(w http.ResponseWriter, _ *http.Request) {
		writeEnvelope(t, w, http.StatusOK, map[string]any{
			"status": true,
			"data":   map[string]any{"mfaRequired": false, "user": map[string]any{"id": "u-1"}},
		})
	})
	_, err := client.Login(t.Context(), "a@b.test", "pw")
	require.Error(t, err)
	assert.Contains(t, err.Error(), SessionCookieName)
}

func TestLogin_MFARequiredIsReportedNotFailed(t *testing.T) {
	t.Parallel()
	client, _ := newStub(t, func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: SessionCookieName, Value: "pending", Path: "/"})
		writeEnvelope(t, w, http.StatusOK, map[string]any{
			"status": true,
			"data":   map[string]any{"mfaRequired": true, "user": map[string]any{"id": "u-2"}},
		})
	})
	session, err := client.Login(t.Context(), "mfa@acme.test", "pw")
	require.NoError(t, err)
	assert.True(t, session.MFARequired)
	assert.Equal(t, "pending", session.Token)
}

func TestSessionsAreIndependent(t *testing.T) {
	t.Parallel()
	var (
		mu      sync.Mutex
		cookies []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth/login" {
			var body map[string]string
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			http.SetCookie(w, &http.Cookie{Name: SessionCookieName, Value: "sess-" + body["email"], Path: "/"})
			writeEnvelope(t, w, http.StatusOK, map[string]any{
				"status": true,
				"data":   map[string]any{"mfaRequired": false, "user": map[string]any{"email": body["email"]}},
			})
			return
		}
		mu.Lock()
		cookies = append(cookies, r.Header.Get("Cookie"))
		mu.Unlock()
		writeEnvelope(t, w, http.StatusOK, map[string]any{"status": true, "data": map[string]any{}})
	}))
	defer srv.Close()

	client := New(srv.URL)
	preparer, err := client.Login(t.Context(), "preparer@acme.test", "pw")
	require.NoError(t, err)
	reviewer, err := client.Login(t.Context(), "reviewer@acme.test", "pw")
	require.NoError(t, err)

	require.NoError(t, preparer.PUT(t.Context(), "/task-instances/t-1", map[string]any{"status": "in_progress"}, nil))
	require.NoError(t, reviewer.POST(t.Context(), "/task-instances/t-1/approve", nil, nil))

	assert.Equal(t, []string{
		SessionCookieName + "=sess-preparer@acme.test",
		SessionCookieName + "=sess-reviewer@acme.test",
	}, cookies)
}

func TestAcceptInvite(t *testing.T) {
	t.Parallel()
	var body map[string]any
	client, _ := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		writeEnvelope(t, w, http.StatusOK, map[string]any{
			"status": true,
			"data":   map[string]any{"email": "preparer@acme.test"},
		})
	})

	email, err := client.AcceptInvite(t.Context(), "zti_token", "demo-password-1", "Jane Doe")
	require.NoError(t, err)
	assert.Equal(t, "preparer@acme.test", email)
	assert.Equal(t, map[string]any{"token": "zti_token", "password": "demo-password-1", "name": "Jane Doe"}, body)
}

func TestAcceptInvite_OmitsEmptyName(t *testing.T) {
	t.Parallel()
	var body map[string]any
	client, _ := newStub(t, func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		writeEnvelope(t, w, http.StatusOK, map[string]any{"status": true, "data": map[string]any{"email": "x@y.test"}})
	})
	_, err := client.AcceptInvite(t.Context(), "zti_token", "demo-password-1", "")
	require.NoError(t, err)
	_, hasName := body["name"]
	assert.False(t, hasName, "an empty name must not be sent (the API validates min=1)")
}

func TestMe_Unauthorized(t *testing.T) {
	t.Parallel()
	client, _ := newStub(t, func(w http.ResponseWriter, _ *http.Request) {
		writeEnvelope(t, w, http.StatusUnauthorized, map[string]any{
			"status": false,
			"error":  map[string]any{"code": "UNAUTHORIZED", "message": "no session"},
		})
	})
	_, err := client.Me(t.Context())
	require.True(t, IsUnauthorized(err))
}
