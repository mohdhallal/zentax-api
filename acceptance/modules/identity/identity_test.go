package identity_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
	"github.com/stretchr/testify/suite"

	"github.com/mohamadhallal/zentax-api/acceptance"
)

type IdentitySuite struct {
	acceptance.Suite
}

func TestIdentitySuite(t *testing.T) {
	suite.Run(t, new(IdentitySuite))
}

const cookieName = "zentax_session"

func sessionCookie(resp *acceptance.TestResponse) string {
	for _, c := range resp.Cookies() {
		if c.Name == cookieName {
			return c.Value
		}
	}
	return ""
}

// TestLoginSessionLogout covers the core flow: bad password → 401, login → session
// cookie, session authenticates /auth/me + a tenant route, logout revokes it.
func (s *IdentitySuite) TestLoginSessionLogout() {
	tenant := s.InsertTenant("auth-a", "Auth Tenant A")
	s.InsertUserWithPassword(tenant, "admin@acme.com", "s3cret-password")

	s.Client.External().
		POST(s.T(), "/auth/login", map[string]any{"email": "admin@acme.com", "password": "nope"}).
		AssertStatus(s.T(), http.StatusUnauthorized)

	login := s.Client.External().
		POST(s.T(), "/auth/login", map[string]any{"email": "admin@acme.com", "password": "s3cret-password"})
	login.AssertStatus(s.T(), http.StatusOK)
	token := sessionCookie(login)
	s.Require().NotEmpty(token)

	var me struct {
		Email string `json:"email"`
	}
	s.Client.External().WithSession(token).GET(s.T(), "/auth/me").DecodeData(s.T(), &me)
	s.Require().Equal("admin@acme.com", me.Email)

	// the session authenticates a tenant route (tenant comes from the session)
	s.Client.External().WithSession(token).GET(s.T(), "/entities").AssertStatus(s.T(), http.StatusOK)

	s.Client.External().WithSession(token).POST(s.T(), "/auth/logout", nil).AssertStatus(s.T(), http.StatusNoContent)
	s.Client.External().WithSession(token).GET(s.T(), "/auth/me").AssertStatus(s.T(), http.StatusUnauthorized)
}

// TestMfaFlow covers enroll → enable → re-login (mfa_pending) → verify → full
// session, including that a pending session cannot reach tenant routes.
func (s *IdentitySuite) TestMfaFlow() {
	tenant := s.InsertTenant("auth-b", "Auth Tenant B")
	s.InsertUserWithPassword(tenant, "mfa@acme.com", "s3cret-password")

	login := s.Client.External().
		POST(s.T(), "/auth/login", map[string]any{"email": "mfa@acme.com", "password": "s3cret-password"})
	login.AssertStatus(s.T(), http.StatusOK)
	token := sessionCookie(login)

	var enroll struct {
		Secret string `json:"secret"`
	}
	s.Client.External().WithSession(token).POST(s.T(), "/auth/mfa/enroll", nil).DecodeData(s.T(), &enroll)
	s.Require().NotEmpty(enroll.Secret)

	code, err := totp.GenerateCode(enroll.Secret, time.Now())
	s.Require().NoError(err)
	s.Client.External().WithSession(token).
		POST(s.T(), "/auth/mfa/enable", map[string]any{"code": code}).
		AssertStatus(s.T(), http.StatusNoContent)

	// re-login now requires MFA
	login2 := s.Client.External().
		POST(s.T(), "/auth/login", map[string]any{"email": "mfa@acme.com", "password": "s3cret-password"})
	login2.AssertStatus(s.T(), http.StatusOK)
	var lr struct {
		MFARequired bool `json:"mfaRequired"`
	}
	login2.DecodeData(s.T(), &lr)
	s.Require().True(lr.MFARequired)
	pending := sessionCookie(login2)

	// a pending session cannot reach a tenant route
	s.Client.External().WithSession(pending).GET(s.T(), "/entities").AssertStatus(s.T(), http.StatusUnauthorized)

	// verify → full session (rotated token)
	code2, err := totp.GenerateCode(enroll.Secret, time.Now())
	s.Require().NoError(err)
	verify := s.Client.External().WithSession(pending).
		POST(s.T(), "/auth/mfa/verify", map[string]any{"code": code2})
	verify.AssertStatus(s.T(), http.StatusOK)
	full := sessionCookie(verify)
	s.Require().NotEmpty(full)

	s.Client.External().WithSession(full).GET(s.T(), "/entities").AssertStatus(s.T(), http.StatusOK)
}
