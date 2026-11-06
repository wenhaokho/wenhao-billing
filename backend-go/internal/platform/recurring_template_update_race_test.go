package platform

import (
	"context"
	"github.com/LOQATO/gokit/auth"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/goosemigrator"
	"github.com/wenhaokho/wenhao-billing/backend/migrations"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRecurringTemplateUpdateRaces(t *testing.T) {
	u, e := url.Parse(os.Getenv("PGTESTDB_URL"))
	if e != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	db := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	p, e := pgxpool.New(context.Background(), db.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	_, e = p.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','a@example.com','x','admin'); INSERT INTO customers(customer_id,name) VALUES ('10000000-0000-0000-0000-000000000001','A'); INSERT INTO invoices(invoice_id,customer_id,invoice_type,currency,amount,balance_due,status,is_template,created_at) VALUES ('20000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','MILESTONE','USD',10,10,'DRAFT',false,'2025-01-01')`)
	if e != nil {
		t.Fatal(e)
	}
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode}
	h, e := NewHandlerWithSessions(p, nil, cfg)
	if e != nil {
		t.Fatal(e)
	}
	token, e := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000001"})
	if e != nil {
		t.Fatal(e)
	}

	_, e = p.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000002','b@example.com','x','admin'); INSERT INTO invoices(invoice_id,customer_id,invoice_type,currency,amount,balance_due,status,is_template,billing_cycle_ref) VALUES ('20000000-0000-0000-0000-000000000002','10000000-0000-0000-0000-000000000001','RECURRING','USD',1,1,'DRAFT',true,'{"frequency":"DAILY","interval":1,"start_date":"2026-01-01","end_mode":"NEVER"}'),('20000000-0000-0000-0000-000000000003','10000000-0000-0000-0000-000000000001','RECURRING','USD',1,1,'DRAFT',true,'{"frequency":"DAILY","interval":1,"start_date":"2026-01-01","end_mode":"NEVER"}'); INSERT INTO invoice_line_items(line_item_id,invoice_id,position,description,quantity,unit_price,amount) VALUES ('40000000-0000-0000-0000-000000000001','20000000-0000-0000-0000-000000000002',0,'old',1,1,1),('40000000-0000-0000-0000-000000000002','20000000-0000-0000-0000-000000000003',0,'old',1,1,1)`)
	if e != nil {
		t.Fatal(e)
	}
	token2, e := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": "00000000-0000-0000-0000-000000000002"})
	if e != nil {
		t.Fatal(e)
	}
	body := `{"customer_id":"10000000-0000-0000-0000-000000000001","currency":"USD","payment_terms":"N","line_items":[{"description":"new","quantity":2,"unit_price":3}],"schedule":{"frequency":"DAILY","start_date":"2026-01-01"}}`
	call := func(method, path, content, tok string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(content))
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: "session", Value: tok})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	id := "20000000-0000-0000-0000-000000000002"
	update := "/api/v1/invoices/recurring-templates/" + id
	for _, other := range []struct {
		method, path, body string
		status             int
	}{{"PATCH", update, `{"action":"PAUSE"}`, 200}, {"POST", "/api/v1/invoices/recurring/" + id + "/trigger", `{"cycle_key":"2026-01-01"}`, 200}, {"POST", "/api/v1/invoices/" + id + "/finalize", "", 400}} {
		_, e = p.Exec(context.Background(), `UPDATE invoices SET billing_cycle_ref='{"frequency":"DAILY","interval":1,"start_date":"2026-01-01","end_mode":"NEVER"}' WHERE invoice_id=$1`, id)
		if e != nil {
			t.Fatal(e)
		}
		tx, err := p.Begin(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(context.Background(), `SELECT 1 FROM invoices WHERE invoice_id=$1 FOR UPDATE`, id); err != nil {
			t.Fatal(err)
		}
		a := make(chan *httptest.ResponseRecorder, 1)
		b := make(chan *httptest.ResponseRecorder, 1)
		go func() { a <- call("PUT", update, body, token) }()
		go func() { b <- call(other.method, other.path, other.body, token2) }()
		deadline := time.Now().Add(5 * time.Second)
		for {
			var n int
			err = p.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%FROM invoices%' AND pid<>pg_backend_pid()`).Scan(&n)
			if err != nil {
				t.Fatal(err)
			}
			if n >= 2 {
				break
			}
			if time.Now().After(deadline) {
				_ = tx.Rollback(context.Background())
				<-a
				<-b
				t.Fatalf("%s: only %d blocked", other.path, n)
			}
			time.Sleep(10 * time.Millisecond)
		}
		if err = tx.Commit(context.Background()); err != nil {
			t.Fatal(err)
		}
		wa, wb := <-a, <-b
		if wa.Code != 200 || wb.Code != other.status {
			t.Fatalf("race %s: update=%d %s other=%d %s", other.path, wa.Code, wa.Body, wb.Code, wb.Body)
		}
	}
	// A separate template can be edited while the first template is locked.
	tx, e := p.Begin(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(context.Background())
	if _, e = tx.Exec(context.Background(), `SELECT 1 FROM invoices WHERE invoice_id=$1 FOR UPDATE`, id); e != nil {
		t.Fatal(e)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- call("PUT", "/api/v1/invoices/recurring-templates/20000000-0000-0000-0000-000000000003", body, token)
	}()
	select {
	case w := <-done:
		if w.Code != 200 {
			t.Fatalf("disjoint: %d %s", w.Code, w.Body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("disjoint update blocked")
	}
	if e = tx.Commit(context.Background()); e != nil {
		t.Fatal(e)
	}
	tx, e = p.Begin(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(context.Background())
	if _, e = tx.Exec(context.Background(), `UPDATE users SET role='user' WHERE user_id='00000000-0000-0000-0000-000000000002'`); e != nil {
		t.Fatal(e)
	}
	demoted := make(chan *httptest.ResponseRecorder, 1)
	go func() { demoted <- call("PUT", update, body, token2) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var blocked bool
		if e = p.QueryRow(context.Background(), `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%FROM users%' AND pid<>pg_backend_pid())`).Scan(&blocked); e != nil {
			t.Fatal(e)
		}
		if blocked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("demotion did not block")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if e = tx.Commit(context.Background()); e != nil {
		t.Fatal(e)
	}
	if w := <-demoted; w.Code != 403 {
		t.Fatalf("demoted request: %d %s", w.Code, w.Body)
	}

}
