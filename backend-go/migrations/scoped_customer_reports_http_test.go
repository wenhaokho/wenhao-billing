package migrations

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func testScopedCustomerReports(t *testing.T, pool *pgxpool.Pool, a, b, good, foreign string, request func(string, string, string, string) *httptest.ResponseRecorder) {
	t.Helper()
	ctx := context.Background()
	today := time.Now().Format(time.DateOnly)
	const own = "60000000-0000-0000-0000-000000000021"
	const other = "60000000-0000-0000-0000-000000000022"
	const empty = "60000000-0000-0000-0000-000000000023"
	const missing = "60000000-0000-0000-0000-000000000024"
	const usd = "60000000-0000-0000-0000-000000000025"
	const idr = "60000000-0000-0000-0000-000000000026"
	const voided = "60000000-0000-0000-0000-000000000027"
	const draft = "60000000-0000-0000-0000-000000000028"
	const base = "/api/v1/customers/"
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	var restore []func()
	defer func() {
		for _, fn := range restore {
			fn()
		}
	}()
	exec(`INSERT INTO customers(business_id,customer_id,name) VALUES ($1,$3,'Scoped report'),($2,$4,'Other scope'),($1,$5,'Empty report')`, a, b, own, other, empty)
	defer exec(`DELETE FROM customers WHERE customer_id IN ($1,$2,$3)`, own, other, empty)
	exec(`INSERT INTO invoices(business_id,invoice_id,customer_id,invoice_type,currency,amount,balance_due,status,is_template,issue_date,due_date)
VALUES ($1,$2,$6,'MILESTONE','USD',100000000000000.1234,100000000000000.1234,'OPEN',false,$7::date-4,$7::date-1),
($1,$3,$6,'USAGE','IDR',5.4321,5.4321,'PARTIAL',false,$7::date-3,$7::date),
($1,$4,$6,'MILESTONE','USD',99,99,'VOID',false,$7::date-1,$7::date+1),
($1,$5,$6,'RECURRING','USD',6.1001,6.1001,'DRAFT',true,$7::date-2,$7::date+2)`, a, usd, idr, voided, draft, own, today)
	defer exec(`DELETE FROM invoices WHERE customer_id=$1`, own)
	exec(`INSERT INTO invoice_line_items(business_id,line_item_id,invoice_id,position,description,quantity,unit_price,amount)
VALUES ($1,gen_random_uuid(),$2,2,'second report line',1,2,2),($1,gen_random_uuid(),$2,1,'first report line',1,3,3)`, a, usd)
	defer exec(`DELETE FROM invoice_line_items WHERE invoice_id=$1`, usd)
	exec(`INSERT INTO payments(business_id,payment_id,customer_id,amount,currency,payer_name,payment_date,intake_source,status)
VALUES ($1,gen_random_uuid(),$2,1.2345,'USD','A',$5::date-365,'MANUAL','CLEARED'),
($1,gen_random_uuid(),$2,2.3456,'IDR','A',$5::date,'MANUAL','CLEARED'),
($1,gen_random_uuid(),$2,100,'USD','A',$5::date-366,'MANUAL','CLEARED'),
($1,gen_random_uuid(),$2,100,'USD','A',$5::date,'MANUAL','PENDING_MANUAL_REVIEW'),
($3,gen_random_uuid(),$4,100,'USD','B',$5::date,'MANUAL','CLEARED')`, a, own, b, other, today)
	defer exec(`DELETE FROM payments WHERE customer_id IN ($1,$2)`, own, other)
	exec(`UPDATE payments SET invoice_id=$3 WHERE business_id=$1 AND customer_id=$2 AND currency='USD' AND amount=1.2345`, a, own, usd)

	for _, suffix := range []string{"/billing-summary", "/invoices"} {
		w1 := request("GET", base+other+suffix, good, "")
		w2 := request("GET", base+missing+suffix, good, "")
		if w1.Code != 404 || w2.Code != 404 || w1.Body.String() != w2.Body.String() {
			t.Fatalf("foreign and missing customers: %d %s / %d %s", w1.Code, w1.Body, w2.Code, w2.Body)
		}
		if w := request("GET", base+"bad"+suffix, good, ""); w.Code != 422 {
			t.Fatalf("invalid customer ID: %d %s", w.Code, w.Body)
		}
	}
	read := func(path string, out any) string {
		t.Helper()
		w := request("GET", base+path, good, "")
		if w.Code != 200 || strings.Contains(w.Body.String(), "business_id") || json.Unmarshal(w.Body.Bytes(), out) != nil {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body)
		}
		return w.Body.String()
	}
	for _, path := range []string{"balances", own + "/billing-summary", own + "/invoices"} {
		for _, token := range []string{"", foreign} {
			if w := request("GET", base+path, token, ""); w.Code != 401 {
				t.Fatalf("%s authorization: %d %s", path, w.Code, w.Body)
			}
		}
	}
	var invoices, emptyInvoices []map[string]any
	invoiceBody := read(own+"/invoices?limit=0&status=PAID", &invoices)
	read(empty+"/invoices", &emptyInvoices)
	if len(invoices) != 4 || invoices[0]["invoice_id"] != voided || invoices[1]["invoice_id"] != draft || invoices[2]["invoice_id"] != idr || invoices[3]["invoice_id"] != usd || emptyInvoices == nil || len(emptyInvoices) != 0 {
		t.Fatalf("invoice ordering or empty array: %v / %v", invoices, emptyInvoices)
	}
	lines := invoices[3]["line_items"].([]any)
	if len(lines) != 2 || lines[0].(map[string]any)["description"] != "first report line" || lines[1].(map[string]any)["description"] != "second report line" {
		t.Fatalf("invoice line order: %v", lines)
	}
	var summary, emptySummary map[string]any
	summaryBody := read(own+"/billing-summary", &summary)
	read(empty+"/billing-summary", &emptySummary)
	expectedUnpaid := []any{map[string]any{"currency": "IDR", "amount": "5.4321"}, map[string]any{"currency": "USD", "amount": "100000000000006.2235"}}
	expectedPaid := []any{map[string]any{"currency": "IDR", "amount": "2.3456"}, map[string]any{"currency": "USD", "amount": "1.2345"}}
	if !reflect.DeepEqual(summary["total_unpaid"], expectedUnpaid) || !reflect.DeepEqual(summary["paid_last_12_months"], expectedPaid) ||
		summary["last_invoice_id"] != voided || emptySummary["last_invoice_id"] != nil {
		t.Fatalf("summary totals or latest invoice: %v / %v", summary, emptySummary)
	}
	for _, key := range []string{"milestone", "recurring", "usage", "unpaid_invoices", "total_unpaid", "paid_last_12_months"} {
		if rows, ok := emptySummary[key].([]any); !ok || len(rows) != 0 {
			t.Fatalf("empty %s: %v", key, emptySummary[key])
		}
	}
	readBalances := func() map[string]string {
		t.Helper()
		var balances []map[string]any
		read("balances", &balances)
		got := map[string]string{}
		for _, row := range balances {
			if row["customer_id"] == other {
				t.Fatal("foreign customer in balances")
			}
			if row["customer_id"] == own {
				got[row["currency"].(string)] = row["balance"].(string) + ":" + row["overdue"].(string)
			}
		}
		return got
	}
	expectedBalances := map[string]string{"USD": "100000000000000.1234:100000000000000.1234", "IDR": "5.4321:0"}
	if got := readBalances(); !reflect.DeepEqual(got, expectedBalances) {
		t.Fatalf("balance totals: %v", got)
	}

	dropConstraint := func(table, name string) {
		t.Helper()
		var definition string
		if err := pool.QueryRow(ctx, `SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid=$1::regclass AND conname=$2`, table, name).Scan(&definition); err != nil {
			t.Fatal(err)
		}
		exec("ALTER TABLE " + table + " DROP CONSTRAINT " + name)
		restore = append(restore, func() { exec("ALTER TABLE " + table + " ADD CONSTRAINT " + name + " " + definition) })
	}
	dropConstraint("invoices", "invoices_customer_id_fkey")
	dropConstraint("invoice_line_items", "invoice_line_items_invoice_id_fkey")
	dropConstraint("payments", "payments_customer_id_fkey")
	dropConstraint("invoices", "invoices_project_id_fkey")
	dropConstraint("payments", "payments_invoice_id_fkey")
	dropConstraint("invoices", "invoices_source_quote_id_fkey")
	dropConstraint("invoice_line_items", "invoice_line_items_item_id_fkey")
	foreignID := func(query string) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, query, b).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	foreignInvoice := foreignID(`SELECT invoice_id::text FROM invoices WHERE business_id=$1 ORDER BY invoice_id LIMIT 1`)
	exec(`INSERT INTO payments(business_id,payment_id,customer_id,invoice_id,amount,currency,payer_name,payment_date,intake_source,status)
VALUES ($1,gen_random_uuid(),$2,$3,900,'USD','Damaged linkage',$4::date,'MANUAL','CLEARED')`, a, own, foreignInvoice, today)
	defer exec(`DELETE FROM invoices WHERE business_id=$1 AND customer_id=$2`, a, other)
	queries := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO invoices(business_id,invoice_id,customer_id,invoice_type,currency,amount,balance_due,status)
VALUES ($1,gen_random_uuid(),$4,'MILESTONE','USD',900,900,'OPEN'),($2,gen_random_uuid(),$3,'MILESTONE','USD',901,901,'OPEN')`, []any{a, b, own, other}},
		{`INSERT INTO invoice_line_items(business_id,line_item_id,invoice_id,position,description,quantity,unit_price,amount)
VALUES ($1,gen_random_uuid(),$2,0,'foreign line',1,900,900)`, []any{b, usd}},
		{`INSERT INTO payments(business_id,payment_id,customer_id,amount,currency,payer_name,payment_date,intake_source,status)
VALUES ($1,gen_random_uuid(),$2,900,'USD','B',$3::date,'MANUAL','CLEARED')`, []any{b, own, today}},
	}
	for _, q := range queries {
		exec(q.sql, q.args...)
	}
	if read(own+"/invoices", &invoices) != invoiceBody || read(own+"/billing-summary", &summary) != summaryBody ||
		!reflect.DeepEqual(readBalances(), expectedBalances) {
		t.Fatal("damaged foreign rows changed the report")
	}
	references := []struct {
		name, lookup, damage, restore string
	}{
		{"project", `SELECT project_id::text FROM projects WHERE business_id=$1 ORDER BY project_id LIMIT 1`,
			`UPDATE invoices SET project_id=$3 WHERE business_id=$1 AND invoice_id=$2`,
			`UPDATE invoices SET project_id=NULL WHERE business_id=$1 AND invoice_id=$2`},
		{"source quotation", `SELECT quotation_id::text FROM quotations WHERE business_id=$1 ORDER BY quotation_id LIMIT 1`,
			`UPDATE invoices SET source_quote_id=$3 WHERE business_id=$1 AND invoice_id=$2`,
			`UPDATE invoices SET source_quote_id=NULL WHERE business_id=$1 AND invoice_id=$2`},
		{"line item", `SELECT item_id::text FROM items WHERE business_id=$1 ORDER BY item_id LIMIT 1`,
			`UPDATE invoice_line_items SET item_id=$3 WHERE business_id=$1 AND invoice_id=$2 AND position=1`,
			`UPDATE invoice_line_items SET item_id=NULL WHERE business_id=$1 AND invoice_id=$2 AND position=1`},
	}
	for _, ref := range references {
		exec(ref.damage, a, usd, foreignID(ref.lookup))
		for _, suffix := range []string{"/invoices", "/billing-summary"} {
			w := request("GET", base+own+suffix, good, "")
			if w.Code != 500 || w.Body.String() != "{\"detail\":\"internal server error\"}\n" {
				t.Fatalf("owned invoice exposes a damaged %s on %s: %d %s", ref.name, suffix, w.Code, w.Body)
			}
		}
		exec(ref.restore, a, usd)
	}
}
