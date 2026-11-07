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

func testScopedInvoiceLifecycle(t *testing.T, pool *pgxpool.Pool, a, b, bUser, invoiceB, itemA, itemB, missing, good string,
	token func(string, string) string, request func(string, string, string, string) *httptest.ResponseRecorder,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	seed := func(status, balance string, template bool) string {
		t.Helper()
		n++
		id := fmt.Sprintf("e265075e-d3b9-4deb-9cb4-%012d", n)
		exec(`INSERT INTO invoices(business_id,invoice_id,invoice_type,currency,amount,balance_due,status,is_template)
			VALUES ($1,$2,'MILESTONE','USD',10,$3,$4,$5)`, a, id, balance, status, template)
		return id
	}
	line := func(owner, invoice, item, amount string) {
		t.Helper()
		exec(`INSERT INTO invoice_line_items(business_id,line_item_id,invoice_id,item_id,position,description,quantity,unit_price,amount)
			VALUES ($1,gen_random_uuid(),$2,$3,0,'Lifecycle',1,10,$4)`, owner, invoice, item, amount)
	}
	call := func(method, id, action, session, body string) *httptest.ResponseRecorder {
		path := "/api/v1/invoices/" + id
		if action != "" {
			path += "/" + action
		}
		return request(method, path, session, body)
	}
	expect := func(w *httptest.ResponseRecorder, code int) {
		t.Helper()
		if w.Code != code {
			t.Fatalf("got %d %s, want %d", w.Code, w.Body, code)
		}
	}
	expectTransition := func(w *httptest.ResponseRecorder, status, balance string) {
		t.Helper()
		expect(w, 200)
		var invoice map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &invoice); err != nil || invoice["status"] != status || invoice["balance_due"] != balance || invoice["business_id"] != nil {
			t.Fatalf("transition response: %s / %v", w.Body, err)
		}
	}
	snapshot := func(owner string, includeInvoice bool) string {
		t.Helper()
		var value string
		err := pool.QueryRow(ctx, `SELECT jsonb_build_array(
			(SELECT jsonb_agg(to_jsonb(i) ORDER BY invoice_id) FROM invoices i WHERE business_id=$1 AND $2),
			(SELECT jsonb_agg(to_jsonb(l) ORDER BY line_item_id) FROM invoice_line_items l WHERE business_id=$1 AND $2),
			(SELECT jsonb_agg(to_jsonb(p) ORDER BY payment_id) FROM payments p WHERE business_id=$1),
			(SELECT jsonb_agg(to_jsonb(j) ORDER BY entry_id) FROM journal_entries j WHERE business_id=$1),
			(SELECT jsonb_agg(to_jsonb(j) ORDER BY line_id) FROM journal_lines j WHERE business_id=$1),
			(SELECT jsonb_agg(to_jsonb(h) ORDER BY invoice_id,item_id) FROM hosting_restore_intents h WHERE business_id=$1))::text`, owner, includeInvoice).Scan(&value)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	exec(`UPDATE items SET is_hosting=true WHERE business_id=$1 AND item_id=$2`, a, itemA)
	exec(`UPDATE items SET is_hosting=true WHERE business_id=$1 AND item_id=$2`, b, itemB)
	blockedDelete := seed("DRAFT", "10", false)
	line(a, blockedDelete, itemA, "10")
	exec(`INSERT INTO hosting_restore_intents(business_id,invoice_id,item_id) VALUES ($1,$2,$3)`, a, blockedDelete, itemA)
	foreignBefore, effectsBefore := snapshot(b, true), snapshot(a, false)
	for _, action := range []string{"", "finalize", "mark-paid", "void"} {
		method, body := "POST", ""
		if action == "" {
			method = "DELETE"
		}
		if action == "void" {
			body = `{"reason":"test"}`
		}
		f, m := call(method, invoiceB, action, good, body), call(method, missing, action, good, body)
		expect(f, 400)
		expect(m, 400)
		if strings.ReplaceAll(f.Body.String(), invoiceB, missing) != m.Body.String() {
			t.Fatalf("foreign and missing responses differ: %s / %s", f.Body, m.Body)
		}
		expect(call(method, blockedDelete, action, token(bUser, b), body), 401)
		expect(call(method, blockedDelete, action, token(bUser, a), body), 403)
		expect(call(method, "bad", action, good, body), 422)
	}
	beforeFailure := snapshot(a, true)
	expect(call("DELETE", blockedDelete, "", good, ""), 500)
	if snapshot(a, true) != beforeFailure {
		t.Fatal("failed delete did not roll back its line cascade")
	}
	draft := seed("DRAFT", "10.0001", false)
	line(a, draft, itemA, "10")
	expectTransition(call("POST", draft, "finalize", good, ""), "OPEN", "10.0001")
	expect(call("POST", draft, "finalize", good, ""), 400)
	expect(call("DELETE", draft, "", good, ""), 400)
	expect(call("POST", draft, "mark-paid", good, ""), 400)
	expect(call("POST", draft, "void", good, `{}`), 422)
	expectTransition(call("POST", draft, "void", good, `{"reason":"write-off"}`), "VOID", "0.0000")
	expectTransition(call("POST", seed("DRAFT", "-1", false), "finalize", good, ""), "PAID", "0.0000")
	expect(call("POST", seed("DRAFT", "0", true), "finalize", good, ""), 400)
	paid := seed("PARTIAL", "0", false)
	expectTransition(call("POST", paid, "mark-paid", good, ""), "PAID", "0.0000")
	expect(call("POST", paid, "void", good, `{"reason":"test"}`), 400)
	corrupt := seed("DRAFT", "10", false)
	line(a, corrupt, itemA, "NaN")
	beforeFailure = snapshot(a, true)
	expect(call("POST", corrupt, "finalize", good, ""), 500)
	if snapshot(a, true) != beforeFailure {
		t.Fatal("invalid response committed the status change")
	}
	expect(call("DELETE", corrupt, "", good, ""), 204)
	var remaining int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM invoice_line_items WHERE business_id=$1 AND invoice_id=$2`, a, corrupt).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("draft line cascade: %d / %v", remaining, err)
	}

	// Hold both businesses' hosting items. Finalize must wait for A's item
	// and must finish while B's item remains locked.
	race := seed("DRAFT", "10", false)
	line(a, race, itemA, "10")
	itemLocks, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer itemLocks.Rollback(ctx)
	foreignLock, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer foreignLock.Rollback(ctx)
	const lockItem = `SELECT item_id FROM items WHERE business_id=$1 AND item_id=$2 FOR UPDATE`
	if _, err := itemLocks.Exec(ctx, lockItem, a, itemA); err != nil {
		t.Fatal(err)
	}
	if _, err := foreignLock.Exec(ctx, lockItem, b, itemB); err != nil {
		t.Fatal(err)
	}
	const otherAdmin = "e265075e-d3b9-4deb-9cb4-ffffffffffff"
	exec(`INSERT INTO users(business_id,user_id,email,password_hash,role)
		VALUES ($1,$2,'invoice-lifecycle-admin@example.test','unused','admin')`, a, otherAdmin)
	defer exec(`DELETE FROM users WHERE business_id=$1 AND user_id=$2`, a, otherAdmin)
	otherSession := token(otherAdmin, a)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- call("POST", race, "finalize", good, "") }()
	waitForLock := func(queryPattern string, pending <-chan *httptest.ResponseRecorder) {
		t.Helper()
		for {
			var waiting bool
			if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity
				WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE $1)`, queryPattern).Scan(&waiting); err != nil {
				t.Fatal(err)
			}
			if waiting {
				return
			}
			select {
			case w := <-pending:
				t.Fatalf("request skipped its lock: %d %s", w.Code, w.Body)
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	waitForLock("%scoped invoice lifecycle item lock%", done)
	deleted := make(chan *httptest.ResponseRecorder, 1)
	go func() { deleted <- call("DELETE", race, "", otherSession, "") }()
	waitForLock("%FROM businesses%FOR NO KEY UPDATE%", deleted)
	if err := itemLocks.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case w := <-done:
		expectTransition(w, "OPEN", "10.0000")
	case <-ctx.Done():
		t.Fatal("finalize waited for a foreign hosting item")
	}
	select {
	case w := <-deleted:
		expect(w, 400)
	case <-ctx.Done():
		t.Fatal("delete did not observe the finalized invoice")
	}
	if snapshot(b, true) != foreignBefore || snapshot(a, false) != effectsBefore {
		t.Fatal("invoice lifecycle mutated foreign records, payments, journals, or restoration intents")
	}
}
