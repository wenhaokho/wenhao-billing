// Package mcp is the MCP connector transport. The server file builds the
// stateless streamable-HTTP server: the ping tool, the Host/Origin guard,
// the bearer verifier, and the /mcp -> /mcp/ redirect. Tools land in
// later tasks; this file wires only ping.
package mcp

import (
	"context"
	"net/http"
	"strings"

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

func pingTool(ctx context.Context, _ *mcp.CallToolRequest, _ pingIn) (*mcp.CallToolResult, pingOut, error) {
	// Tools prove the transport authenticated them; the verifier already
	// refused unauthenticated calls, so a missing principal is a bug, but
	// ping still answers pong for direct (non-transport) calls.
	_, _ = principal(ctx)
	return nil, pingOut{Result: "pong"}, nil
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
	mcp.AddTool(server, &mcp.Tool{Name: "ping", Description: "Health check \u2014 returns 'pong'."}, pingTool)
	stream := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{Stateless: true, DisableLocalhostProtection: true},
	)
	resourceURL := strings.TrimRight(deps.Cfg.MCPPublicBaseURL, "/") + "/.well-known/oauth-protected-resource"
	verifier := newVerifier(deps.Pool, clock, tokens)
	authed := auth.RequireBearerToken(verifier.verify, &auth.RequireBearerTokenOptions{
		ResourceMetadataURL:    resourceURL,
		AllowMissingExpiration: true,
	})
	guarded := newHostGuard(deps.Cfg).wrap(authed(stream))
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
