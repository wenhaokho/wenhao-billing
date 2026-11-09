// Package mcp is the MCP connector transport. The server file builds the
// stateless streamable-HTTP server: the ping tool, the Host/Origin guard,
// the bearer verifier, and the /mcp -> /mcp/ redirect. Tools land in
// later tasks; this file wires only ping.
package mcp

import (
	"context"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wenhaokho/wenhao-billing/backend/internal/email"
	"github.com/wenhaokho/wenhao-billing/backend/internal/oauth"
	"github.com/wenhaokho/wenhao-billing/backend/internal/payments"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// Deps are what NewHandler needs. Sender and Payments are used by the
// gated email and reconciliation tools in later tasks; ping alone needs
// Pool, Clock and Cfg.
type Deps struct {
	Pool     *pgxpool.Pool
	Clock    platform.Clock
	Cfg      platform.Config
	Sender   email.Sender
	Payments payments.Service
}

type pingIn struct{}

type pingOut struct {
	Result string `json:"result" jsonschema:"the pong result"`
}

func pingTool(ctx context.Context, _ pgx.Tx, _ principalRef, _ pingIn) (pingOut, error) {
	// The transport's verifier already refused unauthenticated calls and the
	// registration helper refuses a call without a principal, so reaching
	// here means the request was authenticated.
	_, _ = principal(ctx)
	return pingOut{Result: "pong"}, nil
}

// bearer wraps a handler with the SDK's bearer-token middleware: the
// verifier checks the access token and the admin on every request, and the
// 401 carries the protected-resource metadata challenge.
func bearer(next http.Handler, v *verifier, resourceURL string) http.Handler {
	return auth.RequireBearerToken(v.verify, &auth.RequireBearerTokenOptions{
		ResourceMetadataURL:    resourceURL,
		AllowMissingExpiration: true,
	})(next)
}

// NewHandler builds the /mcp endpoint. The bare /mcp path redirects to
// /mcp/ with 307 (method and body preserved, query dropped), as Python
// does; every /mcp/ request passes the Host/Origin guard (421) and the
// bearer verifier (401) before reaching the stateless streamable handler.
func NewHandler(deps Deps) http.Handler {
	clock := deps.Clock
	if clock == nil {
		clock = platform.SystemClock{}
	}
	tokens := oauth.NewTokens(deps.Cfg.MCPTokenSecret, deps.Cfg.MCPAccessTokenTTL, deps.Cfg.MCPRefreshTokenTTL)
	server := mcp.NewServer(&mcp.Implementation{Name: "wenhao-billing", Version: "1.0.0"}, nil)
	addTool(server, deps, &mcp.Tool{Name: "ping", Description: "Health check \u2014 returns 'pong'."}, pingTool)
	stream := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true, DisableLocalhostProtection: true},
	)
	resourceURL := strings.TrimRight(deps.Cfg.MCPPublicBaseURL, "/") + "/.well-known/oauth-protected-resource"
	verifier := newVerifier(deps.Pool, clock, tokens)
	guarded := newHostGuard(deps.Cfg).wrap(bearer(stream, verifier, resourceURL))
	mcpHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mcp/" {
			http.NotFound(w, r)
			return
		}
		guarded.ServeHTTP(w, r)
	})
	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/mcp/", http.StatusTemporaryRedirect)
	})
	mux.Handle("/mcp/", mcpHandler)
	return mux
}
