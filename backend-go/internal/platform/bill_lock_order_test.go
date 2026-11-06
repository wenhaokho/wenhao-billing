package platform

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/LOQATO/gokit/auth"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/goosemigrator"
	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

func TestBillCrossAliasLockOrder(t *testing.T) {
	for _, createFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("create_first_%t", createFirst), func(t *testing.T) { testBillCrossAliasLockOrder(t, createFirst) })
	}
}
func testBillCrossAliasLockOrder(t *testing.T, createFirst bool) {
	u, e := url.Parse(os.Getenv("PGTESTDB_URL"))
	if e != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	cfgdb := pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}
	db := pgtestdb.Custom(t, cfgdb, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	ctx := context.Background()
	p, e := pgxpool.New(ctx, db.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	_, e = p.Exec(ctx, `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'),('00000000-0000-0000-0000-000000000002','b@example.com','x','admin'); INSERT INTO vendors(vendor_id,name) VALUES ('10000000-0000-0000-0000-000000000001','a'),('10000000-0000-0000-0000-000000000002','b'); INSERT INTO items(item_id,name,item_type) VALUES ('a0000000-0000-0000-0000-000000000001','a','SERVICE'),('b0000000-0000-0000-0000-000000000002','b','SERVICE'); INSERT INTO bills(bill_id,vendor_id,currency,amount,balance_due,status) VALUES ('50000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000002','USD',0,0,'DRAFT')`)
	if e != nil {
		t.Fatal(e)
	}
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode}
	h, e := NewHandlerWithSessions(p, nil, cfg)
	if e != nil {
		t.Fatal(e)
	}
	tokens := make([]string, 2)
	for i := range tokens {
		tokens[i], e = (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": fmt.Sprintf("00000000-0000-0000-0000-%012d", i+1)})
		if e != nil {
			t.Fatal(e)
		}
	}
	lock, e := p.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer lock.Rollback(ctx)
	if _, e = lock.Exec(ctx, `SELECT 1 FROM items WHERE item_id='a0000000-0000-0000-0000-000000000001' FOR UPDATE`); e != nil {
		t.Fatal(e)
	}
	call := func(method, path, body, token string) int {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "session", Value: token})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Code
	}

	good := `[{"item_id":"a0000000-0000-0000-0000-000000000001","description":"a","quantity":1,"unit_price":1},{"item_id":"{B0000000-0000-0000-0000-000000000002}","description":"b","quantity":1,"unit_price":1},{"item_id":"A0000000-0000-0000-0000-000000000001","description":"duplicate a","quantity":1,"unit_price":1}]`
	reversed := `[{"item_id":"urn:uuid:a0000000-0000-0000-0000-000000000001","description":"a","quantity":1,"unit_price":1},{"item_id":"b0000000-0000-0000-0000-000000000002","description":"b","quantity":1,"unit_price":1}]`
	patchLines, createLines := good, reversed
	if createFirst {
		patchLines, createLines = reversed, good
	}
	patch, create := make(chan int, 1), make(chan int, 1)
	startPatch := func() {
		go func() {
			patch <- call("PATCH", "/api/v1/bills/50000000-0000-0000-0000-000000000001", `{"line_items":`+patchLines+`}`, tokens[1])
		}()
	}
	startCreate := func() {
		go func() {
			create <- call("POST", "/api/v1/bills", `{"vendor_id":"10000000-0000-0000-0000-000000000001","currency":"USD","line_items":`+createLines+`}`, tokens[0])
		}()
	}
	wait := func(n int) {
		deadline := time.Now().Add(8 * time.Second)
		for {
			var count int
			e = p.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%FROM items WHERE item_id = $1 FOR UPDATE%'`).Scan(&count)
			if e != nil {
				t.Fatal(e)
			}
			if count >= n {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("expected %d blocked item locks, got %d", n, count)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	if createFirst {
		startCreate()
	} else {
		startPatch()
	}
	wait(1)
	if createFirst {
		startPatch()
	} else {
		startCreate()
	}
	wait(2)

	if e = lock.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	for _, v := range []struct {
		name string
		ch   <-chan int
		want int
	}{{"patch", patch, 200}, {"create", create, 201}} {
		select {
		case code := <-v.ch:
			if code != v.want {
				t.Errorf("%s: got %d", v.name, code)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("%s did not complete", v.name)
		}
	}
}
