package mcp_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/oauth2"

	"github.com/LOQATO/gokit/db"
	"github.com/LOQATO/gokit/testkit"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/mcp"
	"github.com/wenhaokho/wenhao-billing/backend/internal/oauth"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

const (
	mcpTestSecret = "mcp-test-token-secret-32-bytes!"
	publicBase    = "http://localhost:8000"
)

type staticOAuthHandler struct{ token string }

func (h *staticOAuthHandler) TokenSource(context.Context) (oauth2.TokenSource, error) {
	return oauth2.StaticTokenSource(&oauth2.Token{AccessToken: h.token, TokenType: "Bearer"}), nil
}

func (h *staticOAuthHandler) Authorize(context.Context, *http.Request, *http.Response) error {
	return nil
}

func testConfig() platform.Config {
	return platform.Config{
		Environment:           "test",
		SessionSecret:         "apitest-session-secret-32-bytes!",
		SessionMaxAge:         8 * time.Hour,
		SessionRememberMaxAge: 30 * 24 * time.Hour,
		AppBaseURL:            "http://app.test/",
		MCPEnabled:            true,
		MCPConfirmSecret:      "mcp-test-confirm-secret-32-bytes!",
		MCPTokenSecret:        mcpTestSecret,
		MCPAccessTokenTTL:     time.Hour,
		MCPRefreshTokenTTL:    30 * 24 * time.Hour,
		MCPPublicBaseURL:      publicBase,
		BaseCurrency:          "IDR",
	}
}

type harness struct {
	pool    *pgxpool.Pool
	clock   *testkit.Clock
	cfg     platform.Config
	tokens  *oauth.Tokens
	adminID uuid.UUID
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()
	pool, err := db.Open(ctx, db.PoolConfig{URL: testkit.PostgresURL(t, migrations.FS)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	clock := testkit.NewClock(apitest.Start)
	cfg := testConfig()
	tokens := oauth.NewTokens(cfg.MCPTokenSecret, cfg.MCPAccessTokenTTL, cfg.MCPRefreshTokenTTL)
	adminID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users (user_id, email, password_hash, role) VALUES ($1, 'mcp-admin@example.com', 'x', 'admin')`, adminID); err != nil {
		t.Fatal(err)
	}
	return &harness{pool: pool, clock: clock, cfg: cfg, tokens: tokens, adminID: adminID}
}

func (h *harness) handler(t *testing.T) http.Handler {
	t.Helper()
	return mcp.NewHandler(mcp.Deps{Pool: h.pool, Clock: h.clock, Cfg: h.cfg})
}

func (h *harness) accessToken(t *testing.T, id uuid.UUID) string {
	t.Helper()
	return h.tokens.MintAccess(id, h.clock.Now())
}

func (h *harness) seedStaff(t *testing.T) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := h.pool.Exec(t.Context(), `INSERT INTO users (user_id, email, password_hash, role) VALUES ($1, 'staff@example.com', 'x', 'staff')`, id); err != nil {
		t.Fatal(err)
	}
	return id
}

func postInitialize(handler http.Handler, token string) *httptest.ResponseRecorder {
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"test","version":"1.0.0"}}}`
	req := httptest.NewRequest(http.MethodPost, "/mcp/", strings.NewReader(body))
	req.Host = "localhost:8000"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func mustServer(t *testing.T, handler http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func sdkClient(t *testing.T, srv *httptest.Server, token string) (*sdkmcp.ClientSession, func()) {
	t.Helper()
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	transport := &sdkmcp.StreamableClientTransport{
		Endpoint:     srv.URL + "/mcp/",
		OAuthHandler: &staticOAuthHandler{token: token},
	}
	sess, err := client.Connect(t.Context(), transport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	return sess, func() { _ = sess.Close() }
}

func loadPythonSession(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile("testdata/python_session.json")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// test_mcp_requires_auth: /mcp/ without a token is 401.
func TestMCPRequiresAuth(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	rec := postInitialize(h.handler(t), "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401: %s", rec.Code, rec.Body)
	}
}

// test_mcp_bare_path_redirects_to_trailing_slash: 307 /mcp/ for GET/POST/DELETE.
func TestMCPBarePathRedirectsToTrailingSlash(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	handler := h.handler(t)
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete, http.MethodPut} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(method, "/mcp", nil)
			req.Header.Set("Accept", "application/json, text/event-stream")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusTemporaryRedirect {
				t.Fatalf("status %d, want 307", rec.Code)
			}
			if got := rec.Header().Get("Location"); got != "/mcp/" {
				t.Fatalf("Location %q, want /mcp/", got)
			}
		})
	}
	t.Run("query_dropped", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodPost, "/mcp?x=1", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusTemporaryRedirect {
			t.Fatalf("status %d, want 307", rec.Code)
		}
		if got := rec.Header().Get("Location"); got != "/mcp/" {
			t.Fatalf("Location %q, want /mcp/ (query dropped)", got)
		}
	})
}

func TestTokenVerifierAcceptsValidAccessTokenForAdmin(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	srv := mustServer(t, h.handler(t))
	sess, closeFn := sdkClient(t, srv, h.accessToken(t, h.adminID))
	defer closeFn()
	res, err := sess.CallTool(t.Context(), &sdkmcp.CallToolParams{Name: "ping", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("ping isError: %v", res.Content)
	}
}

func TestTokenVerifierRejectsNonAdminSubject(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	staff := h.seedStaff(t)
	rec := postInitialize(h.handler(t), h.accessToken(t, staff))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401: %s", rec.Code, rec.Body)
	}
}

func TestTokenVerifierRejectsDeletedSubject(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	rec := postInitialize(h.handler(t), h.accessToken(t, uuid.New()))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401: %s", rec.Code, rec.Body)
	}
}

func TestTokenVerifierRejectsBadToken(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	rec := postInitialize(h.handler(t), "not-a-jwt")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401: %s", rec.Code, rec.Body)
	}
}

func TestTokenVerifierRejectsRefreshToken(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	refresh, _ := h.tokens.MintRefresh(h.adminID, h.clock.Now())
	rec := postInitialize(h.handler(t), refresh)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401: %s", rec.Code, rec.Body)
	}
}

func TestVerifierRechecksAdminEachRequest(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	handler := h.handler(t)
	tok := h.accessToken(t, h.adminID)
	srv := mustServer(t, handler)
	sess, closeFn := sdkClient(t, srv, tok)
	if _, err := sess.CallTool(t.Context(), &sdkmcp.CallToolParams{Name: "ping", Arguments: map[string]any{}}); err != nil {
		_ = sess.Close()
		t.Fatal(err)
	}
	_ = sess.Close()
	closeFn()
	if _, err := h.pool.Exec(t.Context(), `UPDATE users SET role='staff' WHERE user_id=$1`, h.adminID); err != nil {
		t.Fatal(err)
	}
	rec := postInitialize(handler, tok)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("after demotion status %d, want 401: %s", rec.Code, rec.Body)
	}
}

func TestWrongHost421(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	handler := h.handler(t)
	tok := h.accessToken(t, h.adminID)

	t.Run("host", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodPost, "/mcp/", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
		req.Host = "evil.example"
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != 421 {
			t.Fatalf("status %d, want 421 Misdirected Request: %s", rec.Code, rec.Body)
		}
	})

	t.Run("origin", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodPost, "/mcp/", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`))
		req.Host = "localhost:8000"
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Origin", "https://evil.example")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != 421 {
			t.Fatalf("status %d, want 421 for bad Origin: %s", rec.Code, rec.Body)
		}
	})

	t.Run("good_origin_passes", func(t *testing.T) {
		t.Parallel()
		body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`
		req := httptest.NewRequest(http.MethodPost, "/mcp/", strings.NewReader(body))
		req.Host = "localhost:8000"
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Origin", publicBase)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code == 421 {
			t.Fatalf("status 421 for good Origin, want pass-through: %s", rec.Body)
		}
	})
}

func TestWwwAuthenticateMatchesPython(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	rec := postInitialize(h.handler(t), "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", rec.Code)
	}
	got := rec.Header().Get("WWW-Authenticate")
	py := loadPythonSession(t)
	headers, _ := py["unauthorized"].(map[string]any)["headers"].(map[string]any)
	want, _ := headers["www-authenticate"].(string)
	if got != want {
		t.Fatalf("WWW-Authenticate %q, want Python %q", got, want)
	}
}

// TestVerifierDatabaseErrorIsGeneric: a database failure in the verifier is
// a 500 whose body carries no driver text. The detail is logged, not sent.
func TestVerifierDatabaseErrorIsGeneric(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	// A pool that is closed: the verifier's SELECT fails with a driver
	// error, not with "no rows".
	h.pool.Close()
	rec := postInitialize(h.handler(t), h.accessToken(t, h.adminID))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500: %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	for _, leak := range []string{"closed pool", "pgx", "conn closed", "database"} {
		if strings.Contains(strings.ToLower(body), leak) {
			t.Fatalf("the driver text leaked into the 500 body (%q): %s", leak, body)
		}
	}
}

func TestPingShapeMatchesPython(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	srv := mustServer(t, h.handler(t))
	sess, closeFn := sdkClient(t, srv, h.accessToken(t, h.adminID))
	defer closeFn()
	res, err := sess.CallTool(t.Context(), &sdkmcp.CallToolParams{Name: "ping", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("ping isError: %v", res.Content)
	}
	sc, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if string(sc) != `{"result":"pong"}` {
		py := loadPythonSession(t)
		pyPing, _ := py["ping"].(map[string]any)
		pySC, _ := json.Marshal(pyPing["structuredContent"])
		t.Fatalf("structuredContent %s, want {\"result\":\"pong\"} (Python %s)", sc, pySC)
	}
	if got := sess.InitializeResult().ServerInfo.Name; got != "wenhao-billing" {
		t.Fatalf("server name %q, want wenhao-billing", got)
	}
	list, err := sess.ListTools(t.Context(), &sdkmcp.ListToolsParams{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Tools) == 0 || list.Tools[0].Name != "ping" {
		t.Fatalf("tools %v, want ping first (Python's order)", list.Tools)
	}
	if list.Tools[0].Description != "Health check \u2014 returns 'pong'." {
		t.Fatalf("ping description %q", list.Tools[0].Description)
	}
}

// The rollback of a failed tool is TestToolErrorRollsBackItsWrites in
// tools_test.go: it drives the tool through the transport, so the tool's
// writes really do go through the helper's transaction.

const (
	verifier    = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	redirectURI = "http://localhost/cb"
)

func challengeFor(v string) string {
	sum := sha256.Sum256([]byte(v))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func TestE2EOAuthThenAuthenticatedToolCalls(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	srv := mustServer(t, combinedHandler(t, h))
	tok := obtainAccessToken(t, srv.URL, h)
	sess, closeFn := sdkClient(t, srv, tok)
	defer closeFn()
	res, err := sess.CallTool(t.Context(), &sdkmcp.CallToolParams{Name: "ping", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("ping isError: %v", res.Content)
	}
}

func TestE2EBadTokenRejected(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	rec := postInitialize(h.handler(t), "not-a-real-token")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", rec.Code)
	}
}

func TestE2ENonAdminTokenRejected(t *testing.T) {
	t.Parallel()
	h := newHarness(t)
	staff := h.seedStaff(t)
	rec := postInitialize(h.handler(t), h.accessToken(t, staff))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", rec.Code)
	}
}

// send_invoice_email over transport is deferred to Task 10 (needs plan 9 PDF/Resend).
func TestE2ESendInvoiceEmailRequiresConfirmationOverTransport(t *testing.T) {
	t.Parallel()
	t.Skip("send_invoice_email transport test deferred to Task 10")
}

func combinedHandler(t *testing.T, h *harness) http.Handler {
	t.Helper()
	sessions := platform.NewSessions(h.cfg)
	sessions.Clock = h.clock
	r := chi.NewMux()
	oauth.Mount(r, oauth.Deps{Pool: h.pool, PublicBaseURL: h.cfg.MCPPublicBaseURL, AppBaseURL: h.cfg.AppBaseURL, Sessions: sessions, Clock: h.clock, Tokens: h.tokens})
	handler := h.handler(t)
	r.Handle("/mcp", handler)
	r.Handle("/mcp/*", handler)
	return r
}

var csrfRE = regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)

func obtainAccessToken(t *testing.T, base string, h *harness) string {
	t.Helper()
	ctx := t.Context()
	regBody, _ := json.Marshal(map[string]any{"client_name": "e2e-smoke", "redirect_uris": []string{redirectURI}})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/oauth/register", strings.NewReader(string(regBody)))
	req.Header.Set("Content-Type", "application/json")
	regResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = regResp.Body.Close() }()
	if regResp.StatusCode != http.StatusCreated {
		t.Fatalf("register %d", regResp.StatusCode)
	}
	var reg struct {
		ClientID string `json:"client_id"`
	}
	if err := json.NewDecoder(regResp.Body).Decode(&reg); err != nil {
		t.Fatal(err)
	}
	sessions := platform.NewSessions(h.cfg)
	sessions.Clock = h.clock
	cookieTok, _, err := sessions.Issue(h.adminID, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	challenge := challengeFor(verifier)
	authURL := base + "/oauth/authorize?response_type=code&client_id=" + reg.ClientID + "&redirect_uri=" + redirectURI +
		"&code_challenge=" + challenge + "&code_challenge_method=S256&state=e2e"
	req, _ = http.NewRequestWithContext(ctx, http.MethodGet, authURL, nil)
	req.AddCookie(&http.Cookie{Name: platform.SessionCookie, Value: cookieTok})
	pageResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pageResp.Body.Close() }()
	if pageResp.StatusCode != http.StatusOK {
		t.Fatalf("authorize %d", pageResp.StatusCode)
	}
	body, err := io.ReadAll(pageResp.Body)
	if err != nil {
		t.Fatal(err)
	}
	m := csrfRE.FindStringSubmatch(string(body))
	if len(m) != 2 {
		t.Fatalf("no csrf in page: %s", string(body)[:min(400, len(body))])
	}
	csrf := m[1]
	var csrfCookie *http.Cookie
	for _, c := range pageResp.Cookies() {
		if c.Name == "oauth_csrf" {
			csrfCookie = c
			break
		}
	}
	form := "response_type=code&client_id=" + reg.ClientID + "&redirect_uri=" + redirectURI +
		"&code_challenge=" + challenge + "&code_challenge_method=S256&state=e2e&csrf_token=" + csrf + "&decision=approve"
	req, _ = http.NewRequestWithContext(ctx, http.MethodPost, base+"/oauth/authorize/consent", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: platform.SessionCookie, Value: cookieTok})
	if csrfCookie != nil {
		req.AddCookie(csrfCookie)
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	consentResp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = consentResp.Body.Close() }()
	if consentResp.StatusCode != http.StatusFound && consentResp.StatusCode != http.StatusSeeOther && consentResp.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("consent %d", consentResp.StatusCode)
	}
	loc := consentResp.Header.Get("Location")
	if !strings.Contains(loc, "code=") {
		t.Fatalf("no code in Location %q", loc)
	}
	var code string
	q := strings.SplitN(loc, "?", 2)[1]
	for _, kv := range strings.Split(q, "&") {
		k, v, _ := strings.Cut(kv, "=")
		if k == "code" {
			code = v
			break
		}
	}
	if code == "" {
		t.Fatalf("empty code from %q", loc)
	}
	tokenForm := "grant_type=authorization_code&code=" + code + "&redirect_uri=" + redirectURI + "&client_id=" + reg.ClientID + "&code_verifier=" + verifier
	req, _ = http.NewRequestWithContext(ctx, http.MethodPost, base+"/oauth/token", strings.NewReader(tokenForm))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tokResp.Body.Close() }()
	if tokResp.StatusCode != http.StatusOK {
		t.Fatalf("token %d", tokResp.StatusCode)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(tokResp.Body).Decode(&tok); err != nil {
		t.Fatal(err)
	}
	return tok.AccessToken
}
