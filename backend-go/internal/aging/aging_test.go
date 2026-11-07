package aging

import (
	"github.com/shopspring/decimal"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func dt(s string) time.Time {
	t, e := time.Parse("2006-01-02", s)
	if e != nil {
		panic(e)
	}
	return t
}
func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }
func TestBoundariesAndCurrencies(t *testing.T) {
	as := dt("2024-04-01")
	var records []Record
	for _, n := range []int{-1, 0, 1, 30, 31, 60, 61, 90, 91} {
		d := as.AddDate(0, 0, -n)
		records = append(records, Record{"1", "Alice", "USD", dt("2023-01-01"), &d, dec("1")})
	}
	records = append(records, Record{"2", "Bob", "IDR", dt("2024-04-02"), nil, dec("100")}, Record{"2", "Bob", "IDR", dt("2024-01-01"), nil, dec("2")}, Record{"1", "Alice", "usd", dt("2024-01-01"), nil, dec("3")}, Record{"3", "Carol", "US ", dt("2024-01-01"), nil, dec("4")})
	r, e := Calculate(as, "AR", true, records)
	if e != nil {
		t.Fatal(e)
	}
	b := r.TotalsByCurrency["USD"]
	for _, v := range []decimal.Decimal{b.Current, b.D1To30, b.D31To60, b.D61To90} {
		if !v.Equal(dec("2")) {
			t.Fatal(b)
		}
	}
	if !b.D90Plus.Equal(dec("1")) || !b.Total.Equal(dec("9")) {
		t.Fatal(b)
	}
	idr := r.TotalsByCurrency["IDR"]
	if !idr.Current.Equal(dec("2")) || !idr.Total.Equal(dec("2")) {
		t.Fatalf("future cutoff or missing due date: %+v", idr)
	}
	if !r.TotalsByCurrency["usd"].Total.Equal(dec("3")) || len(r.TotalsByCurrency) != 4 {
		t.Fatalf("currency grouping: %+v", r.TotalsByCurrency)
	}
	if len(r.Rows) != 4 || r.Rows[0].PartyID != "1" || r.Rows[0].Currency != "USD" || r.Rows[1].Currency != "usd" || r.Rows[2].PartyID != "2" || r.Rows[3].Currency != "US " {
		t.Fatalf("party grouping: %+v", r.Rows)
	}
	if _, e = Calculate(as, "AR", false, records); e == nil {
		t.Fatal("unverified snapshot accepted")
	}

}
func TestRejectAmbiguity(t *testing.T) {
	as := dt("2024-01-01")
	good := Record{"1", "Name", "USD", as, nil, dec("1")}
	for _, bad := range []Record{{"", "Name", "USD", as, nil, dec("1")}, {"1", "Name", "US", as, nil, dec("1")}, {"1", "Name", "USD", as, nil, dec("0")}, {"1", "Name", "USD", as, nil, dec("0.00001")}, {"1", "Other", "USD", as, nil, dec("1")}} {
		if _, e := Calculate(as, "AP", true, []Record{good, bad}); e == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
}

func TestPythonBucketDifferential(t *testing.T) {
	script := `import ast, sys
from decimal import Decimal
from types import SimpleNamespace
source = open(sys.argv[1], encoding="utf-8").read()
tree = ast.parse(source)
functions = [node for node in tree.body if isinstance(node, ast.FunctionDef) and node.name in ("_empty_buckets", "_add_to_bucket")]
if len(functions) != 2:
    raise RuntimeError("aging oracle functions missing")
module = ast.Module(body=functions, type_ignores=[])
namespace = {"Decimal": Decimal, "AgingBuckets": SimpleNamespace, "_ZERO": Decimal(0)}
exec(compile(module, sys.argv[1], "exec"), namespace)
b = namespace["_empty_buckets"]()
for day in [-1, 0, 1, 30, 31, 60, 61, 90, 91]:
    namespace["_add_to_bucket"](b, day, Decimal("1.2345"))
print(",".join(str(getattr(b, n)) for n in ("current", "d_1_30", "d_31_60", "d_61_90", "d_90_plus", "total")))`
	cmd := exec.Command("python3", "-c", script, filepath.Join("..", "..", "..", "backend", "app", "api", "v1", "routers", "accounting.py"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Python standard-library oracle failed: %v: %s", err, out)
	}
	as := dt("2024-04-01")
	var records []Record
	for _, n := range []int{-1, 0, 1, 30, 31, 60, 61, 90, 91} {
		d := as.AddDate(0, 0, -n)
		records = append(records, Record{"1", "Alice", "USD", dt("2023-01-01"), &d, dec("1.2345")})
	}
	r, err := Calculate(as, "AR", true, records)
	if err != nil {
		t.Fatal(err)
	}
	b := r.TotalsByCurrency["USD"]
	values := []decimal.Decimal{b.Current, b.D1To30, b.D31To60, b.D61To90, b.D90Plus, b.Total}
	pythonValues := strings.Split(strings.TrimSpace(string(out)), ",")
	if len(pythonValues) != len(values) {
		t.Fatalf("unexpected Python output %s", out)
	}
	for i, value := range values {
		if !value.Equal(dec(pythonValues[i])) {
			t.Fatalf("bucket %d: Go %s; Python %s", i, value, pythonValues[i])
		}
	}
}
