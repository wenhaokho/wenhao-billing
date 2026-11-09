package auth

import (
	"net/http"
	"time"
)

// CookieSpec names a session cookie. Every cookie set here is HttpOnly and
// SameSite=Lax.
type CookieSpec struct {
	Name   string
	Domain string
	Path   string
	Secure bool
	MaxAge time.Duration
}

// SetCookie sets the cookie to value.
func SetCookie(w http.ResponseWriter, c CookieSpec, value string) {
	//nolint:gosec // Secure is the caller's choice: local development runs on plain http.
	http.SetCookie(w, &http.Cookie{
		Name: c.Name, Value: value, Domain: c.Domain, Path: c.Path,
		MaxAge: int(c.MaxAge.Seconds()), Secure: c.Secure, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}

// ClearCookie removes the cookie.
func ClearCookie(w http.ResponseWriter, c CookieSpec) {
	//nolint:gosec // Secure is the caller's choice: local development runs on plain http.
	http.SetCookie(w, &http.Cookie{
		Name: c.Name, Value: "", Domain: c.Domain, Path: c.Path,
		MaxAge: -1, Secure: c.Secure, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
}
