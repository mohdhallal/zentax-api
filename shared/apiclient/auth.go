package apiclient

import (
	"context"
	"fmt"
	"net/http"
)

// User is the public view of a user (dto.UserToJSON) — never auth material.
type User struct {
	ID         string `json:"id"`
	TenantID   string `json:"tenantId"`
	Email      string `json:"email"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	MFAEnabled bool   `json:"mfaEnabled"`
	CreatedAt  string `json:"createdAt"`
}

// TenantRef is the tenant block /auth/me embeds. Timezone is the IANA zone the
// report SQL and the UI compute "today" in (ADR-0003).
type TenantRef struct {
	ID       string `json:"id"`
	Slug     string `json:"slug"`
	Name     string `json:"name"`
	Timezone string `json:"timezone"`
}

// Identity is the /auth/me payload: the caller plus their tenant.
type Identity struct {
	User
	Tenant TenantRef `json:"tenant"`
}

// Session is a Client bound to one logged-in user's session cookie. Because it
// embeds *Client, every request method is available on it directly.
type Session struct {
	*Client

	// Token is the zentax_session cookie value backing this session.
	Token string
	// User is who logged in.
	User User
	// MFARequired is true when the login left the session in the mfa_pending
	// state: the token authenticates /auth/mfa/verify and nothing else.
	MFARequired bool
}

// Login posts /auth/login and captures the zentax_session cookie. The returned
// Session shares the receiver's *http.Client, so several users can be logged in
// and driven from one process.
//
// A login that needs a second factor is NOT an error: it returns a Session with
// MFARequired set, whose token only gets past /auth/mfa/verify.
func (c *Client) Login(ctx context.Context, email, password string) (*Session, error) {
	const path = "/auth/login"

	var payload struct {
		MFARequired bool `json:"mfaRequired"`
		User        User `json:"user"`
	}
	raw, err := c.request(ctx, http.MethodPost, path,
		map[string]string{"email": email, "password": password})
	if err != nil {
		return nil, err
	}
	if _, err := decodeEnvelope(http.MethodPost, path, raw, &payload); err != nil {
		return nil, err
	}

	token := sessionCookie(raw.header)
	if token == "" {
		return nil, fmt.Errorf("apiclient: POST %s: no %s cookie in the response", path, SessionCookieName)
	}
	return &Session{
		Client:      c.WithSession(token),
		Token:       token,
		User:        payload.User,
		MFARequired: payload.MFARequired,
	}, nil
}

// AcceptInvite redeems an invite token (the public POST /auth/accept-invite):
// the invited person sets their password and becomes active. name is optional —
// pass "" to keep the name the inviting admin gave. It returns the account's
// e-mail address, as the API does. No session is involved; call this on an
// anonymous client, then Login.
func (c *Client) AcceptInvite(ctx context.Context, token, password, name string) (string, error) {
	body := map[string]any{"token": token, "password": password}
	if name != "" {
		body["name"] = name
	}
	var out struct {
		Email string `json:"email"`
	}
	if err := c.POST(ctx, "/auth/accept-invite", body, &out); err != nil {
		return "", err
	}
	return out.Email, nil
}

// Me returns the calling session's user and tenant (GET /auth/me). It is also
// the cheapest check that a session is still valid.
func (c *Client) Me(ctx context.Context) (*Identity, error) {
	var me Identity
	if err := c.GET(ctx, "/auth/me", nil, &me); err != nil {
		return nil, err
	}
	return &me, nil
}

// sessionCookie reads the zentax_session value out of a response's Set-Cookie
// headers.
func sessionCookie(header http.Header) string {
	resp := http.Response{Header: header}
	for _, cookie := range resp.Cookies() {
		if cookie.Name == SessionCookieName {
			return cookie.Value
		}
	}
	return ""
}
