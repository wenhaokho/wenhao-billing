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

func testScopedRecurringTemplateWrites(t *testing.T, pool *pgxpool.Pool, a, b, bUser, customerA, customerB, projectA, projectB, itemA, itemB, missing, good string,
	token func(string, string) string, request func(string, string, string, string) *httptest.ResponseRecorder,
) {
	t.Helper()
	const createPath = "/api/v1/invoices/recurring-template"
	const templatePath = "/api/v1/invoices/recurring-templates/"
	ctx := context.Background()
	expect := func(w *httptest.ResponseRecorder, code int) map[string]any {
		t.Helper()
		var result map[string]any
		if w.Code != code || json.Unmarshal(w.Body.Bytes(), &result) != nil {
			t.Fatalf("got %d %s, want %d with JSON", w.Code, w.Body, code)
		}
		return result
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	var originalType string
	var originalRate *string
	var originalGross bool
	if err := pool.QueryRow(ctx, `SELECT default_wht_type,default_wht_rate::text,default_wht_gross_up
FROM customers WHERE business_id=$1 AND customer_id=$2`, a, customerA).Scan(&originalType, &originalRate, &originalGross); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE customers SET default_wht_type='PPH23',default_wht_rate=2,default_wht_gross_up=true WHERE business_id=$1 AND customer_id=$2`, a, customerA)
	t.Cleanup(func() {
		exec(`UPDATE customers SET default_wht_type=$3,default_wht_rate=$4::text::numeric,default_wht_gross_up=$5 WHERE business_id=$1 AND customer_id=$2`, a, customerA, originalType, originalRate, originalGross)
	})
	body := func(customer, project, item string) string {
		return fmt.Sprintf(`{"customer_id":%q,"project_id":%q,"business_id":%q,"currency":"USD","payment_terms":"Net 30","invoice_number":"ignored","line_items":[{"item_id":%q,"description":"template work","quantity":"1.1250","unit_price":"2.5001"}],"schedule":{"frequency":"DAILY","start_date":"2026-01-01","end_mode":"AFTER_N","end_after_cycles":1}}`, customer, project, b, item)
	}
	cleanupInvoice := func(owner, id string) {
		t.Cleanup(func() {
			exec(`DELETE FROM invoice_line_items WHERE business_id=$1 AND invoice_id=$2`, owner, id)
			exec(`DELETE FROM invoices WHERE business_id=$1 AND invoice_id=$2`, owner, id)
		})
	}
	valid := body(customerA, projectA, itemA)
	foreign := body(customerB, projectB, itemB)
	expect(request("POST", createPath, token(bUser, b), valid), 401)
	expect(request("POST", createPath, token(bUser, a), valid), 403)
	row := expect(request("POST", createPath, good, valid), 201)
	id := row["invoice_id"].(string)
	cleanupInvoice(a, id)
	target := templatePath + id
	var owned int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM invoices i JOIN invoice_line_items l USING (business_id,invoice_id)
WHERE i.business_id=$1 AND i.invoice_id=$2 AND i.customer_id=$3 AND i.project_id=$4 AND l.item_id=$5`, a, id, customerA, projectA, itemA).Scan(&owned); err != nil || owned != 1 {
		t.Fatalf("persisted template ownership: %d / %v", owned, err)
	}

	if row["invoice_number"] != nil || row["issue_date"] != nil || row["due_date"] != nil ||
		row["status"] != "DRAFT" || row["invoice_type"] != "RECURRING" || row["is_template"] != true ||
		row["subtotal"] != "2.8126" || row["wht_type"] != "PPH23" || row["wht_amount"] != "0.0574" ||
		row["amount"] != "2.8700" || row["business_id"] != nil {
		t.Fatalf("template response: %v", row)
	}
	var foreignID string
	if err := pool.QueryRow(ctx, `INSERT INTO invoices(business_id,invoice_id,customer_id,invoice_type,currency,amount,balance_due,status,is_template,billing_cycle_ref)
VALUES($1,gen_random_uuid(),$2,'RECURRING','USD',1,1,'DRAFT',true,'{"frequency":"DAILY","start_date":"2026-01-01"}') RETURNING invoice_id::text`, b, customerB).Scan(&foreignID); err != nil {
		t.Fatal(err)
	}
	cleanupInvoice(b, foreignID)
	snapshot := func() string {
		t.Helper()
		var value string
		err := pool.QueryRow(ctx, `SELECT jsonb_build_array(
(SELECT jsonb_agg(to_jsonb(i) ORDER BY invoice_id) FROM invoices i),
(SELECT jsonb_agg(to_jsonb(l) ORDER BY line_item_id) FROM invoice_line_items l))::text`).Scan(&value)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}

	before := snapshot()
	for _, pair := range [][2]string{{customerA, customerB}, {projectA, projectB}, {itemA, itemB}} {
		for _, method := range []string{"POST", "PUT"} {
			path := createPath
			if method == "PUT" {
				path = target
			}
			f := request(method, path, good, strings.Replace(valid, pair[0], pair[1], 1))
			m := request(method, path, good, strings.Replace(valid, pair[0], missing, 1))
			expect(f, 400)
			expect(m, 400)
			if f.Body.String() != m.Body.String() {
				t.Fatalf("%s foreign and absent reference responses differ: %s / %s", method, f.Body, m.Body)
			}
		}
	}
	for _, route := range []struct {
		method, body string
		status       int
	}{{"PUT", foreign, 400}, {"PATCH", `{"action":"pause"}`, 404}} {
		f := request(route.method, templatePath+foreignID, good, route.body)
		m := request(route.method, templatePath+missing, good, route.body)
		expect(f, route.status)
		expect(m, route.status)
		if f.Body.String() != m.Body.String() {
			t.Fatalf("%s foreign and absent template responses differ: %s / %s", route.method, f.Body, m.Body)
		}
	}
	expect(request("PATCH", target, good, `{"action":"unknown"}`), 400)
	expect(request("PUT", target, good, strings.Replace(valid, `"end_after_cycles":1`, `"end_after_cycles":null`, 1)), 400)
	exec(`ALTER TABLE invoice_line_items ADD CONSTRAINT test_scoped_template_line CHECK (description <> 'reject scoped template line')`)
	badLine := strings.Replace(valid, "template work", "reject scoped template line", 1)
	for _, method := range []string{"POST", "PUT"} {
		path := createPath
		if method == "PUT" {
			path = target
		}
		if w := request(method, path, good, badLine); w.Code != 500 {
			t.Fatalf("failed line write: %d %s", w.Code, w.Body)
		}
	}
	exec(`ALTER TABLE invoice_line_items DROP CONSTRAINT test_scoped_template_line`)
	if snapshot() != before {
		t.Fatal("rejected template write persisted data")
	}
	// A foreign child can carry the same JSON template ID, but cannot end this schedule.
	var foreignChild string
	if err := pool.QueryRow(ctx, `INSERT INTO invoices(business_id,invoice_id,invoice_type,currency,amount,balance_due,status,is_template,issue_date,billing_cycle_ref)
VALUES($1,gen_random_uuid(),'RECURRING','USD',1,1,'DRAFT',false,'2026-01-01',jsonb_build_object('template_invoice_id',$2::text,'cycle_key','2026-01-01')) RETURNING invoice_id::text`, b, id).Scan(&foreignChild); err != nil {
		t.Fatal(err)
	}
	cleanupInvoice(b, foreignChild)

	exec(`UPDATE invoice_line_items SET amount='NaN' WHERE business_id=$1 AND invoice_id=$2`, a, id)
	beforeAction := snapshot()
	expect(request("PATCH", target, good, `{"action":"pause"}`), 500)
	if snapshot() != beforeAction {
		t.Fatal("schedule update committed despite response serialization failure")
	}
	exec(`UPDATE invoice_line_items SET amount=2.8126 WHERE business_id=$1 AND invoice_id=$2`, a, id)

	var secondAdmin string
	if err := pool.QueryRow(ctx, `INSERT INTO users(user_id,business_id,email,password_hash,role)
VALUES(gen_random_uuid(),$1,'recurring-template-admin@example.com','unused','admin') RETURNING user_id::text`, a).Scan(&secondAdmin); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { exec(`DELETE FROM users WHERE business_id=$1 AND user_id=$2`, a, secondAdmin) })
	secondToken := token(secondAdmin, a)
	concurrent := func(first, second func() *httptest.ResponseRecorder) (*httptest.ResponseRecorder, *httptest.ResponseRecorder) {
		t.Helper()
		start := make(chan struct{})
		one, two := make(chan *httptest.ResponseRecorder, 1), make(chan *httptest.ResponseRecorder, 1)
		go func() { <-start; one <- first() }()
		go func() { <-start; two <- second() }()
		close(start)
		wait, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		select {
		case f := <-one:
			select {
			case s := <-two:
				return f, s
			case <-wait.Done():
			}
		case <-wait.Done():
		}
		t.Fatal("concurrent template writes timed out")
		return nil, nil
	}
	paused, updated := concurrent(
		func() *httptest.ResponseRecorder { return request("PATCH", target, good, `{"action":"pause"}`) },
		func() *httptest.ResponseRecorder {
			return request("PUT", target, secondToken, strings.Replace(valid, "template work", "updated template", 1))
		},
	)
	expect(paused, 200)
	expect(updated, 200)
	final := expect(request("GET", target, good, ""), 200)
	if final["billing_cycle_ref"].(map[string]any)["paused"] != true || final["status"] != "DRAFT" ||
		final["line_items"].([]any)[0].(map[string]any)["description"] != "updated template" ||
		final["wht_type"] != row["wht_type"] || final["wht_amount"] != row["wht_amount"] {
		t.Fatalf("concurrent writes lost schedule or template data: %v", final)
	}
	expect(request("PATCH", target, good, `{"action":"resume"}`), 200)
	end := expect(request("PATCH", target, good, `{"action":"end_now"}`), 200)
	if end["billing_cycle_ref"].(map[string]any)["end_mode"] != "ON_DATE" {
		t.Fatalf("ended template: %v", end)
	}
	ended := snapshot()
	expect(request("PUT", target, good, valid), 409)
	expect(request("PATCH", target, good, `{"action":"pause"}`), 409)
	expect(request("PATCH", target, good, `{"action":"resume"}`), 409)
	if snapshot() != ended {
		t.Fatal("ended template changed")
	}
	// A template and a numbered invoice can be created concurrently using the same item.
	template, invoice := concurrent(
		func() *httptest.ResponseRecorder { return request("POST", createPath, good, valid) },
		func() *httptest.ResponseRecorder {
			return request("POST", "/api/v1/invoices", secondToken, strings.Replace(valid, `"invoice_number":"ignored",`, "", 1))
		},
	)
	templateRow, invoiceRow := expect(template, 201), expect(invoice, 201)
	cleanupInvoice(a, templateRow["invoice_id"].(string))
	cleanupInvoice(a, invoiceRow["invoice_id"].(string))
	if templateRow["invoice_number"] != nil || invoiceRow["invoice_number"] == nil ||
		!strings.HasPrefix(invoiceRow["invoice_number"].(string), fmt.Sprintf("WH%d", time.Now().Year())) {
		t.Fatalf("number allocation for template and invoice: %v / %v", templateRow["invoice_number"], invoiceRow["invoice_number"])
	}

	// Use a new template so the earlier schedule-action assertions stay independent.
	valid = strings.Replace(valid, `"end_after_cycles":1`, `"end_after_cycles":3`, 1)
	id = expect(request("POST", createPath, good, valid), 201)["invoice_id"].(string)
	cleanupInvoice(a, id)
	target = templatePath + id
	trigger := func(template, credential, payload string) *httptest.ResponseRecorder {
		return request("POST", "/api/v1/invoices/recurring/"+template+"/trigger", credential, payload)
	}
	expect(trigger(foreignID, good, `{"cycle_key":"2026-01-01"}`), 400)
	expect(trigger(missing, good, `{"cycle_key":"2026-01-01"}`), 400)
	expect(trigger(id, token(bUser, a), `{"cycle_key":"2026-01-01"}`), 403)
	exec(`UPDATE invoices SET billing_cycle_ref=jsonb_set(billing_cycle_ref,'{template_invoice_id}',to_jsonb($3::text))
		WHERE business_id=$1 AND invoice_id=$2`, b, foreignChild, id)

	var malformed string
	if err := pool.QueryRow(ctx, `INSERT INTO invoices(business_id,invoice_id,invoice_type,currency,amount,balance_due,status,billing_cycle_ref)
		VALUES($1,gen_random_uuid(),'RECURRING','USD',1,1,'DRAFT',jsonb_build_object('template_invoice_id',$2::text,'cycle_key',123))
		RETURNING invoice_id::text`, a, id).Scan(&malformed); err != nil {
		t.Fatal(err)
	}
	beforeMalformed := snapshot()
	blocked := expect(trigger(id, good, `{"cycle_key":"2026-01-01"}`), 409)
	if blocked["detail"] != "Existing recurring invoice has an ambiguous cycle key; manual review required" || snapshot() != beforeMalformed {
		t.Fatalf("ambiguous cycle: %v", blocked)
	}
	exec(`DELETE FROM invoices WHERE business_id=$1 AND invoice_id=$2`, a, malformed)

	for _, ref := range []struct {
		table, constraint, update, local, foreign string
	}{
		{"invoices", "invoices_customer_id_fkey",
			`UPDATE invoices SET customer_id=$3 WHERE business_id=$1 AND invoice_id=$2`, customerA, customerB},
		{"invoices", "invoices_project_id_fkey",
			`UPDATE invoices SET project_id=$3 WHERE business_id=$1 AND invoice_id=$2`, projectA, projectB},
		{"invoice_line_items", "invoice_line_items_item_id_fkey",
			`UPDATE invoice_line_items SET item_id=$3 WHERE business_id=$1 AND invoice_id=$2`, itemA, itemB},
	} {
		func() {
			var definition string
			if err := pool.QueryRow(ctx, `SELECT pg_get_constraintdef(oid) FROM pg_constraint
				WHERE conrelid=$1::regclass AND conname=$2`, ref.table, ref.constraint).Scan(&definition); err != nil {
				t.Fatal(err)
			}
			exec("ALTER TABLE " + ref.table + " DROP CONSTRAINT " + ref.constraint)
			defer func() {
				exec(ref.update, a, id, ref.local)
				exec("ALTER TABLE " + ref.table + " ADD CONSTRAINT " + ref.constraint + " " + definition)
			}()
			for _, invalid := range []string{ref.foreign, missing} {
				exec(ref.update, a, id, invalid)
				result := expect(trigger(id, good, `{"cycle_key":"2026-01-01"}`), 400)
				if result["detail"] != "referenced record not found" {
					t.Fatalf("damaged template reference: %v", result)
				}
			}
		}()
	}

	// Reject one recipient after the child, lines and event have been inserted.
	exec(`ALTER TABLE recurring_draft_deliveries ADD CONSTRAINT test_scoped_trigger_delivery
		CHECK (email <> 'recurring-template-admin@example.com') NOT VALID`)
	beforeFailure := snapshot()
	var eventsBefore, eventsAfter int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM recurring_draft_events WHERE business_id=$1`, a).Scan(&eventsBefore); err != nil {
		t.Fatal(err)
	}
	expect(trigger(id, good, `{"cycle_key":"2026-01-01"}`), 500)
	exec(`ALTER TABLE recurring_draft_deliveries DROP CONSTRAINT test_scoped_trigger_delivery`)
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM recurring_draft_events WHERE business_id=$1`, a).Scan(&eventsAfter); err != nil {
		t.Fatal(err)
	}
	if snapshot() != beforeFailure || eventsAfter != eventsBefore {
		t.Fatal("failed delivery insertion persisted draft, lines or event")
	}

	triggerPath := "/api/v1/invoices/recurring/" + id + "/trigger"
	cycle := `{"cycle_key":"2026-01-01"}`
	triggered, repeated := concurrent(
		func() *httptest.ResponseRecorder { return request("POST", triggerPath, good, cycle) },
		func() *httptest.ResponseRecorder { return request("POST", triggerPath, secondToken, cycle) },
	)
	child := expect(triggered, 200)
	childID := child["invoice_id"].(string)
	cleanupInvoice(a, childID)
	t.Cleanup(func() {
		exec(`DELETE FROM recurring_draft_deliveries WHERE business_id=$1 AND invoice_id=$2`, a, childID)
		exec(`DELETE FROM recurring_draft_events WHERE business_id=$1 AND invoice_id=$2`, a, childID)
	})
	again := expect(repeated, 200)
	if again["invoice_id"] != childID || childID == id || childID == foreignChild || child["amount"] != "2.8700" || child["wht_amount"] != "0.0574" {
		t.Fatalf("trigger response: %v / %v", child, again)
	}
	var eventCount, recipients, users int
	if err := pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM recurring_draft_events WHERE business_id=$1 AND invoice_id=$2),
		(SELECT count(*) FROM recurring_draft_deliveries d JOIN users u USING (business_id,user_id)
		  WHERE d.business_id=$1 AND d.invoice_id=$2 AND d.email=u.email),
		(SELECT count(*) FROM users WHERE business_id=$1)`, a, childID).Scan(&eventCount, &recipients, &users); err != nil || eventCount != 1 || recipients != users || users == 0 {
		t.Fatalf("outbox: events=%d deliveries=%d users=%d error=%v", eventCount, recipients, users, err)
	}

	// Hold the item so the trigger pauses while owning the template row.
	lockCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	gate, err := pool.Begin(lockCtx)
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Rollback(ctx)
	if _, err = gate.Exec(lockCtx, `SELECT item_id FROM items WHERE business_id=$1 AND item_id=$2 FOR UPDATE`, a, itemA); err != nil {
		t.Fatal(err)
	}
	waitForLock := func(pattern string) {
		t.Helper()
		for {
			var waiting bool
			err := pool.QueryRow(lockCtx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity
				WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE $1)`, pattern).Scan(&waiting)
			if err != nil {
				t.Fatal(err)
			}
			if waiting {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	first, edit := make(chan *httptest.ResponseRecorder, 1), make(chan *httptest.ResponseRecorder, 1)
	go func() { first <- trigger(id, good, `{"cycle_key":"2026-01-02"}`) }()
	waitForLock("%name: ItemIsHosting :one%")
	go func() {
		edit <- request("PUT", target, secondToken, strings.Replace(valid, "template work", "later edit", 1))
	}()
	waitForLock("%FROM businesses%FOR NO KEY UPDATE%")
	if err = gate.Commit(lockCtx); err != nil {
		t.Fatal(err)
	}
	for _, pending := range []<-chan *httptest.ResponseRecorder{first, edit} {
		select {
		case response := <-pending:
			result := expect(response, 200)
			line := result["line_items"].([]any)[0].(map[string]any)
			if pending == first {
				generated := result["invoice_id"].(string)
				cleanupInvoice(a, generated)
				t.Cleanup(func() {
					exec(`DELETE FROM recurring_draft_deliveries WHERE business_id=$1 AND invoice_id=$2`, a, generated)
					exec(`DELETE FROM recurring_draft_events WHERE business_id=$1 AND invoice_id=$2`, a, generated)
				})
				if !strings.HasPrefix(line["description"].(string), "template work") {
					t.Fatalf("draft used a later edit: %v", line)
				}
			} else if line["description"] != "later edit" {
				t.Fatalf("template edit failed: %v", line)
			}
		case <-lockCtx.Done():
			t.Fatal("trigger or edit timed out")
		}
	}

}
