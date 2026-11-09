// Package mcp is the MCP connector transport. The server file builds the
// stateless streamable-HTTP server: the ping tool, the Host/Origin guard,
// the bearer verifier, and the /mcp -> /mcp/ redirect. Tools land in
// later tasks; this file wires only ping.
package mcp

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wenhaokho/wenhao-billing/backend/internal/email"
	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices"
	"github.com/wenhaokho/wenhao-billing/backend/internal/oauth"
	"github.com/wenhaokho/wenhao-billing/backend/internal/payments"
	"github.com/wenhaokho/wenhao-billing/backend/internal/payments/paymentsdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// Deps are what NewHandler needs. Sender is used by the gated email tools in
// a later task; Payments by resolve_reconciliation_match; ping alone needs
// Pool, Clock and Cfg.
type Deps struct {
	Pool     *pgxpool.Pool
	Clock    platform.Clock
	Cfg      platform.Config
	Sender   email.Sender
	Payments payments.Service
	// Notifier hears of the draft a trigger_recurring call generated, once it
	// is committed (invoices.DraftNotifier: best effort, it never fails the
	// tool). Nil logs the draft (invoices.LogNotifier).
	Notifier invoices.DraftNotifier
	// AfterPayment is called with the payment resolve_reconciliation_match
	// cleared, once the transaction committed and never after a refusal: the
	// seam for the hosting restore that must see the committed rows (plan 9
	// sets it to payments.Service.AfterCommit). Nil does nothing; a panic in
	// it is logged and reported, never the tool's failure.
	AfterPayment func(ctx context.Context, p paymentsdb.Payment)
}

type pingIn struct{}

type pingOut struct {
	Result string `json:"result"`
}

// pingTool is registered withoutTx: a health check must not touch the
// database, so it gets a nil tx and the helper never opens one. The
// transport's verifier already refused unauthenticated calls and the
// helper refuses a call without a principal, so reaching here means the
// request was authenticated.
func pingTool(context.Context, pgx.Tx, principalRef, pingIn) (pingOut, error) {
	return pingOut{Result: "pong"}, nil
}

// registerPing adds the ping tool. It declares its own {"result": ...}
// schema (as Python wraps it; ping never refuses) and runs withoutTx, so it
// never touches d.Pool.
func registerPing(s *mcp.Server, d Deps) {
	addTool(s, d, &mcp.Tool{Name: "ping", Description: "Health check \u2014 returns 'pong'.", InputSchema: inputSchema(inNoArgs)}, pingTool, withoutTx(), withOutSchema())
}

// toolOrder is Python's tools/list order (FastMCP lists tools in
// registration order: ping, then each tools module in import order, then
// each module's source order). The SDK lists by name, so orderTools puts the
// list back in this order; a tool missing from it sorts last, by name.
var toolOrder = []string{
	"ping", "delete_invoice", "delete_bill", "delete_customer", "send_invoice_email", "send_payment_reminder",
	"list_bills", "get_bill", "list_items", "list_vendors", "resolve_vendor", "list_customers", "get_customer",
	"resolve_customer", "list_invoices", "get_invoice", "get_business_profile", "get_stats", "list_projects",
	"get_project", "list_quotations", "get_quotation", "get_reconciliation_queue", "create_bill", "update_bill",
	"create_item", "update_item", "create_vendor", "update_vendor", "create_customer", "update_customer",
	"create_invoice", "update_invoice", "finalize_invoice", "void_invoice", "create_recurring_template",
	"trigger_recurring", "create_project", "update_project", "create_quotation", "update_quotation",
	"resolve_reconciliation_match",
}

// orderTools is receiving middleware that sorts a tools/list result into
// toolOrder.
func orderTools(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		res, err := next(ctx, method, req)
		if list, ok := res.(*mcp.ListToolsResult); ok && err == nil {
			rank := func(name string) int {
				if i := slices.Index(toolOrder, name); i >= 0 {
					return i
				}
				return len(toolOrder)
			}
			slices.SortStableFunc(list.Tools, func(a, b *mcp.Tool) int {
				if c := rank(a.Name) - rank(b.Name); c != 0 {
					return c
				}
				return strings.Compare(a.Name, b.Name)
			})
		}
		return res, err
	}
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
	deps.Clock = clock
	tokens := oauth.NewTokens(deps.Cfg.MCPTokenSecret, deps.Cfg.MCPAccessTokenTTL, deps.Cfg.MCPRefreshTokenTTL)
	server := mcp.NewServer(&mcp.Implementation{Name: "wenhao-billing", Version: "1.0.0"}, nil)
	server.AddReceivingMiddleware(orderTools)
	registerPing(server, deps)
	registerReadTools(server, deps)
	registerWriteTools(server, deps)
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
