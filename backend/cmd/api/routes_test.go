package main

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

var pathParam = regexp.MustCompile(`\{[^}]+\}`)

const testSecret = "test-secret-at-least-32-bytes-long!!"

func testSessions() platform.Sessions {
	return platform.NewSessions(platform.Config{Environment: "test", SessionSecret: testSecret})
}

// operations are the registered operations of the real server.
func operations(api huma.API) map[string]*huma.Operation {
	out := map[string]*huma.Operation{}
	for path, item := range api.OpenAPI().Paths {
		for method, op := range map[string]*huma.Operation{
			http.MethodGet: item.Get, http.MethodPost: item.Post, http.MethodPut: item.Put,
			http.MethodPatch: item.Patch, http.MethodDelete: item.Delete,
		} {
			if op != nil {
				out[method+" "+path] = op
			}
		}
	}
	return out
}

// Every operation a feature registers, except those tagged public, refuses a
// caller with no session cookie. A new feature is covered the moment it is
// registered.
func TestEveryOperationIsAuthenticated(t *testing.T) {
	handler, api := platform.NewServer(platform.Config{Environment: "test"}, platform.Deps{Sessions: testSessions()})
	register(api, features{Sessions: testSessions()})
	checked := 0
	for key, op := range operations(api) {
		if platform.IsPublic(op) {
			continue
		}
		method, path, _ := strings.Cut(key, " ")
		url := pathParam.ReplaceAllString(path, "00000000-0000-7000-8000-000000000000")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(method, url, strings.NewReader("{}")))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s without a session: %d, want 401", key, rec.Code)
		}
		checked++
	}
	if checked == 0 {
		// Until the first protected operation exists, the only registered
		// operation is the public /health. Any other shape with nothing
		// protected is a real failure, so the guard re-arms by itself the
		// moment a feature registers an operation.
		paths := api.OpenAPI().Paths
		if _, onlyHealth := paths["/health"]; onlyHealth && len(paths) == 1 {
			t.Skip("only the public /health is registered; no protected operation to check yet")
		}
		t.Error("no protected operation was registered, so nothing was checked")
	}
}

// Every operation is either public or admin-only: a feature registered
// without platform.Admin fails here, not in production (where the middleware
// refuses it to everyone).
func TestEveryOperationIsAdminOrPublic(t *testing.T) {
	_, api := platform.NewServer(platform.Config{Environment: "test"}, platform.Deps{})
	register(api, features{Sessions: testSessions()})
	for key, op := range operations(api) {
		if !platform.IsPublic(op) && !platform.IsAdmin(op) {
			t.Errorf("%s is neither public nor admin-only (use platform.Admin)", key)
		}
	}
}

// The frontend imports generated types by these names. An anonymous request
// body gets a name from its Go field ("CreateInBody"), and a second feature
// with the same shape becomes "CreateInBody1", renaming the first one's
// whenever registration order changes. Bodies must be named types.
func TestSchemaNamesAreStable(t *testing.T) {
	_, api := platform.NewServer(platform.Config{Environment: "test"}, platform.Deps{})
	register(api, features{Sessions: testSessions()})
	endsInDigit := regexp.MustCompile(`[0-9]$`)
	for name := range api.OpenAPI().Components.Schemas.Map() {
		if strings.Contains(name, "InBody") || endsInDigit.MatchString(name) {
			t.Errorf("schema %q: give the body a named type", name)
		}
	}
}

// Every response field is always present: Huma lists a property in required
// unless its field has omitempty, so a property missing from required is one
// the encoder may leave out. Nullable fields are pointers without omitempty.
func TestResponsesNeverOmitFields(t *testing.T) {
	_, api := platform.NewServer(platform.Config{Environment: "test"}, platform.Deps{})
	register(api, features{Sessions: testSessions()})
	reg := api.OpenAPI().Components.Schemas
	seen := map[*huma.Schema]bool{}
	var walk func(where string, s *huma.Schema)
	walk = func(where string, s *huma.Schema) {
		if s == nil {
			return
		}
		if s.Ref != "" {
			where = s.Ref
			s = reg.SchemaFromRef(s.Ref)
			if s == nil {
				t.Errorf("%s: unresolved", where)
				return
			}
		}
		if seen[s] {
			return
		}
		seen[s] = true
		for name, p := range s.Properties {
			if !slices.Contains(s.Required, name) {
				t.Errorf("%s: %s may be omitted (drop its omitempty)", where, name)
			}
			walk(where+"."+name, p)
		}
		walk(where+"[]", s.Items)
		if ap, ok := s.AdditionalProperties.(*huma.Schema); ok {
			walk(where+"{}", ap)
		}
		for _, list := range [][]*huma.Schema{s.OneOf, s.AnyOf, s.AllOf} {
			for _, b := range list {
				walk(where, b)
			}
		}
	}
	checked := 0
	for key, op := range operations(api) {
		for status, resp := range op.Responses {
			// Error bodies are apierr's problem+json, not a feature's.
			if !strings.HasPrefix(status, "2") {
				continue
			}
			for _, mt := range resp.Content {
				walk(key+" "+status, mt.Schema)
				checked++
			}
		}
	}
	if checked == 0 {
		t.Error("no response schema was checked")
	}
}
