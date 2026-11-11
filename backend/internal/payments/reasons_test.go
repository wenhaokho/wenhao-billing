package payments

import (
	"context"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/matching"
	"github.com/wenhaokho/wenhao-billing/backend/internal/payments/paymentsdb"
	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

// engineReasons runs the real matching engine through each of its outcomes
// and returns every reason it wrote, so the table below follows the engine's
// sentences rather than a copy of them.
func engineReasons(t *testing.T) []string {
	t.Helper()
	issue, due := day(2026, 4, 10), day(2026, 4, 24)
	cust := uuid.New()
	cand := func(cur string) matching.Candidate {
		return matching.Candidate{
			InvoiceID: uuid.New(), CustomerID: cust, Currency: cur, Status: "SENT",
			BalanceDue: dec("1000.0000"), WhtOutstanding: decimal.Zero,
			IssueDate: &issue, DueDate: &due, CustomerName: "Acme Corp",
		}
	}
	pay := func(amt, cur, payer string) matching.Incoming {
		return matching.Incoming{Amount: dec(amt), Currency: cur, PayerName: payer, PaymentDate: due}
	}
	var out []string
	for _, c := range []struct {
		in    matching.Incoming
		cands []matching.Candidate
		want  string
	}{
		{pay("1000", "USD", "Acme Corp"), nil, ReasonNoCandidate},
		{pay("999", "SGD", "Zzz Unknown"), []matching.Candidate{cand("USD")}, ReasonCurrencyMismatch},
		{pay("999", "USD", "Acme Corp"), []matching.Candidate{cand("USD")}, ReasonAmountMismatch},
		{pay("1000", "USD", "Zzz Unknown"), []matching.Candidate{cand("USD")}, ReasonLowConfidence},
		{pay("1000", "USD", "Acme Corp"), []matching.Candidate{cand("USD"), cand("USD")}, ReasonAmbiguous},
		{pay("1000", "USD", "Acme Corp"), []matching.Candidate{cand("USD")}, ReasonExactMatch},
	} {
		r := matching.Match(c.in, c.cands)
		if !slices.Contains(ReasonCodes(r.Reasons), c.want) {
			t.Fatalf("engine reasons %q carry no %s", r.Reasons, c.want)
		}
		out = append(out, r.Reasons...)
	}
	return out
}

// knownReasons is every sentence the code writes, with its code: the engine's
// (from engineReasons, coded there), the held sentence of clearIncoming, the
// manual entry's, the manual approval's, plus texts no rule knows.
func knownReasons(t *testing.T) map[string]string {
	t.Helper()
	m := map[string]string{
		"no open invoice matches payer/currency":                      ReasonNoCandidate,
		"currency mismatch: payment SGD vs invoice USD":               ReasonCurrencyMismatch,
		"amount mismatch: delta=-1.0000":                              ReasonAmountMismatch,
		"confidence 72.5 below threshold 95.0":                        ReasonLowConfidence,
		"ambiguous match: 2 open invoices match exactly":              ReasonAmbiguous,
		"exact amount+currency, high payer+date confidence":           ReasonExactMatch,
		fmt.Sprintf("invoice %s changed before clearing", uuid.New()): ReasonClearFailed,
		"manual-entry": ReasonManualEntry,
		fmt.Sprintf("manual approval against invoice %s", uuid.New()): ReasonManualApproval,
		"alias match: Acme matched Acme Corp":                         ReasonAliasMatch,
		"something new":                                               ReasonOther,
		"bank returned funds":                                         ReasonOther,
		"invoice 42 was paid by cheque":                               ReasonOther,
		"confidence is fine":                                          ReasonOther,
		"Currency mismatch: upper case is not the code's":             ReasonOther,
		"":                                    ReasonOther,
		"invoice " + clearFailedSuffixForTest: ReasonClearFailed,
		"ledger refused: account 1100 is inactive (a held clearing sentence)": ReasonOther,
	}
	for _, r := range engineReasons(t) {
		if _, ok := m[r]; !ok {
			m[r] = ReasonCode(r) // checked against the expected code in engineReasons
		}
	}
	return m
}

// clearFailedSuffixForTest is the tail of clearIncoming's held sentence with
// an empty id: still CLEAR_FAILED in Go and in SQL alike.
const clearFailedSuffixForTest = " changed before clearing"

func TestReasonCode(t *testing.T) {
	t.Parallel()
	for text, want := range knownReasons(t) {
		t.Run(text, func(t *testing.T) {
			t.Parallel()
			if got := ReasonCode(text); got != want {
				t.Errorf("ReasonCode(%q) = %s, want %s", text, got, want)
			}
		})
	}
	if got := ReasonCodes(nil); got == nil || len(got) != 0 {
		t.Errorf("ReasonCodes(nil) = %#v, want empty non-nil", got)
	}
}

// backfillSQL is the UPDATE statement of migration 00011's Up section.
func backfillSQL(t *testing.T) string {
	t.Helper()
	raw, err := fs.ReadFile(migrations.FS, "00011_reconciliation_reason_codes.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, _, ok := strings.Cut(string(raw), "-- +goose Down")
	if !ok {
		t.Fatal("migration has no Down section")
	}
	i := strings.Index(up, "UPDATE reconciliation_log")
	if i < 0 {
		t.Fatal("migration Up has no UPDATE reconciliation_log")
	}
	stmt, _, ok := strings.Cut(up[i:], ";")
	if !ok {
		t.Fatal("UPDATE is not terminated")
	}
	return stmt
}

// seedLogPayment inserts a held payment for log rows to hang off.
func (e *env) seedLogPayment(t *testing.T) uuid.UUID {
	t.Helper()
	id := uuid.New()
	e.exec(t, `INSERT INTO payments (payment_id, amount, currency, payer_name, payment_date, intake_source, external_ref, status, adjustment_type)
VALUES ($1, 100, 'USD', 'Payer', '2026-04-15', 'WEBHOOK', $2, 'PENDING_MANUAL_REVIEW', 'NONE')`, id, id.String())
	return id
}

func (e *env) reasonCodes(t *testing.T, logID uuid.UUID) []string {
	t.Helper()
	var codes []string
	if err := e.pool.QueryRow(context.Background(), `SELECT reason_codes FROM reconciliation_log WHERE log_id = $1`, logID).Scan(&codes); err != nil {
		t.Fatal(err)
	}
	return codes
}

func TestReasonCodesBackfill(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	pay := e.seedLogPayment(t)
	known := knownReasons(t)
	texts := make([]string, 0, len(known))
	for text := range known {
		texts = append(texts, text)
	}
	slices.Sort(texts)

	single := map[uuid.UUID]string{}
	for _, text := range texts {
		id := uuid.New()
		e.exec(t, `INSERT INTO reconciliation_log (log_id, payment_id, action, reasons, reason_codes) VALUES ($1, $2, 'HELD', $3, '{}')`,
			id, pay, []string{text})
		single[id] = text
	}
	all := uuid.New() // every text in one row: order is kept
	e.exec(t, `INSERT INTO reconciliation_log (log_id, payment_id, action, reasons, reason_codes) VALUES ($1, $2, 'HELD', $3, '{}')`,
		all, pay, texts)
	empty := uuid.New()
	e.exec(t, `INSERT INTO reconciliation_log (log_id, payment_id, action, reasons, reason_codes) VALUES ($1, $2, 'HELD', '{}', '{}')`,
		empty, pay)
	multi := map[uuid.UUID]int{}
	for i, r := range multiReasonRows {
		id := uuid.New()
		e.exec(t, `INSERT INTO reconciliation_log (log_id, payment_id, action, reasons, reason_codes) VALUES ($1, $2, 'HELD', $3, '{}')`,
			id, pay, r.reasons)
		multi[id] = i
	}

	e.exec(t, backfillSQL(t))

	for id, i := range multi {
		r := multiReasonRows[i]
		if got := e.reasonCodes(t, id); !slices.Equal(got, r.want) || !slices.Equal(got, ReasonCodes(r.reasons)) {
			t.Errorf("backfill of %q = %v, want %v", r.reasons, got, r.want)
		}
	}

	for id, text := range single {
		if got := e.reasonCodes(t, id); !slices.Equal(got, []string{ReasonCode(text)}) {
			t.Errorf("backfill of %q = %v, want [%s]", text, got, ReasonCode(text))
		}
	}
	if got, want := e.reasonCodes(t, all), ReasonCodes(texts); !slices.Equal(got, want) {
		t.Errorf("backfill of all = %v, want %v", got, want)
	}
	if got := e.reasonCodes(t, empty); got == nil || len(got) != 0 {
		t.Errorf("backfill of no reasons = %#v, want {}", got)
	}
}

func TestLogActionWritesCodes(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	codesOf := func(paymentID uuid.UUID) [][]string {
		t.Helper()
		rows, err := e.pool.Query(context.Background(), `SELECT reasons, reason_codes FROM reconciliation_log
WHERE payment_id = $1 ORDER BY created_at, log_id`, paymentID)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out [][]string
		for rows.Next() {
			var reasons, codes []string
			if err := rows.Scan(&reasons, &codes); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(codes, ReasonCodes(reasons)) {
				t.Errorf("codes %v for reasons %q", codes, reasons)
			}
			out = append(out, codes)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return out
	}

	// Held by the engine, approved by hand, then reversed.
	invID := e.sentInvoice(t)
	p := e.incoming(t, payload(amount("999.00")))
	e.mustApprove(t, p.PaymentID, invID)
	e.mustReverse(t, p.PaymentID, "bank returned funds")
	got := codesOf(p.PaymentID)
	want := [][]string{{ReasonAmountMismatch, ReasonLowConfidence}, {ReasonManualApproval}, {ReasonOther}}
	if len(got) != len(want) {
		t.Fatalf("codes = %v, want %v", got, want)
	}
	for i := range want {
		if !slices.Equal(got[i], want[i]) {
			t.Errorf("row %d codes = %v, want %v", i, got[i], want[i])
		}
	}

	// Several reasons keep their order; none is stored as {}.
	pay := e.seedLogPayment(t)
	q := paymentsdb.New(e.pool)
	reasons := []string{"confidence 50 below threshold 95.0", "manual-entry", "notes"}
	if err := logAction(context.Background(), q, pay, actionHeld, reasons, nil); err != nil {
		t.Fatal(err)
	}
	if err := logAction(context.Background(), q, pay, actionHeld, nil, nil); err != nil {
		t.Fatal(err)
	}
	got = codesOf(pay)
	if len(got) != 2 || !slices.Equal(got[0], []string{ReasonLowConfidence, ReasonManualEntry, ReasonOther}) || got[1] == nil || len(got[1]) != 0 {
		t.Errorf("codes = %#v", got)
	}

	// The engine clears, the posting finds no rate: held with the FX
	// refusal after the exact-match reason, which is CLEAR_FAILED.
	fxe := newEnv(t)
	fxe.rateless(t, "1000.00", "0")
	held := fxe.incoming(t, payload(amount("1000.00"), paid(day(2025, 12, 1))))
	if held.Status != "PENDING_MANUAL_REVIEW" {
		t.Fatalf("status %s, want held", held.Status)
	}
	var fxReasons, fxCodes []string
	if err := fxe.pool.QueryRow(context.Background(), `SELECT reasons, reason_codes FROM reconciliation_log WHERE payment_id = $1`,
		held.PaymentID).Scan(&fxReasons, &fxCodes); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(fxCodes, []string{ReasonExactMatch, ReasonClearFailed}) || len(fxReasons) != 2 ||
		!strings.HasPrefix(fxReasons[1], "no fx rate for USD->IDR") {
		t.Errorf("reasons %q codes %v, want [EXACT_MATCH CLEAR_FAILED]", fxReasons, fxCodes)
	}
}

// multiReasonRows are rows of several reasons whose codes depend on their
// position: anything after an exact-match reason is CLEAR_FAILED.
var multiReasonRows = []struct {
	reasons, want []string
}{
	{
		[]string{"exact amount+currency, high payer+date confidence", "no fx rate for USD->IDR on or before 2025-12-01"},
		[]string{ReasonExactMatch, ReasonClearFailed},
	},
	{
		[]string{"exact amount+currency, high payer+date confidence", "payment 5 already has an active entry"},
		[]string{ReasonExactMatch, ReasonClearFailed},
	},
	{
		[]string{"exact amount+currency, high payer+date confidence", "invoice 7 changed before clearing"},
		[]string{ReasonExactMatch, ReasonClearFailed},
	},
	{
		[]string{"no fx rate for USD->IDR on or before 2025-12-01", "exact amount+currency, high payer+date confidence"},
		[]string{ReasonOther, ReasonExactMatch},
	},
	{
		[]string{"amount mismatch: delta=-1", "confidence 50 below threshold 95.0"},
		[]string{ReasonAmountMismatch, ReasonLowConfidence},
	},
}

func TestReasonCodesPositional(t *testing.T) {
	t.Parallel()
	for _, r := range multiReasonRows {
		if got := ReasonCodes(r.reasons); !slices.Equal(got, r.want) {
			t.Errorf("ReasonCodes(%q) = %v, want %v", r.reasons, got, r.want)
		}
	}
}
