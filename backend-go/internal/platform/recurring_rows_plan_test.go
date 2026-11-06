package platform

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/goosemigrator"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

func TestRecurringRowsSingleChildScan(t *testing.T) {
	u, e := url.Parse(os.Getenv("PGTESTDB_URL"))
	if e != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	db := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	ctx := context.Background()
	p, e := pgxpool.New(ctx, db.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	_, e = p.Exec(ctx, `INSERT INTO invoices(invoice_id,invoice_type,currency,amount,balance_due,status,is_template,created_at,billing_cycle_ref,issue_date)
 SELECT md5('template'||n)::uuid,'RECURRING','USD',1,1,'DRAFT',true, timestamp '2025-01-01' + n * interval '1 second','{"frequency":"DAILY","start_date":"2025-01-01"}',NULL FROM generate_series(1,100) n;
 INSERT INTO invoices(invoice_id,invoice_type,currency,amount,balance_due,status,is_template,created_at,billing_cycle_ref,issue_date)
 SELECT md5('child'||n)::uuid,'RECURRING','USD',1,1,CASE WHEN n%11=0 THEN 'VOID' ELSE 'OPEN' END,false,now(),jsonb_build_object('template_invoice_id',md5('template'||(1+(n%100)))::uuid::text,'cycle_key','2025-01-01'),date '2025-01-01'+(n%20) FROM generate_series(1,20000) n; ANALYZE invoices`)
	if e != nil {
		t.Fatal(e)
	}
	rows, e := dbquery.New(p).RecurringTemplateRows(ctx)
	if e != nil || len(rows) != 100 {
		t.Fatalf("rows=%d error=%v", len(rows), e)
	}
	for _, row := range rows {
		var count int64
		var latest string
		e = p.QueryRow(ctx, `SELECT count(*), max(issue_date)::text FROM invoices WHERE NOT is_template AND invoice_type='RECURRING' AND status<>'VOID' AND billing_cycle_ref->>'template_invoice_id'=$1::uuid::text`, row.InvoiceID).Scan(&count, &latest)
		if e != nil || count != row.GeneratedCount || !row.PreviousIssueDate.Valid || row.PreviousIssueDate.Time.Format("2006-01-02") != latest {
			t.Fatalf("incorrect child aggregate: row=%+v count=%d latest=%s error=%v", row, count, latest, e)
		}
	}
	data, e := os.ReadFile("../../queries/invoices.sql")
	if e != nil {
		t.Fatal(e)
	}
	query := strings.Split(strings.Split(string(data), "-- name: RecurringTemplateRows :many\n")[1], ";\n")[0]
	var plan []byte
	if e = p.QueryRow(ctx, "EXPLAIN (ANALYZE, FORMAT JSON) "+query).Scan(&plan); e != nil {
		t.Fatal(e)
	}
	var decoded []map[string]any
	if e = json.Unmarshal(plan, &decoded); e != nil {
		t.Fatal(e)
	}
	var visit func(map[string]any)
	visit = func(n map[string]any) {
		if n["Relation Name"] == "invoices" {
			t.Logf("invoice scan: node=%v loops=%v rows=%v", n["Node Type"], n["Actual Loops"], n["Actual Rows"])
		}
		if n["Relation Name"] == "invoices" && n["Actual Loops"].(float64) > 1 {
			t.Errorf("invoice scan repeated %.0f times", n["Actual Loops"])
		}
		if a, ok := n["Plans"].([]any); ok {
			for _, v := range a {
				visit(v.(map[string]any))
			}
		}
	}
	visit(decoded[0]["Plan"].(map[string]any))
}
