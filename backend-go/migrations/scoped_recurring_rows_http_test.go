package migrations

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func testScopedRecurringTemplateRows(t *testing.T, pool *pgxpool.Pool, a, b, template, good, foreign, forged string, request func(string, string, string, string) *httptest.ResponseRecorder) {
	t.Helper()
	const path = "/api/v1/invoices/recurring-templates/rows"
	ctx := context.Background()
	for _, check := range []struct {
		token string
		code  int
	}{{"", 401}, {foreign, 401}, {forged, 403}} {
		if w := request("GET", path, check.token, ""); w.Code != check.code {
			t.Fatalf("recurring rows authorization: got %d %s, want %d", w.Code, w.Body, check.code)
		}
	}
	read := func(path string) []map[string]any {
		t.Helper()
		w := request("GET", path, good, "")
		var rows []map[string]any
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &rows) != nil {
			t.Fatalf("recurring rows: %d %s", w.Code, w.Body)
		}
		return rows
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	if rows := read(path); len(rows) != 1 || rows[0]["template_id"] != template || rows[0]["status"] != "MISCONFIGURED" {
		t.Fatalf("initial recurring rows: %v", rows)
	}
	exec(`UPDATE invoices SET billing_cycle_ref='{"frequency":"DAILY","start_date":"2026-01-01","end_mode":"NEVER","paused":true}', payment_terms='Net 30'
WHERE business_id=$1 AND invoice_id=$2`, a, template)
	exec(`INSERT INTO invoices(business_id,invoice_id,invoice_type,currency,amount,balance_due,status,is_template,issue_date,billing_cycle_ref)
SELECT owner::uuid,md5('rows-child'||seq)::uuid,kind,'USD',1,1,state,false,issued::date,ref
FROM (VALUES
  ($1::text,1,'RECURRING','OPEN','2026-01-03',jsonb_build_object('template_invoice_id',$3::text,'cycle_key','2026-01-02')),
  ($1::text,2,'RECURRING','OPEN','2026-01-03',jsonb_build_object('template_invoice_id',$3::text,'cycle_key','2026-01-02')),
  ($1::text,3,'RECURRING','VOID','2026-01-05',jsonb_build_object('template_invoice_id',$3::text)),
  ($1::text,4,'MILESTONE','OPEN','2026-01-05',jsonb_build_object('template_invoice_id',$3::text)),
  ($2::text,5,'RECURRING','OPEN','2026-01-06',jsonb_build_object('template_invoice_id',$3::text,'cycle_key','2026-01-06')),
  ($1::text,6,'RECURRING','OPEN','2026-01-07','["invalid-reference"]'::jsonb)
) AS children(owner,seq,kind,state,issued,ref)`, a, b, template)
	defer exec(`DELETE FROM invoices WHERE business_id IN ($1,$2) AND invoice_id IN (SELECT md5('rows-child'||i)::uuid FROM generate_series(1,6) i)`, a, b)
	rows := read(path)
	if len(rows) != 1 {
		t.Fatalf("unexpected recurring rows: %v", rows)
	}
	row := rows[0]
	if row["generated_count"] != float64(2) || row["previous_issue_date"] != "2026-01-02" || row["next_run_date"] != "2026-01-03" ||
		row["amount"] != "1.0000" || row["payment_terms"] != "Net 30" || row["customer_name"] == nil ||
		row["business_id"] != nil || row["status"] != "PAUSED" {
		t.Fatalf("incorrect recurring summary: %v", row)
	}
	if filtered := read(path + "?limit=0&status=ENDED&customer_id=invalid"); !reflect.DeepEqual(filtered, rows) {
		t.Fatalf("query parameters changed recurring summaries: %v", filtered)
	}
	exec(`UPDATE invoices SET billing_cycle_ref=jsonb_set(billing_cycle_ref,'{cycle_key}','{"invalid":true}') WHERE business_id=$1 AND invoice_id IN (md5('rows-child1')::uuid,md5('rows-child2')::uuid)`, a)
	if fallback := read(path)[0]["previous_issue_date"]; fallback != "2026-01-03" {
		t.Fatalf("invalid cycle key fallback: %v", fallback)
	}
	{
		var constraint string
		if err := pool.QueryRow(ctx, `SELECT pg_get_constraintdef(oid) FROM pg_constraint
WHERE conrelid='invoices'::regclass AND conname='invoices_customer_id_fkey'`).Scan(&constraint); err != nil {
			t.Fatal(err)
		}
		exec(`ALTER TABLE invoices DROP CONSTRAINT invoices_customer_id_fkey`)
		defer exec(`ALTER TABLE invoices ADD CONSTRAINT invoices_customer_id_fkey ` + constraint)
		const damaged = "60000000-0000-0000-0000-000000000001"
		exec(`INSERT INTO invoices(business_id,invoice_id,customer_id,invoice_type,currency,amount,balance_due,status,is_template,billing_cycle_ref,created_at)
VALUES ($1,$2,NULL,'RECURRING','USD',12,12,'DRAFT',true,'["invalid-schedule"]','2099-01-01')`, a, damaged)
		defer exec(`DELETE FROM invoices WHERE business_id=$1 AND invoice_id=$2`, a, damaged)

		ordered := read(path)
		if len(ordered) != 2 || ordered[0]["template_id"] != damaged || ordered[1]["template_id"] != template ||
			ordered[0]["customer_name"] != nil || ordered[0]["customer_id"] != nil || ordered[0]["status"] != "MISCONFIGURED" ||
			ordered[0]["generated_count"] != float64(0) || ordered[0]["previous_issue_date"] != nil {
			t.Fatalf("malformed schedule and newest-first rows: %v", ordered)
		}
		exec(`UPDATE invoices SET customer_id=(SELECT customer_id FROM customers WHERE business_id=$3 LIMIT 1)
WHERE business_id=$1 AND invoice_id=$2`, a, damaged, b)
		foreign := request("GET", path, good, "")
		exec(`UPDATE invoices SET customer_id='60000000-0000-0000-0000-000000000099' WHERE business_id=$1 AND invoice_id=$2`, a, damaged)
		missing := request("GET", path, good, "")
		if foreign.Code != 500 || missing.Code != 500 || foreign.Body.String() != missing.Body.String() {
			t.Fatalf("unresolved customers returned different responses: %d %s / %d %s", foreign.Code, foreign.Body, missing.Code, missing.Body)
		}

	}

}
