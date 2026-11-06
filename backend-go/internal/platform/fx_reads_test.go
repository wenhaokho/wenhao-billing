package platform

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFXReadRequiresAuthentication(t *testing.T) {
	h, err := NewHandlerWithSessions(nil, nil, SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/accounting/base-currency", "/api/v1/accounting/fx-rates", "/api/v1/accounting/fx-rates/latest"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 401 {
			t.Errorf("%s: got %d, want 401", path, w.Code)
		}
	}
}

func TestFXReadOpenAPI(t *testing.T) {
	h, err := NewHandlerWithSessions(nil, nil, SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/openapi.json", nil))
	var doc map[string]any
	if err = json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	paths := doc["paths"].(map[string]any)
	for _, path := range []string{"/api/v1/accounting/base-currency", "/api/v1/accounting/fx-rates", "/api/v1/accounting/fx-rates/latest"} {
		op := paths[path].(map[string]any)["get"].(map[string]any)
		if op["security"] == nil {
			t.Errorf("%s no security", path)
		}
		resp := op["responses"].(map[string]any)
		for _, code := range []string{"200", "401", "403", "422", "500"} {
			if resp[code] == nil {
				t.Errorf("%s missing %s", path, code)
				continue
			}
			if code != "200" {
				r := resp[code].(map[string]any)
				schema := r["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
				want := "#/components/schemas/AuthErrorBody"
				if code == "422" {
					want = "#/components/schemas/ValidationErrorBody"
				}
				if schema["$ref"] != want {
					t.Errorf("%s %s schema %v", path, code, schema)
				}
			}
		}
		if strings.HasSuffix(path, "latest") && resp["404"] == nil {
			t.Error("latest missing 404")
		}
		if !strings.HasSuffix(path, "base-currency") {
			params := op["parameters"].([]any)
			names := map[string]bool{}
			for _, v := range params {
				p := v.(map[string]any)
				name := p["name"].(string)
				names[name] = true
				schema := p["schema"].(map[string]any)
				isRequired := strings.HasSuffix(path, "latest") && name == "from_currency"
				if (p["required"] == true) != isRequired {
					t.Errorf("%s %s required %v", path, name, p)
				}
				if name != "on_date" {
					if schema["minLength"] != float64(3) || schema["maxLength"] != float64(3) {
						t.Errorf("%s %s schema %v", path, name, schema)
					}
				} else if schema["format"] != "date" {
					t.Errorf("date schema %v", schema)
				}
			}
			for _, n := range []string{"from_currency", "to_currency"} {
				if !names[n] {
					t.Errorf("%s missing %s", path, n)
				}
			}
			if strings.HasSuffix(path, "latest") && !names["on_date"] {
				t.Error("missing on_date")
			}
		}
	}
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)
	fx := schemas["FxOutput"].(map[string]any)
	props := fx["properties"].(map[string]any)
	required := map[string]bool{}
	for _, v := range fx["required"].([]any) {
		required[v.(string)] = true
	}
	for _, name := range []string{"rate_id", "from_currency", "to_currency", "rate", "as_of_date", "source", "created_at"} {
		p := props[name].(map[string]any)
		want := "string"
		if name == "rate_id" {
			want = "integer"
		}
		if p["type"] != want || !required[name] {
			t.Errorf("%s schema %v required %v", name, p, required[name])
		}
		if p["nullable"] == true {
			t.Errorf("%s nullable", name)
		}
	}
	if props["as_of_date"].(map[string]any)["format"] != "date" || props["created_at"].(map[string]any)["format"] != "date-time" {
		t.Error("date formats")
	}
	for _, name := range []string{"rate_id", "from_currency", "to_currency", "rate", "as_of_date", "source", "created_at"} {
		if props[name] == nil {
			t.Errorf("missing %s", name)
		}
	}
}

func TestFXBaseCurrencyConfiguration(t *testing.T) {
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode}
	for _, base := range []string{"US", "USDD"} {
		cfg.BaseCurrency = base
		if _, err := NewHandlerWithSessions(nil, nil, cfg); err == nil {
			t.Errorf("accepted %q", base)
		}
	}
	for _, base := range []string{"", "idr", "USD"} {
		cfg.BaseCurrency = base
		if _, err := NewHandlerWithSessions(nil, nil, cfg); err != nil {
			t.Errorf("rejected %q: %v", base, err)
		}
	}
}
