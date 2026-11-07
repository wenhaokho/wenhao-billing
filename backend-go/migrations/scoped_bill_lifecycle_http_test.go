package migrations

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func testScopedBillLifecycle(
	t *testing.T, pool *pgxpool.Pool, a, b, bUser, billA, billB, missingID, good string,
	token func(string, string) string,
	request func(string, string, string, string) *httptest.ResponseRecorder,
	snapshot func(string) string,
) {
	t.Helper()
	ctx := context.Background()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	post := func(id, action, session, body string) *httptest.ResponseRecorder {
		return request("POST", "/api/v1/bills/"+id+"/"+action, session, body)
	}
	payment := func(amount string) string {
		return `{"amount":"` + amount + `","payment_date":"2026-01-01","wht_amount":"99","business_id":"` + b + `"}`
	}
	expect := func(w *httptest.ResponseRecorder, status int, detail string) {
		t.Helper()
		if w.Code != status || !strings.Contains(w.Body.String(), detail) {
			t.Fatalf("got %d %s, want %d with %q", w.Code, w.Body.String(), status, detail)
		}
	}
	state := func(id, status, balance string) {
		t.Helper()
		var gotStatus, gotBalance string
		err := pool.QueryRow(ctx, `SELECT status,balance_due::text FROM bills WHERE business_id=$1 AND bill_id=$2`, a, id).Scan(&gotStatus, &gotBalance)
		if err != nil || gotStatus != status || gotBalance != balance {
			t.Fatalf("bill state %s/%s, want %s/%s: %v", gotStatus, gotBalance, status, balance, err)
		}
	}
	accountingCounts := func() [3]int {
		t.Helper()
		var counts [3]int
		err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM payments),(SELECT count(*) FROM journal_entries),(SELECT count(*) FROM journal_lines)`).Scan(&counts[0], &counts[1], &counts[2])
		if err != nil {
			t.Fatal(err)
		}
		return counts
	}
	beforeCounts := accountingCounts()
	beforeB := snapshot(billB)
	originalA := snapshot(billA)
	for action, body := range map[string]string{"record-payment": payment("1"), "void": "{}"} {
		foreign := post(billB, action, good, body)
		missing := post(missingID, action, good, body)
		expect(foreign, 404, "bill not found")
		if foreign.Body.String() != missing.Body.String() || missing.Code != 404 {
			t.Fatalf("foreign and missing %s responses differ", action)
		}
		expect(post(billA, action, token(bUser, b), body), 401, "not authenticated")
		expect(post(billA, action, token(bUser, a), body), 403, "admin required")
	}
	expect(post(billA, "record-payment", good, payment("1")), 400, "cannot record payment on PAID bill")
	expect(post(billA, "void", good, "{}"), 400, "bill is PAID and cannot be voided")
	exec(`UPDATE bills SET status='OPEN' WHERE bill_id=$1 AND business_id=$2`, billA, a)
	beforeA := snapshot(billA)
	expect(post(billA, "record-payment", good, payment("5.6251")), 400, "amount 5.6251 exceeds balance due 5.6250")
	expect(post(billA, "record-payment", good, payment("0.00001")), 422, "decimal_max_places")
	expect(post(billA, "record-payment", good, `{"amount":1,"payment_date":"2026-01-01T00:00:01Z"}`), 422, "payment_date")
	expect(post(billA, "void", good, "[]"), 422, "dict_type")
	if snapshot(billA) != beforeA {
		t.Fatal("invalid requests changed the owned bill")
	}

	// PostgreSQL accepts NaN in a numeric column. The stored line is readable
	// by pgx but cannot be returned as a bill money value.
	exec(`UPDATE bill_line_items SET amount='NaN' WHERE bill_id=$1 AND business_id=$2`, billA, a)
	beforeFailure := snapshot(billA)
	expect(post(billA, "record-payment", good, payment("1")), 500, "Internal Server Error")
	expect(post(billA, "void", good, `{"reason":"must roll back"}`), 500, "Internal Server Error")
	if snapshot(billA) != beforeFailure {
		t.Fatal("failed response conversion committed a bill mutation")
	}
	exec(`UPDATE bill_line_items SET amount='0.0002' WHERE bill_id=$1 AND business_id=$2`, billA, a)

	partial := post(billA, "record-payment", good, `{"amount":1.0001,"payment_date":1767225600}`)
	expect(partial, 200, `"balance_due":"4.6249"`)
	var out map[string]any
	if err := json.Unmarshal(partial.Body.Bytes(), &out); err != nil || out["status"] != "PARTIAL" || out["business_id"] != nil {
		t.Fatalf("partial response %s: %v", partial.Body.String(), err)
	}
	if lines, ok := out["line_items"].([]any); !ok || len(lines) != 1 {
		t.Fatalf("partial response lost its line: %s", partial.Body.String())
	}
	state(billA, "PARTIAL", "4.6249")

	var secondAdmin string
	err := pool.QueryRow(ctx, `INSERT INTO users (user_id,business_id,email,password_hash,role)
		VALUES (gen_random_uuid(),$1,'bill-lifecycle-admin@example.com','unused','admin') RETURNING user_id::text`, a).Scan(&secondAdmin)
	if err != nil {
		t.Fatal(err)
	}
	secondSession := token(secondAdmin, a)
	compete := func(firstAction, firstBody, secondAction, secondBody string) (*httptest.ResponseRecorder, *httptest.ResponseRecorder) {
		start := make(chan struct{})
		firstResult, secondResult := make(chan *httptest.ResponseRecorder, 1), make(chan *httptest.ResponseRecorder, 1)
		go func() {
			<-start
			firstResult <- post(billA, firstAction, good, firstBody)
		}()
		go func() {
			<-start
			secondResult <- post(billA, secondAction, secondSession, secondBody)
		}()
		close(start)
		return <-firstResult, <-secondResult
	}
	first, second := compete("record-payment", payment("3.0001"), "record-payment", payment("3.0001"))
	if first.Code != 200 {
		first, second = second, first
	}
	expect(first, 200, `"balance_due":"1.6248"`)
	expect(second, 400, "amount 3.0001 exceeds balance due 1.6248")
	state(billA, "PARTIAL", "1.6248")
	expect(post(billA, "record-payment", good, payment("1.6248")), 200, `"status":"PAID"`)
	state(billA, "PAID", "0.0000")

	exec(`UPDATE bills SET status='OPEN', balance_due='5.6250' WHERE bill_id=$1 AND business_id=$2`, billA, a)
	expect(post(billA, "void", good, `{"reason":{"why":"duplicate","confirmed":true}}`), 200, `[voided] {'why': 'duplicate', 'confirmed': True}`)
	state(billA, "VOID", "0.0000")
	expect(post(billA, "void", good, "{}"), 400, "bill is VOID and cannot be voided")
	expect(post(billA, "record-payment", good, payment("1")), 400, "cannot record payment on VOID bill")
	exec(`UPDATE bills SET status='OPEN', balance_due='5.6250', notes='updated' WHERE bill_id=$1 AND business_id=$2`, billA, a)
	paid, voided := compete("record-payment", payment("5.6250"), "void", "{}")
	if paid.Code == 200 {
		expect(paid, 200, `"status":"PAID"`)
		expect(voided, 400, "bill is PAID and cannot be voided")
		state(billA, "PAID", "0.0000")
	} else {
		expect(paid, 400, "cannot record payment on VOID bill")
		expect(voided, 200, "[voided] no reason given")
		state(billA, "VOID", "0.0000")
	}

	// Restore the shared HTTP fixture before the remaining route tests.
	exec(`DELETE FROM users WHERE user_id=$1 AND business_id=$2`, secondAdmin, a)
	exec(`UPDATE bills SET status='PAID', balance_due='5.6250', notes='updated' WHERE bill_id=$1 AND business_id=$2`, billA, a)
	if snapshot(billA) != originalA {
		t.Fatal("bill lifecycle fixture restoration changed the shared bill")
	}
	if snapshot(billB) != beforeB || accountingCounts() != beforeCounts {
		t.Fatal("bill lifecycle changed foreign data or accounting row counts")
	}
}
