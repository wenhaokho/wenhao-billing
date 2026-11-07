package platform

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/goosemigrator"
	"github.com/pressly/goose/v3"
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
	checkRecurringRowsPlan(t, p, "../../queries/invoices.sql")
	owner := migrateRecurringRowsFixture(t, p)
	_, e = p.Exec(ctx, `WITH other AS (INSERT INTO businesses VALUES (gen_random_uuid()) RETURNING business_id)
INSERT INTO invoices(business_id,invoice_id,invoice_type,currency,amount,balance_due,status,is_template,created_at,billing_cycle_ref,issue_date)
SELECT other.business_id, md5('foreign-child'||n)::uuid,'RECURRING','USD',1,1,'OPEN',false,now(),
       jsonb_build_object('template_invoice_id',md5('template'||(1+(n%100)))::uuid::text),date '2025-01-01'+(n%20)
FROM other CROSS JOIN generate_series(1,20000) n`)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = p.Exec(ctx, "ANALYZE invoices"); e != nil {
		t.Fatal(e)
	}
	checkRecurringRowsPlan(t, p, "../../queries_v8/invoices.sql", owner)
}

func migrateRecurringRowsFixture(t *testing.T, p *pgxpool.Pool) string {
	t.Helper()
	ctx := context.Background()
	db := stdlib.OpenDBFromPool(p)
	defer db.Close()
	provider, err := goose.NewProvider(goose.DialectPostgres, db, os.DirFS("../../migrations"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(ctx, 8); err != nil {
		t.Fatal(err)
	}
	var owner string
	if err = p.QueryRow(ctx, "SELECT business_id::text FROM businesses").Scan(&owner); err != nil {
		t.Fatal(err)
	}
	return owner
}

func checkRecurringRowsPlan(t *testing.T, p *pgxpool.Pool, path string, args ...any) {
	t.Helper()
	data, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	query := strings.Split(strings.Split(string(data), "-- name: RecurringTemplateRows :many\n")[1], ";\n")[0]
	query = strings.ReplaceAll(query, "sqlc.arg(business_id)", "$1")
	var plan []byte
	if e = p.QueryRow(context.Background(), "EXPLAIN (ANALYZE, FORMAT JSON) "+query, args...).Scan(&plan); e != nil {
		t.Fatal(e)
	}
	var decoded []map[string]any
	if e = json.Unmarshal(plan, &decoded); e != nil {
		t.Fatal(e)
	}
	var childScanSeen bool
	var visit func(map[string]any)
	visit = func(n map[string]any) {
		if n["Relation Name"] == "invoices" {
			t.Logf("invoice scan: node=%v loops=%v rows=%v", n["Node Type"], n["Actual Loops"], n["Actual Rows"])
			rowCount := n["Actual Rows"].(float64)
			const expectedChildren = 20000 - 20000/11
			childScanSeen = childScanSeen || rowCount == expectedChildren
			if len(args) != 0 && rowCount > expectedChildren {
				t.Errorf("foreign children reached ranked scan: %v", n["Actual Rows"])
			}
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
	if !childScanSeen {
		t.Error("ranked child scan did not return the expected number of owned non-void rows")
	}
}
