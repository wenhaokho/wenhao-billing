package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

func send(t *testing.T, cfg platform.Config, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	return sendHost(t, cfg, method, path, "")
}

// sendHost sends a request with a chosen Host header. The MCP connector's
// DNS-rebinding guard reads it, so a test that must reach the transport
// names an allowed host.
func sendHost(t *testing.T, cfg platform.Config, method, path, host string) *httptest.ResponseRecorder {
	t.Helper()
	handler, _ := platform.NewServer(cfg, platform.Deps{Root: oauthRoot(cfg, nil, platform.Sessions{}, nil, nil, nil)})
	req := httptest.NewRequest(method, path, nil)
	if host != "" {
		req.Host = host
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestMcpDisabledHidesRoutes(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"/.well-known/oauth-authorization-server", "/.well-known/oauth-protected-resource"} {
		t.Run(path, func(t *testing.T) {
			t.Parallel()
			if rec := send(t, platform.Config{MCPEnabled: false, MCPPublicBaseURL: "http://x"}, http.MethodGet, path); rec.Code != http.StatusNotFound {
				t.Errorf("MCP disabled: GET %s = %d, want 404", path, rec.Code)
			}
			on := send(t, platform.Config{MCPEnabled: true, MCPPublicBaseURL: "http://x"}, http.MethodGet, path)
			if on.Code != http.StatusOK {
				t.Errorf("MCP enabled: GET %s = %d, want 200", path, on.Code)
			}
			if cc := on.Header().Get("Cache-Control"); cc != "no-store" {
				t.Errorf("GET %s Cache-Control = %q, want no-store", path, cc)
			}
		})
	}
	t.Run("register", func(t *testing.T) {
		t.Parallel()
		// Registration is hidden too: no route answers a POST.
		if rec := send(t, platform.Config{MCPEnabled: false}, http.MethodPost, "/oauth/register"); rec.Code != http.StatusNotFound {
			t.Errorf("MCP disabled: POST /oauth/register = %d, want 404", rec.Code)
		}
	})
	t.Run("authorize", func(t *testing.T) {
		t.Parallel()
		for _, c := range []struct{ method, path string }{{http.MethodGet, "/oauth/authorize"}, {http.MethodPost, "/oauth/authorize/consent"}} {
			if rec := send(t, platform.Config{MCPEnabled: false}, c.method, c.path); rec.Code != http.StatusNotFound {
				t.Errorf("MCP disabled: %s %s = %d, want 404", c.method, c.path, rec.Code)
			}
			// Mounted when enabled: a bad request is the OAuth 400, not a 404.
			if rec := send(t, platform.Config{MCPEnabled: true}, c.method, c.path); rec.Code != http.StatusBadRequest {
				t.Errorf("MCP enabled: %s %s = %d, want 400", c.method, c.path, rec.Code)
			}
		}
	})
}

// TestMcpTransportIsMounted goes through platform.NewServer, the way serve
// does: the /mcp routes are there only when MCP_ENABLED.
func TestMcpTransportIsMounted(t *testing.T) {
	t.Parallel()
	t.Run("disabled", func(t *testing.T) {
		t.Parallel()
		for _, path := range []string{"/mcp", "/mcp/"} {
			if rec := send(t, platform.Config{MCPEnabled: false}, http.MethodGet, path); rec.Code != http.StatusNotFound {
				t.Errorf("MCP disabled: GET %s = %d, want 404", path, rec.Code)
			}
		}
	})
	t.Run("enabled", func(t *testing.T) {
		t.Parallel()
		cfg := platform.Config{MCPEnabled: true, MCPPublicBaseURL: "http://localhost:8000"}
		// The bare path redirects to the trailing slash.
		rec := send(t, cfg, http.MethodGet, "/mcp")
		if rec.Code != http.StatusTemporaryRedirect {
			t.Errorf("MCP enabled: GET /mcp = %d, want 307", rec.Code)
		}
		if loc := rec.Header().Get("Location"); loc != "/mcp/" {
			t.Errorf("MCP enabled: GET /mcp Location = %q, want /mcp/", loc)
		}
		// The trailing slash is the transport, and it refuses an
		// unauthenticated call with 401. The Host must be one the
		// DNS-rebinding guard allows, or the guard answers 421 first.
		rec = sendHost(t, cfg, http.MethodPost, "/mcp/", "localhost:8000")
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("MCP enabled: POST /mcp/ = %d, want 401", rec.Code)
		}
	})
}
