package fxrevaluation

import (
	"github.com/shopspring/decimal"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }
func TestPythonDifferential(t *testing.T) {
	date := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)
	cases := []struct{ typ, face, base, rate string }{{"ASSET", "10", "9", "1.25"}, {"LIABILITY", "10", "9", "1.25"}, {"ASSET", "-1", "-2", "1.00005"}, {"ASSET", "1", "1", "1.00015"}, {"LIABILITY", "0", "3", "2"}, {"ASSET", "999999999999999.9999", "3", "99999999999.99999999"}}
	script := `from decimal import Decimal, InvalidOperation
import sys
try:
 f,b,r=map(Decimal,sys.argv[2:]); v=(f*r).quantize(Decimal('0.0001')); g=(v-b)*(1 if sys.argv[1]=='ASSET' else -1); print(v, g)
except InvalidOperation: print('error')`
	for _, c := range cases {
		t.Run(c.typ+"/"+c.face+"/"+c.rate, func(t *testing.T) {
			f, b := d(c.face), d(c.base)
			row := Balance{AccountID: 1, Code: "100", Name: "test", Type: c.typ, Currency: "USD"}
			if c.typ == "LIABILITY" {
				f = f.Neg()
				b = b.Neg()
			}
			if f.IsNegative() {
				row.Credit = f.Neg()
			} else {
				row.Debit = f
			}
			if b.IsNegative() {
				row.BaseCredit = b.Neg()
			} else {
				row.BaseDebit = b
			}
			py, e := exec.Command("python3", "-c", script, c.typ, c.face, c.base, c.rate).Output()
			if e != nil {
				t.Fatal(e)
			}
			got, e := Calculate(date, "IDR", true, []Balance{row}, map[string]Rate{"USD": {d(c.rate), date}})
			if strings.TrimSpace(string(py)) == "error" {
				if e == nil {
					t.Fatal("expected precision error")
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			p := strings.Fields(string(py))
			if !got.Lines[0].RevaluedBase.Equal(d(p[0])) || !got.Lines[0].UnrealisedGainLoss.Equal(d(p[1])) {
				t.Fatalf("%+v != %s", got.Lines[0], py)
			}
		})
	}
}
func TestMissingAndValidation(t *testing.T) {
	date := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)
	b := Balance{AccountID: 1, Code: "2", Name: "asset", Type: "ASSET", Currency: "USD", Debit: d("1")}
	c := Balance{AccountID: 2, Code: "1", Name: "liability", Type: "LIABILITY", Currency: "EUR", Credit: d("2")}
	got, e := Calculate(date, "IDR", true, []Balance{b, c}, nil)
	if e != nil || len(got.Lines) != 2 || strings.Join(got.MissingRates, ",") != "EUR,USD" || got.Lines[0].Currency != "EUR" || !got.Lines[0].RateMissing {
		t.Fatalf("%+v %v", got, e)
	}
	for _, r := range []Rate{{d("0"), date}, {d("-1"), date}, {d("1"), date.AddDate(0, 0, 1)}, {d("0.000000001"), date}, {d("100000000000"), date}} {
		if _, e := Calculate(date, "IDR", true, []Balance{b}, map[string]Rate{"USD": r}); e == nil {
			t.Fatalf("accepted %+v", r)
		}
	}
	if _, e := Calculate(date, "IDR", false, []Balance{b}, nil); e == nil {
		t.Fatal("unverified snapshot")
	}
	if _, e := Calculate(date, "IDR", true, []Balance{b, b}, nil); e == nil {
		t.Fatal("duplicate")
	}
	b.Debit = decimal.Zero
	got, e = Calculate(date, "IDR", true, []Balance{b}, nil)
	if e != nil || len(got.Lines) != 0 {
		t.Fatal(got, e)
	}
}

func TestExactLargeGainsAndTotals(t *testing.T) {
	date := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)
	max := d("999999999999999999999999.9999")
	rows := []Balance{
		{AccountID: 1, Code: "1", Name: "a", Type: "ASSET", Currency: "USD", Debit: max, BaseCredit: max},
		{AccountID: 2, Code: "2", Name: "b", Type: "ASSET", Currency: "USD", Debit: max, BaseCredit: max},
	}
	got, err := Calculate(date, "IDR", true, rows, map[string]Rate{"USD": {d("1"), date}})
	if err != nil {
		t.Fatal(err)
	}
	wantGain := d("1999999999999999999999999.9998")
	wantTotal := d("3999999999999999999999999.9996")
	if !got.Lines[0].UnrealisedGainLoss.Equal(wantGain) || !got.TotalUnrealisedGain.Equal(wantTotal) {
		t.Fatalf("gain %s total %s", got.Lines[0].UnrealisedGainLoss, got.TotalUnrealisedGain)
	}
	// Python's default 28-digit context loses the final decimal on this subtraction.
	py, err := exec.Command("python3", "-c", "from decimal import Decimal; a=Decimal('999999999999999999999999.9999'); g=a-(-a); print(g, g+g)").Output()
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(py))
	if d(fields[0]).Equal(wantGain) || d(fields[1]).Equal(wantTotal) {
		t.Fatalf("expected Python precision loss, got %s", py)
	}
}

func TestMaximumStoredRate(t *testing.T) {
	date := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)
	row := Balance{AccountID: 1, Code: "1", Name: "asset", Type: "ASSET", Currency: "USD", Debit: d("1")}
	got, err := Calculate(date, "IDR", true, []Balance{row}, map[string]Rate{"USD": {d("99999999999.99999999"), date}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Lines) != 1 || !got.Lines[0].RevaluedBase.Equal(d("100000000000.0000")) {
		t.Fatalf("unexpected report: %+v", got)
	}
}
