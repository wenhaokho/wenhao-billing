package compare

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Seeded chart_of_accounts ids (migrations/00001_baseline.sql).
const (
	acctBank    = 1  // 1000
	acctAR      = 2  // 1100
	acctAP      = 3  // 2000
	acctRevenue = 6  // 4000
	acctExpense = 9  // 5000
	acctFXGain  = 12 // 4900
	acctFXLoss  = 13 // 5900
)

// jLine is one journal line a test inserts.
type jLine struct {
	account                      int
	debit, credit, currency      string
	baseDebit, baseCredit, ratio string
}

// insertEntry inserts one journal entry and its lines on one side.
func insertEntry(t *testing.T, pool *pgxpool.Pool, entryID, source, sourceID, memo string, lines ...jLine) {
	t.Helper()
	mustExec(t, pool, `INSERT INTO journal_entries (entry_id, source_type, source_id, memo) VALUES ($1, $2, $3, $4)`,
		entryID, source, sourceID, memo)
	for _, l := range lines {
		rate := l.ratio
		if rate == "" {
			rate = "1"
		}
		mustExec(t, pool, `INSERT INTO journal_lines (line_id, entry_id, account_id, debit, credit, currency,
			base_amount_debit, base_amount_credit, fx_rate)
			VALUES (gen_random_uuid(), $1, $2, $3::numeric, $4::numeric, $5, $6::numeric, $7::numeric, $8::numeric)`,
			entryID, l.account, l.debit, l.credit, l.currency, l.baseDebit, l.baseCredit, rate)
	}
}

// idr is a base-currency line: the base amounts equal the amounts.
func idr(account int, debit, credit string) jLine {
	return jLine{account: account, debit: debit, credit: credit, currency: "IDR", baseDebit: debit, baseCredit: credit}
}

// classified is each difference as "table side classify", sorted.
func classified(diffs []RowDiff) []string {
	out := make([]string, 0, len(diffs))
	for _, d := range diffs {
		side := "both"
		switch {
		case d.Python == "":
			side = "go"
		case d.Go == "":
			side = "python"
		}
		out = append(out, d.Table+" "+side+" "+d.Classify)
	}
	sort.Strings(out)
	return out
}

const (
	pySource = "a1000000-0000-4000-8000-000000000001"
	goSource = "b1000000-0000-4000-8000-000000000001"
	pyEntry  = "a2000000-0000-4000-8000-000000000001"
	goEntry  = "b2000000-0000-4000-8000-000000000001"
	pyVend   = "a3000000-0000-4000-8000-000000000001"
	goVend   = "b3000000-0000-4000-8000-000000000001"
	pyBillID = "a4000000-0000-4000-8000-000000000001"
	goBillID = "b4000000-0000-4000-8000-000000000001"
)

// TestClassifyAccrualEntries is plan 12 Task 8: each accrual rule names its
// PORT-NOTES note on exactly the rows it describes, every difference is
// still reported, and anything the rules do not describe stays unclassified.
func TestClassifyAccrualEntries(t *testing.T) {
	t.Parallel()
	journal := []string{"journal_entries", "journal_lines"}
	// payment is a PAYMENT entry on both sides for the mapped payment.
	payment := func(f *rowsFixture, pyLines, goLines []jLine) IDMaps {
		insertEntry(t, f.py, pyEntry, "PAYMENT", pySource, "Payment cleared", pyLines...)
		insertEntry(t, f.gov, goEntry, "PAYMENT", goSource, "Payment cleared", goLines...)
		return mapsOf(t, "payments", pySource, goSource)
	}
	// usdBank and usdAR are a USD 100 payment at the payment date's 15000.
	usdBank := jLine{account: acctBank, debit: "100", credit: "0", currency: "USD", baseDebit: "1500000", baseCredit: "0", ratio: "15000"}
	usdAR := jLine{account: acctAR, debit: "0", credit: "100", currency: "USD", baseDebit: "0", baseCredit: "1500000", ratio: "15000"}
	// goAR relieves AR at the issue date's 16000; the difference is a 5900 loss.
	goAR := jLine{account: acctAR, debit: "0", credit: "100", currency: "USD", baseDebit: "0", baseCredit: "1600000", ratio: "16000"}
	goLoss := idr(acctFXLoss, "100000", "0")
	// bill inserts a vendor and a bill on both sides, then applies each side's
	// own column values.
	bill := func(f *rowsFixture, py, gov string) IDMaps {
		for _, s := range []struct {
			pool         *pgxpool.Pool
			vendor, bill string
			set          string
		}{{f.py, pyVend, pyBillID, py}, {f.gov, goVend, goBillID, gov}} {
			mustExec(t, s.pool, `INSERT INTO vendors (vendor_id, name) VALUES ($1, 'V')`, s.vendor)
			mustExec(t, s.pool, `INSERT INTO bills (bill_id, vendor_id, bill_number, currency, amount, balance_due, status)
				VALUES ($1, $2, 'B-1', 'IDR', 1000, 1000, 'OPEN')`, s.bill, s.vendor)
			mustExec(t, s.pool, `UPDATE bills SET `+s.set+` WHERE bill_id = $1`, s.bill)
		}
		return mapsOf(t, "vendors", pyVend, goVend, "bills", pyBillID, goBillID)
	}
	const voidedByPatch = `status = 'VOID', balance_due = 0, notes = E'\n[voided] status set to VOID via update'`

	cases := []struct {
		name   string
		tables []string
		setup  func(f *rowsFixture) IDMaps
		want   []string
	}{
		{
			name: "invoice issue on Go only is PN 162", tables: journal,
			setup: func(f *rowsFixture) IDMaps {
				insertEntry(t, f.gov, goEntry, "INVOICE", goSource, "Invoice issued", idr(acctAR, "1000", "0"), idr(acctRevenue, "0", "1000"))
				return IDMaps{}
			},
			want: []string{"journal_entries go PN 162", "journal_lines go PN 162", "journal_lines go PN 162"},
		},
		{
			name: "bill open on Go only is PN 164", tables: journal,
			setup: func(f *rowsFixture) IDMaps {
				insertEntry(t, f.gov, goEntry, "BILL", goSource, "Bill opened", idr(acctExpense, "1000", "0"), idr(acctAP, "0", "1000"))
				return IDMaps{}
			},
			want: []string{"journal_entries go PN 164", "journal_lines go PN 164", "journal_lines go PN 164"},
		},
		{
			name: "bill payment on Go only is PN 165", tables: journal,
			setup: func(f *rowsFixture) IDMaps {
				insertEntry(t, f.gov, goEntry, "BILL_PAYMENT", goSource, "Bill paid", idr(acctAP, "400", "0"), idr(acctBank, "0", "400"))
				return IDMaps{}
			},
			want: []string{"journal_entries go PN 165", "journal_lines go PN 165", "journal_lines go PN 165"},
		},
		{
			// PN 161 is not one of the accrual rules: a Go-only PAYMENT
			// entry is reported unclassified.
			name: "payment on Go only stays unclassified", tables: journal,
			setup: func(f *rowsFixture) IDMaps {
				insertEntry(t, f.gov, goEntry, "PAYMENT", goSource, "Payment cleared", idr(acctBank, "1000", "0"), idr(acctAR, "0", "1000"))
				return IDMaps{}
			},
			want: []string{"journal_entries go ", "journal_lines go ", "journal_lines go "},
		},
		{
			name: "invoice entry on Python only stays unclassified", tables: journal,
			setup: func(f *rowsFixture) IDMaps {
				insertEntry(t, f.py, pyEntry, "INVOICE", pySource, "Invoice issued", idr(acctAR, "1000", "0"), idr(acctRevenue, "0", "1000"))
				return IDMaps{}
			},
			want: []string{"journal_entries python ", "journal_lines python ", "journal_lines python "},
		},
		{
			// The entries pair (same source and memo); their lines differ,
			// so the group is not Go-only.
			name: "paired invoice entries with other lines stay unclassified", tables: journal,
			setup: func(f *rowsFixture) IDMaps {
				insertEntry(t, f.py, pyEntry, "INVOICE", pySource, "Invoice issued", idr(acctAR, "1000", "0"), idr(acctRevenue, "0", "1000"))
				insertEntry(t, f.gov, goEntry, "INVOICE", goSource, "Invoice issued", idr(acctAR, "900", "0"), idr(acctRevenue, "0", "900"))
				return mapsOf(t, "invoices", pySource, goSource)
			},
			want: []string{
				"journal_lines go ", "journal_lines go ",
				"journal_lines python ", "journal_lines python ",
			},
		},
		{
			name: "payment relieved at issue basis is PN 163", tables: journal,
			setup: func(f *rowsFixture) IDMaps {
				return payment(f, []jLine{usdBank, usdAR}, []jLine{usdBank, goAR, goLoss})
			},
			want: []string{"journal_lines go PN 163", "journal_lines go PN 163", "journal_lines python PN 163"},
		},
		{
			name: "payment bank line amount differs stays unclassified", tables: journal,
			setup: func(f *rowsFixture) IDMaps {
				goBank := usdBank
				goBank.debit = "99"
				return payment(f, []jLine{usdBank, usdAR}, []jLine{goBank, goAR, goLoss})
			},
			want: []string{"journal_lines go ", "journal_lines go ", "journal_lines go ", "journal_lines python ", "journal_lines python "},
		},
		{
			// A 4900 line on Python only is FX too, and dropped; a 1300
			// withholding line is not.
			name: "payment withholding line differs stays unclassified", tables: journal,
			setup: func(f *rowsFixture) IDMaps {
				wht := idr(14, "50", "0")
				return payment(f, []jLine{usdBank, usdAR}, []jLine{usdBank, goAR, goLoss, wht})
			},
			want: []string{"journal_lines go ", "journal_lines go ", "journal_lines go ", "journal_lines python "},
		},
		{
			name: "payment differing only in FX lines is PN 163", tables: journal,
			setup: func(f *rowsFixture) IDMaps {
				return payment(f, []jLine{usdBank, usdAR, idr(acctFXGain, "0", "20000")}, []jLine{usdBank, usdAR, goLoss})
			},
			want: []string{"journal_lines go PN 163", "journal_lines python PN 163"},
		},
		{
			name: "bill voided by PATCH is PN 164", tables: []string{"vendors", "bills"},
			setup: func(f *rowsFixture) IDMaps {
				return bill(f, `status = 'VOID'`, voidedByPatch)
			},
			want: []string{"bills both PN 164"},
		},
		{
			name: "bill voided by PATCH with another column differing stays unclassified", tables: []string{"vendors", "bills"},
			setup: func(f *rowsFixture) IDMaps {
				return bill(f, `status = 'VOID'`, voidedByPatch+`, bill_number = 'B-2'`)
			},
			want: []string{"bills both "},
		},
		{
			name: "bill zeroed without a voided line stays unclassified", tables: []string{"vendors", "bills"},
			setup: func(f *rowsFixture) IDMaps {
				return bill(f, `status = 'VOID'`, `status = 'VOID', balance_due = 0`)
			},
			want: []string{"bills both "},
		},
		{
			name: "bill voided on both with other notes stays unclassified", tables: []string{"vendors", "bills"},
			setup: func(f *rowsFixture) IDMaps {
				return bill(f, `status = 'VOID', balance_due = 0, notes = E'\n[voided] a'`, `status = 'VOID', balance_due = 0, notes = E'\n[voided] b'`)
			},
			want: []string{"bills both "},
		},
		{
			name: "bill open on Python, void on Go stays unclassified", tables: []string{"vendors", "bills"},
			setup: func(f *rowsFixture) IDMaps {
				return bill(f, `status = 'OPEN'`, voidedByPatch)
			},
			want: []string{"bills both "},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newRowsFixture(t, tc.tables...)
			f.snapshot(t)
			maps := tc.setup(f)
			got := classified(f.compare(t, maps))
			want := append([]string(nil), tc.want...)
			sort.Strings(want)
			if strings.Join(got, "|") != strings.Join(want, "|") {
				t.Fatalf("classified\n  %q\nwant\n  %q", got, want)
			}
		})
	}
}

// TestBillPaymentsIsGoOnly: bill_payments is Go's own table (PN 165). A
// scenario cannot list it, and the row comparison skips it.
func TestBillPaymentsIsGoOnly(t *testing.T) {
	t.Parallel()
	_, err := ParseWrites([]byte("scenarios:\n  - id: x\n    tables: [bill_payments]\n    steps:\n      - id: s\n        method: POST\n        path: /x\n"))
	if err == nil || !strings.Contains(err.Error(), "Go-only") {
		t.Fatalf("error %v, want the Go-only refusal for bill_payments", err)
	}

	f := newRowsFixture(t, "bill_payments")
	f.snapshot(t)
	mustExec(t, f.gov, `INSERT INTO vendors (vendor_id, name) VALUES ($1, 'V')`, goVend)
	mustExec(t, f.gov, `INSERT INTO bills (bill_id, vendor_id, currency, amount, balance_due, status) VALUES ($1, $2, 'IDR', 1000, 600, 'PARTIAL')`, goBillID, goVend)
	mustExec(t, f.gov, `INSERT INTO bill_payments (bill_payment_id, bill_id, amount, currency, payment_date)
		VALUES (gen_random_uuid(), $1, 400, 'IDR', '2026-01-02')`, goBillID)
	if diffs := f.compare(t, IDMaps{}); len(diffs) != 0 {
		t.Fatalf("diffs %+v, want none: bill_payments is not compared", diffs)
	}
}

// TestWritesReportCountsUnclassified: a classified row difference is still
// counted and listed with its note; Unclassified counts the rest, step answer
// differences included, and the exit code is unchanged.
func TestWritesReportCountsUnclassified(t *testing.T) {
	t.Parallel()
	pyDB, goDB := openTestDB(t), openTestDB(t)
	goPool := openTestPool(t, goDB)
	py := writeBackend(t, true, func(w http.ResponseWriter, _ *http.Request) {
		jsonBody(w, http.StatusOK, `{"ok":true}`)
	})
	gov := writeBackend(t, false, func(w http.ResponseWriter, _ *http.Request) {
		// Go posts an issue entry (PN 162) and a payment entry Python never
		// posts (unclassified).
		insertEntry(t, goPool, "b2000000-0000-4000-8000-000000000001", "INVOICE", goSource, "Invoice issued",
			idr(acctAR, "1000", "0"), idr(acctRevenue, "0", "1000"))
		insertEntry(t, goPool, "b2000000-0000-4000-8000-000000000002", "PAYMENT", goSource, "Payment cleared",
			idr(acctBank, "1000", "0"), idr(acctAR, "0", "1000"))
		jsonBody(w, http.StatusOK, `{"ok":false}`)
	})
	scenarios := WritesScenarios{Scenarios: []WritesScenario{{
		ID: "accrual", Tables: []string{"journal_entries", "journal_lines"},
		Steps: []WriteStep{{ID: "s", Method: http.MethodPost, Path: "/api/v1/things"}},
	}}}
	report, err := RunWrites(context.Background(), writeOptions(t, py, gov, pyDB, goDB, scenarios))
	if err != nil {
		t.Fatalf("RunWrites: %v", err)
	}
	if report.Differences != 7 || report.Unclassified != 4 {
		t.Fatalf("differences %d unclassified %d, want 7 (6 rows + $.ok) and 4: rows %+v", report.Differences, report.Unclassified, report.RowDiffs)
	}
	notes := 0
	for _, d := range report.RowDiffs {
		if d.Classify == "PN 162" {
			notes++
		}
	}
	if notes != 3 || len(report.RowDiffs) != 6 {
		t.Fatalf("row diffs %+v, want all 6 listed, 3 of them PN 162", report.RowDiffs)
	}
	if got := WritesSummary(report); got != "7 differences, 4 unclassified" {
		t.Fatalf("summary %q", got)
	}
	if got := WritesExitCode(report, nil); got != 1 {
		t.Fatalf("exit code %d, want 1: classified differences still count", got)
	}
	if got := WritesExitCode(WritesReport{Differences: 3}, nil); got != 1 {
		t.Fatalf("exit code %d for only classified differences, want 1", got)
	}
}
