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

func TestInvoiceBillCrossAliasLockOrder(t *testing.T) {
	for _, ordinary := range []bool{false, true} {
		for _, update := range []bool{false, true} {
			for _, createFirst := range []bool{false, true} {
				t.Run(fmt.Sprintf("ordinary_%t_update_%t_first_%t", ordinary, update, createFirst), func(t *testing.T) { testInvoiceBillCrossAliasLockOrder(t, createFirst, update, ordinary) })
			}
		}
	}
}
func testInvoiceBillCrossAliasLockOrder(t *testing.T, createFirst, update, ordinary bool) {
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
	_, e = p.Exec(ctx, `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'),('00000000-0000-0000-0000-000000000002','b@example.com','x','admin'); INSERT INTO customers(customer_id,name) VALUES ('20000000-0000-0000-0000-000000000001','a'); INSERT INTO invoices(invoice_id,customer_id,invoice_type,currency,amount,balance_due,status,is_template,billing_cycle_ref) VALUES ('30000000-0000-0000-0000-000000000001','20000000-0000-0000-0000-000000000001','RECURRING','USD',1,1,'DRAFT',true,'{"frequency":"DAILY","interval":1,"start_date":"2026-01-01","end_mode":"NEVER"}'); INSERT INTO invoices(invoice_id,customer_id,invoice_type,currency,amount,balance_due,status,is_template) VALUES ('30000000-0000-0000-0000-000000000002','20000000-0000-0000-0000-000000000001','MILESTONE','USD',1,1,'DRAFT',false); INSERT INTO vendors(vendor_id,name) VALUES ('10000000-0000-0000-0000-000000000001','a'),('10000000-0000-0000-0000-000000000002','b'); INSERT INTO items(item_id,name,item_type) VALUES ('a0000000-0000-0000-0000-000000000001','a','SERVICE'),('b0000000-0000-0000-0000-000000000002','b','SERVICE'); INSERT INTO bills(bill_id,vendor_id,currency,amount,balance_due,status) VALUES ('50000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000002','USD',0,0,'DRAFT')`)
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
			method, path := "POST", "/api/v1/invoices/recurring-template"
			if update {
				method, path = "PUT", "/api/v1/invoices/recurring-templates/30000000-0000-0000-0000-000000000001"
			}
			if ordinary {
				method, path = "POST", "/api/v1/invoices"
				if update {
					method, path = "PATCH", "/api/v1/invoices/30000000-0000-0000-0000-000000000002"
				}
				createLines = `[{"item_id":"b0000000-0000-0000-0000-000000000002","description":"b","quantity":1,"unit_price":1},{"item_id":"a0000000-0000-0000-0000-000000000001","description":"a","quantity":1,"unit_price":1}]`
			}
			create <- call(method, path, `{"customer_id":"20000000-0000-0000-0000-000000000001","currency":"USD","invoice_type":"MILESTONE","payment_terms":"Net 30","schedule":{"frequency":"DAILY","start_date":"2026-01-01"},"line_items":`+createLines+`}`, tokens[0])
		}()
	}
	wait := func(n int) {
		deadline := time.Now().Add(8 * time.Second)
		for {
			var count int
			e = p.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND (query LIKE '%FROM items WHERE item_id = $1 FOR UPDATE%' OR query LIKE '%FROM items WHERE item_id=$1 FOR KEY SHARE%')`).Scan(&count)
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

	// Both requests are waiting for the lower UUID, so the higher UUID must
	// still be available regardless of which endpoint reached the lock first.
	probe, e := p.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	if ordinary && !update {
		var acquired bool
		if e = probe.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock(749221001)`).Scan(&acquired); e != nil || !acquired {
			t.Errorf("invoice number allocation held before item locks: acquired=%t err=%v", acquired, e)
		}
	}
	_, probeErr := probe.Exec(ctx, `SELECT 1 FROM items WHERE item_id='b0000000-0000-0000-0000-000000000002' FOR UPDATE NOWAIT`)
	_ = probe.Rollback(ctx)
	if probeErr != nil {
		t.Errorf("higher item was locked before lower item: %v", probeErr)
	}

	if e = lock.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	invoiceStatus := 201
	if update {
		invoiceStatus = 200
	}
	for _, v := range []struct {
		name string
		ch   <-chan int
		want int
	}{{"patch", patch, 200}, {"create", create, invoiceStatus}} {
		select {
		case code := <-v.ch:
			if code != v.want {
				t.Errorf("%s: got %d", v.name, code)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("%s did not complete", v.name)
		}
	}
	if !createFirst {
		var count int
		if e = p.QueryRow(ctx, `SELECT count(*) FROM bill_line_items WHERE bill_id='50000000-0000-0000-0000-000000000001'`).Scan(&count); e != nil || count != 3 {
			t.Errorf("duplicate item lines must persist: count=%d err=%v", count, e)
		}
	}

}
