package migrations

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func testScopedUsageLock(t *testing.T, pool *pgxpool.Pool, a, b, bUser, customerA, customerB, missing, good string,
	token func(string, string) string, request func(string, string, string, string) *httptest.ResponseRecorder,
) {
	t.Helper()
	ctx := context.Background()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	insert := func(owner, customer string) string {
		t.Helper()
		var id string
		err := pool.QueryRow(ctx, `INSERT INTO invoices(business_id,invoice_id,customer_id,invoice_type,currency,amount,balance_due,status,wht_type,wht_rate,wht_gross_up,billing_cycle_ref)
VALUES($1,gen_random_uuid(),$2,'USAGE','USD',1,1,'DRAFT','PPH23',2,true,'{"cut_off_day":31,"nested":{"n":12345678901234567890}}') RETURNING invoice_id::text`, owner, customer).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { exec(`DELETE FROM invoices WHERE business_id=$1 AND invoice_id=$2`, owner, id) })
		return id
	}
	own, foreign := insert(a, customerA), insert(b, customerB)
	snapshot := func(owner, id string) string {
		t.Helper()
		var value string
		if err := pool.QueryRow(ctx, `SELECT to_jsonb(i)::text FROM invoices i WHERE business_id=$1 AND invoice_id=$2`, owner, id).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	call := func(id, cookie, body string) *httptest.ResponseRecorder {
		return request("POST", "/api/v1/invoices/usage/"+id+"/lock", cookie, body)
	}
	expect := func(w *httptest.ResponseRecorder, code int) {
		t.Helper()
		if w.Code != code {
			t.Fatalf("got %d %s, want %d", w.Code, w.Body, code)
		}
	}
	body := fmt.Sprintf(`{"accrued_amount":"100.0000","cutoff_date":"2026-10-31","business_id":%q}`, b)
	before, foreignBefore := snapshot(a, own), snapshot(b, foreign)
	expect(call(own, token(bUser, b), body), 401)
	expect(call(own, token(bUser, a), body), 403)
	expect(call("not-a-uuid", good, body), 422)
	for _, invalid := range []string{`{"cutoff_date":"2026-10-31"}`, `{"accrued_amount":-1,"cutoff_date":"2026-10-31"}`, `{"accrued_amount":1,"cutoff_date":"bad"}`} {
		expect(call(own, good, invalid), 422)
	}
	for _, id := range []string{foreign, missing} {
		w := call(id, good, body)
		expect(w, 400)
		if w.Body.String() != fmt.Sprintf("{\"detail\":\"invoice %s not found\"}\n", id) {
			t.Fatalf("not found contract: %s", w.Body)
		}
	}
	if snapshot(a, own) != before || snapshot(b, foreign) != foreignBefore {
		t.Fatal("denied lock changed invoice state")
	}
	for range 2 {
		w := call(own, good, body)
		expect(w, 200)
		var row map[string]any
		if json.Unmarshal(w.Body.Bytes(), &row) != nil || row["invoice_id"] != own || row["status"] != "DRAFT" ||
			row["wht_base"] != "100.0000" || row["wht_amount"] != "2.0400" || row["amount"] != "102.0400" ||
			row["balance_due"] != "102.0400" || row["business_id"] != nil || !strings.Contains(w.Body.String(), "12345678901234567890") ||
			!strings.Contains(w.Body.String(), `"cutoff_date":"2026-10-31"`) || !strings.Contains(w.Body.String(), `"locked":true`) {
			t.Fatalf("usage lock response: %s", w.Body)
		}
	}
	changed := `{"accrued_amount":50,"cutoff_date":"2026-11-01"}`
	expect(call(own, good, changed), 200)
	if state := snapshot(a, own); !strings.Contains(state, `"amount": 51.0200`) || !strings.Contains(state, `"cutoff_date": "2026-11-01"`) {
		t.Fatalf("explicit replacement was not persisted: %s", state)
	}

	exec(`UPDATE invoices SET wht_gross_up=false WHERE business_id=$1 AND invoice_id=$2`, a, own)
	for _, tc := range []struct{ amount, tax string }{{"0.2499", "0.0000"}, {"0.2500", "0.0100"}} {
		body := fmt.Sprintf(`{"accrued_amount":%q,"cutoff_date":"2026-11-01"}`, tc.amount)
		w := call(own, good, body)
		expect(w, 200)
		if !strings.Contains(w.Body.String(), fmt.Sprintf(`"wht_amount":%q`, tc.tax)) {
			t.Fatalf("withholding at cent boundary: %s", w.Body)
		}
	}
	exec(`UPDATE invoices SET wht_rate=101 WHERE business_id=$1 AND invoice_id=$2`, a, own)
	before = snapshot(a, own)
	expect(call(own, good, body), 500)
	if snapshot(a, own) != before {
		t.Fatal("corrupt withholding rate changed invoice")
	}
	exec(`UPDATE invoices SET wht_rate=2,wht_gross_up=true WHERE business_id=$1 AND invoice_id=$2`, a, own)
	expect(call(own, good, changed), 200)

	t.Run("DamagedCustomerReference", func(t *testing.T) {
		var constraint string
		if err := pool.QueryRow(ctx, `SELECT pg_get_constraintdef(oid) FROM pg_constraint
WHERE conrelid='invoices'::regclass AND conname='invoices_customer_id_fkey'`).Scan(&constraint); err != nil {
			t.Fatal(err)
		}
		exec(`ALTER TABLE invoices DROP CONSTRAINT invoices_customer_id_fkey`)
		const setCustomer = `UPDATE invoices SET customer_id=$3 WHERE business_id=$1 AND invoice_id=$2`
		t.Cleanup(func() {
			exec(setCustomer, a, own, customerA)
			exec(`ALTER TABLE invoices ADD CONSTRAINT invoices_customer_id_fkey ` + constraint)
		})
		for _, ref := range []string{customerB, missing} {
			exec(setCustomer, a, own, ref)
			damaged := snapshot(a, own)
			w := call(own, good, body)
			expect(w, 400)
			if !strings.Contains(w.Body.String(), "referenced record not found") || snapshot(a, own) != damaged {
				t.Fatalf("invalid customer changed usage invoice: %s", w.Body)
			}
		}
	})

	// Force response conversion to fail after the write and verify full rollback.
	exec(`UPDATE invoices SET created_at='infinity' WHERE business_id=$1 AND invoice_id=$2`, a, own)
	before = snapshot(a, own)
	expect(call(own, good, body), 500)
	if snapshot(a, own) != before {
		t.Fatal("response failure committed a usage lock")
	}
	exec(`UPDATE invoices SET created_at=now() WHERE business_id=$1 AND invoice_id=$2`, a, own)

	// Hold a status transition open until the request waits for its invoice.
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `UPDATE invoices SET status='OPEN' WHERE business_id=$1 AND invoice_id=$2`, a, own); err != nil {
		t.Fatal(err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- call(own, good, body) }()
	for deadline := time.Now().Add(5 * time.Second); ; {
		var waiting bool
		err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity
WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%name: LockInvoice :one%')`).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("usage lock did not wait for invoice")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case w := <-done:
		expect(w, 400)
		if !strings.Contains(w.Body.String(), "status=OPEN") {
			t.Fatalf("lock did not observe committed status: %s", w.Body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("usage lock did not resume")
	}
	if state := snapshot(a, own); !strings.Contains(state, `"amount": 51.0200`) || snapshot(b, foreign) != foreignBefore {
		t.Fatal("concurrent transition or foreign invoice changed")
	}
}
