package migrations

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testScopedInvoicePayments(t *testing.T, pool *pgxpool.Pool, a, b, aUser, bUser, invoiceB, customerA, customerB, missing, good string,
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
	seed := func() string {
		t.Helper()
		var id string
		err := pool.QueryRow(ctx, `INSERT INTO invoices(business_id,invoice_id,customer_id,invoice_type,currency,amount,balance_due,wht_amount,status)
			VALUES ($1,gen_random_uuid(),$2,'MILESTONE','SGD',10,10,2,'OPEN') RETURNING invoice_id::text`, a, customerA).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	post := func(id, session, payload string) *httptest.ResponseRecorder {
		return request("POST", "/api/v1/invoices/"+id+"/record-payment", session, payload)
	}
	list := func(id, session string) *httptest.ResponseRecorder {
		return request("GET", "/api/v1/invoices/"+id+"/payments", session, "")
	}
	expect := func(w *httptest.ResponseRecorder, code int, body string) {
		t.Helper()
		if w.Code != code || !strings.Contains(w.Body.String(), body) {
			t.Fatalf("want %d %q, got %d %s", code, body, w.Code, w.Body)
		}
	}
	id := seed()
	const partial = `{"amount":"3","wht_amount":"1","payment_date":"2026-01-02","currency":"USD","notes":"audit"}`
	expect(list(id, good), 200, "[]")
	expect(post(id, token(bUser, b), partial), 401, "")
	expect(post(id, token(bUser, a), partial), 403, "")
	for _, id := range []string{invoiceB, missing} {
		expect(post(id, good, partial), 404, "invoice not found")
		expect(list(id, good), 404, "invoice not found")
	}
	expect(post(id, good, `{"amount":"0","payment_date":"2026-01-01"}`), 400, "both be zero")
	expect(post(id, good, `{"amount":"10","wht_amount":"1","payment_date":"2026-01-01"}`), 400, "exceeds balance")
	expect(post(id, good, `{"amount":"0","wht_amount":"3","payment_date":"2026-01-01"}`), 400, "outstanding withholding")
	expect(post(id, good, `{"amount":"0.00001","payment_date":"2026-01-01"}`), 422, "")
	seedItem := func(owner string) string {
		t.Helper()
		var id string
		err := pool.QueryRow(ctx, `INSERT INTO items(business_id,item_id,name,item_type,is_hosting)
			VALUES ($1,gen_random_uuid(),'Payment hosting','SERVICE',true) RETURNING item_id::text`, owner).Scan(&id)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	itemA, itemB := seedItem(a), seedItem(b)
	exec(`INSERT INTO invoice_line_items(business_id,line_item_id,invoice_id,item_id,position,description,quantity,unit_price,amount)
		VALUES ($1,gen_random_uuid(),$2,$3,0,'hosting',1,10,10), ($1,gen_random_uuid(),$2,$3,1,'duplicate hosting',1,0,0)`, a, id, itemA)
	w := post(id, good, partial)
	expect(w, 201, "")
	var p map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil || p["amount"] != "3.0000" || p["wht_amount"] != "1.0000" || p["currency"] != "SGD" || p["customer_id"] != customerA || p["business_id"] != nil {
		t.Fatalf("payment response: %s / %v", w.Body, err)
	}
	check := func(invoice, status, balance, wht string, payments, intents int) {
		t.Helper()
		var gotStatus, gotBalance, gotWht string
		var gotPayments, gotIntents int
		err := pool.QueryRow(ctx, `SELECT i.status,i.balance_due::text,i.wht_booked::text,
			(SELECT count(*) FROM payments p WHERE p.business_id=i.business_id AND p.invoice_id=i.invoice_id),
			(SELECT count(*) FROM hosting_restore_intents h WHERE h.business_id=i.business_id AND h.invoice_id=i.invoice_id)
			FROM invoices i WHERE i.business_id=$1 AND i.invoice_id=$2`, a, invoice).Scan(&gotStatus, &gotBalance, &gotWht, &gotPayments, &gotIntents)
		if err != nil || gotStatus != status || gotBalance != balance || gotWht != wht || gotPayments != payments || gotIntents != intents {
			t.Fatalf("invoice state: %s %s %s %d %d / %v", gotStatus, gotBalance, gotWht, gotPayments, gotIntents, err)
		}
	}
	check(id, "PARTIAL", "6.0000", "1.0000", 1, 0)
	expect(list(id, good), 200, p["payment_id"].(string))
	var audit int
	err := pool.QueryRow(ctx, `SELECT count(*) FROM reconciliation_log WHERE business_id=$1 AND payment_id=$2 AND actor_user_id=$3 AND reasons=ARRAY['manual-entry','audit']`, a, p["payment_id"], aUser).Scan(&audit)
	if err != nil || audit != 1 {
		t.Fatalf("payment audit: %d / %v", audit, err)
	}
	foreignKeyFails := func(query string, args ...any) {
		t.Helper()
		_, err := pool.Exec(ctx, query, args...)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
			t.Fatalf("cross-business reference: %v", err)
		}
	}
	foreignKeyFails(`UPDATE invoices SET customer_id=$3 WHERE business_id=$1 AND invoice_id=$2`, a, id, customerB)
	foreignKeyFails(`UPDATE invoice_line_items SET item_id=$3 WHERE business_id=$1 AND invoice_id=$2`, a, id, itemB)
	foreignKeyFails(`UPDATE payments SET invoice_id=$3 WHERE business_id=$1 AND payment_id=$2`, a, p["payment_id"], invoiceB)
	foreignKeyFails(`UPDATE payments SET customer_id=$3 WHERE business_id=$1 AND payment_id=$2`, a, p["payment_id"], customerB)
	foreignKeyFails(`UPDATE reconciliation_log SET actor_user_id=$3 WHERE business_id=$1 AND payment_id=$2`, a, p["payment_id"], bUser)
	foreignKeyFails(`INSERT INTO hosting_restore_intents(business_id,invoice_id,item_id) VALUES ($1,$2,$3)`, a, id, itemB)

	const settle = `{"amount":"5","wht_amount":"1","payment_date":"2026-01-03","notes":"force payment rollback"}`
	for _, tc := range []struct {
		table, constraint, update, row, correct, foreign string
		check                                            func()
	}{
		{"invoices", "invoices_customer_id_fkey",
			`UPDATE invoices SET customer_id=$3 WHERE business_id=$1 AND invoice_id=$2`, id, customerA, customerB, func() {
				expect(post(id, good, settle), 404, "invoice not found")
				expect(list(id, good), 404, "invoice not found")
			}},
		{"invoice_line_items", "invoice_line_items_item_id_fkey",
			`UPDATE invoice_line_items SET item_id=$3 WHERE business_id=$1 AND invoice_id=$2`, id, itemA, itemB, func() {
				expect(post(id, good, settle), 500, "internal server error")
			}},
		{"payments", "payments_customer_id_fkey",
			`UPDATE payments SET customer_id=$3 WHERE business_id=$1 AND payment_id=$2`, p["payment_id"].(string), customerA, customerB, func() {
				expect(list(id, good), 200, "[]")
			}},
	} {
		func() {
			var def string
			if err := pool.QueryRow(ctx, `SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid=$1::regclass AND conname=$2`,
				tc.table, tc.constraint).Scan(&def); err != nil {
				t.Fatal(err)
			}
			exec("ALTER TABLE " + tc.table + " DROP CONSTRAINT " + tc.constraint)
			defer func() {
				exec(tc.update, a, tc.row, tc.correct)
				exec("ALTER TABLE " + tc.table + " ADD CONSTRAINT " + tc.constraint + " " + def)
			}()
			for _, ref := range []string{tc.foreign, missing} {
				exec(tc.update, a, tc.row, ref)
				tc.check()
			}
		}()
	}
	check(id, "PARTIAL", "6.0000", "1.0000", 1, 0)

	exec(`ALTER TABLE reconciliation_log ADD CONSTRAINT test_scoped_invoice_payment_rollback
		CHECK (NOT (reasons @> ARRAY['force payment rollback'])) NOT VALID`)
	expect(post(id, good, settle), 500, "internal server error")
	exec(`ALTER TABLE reconciliation_log DROP CONSTRAINT test_scoped_invoice_payment_rollback`)
	check(id, "PARTIAL", "6.0000", "1.0000", 1, 0)
	var unchanged bool
	if err := pool.QueryRow(ctx, `SELECT hosting_last_paid_at IS NULL FROM items WHERE business_id=$1 AND item_id=$2`, a, itemA).Scan(&unchanged); err != nil || !unchanged {
		t.Fatalf("rollback retained hosting timestamp: %v / %v", unchanged, err)
	}

	// Two administrators avoid serializing the requests on the user row.
	var secondAdmin, target string
	if err := pool.QueryRow(ctx, `INSERT INTO users(business_id,user_id,email,password_hash,role)
		VALUES ($1,gen_random_uuid(),'second-payment-admin@example.test','x','admin') RETURNING user_id::text`, a).Scan(&secondAdmin); err != nil {
		t.Fatal(err)
	}
	// Restore the fixture's single administrator assumption after reassigning the
	// test log, in case the second request acquired the invoice first.
	defer func() {
		exec(`UPDATE reconciliation_log SET actor_user_id=$3 WHERE business_id=$1 AND actor_user_id=$2`, a, secondAdmin, aUser)
		exec(`DELETE FROM users WHERE business_id=$1 AND user_id=$2`, a, secondAdmin)
	}()
	if err := pool.QueryRow(ctx, `INSERT INTO cloudflare_targets(business_id,target_id,item_id,zone_id,record_id,record_name,record_type,live_content,maintenance_content,provider_status)
		VALUES ($1,gen_random_uuid(),$2,'payment-zone','payment-record','payment.example.test','A','192.0.2.1','192.0.2.2','SUSPENDED') RETURNING target_id::text`, a, itemA).Scan(&target); err != nil {
		t.Fatal(err)
	}
	defer exec(`DELETE FROM cloudflare_targets WHERE business_id=$1 AND target_id=$2`, a, target)

	lock, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback(ctx)
	if _, err := lock.Exec(ctx, `SELECT target_id FROM cloudflare_targets WHERE business_id=$1 AND target_id=$2 FOR UPDATE`, a, target); err != nil {
		t.Fatal(err)
	}
	waitForLock := func(pattern string) {
		t.Helper()
		for {
			var waiting bool
			err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database()
				AND wait_event_type='Lock' AND query LIKE $1)`, pattern).Scan(&waiting)
			if err != nil {
				t.Fatal(err)
			}
			if waiting {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	result := make(chan *httptest.ResponseRecorder, 2)
	go func() { result <- post(id, good, settle) }()
	waitForLock("%FROM cloudflare_targets WHERE business_id=%FOR UPDATE%")
	secondSession := token(secondAdmin, a)
	go func() { result <- post(id, secondSession, settle) }()
	waitForLock("%FROM businesses%FOR NO KEY UPDATE%")
	check(id, "PARTIAL", "6.0000", "1.0000", 1, 0)
	if err := lock.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	codes := map[int]int{}
	for range 2 {
		select {
		case w := <-result:
			codes[w.Code]++
			if w.Code == 400 {
				expect(w, 400, "exceeds balance")
			} else {
				expect(w, 201, "")
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if codes[201] != 1 || codes[400] != 1 {
		t.Fatalf("concurrent payments: %v", codes)
	}
	check(id, "PAID", "0.0000", "2.0000", 2, 1)
	var queueSafe bool
	err = pool.QueryRow(ctx, `SELECT
		EXISTS (SELECT FROM hosting_restore_intents WHERE business_id=$1 AND invoice_id=$3 AND item_id=$4 AND lease_token IS NULL AND attempts=0)
		AND EXISTS (SELECT FROM items WHERE business_id=$1 AND item_id=$4 AND hosting_last_paid_at IS NOT NULL)
		AND EXISTS (SELECT FROM items WHERE business_id=$2 AND item_id=$5 AND hosting_last_paid_at IS NULL)
		AND EXISTS (SELECT FROM cloudflare_targets WHERE business_id=$1 AND target_id=$6 AND provider_status='SUSPENDED')
		AND NOT EXISTS (SELECT FROM journal_entries WHERE business_id=$1 AND source_id IN
			(SELECT payment_id FROM payments WHERE business_id=$1 AND invoice_id=$3))`, a, b, id, itemA, itemB, target).Scan(&queueSafe)
	if err != nil || !queueSafe {
		t.Fatalf("unsafe hosting or ledger state: %v / %v", queueSafe, err)
	}
	w = list(id, good)
	expect(w, 200, "")
	var listed []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &listed); err != nil || len(listed) != 2 ||
		listed[0]["payment_date"] != "2026-01-03" || listed[1]["payment_id"] != p["payment_id"] {
		t.Fatalf("newest cleared payment list: %s / %v", w.Body, err)
	}

}
