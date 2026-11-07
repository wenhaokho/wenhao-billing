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

func testScopedInvoiceWrites(t *testing.T, pool *pgxpool.Pool, a, b, aUser, bUser, invoiceB, customerA, customerB, projectA, projectB, itemA, itemB, missing, good string,
	token func(string, string) string, request func(string, string, string, string) *httptest.ResponseRecorder,
) {
	t.Helper()
	const path = "/api/v1/invoices"
	ctx := context.Background()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := func(owner string) string {
		t.Helper()
		var value string
		err := pool.QueryRow(ctx, `SELECT jsonb_build_array(
   (SELECT jsonb_agg(to_jsonb(i) ORDER BY invoice_id) FROM invoices i WHERE business_id=$1),
   (SELECT jsonb_agg(to_jsonb(l) ORDER BY line_item_id) FROM invoice_line_items l WHERE business_id=$1))::text`, owner).Scan(&value)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	expect := func(w *httptest.ResponseRecorder, code int) map[string]any {
		t.Helper()
		var result map[string]any
		if w.Code != code || json.Unmarshal(w.Body.Bytes(), &result) != nil {
			t.Fatalf("got %d %s, want %d with JSON", w.Code, w.Body, code)
		}
		return result
	}
	sameFailure := func(method, foreignPath, missingPath, foreignBody, missingBody string) {
		t.Helper()
		f, m := request(method, foreignPath, good, foreignBody), request(method, missingPath, good, missingBody)
		expect(f, 400)
		expect(m, 400)
		if f.Body.String() != m.Body.String() {
			t.Fatalf("foreign and missing responses differ: %s / %s", f.Body, m.Body)
		}
	}
	lines := func(item string) string {
		return fmt.Sprintf(`[{"item_id":%q,"description":"work","quantity":"1.1250","unit_price":"2.5001","business_id":%q}]`, item, b)
	}
	create := func(customer, project, item string) string {
		return fmt.Sprintf(`{"customer_id":%q,"project_id":%q,"currency":"USD","discount_type":"AMOUNT","discount_value":"0.3126","issue_date":"2026-01-01T00:00:00","payment_terms":"NET 7","line_items":%s,"business_id":%q}`, customer, project, lines(item), b)
	}
	original, foreignOriginal := snapshot(a), snapshot(b)
	var numberBefore *string
	if err := pool.QueryRow(ctx, `SELECT invoice_number FROM invoices WHERE business_id=$1 AND invoice_id=$2`, b, invoiceB).Scan(&numberBefore); err != nil {
		t.Fatal(err)
	}
	var typBefore string
	var rateBefore *string
	var grossBefore bool
	if err := pool.QueryRow(ctx, `SELECT default_wht_type,default_wht_rate::text,default_wht_gross_up FROM customers WHERE business_id=$1 AND customer_id=$2`, a, customerA).Scan(&typBefore, &rateBefore, &grossBefore); err != nil {
		t.Fatal(err)
	}
	prefix := fmt.Sprintf("WH%d", time.Now().Year())
	shared := prefix + "999999999999999"
	exec(`UPDATE invoices SET invoice_number=$3 WHERE business_id=$1 AND invoice_id=$2`, b, invoiceB, shared)
	exec(`UPDATE customers SET default_wht_type='PPH23',default_wht_rate=2,default_wht_gross_up=true WHERE business_id=$1 AND customer_id=$2`, a, customerA)
	foreignBefore := snapshot(b)
	body := create(customerA, projectA, itemA)
	expect(request("POST", path, token(bUser, b), body), 401)
	expect(request("POST", path, token(bUser, a), body), 403)
	sameFailure("POST", path, path, create(customerB, projectA, itemA), create(missing, projectA, itemA))
	sameFailure("POST", path, path, create(customerA, projectB, itemA), create(customerA, missing, itemA))
	sameFailure("POST", path, path, create(customerA, projectA, itemB), create(customerA, projectA, missing))
	rejected := strings.TrimSuffix(lines(itemA), "]") + `,{"description":"reject scoped invoice line","quantity":1,"unit_price":1}]`
	exec(`ALTER TABLE invoice_line_items ADD CONSTRAINT test_scoped_invoice_write CHECK (description <> 'reject scoped invoice line')`)
	expect(request("POST", path, good, strings.Replace(body, lines(itemA), rejected, 1)), 500)
	if snapshot(a) != original {
		t.Fatal("failed create persisted invoice or lines")
	}
	created := expect(request("POST", path, good, body), 201)
	id := created["invoice_id"].(string)
	number := created["invoice_number"].(string)
	target := path + "/" + id
	if !strings.HasPrefix(number, prefix) || len(number) >= len(shared) || created["business_id"] != nil ||
		created["subtotal"] != "2.8126" || created["wht_base"] != "2.5000" || created["wht_amount"] != "0.0500" ||
		created["amount"] != "2.5500" || created["balance_due"] != "2.5500" || created["due_date"] != "2026-01-08" || created["status"] != "DRAFT" {
		t.Fatalf("created invoice: %v", created)
	}
	line := created["line_items"].([]any)[0].(map[string]any)
	if line["item_id"] != itemA || line["amount"] != "2.8126" || line["business_id"] != nil {
		t.Fatalf("created invoice line: %v", line)
	}
	var owned int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM invoices i JOIN invoice_line_items l USING (business_id,invoice_id) WHERE i.business_id=$1 AND i.invoice_id=$2 AND i.customer_id=$3 AND i.project_id=$4 AND l.item_id=$5`, a, id, customerA, projectA, itemA).Scan(&owned); err != nil || owned != 1 {
		t.Fatalf("persisted ownership: %d / %v", owned, err)
	}
	beforePatch := snapshot(a)
	sameFailure("PATCH", path+"/"+invoiceB, path+"/"+missing, "{}", "{}")
	sameFailure("PATCH", target, target, fmt.Sprintf(`{"customer_id":%q}`, customerB), fmt.Sprintf(`{"customer_id":%q}`, missing))
	sameFailure("PATCH", target, target, fmt.Sprintf(`{"project_id":%q}`, projectB), fmt.Sprintf(`{"project_id":%q}`, missing))
	sameFailure("PATCH", target, target, `{"line_items":`+lines(itemB)+`}`, `{"line_items":`+lines(missing)+`}`)
	expect(request("PATCH", target, good, `{"notes":"rollback","line_items":`+rejected+`}`), 500)
	exec(`ALTER TABLE invoice_line_items DROP CONSTRAINT test_scoped_invoice_write`)
	if snapshot(a) != beforePatch {
		t.Fatal("failed replacement changed owned invoice or lines")
	}
	exec(`UPDATE invoice_line_items SET amount='NaN' WHERE business_id=$1 AND invoice_id=$2`, a, id)
	beforeResponse := snapshot(a)
	expect(request("PATCH", target, good, `{"notes":"must roll back"}`), 500)
	if snapshot(a) != beforeResponse {
		t.Fatal("response conversion failure committed mutation")
	}
	updated := expect(request("PATCH", target, good, fmt.Sprintf(`{"invoice_number":%q,"discount_value":null,"line_items":%s}`, shared, lines(itemA))), 200)
	if updated["invoice_number"] != shared || updated["amount"] != "2.8700" || updated["wht_amount"] != "0.0574" || updated["balance_due"] != "2.8700" {
		t.Fatalf("updated invoice: %v", updated)
	}
	var secondAdmin string
	if err := pool.QueryRow(ctx, `INSERT INTO users(user_id,business_id,email,password_hash,role)
 VALUES(gen_random_uuid(),$1,'invoice-writes-admin@example.com','unused','admin') RETURNING user_id::text`, a).Scan(&secondAdmin); err != nil {
		t.Fatal(err)
	}
	secondToken := token(secondAdmin, a)
	results := make(chan *httptest.ResponseRecorder, 2)
	concurrent := func(method1, target1, body1, body2 string) (*httptest.ResponseRecorder, *httptest.ResponseRecorder) {
		start := make(chan struct{})
		go func() { <-start; results <- request(method1, target1, good, body1) }()
		go func() { <-start; results <- request("POST", path, secondToken, body2) }()
		close(start)
		return <-results, <-results
	}
	noRefs := `{"currency":"USD","line_items":[{"description":"concurrent invoice","quantity":1,"unit_price":1}]}`
	first, second := concurrent("POST", path, noRefs, noRefs)
	row1, row2 := expect(first, 201), expect(second, 201)
	if row1["invoice_number"] == row2["invoice_number"] {
		t.Fatal("allocated the same number twice")
	}
	raceNumber := "Scoped-invoice-number-race"
	first, second = concurrent("PATCH", target, fmt.Sprintf(`{"invoice_number":%q}`, raceNumber),
		strings.TrimSuffix(noRefs, "}")+fmt.Sprintf(`,"invoice_number":%q}`, raceNumber))
	if first.Code == 409 {
		first, second = second, first
	}
	if first.Code != 200 && first.Code != 201 {
		t.Fatalf("number race winner: %d %s", first.Code, first.Body)
	}
	winner := expect(first, first.Code)
	expect(second, 409)
	beforeDuplicate := snapshot(a)
	expect(request("PATCH", path+"/"+row1["invoice_id"].(string), good, fmt.Sprintf(`{"invoice_number":%q,"line_items":%s}`, raceNumber, lines(itemA))), 409)
	if snapshot(a) != beforeDuplicate {
		t.Fatal("duplicate number committed line replacement")
	}

	waitForWrite := func(update, pattern string, want int, args ...any) {
		t.Helper()
		lockCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		tx, err := pool.Begin(lockCtx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if _, err := tx.Exec(lockCtx, update, args...); err != nil {
			t.Fatal(err)
		}
		go func() { results <- request("PATCH", target, good, `{"notes":"stale write"}`) }()
		for {
			var blocked bool
			err := pool.QueryRow(lockCtx, `SELECT EXISTS(SELECT FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE $1)`, pattern).Scan(&blocked)
			if err != nil {
				t.Fatalf("write did not wait for %s: %v", pattern, err)
			}
			if blocked {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if err := tx.Commit(lockCtx); err != nil {
			t.Fatal(err)
		}
		select {
		case w := <-results:
			expect(w, want)
		case <-lockCtx.Done():
			t.Fatal("write did not resume after lock release")
		}
	}
	waitForWrite(`UPDATE users SET role='viewer' WHERE business_id=$1 AND user_id=$2`, "%FROM users WHERE%FOR UPDATE%", 403, a, aUser)
	exec(`UPDATE users SET role='admin' WHERE business_id=$1 AND user_id=$2`, a, aUser)
	waitForWrite(`UPDATE invoices SET status='SENT' WHERE business_id=$1 AND invoice_id=$2`, "%FROM invoices WHERE business_id=%FOR UPDATE%", 400, a, id)
	exec(`UPDATE invoices SET status='DRAFT' WHERE business_id=$1 AND invoice_id=$2`, a, id)
	if snapshot(a) != beforeDuplicate || snapshot(b) != foreignBefore {
		t.Fatal("rejected or stale writes changed invoice data")
	}
	// Project PATCH locks the project first and validates a changed customer
	// through its foreign key. Pause it immediately before that FK check.
	var raceCustomer, raceProject string
	if err := pool.QueryRow(ctx, `INSERT INTO customers(customer_id,business_id,name)
 VALUES(gen_random_uuid(),$1,'Invoice project race customer') RETURNING customer_id::text`, a).Scan(&raceCustomer); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO projects(project_id,business_id,customer_id,code,name,currency,status,active)
 VALUES(gen_random_uuid(),$1,$2,'INVOICE-LOCK-TEST','Invoice lock test','USD','ACTIVE',true) RETURNING project_id::text`, a, customerA).Scan(&raceProject); err != nil {
		t.Fatal(err)
	}
	exec(`CREATE FUNCTION test_invoice_project_lock() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN PERFORM pg_advisory_xact_lock(10390822); RETURN NEW; END $$`)
	exec(`CREATE TRIGGER test_invoice_project_lock BEFORE UPDATE ON projects FOR EACH ROW
 WHEN (OLD.code = 'INVOICE-LOCK-TEST') EXECUTE FUNCTION test_invoice_project_lock()`)
	gateCtx, cancelGate := context.WithTimeout(ctx, 5*time.Second)
	defer cancelGate()
	gate, err := pool.Begin(gateCtx)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Rollback(ctx)
	if _, err := gate.Exec(gateCtx, `SELECT pg_advisory_xact_lock(10390822)`); err != nil {
		t.Fatal(err)
	}
	waitForBlocked := func(pattern string) {
		t.Helper()
		for {
			var blocked bool
			err := pool.QueryRow(gateCtx, `SELECT EXISTS(SELECT FROM pg_stat_activity
   WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE $1)`, pattern).Scan(&blocked)
			if err != nil {
				t.Fatalf("endpoint did not wait at %s: %v", pattern, err)
			}
			if blocked {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	projectResult := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		projectResult <- request("PATCH", "/api/v1/projects/"+raceProject, secondToken, fmt.Sprintf(`{"customer_id":%q}`, raceCustomer))
	}()
	waitForBlocked("%UPDATE projects SET customer_id=%")
	go func() {
		results <- request("POST", path, good, create(raceCustomer, raceProject, itemA))
	}()
	waitForBlocked("%FROM projects WHERE business_id=%FOR%")
	if err := gate.Commit(gateCtx); err != nil {
		t.Fatal(err)
	}
	getResult := func(ch <-chan *httptest.ResponseRecorder) *httptest.ResponseRecorder {
		t.Helper()
		select {
		case w := <-ch:
			return w
		case <-gateCtx.Done():
			t.Fatal("project and invoice writes did not finish")
			return nil
		}
	}
	expect(getResult(projectResult), 200)
	raceInvoice := expect(getResult(results), 201)["invoice_id"]
	exec(`DROP TRIGGER test_invoice_project_lock ON projects`)
	exec(`DROP FUNCTION test_invoice_project_lock()`)
	exec(`DELETE FROM invoices WHERE business_id=$1 AND invoice_id=$2`, a, raceInvoice)
	exec(`DELETE FROM projects WHERE business_id=$1 AND project_id=$2`, a, raceProject)
	exec(`DELETE FROM customers WHERE business_id=$1 AND customer_id=$2`, a, raceCustomer)
	exec(`DELETE FROM invoices WHERE business_id=$1 AND invoice_id IN ($2,$3,$4,$5)`, a, id, row1["invoice_id"], row2["invoice_id"], winner["invoice_id"])
	exec(`DELETE FROM users WHERE business_id=$1 AND user_id=$2`, a, secondAdmin)
	exec(`UPDATE invoices SET invoice_number=$3 WHERE business_id=$1 AND invoice_id=$2`, b, invoiceB, numberBefore)
	exec(`UPDATE customers SET default_wht_type=$3,default_wht_rate=$4::text::numeric,default_wht_gross_up=$5 WHERE business_id=$1 AND customer_id=$2`, a, customerA, typBefore, rateBefore, grossBefore)
	if snapshot(a) != original || snapshot(b) != foreignOriginal {
		t.Fatal("invoice fixture restoration changed existing rows")
	}
}
