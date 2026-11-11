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
	acctWHT     = 14 // 1300
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

// reverse marks original as reversed by reversal.
func reverse(t *testing.T, pool *pgxpool.Pool, original, reversal string) {
	t.Helper()
	mustExec(t, pool, `UPDATE journal_entries SET reversed_by_entry_id = $2 WHERE entry_id = $1`, original, reversal)
}

// flip is lines with each side swapped, as a reversal posts them.
func flip(lines ...jLine) []jLine {
	out := make([]jLine, len(lines))
	for i, l := range lines {
		l.debit, l.credit = l.credit, l.debit
		l.baseDebit, l.baseCredit = l.baseCredit, l.baseDebit
		out[i] = l
	}
	return out
}

// idr is a base-currency line: the base amounts equal the amounts.
func idr(account int, debit, credit string) jLine {
	return jLine{account: account, debit: debit, credit: credit, currency: "IDR", baseDebit: debit, baseCredit: credit}
}

// usd is a USD line at rate with its bases.
func usd(account int, debit, credit, baseDebit, baseCredit, rate string) jLine {
	return jLine{account: account, debit: debit, credit: credit, currency: "USD", baseDebit: baseDebit, baseCredit: baseCredit, ratio: rate}
}

// insertGoInvoice inserts an invoice (issueDate "" is NULL; createdAt "" is now).
func insertGoInvoice(t *testing.T, pool *pgxpool.Pool, id, status, currency, amount, issueDate string, template bool, createdAt string) {
	t.Helper()
	mustExec(t, pool, `INSERT INTO invoices (invoice_id, invoice_type, currency, amount, balance_due, status, issue_date, is_template, created_at)
		VALUES ($1, 'MILESTONE', $2, $3::numeric, $3::numeric, $4, NULLIF($5, '')::date, $6, COALESCE(NULLIF($7, '')::timestamp, CURRENT_TIMESTAMP))`,
		id, currency, amount, status, issueDate, template, createdAt)
}

// goBill inserts a vendor and an IDR bill.
func goBill(t *testing.T, pool *pgxpool.Pool, vendor, id, status, amount string) {
	t.Helper()
	mustExec(t, pool, `INSERT INTO vendors (vendor_id, name) VALUES ($1, 'V')`, vendor)
	mustExec(t, pool, `INSERT INTO bills (bill_id, vendor_id, currency, amount, balance_due, status, issue_date)
		VALUES ($1, $2, 'IDR', $3::numeric, $3::numeric, $4, '2026-01-01')`, id, vendor, amount, status)
}

// payment inserts one payment row.
func payment(t *testing.T, pool *pgxpool.Pool, id, intake, amount string) {
	t.Helper()
	mustExec(t, pool, `INSERT INTO payments (payment_id, amount, currency, payer_name, payment_date, intake_source, status)
		VALUES ($1, $2::numeric, 'IDR', 'P', '2026-01-02', $3, 'CLEARED')`, id, amount, intake)
}

// TestClassifyReversedManualPayment: Go reverses a manual payment that
// Python refuses to reverse (PN 161). The payment (FLAGGED), the invoice
// (balance restored, OPEN) and Go's REVERSED log row are PN 161 only when the
// payment's Go clearing and reversal are PN 161 entries and the rows differ
// by exactly the reversal.
func TestClassifyReversedManualPayment(t *testing.T) {
	t.Parallel()
	const pyInv, goInv = "a5000000-0000-4000-8000-000000000001", "b5000000-0000-4000-8000-000000000001"
	cleared := []jLine{idr(acctBank, "1000", "0"), idr(acctAR, "0", "1000")}
	tables := []string{"invoices", "payments", "reconciliation_log", "journal_entries", "journal_lines"}
	for _, tc := range []struct {
		name      string
		goBalance string
		reversed  bool
		want      []string
	}{
		{"reversed", "1000", true, append([]string{
			"invoices both PN 161", "payments both PN 161", "reconciliation_log go PN 161",
		}, times(6, "journal go PN 161")...)},
		{"balance off by one", "999", true, append([]string{
			"invoices both ", "payments both PN 161", "reconciliation_log go PN 161",
		}, times(6, "journal go PN 161")...)},
		{"clearing not reversed", "1000", false, append([]string{
			"invoices both ", "payments both ", "reconciliation_log go ",
		}, times(3, "journal go PN 161")...)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newRowsFixture(t, tables...)
			f.snapshot(t)
			for _, s := range []struct {
				pool               *pgxpool.Pool
				inv, pay, status   string
				balance, payStatus string
			}{{f.py, pyInv, pySource, "PAID", "0", "CLEARED"}, {f.gov, goInv, goSource, "OPEN", tc.goBalance, "FLAGGED"}} {
				insertGoInvoice(t, s.pool, s.inv, s.status, "IDR", "1000", "2026-01-01", false, "")
				mustExec(t, s.pool, `UPDATE invoices SET balance_due = $2::numeric WHERE invoice_id = $1`, s.inv, s.balance)
				mustExec(t, s.pool, `INSERT INTO payments (payment_id, invoice_id, amount, currency, payer_name, payment_date, intake_source, status)
					VALUES ($1, $2, 1000, 'IDR', 'P', '2026-01-02', 'MANUAL', $3)`, s.pay, s.inv, s.payStatus)
			}
			mustExec(t, f.gov, `INSERT INTO reconciliation_log (log_id, payment_id, action, reasons) VALUES (gen_random_uuid(), $1, 'REVERSED', ARRAY['compare'])`, goSource)
			insertEntry(t, f.gov, goEntry, "PAYMENT", goSource, "Payment cleared", cleared...)
			if tc.reversed {
				insertEntry(t, f.gov, goEntry2, "PAYMENT", goSource, "Reversal", flip(cleared...)...)
				reverse(t, f.gov, goEntry, goEntry2)
			}
			got := classified(f.compare(t, mapsOf(t, "invoices", pyInv, goInv, "payments", pySource, goSource)))
			for i, s := range got {
				got[i] = strings.Replace(strings.Replace(s, "journal_entries", "journal", 1), "journal_lines", "journal", 1)
			}
			sort.Strings(got)
			want := append([]string(nil), tc.want...)
			sort.Strings(want)
			if strings.Join(got, "|") != strings.Join(want, "|") {
				t.Fatalf("classified\n  %q\nwant\n  %q", got, want)
			}
		})
	}
}

// usdRate inserts a USD→IDR rate on Go.
func usdRate(t *testing.T, pool *pgxpool.Pool, rate, day string) {
	t.Helper()
	mustExec(t, pool, `INSERT INTO fx_rates (from_currency, to_currency, rate, as_of_date, source) VALUES ('USD', 'IDR', $1::numeric, $2::date, 'TEST')`, rate, day)
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

// times is n copies of s.
func times(n int, s string) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = s
	}
	return out
}

const (
	pySource = "a1000000-0000-4000-8000-000000000001"
	goSource = "b1000000-0000-4000-8000-000000000001"
	pyEntry  = "a2000000-0000-4000-8000-000000000001"
	goEntry  = "b2000000-0000-4000-8000-000000000001"
	goEntry2 = "b2000000-0000-4000-8000-000000000002"
	pyVend   = "a3000000-0000-4000-8000-000000000001"
	goVend   = "b3000000-0000-4000-8000-000000000001"
	pyBillID = "a4000000-0000-4000-8000-000000000001"
	goBillID = "b4000000-0000-4000-8000-000000000001"
)

// TestClassifyAccrualEntries is plan 12 Task 8 (and its review round 1):
// each accrual rule names its PORT-NOTES note on exactly the rows it
// describes and passes its self-consistency checks; every difference is still
// reported; anything else stays unclassified, naming the check it failed.
func TestClassifyAccrualEntries(t *testing.T) {
	t.Parallel()
	journal := []string{"journal_entries", "journal_lines"}
	issue := []jLine{idr(acctAR, "1000", "0"), idr(acctRevenue, "0", "1000")}
	// paidPair is a PAYMENT entry on both sides for the mapped payment.
	paidPair := func(f *rowsFixture, pyLines, goLines []jLine) IDMaps {
		insertEntry(t, f.py, pyEntry, "PAYMENT", pySource, "Payment cleared", pyLines...)
		insertEntry(t, f.gov, goEntry, "PAYMENT", goSource, "Payment cleared", goLines...)
		return mapsOf(t, "payments", pySource, goSource)
	}
	// A USD 100 payment at the payment date's 15000; Go relieves AR at the
	// issue date's 16000 and books the 100,000 difference as a 5900 loss.
	usdBank := usd(acctBank, "100", "0", "1500000", "0", "15000")
	usdAR := usd(acctAR, "0", "100", "0", "1500000", "15000")
	goAR := usd(acctAR, "0", "100", "0", "1600000", "16000")
	goLoss := idr(acctFXLoss, "100000", "0")
	// manual is a manual payment of 1000 on both sides, and its Go entry.
	manual := func(f *rowsFixture, pyIntake, bankAmount string) IDMaps {
		payment(t, f.py, pySource, pyIntake, "1000")
		payment(t, f.gov, goSource, "MANUAL", "1000")
		insertEntry(t, f.gov, goEntry, "PAYMENT", goSource, "Payment cleared",
			idr(acctBank, bankAmount, "0"), idr(acctAR, "0", bankAmount))
		return mapsOf(t, "payments", pySource, goSource)
	}
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
	bills := []string{"vendors", "bills"}

	cases := []struct {
		name      string
		tables    []string
		setup     func(f *rowsFixture) IDMaps
		want      []string
		wantCheck string
	}{
		// --- PN 162 -----------------------------------------------------
		{
			name: "invoice issue on Go only is PN 162", tables: journal,
			setup: func(f *rowsFixture) IDMaps {
				insertGoInvoice(t, f.gov, goSource, "OPEN", "IDR", "1000", "2026-01-01", false, "")
				insertEntry(t, f.gov, goEntry, "INVOICE", goSource, "Invoice issued", issue...)
				return IDMaps{}
			},
			want: times(3, "go PN 162"),
		},
		{
			name: "invoice void pair is PN 162", tables: journal,
			setup: func(f *rowsFixture) IDMaps {
				insertGoInvoice(t, f.gov, goSource, "VOID", "IDR", "1000", "2026-01-01", false, "")
				insertEntry(t, f.gov, goEntry, "INVOICE", goSource, "Invoice issued", issue...)
				insertEntry(t, f.gov, goEntry2, "INVOICE", goSource, "Void", flip(issue...)...)
				reverse(t, f.gov, goEntry, goEntry2)
				return IDMaps{}
			},
			want: times(6, "go PN 162"),
		},
		{
			name: "void reversal that does not mirror stays unclassified", tables: journal,
			setup: func(f *rowsFixture) IDMaps {
				insertGoInvoice(t, f.gov, goSource, "VOID", "IDR", "1000", "2026-01-01", false, "")
				insertEntry(t, f.gov, goEntry, "INVOICE", goSource, "Invoice issued", issue...)
				insertEntry(t, f.gov, goEntry2, "INVOICE", goSource, "Void", idr(acctAR, "0", "900"), idr(acctRevenue, "900", "0"))
				reverse(t, f.gov, goEntry, goEntry2)
				return IDMaps{}
			},
			want:      append(times(3, "go PN 162"), times(3, "go ")...),
			wantCheck: "do not mirror",
		},
		{
			name: "unbalanced invoice entry stays unclassified", tables: journal,
			setup: func(f *rowsFixture) IDMaps {
				insertGoInvoice(t, f.gov, goSource, "OPEN", "IDR", "1000", "2026-01-01", false, "")
				insertEntry(t, f.gov, goEntry, "INVOICE", goSource, "Invoice issued", idr(acctAR, "1000", "0"), idr(acctExpense, "0", "900"))
				return IDMaps{}
			},
			want: times(3, "go "), wantCheck: "base debits 1000 ≠ base credits 900",
		},
		{
			name: "two issue entries for one invoice stay unclassified", tables: journal,
			setup: func(f *rowsFixture) IDMaps {
				insertGoInvoice(t, f.gov, goSource, "OPEN", "IDR", "1000", "2026-01-01", false, "")
				insertEntry(t, f.gov, goEntry, "INVOICE", goSource, "Invoice issued", issue...)
				insertEntry(t, f.gov, goEntry2, "INVOICE", goSource, "Invoice issued", issue...)
				return IDMaps{}
			},
			want: times(6, "go "), wantCheck: "2 active entries",
		},
		{
			name: "issue entry of a DRAFT invoice stays unclassified", tables: journal,
			setup: func(f *rowsFixture) IDMaps {
				insertGoInvoice(t, f.gov, goSource, "DRAFT", "IDR", "1000", "2026-01-01", false, "")
				insertEntry(t, f.gov, goEntry, "INVOICE", goSource, "Invoice issued", issue...)
				return IDMaps{}
			},
			want: times(3, "go "), wantCheck: "document is DRAFT",
		},
		{
			name: "issue entry of a template stays unclassified", tables: journal,
			setup: func(f *rowsFixture) IDMaps {
				insertGoInvoice(t, f.gov, goSource, "OPEN", "IDR", "1000", "2026-01-01", true, "")
				insertEntry(t, f.gov, goEntry, "INVOICE", goSource, "Invoice issued", issue...)
				return IDMaps{}
			},
			want: times(3, "go "), wantCheck: "template true",
		},
		{
			name: "issue entry for another amount stays unclassified", tables: journal,
			setup: func(f *rowsFixture) IDMaps {
				insertGoInvoice(t, f.gov, goSource, "OPEN", "IDR", "2000", "2026-01-01", false, "")
				insertEntry(t, f.gov, goEntry, "INVOICE", goSource, "Invoice issued", issue...)
				return IDMaps{}
			},
			want: times(3, "go "), wantCheck: "control line is 1000 IDR, the document 2000 IDR",
		},
		{
			name: "issue entry without its invoice stays unclassified", tables: journal,
			setup: func(f *rowsFixture) IDMaps {
				insertEntry(t, f.gov, goEntry, "INVOICE", goSource, "Invoice issued", issue...)
				return IDMaps{}
			},
			want: times(3, "go "), wantCheck: "does not exist",
		},
		{
			name: "USD invoice at the issue date's rate is PN 162", tables: journal,
			setup: func(f *rowsFixture) IDMaps {
				usdRate(t, f.gov, "14000", "1990-01-01")
				usdRate(t, f.gov, "17000", "1990-07-01")
				insertGoInvoice(t, f.gov, goSource, "OPEN", "USD", "100", "1990-06-01", false, "")
				insertEntry(t, f.gov, goEntry, "INVOICE", goSource, "Invoice issued",
					usd(acctAR, "100", "0", "1400000", "0", "14000"), usd(acctRevenue, "0", "100", "0", "1400000", "14000"))
				return IDMaps{}
			},
			want: times(3, "go PN 162"),
		},
		{
			name: "USD invoice with no issue date uses its created_at date", tables: journal,
			setup: func(f *rowsFixture) IDMaps {
				usdRate(t, f.gov, "14000", "1990-01-01")
				insertGoInvoice(t, f.gov, goSource, "OPEN", "USD", "100", "", false, "1990-06-01 10:00")
				insertEntry(t, f.gov, goEntry, "INVOICE", goSource, "Invoice issued",
					usd(acctAR, "100", "0", "1400000", "0", "14000"), usd(acctRevenue, "0", "100", "0", "1400000", "14000"))
				return IDMaps{}
			},
			want: times(3, "go PN 162"),
		},
		{
			name: "USD invoice at another rate stays unclassified", tables: journal,
			setup: func(f *rowsFixture) IDMaps {
				usdRate(t, f.gov, "14000", "1990-01-01")
				insertGoInvoice(t, f.gov, goSource, "OPEN", "USD", "100", "1990-06-01", false, "")
				insertEntry(t, f.gov, goEntry, "INVOICE", goSource, "Invoice issued",
					usd(acctAR, "100", "0", "1500000", "0", "15000"), usd(acctRevenue, "0", "100", "0", "1500000", "15000"))
				return IDMaps{}
			},
			want: times(3, "go "), wantCheck: "not the USD rate 14000",
		},
		{
			name: "invoice entry on Python only stays unclassified", tables: journal,
			setup: func(f *rowsFixture) IDMaps {
				insertEntry(t, f.py, pyEntry, "INVOICE", pySource, "Invoice issued", issue...)
				return IDMaps{}
			},
			want: times(3, "python "),
		},
		{
			// The entries pair (same source and memo); their lines differ,
			// so the group is not Go-only.
			name: "paired invoice entries with other lines stay unclassified", tables: journal,
			setup: func(f *rowsFixture) IDMaps {
				insertEntry(t, f.py, pyEntry, "INVOICE", pySource, "Invoice issued", issue...)
				insertEntry(t, f.gov, goEntry, "INVOICE", goSource, "Invoice issued", idr(acctAR, "900", "0"), idr(acctRevenue, "0", "900"))
				return mapsOf(t, "invoices", pySource, goSource)
			},
			want: append(times(2, "go "), times(2, "python ")...),
		},
		// --- PN 164 and PN 165 entries ------------------------------------
		{
			name: "bill open on Go only is PN 164", tables: journal,
			setup: func(f *rowsFixture) IDMaps {
				goBill(t, f.gov, goVend, goSource, "OPEN", "1000")
				insertEntry(t, f.gov, goEntry, "BILL", goSource, "Bill opened", idr(acctExpense, "1000", "0"), idr(acctAP, "0", "1000"))
				return IDMaps{}
			},
			want: times(3, "go PN 164"),
		},
		{
			name: "bill entry crediting AR stays unclassified", tables: journal,
			setup: func(f *rowsFixture) IDMaps {
				goBill(t, f.gov, goVend, goSource, "OPEN", "1000")
				insertEntry(t, f.gov, goEntry, "BILL", goSource, "Bill opened", idr(acctExpense, "1000", "0"), idr(acctAR, "0", "1000"))
				return IDMaps{}
			},
			want: times(3, "go "), wantCheck: "one credit line on 2000, has 0",
		},
		{
			name: "bill payment on Go only is PN 165", tables: journal,
			setup: func(f *rowsFixture) IDMaps {
				insertEntry(t, f.gov, goEntry, "BILL_PAYMENT", goSource, "Bill paid", idr(acctAP, "400", "0"), idr(acctBank, "0", "400"))
				return IDMaps{}
			},
			want: times(3, "go PN 165"),
		},
		{
			name: "bill payment entry with no lines stays unclassified", tables: journal,
			setup: func(f *rowsFixture) IDMaps {
				insertEntry(t, f.gov, goEntry, "BILL_PAYMENT", goSource, "Bill paid")
				return IDMaps{}
			},
			want: times(1, "go "), wantCheck: "it has 0 lines",
		},
		// --- PN 161 -------------------------------------------------------
		{
			name: "manual payment on Go only is PN 161", tables: journal,
			setup: func(f *rowsFixture) IDMaps { return manual(f, "MANUAL", "1000") },
			want:  times(3, "go PN 161"),
		},
		{
			name: "manual payment and its reversal are PN 161", tables: journal,
			setup: func(f *rowsFixture) IDMaps {
				maps := manual(f, "MANUAL", "1000")
				insertEntry(t, f.gov, goEntry2, "PAYMENT", goSource, "Reversal", flip(idr(acctBank, "1000", "0"), idr(acctAR, "0", "1000"))...)
				reverse(t, f.gov, goEntry, goEntry2)
				return maps
			},
			want: times(6, "go PN 161"),
		},
		{
			name: "webhook payment on Go only stays unclassified", tables: journal,
			setup: func(f *rowsFixture) IDMaps { return manual(f, "WEBHOOK", "1000") },
			want:  times(3, "go "), wantCheck: "intake_source is MANUAL on Go and WEBHOOK on Python",
		},
		{
			name: "manual payment entry for another amount stays unclassified", tables: journal,
			setup: func(f *rowsFixture) IDMaps { return manual(f, "MANUAL", "900") },
			want:  times(3, "go "), wantCheck: "bank line is 900 IDR, the payment 1000 IDR",
		},
		{
			// Cleared, reversed and cleared again: one active entry, but two
			// that are not reversals.
			name: "second clearing of a manual payment stays unclassified", tables: journal,
			setup: func(f *rowsFixture) IDMaps {
				maps := manual(f, "MANUAL", "1000")
				const reversal = "b2000000-0000-4000-8000-000000000003"
				insertEntry(t, f.gov, reversal, "PAYMENT", goSource, "Reversal", flip(idr(acctBank, "1000", "0"), idr(acctAR, "0", "1000"))...)
				reverse(t, f.gov, goEntry, reversal)
				insertEntry(t, f.gov, goEntry2, "PAYMENT", goSource, "Payment cleared again", idr(acctBank, "1000", "0"), idr(acctAR, "0", "1000"))
				return maps
			},
			want: times(9, "go "), wantCheck: "has 2 entries that are not reversals",
		},
		{
			name: "payment on Go only with no Python payment stays unclassified", tables: journal,
			setup: func(f *rowsFixture) IDMaps {
				payment(t, f.gov, goSource, "MANUAL", "1000")
				insertEntry(t, f.gov, goEntry, "PAYMENT", goSource, "Payment cleared", idr(acctBank, "1000", "0"), idr(acctAR, "0", "1000"))
				return IDMaps{}
			},
			want: times(3, "go "), wantCheck: "pairs with 0 Python payments",
		},
		// --- PN 163 -------------------------------------------------------
		{
			name: "payment relieved at issue basis is PN 163", tables: journal,
			setup: func(f *rowsFixture) IDMaps {
				return paidPair(f, []jLine{usdBank, usdAR}, []jLine{usdBank, goAR, goLoss})
			},
			want: []string{"go PN 163", "go PN 163", "python PN 163"},
		},
		{
			name: "payment with FX lines on both sides is PN 163", tables: journal,
			setup: func(f *rowsFixture) IDMaps {
				pyAR := usd(acctAR, "0", "100", "0", "1480000", "14800")
				return paidPair(f, []jLine{usdBank, pyAR, idr(acctFXGain, "0", "20000")}, []jLine{usdBank, goAR, goLoss})
			},
			want: []string{"go PN 163", "go PN 163", "python PN 163", "python PN 163"},
		},
		{
			// Probe C: the bank line's base is at the issue rate.
			name: "payment bank base at another rate stays unclassified", tables: journal,
			setup: func(f *rowsFixture) IDMaps {
				goBank := usd(acctBank, "100", "0", "1600000", "0", "16000")
				return paidPair(f, []jLine{usdBank, usdAR}, []jLine{goBank, goAR})
			},
			want:      []string{"go ", "go ", "python ", "python "},
			wantCheck: "lines other than the receivable's base",
		},
		{
			// Probe D: the loss is not the receivable's base difference.
			name: "payment with a wrong FX loss stays unclassified", tables: journal,
			setup: func(f *rowsFixture) IDMaps {
				return paidPair(f, []jLine{usdBank, usdAR}, []jLine{usdBank, goAR, idr(acctFXLoss, "999999", "0")})
			},
			want:      []string{"go ", "go ", "python "},
			wantCheck: "go base debits",
		},
		{
			// Probe E: a gain where a loss is due.
			name: "payment with a gain for a loss stays unclassified", tables: journal,
			setup: func(f *rowsFixture) IDMaps {
				return paidPair(f, []jLine{usdBank, usdAR}, []jLine{usdBank, goAR, idr(acctFXGain, "0", "100000")})
			},
			want:      []string{"go ", "go ", "python "},
			wantCheck: "go base debits",
		},
		{
			// Both sides unbalanced by the same 100,000, so the FX change
			// still equals the receivable's change: only the balance check
			// refuses it.
			name: "payment unbalanced on both sides stays unclassified", tables: journal,
			setup: func(f *rowsFixture) IDMaps {
				pyAR := usd(acctAR, "0", "100", "0", "1400000", "14000")
				return paidPair(f, []jLine{usdBank, pyAR}, []jLine{usdBank, goAR, idr(acctFXLoss, "200000", "0")})
			},
			want:      []string{"go ", "go ", "python "},
			wantCheck: "python base debits",
		},
		{
			name: "payment bank line amount differs stays unclassified", tables: journal,
			setup: func(f *rowsFixture) IDMaps {
				goBank := usdBank
				goBank.debit = "99"
				return paidPair(f, []jLine{usdBank, usdAR}, []jLine{goBank, goAR, goLoss})
			},
			want: []string{"go ", "go ", "go ", "python ", "python "},
		},
		{
			name: "payment withholding line differs stays unclassified", tables: journal,
			setup: func(f *rowsFixture) IDMaps {
				goBank := usd(acctBank, "95", "0", "1425000", "0", "15000")
				wht := usd(acctWHT, "5", "0", "75000", "0", "15000")
				return paidPair(f, []jLine{usdBank, usdAR}, []jLine{goBank, wht, goAR, goLoss})
			},
			want:      []string{"go ", "go ", "go ", "go ", "python ", "python "},
			wantCheck: "lines other than the receivable's base",
		},
		// --- bills ----------------------------------------------------------
		{
			name: "bill voided by PATCH is PN 164", tables: bills,
			setup: func(f *rowsFixture) IDMaps { return bill(f, `status = 'VOID'`, voidedByPatch) },
			want:  []string{"both PN 164"},
		},
		{
			name: "bill voided by PATCH with another column differing stays unclassified", tables: bills,
			setup: func(f *rowsFixture) IDMaps { return bill(f, `status = 'VOID'`, voidedByPatch+`, bill_number = 'B-2'`) },
			want:  []string{"both "},
		},
		{
			name: "bill voided with another reason stays unclassified", tables: bills,
			setup: func(f *rowsFixture) IDMaps {
				return bill(f, `status = 'VOID'`, `status = 'VOID', balance_due = 0, notes = E'\n[voided] status set to VOID'`)
			},
			want: []string{"both "},
		},
		{
			name: "bill zeroed without a voided line stays unclassified", tables: bills,
			setup: func(f *rowsFixture) IDMaps { return bill(f, `status = 'VOID'`, `status = 'VOID', balance_due = 0`) },
			want:  []string{"both "},
		},
		{
			name: "bill voided on both with other notes stays unclassified", tables: bills,
			setup: func(f *rowsFixture) IDMaps {
				return bill(f, `status = 'VOID', balance_due = 0, notes = E'\n[voided] a'`, `status = 'VOID', balance_due = 0, notes = E'\n[voided] b'`)
			},
			want: []string{"both "},
		},
		{
			name: "bill open on Python, void on Go stays unclassified", tables: bills,
			setup: func(f *rowsFixture) IDMaps { return bill(f, `status = 'OPEN'`, voidedByPatch) },
			want:  []string{"both "},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newRowsFixture(t, tc.tables...)
			f.snapshot(t)
			maps := tc.setup(f)
			diffs := f.compare(t, maps)
			got := classified(diffs)
			for i, s := range got {
				// "table side classify" → "side classify"
				got[i] = s[strings.IndexByte(s, ' ')+1:]
			}
			want := append([]string(nil), tc.want...)
			sort.Strings(got)
			sort.Strings(want)
			if strings.Join(got, "|") != strings.Join(want, "|") {
				t.Fatalf("classified\n  %q\nwant\n  %q\ndiffs %+v", got, want, diffs)
			}
			if tc.wantCheck != "" {
				var found bool
				for _, d := range diffs {
					if d.Classify == "" && strings.Contains(d.Check, tc.wantCheck) {
						found = true
					}
				}
				if !found {
					t.Fatalf("no unclassified difference names the check %q: %+v", tc.wantCheck, diffs)
				}
			}
			for _, d := range diffs {
				if d.Classify != "" && d.Check != "" {
					t.Fatalf("difference %+v is classified and names a failed check", d)
				}
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
	goBill(t, f.gov, goVend, goBillID, "PARTIAL", "1000")
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
		// Go posts an issue entry (PN 162) and a payment entry for a payment
		// Python does not have (unclassified).
		insertGoInvoice(t, goPool, goSource, "OPEN", "IDR", "1000", "2026-01-01", false, "")
		insertEntry(t, goPool, goEntry, "INVOICE", goSource, "Invoice issued",
			idr(acctAR, "1000", "0"), idr(acctRevenue, "0", "1000"))
		insertEntry(t, goPool, goEntry2, "PAYMENT", goSource, "Payment cleared",
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
	notes, checks := 0, 0
	for _, d := range report.RowDiffs {
		if d.Classify == "PN 162" {
			notes++
		}
		if strings.HasPrefix(d.Check, "PN 161 check failed") {
			checks++
		}
	}
	if notes != 3 || checks != 3 || len(report.RowDiffs) != 6 {
		t.Fatalf("row diffs %+v, want all 6 listed, 3 of them PN 162 and 3 naming the PN 161 check", report.RowDiffs)
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

// TestWritesClassifiesAnswerPaths: classify_answers names the note for the
// listed JSON paths only; the differences are still counted, and a
// difference at another path stays unclassified.
func TestWritesClassifiesAnswerPaths(t *testing.T) {
	t.Parallel()
	pyDB, goDB := openTestDB(t), openTestDB(t)
	py := writeBackend(t, true, func(w http.ResponseWriter, _ *http.Request) {
		jsonBody(w, http.StatusOK, `{"balance_due":"1000.0000","notes":null,"name":"a"}`)
	})
	gov := writeBackend(t, false, func(w http.ResponseWriter, _ *http.Request) {
		jsonBody(w, http.StatusOK, `{"balance_due":"0.0000","notes":"\n[voided] x","name":"b"}`)
	})
	scenarios := WritesScenarios{Scenarios: []WritesScenario{{
		ID: "bill",
		Steps: []WriteStep{{
			ID: "patch", Method: http.MethodPatch, Path: "/api/v1/bills/x",
			ClassifyAnswers: &ClassifyAnswers{Note: "PN 164", Paths: []string{"$.balance_due", "$.notes"}},
		}},
	}}}
	report, err := RunWrites(context.Background(), writeOptions(t, py, gov, pyDB, goDB, scenarios))
	if err != nil {
		t.Fatalf("RunWrites: %v", err)
	}
	got := map[string]string{}
	for _, d := range report.Scenarios[0].Steps[0].Differences {
		got[d.Path] = d.Classify
	}
	if len(got) != 3 || got["$.balance_due"] != "PN 164" || got["$.notes"] != "PN 164" || got["$.name"] != "" {
		t.Fatalf("differences %+v, want balance_due and notes PN 164 and name unclassified", report.Scenarios[0].Steps[0].Differences)
	}
	if report.Differences != 3 || report.Unclassified != 1 {
		t.Fatalf("differences %d unclassified %d, want 3 and 1", report.Differences, report.Unclassified)
	}
}

// TestWritesParseNotes: every step note is exactly one PN, and
// classify_answers needs a PN, at least one $.path, and a compared answer.
func TestWritesParseNotes(t *testing.T) {
	t.Parallel()
	doc := func(step string) []byte {
		return []byte("scenarios:\n  - id: x\n    steps:\n      - {id: s, method: POST, path: /x" + step + "}\n")
	}
	cases := []struct{ name, step, wantErr string }{
		{"expect_status PN", `, expect_status: {python: 400, go: 200}, note: "PN 161"`, ""},
		{"expect_status free text", `, expect_status: {python: 400, go: 200}, note: "PN 161 reversal"`, "not one PN"},
		{"go_only no space", `, go_only: true, note: "PN165"`, "not one PN"},
		{"go_only words", `, go_only: true, note: "see 165"`, "not one PN"},
		{"classify_answers ok", `, classify_answers: {note: "PN 164", paths: ["$.notes"]}`, ""},
		{"classify_answers bad note", `, classify_answers: {note: "164", paths: ["$.notes"]}`, "not one PN"},
		{"classify_answers no paths", `, classify_answers: {note: "PN 164"}`, "names no paths"},
		{"classify_answers wildcard", `, classify_answers: {note: "PN 164", paths: ["$.*"]}`, "not one JSON path"},
		{"classify_answers root", `, classify_answers: {note: "PN 164", paths: ["$"]}`, "not one JSON path"},
		{"classify_answers on go_only", `, go_only: true, note: "PN 165", classify_answers: {note: "PN 165", paths: ["$.a"]}`, "does not apply"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseWrites(doc(tc.step))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("ParseWrites: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %v, want %q", err, tc.wantErr)
			}
		})
	}
}
