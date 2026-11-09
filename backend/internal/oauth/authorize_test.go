package oauth_test

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LOQATO/gokit/db"
	"github.com/LOQATO/gokit/testkit"

	"github.com/wenhaokho/wenhao-billing/backend/internal/oauth"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

const (
	appBase     = "https://app.example.com"
	redirectURI = "http://localhost/cb"
	verifier    = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	csrfCookie  = "oauth_csrf"
)

func challengeFor(v string) string {
	sum := sha256.Sum256([]byte(v))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

type fixedClock time.Time

func (c fixedClock) Now() time.Time { return time.Time(c) }

// authApp is the OAuth routes on a fresh database with sessions and tokens on
// one fixed clock.
type authApp struct {
	h        http.Handler
	pool     *pgxpool.Pool
	sessions platform.Sessions
	tokens   *oauth.Tokens
	now      time.Time
}

func newAuthApp(t *testing.T, environment string) authApp {
	t.Helper()
	pool, err := db.Open(t.Context(), db.PoolConfig{URL: testkit.PostgresURL(t, migrations.FS)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	now := time.Date(2026, 11, 10, 9, 0, 0, 0, time.UTC)
	sessions := platform.NewSessions(platform.Config{
		Environment: environment, SessionSecret: "test-session-secret-at-least-32-bytes",
		SessionMaxAge: 8 * time.Hour, SessionRememberMaxAge: 30 * 24 * time.Hour,
	})
	sessions.Clock = fixedClock(now)
	tokens := oauth.NewTokens("test-mcp-token-secret", time.Hour, 30*24*time.Hour)
	r := chi.NewMux()
	oauth.Mount(r, oauth.Deps{
		Pool: pool, PublicBaseURL: publicBase, AppBaseURL: appBase + "/",
		Sessions: sessions, Tokens: tokens, Clock: fixedClock(now),
	})
	return authApp{h: r, pool: pool, sessions: sessions, tokens: tokens, now: now}
}

// user inserts a user with this role and returns a session cookie for it.
func (a authApp) user(t *testing.T, role string) (uuid.UUID, *http.Cookie) {
	t.Helper()
	id := uuid.New()
	if _, err := a.pool.Exec(t.Context(),
		`INSERT INTO users (user_id, email, password_hash, role) VALUES ($1, $2, 'x', $3)`,
		id, id.String()+"@example.com", role); err != nil {
		t.Fatal(err)
	}
	tok, _, err := a.sessions.Issue(id, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	return id, &http.Cookie{Name: platform.SessionCookie, Value: tok}
}

// register registers a client named name with these redirect URIs.
func (a authApp) register(t *testing.T, name string, uris ...string) string {
	t.Helper()
	if len(uris) == 0 {
		uris = []string{redirectURI}
	}
	var b strings.Builder
	b.WriteString(`{"client_name":"` + name + `","redirect_uris":[`)
	for i, u := range uris {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`"` + u + `"`)
	}
	b.WriteString("]}")
	m := decode(t, do(a.h, http.MethodPost, "/oauth/register", b.String()), http.StatusCreated)
	return m["client_id"].(string)
}

func authorizeParams(cid string, state string) url.Values {
	v := url.Values{
		"response_type":         {"code"},
		"client_id":             {cid},
		"redirect_uri":          {redirectURI},
		"code_challenge":        {challengeFor(verifier)},
		"code_challenge_method": {"S256"},
	}
	if state != "" {
		v.Set("state", state)
	}
	return v
}

func (a authApp) get(query string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/oauth/authorize?"+query, nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	a.h.ServeHTTP(rec, req)
	return rec
}

func (a authApp) post(form url.Values, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	return a.postWith(form, nil, cookies...)
}

// postWith posts the consent form with extra request headers.
func (a authApp) postWith(form url.Values, headers map[string]string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/oauth/authorize/consent", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	a.h.ServeHTTP(rec, req)
	return rec
}

func consentForm(cid, csrf, state, decision string) url.Values {
	v := authorizeParams(cid, state)
	v.Set("csrf_token", csrf)
	v.Set("decision", decision)
	return v
}

var csrfInPage = regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)

// csrfShape is a bound CSRF token: token_urlsafe(32) nonce, a dot, hex HMAC-SHA256.
var csrfShape = regexp.MustCompile(`^[A-Za-z0-9_-]{43}\.[0-9a-f]{64}$`)

// consentPage GETs the consent page as the session's user and returns the
// form's CSRF token and the oauth_csrf cookie.
func (a authApp) consentPage(t *testing.T, session *http.Cookie, query string) (string, *http.Cookie) {
	t.Helper()
	rec := a.get(query, session)
	if rec.Code != http.StatusOK {
		t.Fatalf("authorize GET = %d, want 200; body %s", rec.Code, rec.Body)
	}
	m := csrfInPage.FindStringSubmatch(rec.Body.String())
	if m == nil {
		t.Fatalf("no csrf_token in consent page: %s", rec.Body)
	}
	return m[1], responseCookie(t, rec, csrfCookie)
}

func responseCookie(t *testing.T, rec *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no %s cookie in %v", name, rec.Header().Values("Set-Cookie"))
	return nil
}

func noLocation(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Fatalf("Location = %q, want none", loc)
	}
}

// test_authorize_redirects_to_login_when_anonymous
func TestAuthorizeRedirectsToLoginWhenAnonymous(t *testing.T) {
	t.Parallel()
	a := newAuthApp(t, "test")
	cid := a.register(t, "cli")
	rec := a.get(authorizeParams(cid, "").Encode())
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302; body %s", rec.Code, rec.Body)
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, appBase+"/login?") || strings.Contains(loc, "code=") {
		t.Fatalf("Location = %q, want the frontend login and no code", loc)
	}
}

// Go change 3: return_to is the authorize path and its query, never an
// absolute URL, so the SPA's same-origin check accepts it behind the proxy.
func TestAuthorizeReturnToIsPath(t *testing.T) {
	t.Parallel()
	a := newAuthApp(t, "test")
	cid := a.register(t, "cli")
	query := authorizeParams(cid, "a b&c").Encode() + "&scope=mcp"
	rec := a.get(query)
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302; body %s", rec.Code, rec.Body)
	}
	want := appBase + "/login?" + url.Values{"return_to": {"/oauth/authorize?" + query}}.Encode()
	if got := rec.Header().Get("Location"); got != want {
		t.Fatalf("Location = %q,\nwant %q", got, want)
	}
}

// Go change 2: an expired session, or one from an older session epoch,
// bounces to the login like no session at all.
func TestAuthorizeExpiredSessionBounces(t *testing.T) {
	t.Parallel()
	a := newAuthApp(t, "test")
	cid := a.register(t, "cli")
	id, _ := a.user(t, "admin")

	if _, err := a.pool.Exec(t.Context(), `UPDATE users SET session_epoch = 1 WHERE user_id = $1`, id); err != nil {
		t.Fatal(err)
	}
	old := a.sessions
	old.Clock = fixedClock(a.now.Add(-9 * time.Hour)) // an 8-hour session signed 9 hours ago
	expired, _, err := old.Issue(id, 1, false)        // at the current epoch: only its age refuses it
	if err != nil {
		t.Fatal(err)
	}
	stale, _, err := a.sessions.Issue(id, 0, false) // live, but signed out since
	if err != nil {
		t.Fatal(err)
	}
	// The same user at the current epoch and time is let in.
	current, _, err := a.sessions.Issue(id, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	if rec := a.get(authorizeParams(cid, "").Encode(), &http.Cookie{Name: platform.SessionCookie, Value: current}); rec.Code != http.StatusOK {
		t.Fatalf("current session: status = %d, want 200; body %s", rec.Code, rec.Body)
	}
	for name, tok := range map[string]string{"expired": expired, "older epoch": stale} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cookie := &http.Cookie{Name: platform.SessionCookie, Value: tok}
			rec := a.get(authorizeParams(cid, "").Encode(), cookie)
			if rec.Code != http.StatusFound || !strings.HasPrefix(rec.Header().Get("Location"), appBase+"/login?return_to=") {
				t.Fatalf("status = %d, Location %q; want 302 to the login; body %s", rec.Code, rec.Header().Get("Location"), rec.Body)
			}
			// The consent POST refuses it too.
			wantOAuthError(t, a.post(consentForm(cid, "x", "", "approve"), cookie, &http.Cookie{Name: csrfCookie, Value: "x"}),
				http.StatusForbidden, "access_denied", "admin session required")
		})
	}
}

// test_authorize_get_renders_consent_and_mints_no_code, and the page is
// Python's, byte for byte.
func TestAuthorizeGetRendersConsentAndMintsNoCode(t *testing.T) {
	t.Parallel()
	a := newAuthApp(t, "test")
	cid := a.register(t, `cli <&> \"q\" 'a'`)
	_, session := a.user(t, "admin")
	rec := a.get(authorizeParams(cid, `x"y<z`).Encode(), session)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body)
	}
	noLocation(t, rec)
	if ct := rec.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}
	body := rec.Body.String()
	if strings.Contains(body, "code=") {
		t.Fatal("the GET minted a code")
	}
	m := csrfInPage.FindStringSubmatch(body)
	if m == nil || !csrfShape.MatchString(m[1]) {
		t.Fatalf("csrf token %v, want a 43-character nonce and its hex HMAC", m)
	}
	golden, err := os.ReadFile("testdata/consent_page.golden.html")
	if err != nil {
		t.Fatal(err)
	}
	want := strings.NewReplacer("CLIENT_ID", cid, "CHALLENGE", challengeFor(verifier), "CSRF_TOKEN", m[1]).Replace(strings.TrimSuffix(string(golden), "\n"))
	if body != want {
		t.Fatalf("consent page differs from Python's.\ngot:\n%s\nwant:\n%s", body, want)
	}
}

// Without state the hidden input is left out, and the client id labels a
// client registered without a name.
func TestConsentPageOmitsStateAndFallsBackToClientID(t *testing.T) {
	t.Parallel()
	a := newAuthApp(t, "test")
	m := decode(t, do(a.h, http.MethodPost, "/oauth/register", `{"redirect_uris":["`+redirectURI+`"]}`), http.StatusCreated)
	cid := m["client_id"].(string)
	_, session := a.user(t, "admin")
	body := a.get(authorizeParams(cid, "").Encode(), session).Body.String()
	if strings.Contains(body, `name="state"`) {
		t.Error("state input rendered without a state")
	}
	if !strings.Contains(body, `<span class="client">`+cid+`</span>`) {
		t.Error("client id is not the label")
	}
	// Hidden inputs in Python's order.
	order := []string{"response_type", "client_id", "redirect_uri", "code_challenge", "code_challenge_method", "csrf_token"}
	last := -1
	for _, n := range order {
		i := strings.Index(body, `name="`+n+`" value=`)
		if i <= last {
			t.Fatalf("hidden input %s out of order", n)
		}
		last = i
	}
}

// test_consent_post_without_csrf_mints_nothing
func TestConsentPostWithoutCsrfMintsNothing(t *testing.T) {
	t.Parallel()
	a := newAuthApp(t, "test")
	cid := a.register(t, "cli")
	_, session := a.user(t, "admin")
	token, cookie := a.consentPage(t, session, authorizeParams(cid, "").Encode())
	for name, c := range map[string]struct {
		form    string
		cookies []*http.Cookie
	}{
		"empty form token": {"", []*http.Cookie{session, cookie}},
		"no cookie":        {token, []*http.Cookie{session}},
		"empty cookie":     {token, []*http.Cookie{session, {Name: csrfCookie, Value: ""}}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			rec := a.post(consentForm(cid, c.form, "", "approve"), c.cookies...)
			wantOAuthError(t, rec, http.StatusBadRequest, "invalid_request", "invalid or missing CSRF token")
			noLocation(t, rec)
		})
	}
}

// test_consent_post_with_wrong_csrf_mints_nothing
func TestConsentPostWithWrongCsrfMintsNothing(t *testing.T) {
	t.Parallel()
	a := newAuthApp(t, "test")
	cid := a.register(t, "cli")
	_, session := a.user(t, "admin")
	_, cookie := a.consentPage(t, session, authorizeParams(cid, "").Encode())
	rec := a.post(consentForm(cid, "not-the-real-token", "", "approve"), session, cookie)
	wantOAuthError(t, rec, http.StatusBadRequest, "invalid_request", "invalid or missing CSRF token")
	noLocation(t, rec)
}

// test_consent_post_deny_mints_nothing; Go change 6: a denial goes back to
// the client with error=access_denied and the state.
func TestDenyRedirectsWithAccessDenied(t *testing.T) {
	t.Parallel()
	a := newAuthApp(t, "test")
	cid := a.register(t, "cli")
	_, session := a.user(t, "admin")
	token, cookie := a.consentPage(t, session, authorizeParams(cid, "xyz").Encode())
	rec := a.post(consentForm(cid, token, "xyz", "deny"), session, cookie)
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302; body %s", rec.Code, rec.Body)
	}
	if got, want := rec.Header().Get("Location"), redirectURI+"?error=access_denied&state=xyz"; got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}
}

// A denial is only sent to a validated redirect URI: an unregistered one is
// refused in JSON, never redirected to.
func TestDenyToUnregisteredRedirectIsRefused(t *testing.T) {
	t.Parallel()
	a := newAuthApp(t, "test")
	cid := a.register(t, "cli")
	_, session := a.user(t, "admin")
	token, cookie := a.consentPage(t, session, authorizeParams(cid, "").Encode())
	form := consentForm(cid, token, "s", "deny")
	form.Set("redirect_uri", "http://attacker.example/steal")
	rec := a.post(form, session, cookie)
	wantOAuthError(t, rec, http.StatusBadRequest, "invalid_request", "redirect_uri is not registered for this client")
	noLocation(t, rec)
}

// test_full_consent_flow_mints_code: the code binds the user, client,
// redirect URI and challenge; state comes back.
func TestFullConsentFlowMintsCode(t *testing.T) {
	t.Parallel()
	a := newAuthApp(t, "test")
	withQuery := redirectURI + "?k=v"
	cid := a.register(t, "cli", redirectURI, withQuery)
	id, session := a.user(t, "admin")
	for _, ru := range []string{redirectURI, withQuery} {
		t.Run(ru, func(t *testing.T) {
			t.Parallel()
			q := authorizeParams(cid, "xyz")
			q.Set("redirect_uri", ru)
			token, cookie := a.consentPage(t, session, q.Encode())
			form := consentForm(cid, token, "xyz", "approve")
			form.Set("redirect_uri", ru)
			rec := a.post(form, session, cookie)
			if rec.Code != http.StatusFound {
				t.Fatalf("status = %d, want 302; body %s", rec.Code, rec.Body)
			}
			loc := rec.Header().Get("Location")
			if !strings.HasPrefix(loc, ru+map[bool]string{true: "&", false: "?"}[strings.Contains(ru, "?")]+"code=") {
				t.Fatalf("Location = %q, want %s with the code appended", loc, ru)
			}
			u, err := url.Parse(loc)
			if err != nil {
				t.Fatal(err)
			}
			if s := u.Query().Get("state"); s != "xyz" {
				t.Errorf("state = %q, want xyz", s)
			}
			claims, err := a.tokens.VerifyCode(u.Query().Get("code"), a.now)
			if err != nil {
				t.Fatal(err)
			}
			want := oauth.CodeClaims{UserID: id, ClientID: cid, RedirectURI: ru, CodeChallenge: challengeFor(verifier)}
			if claims != want {
				t.Fatalf("code claims = %+v, want %+v", claims, want)
			}
		})
	}
}

// An empty state is dropped from the redirect, as in Python.
func TestConsentEmptyStateIsDropped(t *testing.T) {
	t.Parallel()
	a := newAuthApp(t, "test")
	cid := a.register(t, "cli")
	_, session := a.user(t, "admin")
	token, cookie := a.consentPage(t, session, authorizeParams(cid, "").Encode())
	rec := a.post(consentForm(cid, token, "", "approve"), session, cookie)
	if loc := rec.Header().Get("Location"); rec.Code != http.StatusFound || strings.Contains(loc, "state") {
		t.Fatalf("status %d Location %q, want 302 with no state", rec.Code, loc)
	}
}

// test_authorize_refuses_non_admin_session; the consent POST refuses it too.
func TestAuthorizeRefusesNonAdminSession(t *testing.T) {
	t.Parallel()
	a := newAuthApp(t, "test")
	cid := a.register(t, "cli")
	_, staff := a.user(t, "staff")
	rec := a.get(authorizeParams(cid, "").Encode(), staff)
	wantOAuthError(t, rec, http.StatusForbidden, "access_denied", "admin role required")
	noLocation(t, rec)

	// Even with a CSRF pair of its own making, a staff session mints nothing.
	rec = a.post(consentForm(cid, "t", "", "approve"), staff, &http.Cookie{Name: csrfCookie, Value: "t"})
	wantOAuthError(t, rec, http.StatusForbidden, "access_denied", "admin session required")
	noLocation(t, rec)
}

// A session whose user was deleted is refused like a non-admin, as in Python.
func TestAuthorizeRefusesDeletedSessionUser(t *testing.T) {
	t.Parallel()
	a := newAuthApp(t, "test")
	cid := a.register(t, "cli")
	id, session := a.user(t, "admin")
	if _, err := a.pool.Exec(t.Context(), `DELETE FROM users WHERE user_id = $1`, id); err != nil {
		t.Fatal(err)
	}
	wantOAuthError(t, a.get(authorizeParams(cid, "").Encode(), session), http.StatusForbidden, "access_denied", "admin role required")
}

// test_authorize_rejects_unknown_client
func TestAuthorizeRejectsUnknownClient(t *testing.T) {
	t.Parallel()
	a := newAuthApp(t, "test")
	_, session := a.user(t, "admin")
	rec := a.get(authorizeParams("definitely-not-registered", "").Encode(), session)
	wantOAuthError(t, rec, http.StatusUnauthorized, "invalid_client", "unknown client_id")
	noLocation(t, rec)
}

// test_authorize_rejects_unregistered_redirect_uri, at authorize and at
// consent; a registered prefix is not a match.
func TestAuthorizeRejectsUnregisteredRedirectUri(t *testing.T) {
	t.Parallel()
	a := newAuthApp(t, "test")
	cid := a.register(t, "cli")
	_, session := a.user(t, "admin")
	for _, bad := range []string{"http://attacker.example/steal", redirectURI + "/more", redirectURI + "?x=1", "http://localhost/c"} {
		t.Run(bad, func(t *testing.T) {
			t.Parallel()
			q := authorizeParams(cid, "")
			q.Set("redirect_uri", bad)
			for _, rec := range []*httptest.ResponseRecorder{a.get(q.Encode(), session), a.get(q.Encode())} {
				wantOAuthError(t, rec, http.StatusBadRequest, "invalid_request", "redirect_uri is not registered for this client")
				noLocation(t, rec)
			}
			token, cookie := a.consentPage(t, session, authorizeParams(cid, "").Encode())
			form := consentForm(cid, token, "", "approve")
			form.Set("redirect_uri", bad)
			rec := a.post(form, session, cookie)
			wantOAuthError(t, rec, http.StatusBadRequest, "invalid_request", "redirect_uri is not registered for this client")
			noLocation(t, rec)
		})
	}
}

// The checks run in Python's order, before the session is looked at: an
// anonymous browser gets the error, not the login.
func TestAuthorizeValidationErrorsInOrder(t *testing.T) {
	t.Parallel()
	a := newAuthApp(t, "test")
	cid := a.register(t, "cli")
	_, session := a.user(t, "admin")
	cases := []struct {
		name         string
		edit         func(url.Values)
		status       int
		code, desc   string
		consentCheck bool
	}{
		{
			"response_type", func(v url.Values) { v.Set("response_type", "token"); v.Del("client_id"); v.Del("code_challenge") },
			400, "unsupported_response_type", "response_type must be 'code'", true,
		},
		{
			"no response_type", func(v url.Values) { v.Del("response_type") },
			400, "unsupported_response_type", "response_type must be 'code'", false,
		},
		{
			"missing client_id", func(v url.Values) { v.Del("client_id"); v.Del("code_challenge") },
			400, "invalid_request", "client_id and redirect_uri are required", true,
		},
		{
			"missing redirect_uri", func(v url.Values) { v.Set("redirect_uri", ""); v.Set("code_challenge_method", "plain") },
			400, "invalid_request", "client_id and redirect_uri are required", true,
		},
		{
			"missing challenge", func(v url.Values) { v.Del("code_challenge"); v.Set("code_challenge_method", "plain") },
			400, "invalid_request", "code_challenge is required (PKCE)", true,
		},
		{
			"plain", func(v url.Values) { v.Set("code_challenge_method", "plain"); v.Set("client_id", "unknown") },
			400, "invalid_request", "code_challenge_method must be 'S256'", true,
		},
		{
			"unknown client", func(v url.Values) { v.Set("client_id", "unknown"); v.Set("redirect_uri", "http://x.example/") },
			401, "invalid_client", "unknown client_id", true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			q := authorizeParams(cid, "")
			c.edit(q)
			for _, rec := range []*httptest.ResponseRecorder{a.get(q.Encode()), a.get(q.Encode(), session)} {
				wantOAuthError(t, rec, c.status, c.code, c.desc)
				noLocation(t, rec)
			}
			if !c.consentCheck {
				return
			}
			token, cookie := a.consentPage(t, session, authorizeParams(cid, "").Encode())
			form := consentForm(cid, token, "", "approve")
			for k := range form {
				if k != "csrf_token" && k != "decision" {
					form.Del(k)
				}
			}
			for k, v := range q {
				form[k] = v
			}
			// The consent form requires the fields to be present; an empty
			// value reaches the shared validation.
			for _, k := range []string{"response_type", "client_id", "redirect_uri", "code_challenge"} {
				if !form.Has(k) {
					form.Set(k, "")
				}
			}
			rec := a.post(form, session, cookie)
			wantOAuthError(t, rec, c.status, c.code, c.desc)
			noLocation(t, rec)
		})
	}
	t.Run("method defaults to S256", func(t *testing.T) {
		t.Parallel()
		q := authorizeParams(cid, "")
		q.Del("code_challenge_method")
		if rec := a.get(q.Encode(), session); rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body)
		}
	})
}

// Go change 9: a consent POST missing a required field is an OAuth 400, not
// FastAPI's 422.
func TestConsentMissingFieldIsOauth400(t *testing.T) {
	t.Parallel()
	a := newAuthApp(t, "test")
	cid := a.register(t, "cli")
	_, session := a.user(t, "admin")
	for _, field := range []string{"response_type", "client_id", "redirect_uri", "code_challenge", "decision"} {
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			token, cookie := a.consentPage(t, session, authorizeParams(cid, "").Encode())
			form := consentForm(cid, token, "", "approve")
			form.Del(field)
			rec := a.post(form, session, cookie)
			wantOAuthError(t, rec, http.StatusBadRequest, "invalid_request", "missing form field: "+field)
			noLocation(t, rec)
		})
	}
}

// The CSRF cookie is HttpOnly, SameSite=Lax, 10 minutes, Secure with the
// session cookie, and every consent POST clears it, so a token works once.
func TestCsrfCookieSingleUse(t *testing.T) {
	t.Parallel()
	for _, env := range []string{"test", "production"} {
		t.Run(env, func(t *testing.T) {
			t.Parallel()
			a := newAuthApp(t, env)
			cid := a.register(t, "cli")
			_, session := a.user(t, "admin")
			token, cookie := a.consentPage(t, session, authorizeParams(cid, "").Encode())
			if cookie.Value != token || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode ||
				cookie.MaxAge != 600 || cookie.Path != "/oauth/" || cookie.Secure != (env == "production") {
				t.Fatalf("csrf cookie = %+v (%s)", cookie, cookie.String())
			}

			// A refused POST (wrong token) still burns the cookie.
			rec := a.post(consentForm(cid, "wrong", "", "approve"), session, cookie)
			wantOAuthError(t, rec, http.StatusBadRequest, "invalid_request", "invalid or missing CSRF token")
			wantCleared(t, rec)

			// A good POST clears it too; the browser then has no cookie and
			// the same form cannot be posted again.
			token, cookie = a.consentPage(t, session, authorizeParams(cid, "").Encode())
			rec = a.post(consentForm(cid, token, "", "approve"), session, cookie)
			if rec.Code != http.StatusFound {
				t.Fatalf("approve = %d; body %s", rec.Code, rec.Body)
			}
			wantCleared(t, rec)
			rec = a.post(consentForm(cid, token, "", "approve"), session)
			wantOAuthError(t, rec, http.StatusBadRequest, "invalid_request", "invalid or missing CSRF token")

			// A new GET replaces the token: the old one no longer matches.
			_, newer := a.consentPage(t, session, authorizeParams(cid, "").Encode())
			rec = a.post(consentForm(cid, token, "", "approve"), session, newer)
			wantOAuthError(t, rec, http.StatusBadRequest, "invalid_request", "invalid or missing CSRF token")
		})
	}
}

func wantCleared(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	c := responseCookie(t, rec, csrfCookie)
	if c.Value != "" || c.MaxAge >= 0 || c.Path != "/oauth/" {
		t.Fatalf("csrf cookie not cleared: %s", c.String())
	}
}

// The CSRF token is bound to the session it was issued to: a cookie and form
// pair the server did not mint for this user and epoch is refused, so a
// sibling subdomain that plants oauth_csrf cannot approve its own client.
func TestConsentCsrfBoundToSession(t *testing.T) {
	t.Parallel()
	a := newAuthApp(t, "test")
	cid := a.register(t, "cli")
	_, alice := a.user(t, "admin")
	_, bob := a.user(t, "admin")
	bobToken, bobCookie := a.consentPage(t, bob, authorizeParams(cid, "").Encode())
	forged := strings.Repeat("A", 43) + "." + strings.Repeat("0", 64)
	cases := map[string]struct{ token, cookie string }{
		"forged pair":          {forged, forged},
		"bare nonce pair":      {strings.Repeat("A", 43), strings.Repeat("A", 43)},
		"another user's token": {bobToken, bobCookie.Value},
		"nonce swapped":        {"B" + bobToken[1:], "B" + bobToken[1:]},
		"mac of another nonce": {strings.Repeat("A", 43) + bobToken[43:], strings.Repeat("A", 43) + bobToken[43:]},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			rec := a.post(consentForm(cid, c.token, "", "approve"), alice, &http.Cookie{Name: csrfCookie, Value: c.cookie})
			wantOAuthError(t, rec, http.StatusBadRequest, "invalid_request", "invalid or missing CSRF token")
			noLocation(t, rec)
		})
	}
	t.Run("own token", func(t *testing.T) {
		t.Parallel()
		token, cookie := a.consentPage(t, alice, authorizeParams(cid, "").Encode())
		if rec := a.post(consentForm(cid, token, "", "approve"), alice, cookie); rec.Code != http.StatusFound {
			t.Fatalf("status = %d, want 302; body %s", rec.Code, rec.Body)
		}
	})
}

// A consent POST sent by another origin is refused even with a good token;
// Origin is checked when present, else Sec-Fetch-Site.
func TestConsentRefusesCrossOrigin(t *testing.T) {
	t.Parallel()
	a := newAuthApp(t, "test")
	cid := a.register(t, "cli")
	_, session := a.user(t, "admin")
	token, cookie := a.consentPage(t, session, authorizeParams(cid, "").Encode())
	// httptest requests are for Host example.com.
	for name, c := range map[string]struct {
		headers map[string]string
		ok      bool
	}{
		"cross origin":           {map[string]string{"Origin": "https://evil.example.com"}, false},
		"sibling subdomain":      {map[string]string{"Origin": "https://sub.example.com"}, false},
		"null origin":            {map[string]string{"Origin": "null"}, false},
		"origin wins over fetch": {map[string]string{"Origin": "https://evil.example.com", "Sec-Fetch-Site": "same-origin"}, false},
		"fetch cross-site":       {map[string]string{"Sec-Fetch-Site": "cross-site"}, false},
		"fetch same-site":        {map[string]string{"Sec-Fetch-Site": "same-site"}, false},
		"same origin":            {map[string]string{"Origin": "http://example.com"}, true},
		"same host behind tls":   {map[string]string{"Origin": "https://example.com"}, true},
		"fetch same-origin":      {map[string]string{"Sec-Fetch-Site": "same-origin"}, true},
		"neither header":         {nil, true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			rec := a.postWith(consentForm(cid, token, "", "approve"), c.headers, session, cookie)
			if c.ok {
				if rec.Code != http.StatusFound {
					t.Fatalf("status = %d, want 302; body %s", rec.Code, rec.Body)
				}
				return
			}
			wantOAuthError(t, rec, http.StatusBadRequest, "invalid_request", "invalid or missing CSRF token")
			noLocation(t, rec)
		})
	}
}
