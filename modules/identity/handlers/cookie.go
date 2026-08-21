package handlers

import (
	"net/http"
	"time"
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

func clientIP(r *http.Request) string {
	return r.RemoteAddr
}
