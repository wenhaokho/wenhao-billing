package main

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/LOQATO/gokit/auth"

	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

var pathParam = regexp.MustCompile(`\{[^}]+\}`)

// Every operation a feature registers, except those tagged public, refuses a
// caller with no token. A new feature is covered the moment it is registered.
func TestEveryOperationIsAuthenticated(t *testing.T) {
	tokens := auth.Tokens{Secret: []byte("test-secret-at-least-32-bytes-long!!"), AccessTTL: time.Minute}
	handler, api := platform.NewServer(platform.Config{Environment: "test"}, platform.Deps{Tokens: tokens})
	register(api, nil, tokens)
	checked := 0
	for path, item := range api.OpenAPI().Paths {
		for method, op := range map[string]*huma.Operation{
			http.MethodGet: item.Get, http.MethodPost: item.Post, http.MethodPut: item.Put,
			http.MethodPatch: item.Patch, http.MethodDelete: item.Delete,
		} {
			if op == nil || slices.Contains(op.Tags, platform.PublicTag) {
				continue
			}
			url := pathParam.ReplaceAllString(path, "00000000-0000-7000-8000-000000000000")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(method, url, strings.NewReader("{}")))
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("%s %s without a token: %d, want 401", method, path, rec.Code)
			}
			checked++
		}
	}
	if checked == 0 {
		// TEMPORARY (plan 2, Task 1): no feature is registered yet. Change this
		// back to t.Error when the first protected operation lands.
		t.Skip("no protected operation was registered, so nothing was checked")
	}
}

// Every operation is either public or declares the permission it needs: a
// feature copied without one fails here, not in production.
func TestEveryOperationDeclaresAPermission(t *testing.T) {
	_, api := platform.NewServer(platform.Config{Environment: "test"}, platform.Deps{})
	register(api, nil, auth.Tokens{})
	for path, item := range api.OpenAPI().Paths {
		for method, op := range map[string]*huma.Operation{
			http.MethodGet: item.Get, http.MethodPost: item.Post, http.MethodPut: item.Put,
			http.MethodPatch: item.Patch, http.MethodDelete: item.Delete,
		} {
			if op == nil || slices.Contains(op.Tags, platform.PublicTag) {
				continue
			}
			if platform.PermissionOf(op) == "" {
				t.Errorf("%s %s declares no permission (use platform.Needs)", method, path)
			}
		}
	}
}

// The frontend imports generated types by these names. An anonymous request
// body gets a name from its Go field ("CreateInBody"), and a second feature
// with the same shape becomes "CreateInBody1", renaming the first one's
// whenever registration order changes. Bodies must be named types.
func TestSchemaNamesAreStable(t *testing.T) {
	_, api := platform.NewServer(platform.Config{Environment: "test"}, platform.Deps{})
	register(api, nil, auth.Tokens{})
	endsInDigit := regexp.MustCompile(`[0-9]$`)
	for name := range api.OpenAPI().Components.Schemas.Map() {
		if strings.Contains(name, "InBody") || endsInDigit.MatchString(name) {
			t.Errorf("schema %q: give the body a named type", name)
		}
	}
}
