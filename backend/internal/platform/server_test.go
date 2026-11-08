package platform

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/LOQATO/gokit/auth"
	"github.com/LOQATO/gokit/db"
	"github.com/LOQATO/gokit/testkit"

	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

const testSecret = "test-secret-at-least-32-bytes-long!!"

const testOrigin = "http://localhost:3000"

// testServer serves the whole app against a fresh migrated database.
func testServer(t *testing.T) http.Handler {
	t.Helper()
	pool, err := db.Open(context.Background(), db.PoolConfig{URL: testkit.PostgresURL(t, migrations.FS)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	h, _ := NewServer(Config{Environment: "test", CORSOrigins: []string{testOrigin}}, Deps{
		Pool:   pool,
		Tokens: auth.Tokens{Secret: []byte(testSecret), AccessTTL: time.Minute, Leeway: 10 * time.Second},
	})
	return h
}

func get(t *testing.T, h http.Handler, path string, header ...string) *httptest.ResponseRecorder {
	t.Helper()
	return do(t, h, http.MethodGet, path, "", header...)
}

func do(t *testing.T, h http.Handler, method, path, body string, header ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

type named struct {
	Body struct {
		Name string `json:"name" required:"true"`
	}
}

type okOut struct {
	Body struct {
		Name string `json:"name"`
	}
}

// probeServer is a server with throwaway public operations and no database.
func probeServer(t *testing.T, origins ...string) http.Handler {
	t.Helper()
	h, api := NewServer(Config{Environment: "test", CORSOrigins: origins}, Deps{})
	pub := func(id, method, path string) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Tags: []string{PublicTag}}
	}
	huma.Register(api, pub("fail", http.MethodGet, "/fail"), func(context.Context, *struct{}) (*okOut, error) {
		return nil, Err(400, "invoice_voided", "cannot send a voided invoice")
	})
	huma.Register(api, pub("boom", http.MethodGet, "/boom"), func(context.Context, *struct{}) (*okOut, error) {
		panic("secret internal detail")
	})
	huma.Register(api, pub("create", http.MethodPost, "/create"), func(_ context.Context, in *named) (*okOut, error) {
		out := &okOut{}
		out.Body.Name = in.Body.Name
		return out, nil
	})
	huma.Register(api, pub("apix", http.MethodGet, "/api/v1/x"), func(context.Context, *struct{}) (*okOut, error) {
		return &okOut{}, nil
	})
	return h
}

func TestHealth(t *testing.T) {
	rec := get(t, testServer(t), "/health")
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"status":"ok"}` {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
}

func TestErrorsAreProblemJSON(t *testing.T) {
	rec := get(t, probeServer(t), "/fail")
	want := `{"title":"Bad Request","status":400,"detail":"cannot send a voided invoice","code":"invoice_voided"}`
	if rec.Code != 400 || rec.Header().Get("Content-Type") != "application/problem+json" || strings.TrimSpace(rec.Body.String()) != want {
		t.Errorf("%d %s %s", rec.Code, rec.Header().Get("Content-Type"), rec.Body)
	}
}

func TestErrorDetailIgnoresIndonesian(t *testing.T) {
	rec := get(t, probeServer(t), "/fail", "Accept-Language", "id")
	var b struct{ Detail string }
	_ = json.Unmarshal(rec.Body.Bytes(), &b)
	if b.Detail != "cannot send a voided invoice" {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
}

func TestUnknownRouteIsProblem404(t *testing.T) {
	rec := get(t, probeServer(t), "/nowhere", "Accept-Language", "id")
	var b struct {
		Status       int
		Detail, Code string
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &b)
	if rec.Code != 404 || b.Status != 404 || b.Detail != "Not found" || b.Code != "not_found" {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
}

func TestValidationIs422ProblemWithErrors(t *testing.T) {
	rec := do(t, probeServer(t), http.MethodPost, "/create", `{}`)
	var b struct {
		Detail string
		Errors []struct{ Location, Message string }
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &b)
	if rec.Code != 422 || b.Detail == "" || len(b.Errors) == 0 || !strings.Contains(b.Errors[0].Location, "name") {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
}

func TestPanicIsProblem500(t *testing.T) {
	h := probeServer(t)
	rec := get(t, h, "/boom")
	var b struct {
		Status       int
		Detail, Code string
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &b)
	if rec.Code != 500 || rec.Header().Get("Content-Type") != "application/problem+json" || b.Status != 500 || b.Code != "internal_error" || b.Detail == "" ||
		strings.Contains(rec.Body.String(), "secret") {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
	if rec := get(t, h, "/api/v1/x"); rec.Code != 200 {
		t.Errorf("after a panic: %d", rec.Code)
	}
}

// The Python API ignored unknown body fields; the frontend still sends some.
func TestUnknownBodyFieldsAreIgnored(t *testing.T) {
	rec := do(t, probeServer(t), http.MethodPost, "/create", `{"name":"x","receipt":null}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"name":"x"`) {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
}

func TestNoSchemaMember(t *testing.T) {
	h := probeServer(t)
	rec := do(t, h, http.MethodPost, "/create", `{"name":"x"}`)
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "$schema") {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Header().Get("Link"), "describedBy") {
		t.Errorf("Link header: %v", rec.Header())
	}
}

func TestDocsAndOpenAPIKept(t *testing.T) {
	h := probeServer(t)
	for _, p := range []string{"/docs", "/openapi.json"} {
		if rec := get(t, h, p); rec.Code != 200 {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}
}

func TestNoStoreOnApiPaths(t *testing.T) {
	h := probeServer(t)
	for path, want := range map[string]int{"/api/v1/x": 200, "/api/v1/nope": 404, "/oauth/token": 404, "/mcp": 404, "/.well-known/x": 404} {
		rec := get(t, h, path)
		if rec.Code != want || rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: %d %q", path, rec.Code, rec.Header().Get("Cache-Control"))
		}
	}
	if rec := get(t, h, "/fail"); rec.Header().Get("Cache-Control") != "" {
		t.Errorf("/fail: %q", rec.Header().Get("Cache-Control"))
	}
}

func TestSecurityHeaders(t *testing.T) {
	if rec := get(t, probeServer(t), "/fail"); rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("no nosniff header")
	}
}

func TestCorsAllowsConfiguredOriginWithCredentials(t *testing.T) {
	h := probeServer(t, testOrigin)
	rec := get(t, h, "/fail", "Origin", testOrigin)
	if rec.Header().Get("Access-Control-Allow-Origin") != testOrigin || rec.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Errorf("CORS headers: %v", rec.Header())
	}
	if rec := get(t, h, "/fail", "Origin", "https://evil.example"); rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("an unknown origin was allowed")
	}
}

func TestCorsBlankOriginsAllowsNone(t *testing.T) {
	rec := get(t, probeServer(t), "/fail", "Origin", testOrigin)
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("CORS headers: %v", rec.Header())
	}
}

func TestCorsAllowsSentryHeaders(t *testing.T) {
	rec := do(t, probeServer(t, testOrigin), http.MethodOptions, "/api/v1/x", "",
		"Origin", testOrigin, "Access-Control-Request-Method", "GET", "Access-Control-Request-Headers", "sentry-trace, baggage")
	allow := strings.ToLower(rec.Header().Get("Access-Control-Allow-Headers"))
	if rec.Code != 204 && rec.Code != 200 || !strings.Contains(allow, "sentry-trace") || !strings.Contains(allow, "baggage") {
		t.Errorf("%d %v", rec.Code, rec.Header())
	}
}
