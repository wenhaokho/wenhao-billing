package matching

import (
	"bytes"
	"encoding/json"
	"github.com/shopspring/decimal"
	"os/exec"
	"testing"
	"time"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }
func day(s string) time.Time {
	v, e := time.Parse("2006-01-02", s)
	if e != nil {
		panic(e)
	}
	return v
}
func fixture() (Payment, Candidate) {
	return Payment{Amount: d("95"), Currency: "USD", PayerName: "Acme", Date: day("2026-01-01")}, Candidate{ID: "1", CustomerName: "Acme", Currency: "USD", BalanceDue: d("100"), WithholdingOutstanding: d("5"), IssueDate: day("2026-01-01"), DueDate: day("2026-01-15"), Status: "OPEN"}
}
func TestSafeSelection(t *testing.T) {
	p, c := fixture()
	if r := Match(p, []Candidate{c}); r.Status != Cleared || !r.Confidence.Equal(d("100")) {
		t.Fatalf("exact: %+v", r)
	}
	other := c
	other.ID = "2"
	if r := Match(p, []Candidate{c, other}); r.Status != PendingManualReview || !r.Ambiguous || r.CandidateID != "" {
		t.Fatalf("tie: %+v", r)
	}
	p.Amount = d("94")
	if r := Match(p, []Candidate{c}); r.Status != PendingManualReview || !r.AmountMismatch {
		t.Fatalf("mismatch: %+v", r)
	}
	if r := Match(p, nil); r.Status != PendingManualReview || r.CandidateID != "" {
		t.Fatalf("empty: %+v", r)
	}
	p, c = fixture()
	p.Currency = "!"
	if r := Match(p, []Candidate{c}); r.Status != PendingManualReview {
		t.Fatalf("invalid: %+v", r)
	}
	p, c = fixture()
	c.IssueDate = time.Time{}
	if r := Match(p, []Candidate{c}); r.Status != PendingManualReview {
		t.Fatalf("invalid candidate: %+v", r)
	}
	p, c = fixture()
	p.Date = day("2026-01-08")
	if r := Match(p, []Candidate{c}); !r.Confidence.Equal(d("95")) || r.Status != Cleared {
		t.Fatalf("boundary: %+v", r)
	}
	p.Date = day("2026-01-08")
	c.DueDate = day("2026-02-01")
	c.IssueDate = day("2025-12-31")
	if r := Match(p, []Candidate{c}); r.Status != PendingManualReview || !r.Confidence.LessThan(d("95")) {
		t.Fatalf("below: %+v", r)
	}
}

func TestScorePython(t *testing.T) {
	cases := []struct{ payer, name, aliases, date string }{
		{"Acme", "Acme", "", "2026-01-01"}, {"ACME CO", "Acme Company", "Acme Co", "2026-01-04"}, {"tide", "diet", "", "2026-01-08"}, {"İ", "i", "", "2026-01-01"}, {"\u001cAcme\u001f", "acme", "", "2026-01-01"}, {"abxcd", "abcd", "", "2026-02-01"},
	}
	script := `import json,sys
from datetime import date
from decimal import Decimal
from uuid import UUID
from app.services.matching_engine import IncomingPayment, InvoiceCandidate, _score
rows=json.load(sys.stdin)
out=[]
for payer,name,alias,day in rows:
 p=IncomingPayment(Decimal('95'),'USD',payer,None,date.fromisoformat(day),'test')
 c=InvoiceCandidate(UUID(int=1),UUID(int=2),name,(alias,) if alias else (), 'USD',Decimal('100'),date(2026,1,1),date(2026,1,15),'OPEN',Decimal('5'))
 out.append(str(_score(p,c)))
print(json.dumps(out))`
	rows := make([][]string, 0, len(cases))
	for _, v := range cases {
		rows = append(rows, []string{v.payer, v.name, v.aliases, v.date})
	}
	input, _ := json.Marshal(rows)
	cmd := exec.Command("python", "-c", script)
	cmd.Stdin = bytes.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("python: %v: %s", err, out)
	}
	var expected []string
	if err = json.Unmarshal(out, &expected); err != nil {
		t.Fatal(err)
	}
	for i, v := range cases {
		p, c := fixture()
		p.PayerName = v.payer
		p.Date = day(v.date)
		c.CustomerName = v.name
		if v.aliases != "" {
			c.Aliases = []string{v.aliases}
		}
		got := Score(p, c)
		if !got.Equal(d(expected[i])) {
			t.Errorf("%+v: got %s, Python %s", v, got, expected[i])
		}
	}
}

func TestCompetingExactCandidates(t *testing.T) {
	p, c := fixture()
	other := c
	other.ID = "2"
	other.CustomerName = "Unrelated"
	other.IssueDate = day("2025-12-01")
	other.DueDate = day("2025-12-02")
	for _, cs := range [][]Candidate{{c, other}, {other, c}} {
		r := Match(p, cs)
		if r.Status != PendingManualReview || !r.Ambiguous || r.CandidateID != "" {
			t.Fatalf("competing candidates: %+v", r)
		}
	}
	if r := Match(p, []Candidate{c, c}); r.Status != Cleared || r.Ambiguous || r.CandidateID != c.ID {
		t.Fatalf("identical duplicate: %+v", r)
	}
	conflicting := c
	conflicting.CustomerName = "Different"
	for _, cs := range [][]Candidate{{c, conflicting}, {conflicting, c}} {
		if r := Match(p, cs); r.Status != PendingManualReview || r.CandidateID != "" {
			t.Fatalf("conflicting duplicate: %+v", r)
		}
	}
	other.BalanceDue = d("100.0001")
	if r := Match(p, []Candidate{c, other}); r.Status != PendingManualReview || !r.Ambiguous {
		t.Fatalf("epsilon candidate: %+v", r)
	}
	other.BalanceDue = d("100")
	other.Currency = "SGD"
	if r := Match(p, []Candidate{c, other}); r.Status != Cleared {
		t.Fatalf("different currency: %+v", r)
	}
	other.Currency = "USD"
	other.BalanceDue = d("101")
	if r := Match(p, []Candidate{c, other}); r.Status != Cleared {
		t.Fatalf("different amount: %+v", r)
	}
}

func TestAmountToleranceNeverAutoClears(t *testing.T) {
	p, c := fixture()
	for _, amount := range []string{"94.9999", "95.0001"} {
		p.Amount = d(amount)
		r := Match(p, []Candidate{c})
		if r.Status != PendingManualReview || !r.AmountMismatch {
			t.Errorf("%s: %+v", amount, r)
		}
	}
}

func TestInvalidMoneyFailsClosed(t *testing.T) {
	p, c := fixture()
	for _, amount := range []string{"0", "-1", "95.00001", "1000000000000000"} {
		p.Amount = d(amount)
		if r := Match(p, []Candidate{c}); r.Status != PendingManualReview {
			t.Errorf("payment %s: %+v", amount, r)
		}
	}
	p, c = fixture()
	c.BalanceDue = d("100.00001")
	if r := Match(p, []Candidate{c}); r.Status != PendingManualReview {
		t.Errorf("invalid balance: %+v", r)
	}
}

func TestInvalidNamesFailClosed(t *testing.T) {
	p, c := fixture()
	c.Aliases = []string{""}
	p.PayerName = "\u001c"
	if r := Match(p, []Candidate{c}); r.Status != PendingManualReview {
		t.Fatalf("blank payer: %+v", r)
	}
	p, c = fixture()
	c.CustomerName = "\u001c"
	if r := Match(p, []Candidate{c}); r.Status != PendingManualReview {
		t.Fatalf("blank customer: %+v", r)
	}
	p, c = fixture()
	p.PayerName = string([]byte{0xff})
	if r := Match(p, []Candidate{c}); r.Status != PendingManualReview {
		t.Fatalf("invalid payer: %+v", r)
	}
	p, c = fixture()
	c.Aliases = []string{string([]byte{0xff})}
	if r := Match(p, []Candidate{c}); r.Status != PendingManualReview {
		t.Fatalf("invalid alias: %+v", r)
	}
	p, c = fixture()
	c.CustomerName = string([]byte{0xff})
	if r := Match(p, []Candidate{c}); r.Status != PendingManualReview {
		t.Fatalf("invalid customer: %+v", r)
	}
}

func TestExtremeMoneyExponentFailsClosed(t *testing.T) {
	p, c := fixture()
	for _, value := range []decimal.Decimal{decimal.New(1, 1000000000), decimal.New(1, -1000000000)} {
		p.Amount = value
		if r := Match(p, []Candidate{c}); r.Status != PendingManualReview {
			t.Fatalf("extreme payment: %+v", r)
		}
		p, c = fixture()
		c.BalanceDue = value
		if r := Match(p, []Candidate{c}); r.Status != PendingManualReview {
			t.Fatalf("extreme balance: %+v", r)
		}
		p, c = fixture()
	}
}
