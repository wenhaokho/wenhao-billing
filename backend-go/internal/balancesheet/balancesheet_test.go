package balancesheet

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
	d := day("2026-01-02T23:59:59Z")
	return []Line{{1, "1000", "Cash", "ASSET", d, dec("10"), dec("2")}, {1, "1000", "Cash", "ASSET", d, dec("0.0001"), dec("0")}, {2, "2000", "Debt", "LIABILITY", d, dec("1"), dec("5")}, {3, "3000", "Capital", "EQUITY", d, dec("0"), dec("3")}, {4, "4000", "Sales", "INCOME", d, dec("0"), dec("7")}, {5, "5000", "Costs", "EXPENSE", d, dec("4"), dec("1")}, {6, "6000", "COGS", "COGS", d, dec("2"), dec("0")}, {7, "7000", "Zero", "ASSET", d, dec("0"), dec("0")}, {8, "8000", "Net zero", "ASSET", d, dec("1"), dec("1")}, {1, "1000", "Cash", "ASSET", day("2026-01-03T00:00:00Z"), dec("99"), dec("0")}}
}
func TestAggregate(t *testing.T) {
	r, e := Aggregate(day("2026-01-02T00:00:00Z"), "IDR", fixture())
	if e != nil {
		t.Fatal(e)
	}
	if len(r.Groups) != 6 || len(r.Groups[0].Lines) != 2 || r.Groups[0].Lines[1].AccountID != 8 {
		t.Fatalf("groups: %+v", r.Groups)
	}
	for i, w := range []string{"8.0001", "4", "3", "7", "3", "2"} {
		if !r.Groups[i].TotalBase.Equal(dec(w)) {
			t.Fatalf("group %d: %+v", i, r.Groups[i])
		}
	}
	if !r.ExpenseTotalBase.Equal(dec("5")) {
		t.Fatal(r.ExpenseTotalBase)
	}
	empty, e := Aggregate(day("2025-01-01T00:00:00Z"), "IDR", fixture())
	if e != nil || empty.Groups == nil || len(empty.Groups) != 0 {
		t.Fatalf("empty %+v %v", empty, e)
	}
}
func TestBoundsAndValidation(t *testing.T) {
	l := fixture()[0]
	for _, v := range []decimal.Decimal{dec("-1"), dec("0.00001"), dec("1000000000000000"), decimal.New(1, 1000000000), decimal.New(1, -1000000000)} {
		l.BaseDebit = v
		if _, e := Aggregate(day("2026-01-02T00:00:00Z"), "IDR", []Line{l}); e == nil {
			t.Fatal("accepted invalid amount", v.Exponent())
		}
	}
	l = fixture()[0]
	l.BaseDebit = dec("999999999999999.9999")
	l.BaseCredit = dec("0")
	l2 := l
	l2.BaseDebit = dec("0.0001")
	r, e := Aggregate(day("2026-01-02T00:00:00Z"), "IDR", []Line{l, l2})
	if e != nil || !r.AssetsTotalBase.Equal(dec("1000000000000000")) {
		t.Fatalf("exact: %+v %v", r, e)
	}
	l2.Name = "Other"
	if _, e = Aggregate(day("2026-01-02T00:00:00Z"), "IDR", []Line{l, l2}); e == nil {
		t.Fatal("accepted inconsistent metadata")
	}
	if _, e = Aggregate(time.Time{}, "IDR", nil); e == nil {
		t.Fatal("accepted zero date")
	}
	if _, e = Aggregate(day("2026-01-02T00:00:00Z"), "idr", nil); e == nil {
		t.Fatal("accepted currency")
	}
	l.Type = "invalid"
	if _, e = Aggregate(day("2026-01-02T00:00:00Z"), "IDR", []Line{l}); e == nil {
		t.Fatal("accepted type")
	}
}
func TestPythonDifferentialAndFutureCutoffDefect(t *testing.T) {
	python := os.Getenv("BILLING_PYTHON")
	if python == "" {
		python = "/tmp/codex-billing-py312/bin/python"
	}
	if _, e := os.Stat(python); e != nil {
		t.Skip("Python backend environment unavailable")
	}
	script := `import json
from datetime import date
from sqlalchemy import create_engine, text
from sqlalchemy.orm import Session
from app.api.v1.routers.accounting import balance_sheet
engine=create_engine('sqlite://')
with engine.begin() as c:
 c.execute(text('CREATE TABLE chart_of_accounts (account_id INTEGER, code TEXT, name TEXT, type TEXT)'))
 c.execute(text('CREATE TABLE journal_entries (entry_id TEXT, posted_at TIMESTAMP)'))
 c.execute(text('CREATE TABLE journal_lines (entry_id TEXT, account_id INTEGER, base_amount_debit NUMERIC, base_amount_credit NUMERIC)'))
 c.execute(text("INSERT INTO chart_of_accounts VALUES (1,'1000','Cash','ASSET'),(2,'2000','Debt','LIABILITY'),(3,'3000','Capital','EQUITY'),(4,'4000','Sales','INCOME'),(5,'5000','Costs','EXPENSE'),(6,'6000','COGS','COGS'),(7,'7000','Zero','ASSET'),(8,'8000','Net zero','ASSET')"))
 c.execute(text("INSERT INTO journal_entries VALUES ('a','2026-01-02 23:59:59'),('b','2026-01-03 00:00:00')"))
 c.execute(text("INSERT INTO journal_lines VALUES ('a',1,10,2),('a',1,0.0001,0),('a',2,1,5),('a',3,0,3),('a',4,0,7),('a',5,4,1),('a',6,2,0),('a',7,0,0),('a',8,1,1),('b',1,99,0)"))
with Session(engine) as db:
 print(json.dumps([balance_sheet(as_of=date(2026,1,2),db=db,_=None).model_dump(mode='json'),balance_sheet(as_of=date(2026,1,3),db=db,_=None).model_dump(mode='json')]))
`
	cmd := exec.Command(python, "-c", script)
	cmd.Dir = "../../../backend"
	cmd.Env = append(os.Environ(), "BASE_CURRENCY=IDR")
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("python: %v\n%s", e, out)
	}
	var p []struct {
		Groups []struct {
			Type  string          `json:"type"`
			Total decimal.Decimal `json:"total_base"`
			Lines []struct {
				ID      int64           `json:"account_id"`
				Code    string          `json:"code"`
				Name    string          `json:"name"`
				Balance decimal.Decimal `json:"base_balance"`
			} `json:"lines"`
		} `json:"groups"`
	}
	if e = json.Unmarshal(out, &p); e != nil {
		t.Fatal(e, string(out))
	}
	r, e := Aggregate(day("2026-01-03T00:00:00Z"), "IDR", fixture())
	if e != nil {
		t.Fatal(e)
	}
	if len(r.Groups) != len(p[1].Groups) {
		t.Fatal("group count")
	}
	for i, g := range r.Groups {
		pg := p[1].Groups[i]
		if g.Type != pg.Type || !g.TotalBase.Equal(pg.Total) || len(g.Lines) != len(pg.Lines) {
			t.Fatalf("group %d: %+v vs %+v", i, g, pg)
		}
		for j, l := range g.Lines {
			pl := pg.Lines[j]
			if l.AccountID != pl.ID || l.Code != pl.Code || l.Name != pl.Name || !l.BaseBalance.Equal(pl.Balance) {
				t.Fatalf("line %+v vs %+v", l, pl)
			}
		}
	}
	historical, e := Aggregate(day("2026-01-02T00:00:00Z"), "IDR", fixture())
	if e != nil {
		t.Fatal(e)
	}
	if !p[0].Groups[0].Total.Equal(dec("107.0001")) || !historical.AssetsTotalBase.Equal(dec("8.0001")) {
		t.Fatalf("cutoff discrepancy: Python %+v, Go %+v", p[0], historical)
	}
}
