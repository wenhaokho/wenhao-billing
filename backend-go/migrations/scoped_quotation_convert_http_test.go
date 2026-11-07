package migrations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testScopedQuotationConvert(t *testing.T, pool *pgxpool.Pool, a, b, bUser, quoteB, customerA, customerB, projectA, projectB, itemA, itemB, missing, good string,
	token func(string, string) string, request func(string, string, string, string) *httptest.ResponseRecorder,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	scanString := func(sql string, args ...any) string {
		t.Helper()
		var value string
		if err := pool.QueryRow(ctx, sql, args...).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	snapshot := func(owner string) string {
		return scanString(`SELECT jsonb_build_array(
   (SELECT jsonb_agg(to_jsonb(q) ORDER BY quotation_id) FROM quotations q WHERE business_id=$1),
   (SELECT jsonb_agg(to_jsonb(i) ORDER BY invoice_id) FROM invoices i WHERE business_id=$1),
   (SELECT jsonb_agg(to_jsonb(l) ORDER BY line_item_id) FROM invoice_line_items l WHERE business_id=$1)
  )::text`, owner)
	}
	expect := func(w *httptest.ResponseRecorder, status int) map[string]any {
		t.Helper()
		var out map[string]any
		if w.Code != status || json.Unmarshal(w.Body.Bytes(), &out) != nil {
			t.Fatalf("got %d %s, want %d and JSON", w.Code, w.Body, status)
		}
		return out
	}
	convert := func(quote, session string) *httptest.ResponseRecorder {
		return request("POST", "/api/v1/quotations/"+quote+"/convert-to-invoice", session,
			fmt.Sprintf(`{"issue_date":"2025-01-01","due_in_days":"3.0","business_id":%q}`, b))
	}
	seedQuote := func() string {
		id := scanString(`INSERT INTO quotations(quotation_id,business_id,customer_id,project_id,currency,subtotal,discount_type,discount_value,amount,status,payment_terms)
   VALUES(gen_random_uuid(),$1,$2,$3,'USD',110,'AMOUNT',10,100,'DRAFT','Net ３０') RETURNING quotation_id::text`, a, customerA, projectA)
		exec(`INSERT INTO quotation_line_items(line_item_id,business_id,quotation_id,item_id,description,quantity,unit_price,amount,position)
   VALUES(gen_random_uuid(),$1,$2,$3,'conversion work',2,55,110,7)`, a, id, itemA)
		return id
	}
	secondAdmin := scanString(`INSERT INTO users(user_id,business_id,email,password_hash,role)
 VALUES(gen_random_uuid(),$1,'convert-admin@example.com','unused','admin') RETURNING user_id::text`, a)
	secondToken := token(secondAdmin, a)
	exec(`UPDATE customers SET default_wht_type='PPH23',default_wht_rate=2,default_wht_gross_up=true WHERE business_id=$1 AND customer_id=$2`, a, customerA)
	prefix := fmt.Sprintf("WH%d", time.Now().Year())
	invoiceB := scanString(`INSERT INTO invoices(invoice_id,business_id,customer_id,invoice_type,invoice_number,currency,amount,balance_due,status)
 VALUES(gen_random_uuid(),$1,$2,'MILESTONE',$3,'USD',10,10,'DRAFT') RETURNING invoice_id::text`, b, customerB, prefix+"999999999999999")
	foreignBefore := snapshot(b)
	quote := seedQuote()
	before := snapshot(a)
	f, m := convert(quoteB, good), convert(missing, good)
	expect(f, 400)
	expect(m, 400)
	if f.Body.String() != m.Body.String() {
		t.Fatalf("foreign and absent quote responses differ: %s / %s", f.Body, m.Body)
	}
	expect(convert(quote, token(bUser, b)), 401)
	expect(convert(quote, token(bUser, a)), 403)
	if snapshot(a) != before {
		t.Fatal("denied conversion changed business A")
	}

	// Temporarily remove each FK in this disposable database to exercise the
	// route's checks against inconsistent stored references.
	for _, ref := range []struct {
		table, column, foreign string
		original               any
		status                 int
	}{
		{"quotations", "customer_id", customerB, customerA, 500},
		{"quotations", "project_id", projectB, projectA, 500},
		{"quotation_line_items", "item_id", itemB, itemA, 500},
		{"quotations", "converted_invoice_id", invoiceB, nil, 400},
	} {
		constraint := ref.table + "_" + ref.column + "_fkey"
		definition := scanString(`SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid=$1::regclass AND conname=$2`, ref.table, constraint)
		alter := "ALTER TABLE " + ref.table
		update := "UPDATE " + ref.table + " SET " + ref.column + "=$3 WHERE business_id=$1 AND quotation_id=$2"
		exec(alter + " DROP CONSTRAINT " + constraint)
		failed := make([]string, 0, 2)
		for _, id := range []string{ref.foreign, missing} {
			exec(update, a, quote, id)
			state := snapshot(a)
			w := convert(quote, good)
			expect(w, ref.status)
			failed = append(failed, w.Body.String())
			if snapshot(a) != state {
				t.Fatal("conversion with an invalid reference wrote data")
			}
		}
		if failed[0] != failed[1] {
			t.Fatalf("foreign and absent reference responses differ: %v", failed)
		}
		exec(update, a, quote, ref.original)
		exec(alter + " ADD CONSTRAINT " + constraint + " " + definition)
	}
	exec(`UPDATE quotation_line_items SET amount='NaN' WHERE business_id=$1 AND quotation_id=$2`, a, quote)
	before = snapshot(a)
	expect(convert(quote, good), 500)
	if snapshot(a) != before {
		t.Fatal("failed response serialization committed conversion")
	}
	exec(`UPDATE quotation_line_items SET amount=110 WHERE business_id=$1 AND quotation_id=$2`, a, quote)
	results := make(chan *httptest.ResponseRecorder, 2)
	start := make(chan struct{})
	for _, session := range []string{good, secondToken} {
		go func() { <-start; results <- convert(quote, session) }()
	}
	close(start)
	first, second := <-results, <-results
	if first.Code != 201 {
		first, second = second, first
	}
	out := expect(first, 201)
	expect(second, 400)
	if out["source_quote_id"] != quote || out["project_id"] != projectA || out["status"] != "DRAFT" ||
		out["subtotal"] != "110.0000" || out["wht_type"] != "PPH23" || out["wht_rate"] != "2.0000" ||
		out["wht_amount"] != "2.0400" || out["amount"] != "102.0400" || out["due_date"] != "2025-01-31" ||
		out["business_id"] != nil || out["invoice_number"] != prefix+"0001" {
		t.Fatalf("conversion invoice: %v", out)
	}
	line := out["line_items"].([]any)[0].(map[string]any)
	if line["item_id"] != itemA || line["position"] != float64(7) || line["amount"] != "110.0000" || line["business_id"] != nil {
		t.Fatalf("conversion line: %v", line)
	}
	if linked := scanString(`SELECT i.invoice_id::text FROM invoices i JOIN quotations q
  ON q.business_id=i.business_id AND q.quotation_id=i.source_quote_id AND q.converted_invoice_id=i.invoice_id
  JOIN invoice_line_items l ON l.business_id=i.business_id AND l.invoice_id=i.invoice_id
  WHERE q.business_id=$1 AND q.quotation_id=$2 AND q.status='INVOICED' AND l.item_id=$3`, a, quote, itemA); linked != out["invoice_id"] {
		t.Fatalf("persisted conversion link: %s, response: %v", linked, out)
	}
	if count := scanString(`SELECT count(*)::text FROM invoices WHERE business_id=$1 AND source_quote_id=$2`, a, quote); count != "1" {
		t.Fatalf("duplicate conversion wrote %s invoices", count)
	}

	waitFor := func(query string) {
		t.Helper()
		wait, stop := context.WithTimeout(ctx, 4*time.Second)
		defer stop()
		for {
			var blocked bool
			if err := pool.QueryRow(wait, `SELECT EXISTS (SELECT FROM pg_stat_activity WHERE datname=current_database()
   AND wait_event_type='Lock' AND query LIKE $1)`, query).Scan(&blocked); err != nil {
				t.Fatalf("did not observe conversion waiting for %s: %v", query, err)
			}
			if blocked {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	raceQuote := seedQuote()
	projectTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer projectTx.Rollback(ctx)
	if _, err = projectTx.Exec(ctx, `SELECT project_id FROM projects WHERE business_id=$1 AND project_id=$2 FOR UPDATE`, a, projectA); err != nil {
		t.Fatal(err)
	}
	go func() { results <- convert(raceQuote, good) }()
	waitFor("%SELECT project_id FROM projects%FOR KEY SHARE%")
	deleteCtx, stopDelete := context.WithTimeout(ctx, 3*time.Second)
	defer stopDelete()
	_, err = projectTx.Exec(deleteCtx, `DELETE FROM projects WHERE business_id=$1 AND project_id=$2`, a, projectA)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23503" {
		t.Fatalf("project deletion should fail without deadlocking: %v", err)
	}
	if err = projectTx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case w := <-results:
		expect(w, 201)
	case <-ctx.Done():
		t.Fatal("conversion did not resume after project deletion rolled back")
	}

	raceQuote = seedQuote()
	writer, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Rollback(ctx)
	if _, err = writer.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('invoice-number:' || $1::uuid::text,0))`, a); err != nil {
		t.Fatal(err)
	}
	go func() { results <- convert(raceQuote, secondToken) }()
	waitFor("%LockInvoiceNumberAllocation%")
	writerCtx, stopWriter := context.WithTimeout(ctx, 3*time.Second)
	defer stopWriter()
	var writerInvoice string
	err = writer.QueryRow(writerCtx, `INSERT INTO invoices(invoice_id,business_id,customer_id,project_id,invoice_type,invoice_number,currency,amount,balance_due,status)
 VALUES(gen_random_uuid(),$1,$2,$3,'MILESTONE',$4,'USD',1,1,'DRAFT') RETURNING invoice_id::text`, a, customerA, projectA, prefix+"0009").Scan(&writerInvoice)
	if err != nil {
		t.Fatalf("number allocator's invoice insert blocked: %v", err)
	}
	if _, err = writer.Exec(writerCtx, `INSERT INTO invoice_line_items(line_item_id,business_id,invoice_id,item_id,description,quantity,unit_price,amount,position)
 VALUES(gen_random_uuid(),$1,$2,$3,'writer',1,1,1,0)`, a, writerInvoice, itemA); err != nil {
		t.Fatalf("number allocator's line insert blocked: %v", err)
	}
	if err = writer.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case w := <-results:
		if expect(w, 201)["invoice_number"] != prefix+"0010" {
			t.Fatalf("conversion missed committed number: %s", w.Body)
		}
	case <-ctx.Done():
		t.Fatal("conversion did not resume after allocation lock released")
	}
	if snapshot(b) != foreignBefore {
		t.Fatal("conversion changed foreign quotations or invoices")
	}
}
