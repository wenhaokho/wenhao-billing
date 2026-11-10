package oauth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/wenhaokho/wenhao-billing/backend/internal/oauth"
)

// appSecret is the MCP_TOKEN_SECRET newAuthApp signs with.
const appSecret = "test-mcp-token-secret"

// postForm posts a form to path, as an OAuth client does.
func (a authApp) postForm(path string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	a.h.ServeHTTP(rec, req)
	return rec
}

func (a authApp) token(form url.Values) *httptest.ResponseRecorder {
	return a.postForm("/oauth/token", form)
}

func (a authApp) revoke(token string) *httptest.ResponseRecorder {
	return a.postForm("/oauth/revoke", url.Values{"token": {token}})
}

// flowCode is Python's _authorize_and_get_code: the consent page, the CSRF
// token, the approval, and the code from the redirect.
func (a authApp) flowCode(t *testing.T, session *http.Cookie, cid string) string {
	t.Helper()
	csrf, cookie := a.consentPage(t, session, authorizeParams(cid, "xyz").Encode())
	rec := a.post(consentForm(cid, csrf, "xyz", "approve"), session, cookie)
	if rec.Code != http.StatusFound {
		t.Fatalf("consent = %d, want 302; body %s", rec.Code, rec.Body)
	}
	u, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if s := u.Query().Get("state"); s != "xyz" {
		t.Fatalf("state = %q, want xyz", s)
	}
	return u.Query().Get("code")
}

// mintCode is a code for user and client bound to redirectURI and the
// challenge of v, issued at the app's clock.
func (a authApp) mintCode(user uuid.UUID, cid, v string) string {
	return a.tokens.MintCode(oauth.CodeClaims{
		UserID: user, ClientID: cid, RedirectURI: redirectURI, CodeChallenge: challengeFor(v),
	}, a.now)
}

func codeForm(code, cid, v string) url.Values {
	return url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {cid},
		"code_verifier": {v},
	}
}

func refreshForm(refresh, cid string) url.Values {
	return url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {cid}}
}

// pairShape is _token_response's body, keys in Python's order.
var pairShape = regexp.MustCompile(`^\{"access_token":"([^"]+)","token_type":"Bearer","expires_in":3600,"refresh_token":"([^"]+)"\}\n$`)

// wantPair fails unless rec is a 200 token pair for user and returns the
// access and refresh tokens.
func (a authApp) wantPair(t *testing.T, rec *httptest.ResponseRecorder, user uuid.UUID) (access, refresh string) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body)
	}
	m := pairShape.FindStringSubmatch(rec.Body.String())
	if m == nil {
		t.Fatalf("body = %s, want the token pair shape", rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q", ct)
	}
	sub, err := a.tokens.VerifyAccess(m[1], a.now)
	if err != nil || sub != user {
		t.Fatalf("access token sub = %v, %v; want %v", sub, err, user)
	}
	sub, _, err = a.tokens.VerifyRefresh(m[2], a.now)
	if err != nil || sub != user {
		t.Fatalf("refresh token sub = %v, %v; want %v", sub, err, user)
	}
	return m[1], m[2]
}

func wantEmptyObject(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusOK || rec.Body.String() != "{}\n" {
		t.Fatalf("revoke = %d %q, want 200 {}", rec.Code, rec.Body)
	}
}

func (a authApp) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := a.pool.QueryRow(t.Context(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// test_register_authorize_token_refresh_revoke
func TestRegisterAuthorizeTokenRefreshRevoke(t *testing.T) {
	t.Parallel()
	a := newAuthApp(t, "test")
	cid := a.register(t, "cli")
	id, session := a.user(t, "admin")
	code := a.flowCode(t, session, cid)

	_, refresh := a.wantPair(t, a.token(codeForm(code, cid, verifier)), id)

	// The refresh grant rotates the pair.
	_, newRefresh := a.wantPair(t, a.token(refreshForm(refresh, cid)), id)
	if newRefresh == refresh {
		t.Fatal("refresh token was not rotated")
	}

	// The rotated-out token is revoked.
	wantOAuthError(t, a.token(refreshForm(refresh, cid)), http.StatusBadRequest, "invalid_grant", "refresh token reuse detected")

	// Revoke the current token; it no longer refreshes.
	wantEmptyObject(t, a.revoke(newRefresh))
	wantOAuthError(t, a.token(refreshForm(newRefresh, cid)), http.StatusBadRequest, "invalid_grant", "refresh token reuse detected")
}

// test_token_rejects_wrong_pkce_verifier. A refused exchange does not spend
// the code: the right verifier still redeems it.
func TestTokenRejectsWrongPkceVerifier(t *testing.T) {
	t.Parallel()
	a := newAuthApp(t, "test")
	cid := a.register(t, "cli")
	id, session := a.user(t, "admin")
	code := a.flowCode(t, session, cid)
	rec := a.token(codeForm(code, cid, "wrong-verifier-value-that-does-not-match-challenge"))
	wantOAuthError(t, rec, http.StatusBadRequest, "invalid_grant", "PKCE verification failed")
	if n := a.count(t, `SELECT count(*) FROM oauth_refresh_tokens`); n != 0 {
		t.Fatalf("%d refresh tokens stored after a refused exchange", n)
	}
	a.wantPair(t, a.token(codeForm(code, cid, verifier)), id)
}

// test_token_requires_client_id
func TestTokenRequiresClientID(t *testing.T) {
	t.Parallel()
	a := newAuthApp(t, "test")
	cid := a.register(t, "cli")
	_, session := a.user(t, "admin")
	form := codeForm(a.flowCode(t, session, cid), cid, verifier)
	form.Del("client_id")
	wantOAuthError(t, a.token(form), http.StatusBadRequest, "invalid_request", "client_id is required")
	form.Set("client_id", "")
	wantOAuthError(t, a.token(form), http.StatusBadRequest, "invalid_request", "client_id is required")
}

// test_refresh_reuse_revokes_successor_family. The family is every live
// refresh token of the user (kept from Python); another user's are untouched.
func TestRefreshReuseRevokesSuccessorFamily(t *testing.T) {
	t.Parallel()
	a := newAuthApp(t, "test")
	cid := a.register(t, "cli")
	id, session := a.user(t, "admin")
	_, r1 := a.wantPair(t, a.token(codeForm(a.flowCode(t, session, cid), cid, verifier)), id)

	other := uuid.New()
	_, otherRefresh := a.wantPair(t, a.token(codeForm(a.mintCode(other, cid, verifier), cid, verifier)), other)

	_, r2 := a.wantPair(t, a.token(refreshForm(r1, cid)), id)
	wantOAuthError(t, a.token(refreshForm(r1, cid)), http.StatusBadRequest, "invalid_grant", "refresh token reuse detected")
	wantOAuthError(t, a.token(refreshForm(r2, cid)), http.StatusBadRequest, "invalid_grant", "refresh token reuse detected")
	if n := a.count(t, `SELECT count(*) FROM oauth_refresh_tokens WHERE user_id = $1 AND NOT revoked`, id.String()); n != 0 {
		t.Fatalf("%d live refresh tokens left for the user", n)
	}

	a.wantPair(t, a.token(refreshForm(otherRefresh, cid)), other)
}

// Go change 1: a code is single use. The spent row expires with the code.
func TestCodeSingleUse(t *testing.T) {
	t.Parallel()
	a := newAuthApp(t, "test")
	cid := a.register(t, "cli")
	id, session := a.user(t, "admin")
	code := a.flowCode(t, session, cid)
	a.wantPair(t, a.token(codeForm(code, cid, verifier)), id)
	wantOAuthError(t, a.token(codeForm(code, cid, verifier)), http.StatusBadRequest, "invalid_grant", "authorization code already used")

	var exp time.Time
	if err := a.pool.QueryRow(t.Context(), `SELECT expires_at FROM oauth_spent_codes`).Scan(&exp); err != nil {
		t.Fatal(err)
	}
	if want := a.now.Add(120 * time.Second); !exp.Equal(want) {
		t.Fatalf("spent code expires_at = %v, want %v", exp, want)
	}
	if n := a.count(t, `SELECT count(*) FROM oauth_refresh_tokens WHERE user_id = $1`, id.String()); n != 1 {
		t.Fatalf("%d refresh tokens stored, want 1", n)
	}
}

// respellings are the other base64url spellings of a token's last character:
// a 43-character HS256 signature leaves the last character's two low bits
// unused, so four characters decode to the same bytes.
func respellings(token string) []string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	i := strings.IndexByte(alphabet, token[len(token)-1])
	var out []string
	for low := range 4 {
		if j := i&^3 | low; j != i {
			out = append(out, token[:len(token)-1]+string(alphabet[j]))
		}
	}
	return out
}

// A code respelled in its signature's unused bits is not a new code: strict
// base64url decoding refuses it, before and after the real code is spent.
// Access and refresh tokens are refused the same way.
func TestCodeSignatureRespellingRefused(t *testing.T) {
	t.Parallel()
	a := newAuthApp(t, "test")
	cid := a.register(t, "cli")
	id := uuid.New()
	code := a.mintCode(id, cid, verifier)
	alts := respellings(code)
	if len(alts) != 3 {
		t.Fatalf("respellings = %v, want 3", alts)
	}
	const refused = "invalid authorization code: Invalid token"
	for _, alt := range alts {
		wantOAuthError(t, a.token(codeForm(alt, cid, verifier)), http.StatusBadRequest, "invalid_grant", refused)
	}
	access, refresh := a.wantPair(t, a.token(codeForm(code, cid, verifier)), id)
	for _, alt := range alts {
		wantOAuthError(t, a.token(codeForm(alt, cid, verifier)), http.StatusBadRequest, "invalid_grant", refused)
	}
	if n := a.count(t, `SELECT count(*) FROM oauth_refresh_tokens`); n != 1 {
		t.Fatalf("%d refresh tokens stored, want 1", n)
	}
	for _, alt := range respellings(access) {
		if _, err := a.tokens.VerifyAccess(alt, a.now); err == nil || err.Error() != "Invalid token" {
			t.Fatalf("respelled access token: err = %v, want Invalid token", err)
		}
	}
	for _, alt := range respellings(refresh) {
		wantOAuthError(t, a.token(refreshForm(alt, cid)), http.StatusBadRequest, "invalid_grant", "invalid refresh token: Invalid token")
	}
}

// race posts form from n goroutines released together and returns the
// responses.
func (a authApp) race(n int, path string, form url.Values) []*httptest.ResponseRecorder {
	recs := make([]*httptest.ResponseRecorder, n)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			recs[i] = a.postForm(path, form)
		}()
	}
	close(start)
	wg.Wait()
	return recs
}

// Concurrent exchanges of one code yield one pair.
func TestCodeReplayConcurrentOnce(t *testing.T) {
	t.Parallel()
	a := newAuthApp(t, "test")
	cid := a.register(t, "cli")
	id := uuid.New()
	recs := a.race(8, "/oauth/token", codeForm(a.mintCode(id, cid, verifier), cid, verifier))
	won := 0
	for _, rec := range recs {
		if rec.Code == http.StatusOK {
			won++
			a.wantPair(t, rec, id)
			continue
		}
		wantOAuthError(t, rec, http.StatusBadRequest, "invalid_grant", "authorization code already used")
	}
	if won != 1 {
		t.Fatalf("%d exchanges succeeded, want 1", won)
	}
	if n := a.count(t, `SELECT count(*) FROM oauth_refresh_tokens`); n != 1 {
		t.Fatalf("%d refresh tokens stored, want 1", n)
	}
}

// Go change 7: the verifier is 43-128 characters of [A-Za-z0-9-._~]
// (RFC 7636 §4.1), checked before the challenge is compared.
func TestVerifierCharsetAndLength(t *testing.T) {
	t.Parallel()
	a := newAuthApp(t, "test")
	cid := a.register(t, "cli")
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~"
	of := func(n int) string { return strings.Repeat(alphabet, 2)[:n] }
	cases := []struct {
		name, v string
		ok      bool
	}{
		{"42 chars", of(42), false},
		{"43 chars", of(43), true},
		{"128 chars", of(128), true},
		{"129 chars", strings.Repeat("a", 129), false},
		{"empty-looking short", "abc", false},
		{"plus", "+" + of(42), false},
		{"slash", of(42) + "/", false},
		{"space", of(21) + " " + of(21), false},
		{"equals padding", of(42) + "=", false},
		{"non-ASCII", of(42) + "é", false},
		{"every unreserved char", alphabet, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			user := uuid.New()
			code := a.mintCode(user, cid, c.v)
			rec := a.token(codeForm(code, cid, c.v))
			if c.ok {
				a.wantPair(t, rec, user)
				return
			}
			wantOAuthError(t, rec, http.StatusBadRequest, "invalid_grant", "invalid code_verifier")
		})
	}
}

// connect opens a connection to the app's database outside its pool.
func (a authApp) connect(t *testing.T) *pgx.Conn {
	t.Helper()
	c, err := pgx.Connect(t.Context(), a.pool.Config().ConnConfig.ConnString())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close(context.WithoutCancel(t.Context())) })
	return c
}

// Concurrent refreshes of one token give one new pair; every other is reuse,
// which then revokes the winner's successor too. To make the race certain,
// another transaction holds the token's row lock until every refresh is
// waiting on a lock: a refresh that read the row without locking it would
// then rotate it as well.
func TestRefreshConcurrentOnePair(t *testing.T) {
	t.Parallel()
	a := newAuthApp(t, "test")
	cid := a.register(t, "cli")
	id := uuid.New()
	_, refresh := a.wantPair(t, a.token(codeForm(a.mintCode(id, cid, verifier), cid, verifier)), id)
	_, jti, err := a.tokens.VerifyRefresh(refresh, a.now)
	if err != nil {
		t.Fatal(err)
	}

	holder, watcher := a.connect(t), a.connect(t)
	tx, err := holder.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `SELECT 1 FROM oauth_refresh_tokens WHERE jti = $1 FOR UPDATE`, jti); err != nil {
		t.Fatal(err)
	}
	n := min(8, int(a.pool.Config().MaxConns))
	done := make(chan []*httptest.ResponseRecorder)
	go func() { done <- a.race(n, "/oauth/token", refreshForm(refresh, cid)) }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := watcher.QueryRow(t.Context(),
			`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`,
		).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting >= n {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d of %d refreshes waiting on the row lock after 10s", waiting, n)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	recs := <-done
	won := 0
	for _, rec := range recs {
		if rec.Code == http.StatusOK {
			won++
			a.wantPair(t, rec, id)
			continue
		}
		wantOAuthError(t, rec, http.StatusBadRequest, "invalid_grant", "refresh token reuse detected")
	}
	if won != 1 {
		t.Fatalf("%d refreshes succeeded, want 1", won)
	}
	if n := a.count(t, `SELECT count(*) FROM oauth_refresh_tokens`); n != 2 {
		t.Fatalf("%d refresh tokens stored, want 2", n)
	}
	if n := a.count(t, `SELECT count(*) FROM oauth_refresh_tokens WHERE NOT revoked`); n != 0 {
		t.Fatalf("%d live refresh tokens left after reuse, want 0", n)
	}
}

// The refresh grant's refusals, in Python's order and sentences.
func TestUnknownJti400(t *testing.T) {
	t.Parallel()
	a := newAuthApp(t, "test")
	cid := a.register(t, "cli")
	id := uuid.New()
	access, refresh := a.wantPair(t, a.token(codeForm(a.mintCode(id, cid, verifier), cid, verifier)), id)
	unstored, _ := a.tokens.MintRefresh(id, a.now)
	expired, _ := a.tokens.MintRefresh(id, a.now.Add(-31*24*time.Hour))
	cases := []struct {
		name, refresh, code, desc string
	}{
		{"unknown jti", unstored, "invalid_grant", "unknown refresh token"},
		{"access token", access, "invalid_grant", "invalid refresh token: not a refresh token"},
		{"garbage", "garbage", "invalid_grant", "invalid refresh token: Not enough segments"},
		{"expired", expired, "invalid_grant", "invalid refresh token: Signature has expired"},
		{"other secret", oauth.NewTokens("another-secret", time.Hour, time.Hour).MintAccess(id, a.now), "invalid_grant", "invalid refresh token: Signature verification failed"},
		{"empty", "", "invalid_request", "refresh_token is required"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			wantOAuthError(t, a.token(refreshForm(c.refresh, cid)), http.StatusBadRequest, c.code, c.desc)
		})
	}
	t.Run("absent", func(t *testing.T) {
		t.Parallel()
		wantOAuthError(t, a.token(url.Values{"grant_type": {"refresh_token"}}), http.StatusBadRequest, "invalid_request", "refresh_token is required")
	})
	t.Run("stored token still refreshes", func(t *testing.T) {
		t.Parallel()
		a.wantPair(t, a.token(refreshForm(refresh, "ignored-client")), id)
	})
}

func TestUnsupportedGrantType(t *testing.T) {
	t.Parallel()
	a := newAuthApp(t, "test")
	wantOAuthError(t, a.token(url.Values{"grant_type": {"password"}}), http.StatusBadRequest,
		"unsupported_grant_type", "unsupported grant_type: password")
	wantOAuthError(t, a.token(url.Values{"grant_type": {"client_credentials"}, "code": {"x"}}), http.StatusBadRequest,
		"unsupported_grant_type", "unsupported grant_type: client_credentials")
	// Go change 9: Python answered FastAPI's 422.
	wantOAuthError(t, a.token(url.Values{"code": {"x"}}), http.StatusBadRequest, "invalid_request", "missing form field: grant_type")
	wantOAuthError(t, a.token(url.Values{"grant_type": {""}, "code": {"x"}}), http.StatusBadRequest, "invalid_request", "missing form field: grant_type")
}

// The authorization_code grant's refusals in Python's order. None of them
// spends the code.
func TestCodeGrantRefusalsInOrder(t *testing.T) {
	t.Parallel()
	a := newAuthApp(t, "test")
	cid := a.register(t, "cli")
	other := a.register(t, "other")
	cases := []struct {
		name  string
		edit  func(user uuid.UUID, f url.Values)
		code  string
		desc  string
		spent bool
	}{
		{
			"no code", func(_ uuid.UUID, f url.Values) { f.Del("code"); f.Del("client_id") },
			"invalid_request", "code and code_verifier are required", false,
		},
		{
			"no verifier", func(_ uuid.UUID, f url.Values) { f.Set("code_verifier", ""); f.Del("client_id") },
			"invalid_request", "code and code_verifier are required", false,
		},
		{
			"no client_id", func(_ uuid.UUID, f url.Values) { f.Del("client_id"); f.Set("code", "garbage") },
			"invalid_request", "client_id is required", false,
		},
		{
			"garbage code", func(_ uuid.UUID, f url.Values) { f.Set("code", "garbage"); f.Set("client_id", other) },
			"invalid_grant", "invalid authorization code: Not enough segments", false,
		},
		{"expired code", func(u uuid.UUID, f url.Values) {
			f.Set("code", a.tokens.MintCode(oauth.CodeClaims{UserID: u, ClientID: cid, RedirectURI: redirectURI, CodeChallenge: challengeFor(verifier)}, a.now.Add(-121*time.Second)))
		}, "invalid_grant", "invalid authorization code: Signature has expired", false},
		{
			"access token as code", func(u uuid.UUID, f url.Values) { f.Set("code", a.tokens.MintAccess(u, a.now)) },
			"invalid_grant", "invalid authorization code: not an authorization code", false,
		},
		{"non-UUID sub", func(_ uuid.UUID, f url.Values) {
			f.Set("code", forge(t, appSecret, hs256, map[string]any{
				"typ": "code", "sub": "admin", "client_id": cid, "redirect_uri": redirectURI,
				"code_challenge": challengeFor(verifier), "iat": a.now.Unix(), "exp": a.now.Add(time.Minute).Unix(),
			}))
		}, "invalid_grant", "invalid authorization code: invalid sub claim", false},
		{
			"no redirect_uri", func(_ uuid.UUID, f url.Values) { f.Del("redirect_uri"); f.Set("client_id", other) },
			"invalid_grant", "redirect_uri mismatch", false,
		},
		{
			"other redirect_uri", func(_ uuid.UUID, f url.Values) { f.Set("redirect_uri", redirectURI+"/x"); f.Set("client_id", other) },
			"invalid_grant", "redirect_uri mismatch", false,
		},
		{
			"other client_id", func(_ uuid.UUID, f url.Values) { f.Set("client_id", other); f.Set("code_verifier", "short") },
			"invalid_grant", "client_id mismatch", false,
		},
		{
			"bad verifier before PKCE", func(_ uuid.UUID, f url.Values) { f.Set("code_verifier", "short") },
			"invalid_grant", "invalid code_verifier", false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			user := uuid.New()
			code := a.mintCode(user, cid, verifier)
			form := codeForm(code, cid, verifier)
			c.edit(user, form)
			wantOAuthError(t, a.token(form), http.StatusBadRequest, c.code, c.desc)
			// The refusal did not spend the code.
			a.wantPair(t, a.token(codeForm(code, cid, verifier)), user)
		})
	}
}

// Revoke knows refresh tokens only (kept from Python): an access token is a
// no-op and the user's refresh token still works.
func TestRevokeAccessTokenNoop(t *testing.T) {
	t.Parallel()
	a := newAuthApp(t, "test")
	cid := a.register(t, "cli")
	id := uuid.New()
	access, refresh := a.wantPair(t, a.token(codeForm(a.mintCode(id, cid, verifier), cid, verifier)), id)
	wantEmptyObject(t, a.revoke(access))
	if n := a.count(t, `SELECT count(*) FROM oauth_refresh_tokens WHERE revoked`); n != 0 {
		t.Fatalf("%d tokens revoked by an access-token revoke", n)
	}
	a.wantPair(t, a.token(refreshForm(refresh, cid)), id)
}

// RFC 7009: revoke answers 200 {} for any token, known or not.
func TestRevokeAlways200(t *testing.T) {
	t.Parallel()
	a := newAuthApp(t, "test")
	cid := a.register(t, "cli")
	id := uuid.New()
	_, refresh := a.wantPair(t, a.token(codeForm(a.mintCode(id, cid, verifier), cid, verifier)), id)
	unstored, _ := a.tokens.MintRefresh(id, a.now)
	for _, tok := range []string{"garbage", unstored, refresh, refresh} {
		wantEmptyObject(t, a.revoke(tok))
	}
	if n := a.count(t, `SELECT count(*) FROM oauth_refresh_tokens WHERE NOT revoked`); n != 0 {
		t.Fatalf("%d live refresh tokens after revoke, want 0", n)
	}
	// Extra form fields (token_type_hint, client_id) are ignored.
	wantEmptyObject(t, a.postForm("/oauth/revoke", url.Values{"token": {"x"}, "token_type_hint": {"refresh_token"}, "client_id": {cid}}))
	// Go change 9: a missing token is an OAuth 400, not FastAPI's 422.
	wantOAuthError(t, a.postForm("/oauth/revoke", url.Values{}), http.StatusBadRequest, "invalid_request", "missing form field: token")
	wantOAuthError(t, a.revoke(""), http.StatusBadRequest, "invalid_request", "missing form field: token")
}

// Spent-code rows more than five minutes past their expiry (the grace for
// clock skew between replicas) are pruned on a token request.
func TestSpentCodesPruned(t *testing.T) {
	t.Parallel()
	a := newAuthApp(t, "test")
	if _, err := a.pool.Exec(t.Context(),
		`INSERT INTO oauth_spent_codes (code_hash, expires_at) VALUES ('old', $1), ('grace', $2), ('recent', $3), ('live', $4)`,
		a.now.Add(-5*time.Minute-time.Second), a.now.Add(-5*time.Minute), a.now.Add(-time.Second), a.now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	wantOAuthError(t, a.token(url.Values{"grant_type": {"password"}}), http.StatusBadRequest,
		"unsupported_grant_type", "unsupported grant_type: password")
	var left []string
	rows, err := a.pool.Query(t.Context(), `SELECT code_hash FROM oauth_spent_codes ORDER BY code_hash`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		left = append(left, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(left, ",") != "grace,live,recent" {
		t.Fatalf("spent codes left = %v, want [grace live recent]", left)
	}
}
