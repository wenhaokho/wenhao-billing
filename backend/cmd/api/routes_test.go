package main

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"slices"
	"strconv"
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

// acceptsNull reads a schema as a client generator does, from its JSON: a
// null type, a type list holding "null", or a oneOf/anyOf branch that does.
func acceptsNull(m map[string]any) bool {
	switch typ := m["type"].(type) {
	case string:
		return typ == "null"
	case []any:
		if slices.Contains(typ, any("null")) {
			return true
		}
	}
	for _, k := range []string{"oneOf", "anyOf"} {
		branches, _ := m[k].([]any)
		for _, b := range branches {
			if bm, ok := b.(map[string]any); ok && acceptsNull(bm) {
				return true
			}
		}
	}
	return false
}

// Every pointer field of a response body is documented as nullable, whatever
// its schema (a scalar, a decimal's oneOf, a $ref): the frontend's generated
// types must allow the null the encoder writes. The Go types are walked
// from each response's component.
func TestNullableResponseFieldsAcceptNull(t *testing.T) {
	_, api := platform.NewServer(platform.Config{Environment: "test"}, platform.Deps{})
	register(api, features{Sessions: testSessions()})
	doc, err := json.Marshal(api.OpenAPI())
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Components struct {
			Schemas map[string]struct {
				Properties map[string]map[string]any `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatal(err)
	}
	reg := api.OpenAPI().Components.Schemas
	seen := map[reflect.Type]bool{}
	checked := 0
	var walk func(ref string)
	walk = func(ref string) {
		typ := reg.TypeFromRef(ref)
		for typ != nil && (typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice) {
			typ = typ.Elem()
		}
		if typ == nil || typ.Kind() != reflect.Struct || seen[typ] {
			return
		}
		seen[typ] = true
		name := strings.TrimPrefix(ref, "#/components/schemas/")
		props := parsed.Components.Schemas[name].Properties
		for f := range typ.Fields() {
			field, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if f.Type.Kind() == reflect.Pointer {
				if !acceptsNull(props[field]) {
					t.Errorf("%s.%s is a pointer but its schema refuses null: %v", name, field, props[field])
				}
				checked++
			}
		}
		for _, p := range reg.SchemaFromRef(ref).Properties {
			for _, s := range append([]*huma.Schema{p, p.Items}, p.AnyOf...) {
				if s != nil && s.Ref != "" {
					walk(s.Ref)
				}
			}
		}
	}
	for _, op := range operations(api) {
		for status, resp := range op.Responses {
			if !strings.HasPrefix(status, "2") {
				continue
			}
			for _, mt := range resp.Content {
				if s := mt.Schema; s.Ref != "" {
					walk(s.Ref)
				} else if s.Items != nil && s.Items.Ref != "" {
					walk(s.Items.Ref)
				}
			}
		}
	}
	for _, want := range []string{"ItemOut", "CloudflareTargetOut", "VendorOut"} {
		if !slices.ContainsFunc(slices.Collect(maps.Keys(seen)), func(t reflect.Type) bool { return t.Name() == want }) {
			t.Errorf("%s was not walked", want)
		}
	}
	if checked == 0 {
		t.Error("no pointer field was checked")
	}
}

// No schema in the real server's OpenAPI has an empty type: Huma writes
// `"type": ["", "null"]` for a nullable schema with no type of its own,
// which is not JSON Schema.
func TestOpenAPIHasNoEmptyType(t *testing.T) {
	_, api := platform.NewServer(platform.Config{Environment: "test"}, platform.Deps{})
	register(api, features{Sessions: testSessions()})
	doc, err := json.Marshal(api.OpenAPI())
	if err != nil {
		t.Fatal(err)
	}
	var v any
	if err := json.Unmarshal(doc, &v); err != nil {
		t.Fatal(err)
	}
	var walk func(where string, v any)
	walk = func(where string, v any) {
		switch x := v.(type) {
		case map[string]any:
			switch typ := x["type"].(type) {
			case string:
				if typ == "" {
					t.Errorf("%s: empty type", where)
				}
			case []any:
				if slices.Contains(typ, any("")) {
					t.Errorf("%s: empty type in %v", where, typ)
				}
			}
			for k, c := range x {
				walk(where+"."+k, c)
			}
		case []any:
			for i, c := range x {
				walk(where+"["+strconv.Itoa(i)+"]", c)
			}
		}
	}
	walk("$", v)
}
