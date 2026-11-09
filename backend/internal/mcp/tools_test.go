package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/LOQATO/gokit/db"
	"github.com/LOQATO/gokit/testkit"
	"golang.org/x/oauth2"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/oauth"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

// The tests in this file drive the transport through the SDK's own client
// over httptest, exactly as the Python tests did: the tool is called through
// the real streamable-HTTP path, not by calling the Go function directly.

const fix1TokenSecret = "mcp-fix1-token-secret-32-bytes!"

// fix1Harness is a transport with a signed-in admin and an extra test tool.
type fix1Harness struct {
	pool    *pgxpool.Pool
	clock   *testkit.Clock
	cfg     platform.Config
	tokens  *oauth.Tokens
	adminID uuid.UUID
	srv     *httptest.Server
}

// newFix1Harness serves a transport whose tool set is ping plus the tools
// the test registers.
func newFix1Harness(t *testing.T, register func(s *mcp.Server, d Deps)) *fix1Harness {
	t.Helper()
	ctx := context.Background()
	pool, err := db.Open(ctx, db.PoolConfig{URL: testkit.PostgresURL(t, migrations.FS)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	clock := testkit.NewClock(apitest.Start)
	cfg := platform.Config{
		Environment:           "test",
		SessionSecret:         "apitest-session-secret-32-bytes!",
		SessionMaxAge:         8 * time.Hour,
		SessionRememberMaxAge: 30 * 24 * time.Hour,
		AppBaseURL:            "http://app.test/",
		MCPEnabled:            true,
		MCPConfirmSecret:      "mcp-fix1-confirm-secret-32-bytes!",
		MCPTokenSecret:        fix1TokenSecret,
		MCPAccessTokenTTL:     time.Hour,
		MCPRefreshTokenTTL:    30 * 24 * time.Hour,
		MCPPublicBaseURL:      "http://localhost:8000",
		BaseCurrency:          "IDR",
	}
	tokens := oauth.NewTokens(cfg.MCPTokenSecret, cfg.MCPAccessTokenTTL, cfg.MCPRefreshTokenTTL)
	adminID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO users (user_id, email, password_hash, role) VALUES ($1, 'fix1-admin@example.com', 'x', 'admin')`, adminID); err != nil {
		t.Fatal(err)
	}
	deps := Deps{Pool: pool, Clock: clock, Cfg: cfg}

	// The same wiring NewHandler uses, with the test's tools added.
	server := mcp.NewServer(&mcp.Implementation{Name: "wenhao-billing", Version: "1.0.0"}, nil)
	addTool(server, deps, &mcp.Tool{Name: "ping", Description: "Health check \u2014 returns 'pong'."}, pingTool)
	register(server, deps)
	stream := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true, DisableLocalhostProtection: true},
	)
	resourceURL := strings.TrimRight(cfg.MCPPublicBaseURL, "/") + "/.well-known/oauth-protected-resource"
	authed := bearer(stream, newVerifier(pool, clock, tokens), resourceURL)
	handler := newHostGuard(cfg).wrap(authed)
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/mcp/", http.StatusTemporaryRedirect)
	})
	mux.Handle("/mcp/", handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &fix1Harness{pool: pool, clock: clock, cfg: cfg, tokens: tokens, adminID: adminID, srv: srv}
}

// fix1OAuthHandler is a fixed bearer token, as the SDK's OAuthHandler.
type fix1OAuthHandler struct{ token string }

func (h *fix1OAuthHandler) TokenSource(context.Context) (oauth2.TokenSource, error) {
	return oauth2.StaticTokenSource(&oauth2.Token{AccessToken: h.token, TokenType: "Bearer"}), nil
}

func (h *fix1OAuthHandler) Authorize(context.Context, *http.Request, *http.Response) error {
	return nil
}

// callTool drives one tool call through the SDK client over the transport.
func (h *fix1Harness) callTool(t *testing.T, name string) *mcp.CallToolResult {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "fix1-client", Version: "1.0.0"}, nil)
	transport := &mcp.StreamableClientTransport{
		Endpoint:     h.srv.URL + "/mcp/",
		OAuthHandler: &fix1OAuthHandler{token: h.tokens.MintAccess(h.adminID, h.clock.Now())},
	}
	sess, err := client.Connect(t.Context(), transport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = sess.Close() }()
	res, err := sess.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	return res
}

type fix1In struct{}

type fix1Out struct {
	Result string `json:"result"`
}

// TestToolPanicIsIsErrorAndServerKeepsServing: a tool that panics answers an
// isError result (with no stack text) and the transport keeps serving: the
// next call still works. The SDK runs tool handlers in their own goroutine
// with no recover, so without the helper the panic would kill the process.
func TestToolPanicIsIsErrorAndServerKeepsServing(t *testing.T) {
	t.Parallel()
	h := newFix1Harness(t, func(s *mcp.Server, d Deps) {
		addTool(s, d, &mcp.Tool{Name: "boom", Description: "panics"}, func(context.Context, pgx.Tx, principalRef, fix1In) (fix1Out, error) {
			panic("secret internal detail: table billing_users is missing")
		})
	})

	res := h.callTool(t, "boom")
	if !res.IsError {
		t.Fatalf("panicking tool: IsError = false, want true: %+v", res.Content)
	}
	text := resultText(res)
	if strings.Contains(text, "secret internal detail") {
		t.Fatalf("the panic's text reached the client: %q", text)
	}
	if strings.Contains(text, "goroutine") || strings.Contains(text, ".go:") {
		t.Fatalf("the stack reached the client: %q", text)
	}

	// The server keeps serving: the next call still works.
	res = h.callTool(t, "ping")
	if res.IsError {
		t.Fatalf("ping after a panic: isError: %+v", res.Content)
	}
	sc, _ := json.Marshal(res.StructuredContent)
	if string(sc) != `{"result":"pong"}` {
		t.Fatalf("ping after a panic: structuredContent %s", sc)
	}
}

// TestToolErrorRollsBackItsWrites: a tool that writes a row and then
// returns an error leaves no row behind (the helper's transaction rolls
// back). This replaces the old test, which only exercised db.InTx directly.
func TestToolErrorRollsBackItsWrites(t *testing.T) {
	t.Parallel()
	h := newFix1Harness(t, func(s *mcp.Server, d Deps) {
		addTool(s, d, &mcp.Tool{Name: "write_then_fail", Description: "writes, then fails"}, func(ctx context.Context, tx pgx.Tx, _ principalRef, _ fix1In) (fix1Out, error) {
			if _, err := tx.Exec(ctx, `INSERT INTO users (user_id, email, password_hash, role) VALUES ($1, 'written-by-tool@example.com', 'x', 'admin')`, uuid.New()); err != nil {
				return fix1Out{}, err
			}
			return fix1Out{}, errors.New("simulated failure mid-tool")
		})
	})

	res := h.callTool(t, "write_then_fail")
	if !res.IsError {
		t.Fatalf("failing tool: IsError = false, want true: %+v", res.Content)
	}
	var n int
	if err := h.pool.QueryRow(t.Context(), `SELECT count(*) FROM users WHERE email='written-by-tool@example.com'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("the failed tool left %d row(s) behind", n)
	}
}

// TestToolPanicRollsBackItsWrites: a tool that writes and then panics also
// leaves nothing behind.
func TestToolPanicRollsBackItsWrites(t *testing.T) {
	t.Parallel()
	h := newFix1Harness(t, func(s *mcp.Server, d Deps) {
		addTool(s, d, &mcp.Tool{Name: "write_then_panic", Description: "writes, then panics"}, func(ctx context.Context, tx pgx.Tx, _ principalRef, _ fix1In) (fix1Out, error) {
			if _, err := tx.Exec(ctx, `INSERT INTO users (user_id, email, password_hash, role) VALUES ($1, 'written-by-panicking-tool@example.com', 'x', 'admin')`, uuid.New()); err != nil {
				return fix1Out{}, err
			}
			panic("boom")
		})
	})

	res := h.callTool(t, "write_then_panic")
	if !res.IsError {
		t.Fatalf("panicking tool: IsError = false, want true: %+v", res.Content)
	}
	var n int
	if err := h.pool.QueryRow(t.Context(), `SELECT count(*) FROM users WHERE email='written-by-panicking-tool@example.com'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("the panicked tool left %d row(s) behind", n)
	}
}

// TestToolWithoutPrincipalRunsNothing: the helper fails closed when the
// request carries no principal. The transport's verifier is the only way in,
// so this is belt and braces; the tool body must not run.
func TestToolWithoutPrincipalRunsNothing(t *testing.T) {
	t.Parallel()
	ran := false
	deps := Deps{Pool: nil, Clock: nil, Cfg: platform.Config{}}
	server := mcp.NewServer(&mcp.Implementation{Name: "wenhao-billing", Version: "1.0.0"}, nil)
	handler := addTool(server, deps, &mcp.Tool{Name: "counts_calls", Description: "counts"},
		func(context.Context, pgx.Tx, principalRef, fix1In) (fix1Out, error) {
			ran = true
			return fix1Out{Result: "ran"}, nil
		})
	// A context with no TokenInfo: no principal.
	res, _, err := handler(context.Background(), nil, fix1In{})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !res.IsError {
		t.Fatalf("a call without a principal: IsError = false, want true")
	}
	if ran {
		t.Fatal("the tool body ran without a principal")
	}
}

// resultText is the text of a tool result's content blocks.
func resultText(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
			b.WriteString("\n")
		}
	}
	return b.String()
}
