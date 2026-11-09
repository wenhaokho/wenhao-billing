package oauth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	_ "embed" // the consent page
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/LOQATO/gokit/auth"

	"github.com/wenhaokho/wenhao-billing/backend/internal/oauth/oauthdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// consentTemplate is Python's consent page, text for text, with two slots.
//
//go:embed consent.html
var consentTemplate string

const (
	// csrfCookie holds the consent form's CSRF token between the authorize
	// GET and the consent POST. Python kept it in its server-side session;
	// the Go session is a stateless token, so the value rides in its own
	// short-lived HttpOnly cookie and is compared with the form's copy.
	csrfCookie = "oauth_csrf"
	csrfTTL    = 10 * time.Minute
	csrfPath   = "/oauth/"
	// maxFormBody bounds a POSTed OAuth form; a real one is under 4 KiB.
	maxFormBody = 64 << 10
)

// authorizeParams are the request parameters the authorize GET and the
// consent POST share.
type authorizeParams struct {
	responseType, clientID, redirectURI, codeChallenge, codeChallengeMethod string
	state                                                                   string
	hasState                                                                bool
}

// last is a parameter's last value, as Starlette read query and form
// parameters, and whether it was present at all.
func last(v url.Values, k string) (string, bool) {
	vs, ok := v[k]
	if !ok || len(vs) == 0 {
		return "", false
	}
	return vs[len(vs)-1], true
}

func readParams(v url.Values) authorizeParams {
	var p authorizeParams
	p.responseType, _ = last(v, "response_type")
	p.clientID, _ = last(v, "client_id")
	p.redirectURI, _ = last(v, "redirect_uri")
	p.codeChallenge, _ = last(v, "code_challenge")
	var ok bool
	if p.codeChallengeMethod, ok = last(v, "code_challenge_method"); !ok {
		p.codeChallengeMethod = "S256" // Python's default
	}
	p.state, p.hasState = last(v, "state")
	return p
}

// validate is Python's _validate_authorize_request: the checks in its order,
// each failure a JSON error and never a redirect, because the redirect URI is
// not trusted until it matches a registered one exactly. It returns the
// client's label (its name, else its id) when the request is valid.
func (h handler) validate(w http.ResponseWriter, r *http.Request, p authorizeParams) (string, bool) {
	switch {
	case p.responseType != "code":
		oauthError(w, http.StatusBadRequest, "unsupported_response_type", "response_type must be 'code'")
		return "", false
	case p.clientID == "" || p.redirectURI == "":
		oauthError(w, http.StatusBadRequest, "invalid_request", "client_id and redirect_uri are required")
		return "", false
	case p.codeChallenge == "":
		oauthError(w, http.StatusBadRequest, "invalid_request", "code_challenge is required (PKCE)")
		return "", false
	case p.codeChallengeMethod != "S256":
		oauthError(w, http.StatusBadRequest, "invalid_request", "code_challenge_method must be 'S256'")
		return "", false
	}
	client, err := oauthdb.New(h.pool).GetOAuthClient(r.Context(), p.clientID)
	if errors.Is(err, pgx.ErrNoRows) {
		oauthError(w, http.StatusUnauthorized, "invalid_client", "unknown client_id")
		return "", false
	}
	if err != nil {
		panic(err) // the recoverer logs it, reports it and answers 500
	}
	if !slices.Contains(strings.Fields(client.RedirectUris), p.redirectURI) {
		oauthError(w, http.StatusBadRequest, "invalid_request", "redirect_uri is not registered for this client")
		return "", false
	}
	if client.ClientName != nil && *client.ClientName != "" {
		return *client.ClientName, true
	}
	return p.clientID, true
}

// adminSession is the signed-in admin, or ok false for no live session
// (live: false) or a session that is not an admin's (live: true).
func (h handler) adminSession(r *http.Request) (p platform.Principal, live, ok bool) {
	p, err := h.sessions.RequestPrincipal(r.Context(), h.pool, r)
	if errors.Is(err, platform.ErrNoLiveSession) {
		return platform.Principal{}, false, false
	}
	if err != nil {
		panic(err)
	}
	return p, true, p.Role == "admin"
}

// authorize is GET /oauth/authorize. It only ever renders the consent page:
// a cross-site link on a live admin session must not mint a code by itself.
func (h handler) authorize(w http.ResponseWriter, r *http.Request) {
	p := readParams(r.URL.Query())
	label, ok := h.validate(w, r, p)
	if !ok {
		return
	}
	user, live, admin := h.adminSession(r)
	if !live {
		// Go change 3: return_to is this path and query, not an absolute
		// URL, so the frontend's same-origin check holds behind the proxy.
		// Go change 2: an expired or signed-out session lands here too.
		returnTo := "/oauth/authorize?" + r.URL.RawQuery
		w.Header().Set("Location", h.appBase+"/login?"+url.Values{"return_to": {returnTo}}.Encode())
		w.WriteHeader(http.StatusFound)
		return
	}
	if !admin {
		oauthError(w, http.StatusForbidden, "access_denied", "admin role required")
		return
	}
	// A fresh token per GET (kept from Python): with two authorize tabs open,
	// only the last page's form is accepted.
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(fmt.Sprintf("oauth: random csrf token: %v", err))
	}
	nonce := base64.RawURLEncoding.EncodeToString(b[:]) // 43 characters, as secrets.token_urlsafe(32)
	token := nonce + "." + h.tokens.csrfMAC(nonce, user)
	auth.SetCookie(w, h.csrfCookie(csrfTTL), token)

	var state *string
	if p.hasState {
		state = &p.state
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(consentPage(label, token, p, state)))
}

// csrfMAC binds a CSRF nonce to the session it was issued to: hex
// HMAC-SHA256 under MCP_TOKEN_SECRET of the nonce, the user and the session
// epoch. Python kept the token in its signed server-side session; a plain
// double-submit cookie could be planted by a sibling subdomain (same site,
// so the Lax session cookie still rides along), so the token proves it was
// minted by this server for this session instead.
func (t *Tokens) csrfMAC(nonce string, p platform.Principal) string {
	m := hmac.New(sha256.New, t.secret)
	_, _ = fmt.Fprintf(m, "oauth-csrf\x00%s\x00%s\x00%d", nonce, p.UserID, p.Epoch)
	return hex.EncodeToString(m.Sum(nil))
}

// validCSRF reports whether the cookie and the form carry the same token and
// that token was minted for this session (constant-time compares).
func (t *Tokens) validCSRF(cookie, posted string, p platform.Principal) bool {
	if cookie == "" || posted == "" || subtle.ConstantTimeCompare([]byte(cookie), []byte(posted)) != 1 {
		return false
	}
	nonce, mac, ok := strings.Cut(posted, ".")
	if !ok || nonce == "" {
		return false
	}
	return hmac.Equal([]byte(mac), []byte(t.csrfMAC(nonce, p)))
}

// sameOrigin refuses a consent POST another origin sent. A browser sends
// Origin on a form POST: it must name this host (the scheme is not compared:
// behind the TLS-terminating proxy the API sees plain http). Without Origin,
// Sec-Fetch-Site must be same-origin when present; with neither, the bound
// CSRF token is the only check.
func sameOrigin(r *http.Request) bool {
	if o := r.Header.Get("Origin"); o != "" {
		u, err := url.Parse(o)
		return err == nil && u.Host != "" && strings.EqualFold(u.Host, r.Host)
	}
	if s := r.Header.Get("Sec-Fetch-Site"); s != "" {
		return s == "same-origin"
	}
	return true
}

func (h handler) csrfCookie(maxAge time.Duration) auth.CookieSpec {
	return auth.CookieSpec{Name: csrfCookie, Path: csrfPath, Secure: h.sessions.Cookie.Secure, MaxAge: maxAge}
}

// readForm parses a POSTed form body (never the query string) and checks the
// required fields are present. A bad body or a missing field is answered
// 400 invalid_request and ok is false.
func readForm(w http.ResponseWriter, r *http.Request, required ...string) (url.Values, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxFormBody)
	if err := r.ParseForm(); err != nil {
		oauthError(w, http.StatusBadRequest, "invalid_request", "request body must be a form")
		return nil, false
	}
	form := r.PostForm
	for _, f := range required {
		if _, ok := last(form, f); !ok {
			// Go change 9: Python answered FastAPI's 422.
			oauthError(w, http.StatusBadRequest, "invalid_request", "missing form field: "+f)
			return nil, false
		}
	}
	return form, true
}

// consentFields are the form fields the consent POST requires (FastAPI's
// Form(...)); the others are optional.
var consentFields = []string{"response_type", "client_id", "redirect_uri", "code_challenge", "decision"}

// consent is POST /oauth/authorize/consent, the operator's answer.
func (h handler) consent(w http.ResponseWriter, r *http.Request) {
	// The CSRF cookie is single use: every POST clears it, whatever the
	// outcome, so a refused or replayed form cannot be posted again.
	cookie, _ := r.Cookie(csrfCookie)
	auth.ClearCookie(w, h.csrfCookie(0))

	form, ok := readForm(w, r, consentFields...)
	if !ok {
		return
	}

	user, _, admin := h.adminSession(r)
	if !admin {
		oauthError(w, http.StatusForbidden, "access_denied", "admin session required")
		return
	}

	posted, _ := last(form, "csrf_token")
	if !sameOrigin(r) || cookie == nil || !h.tokens.validCSRF(cookie.Value, posted, user) {
		oauthError(w, http.StatusBadRequest, "invalid_request", "invalid or missing CSRF token")
		return
	}

	// Re-validate the posted request (client, redirect URI, PKCE) before
	// answering it: never trust the form alone, and never redirect, even a
	// denial, to a URI that is not registered.
	p := readParams(form)
	if _, ok := h.validate(w, r, p); !ok {
		return
	}

	q := url.Values{}
	if decision, _ := last(form, "decision"); decision != "approve" {
		// Go change 6: RFC 6749 §4.1.2.1 sends the denial to the client
		// rather than Python's JSON 400, which left the connector waiting.
		q.Set("error", "access_denied")
	} else {
		q.Set("code", h.tokens.MintCode(CodeClaims{
			UserID: user.UserID, ClientID: p.clientID, RedirectURI: p.redirectURI, CodeChallenge: p.codeChallenge,
		}, h.clock.Now()))
	}
	if p.state != "" {
		q.Set("state", p.state)
	}
	sep := "?"
	if strings.Contains(p.redirectURI, "?") {
		sep = "&"
	}
	// Encode sorts the keys: code and error both precede state, Python's order.
	w.Header().Set("Location", p.redirectURI+sep+q.Encode())
	w.WriteHeader(http.StatusFound)
}

// pyEscape is Python's html.escape(s, quote=True).
var pyEscape = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#x27;")

func hidden(b *strings.Builder, name string, value *string) {
	if value == nil {
		return
	}
	b.WriteString(`<input type="hidden" name="` + pyEscape.Replace(name) + `" value="` + pyEscape.Replace(*value) + `">`)
}

// consentPage is Python's _render_consent_page: hidden inputs in its order,
// each with name before value (clients find the token by that order).
func consentPage(label, token string, p authorizeParams, state *string) string {
	code := "code"
	var b strings.Builder
	hidden(&b, "response_type", &code)
	hidden(&b, "client_id", &p.clientID)
	hidden(&b, "redirect_uri", &p.redirectURI)
	hidden(&b, "code_challenge", &p.codeChallenge)
	hidden(&b, "code_challenge_method", &p.codeChallengeMethod)
	hidden(&b, "state", state)
	hidden(&b, "csrf_token", &token)
	// One pass, so a client name containing a slot's text is not expanded.
	return strings.NewReplacer("{{CLIENT_LABEL}}", pyEscape.Replace(label), "{{HIDDEN_FIELDS}}", b.String()).
		Replace(strings.TrimSuffix(consentTemplate, "\n"))
}
