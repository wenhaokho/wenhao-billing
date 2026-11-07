package trialbalance

import (
	"encoding/json"
	"github.com/shopspring/decimal"
	"os"
	"os/exec"
	"testing"
	"time"
)

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }
func day(s string) time.Time {
	v, e := time.Parse(time.RFC3339, s)
	if e != nil {
		panic(e)
	}
	return v
}
func fixture() []Line {
	return []Line{
		{2, "2000", "Payable", "LIABILITY", "USD", day("2026-01-02T23:59:59Z"), dec("0"), dec("1.2345"), dec("0"), dec("20000.1234")},
		{1, "1000", "Cash", "ASSET", "USD", day("2026-01-02T00:00:00Z"), dec("1.2345"), dec("0"), dec("20000.1234"), dec("0")},
		{1, "1000", "Cash", "ASSET", "IDR", day("2026-01-01T12:00:00Z"), dec("0.0001"), dec("0"), dec("0.0001"), dec("0")},
		{1, "1000", "Cash", "ASSET", "USD", day("2026-01-03T00:00:00Z"), dec("99"), dec("0"), dec("99"), dec("0")},
	}
}
func TestAggregate(t *testing.T) {
	r, e := Aggregate(day("2026-01-02T12:00:00Z"), "IDR", fixture())
	if e != nil {
		t.Fatal(e)
	}
	if len(r.Rows) != 3 || r.Rows[0].Currency != "IDR" || r.Rows[1].Currency != "USD" || r.Rows[2].Code != "2000" {
		t.Fatalf("rows: %+v", r.Rows)
	}
	if !r.TotalDebitBase.Equal(dec("20000.1235")) || !r.TotalCreditBase.Equal(dec("20000.1234")) || !r.Rows[2].Net.Equal(dec("-1.2345")) {
		t.Fatalf("report: %+v", r)
	}
	empty, e := Aggregate(day("2026-01-01T00:00:00Z"), "IDR", nil)
	if e != nil || empty.Rows == nil || len(empty.Rows) != 0 || !empty.TotalDebitBase.IsZero() {
		t.Fatalf("empty: %+v %v", empty, e)
	}
	zero := fixture()[0]
	zero.Credit = decimal.Zero
	zero.BaseCredit = decimal.Zero
	z, e := Aggregate(day("2026-01-02T00:00:00Z"), "IDR", []Line{zero})
	if e != nil || len(z.Rows) != 1 || !z.Rows[0].Net.IsZero() {
		t.Fatalf("zero: %+v %v", z, e)
	}
}
func TestInvalid(t *testing.T) {
	for _, mutate := range []func(*Line){func(l *Line) { l.Currency = "usd" }, func(l *Line) { l.AccountID = 0 }, func(l *Line) { l.Debit = dec("-1") }, func(l *Line) { l.PostedAt = time.Time{} }} {
		l := fixture()[0]
		mutate(&l)
		if _, e := Aggregate(day("2026-01-02T00:00:00Z"), "IDR", []Line{l}); e == nil {
			t.Fatalf("accepted %+v", l)
		}
	}
	if _, e := Aggregate(time.Time{}, "IDR", nil); e == nil {
		t.Fatal("accepted zero date")
	}
	if _, e := Aggregate(day("2026-01-02T00:00:00Z"), "idr", nil); e == nil {
		t.Fatal("accepted invalid base currency")
	}
	lines := fixture()
	lines[1].Name = "Other"
	if _, e := Aggregate(day("2026-01-02T00:00:00Z"), "IDR", lines); e == nil {
		t.Fatal("accepted inconsistent metadata")
	}
}
func TestPythonDifferential(t *testing.T) {
	python := os.Getenv("BILLING_PYTHON")
	if python == "" {
		python = "/tmp/codex-billing-py312/bin/python"
	}
	if _, e := os.Stat(python); e != nil {
		t.Skip("set BILLING_PYTHON to a Python environment with backend dependencies")
	}
	script := `import json
from datetime import date
from sqlalchemy import create_engine, text
from sqlalchemy.orm import Session
from app.api.v1.routers.accounting import trial_balance
engine = create_engine('sqlite://')
with engine.begin() as c:
 c.execute(text('CREATE TABLE chart_of_accounts (account_id INTEGER, code TEXT, name TEXT, type TEXT)'))
 c.execute(text('CREATE TABLE journal_entries (entry_id TEXT, posted_at TIMESTAMP)'))
 c.execute(text('CREATE TABLE journal_lines (entry_id TEXT, account_id INTEGER, currency TEXT, debit NUMERIC, credit NUMERIC, base_amount_debit NUMERIC, base_amount_credit NUMERIC)'))
 c.execute(text("INSERT INTO chart_of_accounts VALUES (1,'1000','Cash','ASSET'),(2,'2000','Payable','LIABILITY')"))
 c.execute(text("INSERT INTO journal_entries VALUES ('a','2026-01-02 23:59:59'),('b','2026-01-02 00:00:00'),('c','2026-01-01 12:00:00'),('d','2026-01-03 00:00:00')"))
 c.execute(text("INSERT INTO journal_lines VALUES ('a',2,'USD',0,1.2345,0,20000.1234),('b',1,'USD',1.2345,0,20000.1234,0),('c',1,'IDR',0.0001,0,0.0001,0),('d',1,'USD',99,0,99,0)"))
with Session(engine) as db:
 print(json.dumps(trial_balance(as_of=date(2026,1,2),db=db,_=None).model_dump(mode='json')))
`
	cmd := exec.Command(python, "-c", script)
	cmd.Dir = "../../../backend"
	cmd.Env = append(os.Environ(), "BASE_CURRENCY=IDR")
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("python: %v\n%s", e, out)
	}
	var expected struct {
		Rows []struct {
			AccountID                  int64 `json:"account_id"`
			Code, Name, Type, Currency string
			Debit, Credit, Net         decimal.Decimal
			BaseDebit                  decimal.Decimal `json:"base_debit"`
			BaseCredit                 decimal.Decimal `json:"base_credit"`
			BaseNet                    decimal.Decimal `json:"base_net"`
		} `json:"rows"`
		TotalDebitBase  decimal.Decimal `json:"total_debit_base"`
		TotalCreditBase decimal.Decimal `json:"total_credit_base"`
	}
	if e = json.Unmarshal(out, &expected); e != nil {
		t.Fatalf("%v: %s", e, out)
	}
	got, e := Aggregate(day("2026-01-02T00:00:00Z"), "IDR", fixture())
	if e != nil {
		t.Fatal(e)
	}
	if len(got.Rows) != len(expected.Rows) {
		t.Fatalf("rows: %+v vs %+v", got.Rows, expected.Rows)
	}
	for i, r := range got.Rows {
		p := expected.Rows[i]
		if r.AccountID != p.AccountID || r.Code != p.Code || r.Name != p.Name || r.Type != p.Type || r.Currency != p.Currency || !r.Debit.Equal(p.Debit) || !r.Credit.Equal(p.Credit) || !r.Net.Equal(p.Net) || !r.BaseDebit.Equal(p.BaseDebit) || !r.BaseCredit.Equal(p.BaseCredit) || !r.BaseNet.Equal(p.BaseNet) {
			t.Fatalf("row %d: %+v vs %+v", i, r, p)
		}
	}
	if !got.TotalDebitBase.Equal(expected.TotalDebitBase) || !got.TotalCreditBase.Equal(expected.TotalCreditBase) {
		t.Fatalf("totals: %+v vs %+v", got, expected)
	}
}

func TestExactAggregationAndBounds(t *testing.T) {
	a := fixture()[1]
	a.Debit = dec("999999999999999.9999")
	a.BaseDebit = dec("999999999999999.9999")
	b := a
	b.Debit = dec("0.0001")
	b.BaseDebit = dec("0.0001")
	r, e := Aggregate(day("2026-01-02T00:00:00Z"), "IDR", []Line{a, b})
	if e != nil {
		t.Fatal(e)
	}
	if len(r.Rows) != 1 || !r.Rows[0].Debit.Equal(dec("1000000000000000.0000")) || !r.TotalDebitBase.Equal(dec("1000000000000000.0000")) {
		t.Fatalf("incorrect exact sum: %+v", r)
	}
	for _, v := range []decimal.Decimal{decimal.New(1, 1000000000), decimal.New(1, -1000000000), dec("1000000000000000"), dec("0.00001")} {
		a.Debit = v
		if _, e := Aggregate(day("2026-01-02T00:00:00Z"), "IDR", []Line{a}); e == nil {
			t.Fatal("accepted out-of-range decimal")
		}
	}
}
func TestFutureCorruptionDoesNotAffectHistoricalReport(t *testing.T) {
	lines := fixture()
	lines[3].Currency = "invalid"
	lines[3].Name = "conflict"
	lines[3].Debit = decimal.New(1, 1000000000)
	if _, e := Aggregate(day("2026-01-02T00:00:00Z"), "IDR", lines); e != nil {
		t.Fatal(e)
	}
	if _, e := Aggregate(day("2026-01-03T00:00:00Z"), "IDR", lines); e == nil {
		t.Fatal("accepted corrupt included line")
	}
}
