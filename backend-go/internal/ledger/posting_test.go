package ledger

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/goosemigrator"
	"github.com/shopspring/decimal"
	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

func TestPostPaymentSameCurrency(t *testing.T) {
	u, e := url.Parse(os.Getenv("PGTESTDB_URL"))
	if e != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	db := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	ctx := context.Background()
	c, e := pgx.Connect(ctx, db.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close(ctx)

	tx, e := c.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	sourceID, _ := newID()
	id, e := PostPayment(ctx, tx, PaymentInput{PaymentID: sourceID, Status: "CLEARED", Amount: decimal.NewFromInt(10), Currency: "IDR", PaymentDate: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), InvoiceCurrency: "IDR", BaseCurrency: "IDR"})
	if e != nil {
		t.Fatal(e)
	}
	var n int
	e = tx.QueryRow(ctx, `SELECT count(*) FROM journal_lines WHERE entry_id=$1`, id).Scan(&n)
	if e != nil || n != 2 {
		t.Fatalf("lines %d: %v", n, e)
	}
}

func TestPostingAndReversalScenarios(t *testing.T) {
	u, e := url.Parse(os.Getenv("PGTESTDB_URL"))
	if e != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	db := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	ctx := context.Background()
	c, e := pgx.Connect(ctx, db.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close(ctx)
	_, e = c.Exec(ctx, `INSERT INTO fx_rates(from_currency,to_currency,rate,as_of_date) VALUES ('USD','IDR',2,'2020-01-01')`)
	if e != nil {
		t.Fatal(e)
	}
	tx, e := c.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	source, _ := newID()
	d := func(s string) decimal.Decimal { return decimal.RequireFromString(s) }
	base := PaymentInput{PaymentID: source, Status: "CLEARED", Amount: d("10"), Currency: "USD", InvoiceCurrency: "IDR", BaseCurrency: "IDR", InvoiceBalance: d("15"), PaymentDate: time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC)}
	for _, tc := range []struct {
		name     string
		change   func(*PaymentInput)
		wantCode string
		want     string
	}{
		{"gain", func(p *PaymentInput) {}, "4900", "5"},
		{"loss", func(p *PaymentInput) { p.InvoiceBalance = d("25") }, "5900", "5"},
		{"no plug", func(p *PaymentInput) { p.InvoiceBalance = d("20") }, "", ""},
		{"withholding", func(p *PaymentInput) { p.Withheld = d("2") }, "1300", "2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := base
			tc.change(&p)
			id, e := PostPayment(ctx, tx, p)
			if e != nil {
				t.Fatal(e)
			}
			var n int
			e = tx.QueryRow(ctx, `SELECT count(*) FROM journal_lines l JOIN chart_of_accounts a USING(account_id) WHERE l.entry_id=$1 AND a.code IN ('4900','5900')`, id).Scan(&n)
			if e != nil {
				t.Fatal(e)
			}
			if tc.wantCode == "" && n != 0 {
				t.Fatalf("unexpected plug: %d", n)
			}
			if tc.wantCode != "" {
				var amount string
				e = tx.QueryRow(ctx, `SELECT (l.debit+l.credit)::text FROM journal_lines l JOIN chart_of_accounts a USING(account_id) WHERE l.entry_id=$1 AND a.code=$2`, id, tc.wantCode).Scan(&amount)
				if e != nil || !d(amount).Equal(d(tc.want)) {
					t.Fatalf("amount %q err %v", amount, e)
				}
			}
		})
	}
	for _, tc := range []struct {
		name   string
		change func(*PaymentInput)
	}{{"status", func(p *PaymentInput) { p.Status = "PENDING" }}, {"overprecision", func(p *PaymentInput) { p.Amount = d("1.00001") }}, {"negative withholding", func(p *PaymentInput) { p.Withheld = d("-1") }}, {"invalid balance", func(p *PaymentInput) { p.InvoiceBalance = d("0") }}, {"missing fx", func(p *PaymentInput) { p.Currency = "EUR" }}} {
		t.Run(tc.name, func(t *testing.T) {
			p := base
			tc.change(&p)
			var before, after int
			tx.QueryRow(ctx, `SELECT count(*) FROM journal_entries`).Scan(&before)
			if _, e := PostPayment(ctx, tx, p); e == nil {
				t.Fatal("expected error")
			} else if tc.name == "invalid balance" && !strings.Contains(e.Error(), "cross-currency invoice balance") {
				t.Fatalf("wrong error: %v", e)
			}
			tx.QueryRow(ctx, `SELECT count(*) FROM journal_entries`).Scan(&after)
			if before != after {
				t.Fatal("partial entry")
			}
		})
	}
	// Python ledger.py at USD/IDR=2: DR bank USD 10 (base 20),
	// CR AR IDR 15 (base 15), CR FX gain IDR 5 (base 5).
	parityID, e := PostPayment(ctx, tx, base)
	if e != nil {
		t.Fatal(e)
	}
	rows, e := tx.Query(ctx, `SELECT a.code,l.currency,l.debit::text,l.credit::text,l.base_amount_debit::text,l.base_amount_credit::text,l.fx_rate::text FROM journal_lines l JOIN chart_of_accounts a USING(account_id) WHERE l.entry_id=$1 ORDER BY a.code`, parityID)
	if e != nil {
		t.Fatal(e)
	}
	var got []string
	for rows.Next() {
		var code, currency, dr, cr, bdr, bcr, rate string
		if e = rows.Scan(&code, &currency, &dr, &cr, &bdr, &bcr, &rate); e != nil {
			t.Fatal(e)
		}
		got = append(got, strings.Join([]string{code, currency, dr, cr, bdr, bcr, rate}, ":"))
	}
	rows.Close()
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	want := []string{"1000:USD:10.0000:0.0000:20.0000:0.0000:2.00000000", "1100:IDR:0.0000:15.0000:0.0000:15.0000:1.00000000", "4900:IDR:0.0000:5.0000:0.0000:5.0000:1.00000000"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("journal lines: %v", got)
	}
	bad, _ := newID()
	badLine, _ := newID()
	_, e = tx.Exec(ctx, `INSERT INTO journal_entries(entry_id,source_type,source_id) VALUES ($1,'PAYMENT',$2)`, bad, source)
	if e != nil {
		t.Fatal(e)
	}
	_, e = tx.Exec(ctx, `INSERT INTO journal_lines(line_id,entry_id,account_id,debit,credit,currency,base_amount_debit,base_amount_credit,fx_rate) SELECT $1,$2,account_id,1,0,'IDR',1,0,1 FROM chart_of_accounts WHERE code='1000'`, badLine, bad)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = Reverse(ctx, tx, bad, ""); e == nil {
		t.Fatal("unbalanced reversal accepted")
	}
	missing, _ := newID()
	if _, e = Reverse(ctx, tx, missing, ""); e == nil {
		t.Fatal("missing entry reversed")
	}
	id, e := PostPayment(ctx, tx, base)
	if e != nil {
		t.Fatal(e)
	}
	rev, e := Reverse(ctx, tx, id, "")
	if e != nil {
		t.Fatal(e)
	}
	var unmatched int
	e = tx.QueryRow(ctx, `SELECT count(*) FROM journal_lines a WHERE a.entry_id=$1 AND NOT EXISTS (SELECT 1 FROM journal_lines b WHERE b.entry_id=$2 AND b.account_id=a.account_id AND b.currency=a.currency AND b.debit=a.credit AND b.credit=a.debit AND b.base_amount_debit=a.base_amount_credit AND b.base_amount_credit=a.base_amount_debit AND b.fx_rate=a.fx_rate)`, id, rev).Scan(&unmatched)
	if e != nil || unmatched != 0 {
		t.Fatalf("unmatched %d %v", unmatched, e)
	}
	if _, e = Reverse(ctx, tx, id, ""); e == nil {
		t.Fatal("duplicate reversal accepted")
	}
}

func TestFractionalBaseRounding(t *testing.T) {
	u, _ := url.Parse(os.Getenv("PGTESTDB_URL"))
	if u == nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	db := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	ctx := context.Background()
	c, e := pgx.Connect(ctx, db.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close(ctx)
	_, e = c.Exec(ctx, `INSERT INTO fx_rates(from_currency,to_currency,rate,as_of_date) VALUES ('USD','IDR',0.5,'2020-01-01')`)
	if e != nil {
		t.Fatal(e)
	}
	tx, e := c.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	source, _ := newID()
	id, e := PostPayment(ctx, tx, PaymentInput{PaymentID: source, Status: "CLEARED", Currency: "USD", InvoiceCurrency: "USD", BaseCurrency: "IDR", Amount: decimal.RequireFromString("0.0001"), Withheld: decimal.RequireFromString("0.0001"), PaymentDate: time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC)})
	if e != nil {
		t.Fatal(e)
	}
	var credit string
	e = tx.QueryRow(ctx, `SELECT base_amount_credit::text FROM journal_lines l JOIN chart_of_accounts a USING(account_id) WHERE entry_id=$1 AND a.code='1100'`, id).Scan(&credit)
	if e != nil || credit != "0.0000" {
		t.Fatalf("credit %s %v", credit, e)
	}
}

func TestMissingAccountLeavesNoEntry(t *testing.T) {
	u, _ := url.Parse(os.Getenv("PGTESTDB_URL"))
	if u == nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	db := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	ctx := context.Background()
	c, e := pgx.Connect(ctx, db.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close(ctx)
	tx, e := c.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	_, e = tx.Exec(ctx, `DELETE FROM chart_of_accounts WHERE code='1100'`)
	if e != nil {
		t.Fatal(e)
	}
	source, _ := newID()
	_, e = PostPayment(ctx, tx, PaymentInput{PaymentID: source, Status: "CLEARED", Currency: "IDR", InvoiceCurrency: "IDR", BaseCurrency: "IDR", Amount: decimal.NewFromInt(1), PaymentDate: time.Now()})
	if e == nil || !strings.Contains(e.Error(), "chart of accounts missing code 1100") {
		t.Fatalf("missing account: %v", e)
	}
	var n int
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM journal_entries`).Scan(&n); e != nil || n != 0 {
		t.Fatalf("entries %d %v", n, e)
	}
}

func TestConcurrentReversal(t *testing.T) {
	u, _ := url.Parse(os.Getenv("PGTESTDB_URL"))
	if u == nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	db := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	ctx := context.Background()
	c, e := pgx.Connect(ctx, db.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close(ctx)
	tx, e := c.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	source, _ := newID()
	id, e := PostPayment(ctx, tx, PaymentInput{PaymentID: source, Status: "CLEARED", Currency: "IDR", InvoiceCurrency: "IDR", BaseCurrency: "IDR", Amount: decimal.NewFromInt(10), PaymentDate: time.Now()})
	if e != nil {
		t.Fatal(e)
	}
	if e = tx.Commit(ctx); e != nil {
		t.Fatal(e)
	}
	results := make(chan error, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		go func() {
			conn, err := pgx.Connect(ctx, db.URL())
			if err != nil {
				results <- err
				return
			}
			defer conn.Close(ctx)
			tx, err := conn.Begin(ctx)
			if err != nil {
				results <- err
				return
			}
			defer tx.Rollback(ctx)
			<-start
			_, err = Reverse(ctx, tx, id, "")
			if err == nil {
				err = tx.Commit(ctx)
			}
			results <- err
		}()
	}
	close(start)
	a, b := <-results, <-results
	if (a == nil) == (b == nil) {
		t.Fatalf("expected one success, got %v and %v", a, b)
	}
	var n int
	e = c.QueryRow(ctx, `SELECT count(*) FROM journal_entries`).Scan(&n)
	if e != nil || n != 2 {
		t.Fatalf("entries %d %v", n, e)
	}
}

func TestInsertFailureRollsBackSavepoint(t *testing.T) {
	u, _ := url.Parse(os.Getenv("PGTESTDB_URL"))
	if u == nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	db := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	ctx := context.Background()
	c, e := pgx.Connect(ctx, db.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close(ctx)
	tx, e := c.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	source, _ := newID()
	_, e = PostPayment(ctx, tx, PaymentInput{PaymentID: source, Status: "CLEARED", Currency: "IDR", InvoiceCurrency: "IDR", BaseCurrency: "IDR", Amount: decimal.RequireFromString("1000000000000000"), PaymentDate: time.Now()})
	if e == nil {
		t.Fatal("expected numeric overflow")
	}
	var n int
	e = tx.QueryRow(ctx, `SELECT count(*) FROM journal_entries`).Scan(&n)
	if e != nil || n != 0 {
		t.Fatalf("partial entry %d %v", n, e)
	}
}

func TestCallerRollbackRemovesPosting(t *testing.T) {
	u, e := url.Parse(os.Getenv("PGTESTDB_URL"))
	if e != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	db := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	ctx := context.Background()
	c, e := pgx.Connect(ctx, db.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close(ctx)
	tx, e := c.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	source, _ := newID()
	id, e := PostPayment(ctx, tx, PaymentInput{PaymentID: source, Status: "CLEARED", Currency: "IDR", InvoiceCurrency: "IDR", BaseCurrency: "IDR", Amount: decimal.NewFromInt(10), PaymentDate: time.Now()})
	if e != nil {
		t.Fatal(e)
	}
	if e = tx.Rollback(ctx); e != nil {
		t.Fatal(e)
	}
	var entries, lines int
	e = c.QueryRow(ctx, `SELECT (SELECT count(*) FROM journal_entries WHERE entry_id=$1),(SELECT count(*) FROM journal_lines WHERE entry_id=$1)`, id).Scan(&entries, &lines)
	if e != nil || entries != 0 || lines != 0 {
		t.Fatalf("persisted entries=%d lines=%d: %v", entries, lines, e)
	}
}

type parityEntry struct {
	SourceType string     `json:"source_type"`
	SourceID   string     `json:"source_id"`
	Memo       string     `json:"memo"`
	Lines      [][]string `json:"lines"`
}

func TestPythonParity(t *testing.T) {
	u, e := url.Parse(os.Getenv("PGTESTDB_URL"))
	if e != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	cfg := pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}
	migrator := goosemigrator.New(".", goosemigrator.WithFS(migrations.Files))
	pydb := pgtestdb.Custom(t, cfg, migrator)
	godb := pgtestdb.Custom(t, cfg, migrator)
	script, e := filepath.Abs("testdata/parity.py")
	if e != nil {
		t.Fatal(e)
	}
	backend, e := filepath.Abs("../../../backend")
	if e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command("python", script, pydb.URL())
	cmd.Env = append(os.Environ(), "PYTHONPATH="+backend, "BASE_CURRENCY=IDR")
	output, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("python parity: %v: %s", e, output)
	}
	var want map[string]parityEntry
	if e = json.Unmarshal(output, &want); e != nil {
		t.Fatalf("python output %s: %v", output, e)
	}
	ctx := context.Background()
	c, e := pgx.Connect(ctx, godb.URL())
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close(ctx)
	_, e = c.Exec(ctx, `INSERT INTO fx_rates(from_currency,to_currency,rate,as_of_date) VALUES ('USD','IDR',2,'2020-01-01')`)
	if e != nil {
		t.Fatal(e)
	}
	tx, e := c.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	var source, invoice pgtype.UUID
	if e = source.Scan("11111111-1111-4111-8111-111111111111"); e != nil {
		t.Fatal(e)
	}
	if e = invoice.Scan("22222222-2222-4222-8222-222222222222"); e != nil {
		t.Fatal(e)
	}
	d := decimal.RequireFromString
	for _, tc := range []struct{ name, pc, ic, amount, withheld, balance string }{{"same", "IDR", "IDR", "10", "0", "0"}, {"withheld", "USD", "USD", "10", "0.0001", "0"}, {"gain", "USD", "IDR", "10", "0", "15"}, {"loss", "USD", "IDR", "10", "0", "25"}} {
		id, e := PostPayment(ctx, tx, PaymentInput{PaymentID: source, InvoiceID: invoice, Status: "CLEARED", Currency: tc.pc, InvoiceCurrency: tc.ic, BaseCurrency: "IDR", Amount: d(tc.amount), Withheld: d(tc.withheld), InvoiceBalance: d(tc.balance), PaymentDate: time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC)})
		if e != nil {
			t.Fatal(e)
		}
		rev, e := Reverse(ctx, tx, id, "")
		if e != nil {
			t.Fatal(e)
		}
		for _, v := range []struct {
			kind string
			id   pgtype.UUID
		}{{"post", id}, {"reverse", rev}} {
			var got parityEntry
			e = tx.QueryRow(ctx, `SELECT source_type,source_id::text,memo FROM journal_entries WHERE entry_id=$1`, v.id).Scan(&got.SourceType, &got.SourceID, &got.Memo)
			if e != nil {
				t.Fatal(e)
			}
			rows, e := tx.Query(ctx, `SELECT a.code,l.currency,l.debit::text,l.credit::text,l.base_amount_debit::text,l.base_amount_credit::text,l.fx_rate::text FROM journal_lines l JOIN chart_of_accounts a USING(account_id) WHERE l.entry_id=$1 ORDER BY a.code`, v.id)
			if e != nil {
				t.Fatal(e)
			}
			for rows.Next() {
				var a, b, c, d, e, f, g string
				if err := rows.Scan(&a, &b, &c, &d, &e, &f, &g); err != nil {
					t.Fatal(err)
				}
				got.Lines = append(got.Lines, []string{a, b, c, d, e, f, g})
			}
			rows.Close()
			if e = rows.Err(); e != nil {
				t.Fatal(e)
			}
			key := tc.name + "_" + v.kind
			if v.kind == "reverse" {
				got.Memo = strings.ReplaceAll(got.Memo, uuidString(id), "<entry>")
			}
			if !reflect.DeepEqual(got, want[key]) {
				t.Fatalf("%s: Go=%+v Python=%+v", key, got, want[key])
			}
		}
	}
}
