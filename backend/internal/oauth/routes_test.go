package oauth_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/LOQATO/gokit/db"
	"github.com/LOQATO/gokit/testkit"

	"github.com/wenhaokho/wenhao-billing/backend/internal/oauth"
	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

const publicBase = "https://billing.example.com"

// newHandler is the OAuth routes on a fresh migrated database, and a helper
// reading one text value from it. The base URL has a trailing slash, as an
// operator might type it.
func newHandler(t *testing.T) (http.Handler, func(sql string, args ...any) string) {
	t.Helper()
	pool, err := db.Open(t.Context(), db.PoolConfig{URL: testkit.PostgresURL(t, migrations.FS)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	r := chi.NewMux()
	oauth.Mount(r, oauth.Deps{Pool: pool, PublicBaseURL: publicBase + "/"})
	scalar := func(sql string, args ...any) string {
		var s string
		if err := pool.QueryRow(t.Context(), sql, args...).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	return r, scalar
}

func do(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder, status int) map[string]any {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, status, rec.Body)
	}
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("body is not a JSON object: %v: %s", err, rec.Body)
	}
	return m
}

// wantOAuthError fails unless rec is the OAuth error shape with this status,
// code and description, and nothing else in the body.
func wantOAuthError(t *testing.T, rec *httptest.ResponseRecorder, status int, code, desc string) {
	t.Helper()
	m := decode(t, rec, status)
	if len(m) != 2 || m["error"] != code || m["error_description"] != desc {
		t.Fatalf("body = %v, want error %q / %q", m, code, desc)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json (never problem+json)", ct)
	}
}

// test_authorization_server_metadata, with Python's values and key order.
func TestAuthorizationServerMetadata(t *testing.T) {
	t.Parallel()
	h, _ := newHandler(t)
	rec := do(h, http.MethodGet, "/.well-known/oauth-authorization-server", "")
	m := decode(t, rec, http.StatusOK)
	want := map[string]any{
		"issuer":                                publicBase,
		"authorization_endpoint":                publicBase + "/oauth/authorize",
		"token_endpoint":                        publicBase + "/oauth/token",
		"registration_endpoint":                 publicBase + "/oauth/register",
		"revocation_endpoint":                   publicBase + "/oauth/revoke",
		"response_types_supported":              []any{"code"},
		"grant_types_supported":                 []any{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":      []any{"S256"},
		"token_endpoint_auth_methods_supported": []any{"none"},
	}
	wantJSON, _ := json.Marshal(want)
	gotJSON, _ := json.Marshal(m)
	if string(wantJSON) != string(gotJSON) {
		t.Fatalf("metadata = %s, want %s", gotJSON, wantJSON)
	}
	var keys []string
	for _, o := range regexp.MustCompile(`"([a-z_]+)":`).FindAllStringSubmatch(rec.Body.String(), -1) {
		keys = append(keys, o[1])
	}
	wantKeys := "issuer authorization_endpoint token_endpoint registration_endpoint revocation_endpoint response_types_supported grant_types_supported code_challenge_methods_supported token_endpoint_auth_methods_supported"
	if strings.Join(keys, " ") != wantKeys {
		t.Fatalf("key order = %v", keys)
	}
}

// test_protected_resource_metadata: authorization_servers is present; the
// resource is the origin, not /mcp.
func TestProtectedResourceMetadata(t *testing.T) {
	t.Parallel()
	h, _ := newHandler(t)
	m := decode(t, do(h, http.MethodGet, "/.well-known/oauth-protected-resource", ""), http.StatusOK)
	if len(m) != 2 || m["resource"] != publicBase {
		t.Fatalf("body = %v", m)
	}
	servers, _ := m["authorization_servers"].([]any)
	if len(servers) != 1 || servers[0] != publicBase {
		t.Fatalf("authorization_servers = %v", m["authorization_servers"])
	}
}

var clientIDRE = regexp.MustCompile(`^[0-9a-f]{32}$`)

func TestRegisterReturnsPythonShape(t *testing.T) {
	t.Parallel()
	h, scalar := newHandler(t)
	rec := do(h, http.MethodPost, "/oauth/register",
		`{"client_name":"cli","redirect_uris":["http://localhost/cb","https://app.example.com/cb"],"scope":"x","grant_types":["implicit"]}`)
	m := decode(t, rec, http.StatusCreated)
	id, _ := m["client_id"].(string)
	if !clientIDRE.MatchString(id) {
		t.Fatalf("client_id = %q, want 32 lowercase hex", id)
	}
	// Extra request members are ignored; the answer is Python's, in order.
	wantBody := `{"client_id":"` + id + `","client_name":"cli","redirect_uris":["http://localhost/cb","https://app.example.com/cb"],"token_endpoint_auth_method":"none","grant_types":["authorization_code","refresh_token"],"response_types":["code"]}`
	if got := strings.TrimSpace(rec.Body.String()); got != wantBody {
		t.Fatalf("body = %s\nwant   %s", got, wantBody)
	}
	// Stored space-joined, as Python stored it.
	if got := scalar(`SELECT redirect_uris FROM oauth_clients WHERE client_id = $1`, id); got != "http://localhost/cb https://app.example.com/cb" {
		t.Fatalf("stored redirect_uris = %q", got)
	}
	if got := scalar(`SELECT client_name FROM oauth_clients WHERE client_id = $1`, id); got != "cli" {
		t.Fatalf("stored client_name = %q", got)
	}
}

func TestRegisterWithoutNameReturnsNull(t *testing.T) {
	t.Parallel()
	h, _ := newHandler(t)
	m := decode(t, do(h, http.MethodPost, "/oauth/register", `{"redirect_uris":["https://a.example/cb"]}`), http.StatusCreated)
	if v, ok := m["client_name"]; !ok || v != nil {
		t.Fatalf("client_name = %v (present %v), want null", v, ok)
	}
}

func TestRegisterEmptyRedirectUris400(t *testing.T) {
	t.Parallel()
	h, _ := newHandler(t)
	wantOAuthError(t, do(h, http.MethodPost, "/oauth/register", `{"redirect_uris":[]}`),
		http.StatusBadRequest, "invalid_redirect_uri", "redirect_uris is required")
}

func TestRegisterRejectsNonHttpsAndFragments(t *testing.T) {
	t.Parallel()
	bad := map[string]string{
		"plain http":         "http://example.com/cb",
		"javascript":         "javascript:alert(1)",
		"relative":           "/cb",
		"no host":            "https:///cb",
		"empty":              "",
		"fragment":           "https://example.com/cb#frag",
		"empty fragment":     "https://example.com/cb#",
		"space":              "https://example.com/a b",
		"tab":                "https://example.com/a\tb",
		"newline":            "https://example.com/a\nb",
		"two in one":         "https://a.example/cb https://b.example/cb",
		"custom scheme":      "myapp://callback",
		"ftp":                "ftp://localhost/cb",
		"loopback lookalike": "http://localhost.evil.com/cb",
	}
	for name, uri := range bad {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h, scalar := newHandler(t)
			body, _ := json.Marshal(map[string]any{"redirect_uris": []string{"https://ok.example/cb", uri}})
			wantOAuthError(t, do(h, http.MethodPost, "/oauth/register", string(body)),
				http.StatusBadRequest, "invalid_redirect_uri", "redirect_uri must be https or loopback http")
			if n := scalar(`SELECT count(*)::text FROM oauth_clients`); n != "0" {
				t.Fatalf("a rejected registration stored %s client(s)", n)
			}
		})
	}
}

func TestRegisterAcceptsLoopbackHttp(t *testing.T) {
	t.Parallel()
	for _, uri := range []string{
		"http://localhost/cb", "http://localhost:8765/cb", "http://127.0.0.1:3000/cb", "http://127.0.0.1/cb",
		"http://[::1]:9000/cb", "http://[::1]/cb", "https://app.example.com/cb?x=1",
	} {
		t.Run(uri, func(t *testing.T) {
			t.Parallel()
			h, _ := newHandler(t)
			body, _ := json.Marshal(map[string]any{"redirect_uris": []string{uri}})
			m := decode(t, do(h, http.MethodPost, "/oauth/register", string(body)), http.StatusCreated)
			if got, _ := m["redirect_uris"].([]any); len(got) != 1 || got[0] != uri {
				t.Fatalf("redirect_uris = %v", m["redirect_uris"])
			}
		})
	}
}

// Go change 9: a missing or bad body is 400 invalid_request in the OAuth
// shape, not FastAPI's 422 {"detail"}.
func TestRegisterBadJsonIsOauth400(t *testing.T) {
	t.Parallel()
	const desc = "request body must be JSON with redirect_uris (a list of strings) and an optional client_name"
	for name, body := range map[string]string{
		"empty body":            ``,
		"not json":              `{nope`,
		"missing redirect_uris": `{"client_name":"x"}`,
		"null redirect_uris":    `{"redirect_uris":null}`,
		"string redirect_uris":  `{"redirect_uris":"https://a.example/cb"}`,
		"non-string element":    `{"redirect_uris":[1]}`,
		"array body":            `[]`,
		"name not a string":     `{"redirect_uris":["https://a.example/cb"],"client_name":5}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h, _ := newHandler(t)
			wantOAuthError(t, do(h, http.MethodPost, "/oauth/register", body), http.StatusBadRequest, "invalid_request", desc)
		})
	}
}
