package platform

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAuthOpenAPI(t *testing.T) {
	h, err := NewHandlerWithSessions(nil, nil, SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/openapi.json", nil))
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	var spec map[string]any
	if json.Unmarshal(w.Body.Bytes(), &spec) != nil {
		t.Fatal("invalid spec")
	}
	paths := spec["paths"].(map[string]any)
	op := func(path, method string) map[string]any {
		p, ok := paths[path].(map[string]any)
		if !ok {
			t.Fatalf("missing path %s", path)
		}
		v, ok := p[method].(map[string]any)
		if !ok {
			t.Fatalf("missing %s %s", method, path)
		}
		return v
	}
	login := op("/api/v1/auth/login", "post")
	logout := op("/api/v1/auth/logout", "post")
	me := op("/api/v1/auth/me", "get")
	if login["requestBody"] == nil {
		t.Fatal("login body missing")
	}
	for _, tc := range []struct {
		o     map[string]any
		codes []string
	}{{login, []string{"200", "401", "422"}}, {logout, []string{"204"}}, {me, []string{"200", "401", "403"}}} {
		responses := tc.o["responses"].(map[string]any)
		for _, code := range tc.codes {
			if responses[code] == nil {
				t.Errorf("missing %s", code)
			}
		}
	}
	if me["security"] == nil || login["security"] != nil || logout["security"] != nil {
		t.Fatal("incorrect operation security")
	}
	responses := login["responses"].(map[string]any)
	login200 := responses["200"].(map[string]any)
	if login200["headers"].(map[string]any)["Set-Cookie"] == nil {
		t.Fatal("missing issued cookie header")
	}
	logout204 := logout["responses"].(map[string]any)["204"].(map[string]any)
	if logout204["headers"].(map[string]any)["Set-Cookie"] == nil || logout204["content"] != nil {
		t.Fatal("invalid logout response")
	}
	components := spec["components"].(map[string]any)
	schemas := components["schemas"].(map[string]any)
	loginSchema := schemas["LoginBody"].(map[string]any)
	props := loginSchema["properties"].(map[string]any)
	for _, name := range []string{"email", "password", "remember"} {
		if props[name] == nil {
			t.Errorf("missing login property %s", name)
		}
	}
	if loginSchema["additionalProperties"] != true {
		t.Fatal("unknown request fields should be accepted")
	}
	user := schemas["SessionUser"].(map[string]any)["properties"].(map[string]any)
	for _, name := range []string{"user_id", "email", "role", "display_name", "created_at"} {
		if user[name] == nil {
			t.Errorf("missing user property %s", name)
		}
	}
	for _, tc := range []struct {
		o         map[string]any
		code, ref string
	}{{login, "200", "#/components/schemas/SessionUser"}, {login, "401", "#/components/schemas/AuthErrorBody"}, {login, "422", "#/components/schemas/ValidationErrorBody"}, {me, "200", "#/components/schemas/SessionUser"}, {me, "403", "#/components/schemas/AuthErrorBody"}} {
		r := tc.o["responses"].(map[string]any)[tc.code].(map[string]any)
		schema := r["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
		if schema["$ref"] != tc.ref {
			t.Errorf("incorrect %s response schema", tc.code)
		}
	}
	schemes := components["securitySchemes"].(map[string]any)
	cookie := schemes["sessionCookie"].(map[string]any)
	if cookie["type"] != "apiKey" || cookie["in"] != "cookie" || cookie["name"] != "session" {
		t.Fatal("invalid cookie scheme")
	}
}
