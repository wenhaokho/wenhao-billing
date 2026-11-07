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

func testScopedQuotationWrites(t *testing.T, pool *pgxpool.Pool, a, b, bUser, quoteB, customerA, customerB, projectA, projectB, itemA, itemB, missing, good string,
	token func(string, string) string, request func(string, string, string, string) *httptest.ResponseRecorder,
) {
	t.Helper()
	ctx := context.Background()
	const path = "/api/v1/quotations"
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
			(SELECT jsonb_agg(to_jsonb(q) ORDER BY quotation_id) FROM quotations q WHERE business_id=$1),
			(SELECT jsonb_agg(to_jsonb(l) ORDER BY line_item_id) FROM quotation_line_items l WHERE business_id=$1)
		)::text`, owner).Scan(&value)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	expect := func(w *httptest.ResponseRecorder, code int) map[string]any {
		t.Helper()
		var obj map[string]any
		if w.Code != code || json.Unmarshal(w.Body.Bytes(), &obj) != nil {
			t.Fatalf("got %d %s, want %d and JSON", w.Code, w.Body.String(), code)
		}
		return obj
	}
	lines := func(item string) string {
		return fmt.Sprintf(`[{"item_id":%q,"description":"work","quantity":"1.1250","unit_price":"2.5001","business_id":%q}]`, item, b)
	}
	create := func(customer, project, item string) string {
		return fmt.Sprintf(`{"customer_id":%q,"project_id":%q,"currency":"IDR","discount_type":"AMOUNT","discount_value":"0.3126","issue_date":"2026-01-01","line_items":%s,"business_id":%q}`, customer, project, lines(item), b)
	}
	foreignSnapshot := snapshot(b)
	original := snapshot(a)
	body := create(customerA, projectA, itemA)
	badPosition := func(input, position string) string {
		return strings.Replace(input, `"description":"work"`, `"position":`+position+`,"description":"work"`, 1)
	}
	checkPositions := func(method, target, body string) {
		t.Helper()
		for _, tc := range []struct{ value, typ string }{{"null", "int_type"}, {`"1e2"`, "int_parsing"}} {
			result := request(method, target, good, badPosition(body, tc.value))
			expect(result, 422)
			if !strings.Contains(result.Body.String(), `"type":"`+tc.typ+`"`) ||
				!strings.Contains(result.Body.String(), `["body","line_items",0,"position"]`) {
				t.Fatalf("position %s: %s", tc.value, result.Body.String())
			}
		}
	}
	checkPositions("POST", path, body)
	expect(request("POST", path, token(bUser, b), body), 401)
	expect(request("POST", path, token(bUser, a), body), 403)
	expect(request("POST", path, token(bUser, ""), body), 401)
	sameFailure := func(method, target, foreignBody, missingBody string) {
		t.Helper()
		f := request(method, target, good, foreignBody)
		m := request(method, target, good, missingBody)
		expect(f, 400)
		expect(m, 400)
		if f.Body.String() != m.Body.String() {
			t.Fatalf("foreign and missing reference responses differ: %s / %s", f.Body.String(), m.Body.String())
		}
	}
	sameFailure("POST", path, create(customerB, projectA, itemA), create(missing, projectA, itemA))
	sameFailure("POST", path, create(customerA, projectB, itemA), create(customerA, missing, itemA))
	sameFailure("POST", path, create(customerA, projectA, itemB), create(customerA, projectA, missing))
	if snapshot(a) != original {
		t.Fatal("failed create changed owned quotations")
	}

	// A foreign number in this year's series must not influence allocation.
	var quoteNumber *string
	if err := pool.QueryRow(ctx, `SELECT quotation_number FROM quotations WHERE business_id=$1 AND quotation_id=$2`, b, quoteB).Scan(&quoteNumber); err != nil {
		t.Fatal(err)
	}
	prefix := fmt.Sprintf("Q%d", time.Now().Year())
	exec(`UPDATE quotations SET quotation_number=$1 WHERE business_id=$2 AND quotation_id=$3`, prefix+"999999999999999", b, quoteB)
	foreignAfterSeed := snapshot(b)
	exec(`ALTER TABLE quotation_line_items ADD CONSTRAINT test_quotation_write_line CHECK (description <> 'reject quotation line')`)
	rejected := strings.Replace(body, lines(itemA), `[`+strings.Trim(lines(itemA), "[]")+`,{"description":"reject quotation line","quantity":"1","unit_price":"1"}]`, 1)
	expect(request("POST", path, good, rejected), 500)
	exec(`ALTER TABLE quotation_line_items DROP CONSTRAINT test_quotation_write_line`)
	if snapshot(a) != original {
		t.Fatal("failed second line insert committed a quotation or first line")
	}
	created := expect(request("POST", path, good, body), 201)
	id, ok := created["quotation_id"].(string)
	number, numbered := created["quotation_number"].(string)
	if !ok || !numbered || !strings.HasPrefix(number, prefix) || len(number) >= len(prefix)+15 ||
		created["business_id"] != nil || created["subtotal"] != "2.8126" || created["amount"] != "2.5000" ||
		created["valid_until"] != "2026-01-31" || created["status"] != "DRAFT" {
		t.Fatalf("created quotation: %v", created)
	}
	line := created["line_items"].([]any)[0].(map[string]any)
	if line["business_id"] != nil || line["amount"] != "2.8126" || line["item_id"] != itemA {
		t.Fatalf("created line: %v", line)
	}
	var ownedRows int
	err := pool.QueryRow(ctx, `SELECT count(*) FROM quotations q JOIN quotation_line_items l USING(business_id,quotation_id)
		WHERE q.business_id=$1 AND q.quotation_id=$2 AND q.customer_id=$3 AND q.project_id=$4 AND l.item_id=$5`, a, id, customerA, projectA, itemA).Scan(&ownedRows)
	if err != nil || ownedRows != 1 {
		t.Fatalf("persisted ownership: %d %v", ownedRows, err)
	}

	target := path + "/" + id
	f := request("PATCH", path+"/"+quoteB, good, "{}")
	m := request("PATCH", path+"/"+missing, good, "{}")
	expect(f, 400)
	expect(m, 400)
	if f.Body.String() != m.Body.String() {
		t.Fatalf("foreign and missing target responses differ: %s / %s", f.Body.String(), m.Body.String())
	}
	expect(request("PATCH", target, token(bUser, b), "{}"), 401)
	expect(request("PATCH", target, token(bUser, a), "{}"), 403)
	beforePatch := snapshot(a)
	checkPositions("PATCH", target, `{"line_items":`+lines(itemA)+`}`)
	for _, field := range []struct{ key, foreign string }{{"customer_id", customerB}, {"project_id", projectB}} {
		sameFailure("PATCH", target, fmt.Sprintf(`{%q:%q}`, field.key, field.foreign), fmt.Sprintf(`{%q:%q}`, field.key, missing))
	}
	sameFailure("PATCH", target, `{"line_items":`+lines(itemB)+`}`, `{"line_items":`+lines(missing)+`}`)
	// This overflows the quotation number column after replacement lines have been inserted.
	expect(request("PATCH", target, good, fmt.Sprintf(`{"quotation_number":%q,"line_items":%s}`, strings.Repeat("Q", 51), lines(itemA))), 500)
	if snapshot(a) != beforePatch {
		t.Fatal("failed patch changed owned quotation or lines")
	}
	// Reading an invalid stored amount fails after the header update and rolls it back.
	exec(`UPDATE quotation_line_items SET amount='NaN' WHERE business_id=$1 AND quotation_id=$2`, a, id)
	beforeConversion := snapshot(a)
	expect(request("PATCH", target, good, `{"notes":"must roll back"}`), 500)
	if snapshot(a) != beforeConversion {
		t.Fatal("response conversion failure committed a mutation")
	}
	sharedNumber := prefix + "999999999999999"
	updated := expect(request("PATCH", target, good, fmt.Sprintf(`{"project_id":null,"discount_value":"0.8126","line_items":%s,"business_id":%q,"quotation_number":%q}`, lines(itemA), b, sharedNumber)), 200)
	if updated["amount"] != "2.0000" || updated["project_id"] != nil || updated["business_id"] != nil || updated["quotation_number"] != sharedNumber {
		t.Fatalf("updated quotation: %v", updated)
	}

	var secondCustomer string
	if err := pool.QueryRow(ctx, `INSERT INTO customers (customer_id,business_id,name)
		VALUES (gen_random_uuid(),$1,'Quotation concurrency customer') RETURNING customer_id::text`, a).Scan(&secondCustomer); err != nil {
		t.Fatal(err)
	}
	var secondAdmin string
	err = pool.QueryRow(ctx, `INSERT INTO users (user_id,business_id,email,password_hash,role)
		VALUES (gen_random_uuid(),$1,'quotation-writes-admin@example.com','unused','admin') RETURNING user_id::text`, a).Scan(&secondAdmin)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan *httptest.ResponseRecorder, 2)
	// Distinct admins and customers leave the quotation number as the only shared write lock.
	for _, input := range []struct{ session, customer string }{{good, customerA}, {token(secondAdmin, a), secondCustomer}} {
		go func() {
			<-start
			createBody := fmt.Sprintf(`{"customer_id":%q,"currency":"IDR","line_items":[{"description":"concurrent work","quantity":"1","unit_price":"1"}]}`, input.customer)
			results <- request("POST", path, input.session, createBody)
		}()
	}
	close(start)
	first, second := expect(<-results, 201), expect(<-results, 201)
	if first["quotation_number"] == second["quotation_number"] {
		t.Fatalf("concurrent requests returned the same number: %v", first)
	}
	beforeRejections := snapshot(a)
	for _, action := range []string{"mark-sent", "accept", "decline", "void", ""} {
		method, suffix := "POST", "/"+action
		if action == "" {
			method, suffix = "DELETE", ""
		}
		foreign := request(method, path+"/"+quoteB+suffix, good, "{}")
		absent := request(method, path+"/"+missing+suffix, good, "{}")
		expect(foreign, 400)
		expect(absent, 400)
		if foreign.Body.String() != absent.Body.String() {
			t.Fatalf("%s foreign and missing responses differ: %s / %s", action, foreign.Body.String(), absent.Body.String())
		}
		expect(request(method, target+suffix, token(bUser, a), "{}"), 403)
	}
	if snapshot(a) != beforeRejections || snapshot(b) != foreignAfterSeed {
		t.Fatal("rejected lifecycle actions changed quotation data")
	}

	for _, acceptedBy := range []string{strings.Repeat("é", 256), "Ada\x00"} {
		body, err := json.Marshal(map[string]string{"accepted_by": acceptedBy})
		if err != nil {
			t.Fatal(err)
		}
		failure := request("POST", target+"/accept", good, string(body))
		if failure.Code != 500 || failure.Header().Get("Content-Type") != "text/plain; charset=utf-8" ||
			failure.Body.String() != "Internal Server Error" {
			t.Fatalf("invalid accepted_by response: %d %s", failure.Code, failure.Body.String())
		}
		if snapshot(a) != beforeRejections || snapshot(b) != foreignAfterSeed {
			t.Fatal("invalid accepted_by changed quotation data")
		}
	}

	exec(`UPDATE quotation_line_items SET amount='NaN' WHERE business_id=$1 AND quotation_id=$2`, a, id)
	beforeLifecycle := snapshot(a)
	expect(request("POST", target+"/mark-sent", good, ""), 500)
	if snapshot(a) != beforeLifecycle {
		t.Fatal("failed lifecycle response conversion committed status or timestamp")
	}
	exec(`UPDATE quotation_line_items SET amount=2.8126 WHERE business_id=$1 AND quotation_id=$2`, a, id)
	sent := expect(request("POST", target+"/mark-sent", good, ""), 200)
	if sent["status"] != "SENT" || sent["last_sent_at"] == nil || sent["amount"] != "2.0000" ||
		sent["business_id"] != nil || len(sent["line_items"].([]any)) != 1 {
		t.Fatalf("mark-sent response: %v", sent)
	}
	// Observe the status handler waiting at the quotation lock, then commit a
	// concurrent status change. The waiting request must validate the new status.
	lockCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := pool.Begin(lockCtx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(lockCtx, `UPDATE quotations SET status='INVOICED' WHERE business_id=$1 AND quotation_id=$2`, a, id); err != nil {
		t.Fatal(err)
	}
	go func() { results <- request("POST", target+"/void", good, "") }()
	for {
		var waiting bool
		err := pool.QueryRow(lockCtx, `SELECT EXISTS (
			SELECT FROM pg_stat_activity WHERE datname=current_database()
			AND wait_event_type='Lock' AND query LIKE '%FROM quotations WHERE business_id=%FOR UPDATE%'
		)`).Scan(&waiting)
		if err != nil {
			t.Fatalf("quotation status handler did not wait at row lock: %v", err)
		}
		if waiting {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := tx.Commit(lockCtx); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-results:
		if expect(result, 400)["detail"] != "cannot void an already-invoiced quotation" {
			t.Fatalf("void did not observe committed status: %s", result.Body.String())
		}
	case <-lockCtx.Done():
		t.Fatal("void did not resume after quotation lock released")
	}
	exec(`UPDATE quotations SET status='SENT' WHERE business_id=$1 AND quotation_id=$2`, a, id)

	// Distinct admins force the competing transitions to coordinate on the quotation row.
	start = make(chan struct{})
	for _, transition := range []struct{ action, session, body string }{
		{"accept", good, `{"accepted_by":"Customer"}`},
		{"decline", token(secondAdmin, a), ""},
	} {
		go func() {
			<-start
			results <- request("POST", target+"/"+transition.action, transition.session, transition.body)
		}()
	}
	close(start)
	win, lose := <-results, <-results
	if win.Code != 200 {
		win, lose = lose, win
	}
	winner := expect(win, 200)
	expect(lose, 400)
	var storedStatus string
	if err := pool.QueryRow(ctx, `SELECT status FROM quotations WHERE business_id=$1 AND quotation_id=$2`, a, id).Scan(&storedStatus); err != nil ||
		winner["status"] != storedStatus || (storedStatus != "ACCEPTED" && storedStatus != "DECLINED") {
		t.Fatalf("transition response and persisted state: %v / %s / %v", winner, storedStatus, err)
	}
	expect(request("DELETE", target, good, ""), 400)
	accepted := expect(request("POST", path+"/"+first["quotation_id"].(string)+"/accept", good, `{"accepted_by":"Customer"}`), 200)
	if accepted["status"] != "ACCEPTED" || accepted["accepted_at"] == nil || accepted["accepted_by"] != "Customer" {
		t.Fatalf("accept response: %v", accepted)
	}
	voided := expect(request("POST", target+"/void", good, ""), 200)
	if voided["status"] != "VOID" {
		t.Fatalf("void response: %v", voided)
	}
	draftTarget := path + "/" + second["quotation_id"].(string)
	exec(`CREATE FUNCTION test_block_quotation_delete() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'reject quotation delete'; END $$`)
	exec(`CREATE TRIGGER test_block_quotation_delete BEFORE DELETE ON quotations FOR EACH ROW EXECUTE FUNCTION test_block_quotation_delete()`)
	beforeDelete := snapshot(a)
	expect(request("DELETE", draftTarget, good, ""), 500)
	if snapshot(a) != beforeDelete {
		t.Fatal("failed quotation delete committed child deletion")
	}
	exec(`DROP TRIGGER test_block_quotation_delete ON quotations`)
	exec(`DROP FUNCTION test_block_quotation_delete()`)
	if deleted := request("DELETE", draftTarget, good, ""); deleted.Code != 204 || deleted.Body.Len() != 0 {
		t.Fatalf("delete response: %d %s", deleted.Code, deleted.Body.String())
	}
	expect(request("GET", draftTarget, good, ""), 404)

	if snapshot(b) != foreignAfterSeed {
		t.Fatal("quotation writes changed the foreign quotation or lines")
	}
	exec(`DELETE FROM quotations WHERE business_id=$1 AND quotation_id IN ($2,$3,$4)`, a, id, first["quotation_id"], second["quotation_id"])
	exec(`DELETE FROM users WHERE business_id=$1 AND user_id=$2`, a, secondAdmin)
	exec(`DELETE FROM customers WHERE business_id=$1 AND customer_id=$2`, a, secondCustomer)
	exec(`UPDATE quotations SET quotation_number=$1 WHERE business_id=$2 AND quotation_id=$3`, quoteNumber, b, quoteB)
	if snapshot(a) != original || snapshot(b) != foreignSnapshot {
		t.Fatal("quotation fixture restoration changed existing rows")
	}
}
