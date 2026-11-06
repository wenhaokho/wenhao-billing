package platform

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/peterldowns/pgtestdb"
)

type emptyMigrator struct{}

func (emptyMigrator) Hash() (string, error)                                   { return "billing-http-empty-v1", nil }
func (emptyMigrator) Migrate(context.Context, *sql.DB, pgtestdb.Config) error { return nil }
func testURL(t *testing.T) string {
	t.Helper()
	u, e := url.Parse(os.Getenv("PGTESTDB_URL"))
	if e != nil || u == nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	p, _ := u.User.Password()
	return pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: p, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, emptyMigrator{}).URL()
}
func TestHealthDatabaseAvailability(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := OpenDB(ctx, testURL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	h := NewHandler(pool, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/health", nil))
	if w.Code != 200 {
		t.Fatalf("healthy status = %d", w.Code)
	}
	pool.Close()
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/health", nil))
	if w.Code != 503 {
		t.Fatalf("closed database status = %d", w.Code)
	}
}
func TestInvalidDatabaseConfiguration(t *testing.T) {
	for _, s := range []string{"", "not-a-url", "postgres://user:secret@127.0.0.1:1/db?sslmode=disable", "postgres://localhost/db?pool_max_conns=secret"} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		p, err := OpenDB(ctx, s)
		cancel()
		if p != nil {
			p.Close()
		}
		if err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), s) && s != "" {
			t.Fatalf("unsafe or missing error: %v", err)
		}
	}
}
func TestMissingDatabaseIsRejectedBeforeConnecting(t *testing.T) {
	_, err := OpenDB(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "invalid or missing DATABASE_URL") {
		t.Fatalf("missing configuration: %v", err)
	}
}
func TestHTTPResponses(t *testing.T) {
	h := NewHandler(nil, []string{"http://localhost:5173"})
	for _, tc := range []struct {
		path    string
		noStore bool
	}{{"/api/x", true}, {"/oauth/x", true}, {"/mcp", true}, {"/mcpfoo", true}, {"/.well-known/x", true}, {"/api", false}, {"/oauth", false}, {"/.well-known", false}, {"/health", false}, {"/other", false}} {
		t.Run(tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
			if got := w.Header().Get("Cache-Control"); (got == "no-store") != tc.noStore {
				t.Fatalf("cache header %q", got)
			}
			if tc.path == "/api/x" && (w.Code != 404 || strings.TrimSpace(w.Body.String()) != `{"detail":"Not Found"}`) {
				t.Fatalf("404 = %d %q", w.Code, w.Body.String())
			}
		})
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/openapi.json", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"openapi"`) {
		t.Fatalf("openapi = %d %s", w.Code, w.Body.String())
	}
	for _, origin := range []string{"http://localhost:5173", "http://evil.invalid"} {
		r := httptest.NewRequest("GET", "/api/x", nil)
		r.Header.Set("Origin", origin)
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if got := w.Header().Get("Access-Control-Allow-Origin"); (got == origin) != (origin == "http://localhost:5173") {
			t.Fatalf("origin %s got %s", origin, got)
		}
	}
}
func TestNoStoreStreaming(t *testing.T) {
	h := NoStore(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public")
		io.WriteString(w, "first")
		w.(http.Flusher).Flush()
		io.WriteString(w, "second")
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/mcp/stream", nil))
	if !w.Flushed || w.Body.String() != "firstsecond" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("stream: flushed=%v body=%q headers=%v", w.Flushed, w.Body.String(), w.Header())
	}
}

func TestNoStoreNetworkEmptyAndInformational(t *testing.T) {
	srv := httptest.NewServer(NoStore(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/empty-overwrite" {
			w.Header().Set("Cache-Control", "public")
		}
		if r.URL.Path == "/api/info" {
			w.Header().Set("Cache-Control", "public")
			w.WriteHeader(http.StatusEarlyHints)
			w.Header().Set("Cache-Control", "public")
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	})))
	defer srv.Close()
	for _, tc := range []struct {
		path   string
		status int
	}{{"/api/empty", 200}, {"/api/empty-overwrite", 200}, {"/api/info", 503}} {
		resp, err := srv.Client().Get(srv.URL + tc.path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.status || resp.Header.Get("Cache-Control") != "no-store" {
			t.Errorf("%s: status=%d headers=%v", tc.path, resp.StatusCode, resp.Header)
		}
	}
}
func TestNoStoreNetworkStreaming(t *testing.T) {
	release := make(chan struct{}, 1)
	defer close(release)
	srv := httptest.NewServer(NoStore(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "first")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		io.WriteString(w, "second")
	})))
	defer srv.CloseClientConnections()
	defer srv.Close()
	client := srv.Client()
	client.Timeout = 2 * time.Second
	resp, err := client.Get(srv.URL + "/mcp/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	first := make([]byte, 5)
	if _, err = io.ReadFull(resp.Body, first); err != nil || string(first) != "first" || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("first chunk %q, %v, %v", first, err, resp.Header)
	}
	// The handler cannot write the second chunk until the first is received.
	release <- struct{}{}
	rest, err := io.ReadAll(resp.Body)
	if err != nil || string(rest) != "second" {
		t.Fatalf("remaining chunk %q: %v", rest, err)
	}
}
func TestCORSPreflight(t *testing.T) {
	srv := httptest.NewServer(NewHandler(nil, []string{"http://localhost:5173"}))
	defer srv.Close()
	for _, tc := range []struct {
		origin, method string
		status         int
	}{{"http://localhost:5173", "HEAD", 200}, {"http://localhost:5173", "DELETE", 200}, {"http://evil.invalid", "HEAD", 400}} {
		req, _ := http.NewRequest("OPTIONS", srv.URL+"/api/x", nil)
		req.Header.Set("Origin", tc.origin)
		req.Header.Set("Access-Control-Request-Method", tc.method)
		req.Header.Set("Access-Control-Request-Headers", "X-Token, Content-Type")
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != tc.status || resp.Header.Get("Cache-Control") != "no-store" {
			t.Errorf("preflight %s: %d %v", tc.origin, resp.StatusCode, resp.Header)
		}
		if tc.status == 200 {
			if resp.Header.Get("Access-Control-Allow-Origin") != tc.origin || resp.Header.Get("Access-Control-Allow-Credentials") != "true" || !strings.Contains(resp.Header.Get("Access-Control-Allow-Methods"), tc.method) || resp.Header.Get("Access-Control-Allow-Headers") != "X-Token, Content-Type" {
				t.Errorf("allowed preflight headers: %v", resp.Header)
			}
		} else if resp.Header.Get("Access-Control-Allow-Origin") != "" || resp.Header.Get("Access-Control-Allow-Credentials") != "" {
			t.Errorf("denied preflight headers: %v", resp.Header)
		}
	}
}
