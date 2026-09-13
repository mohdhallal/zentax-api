package handlers

import (
	"net/http"
	"time"

	"github.com/mohamadhallal/zentax-api/platform/securityevent"
)

// CookieConfig controls the session cookie the auth handlers write. httpOnly +
// SameSite=Strict; Secure per config (ADR-0011).
type CookieConfig struct {
	Name        string
	Secure      bool
	AbsoluteTTL time.Duration
}

func (c CookieConfig) set(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     c.Name,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   c.Secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(c.AbsoluteTTL.Seconds()),
	})
}

func (c CookieConfig) clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     c.Name,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   c.Secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}

func (c CookieConfig) token(r *http.Request) string {
	ck, err := r.Cookie(c.Name)
	if err != nil {
		return ""
	}
	return ck.Value
}

// clientIP is the caller's address for sessions.ip: the one the global chain
// resolved through the configured trusted-proxy list
// (middlewares.ClientAddressMiddleware), not the socket peer.
//
// The difference is the whole value of the column. Behind the ALB — or behind
// the self-host web container, which is the only thing that reaches the API —
// r.RemoteAddr is our own infrastructure, so "the sessions this account has
// open, and where from" would answer with one internal address for every
// session in the installation. A router built without the global chain (a unit
// test, a future embedding) binds nothing and falls back to the socket peer,
// which is exactly right for a direct connection.
func clientIP(r *http.Request) string {
	if addr := securityevent.ClientAddr(r.Context()); addr != "" {
		return addr
	}
	return r.RemoteAddr
}
