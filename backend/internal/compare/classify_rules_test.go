package compare

import (
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

const (
	pySource = "a1000000-0000-4000-8000-000000000001"
	goSource = "b1000000-0000-4000-8000-000000000001"
	pyEntry  = "a2000000-0000-4000-8000-000000000001"
	goEntry  = "b2000000-0000-4000-8000-000000000001"
	goEntry2 = "b2000000-0000-4000-8000-000000000002"
	goEntry3 = "b2000000-0000-4000-8000-000000000003"
	goIssue  = "b2000000-0000-4000-8000-000000000009"
	pyVend   = "a3000000-0000-4000-8000-000000000001"
	goVend   = "b3000000-0000-4000-8000-000000000001"
	pyBillID = "a4000000-0000-4000-8000-000000000001"
	goBillID = "b4000000-0000-4000-8000-000000000001"
	goInv    = "b6000000-0000-4000-8000-000000000001"
	pyInv    = "a6000000-0000-4000-8000-000000000001"
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

// postReversed inserts an entry and its mirroring reversal.
func postReversed(t *testing.T, pool *pgxpool.Pool, entryID, reversalID, source, sourceID string, lines ...jLine) {
	t.Helper()
	insertEntry(t, pool, entryID, source, sourceID, "Posted", lines...)
	insertEntry(t, pool, reversalID, source, sourceID, "Reversal: compare", flip(lines...)...)
	reverse(t, pool, entryID, reversalID)
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

// billPayment inserts a bill_payments row dated 2026-01-02.
func billPayment(t *testing.T, pool *pgxpool.Pool, id, bill, amount, status string) {
	t.Helper()
	mustExec(t, pool, `INSERT INTO bill_payments (bill_payment_id, bill_id, amount, currency, payment_date, status)
		VALUES ($1, $2, $3::numeric, 'IDR', '2026-01-02', $4)`, id, bill, amount, status)
}

// payment inserts one payment row dated 2026-01-02 (invoice "" is NULL).
func payment(t *testing.T, pool *pgxpool.Pool, id, invoice, intake, currency, amount, wht, status string) {
	t.Helper()
	mustExec(t, pool, `INSERT INTO payments (payment_id, invoice_id, amount, currency, payer_name, payment_date, intake_source, status, wht_amount)
		VALUES ($1, NULLIF($2, '')::uuid, $3::numeric, $4, 'P', '2026-01-02', $5, $6, $7::numeric)`, id, invoice, amount, currency, intake, status, wht)
}

// usdRate inserts a USD→IDR rate.
func usdRate(t *testing.T, pool *pgxpool.Pool, rate, day string) {
	t.Helper()
	mustExec(t, pool, `INSERT INTO fx_rates (from_currency, to_currency, rate, as_of_date, source) VALUES ('USD', 'IDR', $1::numeric, $2::date, 'TEST')`, rate, day)
}

// idrInvoice is Go's IDR 1000 invoice goInv and its issue entry.
func idrInvoice(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	insertGoInvoice(t, pool, goInv, "OPEN", "IDR", "1000", "2026-01-01", false, "")
	insertEntry(t, pool, goIssue, "INVOICE", goInv, "Invoice issued", idr(acctAR, "1000", "0"), idr(acctRevenue, "0", "1000"))
}

// usdInvoice is Go's USD invoice goInv of face, issued at base (16000 a
// dollar for 100), with the payment date's 15000 rate.
func usdInvoice(t *testing.T, pool *pgxpool.Pool, face, base, rate string) {
	t.Helper()
	insertGoInvoice(t, pool, goInv, "OPEN", "USD", face, "2026-01-01", false, "")
	insertEntry(t, pool, goIssue, "INVOICE", goInv, "Invoice issued",
		usd(acctAR, face, "0", base, "0", rate), usd(acctRevenue, "0", face, "0", base, rate))
	usdRate(t, pool, "15000", "2026-01-01")
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

// classifyCase is one rule case: pre runs before the snapshot (rows the
// dump already had, never compared), setup after it.
type classifyCase struct {
	name      string
	tables    []string
	pre       func(f *rowsFixture)
	setup     func(f *rowsFixture) IDMaps
	want      []string
	wantCheck string
}

// runClassifyCases runs each case: the differences' "side classify" (with
// "journal" for both journal tables) must equal want, an unclassified
// difference must name wantCheck, and no classified difference names a
// failed check.
func runClassifyCases(t *testing.T, cases []classifyCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newRowsFixture(t, tc.tables...)
			if tc.pre != nil {
				tc.pre(f)
			}
			f.snapshot(t)
			maps := tc.setup(f)
			diffs := f.compare(t, maps)
			got := classified(diffs)
			for i, s := range got {
				s = strings.Replace(strings.Replace(s, "journal_entries ", "journal ", 1), "journal_lines ", "journal ", 1)
				if strings.HasPrefix(s, "journal ") && len(tc.tables) == 2 && tc.tables[0] == "journal_entries" {
					s = strings.TrimPrefix(s, "journal ")
				}
				got[i] = s
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

var journal = []string{"journal_entries", "journal_lines"}

// TestClassifyIssueEntries: PN 162 and PN 164 entries, checked against
// their documents (review rounds 1 and 2).
func TestClassifyIssueEntries(t *testing.T) {
	t.Parallel()
	issue := []jLine{idr(acctAR, "1000", "0"), idr(acctRevenue, "0", "1000")}
	opened := []jLine{idr(acctExpense, "1000", "0"), idr(acctAP, "0", "1000")}
	invoice := func(status, amount string) func(f *rowsFixture) {
		return func(f *rowsFixture) {
			insertGoInvoice(t, f.gov, goSource, status, "IDR", amount, "2026-01-01", false, "")
		}
	}
	runClassifyCases(t, []classifyCase{
		{
			name: "invoice issue on Go only is PN 162", tables: journal, pre: invoice("OPEN", "1000"),
			setup: func(f *rowsFixture) IDMaps {
				insertEntry(t, f.gov, goEntry, "INVOICE", goSource, "Invoice issued", issue...)
				return IDMaps{}
			},
			want: times(3, "go PN 162"),
		},
		{
			name: "invoice void pair is PN 162", tables: journal, pre: invoice("VOID", "1000"),
			setup: func(f *rowsFixture) IDMaps {
				postReversed(t, f.gov, goEntry, goEntry2, "INVOICE", goSource, issue...)
				return IDMaps{}
			},
			want: times(6, "go PN 162"),
		},
		{
			// Round 2, probe 1: a void changes no amount; the reversed entry
			// is still checked against the document.
			name: "voided invoice whose issue posted another amount stays unclassified", tables: journal, pre: invoice("VOID", "1000"),
			setup: func(f *rowsFixture) IDMaps {
				postReversed(t, f.gov, goEntry, goEntry2, "INVOICE", goSource, idr(acctAR, "900", "0"), idr(acctRevenue, "0", "900"))
				return IDMaps{}
			},
			want: times(6, "go "), wantCheck: "control line is 900 IDR, want 1000 IDR",
		},
		{
			name: "voided USD invoice issued at another rate stays unclassified", tables: journal,
			pre: func(f *rowsFixture) {
				usdRate(t, f.gov, "14000", "1990-01-01")
				insertGoInvoice(t, f.gov, goSource, "VOID", "USD", "100", "1990-06-01", false, "")
			},
			setup: func(f *rowsFixture) IDMaps {
				postReversed(t, f.gov, goEntry, goEntry2, "INVOICE", goSource,
					usd(acctAR, "100", "0", "1500000", "0", "15000"), usd(acctRevenue, "0", "100", "0", "1500000", "15000"))
				return IDMaps{}
			},
			want: times(6, "go "), wantCheck: "not the USD rate 14000",
		},
		{
			// Round 2, probe 2.
			name: "VOID invoice with an active issue entry stays unclassified", tables: journal, pre: invoice("VOID", "1000"),
			setup: func(f *rowsFixture) IDMaps {
				insertEntry(t, f.gov, goEntry, "INVOICE", goSource, "Invoice issued", issue...)
				return IDMaps{}
			},
			want: times(3, "go "), wantCheck: "document is VOID",
		},
		{
			// An edit re-post: the reversed entry is for the old amount, the
			// active replacement for the current one.
			name: "edited invoice re-post is PN 162", tables: journal, pre: invoice("OPEN", "2000"),
			setup: func(f *rowsFixture) IDMaps {
				postReversed(t, f.gov, goEntry, goEntry2, "INVOICE", goSource, issue...)
				insertEntry(t, f.gov, goEntry3, "INVOICE", goSource, "Invoice issued", idr(acctAR, "2000", "0"), idr(acctRevenue, "0", "2000"))
				return IDMaps{}
			},
			want: times(9, "go PN 162"),
		},
		{
			name: "void reversal that does not mirror stays unclassified", tables: journal, pre: invoice("VOID", "1000"),
			setup: func(f *rowsFixture) IDMaps {
				insertEntry(t, f.gov, goEntry, "INVOICE", goSource, "Invoice issued", issue...)
				insertEntry(t, f.gov, goEntry2, "INVOICE", goSource, "Void", idr(acctAR, "0", "900"), idr(acctRevenue, "900", "0"))
				reverse(t, f.gov, goEntry, goEntry2)
				return IDMaps{}
			},
			want: append(times(3, "go PN 162"), times(3, "go ")...), wantCheck: "do not mirror",
		},
		{
			name: "unbalanced invoice entry stays unclassified", tables: journal, pre: invoice("OPEN", "1000"),
			setup: func(f *rowsFixture) IDMaps {
				insertEntry(t, f.gov, goEntry, "INVOICE", goSource, "Invoice issued", idr(acctAR, "1000", "0"), idr(acctRevenue, "0", "900"))
				return IDMaps{}
			},
			want: times(3, "go "), wantCheck: "base debits 1000 ≠ base credits 900",
		},
		{
			// Round 2, probe 3: every line's account is checked.
			name: "invoice crediting an expense account stays unclassified", tables: journal, pre: invoice("OPEN", "1000"),
			setup: func(f *rowsFixture) IDMaps {
				insertEntry(t, f.gov, goEntry, "INVOICE", goSource, "Invoice issued", idr(acctAR, "1000", "0"), idr(acctExpense, "0", "1000"))
				return IDMaps{}
			},
			want: times(3, "go "), wantCheck: "credit line on 5000 (COGS) is not allowed on a INVOICE entry",
		},
		{
			name: "two issue entries for one invoice stay unclassified", tables: journal, pre: invoice("OPEN", "1000"),
			setup: func(f *rowsFixture) IDMaps {
				insertEntry(t, f.gov, goEntry, "INVOICE", goSource, "Invoice issued", issue...)
				insertEntry(t, f.gov, goEntry2, "INVOICE", goSource, "Invoice issued", issue...)
				return IDMaps{}
			},
			want: times(6, "go "), wantCheck: "2 active entries",
		},
		{
			name: "issue entry of a DRAFT invoice stays unclassified", tables: journal, pre: invoice("DRAFT", "1000"),
			setup: func(f *rowsFixture) IDMaps {
				insertEntry(t, f.gov, goEntry, "INVOICE", goSource, "Invoice issued", issue...)
				return IDMaps{}
			},
			want: times(3, "go "), wantCheck: "document is DRAFT",
		},
		{
			name: "issue entry of a template stays unclassified", tables: journal,
			pre: func(f *rowsFixture) {
				insertGoInvoice(t, f.gov, goSource, "OPEN", "IDR", "1000", "2026-01-01", true, "")
			},
			setup: func(f *rowsFixture) IDMaps {
				insertEntry(t, f.gov, goEntry, "INVOICE", goSource, "Invoice issued", issue...)
				return IDMaps{}
			},
			want: times(3, "go "), wantCheck: "template true",
		},
		{
			name: "issue entry for another amount stays unclassified", tables: journal, pre: invoice("OPEN", "2000"),
			setup: func(f *rowsFixture) IDMaps {
				insertEntry(t, f.gov, goEntry, "INVOICE", goSource, "Invoice issued", issue...)
				return IDMaps{}
			},
			want: times(3, "go "), wantCheck: "control line is 1000 IDR, want 2000 IDR",
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
			pre: func(f *rowsFixture) {
				usdRate(t, f.gov, "14000", "1990-01-01")
				usdRate(t, f.gov, "17000", "1990-07-01")
				insertGoInvoice(t, f.gov, goSource, "OPEN", "USD", "100", "1990-06-01", false, "")
			},
			setup: func(f *rowsFixture) IDMaps {
				insertEntry(t, f.gov, goEntry, "INVOICE", goSource, "Invoice issued",
					usd(acctAR, "100", "0", "1400000", "0", "14000"), usd(acctRevenue, "0", "100", "0", "1400000", "14000"))
				return IDMaps{}
			},
			want: times(3, "go PN 162"),
		},
		{
			name: "USD invoice with no issue date uses its created_at date", tables: journal,
			pre: func(f *rowsFixture) {
				usdRate(t, f.gov, "14000", "1990-01-01")
				insertGoInvoice(t, f.gov, goSource, "OPEN", "USD", "100", "", false, "1990-06-01 10:00")
			},
			setup: func(f *rowsFixture) IDMaps {
				insertEntry(t, f.gov, goEntry, "INVOICE", goSource, "Invoice issued",
					usd(acctAR, "100", "0", "1400000", "0", "14000"), usd(acctRevenue, "0", "100", "0", "1400000", "14000"))
				return IDMaps{}
			},
			want: times(3, "go PN 162"),
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
			name: "paired invoice entries with other lines stay unclassified", tables: journal,
			setup: func(f *rowsFixture) IDMaps {
				insertEntry(t, f.py, pyEntry, "INVOICE", pySource, "Invoice issued", issue...)
				insertEntry(t, f.gov, goEntry, "INVOICE", goSource, "Invoice issued", idr(acctAR, "900", "0"), idr(acctRevenue, "0", "900"))
				return mapsOf(t, "invoices", pySource, goSource)
			},
			want: append(times(2, "go "), times(2, "python ")...),
		},
		{
			name: "bill open on Go only is PN 164", tables: journal,
			pre: func(f *rowsFixture) { goBill(t, f.gov, goVend, goSource, "OPEN", "1000") },
			setup: func(f *rowsFixture) IDMaps {
				insertEntry(t, f.gov, goEntry, "BILL", goSource, "Bill opened", opened...)
				return IDMaps{}
			},
			want: times(3, "go PN 164"),
		},
		{
			name: "voided bill pair is PN 164", tables: journal,
			pre: func(f *rowsFixture) { goBill(t, f.gov, goVend, goSource, "VOID", "1000") },
			setup: func(f *rowsFixture) IDMaps {
				postReversed(t, f.gov, goEntry, goEntry2, "BILL", goSource, opened...)
				return IDMaps{}
			},
			want: times(6, "go PN 164"),
		},
		{
			name: "voided bill whose open entry posted another amount stays unclassified", tables: journal,
			pre: func(f *rowsFixture) { goBill(t, f.gov, goVend, goSource, "VOID", "1000") },
			setup: func(f *rowsFixture) IDMaps {
				postReversed(t, f.gov, goEntry, goEntry2, "BILL", goSource, idr(acctExpense, "900", "0"), idr(acctAP, "0", "900"))
				return IDMaps{}
			},
			want: times(6, "go "), wantCheck: "control line is 900 IDR, want 1000 IDR",
		},
		{
			name: "VOID bill with an active open entry stays unclassified", tables: journal,
			pre: func(f *rowsFixture) { goBill(t, f.gov, goVend, goSource, "VOID", "1000") },
			setup: func(f *rowsFixture) IDMaps {
				insertEntry(t, f.gov, goEntry, "BILL", goSource, "Bill opened", opened...)
				return IDMaps{}
			},
			want: times(3, "go "), wantCheck: "document is VOID",
		},
		{
			name: "bill entry crediting AR stays unclassified", tables: journal,
			pre: func(f *rowsFixture) { goBill(t, f.gov, goVend, goSource, "OPEN", "1000") },
			setup: func(f *rowsFixture) IDMaps {
				insertEntry(t, f.gov, goEntry, "BILL", goSource, "Bill opened", idr(acctExpense, "1000", "0"), idr(acctAR, "0", "1000"))
				return IDMaps{}
			},
			want: times(3, "go "), wantCheck: "credit line on 1100 (ASSET) is not allowed on a BILL entry",
		},
		{
			name: "bill debiting a revenue account stays unclassified", tables: journal,
			pre: func(f *rowsFixture) { goBill(t, f.gov, goVend, goSource, "OPEN", "1000") },
			setup: func(f *rowsFixture) IDMaps {
				insertEntry(t, f.gov, goEntry, "BILL", goSource, "Bill opened", idr(acctRevenue, "1000", "0"), idr(acctAP, "0", "1000"))
				return IDMaps{}
			},
			want: times(3, "go "), wantCheck: "debit line on 4000 (INCOME) is not allowed on a BILL entry",
		},
	})
}

// TestClassifyBillPayments: PN 165 entries must match a Go bill_payments
// row (review round 2).
func TestClassifyBillPayments(t *testing.T) {
	t.Parallel()
	paid := []jLine{idr(acctAP, "400", "0"), idr(acctBank, "0", "400")}
	// The bill and its BILL entry are on Go before the snapshot: the
	// payable's issue basis (PN 164) is 1000 at 1000.
	bill := func(f *rowsFixture) {
		goBill(t, f.gov, goVend, goBillID, "PARTIAL", "1000")
		insertEntry(t, f.gov, goIssue, "BILL", goBillID, "Bill opened", idr(acctExpense, "1000", "0"), idr(acctAP, "0", "1000"))
	}
	runClassifyCases(t, []classifyCase{
		{
			// Review: the 2000 debit is checked against the bill's basis
			// (IDR: base = face), not only its face, currency and bank.
			name: "bill payment relieving the payable off its basis stays unclassified", tables: journal, pre: bill,
			setup: func(f *rowsFixture) IDMaps {
				billPayment(t, f.gov, goSource, goBillID, "400", "CLEARED")
				insertEntry(t, f.gov, goEntry, "BILL_PAYMENT", goSource, "Bill paid",
					jLine{account: acctAP, debit: "400", credit: "0", currency: "IDR", baseDebit: "500", baseCredit: "0", ratio: "1.25"},
					idr(acctBank, "0", "400"), idr(acctFXGain, "0", "100"))
				return IDMaps{}
			},
			want: times(4, "go "), wantCheck: "its payable base 500 for 400 is not the bill's issue basis",
		},
		{
			name: "bill payment on Go only is PN 165", tables: journal, pre: bill,
			setup: func(f *rowsFixture) IDMaps {
				billPayment(t, f.gov, goSource, goBillID, "400", "CLEARED")
				insertEntry(t, f.gov, goEntry, "BILL_PAYMENT", goSource, "Bill paid", paid...)
				return IDMaps{}
			},
			want: times(3, "go PN 165"),
		},
		{
			name: "reversed bill payment is PN 165", tables: journal, pre: bill,
			setup: func(f *rowsFixture) IDMaps {
				billPayment(t, f.gov, goSource, goBillID, "400", "REVERSED")
				postReversed(t, f.gov, goEntry, goEntry2, "BILL_PAYMENT", goSource, paid...)
				return IDMaps{}
			},
			want: times(6, "go PN 165"),
		},
		{
			name: "bill payment entry without its row stays unclassified", tables: journal, pre: bill,
			setup: func(f *rowsFixture) IDMaps {
				insertEntry(t, f.gov, goEntry, "BILL_PAYMENT", goSource, "Bill paid", paid...)
				return IDMaps{}
			},
			want: times(3, "go "), wantCheck: "does not exist",
		},
		{
			name: "bill payment entry for another amount stays unclassified", tables: journal, pre: bill,
			setup: func(f *rowsFixture) IDMaps {
				billPayment(t, f.gov, goSource, goBillID, "500", "CLEARED")
				insertEntry(t, f.gov, goEntry, "BILL_PAYMENT", goSource, "Bill paid", paid...)
				return IDMaps{}
			},
			want: times(3, "go "), wantCheck: "payable line is 400 IDR, the bill payment 500 IDR",
		},
		{
			name: "active bill payment entry of a REVERSED row stays unclassified", tables: journal, pre: bill,
			setup: func(f *rowsFixture) IDMaps {
				billPayment(t, f.gov, goSource, goBillID, "400", "REVERSED")
				insertEntry(t, f.gov, goEntry, "BILL_PAYMENT", goSource, "Bill paid", paid...)
				return IDMaps{}
			},
			want: times(3, "go "), wantCheck: "is REVERSED, want CLEARED",
		},
		{
			name: "bill payment crediting AR stays unclassified", tables: journal, pre: bill,
			setup: func(f *rowsFixture) IDMaps {
				billPayment(t, f.gov, goSource, goBillID, "400", "CLEARED")
				insertEntry(t, f.gov, goEntry, "BILL_PAYMENT", goSource, "Bill paid", idr(acctAP, "400", "0"), idr(acctAR, "0", "400"))
				return IDMaps{}
			},
			want: times(3, "go "), wantCheck: "credit line on 1100 (ASSET) is not allowed on a BILL_PAYMENT entry",
		},
		{
			name: "bill payment entry with no lines stays unclassified", tables: journal, pre: bill,
			setup: func(f *rowsFixture) IDMaps {
				billPayment(t, f.gov, goSource, goBillID, "400", "CLEARED")
				insertEntry(t, f.gov, goEntry, "BILL_PAYMENT", goSource, "Bill paid")
				return IDMaps{}
			},
			want: times(1, "go "), wantCheck: "it has 0 lines",
		},
	})
}

// TestClassifyManualPayments: PN 161 entries (review rounds 1 and 2).
func TestClassifyManualPayments(t *testing.T) {
	t.Parallel()
	cleared := []jLine{idr(acctBank, "1000", "0"), idr(acctAR, "0", "1000")}
	pre := func(f *rowsFixture) { idrInvoice(t, f.gov) }
	// manual is a manual payment of amount (with wht withheld) on both sides,
	// and its Go entry.
	manual := func(pyIntake, amount, wht string, lines ...jLine) func(f *rowsFixture) IDMaps {
		return func(f *rowsFixture) IDMaps {
			payment(t, f.py, pySource, "", pyIntake, "IDR", amount, wht, "CLEARED")
			payment(t, f.gov, goSource, goInv, "MANUAL", "IDR", amount, wht, "CLEARED")
			insertEntry(t, f.gov, goEntry, "PAYMENT", goSource, "Payment cleared", lines...)
			return mapsOf(t, "payments", pySource, goSource)
		}
	}
	runClassifyCases(t, []classifyCase{
		{
			name: "manual payment on Go only is PN 161", tables: journal, pre: pre,
			setup: manual("MANUAL", "1000", "0", cleared...),
			want:  times(3, "go PN 161"),
		},
		{
			name: "manual payment with withholding is PN 161", tables: journal, pre: pre,
			setup: manual("MANUAL", "900", "100", idr(acctBank, "900", "0"), idr(acctWHT, "100", "0"), idr(acctAR, "0", "1000")),
			want:  times(4, "go PN 161"),
		},
		{
			// Round 2, probe 5.
			name: "manual payment without its withholding line stays unclassified", tables: journal, pre: pre,
			setup: manual("MANUAL", "900", "100", idr(acctBank, "900", "0"), idr(acctAR, "0", "900")),
			want:  times(3, "go "), wantCheck: "0 withholding lines for withholding of 100",
		},
		{
			name: "manual payment and its reversal are PN 161", tables: journal, pre: pre,
			setup: func(f *rowsFixture) IDMaps {
				maps := manual("MANUAL", "1000", "0", cleared...)(f)
				insertEntry(t, f.gov, goEntry2, "PAYMENT", goSource, "Reversal: compare", flip(cleared...)...)
				reverse(t, f.gov, goEntry, goEntry2)
				mustExec(t, f.gov, `UPDATE payments SET status = 'FLAGGED' WHERE payment_id = $1`, goSource)
				mustExec(t, f.gov, `INSERT INTO reconciliation_log (log_id, payment_id, action, reasons) VALUES (gen_random_uuid(), $1, 'REVERSED', ARRAY['compare'])`, goSource)
				return maps
			},
			want: times(6, "go PN 161"),
		},
		{
			// Review: a half-done reversal (the entry and its mirror posted,
			// the payment left CLEARED, no REVERSED log row) is not PN 161.
			name: "reversed entry of a payment left CLEARED stays unclassified", tables: journal, pre: pre,
			setup: func(f *rowsFixture) IDMaps {
				maps := manual("MANUAL", "1000", "0", cleared...)(f)
				insertEntry(t, f.gov, goEntry2, "PAYMENT", goSource, "Reversal: compare", flip(cleared...)...)
				reverse(t, f.gov, goEntry, goEntry2)
				return maps
			},
			want: times(6, "go "), wantCheck: "is CLEARED with 0 REVERSED log rows, want FLAGGED with 1",
		},
		{
			name: "reversed entry of a FLAGGED payment without its log row stays unclassified", tables: journal, pre: pre,
			setup: func(f *rowsFixture) IDMaps {
				maps := manual("MANUAL", "1000", "0", cleared...)(f)
				insertEntry(t, f.gov, goEntry2, "PAYMENT", goSource, "Reversal: compare", flip(cleared...)...)
				reverse(t, f.gov, goEntry, goEntry2)
				mustExec(t, f.gov, `UPDATE payments SET status = 'FLAGGED' WHERE payment_id = $1`, goSource)
				return maps
			},
			want: times(6, "go "), wantCheck: "is FLAGGED with 0 REVERSED log rows, want FLAGGED with 1",
		},
		{
			name: "active entry of a FLAGGED payment stays unclassified", tables: journal, pre: pre,
			setup: func(f *rowsFixture) IDMaps {
				maps := manual("MANUAL", "1000", "0", cleared...)(f)
				mustExec(t, f.gov, `UPDATE payments SET status = 'FLAGGED' WHERE payment_id = $1`, goSource)
				return maps
			},
			want: times(3, "go "), wantCheck: "is FLAGGED with 0 REVERSED log rows, want CLEARED with 0",
		},
		{
			name: "USD manual payment at the payment date's rate is PN 161", tables: journal,
			pre: func(f *rowsFixture) { usdInvoice(t, f.gov, "100", "1600000", "16000") },
			setup: func(f *rowsFixture) IDMaps {
				payment(t, f.py, pySource, "", "MANUAL", "USD", "100", "0", "CLEARED")
				payment(t, f.gov, goSource, goInv, "MANUAL", "USD", "100", "0", "CLEARED")
				insertEntry(t, f.gov, goEntry, "PAYMENT", goSource, "Payment cleared",
					usd(acctBank, "100", "0", "1500000", "0", "15000"), usd(acctAR, "0", "100", "0", "1600000", "16000"), idr(acctFXLoss, "100000", "0"))
				return mapsOf(t, "payments", pySource, goSource)
			},
			want: times(4, "go PN 161"),
		},
		{
			// Round 2, probe 5: the bank base is not at the payment date's rate.
			name: "USD manual payment banked at the issue rate stays unclassified", tables: journal,
			pre: func(f *rowsFixture) { usdInvoice(t, f.gov, "100", "1600000", "16000") },
			setup: func(f *rowsFixture) IDMaps {
				payment(t, f.py, pySource, "", "MANUAL", "USD", "100", "0", "CLEARED")
				payment(t, f.gov, goSource, goInv, "MANUAL", "USD", "100", "0", "CLEARED")
				insertEntry(t, f.gov, goEntry, "PAYMENT", goSource, "Payment cleared",
					usd(acctBank, "100", "0", "1600000", "0", "16000"), usd(acctAR, "0", "100", "0", "1600000", "16000"))
				return mapsOf(t, "payments", pySource, goSource)
			},
			want: times(3, "go "), wantCheck: "bank line's rate 16000 and base 1600000 are not the USD rate 15000",
		},
		{
			name: "webhook payment on Go only stays unclassified", tables: journal, pre: pre,
			setup: manual("WEBHOOK", "1000", "0", cleared...),
			want:  times(3, "go "), wantCheck: "intake_source is MANUAL on Go and WEBHOOK on Python",
		},
		{
			name: "manual payment entry for another amount stays unclassified", tables: journal, pre: pre,
			setup: manual("MANUAL", "1000", "0", idr(acctBank, "900", "0"), idr(acctAR, "0", "900")),
			want:  times(3, "go "), wantCheck: "bank line is 900 IDR, want 1000 IDR",
		},
		{
			name: "manual payment crediting revenue stays unclassified", tables: journal, pre: pre,
			setup: manual("MANUAL", "1000", "0", idr(acctBank, "1000", "0"), idr(acctRevenue, "0", "1000")),
			want:  times(3, "go "), wantCheck: "credit line on 4000 (INCOME) is not allowed on a PAYMENT entry",
		},
		{
			// Cleared, reversed and cleared again: one active entry, but two
			// that are not reversals.
			name: "second clearing of a manual payment stays unclassified", tables: journal, pre: pre,
			setup: func(f *rowsFixture) IDMaps {
				maps := manual("MANUAL", "1000", "0", cleared...)(f)
				insertEntry(t, f.gov, goEntry3, "PAYMENT", goSource, "Reversal: compare", flip(cleared...)...)
				reverse(t, f.gov, goEntry, goEntry3)
				insertEntry(t, f.gov, goEntry2, "PAYMENT", goSource, "Payment cleared again", cleared...)
				return maps
			},
			want: times(9, "go "), wantCheck: "has 2 entries that are not reversals",
		},
		{
			name: "payment on Go only with no Python payment stays unclassified", tables: journal, pre: pre,
			setup: func(f *rowsFixture) IDMaps {
				payment(t, f.gov, goSource, goInv, "MANUAL", "IDR", "1000", "0", "CLEARED")
				insertEntry(t, f.gov, goEntry, "PAYMENT", goSource, "Payment cleared", cleared...)
				return IDMaps{}
			},
			want: times(3, "go "), wantCheck: "pairs with 0 Python payments",
		},
	})
}

// TestClassifyPaymentLines: PN 163 on a paired PAYMENT entry (review rounds 1
// and 2). Go's invoice goInv was issued at 16000 a dollar (USD 100, base
// 1,600,000); the payment is banked at the payment date's 15000.
func TestClassifyPaymentLines(t *testing.T) {
	t.Parallel()
	usdBank := usd(acctBank, "100", "0", "1500000", "0", "15000")
	usdAR := usd(acctAR, "0", "100", "0", "1500000", "15000")
	goAR := usd(acctAR, "0", "100", "0", "1600000", "16000")
	goLoss := idr(acctFXLoss, "100000", "0")
	pre := func(f *rowsFixture) { usdInvoice(t, f.gov, "100", "1600000", "16000") }
	paidPair := func(pyLines, goLines []jLine) func(f *rowsFixture) IDMaps {
		return func(f *rowsFixture) IDMaps {
			payment(t, f.gov, goSource, goInv, "WEBHOOK", "USD", "100", "0", "CLEARED")
			insertEntry(t, f.py, pyEntry, "PAYMENT", pySource, "Payment cleared", pyLines...)
			insertEntry(t, f.gov, goEntry, "PAYMENT", goSource, "Payment cleared", goLines...)
			return mapsOf(t, "payments", pySource, goSource)
		}
	}
	runClassifyCases(t, []classifyCase{
		{
			name: "payment relieved at issue basis is PN 163", tables: journal, pre: pre,
			setup: paidPair([]jLine{usdBank, usdAR}, []jLine{usdBank, goAR, goLoss}),
			want:  []string{"go PN 163", "go PN 163", "python PN 163"},
		},
		{
			name: "payment with FX lines on both sides is PN 163", tables: journal, pre: pre,
			setup: paidPair([]jLine{usdBank, usd(acctAR, "0", "100", "0", "1480000", "14800"), idr(acctFXGain, "0", "20000")},
				[]jLine{usdBank, goAR, goLoss}),
			want: []string{"go PN 163", "go PN 163", "python PN 163", "python PN 163"},
		},
		{
			// The last relief of a partly paid invoice takes the remaining
			// basis (1,600,000.0001 less the first relief's 640,000.0003),
			// which a share of the issue basis would miss by rounding.
			name: "last partial relief at the remaining basis is PN 163", tables: journal,
			pre: func(f *rowsFixture) {
				usdInvoice(t, f.gov, "100", "1600000.0001", "16000.000001")
				const first = "b1000000-0000-4000-8000-000000000002"
				payment(t, f.gov, first, goInv, "WEBHOOK", "USD", "40", "0", "CLEARED")
				insertEntry(t, f.gov, "b2000000-0000-4000-8000-000000000008", "PAYMENT", first, "Payment cleared",
					usd(acctBank, "40", "0", "600000", "0", "15000"), usd(acctAR, "0", "40", "0", "640000.0003", "16000.00000750"),
					idr(acctFXLoss, "40000.0003", "0"))
			},
			setup: func(f *rowsFixture) IDMaps {
				payment(t, f.gov, goSource, goInv, "WEBHOOK", "USD", "60", "0", "CLEARED")
				bank := usd(acctBank, "60", "0", "900000", "0", "15000")
				insertEntry(t, f.py, pyEntry, "PAYMENT", pySource, "Payment cleared", bank, usd(acctAR, "0", "60", "0", "900000", "15000"))
				insertEntry(t, f.gov, goEntry, "PAYMENT", goSource, "Payment cleared",
					bank, usd(acctAR, "0", "60", "0", "959999.9998", "15999.99999667"), idr(acctFXLoss, "59999.9998", "0"))
				return mapsOf(t, "payments", pySource, goSource)
			},
			want: []string{"go PN 163", "go PN 163", "python PN 163"},
		},
		{
			// Round 2, probe 4: balanced and consistent, but the receivable
			// is not relieved at the issue basis.
			name: "payment relieved at another basis stays unclassified", tables: journal, pre: pre,
			setup: paidPair([]jLine{usdBank, usdAR}, []jLine{usdBank, usd(acctAR, "0", "100", "0", "1550000", "15500"), idr(acctFXLoss, "50000", "0")}),
			want:  []string{"go ", "go ", "python "}, wantCheck: "is not the invoice's issue basis",
		},
		{
			name: "payment with an FX line in another currency stays unclassified", tables: journal, pre: pre,
			setup: paidPair([]jLine{usdBank, usdAR}, []jLine{usdBank, goAR, {acctFXLoss, "100000", "0", "USD", "100000", "0", "1"}}),
			want:  []string{"go ", "go ", "python "}, wantCheck: "is not in IDR at its base",
		},
		{
			name: "payment with a debited gain account stays unclassified", tables: journal, pre: pre,
			setup: paidPair([]jLine{usdBank, usdAR}, []jLine{usdBank, goAR, idr(acctFXGain, "100000", "0")}),
			want:  []string{"go ", "go ", "python "}, wantCheck: "FX line on 4900 is on the wrong side",
		},
		{
			// Probe C: the bank line's base is at the issue rate.
			name: "payment bank base at another rate stays unclassified", tables: journal, pre: pre,
			setup: paidPair([]jLine{usdBank, usdAR}, []jLine{usd(acctBank, "100", "0", "1600000", "0", "16000"), goAR}),
			want:  []string{"go ", "go ", "python ", "python "}, wantCheck: "lines other than the receivable's base",
		},
		{
			// Probe D: the loss is not the receivable's base difference.
			name: "payment with a wrong FX loss stays unclassified", tables: journal, pre: pre,
			setup: paidPair([]jLine{usdBank, usdAR}, []jLine{usdBank, goAR, idr(acctFXLoss, "999999", "0")}),
			want:  []string{"go ", "go ", "python "}, wantCheck: "go base debits",
		},
		{
			// Probe E: a gain where a loss is due.
			name: "payment with a gain for a loss stays unclassified", tables: journal, pre: pre,
			setup: paidPair([]jLine{usdBank, usdAR}, []jLine{usdBank, goAR, idr(acctFXGain, "0", "100000")}),
			want:  []string{"go ", "go ", "python "}, wantCheck: "go base debits",
		},
		{
			// Both sides unbalanced by the same 200,000 − 100,000.
			name: "payment unbalanced on both sides stays unclassified", tables: journal, pre: pre,
			setup: paidPair([]jLine{usdBank, usd(acctAR, "0", "100", "0", "1400000", "14000")}, []jLine{usdBank, goAR, idr(acctFXLoss, "200000", "0")}),
			want:  []string{"go ", "go ", "python "}, wantCheck: "python base debits",
		},
		{
			name: "payment bank line amount differs stays unclassified", tables: journal, pre: pre,
			setup: paidPair([]jLine{usdBank, usdAR}, []jLine{usd(acctBank, "99", "0", "1500000", "0", "15000"), goAR, goLoss}),
			want:  []string{"go ", "go ", "go ", "python ", "python "},
		},
		{
			name: "payment withholding line differs stays unclassified", tables: journal, pre: pre,
			setup: paidPair([]jLine{usdBank, usdAR}, []jLine{
				usd(acctBank, "95", "0", "1425000", "0", "15000"), usd(acctWHT, "5", "0", "75000", "0", "15000"), goAR, goLoss,
			}),
			want: []string{"go ", "go ", "go ", "go ", "python ", "python "}, wantCheck: "lines other than the receivable's base",
		},
	})
}

// TestClassifyBillRows: a bill voided by PATCH (PN 164).
func TestClassifyBillRows(t *testing.T) {
	t.Parallel()
	bill := func(py, gov string) func(f *rowsFixture) IDMaps {
		return func(f *rowsFixture) IDMaps {
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
	}
	const voidedByPatch = `status = 'VOID', balance_due = 0, notes = E'\n[voided] status set to VOID via update'`
	bills := []string{"vendors", "bills"}
	runClassifyCases(t, []classifyCase{
		{name: "bill voided by PATCH is PN 164", tables: bills, setup: bill(`status = 'VOID'`, voidedByPatch), want: []string{"bills both PN 164"}},
		{
			name: "bill voided by PATCH with another column differing stays unclassified", tables: bills,
			setup: bill(`status = 'VOID'`, voidedByPatch+`, bill_number = 'B-2'`), want: []string{"bills both "},
		},
		{
			name: "bill voided with another reason stays unclassified", tables: bills,
			setup: bill(`status = 'VOID'`, `status = 'VOID', balance_due = 0, notes = E'\n[voided] status set to VOID'`), want: []string{"bills both "},
		},
		{
			name: "bill zeroed without a voided line stays unclassified", tables: bills,
			setup: bill(`status = 'VOID'`, `status = 'VOID', balance_due = 0`), want: []string{"bills both "},
		},
		{
			name: "bill voided on both with other notes stays unclassified", tables: bills,
			setup: bill(`status = 'VOID', balance_due = 0, notes = E'\n[voided] a'`, `status = 'VOID', balance_due = 0, notes = E'\n[voided] b'`),
			want:  []string{"bills both "},
		},
		{
			name: "bill open on Python, void on Go stays unclassified", tables: bills,
			setup: bill(`status = 'OPEN'`, voidedByPatch), want: []string{"bills both "},
		},
	})
}

// TestClassifyReversedManualPayment: Go reverses a manual payment that
// Python refuses to reverse (PN 161). The payment (FLAGGED), the invoice (as
// payments.Service.Reverse sets it) and Go's one REVERSED log row (with the
// reversal's reason) are PN 161 only when the payment's Go clearing and
// reversal are PN 161 entries and the rows differ by exactly the reversal.
func TestClassifyReversedManualPayment(t *testing.T) {
	t.Parallel()
	tables := []string{"invoices", "payments", "reconciliation_log", "journal_entries", "journal_lines"}
	type sideState struct{ status, balance, whtBooked string }
	cases := []struct {
		name     string
		py, gov  sideState
		amount   string // the invoice amount; "" is 1000
		wht      string
		reversed bool
		logs     []string
		want     []string
		// journalFails: the PAYMENT entries are unclassified (the Go
		// payment or its REVERSED rows do not match the entry).
		journalFails bool
	}{
		{
			name: "reversed", py: sideState{"PAID", "0", "0"}, gov: sideState{"OPEN", "1000", "0"}, wht: "0", reversed: true, logs: []string{"compare"},
			want: []string{"invoices both PN 161", "payments both PN 161", "reconciliation_log go PN 161"},
		},
		{
			// PN 168: below the amount the invoice is PARTIAL (another
			// payment of 600 still stands).
			name: "reversed on a PARTIAL invoice stays PARTIAL", amount: "2000", py: sideState{"PARTIAL", "400", "0"}, gov: sideState{"PARTIAL", "1400", "0"}, wht: "0", reversed: true, logs: []string{"compare"},
			want: []string{"invoices both PN 161", "payments both PN 161", "reconciliation_log go PN 161"},
		},
		{
			name: "reversed below the amount but re-opened", amount: "2000", py: sideState{"PARTIAL", "400", "0"}, gov: sideState{"OPEN", "1400", "0"}, wht: "0", reversed: true, logs: []string{"compare"},
			want: []string{"invoices both ", "payments both PN 161", "reconciliation_log go PN 161"},
		},
		{
			name: "reversed on a PARTIAL invoice back to its amount re-opened", amount: "1400", py: sideState{"PARTIAL", "400", "0"}, gov: sideState{"OPEN", "1400", "0"}, wht: "0", reversed: true, logs: []string{"compare"},
			want: []string{"invoices both PN 161", "payments both PN 161", "reconciliation_log go PN 161"},
		},
		{
			// Go refuses a reversal that would leave more than the amount.
			name: "restored above the amount", py: sideState{"PARTIAL", "400", "0"}, gov: sideState{"OPEN", "1400", "0"}, wht: "0", reversed: true, logs: []string{"compare"},
			want: []string{"invoices both ", "payments both PN 161", "reconciliation_log go PN 161"},
		},
		{
			// A VOID invoice keeps its status and balance; wht_booked drops.
			name: "reversed on a VOID invoice with withholding", py: sideState{"VOID", "0", "100"}, gov: sideState{"VOID", "0", "0"}, wht: "100", reversed: true, logs: []string{"compare"},
			want: []string{"invoices both PN 161", "payments both PN 161", "reconciliation_log go PN 161"},
		},
		{
			name: "balance off by one", py: sideState{"PAID", "0", "0"}, gov: sideState{"OPEN", "999", "0"}, wht: "0", reversed: true, logs: []string{"compare"},
			want: []string{"invoices both ", "payments both PN 161", "reconciliation_log go PN 161"},
		},
		{
			name: "log with another reason", py: sideState{"PAID", "0", "0"}, gov: sideState{"OPEN", "1000", "0"}, wht: "0", reversed: true, logs: []string{"other"},
			want: []string{"invoices both PN 161", "payments both PN 161", "reconciliation_log go "},
		},
		{
			// The PAYMENT entries want exactly one REVERSED row, so nothing
			// of the reversal is classified.
			name: "two REVERSED logs", py: sideState{"PAID", "0", "0"}, gov: sideState{"OPEN", "1000", "0"}, wht: "0", reversed: true, logs: []string{"compare", "compare"},
			want: []string{"invoices both ", "payments both ", "reconciliation_log go ", "reconciliation_log go "}, journalFails: true,
		},
		{
			name: "clearing not reversed", py: sideState{"PAID", "0", "0"}, gov: sideState{"OPEN", "1000", "0"}, wht: "0", reversed: false, logs: []string{"compare"},
			want: []string{"invoices both ", "payments both ", "reconciliation_log go "}, journalFails: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newRowsFixture(t, tables...)
			f.snapshot(t)
			amount := tc.amount
			if amount == "" {
				amount = "1000"
			}
			for _, s := range []struct {
				pool             *pgxpool.Pool
				inv, pay, status string
				st               sideState
			}{{f.py, pyInv, pySource, "CLEARED", tc.py}, {f.gov, goInv, goSource, "FLAGGED", tc.gov}} {
				insertGoInvoice(t, s.pool, s.inv, s.st.status, "IDR", amount, "2026-01-01", false, "")
				mustExec(t, s.pool, `UPDATE invoices SET balance_due = $2::numeric, wht_booked = $3::numeric WHERE invoice_id = $1`, s.inv, s.st.balance, s.st.whtBooked)
				payment(t, s.pool, s.pay, s.inv, "MANUAL", "IDR", dec("1000").Sub(dec(tc.wht)).String(), tc.wht, s.status)
			}
			for _, reason := range tc.logs {
				mustExec(t, f.gov, `INSERT INTO reconciliation_log (log_id, payment_id, action, reasons) VALUES (gen_random_uuid(), $1, 'REVERSED', ARRAY[$2])`, goSource, reason)
			}
			issue := []jLine{idr(acctAR, amount, "0"), idr(acctRevenue, "0", amount)}
			journalWant := times(3, "journal go PN 162")
			if tc.gov.status == "VOID" {
				postReversed(t, f.gov, goIssue, "b2000000-0000-4000-8000-00000000000a", "INVOICE", goInv, issue...)
				journalWant = times(6, "journal go PN 162")
			} else {
				insertEntry(t, f.gov, goIssue, "INVOICE", goInv, "Invoice issued", issue...)
			}
			paymentNote := "journal go PN 161"
			if tc.journalFails {
				paymentNote = "journal go "
			}
			cleared := []jLine{idr(acctBank, "1000", "0"), idr(acctAR, "0", "1000")}
			if tc.wht != "0" {
				cleared = []jLine{idr(acctBank, "900", "0"), idr(acctWHT, "100", "0"), idr(acctAR, "0", "1000")}
			}
			if tc.reversed {
				postReversed(t, f.gov, goEntry, goEntry2, "PAYMENT", goSource, cleared...)
				journalWant = append(journalWant, times(2*(len(cleared)+1), paymentNote)...)
			} else {
				insertEntry(t, f.gov, goEntry, "PAYMENT", goSource, "Payment cleared", cleared...)
				journalWant = append(journalWant, times(len(cleared)+1, paymentNote)...)
			}
			got := classified(f.compare(t, mapsOf(t, "invoices", pyInv, goInv, "payments", pySource, goSource)))
			for i, s := range got {
				got[i] = strings.Replace(strings.Replace(s, "journal_entries", "journal", 1), "journal_lines", "journal", 1)
			}
			want := append(append([]string(nil), tc.want...), journalWant...)
			sort.Strings(got)
			sort.Strings(want)
			if strings.Join(got, "|") != strings.Join(want, "|") {
				t.Fatalf("classified\n  %q\nwant\n  %q", got, want)
			}
		})
	}
}
