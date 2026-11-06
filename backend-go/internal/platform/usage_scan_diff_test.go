package platform

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/goosemigrator"
	"github.com/shopspring/decimal"
	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

func TestUsageScanPythonDifferential(t *testing.T) {
	u, err := url.Parse(os.Getenv("PGTESTDB_URL"))
	if err != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	cfg := pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}
	mig := goosemigrator.New(".", goosemigrator.WithFS(migrations.Files))
	gd := pgtestdb.Custom(t, cfg, mig)
	pd := pgtestdb.Custom(t, cfg, mig)
	var pools []*pgxpool.Pool
	for _, d := range []string{gd.URL(), pd.URL()} {
		p, e := pgxpool.New(context.Background(), d)
		if e != nil {
			t.Fatal(e)
		}
		defer p.Close()
		pools = append(pools, p)
	}
	n := 0
	for _, typ := range []string{"USAGE", "MILESTONE", "RECURRING"} {
		for _, status := range []string{"DRAFT", "OPEN", "SENT", "PARTIAL", "PAID", "VOID"} {
			for _, currency := range []string{"USD", "IDR"} {
				for _, ref := range []string{`{"cut_off_day":31}`, `{"cut_off_day":30}`, `{"cut_off_day":31,"locked":true}`, `{}`} {
					n++
					id := fmt.Sprintf("30000000-0000-0000-0000-%012d", n)
					for _, p := range pools {
						_, err = p.Exec(context.Background(), `INSERT INTO invoices(invoice_id,invoice_type,currency,amount,balance_due,status,wht_type,wht_rate,wht_gross_up,billing_cycle_ref) VALUES ($1,$2,$3,1,1,$4,'PPH23',2,true,$5::jsonb)`, id, typ, currency, status, ref)
						if err != nil {
							t.Fatal(err)
						}
					}
				}
			}
		}
	}
	cmd := exec.Command("python", "-c", `import os
from datetime import date
from decimal import Decimal
from sqlalchemy import create_engine
from sqlalchemy.orm import sessionmaker
from app.workers.tasks import usage_lock as worker
class FrozenDate(date):
 @classmethod
 def today(cls): return cls(2026,11,1)
worker.date=FrozenDate
worker.SessionLocal=sessionmaker(bind=create_engine(os.environ['DB'].replace('postgres://','postgresql+psycopg://',1)))
print(worker.cutoff_scan(lambda _: Decimal('100.0050')))
`)
	cmd.Dir = "../../../backend"
	cmd.Env = append(os.Environ(), "DB="+pd.URL())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("python: %s %v", out, err)
	}
	meter := UsageMeterFunc(func(context.Context, pgtype.UUID) (decimal.Decimal, error) {
		return decimal.RequireFromString("100.0050"), nil
	})
	count, err := ScanUsageCutoff(context.Background(), pools[0], time.Date(2026, 11, 1, 3, 0, 0, 0, time.UTC), meter)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(out)) != fmt.Sprint(count) {
		t.Fatalf("count python %s go %d", out, count)
	}
	var snapshots [2][]string
	for i, p := range pools {
		rows, e := p.Query(context.Background(), `SELECT invoice_id::text, amount::text, balance_due::text, coalesce(wht_base::text, 'null'), coalesce(wht_amount::text, 'null'), billing_cycle_ref::text FROM invoices ORDER BY invoice_id`)
		if e != nil {
			t.Fatal(e)
		}
		for rows.Next() {
			var id, a, b, c, d, r string
			if e = rows.Scan(&id, &a, &b, &c, &d, &r); e != nil {
				t.Fatal(e)
			}
			snapshots[i] = append(snapshots[i], strings.Join([]string{id, a, b, c, d, r}, "|"))
		}
		if e = rows.Err(); e != nil {
			t.Fatal(e)
		}
		rows.Close()
	}
	if !reflect.DeepEqual(snapshots[0], snapshots[1]) {
		for i := range snapshots[0] {
			if snapshots[0][i] != snapshots[1][i] {
				t.Fatalf("snapshot go=%s python=%s", snapshots[0][i], snapshots[1][i])
			}
		}
	}
	for _, tc := range []struct {
		name, now, ref     string
		fail, defaultMeter bool
	}{
		{"february", "2025-03-01", `{"cut_off_day":28}`, false, false},
		{"leap", "2024-03-01", `{"cut_off_day":29}`, false, false},
		{"month start", "2026-12-01", `{"cut_off_day":"30"}`, false, false},
		{"missing", "2026-12-01", `{}`, false, false},
		{"malformed cutoff", "2026-12-01", `{"cut_off_day":"bad"}`, false, false},
		{"malformed ref", "2026-12-01", `[1]`, false, false},
		{"meter error", "2026-12-01", `{"cut_off_day":30}`, true, false},
		{"default meter", "2026-12-01", `{"cut_off_day":30}`, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, p := range pools {
				if _, e := p.Exec(context.Background(), `DELETE FROM invoices`); e != nil {
					t.Fatal(e)
				}
				_, e := p.Exec(context.Background(), `INSERT INTO invoices(invoice_id,invoice_type,currency,amount,balance_due,status,wht_type,wht_rate,wht_gross_up,billing_cycle_ref) VALUES ('30000000-0000-0000-0000-000000000001','USAGE','IDR',1,1,'DRAFT','PPH23',2,true,$1::jsonb)`, tc.ref)
				if e != nil {
					t.Fatal(e)
				}
			}
			script := `import os
from datetime import date
from decimal import Decimal
from sqlalchemy import create_engine
from sqlalchemy.orm import sessionmaker
from app.workers.tasks import usage_lock as worker
class FrozenDate(date):
 @classmethod
 def today(cls): return cls.fromisoformat(os.environ['NOW'])
worker.date=FrozenDate
worker.SessionLocal=sessionmaker(bind=create_engine(os.environ['DB'].replace('postgres://','postgresql+psycopg://',1)))
def meter(_):
 if os.environ['FAIL']=='true': raise RuntimeError('meter failure')
 return Decimal('100.0050')
try:
 print('ok',worker.cutoff_scan() if os.environ['DEFAULT']=='true' else worker.cutoff_scan(meter))
except Exception: print('error')
`
			cmd := exec.Command("python", "-c", script)
			cmd.Dir = "../../../backend"
			cmd.Env = append(os.Environ(), "DB="+pd.URL(), "NOW="+tc.now, fmt.Sprintf("FAIL=%t", tc.fail), fmt.Sprintf("DEFAULT=%t", tc.defaultMeter))
			out, e := cmd.CombinedOutput()
			if e != nil {
				t.Fatalf("python %s %v", out, e)
			}
			date, e := time.Parse("2006-01-02", tc.now)
			if e != nil {
				t.Fatal(e)
			}
			var m UsageMeter = meter
			if tc.fail {
				m = UsageMeterFunc(func(context.Context, pgtype.UUID) (decimal.Decimal, error) {
					return decimal.Zero, errors.New("meter error")
				})
			}
			if tc.defaultMeter {
				m = nil
			}
			count, e := ScanUsageCutoff(context.Background(), pools[0], date, m)
			want := fmt.Sprintf("ok %d", count)
			if e != nil {
				want = "error"
			}
			if strings.TrimSpace(string(out)) != want {
				t.Fatalf("outcomes python %s go %s (%v)", out, want, e)
			}
			var snaps [2]string
			for i, p := range pools {
				if e = p.QueryRow(context.Background(), `SELECT json_build_array(amount,balance_due,wht_base,wht_amount,billing_cycle_ref)::text FROM invoices`).Scan(&snaps[i]); e != nil {
					t.Fatal(e)
				}
			}
			if snaps[0] != snaps[1] {
				t.Fatalf("snapshot go %s python %s", snaps[0], snaps[1])
			}
		})
	}

}
