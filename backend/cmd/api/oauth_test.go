package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

func send(t *testing.T, cfg platform.Config, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	handler, _ := platform.NewServer(cfg, platform.Deps{Root: oauthRoot(cfg, nil)})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
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
}
