package platform

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LOQATO/gokit/apierr"
	"github.com/LOQATO/gokit/auth"
	"github.com/LOQATO/gokit/db"
	"github.com/LOQATO/gokit/i18n/i18ntest"
	"github.com/LOQATO/gokit/testkit"

	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

const testSecret = "test-secret-at-least-32-bytes-long!!"

// testServer serves the whole app against a fresh migrated database.
func testServer(t *testing.T) http.Handler {
	t.Helper()
	pool, err := db.Open(context.Background(), db.PoolConfig{URL: testkit.PostgresURL(t, migrations.FS)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	h, _ := NewServer(Config{Environment: "test", CORSOrigins: []string{"http://localhost:3000"}}, Deps{
		Pool:   pool,
		Tokens: auth.Tokens{Secret: []byte(testSecret), AccessTTL: time.Minute, Leeway: 10 * time.Second},
	})
	return h
}

func get(t *testing.T, h http.Handler, path string, header ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHealth(t *testing.T) {
	rec := get(t, testServer(t), "/health")
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"status":"ok"}` {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
}

func TestUnknownRouteIsAnIndonesianEnvelope404(t *testing.T) {
	rec := get(t, testServer(t), "/nowhere", "Accept-Language", "id")
	var body struct {
		Error struct{ Code, Message string } `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != 404 || body.Error.Code != "not_found" || body.Error.Message != "Tidak ditemukan" {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
}

// Huma's default config adds "$schema" to every body; the template drops it.
func TestNoSchemaMember(t *testing.T) {
	h := testServer(t)
	for _, path := range []string{"/health", "/nowhere"} {
		if rec := get(t, h, path); strings.Contains(rec.Body.String(), "$schema") {
			t.Errorf("%s: %s", path, rec.Body)
		}
	}
}

func TestSecurityHeadersAndCORS(t *testing.T) {
	rec := get(t, testServer(t), "/health", "Origin", "http://localhost:3000")
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("no nosniff header")
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "http://localhost:3000" ||
		rec.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Errorf("CORS headers: %v", rec.Header())
	}
	if rec := get(t, testServer(t), "/health", "Origin", "https://evil.example"); rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("an unknown origin was allowed")
	}
}

func TestCatalog(t *testing.T) {
	i18ntest.Scan(t, "../..", Catalog, apierr.Sentences()...)
}
