package compare

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// The row classification (plan 12 Task 8). A rule names the PORT-NOTES note
// a row difference is known for. It never hides the difference: a
// classified difference is still counted and listed, and the exit code is
// unchanged. A rule is narrow: a table, a predicate on the rows' content, and
// for a journal entry Go posts and Python never does, self-consistency
// checks on the entry, its siblings, every one of its lines, and its
// document or payment, mirroring how internal/ledger posts it. A difference
// that fails a check stays unclassified and names the check (RowDiff.Check).

// accrualNotes is the note for a journal entry Go posts and Python never
// does, by source_type: an invoice's issue and void (PN 162), a bill's open,
// edit and void (PN 164), a bill payment and its reversal (PN 165), and a
// manual invoice payment and its reversal (PN 161). A reversal copies its
// original's source_type.
var accrualNotes = map[string]string{
	"INVOICE":      "PN 162",
	"BILL":         "PN 164",
	"BILL_PAYMENT": "PN 165",
	"PAYMENT":      "PN 161",
}

// Account codes the rules name (internal/ledger's codes).
const (
	codeBank       = "1000"
	codeReceivable = "1100"
	codePrepaidTax = "1300"
	codePayable    = "2000"
	codeFXGain     = "4900"
	codeFXLoss     = "5900"
)

// patchVoidNote is the exact notes line Go appends when a PATCH sets a bill
// VOID (bills.updateBill → voidBill with this reason).
const patchVoidNote = "\n[voided] status set to VOID via update"

// DefaultBaseCurrency is the ledger's base currency when none is given.
const DefaultBaseCurrency = "IDR"

// account is one chart_of_accounts row's code and type.
type account struct{ code, typ string }

// classification is what the rules read beyond the two rows of a difference.
type classification struct {
	base       string
	pyCodes    map[string]string  // Python account_id → code
	goAccounts map[string]account // Go account_id → code and type
	entries    map[string]Row     // every Go journal entry, by id
	lines      map[string][]Row   // every Go journal line, by entry id
	reversalOf map[string]string  // reversal entry id → the entry it reverses
	paired     map[string]bool    // Go entry ids a Python entry pairs with
	verdicts   map[string]verdict // Go-only entries
	// payGo is, for a paired PAYMENT entry, why its Go lines are not how
	// Go posts a payment ("" when they are); PN 163 needs it empty.
	payGo map[string]string
	// reversedManual is each Go manual payment whose clearing and reversal
	// are both PN 161 entries, by Go payment id; restored sums them per Go
	// invoice id. They are the rows a manual payment's reversal changes on
	// Go only (Python refuses it, PN 161). reversedLogs is Go's REVERSED
	// reconciliation_log rows by payment id.
	reversedManual map[string]reversedPayment
	restored       map[string]reversedPayment
	reversedLogs   map[string][]Row
}

// reversedPayment is a reversed manual payment's amounts and reason (or, in
// restored, an invoice's sum of the amounts).
type reversedPayment struct {
	reason      string
	amount, wht decimal.Decimal
}

// verdict is one Go-only entry's classification: a note, or the check it
// failed.
type verdict struct{ note, check string }

// prepareClassification reads what the rules need, after the pairs are
// derived, and decides every Go-only journal entry's verdict and every
// paired PAYMENT entry's Go-side check.
func (c *rowComparer) prepareClassification(ctx context.Context, in RowsInput, states map[string]*tableState) error {
	cls := &classification{
		base:           in.BaseCurrency,
		entries:        map[string]Row{},
		lines:          map[string][]Row{},
		reversalOf:     map[string]string{},
		paired:         map[string]bool{},
		verdicts:       map[string]verdict{},
		payGo:          map[string]string{},
		reversedManual: map[string]reversedPayment{},
		restored:       map[string]reversedPayment{},
		reversedLogs:   map[string][]Row{},
	}
	if cls.base == "" {
		cls.base = DefaultBaseCurrency
	}
	var err error
	pyAccounts, err := accounts(ctx, in.Python)
	if err != nil {
		return fmt.Errorf("python accounts: %w", err)
	}
	cls.pyCodes = map[string]string{}
	for id, a := range pyAccounts {
		cls.pyCodes[id] = a.code
	}
	if cls.goAccounts, err = accounts(ctx, in.Go); err != nil {
		return fmt.Errorf("go accounts: %w", err)
	}
	c.cls = cls
	if rl := states["reconciliation_log"]; rl != nil {
		for _, row := range rl.goAll {
			if valueString(row["action"]) == "REVERSED" {
				pid := valueString(row["payment_id"])
				cls.reversedLogs[pid] = append(cls.reversedLogs[pid], row)
			}
		}
	}
	je := states["journal_entries"]
	if je == nil {
		return nil
	}
	for _, row := range je.goAll {
		id := valueString(row["entry_id"])
		cls.entries[id] = row
		if r := valueString(row["reversed_by_entry_id"]); r != "" {
			cls.reversalOf[r] = id
		}
	}
	if jl := states["journal_lines"]; jl != nil {
		for _, row := range jl.goAll {
			id := valueString(row["entry_id"])
			cls.lines[id] = append(cls.lines[id], row)
		}
	}
	for _, goID := range c.maps["journal_entries"] {
		cls.paired[goID] = true
	}
	touched := append(append([]Row(nil), je.goNew...), mapValues(je.goPre)...)
	for _, row := range touched {
		id := valueString(row["entry_id"])
		src := valueString(row["source_type"])
		switch {
		case cls.paired[id] && src == "PAYMENT":
			why, err := c.pairedPaymentCheck(ctx, in, id)
			if err != nil {
				return err
			}
			cls.payGo[id] = why
		case !cls.paired[id] && accrualNotes[src] != "":
			v, err := c.entryVerdict(ctx, in, id, true)
			if err != nil {
				return err
			}
			cls.verdicts[id] = v
		}
	}
	return cls.findReversedManual(ctx, in.Go)
}

// mapValues is a map's values in key order.
func mapValues(m map[string]Row) []Row {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]Row, 0, len(m))
	for _, k := range keys {
		out = append(out, m[k])
	}
	return out
}

// pairedPaymentCheck is the Go side of PN 163 for a paired PAYMENT entry: a
// reversal mirrors its original, which must pass; any other entry's lines
// are how Go posts a payment (goPaymentLines).
func (c *rowComparer) pairedPaymentCheck(ctx context.Context, in RowsInput, id string) (string, error) {
	cls := c.cls
	if orig, ok := cls.reversalOf[id]; ok {
		if !mirrors(cls.lines[id], cls.lines[orig]) {
			return fmt.Sprintf("its lines do not mirror entry %s's", orig), nil
		}
		why, err := cls.goPaymentLines(ctx, in.Go, orig, false)
		if why != "" {
			why = "it reverses entry " + orig + ": " + why
		}
		return why, err
	}
	return cls.goPaymentLines(ctx, in.Go, id, false)
}

// findReversedManual fills reversedManual and restored: a Go-only PAYMENT
// entry that is PN 161, is reversed, and whose reversal is PN 161 too, with
// the reason its reversal memo ("Reversal: <reason>") carries.
func (cls *classification) findReversedManual(ctx context.Context, pool *pgxpool.Pool) error {
	for id, v := range cls.verdicts {
		e := cls.entries[id]
		r := valueString(e["reversed_by_entry_id"])
		if v.note != "PN 161" || r == "" || cls.verdicts[r].note != "PN 161" {
			continue
		}
		if _, isReversal := cls.reversalOf[id]; isReversal {
			continue
		}
		reason, ok := strings.CutPrefix(valueString(cls.entries[r]["memo"]), "Reversal: ")
		if !ok {
			continue
		}
		pid := valueString(e["source_id"])
		var invoiceID, amount, wht string
		err := pool.QueryRow(ctx, `SELECT COALESCE(invoice_id::text, ''), amount::text, wht_amount::text FROM payments WHERE payment_id = $1`, pid).
			Scan(&invoiceID, &amount, &wht)
		if err != nil {
			return fmt.Errorf("reversed manual payment %s: %w", pid, err)
		}
		p := reversedPayment{reason: reason, amount: dec(amount), wht: dec(wht)}
		cls.reversedManual[pid] = p
		if invoiceID != "" {
			sum := cls.restored[invoiceID]
			sum.amount, sum.wht = sum.amount.Add(p.amount), sum.wht.Add(p.wht)
			cls.restored[invoiceID] = sum
		}
	}
	return nil
}

// entryVerdict checks one Go entry. A reversal is checked through the entry
// it reverses (which must pass) and must mirror its lines. Any other entry
// must have at least two lines that balance, be its source's only active
// entry (a payment's only entry that is not a reversal), have every line on
// an account and side its type allows (lineRules), and match its document,
// bill payment row or payment.
func (c *rowComparer) entryVerdict(ctx context.Context, in RowsInput, id string, followReversal bool) (verdict, error) {
	cls := c.cls
	e := cls.entries[id]
	src := valueString(e["source_type"])
	note := accrualNotes[src]
	fail := func(format string, args ...any) (verdict, error) {
		return verdict{check: note + " check failed: entry " + id + ": " + fmt.Sprintf(format, args...)}, nil
	}
	if orig, ok := cls.reversalOf[id]; ok {
		if !followReversal {
			return fail("it reverses a reversal")
		}
		ov, err := c.entryVerdict(ctx, in, orig, false)
		if err != nil {
			return verdict{}, err
		}
		if ov.note == "" {
			return fail("it reverses entry %s, which fails (%s)", orig, ov.check)
		}
		if !mirrors(cls.lines[id], cls.lines[orig]) {
			return fail("its lines do not mirror entry %s's", orig)
		}
		return verdict{note: note}, nil
	}

	lines := cls.lines[id]
	if len(lines) < 2 {
		return fail("it has %d lines", len(lines))
	}
	if why := cls.unbalanced(lines); why != "" {
		return fail("%s", why)
	}
	sourceID := valueString(e["source_id"])
	reversed := valueString(e["reversed_by_entry_id"]) != ""
	var own, active int
	replaced := false
	for sid, sib := range cls.entries {
		if valueString(sib["source_type"]) != src || valueString(sib["source_id"]) != sourceID {
			continue
		}
		if _, isReversal := cls.reversalOf[sid]; isReversal {
			continue
		}
		own++
		if r := valueString(sib["reversed_by_entry_id"]); r == "" {
			active++
			if sid != id {
				replaced = true
			}
		} else if _, ok := cls.entries[r]; !ok {
			return fail("entry %s is reversed by %s, which does not exist", sid, r)
		}
	}
	if src == "PAYMENT" && own != 1 {
		return fail("payment %s has %d entries that are not reversals", sourceID, own)
	}
	if active > 1 {
		return fail("%s %s has %d active entries", strings.ToLower(src), sourceID, active)
	}
	control, why := cls.lineRules(src, lines)
	if why != "" {
		return fail("%s", why)
	}
	switch src {
	case "INVOICE", "BILL":
		if reversed && replaced {
			// An edit re-post (or a return to draft and a re-open): the
			// document now matches the active replacement, which is
			// checked against it on its own.
			return verdict{note: note}, nil
		}
		why, err := cls.checkDocument(ctx, in.Go, src, sourceID, control, !reversed)
		if err != nil {
			return verdict{}, err
		}
		if why != "" {
			return fail("%s", why)
		}
	case "BILL_PAYMENT":
		why, err := cls.checkBillPayment(ctx, in.Go, id, sourceID, lines, reversed)
		if err != nil {
			return verdict{}, err
		}
		if why != "" {
			return fail("%s", why)
		}
	case "PAYMENT":
		why, err := c.checkManualPayment(ctx, in, sourceID)
		if err != nil {
			return verdict{}, err
		}
		if why == "" {
			why, err = cls.checkPaymentReversal(ctx, in.Go, sourceID, reversed)
			if err != nil {
				return verdict{}, err
			}
		}
		if why == "" {
			why, err = cls.goPaymentLines(ctx, in.Go, id, true)
			if err != nil {
				return verdict{}, err
			}
		}
		if why != "" {
			return fail("%s", why)
		}
	}
	return verdict{note: note}, nil
}

// side helpers: a line's debit, credit and bases.
func debit(l Row) decimal.Decimal      { return dec(l["debit"]) }
func credit(l Row) decimal.Decimal     { return dec(l["credit"]) }
func baseDebit(l Row) decimal.Decimal  { return dec(l["base_amount_debit"]) }
func baseCredit(l Row) decimal.Decimal { return dec(l["base_amount_credit"]) }

// goAccount is a Go line's account.
func (cls *classification) goAccount(l Row) account {
	return cls.goAccounts[valueString(l["account_id"])]
}

// isDebit reports whether a line is a debit (debit > 0, credit 0).
func isDebit(l Row) bool { return debit(l).IsPositive() && credit(l).IsZero() }

// isCredit reports whether a line is a credit (credit > 0, debit 0).
func isCredit(l Row) bool { return credit(l).IsPositive() && debit(l).IsZero() }

// lineRules is why an entry's lines are not the accounts and sides its
// source_type posts ("" when they are), and its control line:
//   - INVOICE: one 1100 debit; every other line an INCOME credit;
//   - BILL: one 2000 credit; every other line an EXPENSE, COGS or ASSET
//     debit;
//   - BILL_PAYMENT: one 2000 debit, one 1000 credit, at most one FX line;
//   - PAYMENT: one 1100 credit (the control line), at most one 1000 debit,
//     at most one 1300 debit, at most one FX line.
//
// An FX line is a 4900 credit (gain) or a 5900 debit (loss), in the base
// currency with its amount equal to its base.
func (cls *classification) lineRules(src string, lines []Row) (Row, string) {
	count := map[string]int{}
	var control Row
	for _, l := range lines {
		a := cls.goAccount(l)
		count[a.code]++
		bad := func() (Row, string) {
			side := "credit"
			if !credit(l).IsPositive() {
				side = "debit"
			}
			return nil, fmt.Sprintf("a %s line on %s (%s) is not allowed on a %s entry", side, a.code, a.typ, src)
		}
		switch {
		case src == "INVOICE" && a.code == codeReceivable && isDebit(l),
			src == "BILL" && a.code == codePayable && isCredit(l),
			src == "BILL_PAYMENT" && a.code == codePayable && isDebit(l),
			src == "PAYMENT" && a.code == codeReceivable && isCredit(l):
			control = l
		case src == "INVOICE" && a.code != codeReceivable && a.typ == "INCOME" && isCredit(l),
			src == "BILL" && a.code != codePayable && (a.typ == "EXPENSE" || a.typ == "COGS" || a.typ == "ASSET") && isDebit(l),
			src == "BILL_PAYMENT" && a.code == codeBank && isCredit(l),
			src == "PAYMENT" && (a.code == codeBank || a.code == codePrepaidTax) && isDebit(l):
		case (src == "BILL_PAYMENT" || src == "PAYMENT") && (a.code == codeFXGain || a.code == codeFXLoss):
			if why := cls.fxLine(l); why != "" {
				return nil, why
			}
		default:
			return bad()
		}
	}
	ctlCode := map[string]string{"INVOICE": codeReceivable, "BILL": codePayable, "BILL_PAYMENT": codePayable, "PAYMENT": codeReceivable}[src]
	if count[ctlCode] != 1 || control == nil {
		return nil, fmt.Sprintf("it needs exactly one control line on %s, has %d", ctlCode, count[ctlCode])
	}
	if count[codeFXGain]+count[codeFXLoss] > 1 {
		return nil, "it has more than one FX line"
	}
	switch src {
	case "BILL_PAYMENT":
		if count[codeBank] != 1 {
			return nil, fmt.Sprintf("it needs exactly one bank credit, has %d", count[codeBank])
		}
	case "PAYMENT":
		if count[codeBank] > 1 || count[codePrepaidTax] > 1 {
			return nil, "it has more than one bank or withholding line"
		}
	}
	return control, ""
}

// fxLine is why an FX line is not a 4900 credit or a 5900 debit in the base
// currency with its amount equal to its base ("" when it is).
func (cls *classification) fxLine(l Row) string {
	code := cls.goAccount(l).code
	if (code == codeFXGain && !isCredit(l)) || (code == codeFXLoss && !isDebit(l)) {
		return fmt.Sprintf("an FX line on %s is on the wrong side", code)
	}
	if valueString(l["currency"]) != cls.base || !debit(l).Equal(baseDebit(l)) || !credit(l).Equal(baseCredit(l)) {
		return fmt.Sprintf("an FX line on %s is not in %s at its base", code, cls.base)
	}
	return ""
}

// unbalanced is why lines do not balance: in base over every line, and in
// amount per currency over the lines other than realised FX ("" when they
// balance).
func (cls *classification) unbalanced(lines []Row) string {
	var dr, cr decimal.Decimal
	amounts := map[string]decimal.Decimal{}
	for _, l := range lines {
		dr = dr.Add(baseDebit(l))
		cr = cr.Add(baseCredit(l))
		if code := cls.goAccount(l).code; code == codeFXGain || code == codeFXLoss {
			continue
		}
		ccy := valueString(l["currency"])
		amounts[ccy] = amounts[ccy].Add(debit(l)).Sub(credit(l))
	}
	if !dr.Equal(cr) {
		return fmt.Sprintf("base debits %s ≠ base credits %s", dr, cr)
	}
	for ccy, net := range amounts {
		if !net.IsZero() {
			return fmt.Sprintf("%s amounts are off by %s", ccy, net)
		}
	}
	return ""
}

// rate is Go's rate for currency into the base currency on day (the newest
// on or before it, as fx.GetRate reads it); 1 for the base currency.
func (cls *classification) rate(ctx context.Context, pool *pgxpool.Pool, currency, day string) (decimal.Decimal, bool, error) {
	if currency == cls.base {
		return decimal.NewFromInt(1), true, nil
	}
	var text string
	err := pool.QueryRow(ctx, `SELECT rate::text FROM fx_rates WHERE from_currency = $1 AND to_currency = $2 AND as_of_date <= $3::date
		ORDER BY as_of_date DESC LIMIT 1`, currency, cls.base, day).Scan(&text)
	if errors.Is(err, pgx.ErrNoRows) {
		return decimal.Decimal{}, false, nil
	}
	if err != nil {
		return decimal.Decimal{}, false, err
	}
	return dec(text), true, nil
}

// converted is why a line's amount on side is not amount × the rate for
// currency on day, its base rounded half-even to 4 places ("" when it is).
func (cls *classification) converted(ctx context.Context, pool *pgxpool.Pool, l Row, debitSide bool, amount decimal.Decimal, currency, day, what string) (string, error) {
	got, base := credit(l), baseCredit(l)
	if debitSide {
		got, base = debit(l), baseDebit(l)
	}
	if !got.Equal(amount) || valueString(l["currency"]) != currency {
		return fmt.Sprintf("its %s line is %s %s, want %s %s", what, got, valueString(l["currency"]), amount, currency), nil
	}
	rate, ok, err := cls.rate(ctx, pool, currency, day)
	if err != nil {
		return "", err
	}
	if !ok {
		return fmt.Sprintf("no %s rate on or before %s", currency, day), nil
	}
	if !dec(l["fx_rate"]).Equal(rate) || !base.Equal(platform.RoundHalfEven4(amount.Mul(rate))) {
		return fmt.Sprintf("its %s line's rate %s and base %s are not the %s rate %s on %s", what, dec(l["fx_rate"]), base, currency, rate, day), nil
	}
	return "", nil
}

// checkDocument is why an issue entry's control line does not match its
// document ("" when it does): the line is the document's amount and
// currency at the rate on its issue date (else its created_at date), the
// base rounded half-even to 4 places. An active entry's document must also
// not be a DRAFT, a VOID or a template. A void changes none of amount,
// currency and issue date, so a voided document is still checked.
func (cls *classification) checkDocument(ctx context.Context, pool *pgxpool.Pool, src, id string, ctl Row, active bool) (string, error) {
	q := `SELECT amount::text, currency, COALESCE(issue_date, created_at::date)::text, status, is_template FROM invoices WHERE invoice_id = $1`
	if src == "BILL" {
		q = `SELECT amount::text, currency, COALESCE(issue_date, created_at::date)::text, status, false FROM bills WHERE bill_id = $1`
	}
	var amount, currency, day, status string
	var template bool
	err := pool.QueryRow(ctx, q, id).Scan(&amount, &currency, &day, &status, &template)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Sprintf("its %s %s does not exist", strings.ToLower(src), id), nil
	}
	if err != nil {
		return "", err
	}
	if active && (status == "DRAFT" || status == "VOID" || template) {
		return fmt.Sprintf("its active entry's document is %s (template %v)", status, template), nil
	}
	return cls.converted(ctx, pool, ctl, src == "INVOICE", dec(amount), currency, day, "control")
}

// checkBillPayment is why a BILL_PAYMENT entry does not match its Go
// bill_payments row ("" when it does): the row exists, is REVERSED exactly
// when the entry is reversed, the 2000 debit is its amount and currency at
// the bill's issue basis or remaining basis with fx_rate = base / relief
// (reliefAtBasis, PN 165), and the bank credit is its amount at the rate on
// its payment date.
func (cls *classification) checkBillPayment(ctx context.Context, pool *pgxpool.Pool, entryID, id string, lines []Row, reversed bool) (string, error) {
	var billID, amount, currency, day, status string
	err := pool.QueryRow(ctx, `SELECT bill_id::text, amount::text, currency, payment_date::text, status FROM bill_payments WHERE bill_payment_id = $1`, id).
		Scan(&billID, &amount, &currency, &day, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Sprintf("bill payment %s does not exist", id), nil
	}
	if err != nil {
		return "", err
	}
	if want := map[bool]string{false: "CLEARED", true: "REVERSED"}[reversed]; status != want {
		return fmt.Sprintf("bill payment %s is %s, want %s", id, status, want), nil
	}
	for _, l := range lines {
		switch cls.goAccount(l).code {
		case codePayable:
			if !debit(l).Equal(dec(amount)) || valueString(l["currency"]) != currency {
				return fmt.Sprintf("its payable line is %s %s, the bill payment %s %s", debit(l), valueString(l["currency"]), dec(amount), currency), nil
			}
			if why, err := cls.reliefAtBasis(ctx, pool, payableBasis, entryID, billID, l); why != "" || err != nil {
				return why, err
			}
		case codeBank:
			if why, err := cls.converted(ctx, pool, l, false, dec(amount), currency, day, "bank"); why != "" || err != nil {
				return why, err
			}
		}
	}
	return "", nil
}

// checkPaymentReversal is why a PAYMENT entry's Go payment does not show
// the entry's reversal state ("" when it does), as payments.Service.Reverse
// leaves it (PN 161): FLAGGED with exactly one REVERSED reconciliation_log
// row when the entry is reversed, CLEARED with none otherwise. A half-done
// reversal (the entry reversed, the payment or log not) is not classified.
func (cls *classification) checkPaymentReversal(ctx context.Context, pool *pgxpool.Pool, id string, reversed bool) (string, error) {
	var status string
	var logs int
	err := pool.QueryRow(ctx, `SELECT p.status,
		(SELECT count(*) FROM reconciliation_log l WHERE l.payment_id = p.payment_id AND l.action = 'REVERSED')
		FROM payments p WHERE p.payment_id = $1`, id).Scan(&status, &logs)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Sprintf("payment %s does not exist", id), nil
	}
	if err != nil {
		return "", err
	}
	wantStatus, wantLogs := "CLEARED", 0
	if reversed {
		wantStatus, wantLogs = "FLAGGED", 1
	}
	if status != wantStatus || logs != wantLogs {
		return fmt.Sprintf("payment %s is %s with %d REVERSED log rows, want %s with %d", id, status, logs, wantStatus, wantLogs), nil
	}
	return "", nil
}

// checkManualPayment is why a Go-only PAYMENT entry's payment is not a
// manual one (PN 161) ("" when it is): the payment is paired with exactly
// one Python payment and both have intake_source MANUAL.
func (c *rowComparer) checkManualPayment(ctx context.Context, in RowsInput, goID string) (string, error) {
	var pyIDs []string
	for py, g := range c.maps["payments"] {
		if g == goID {
			pyIDs = append(pyIDs, py)
		}
	}
	if len(pyIDs) != 1 {
		return fmt.Sprintf("payment %s pairs with %d Python payments", goID, len(pyIDs)), nil
	}
	source := func(pool *pgxpool.Pool, id string) (string, error) {
		var s string
		err := pool.QueryRow(ctx, `SELECT intake_source FROM payments WHERE payment_id = $1`, id).Scan(&s)
		if errors.Is(err, pgx.ErrNoRows) {
			return "<none>", nil
		}
		return s, err
	}
	goSource, err := source(in.Go, goID)
	if err != nil {
		return "", err
	}
	pySource, err := source(in.Python, pyIDs[0])
	if err != nil {
		return "", err
	}
	if goSource != "MANUAL" || pySource != "MANUAL" {
		return fmt.Sprintf("intake_source is %s on Go and %s on Python, not MANUAL", goSource, pySource), nil
	}
	return "", nil
}

// goPaymentLines is why a PAYMENT entry (not a reversal) is not how Go posts
// an invoice payment ("" when it is): its lines pass lineRules, the 1100
// credit is in the invoice's currency at the invoice's issue basis (PN 163:
// RoundHalfEven4(relief × issue base / issue face), or the remaining basis
// the way ledger.reliefBase takes it, the last relief taking the whole
// remaining base) with fx_rate = base / relief to 8 places. For a manual
// payment (PN 161) also: the payment's currency is the invoice's, the bank
// debit is its amount at the payment date's rate, a 1300 debit is its
// withholding at that rate (none without withholding), and the 1100 credit is
// amount + withholding.
func (cls *classification) goPaymentLines(ctx context.Context, pool *pgxpool.Pool, entryID string, manual bool) (string, error) {
	lines := cls.lines[entryID]
	ar, why := cls.lineRules("PAYMENT", lines)
	if why != "" {
		return why, nil
	}
	pid := valueString(cls.entries[entryID]["source_id"])
	var invoiceID, amount, currency, day, wht string
	err := pool.QueryRow(ctx, `SELECT COALESCE(invoice_id::text, ''), amount::text, currency, payment_date::text, wht_amount::text
		FROM payments WHERE payment_id = $1`, pid).Scan(&invoiceID, &amount, &currency, &day, &wht)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Sprintf("payment %s does not exist", pid), nil
	}
	if err != nil {
		return "", err
	}
	if invoiceID == "" {
		return fmt.Sprintf("payment %s has no invoice", pid), nil
	}
	var invCurrency string
	if err := pool.QueryRow(ctx, `SELECT currency FROM invoices WHERE invoice_id = $1`, invoiceID).Scan(&invCurrency); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Sprintf("invoice %s does not exist", invoiceID), nil
		}
		return "", err
	}
	if valueString(ar["currency"]) != invCurrency {
		return fmt.Sprintf("its receivable line is in %s, the invoice in %s", valueString(ar["currency"]), invCurrency), nil
	}
	if why, err := cls.reliefAtIssueBasis(ctx, pool, entryID, invoiceID, ar); why != "" || err != nil {
		return why, err
	}
	if !manual {
		return "", nil
	}
	if currency != invCurrency {
		return fmt.Sprintf("payment %s is in %s, its invoice in %s", pid, currency, invCurrency), nil
	}
	var bank, prepaid []Row
	for _, l := range lines {
		switch cls.goAccount(l).code {
		case codeBank:
			bank = append(bank, l)
		case codePrepaidTax:
			prepaid = append(prepaid, l)
		}
	}
	amt, withheld := dec(amount), dec(wht)
	if want := map[bool]int{true: 1, false: 0}[amt.IsPositive()]; len(bank) != want {
		return fmt.Sprintf("it has %d bank lines for a payment of %s", len(bank), amt), nil
	}
	if len(bank) == 1 {
		if why, err := cls.converted(ctx, pool, bank[0], true, amt, currency, day, "bank"); why != "" || err != nil {
			return why, err
		}
	}
	if want := map[bool]int{true: 1, false: 0}[withheld.IsPositive()]; len(prepaid) != want {
		return fmt.Sprintf("it has %d withholding lines for withholding of %s", len(prepaid), withheld), nil
	}
	if len(prepaid) == 1 {
		if why, err := cls.converted(ctx, pool, prepaid[0], true, withheld, invCurrency, day, "withholding"); why != "" || err != nil {
			return why, err
		}
	}
	if want := amt.Add(withheld); !credit(ar).Equal(want) {
		return fmt.Sprintf("its receivable credit is %s, want amount + withholding %s", credit(ar), want), nil
	}
	return "", nil
}

// reliefAtIssueBasis is why a payment's 1100 credit is not relieved at the
// invoice's basis ("" when it is). The issue basis is the invoice's issue
// entry's 1100 debit (the active one, else any). The remaining basis is the
// invoice's receivable on Go's ledger without this entry and its reversal:
// the 1100 lines of its INVOICE entries and of its payments' entries. A base
// that matches either is accepted (a middle partial relief rounded against
// an earlier remaining basis can miss both, and then stays unclassified).
func (cls *classification) reliefAtIssueBasis(ctx context.Context, pool *pgxpool.Pool, entryID, invoiceID string, ar Row) (string, error) {
	return cls.reliefAtBasis(ctx, pool, receivableBasis, entryID, invoiceID, ar)
}

// basisSide is what reliefAtBasis reads for one kind of document: the
// document's and its payments' source types, the query listing its payment
// ids, the control account, and whether that account is a payable (a credit
// balance: the document credits it, a payment debits it).
type basisSide struct {
	doc, control, docSource, paySource, payments, code string
	payable                                            bool
}

var (
	receivableBasis = basisSide{
		doc: "invoice", control: "receivable", docSource: "INVOICE", paySource: "PAYMENT",
		payments: `SELECT payment_id::text FROM payments WHERE invoice_id = $1`, code: codeReceivable,
	}
	payableBasis = basisSide{
		doc: "bill", control: "payable", docSource: "BILL", paySource: "BILL_PAYMENT",
		payments: `SELECT bill_payment_id::text FROM bill_payments WHERE bill_id = $1`, code: codePayable, payable: true,
	}
)

// reliefAtBasis is reliefAtIssueBasis for either side: a bill payment's
// 2000 debit is relieved at the bill's basis the same way (PN 165).
func (cls *classification) reliefAtBasis(ctx context.Context, pool *pgxpool.Pool, s basisSide, entryID, docID string, ctl Row) (string, error) {
	// in is a line's amount and base into the control balance: debit less
	// credit on a receivable, credit less debit on a payable.
	in := func(l Row) (decimal.Decimal, decimal.Decimal) {
		if s.payable {
			return credit(l).Sub(debit(l)), baseCredit(l).Sub(baseDebit(l))
		}
		return debit(l).Sub(credit(l)), baseDebit(l).Sub(baseCredit(l))
	}
	opens := func(l Row) bool { return (s.payable && isCredit(l)) || (!s.payable && isDebit(l)) }
	rows, err := pool.Query(ctx, s.payments, docID)
	if err != nil {
		return "", err
	}
	payments := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return "", err
		}
		payments[id] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return "", err
	}
	skip := map[string]bool{entryID: true, valueString(cls.entries[entryID]["reversed_by_entry_id"]): true}
	var issueFace, issueBase, face, base decimal.Decimal
	issueActive := false
	for id, e := range cls.entries {
		st, sid := valueString(e["source_type"]), valueString(e["source_id"])
		isDoc := st == s.docSource && sid == docID
		isPayment := st == s.paySource && payments[sid]
		if !isDoc && !isPayment {
			continue
		}
		_, isReversal := cls.reversalOf[id]
		for _, l := range cls.lines[id] {
			if cls.goAccount(l).code != s.code {
				continue
			}
			lineFace, lineBase := in(l)
			if !skip[id] {
				face, base = face.Add(lineFace), base.Add(lineBase)
			}
			active := valueString(e["reversed_by_entry_id"]) == ""
			if isDoc && !isReversal && opens(l) && (issueFace.IsZero() || (active && !issueActive)) {
				issueFace, issueBase, issueActive = lineFace, lineBase, active
			}
		}
	}
	if !issueFace.IsPositive() {
		return fmt.Sprintf("%s %s has no issue entry", s.doc, docID), nil
	}
	relief, got := credit(ctl), baseCredit(ctl)
	if s.payable {
		relief, got = debit(ctl), baseDebit(ctl)
	}
	accepted := []decimal.Decimal{platform.RoundHalfEven4(relief.Mul(issueBase).Div(issueFace))}
	if face.IsPositive() && base.IsPositive() {
		switch {
		case relief.Equal(face):
			accepted = append(accepted, base)
		case relief.LessThan(face):
			accepted = append(accepted, platform.RoundHalfEven4(relief.Mul(base).Div(face)))
		}
	}
	ok := false
	for _, a := range accepted {
		if got.Equal(a) {
			ok = true
		}
	}
	if !ok {
		return fmt.Sprintf("its %s base %s for %s is not the %s's issue basis (%v)", s.control, got, relief, s.doc, accepted), nil
	}
	if want := got.DivRound(relief, 8); !dec(ctl["fx_rate"]).Equal(want) {
		return fmt.Sprintf("its %s rate %s is not base / relief %s", s.control, dec(ctl["fx_rate"]), want), nil
	}
	return "", nil
}

// mirrors reports whether reversal's lines are original's with each side
// swapped (debit ↔ credit, base debit ↔ base credit), as Go's postReversal
// writes them.
func mirrors(reversal, original []Row) bool {
	key := func(l Row, swap bool) string {
		dr, cr, bdr, bcr := "debit", "credit", "base_amount_debit", "base_amount_credit"
		if swap {
			dr, cr, bdr, bcr = cr, dr, bcr, bdr
		}
		return strings.Join([]string{
			valueString(l["account_id"]), valueString(l["currency"]), dec(l[dr]).String(), dec(l[cr]).String(),
			dec(l[bdr]).String(), dec(l[bcr]).String(), dec(l["fx_rate"]).String(),
		}, "|")
	}
	if len(reversal) == 0 || len(reversal) != len(original) {
		return false
	}
	counts := map[string]int{}
	for _, l := range original {
		counts[key(l, true)]++
	}
	for _, l := range reversal {
		counts[key(l, false)]--
	}
	for _, n := range counts {
		if n != 0 {
			return false
		}
	}
	return true
}

// classifyRowDiff names the note one paired-table difference is known for:
//   - journal_entries: a Go-only row takes its entry's verdict (a note, or
//     the failed check);
//   - bills: Python is VOID with balance_due ≠ 0 (a PATCH to VOID only sets
//     the status), Go is VOID with 0, Go's notes are Python's plus exactly
//     patchVoidNote, and every other column is equal (PN 164);
//   - payments, invoices, reconciliation_log: the rows a manual payment's
//     reversal changes on Go only (PN 161; see manualPaymentReversed,
//     invoiceRestored, reversedLog).
//
// goRow and pyRow are nil for a side the difference does not have. Anything
// else is returned unclassified.
func (c *rowComparer) classifyRowDiff(d RowDiff, goRow, pyRow Row) RowDiff {
	if c.cls == nil {
		return d
	}
	switch d.Table {
	case "journal_entries":
		if pyRow == nil && goRow != nil && d.Python == "" {
			v := c.cls.verdicts[valueString(goRow["entry_id"])]
			d.Classify, d.Check = v.note, v.check
		}
	case "bills":
		if pyRow != nil && goRow != nil && c.voidedByPatch(pyRow, goRow) {
			d.Classify = "PN 164"
		}
	case "payments":
		if pyRow != nil && goRow != nil && c.manualPaymentReversed(pyRow, goRow) {
			d.Classify = "PN 161"
		}
	case "reconciliation_log":
		if pyRow == nil && goRow != nil && c.reversedLog(goRow) {
			d.Classify = "PN 161"
		}
	case "invoices":
		if pyRow != nil && goRow != nil && c.invoiceRestored(pyRow, goRow) {
			d.Classify = "PN 161"
		}
	}
	return d
}

// voidedByPatch is the bills rule.
func (c *rowComparer) voidedByPatch(py, gov Row) bool {
	if valueString(py["status"]) != "VOID" || valueString(gov["status"]) != "VOID" {
		return false
	}
	if dec(py["balance_due"]).IsZero() || !dec(gov["balance_due"]).IsZero() || valueString(gov["balance_due"]) == "" {
		return false
	}
	if valueString(gov["notes"]) != c.mapped("bills", "notes", py, true)+patchVoidNote {
		return false
	}
	rest := map[string]bool{"balance_due": true, "notes": true}
	return c.canonical("bills", py, true, rest) == c.canonical("bills", gov, false, rest)
}

// manualPaymentReversed is the payments rule for a manual payment Go
// reversed and Python refused to (PN 161): Python CLEARED, Go FLAGGED, its Go
// clearing and reversal both PN 161 entries, every other column equal.
func (c *rowComparer) manualPaymentReversed(py, gov Row) bool {
	if valueString(py["status"]) != "CLEARED" || valueString(gov["status"]) != "FLAGGED" {
		return false
	}
	if _, ok := c.cls.reversedManual[valueString(gov["payment_id"])]; !ok {
		return false
	}
	rest := map[string]bool{"status": true}
	return c.canonical("payments", py, true, rest) == c.canonical("payments", gov, false, rest)
}

// reversedLog is the reconciliation_log rule for the same reversal (PN
// 161): a Go-only REVERSED row for a reversed manual payment, the only
// REVERSED row for it, whose reasons are exactly the reversal's reason.
func (c *rowComparer) reversedLog(gov Row) bool {
	pid := valueString(gov["payment_id"])
	p, ok := c.cls.reversedManual[pid]
	if !ok || valueString(gov["action"]) != "REVERSED" || len(c.cls.reversedLogs[pid]) != 1 {
		return false
	}
	reasons := textArray(gov["reasons"])
	return len(reasons) == 1 && reasons[0] == p.reason
}

// invoiceRestored is the invoices rule for the same reversal (PN 161), as
// payments.Service.Reverse changes the invoice: its wht_booked is Python's
// less the reversed payments' withholding; a VOID invoice stays VOID with
// its balance. Any other takes the receivable left once the payments are
// reversed (PN 168): Python's balance plus their amount and withholding
// (manual payments are in the invoice's currency), OPEN when that is the
// invoice amount, PARTIAL between 0 and the amount, PAID with 0 at or below
// 0; above the amount Go refuses the reversal, so nothing is classified.
// Every other column is equal.
func (c *rowComparer) invoiceRestored(py, gov Row) bool {
	r, ok := c.cls.restored[valueString(gov["invoice_id"])]
	if !ok {
		return false
	}
	face, amount := dec(py["balance_due"]).Add(r.amount).Add(r.wht), dec(py["amount"])
	var wantStatus string
	wantBalance := face
	switch {
	case valueString(py["status"]) == "VOID":
		wantStatus, wantBalance = "VOID", dec(py["balance_due"])
	case !face.IsPositive():
		wantStatus, wantBalance = "PAID", decimal.Zero
	case face.Equal(amount):
		wantStatus = "OPEN"
	case face.LessThan(amount):
		wantStatus = "PARTIAL"
	default:
		return false
	}
	if valueString(gov["status"]) != wantStatus || !dec(gov["balance_due"]).Equal(wantBalance) {
		return false
	}
	if !dec(py["wht_booked"]).Sub(dec(gov["wht_booked"])).Equal(r.wht) {
		return false
	}
	rest := map[string]bool{"balance_due": true, "status": true, "wht_booked": true}
	return c.canonical("invoices", py, true, rest) == c.canonical("invoices", gov, false, rest)
}

// textArray is a text[] column's values.
func textArray(v any) []string {
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		out := make([]string, len(x))
		for i, e := range x {
			out[i] = valueString(e)
		}
		return out
	}
	return nil
}

// lineClassifier classifies journal_lines differences by group (the mapped
// entry id):
//   - a paired PAYMENT entry's lines are PN 163 when its Go lines are how Go
//     posts a payment (payGo) and paymentLinesMatch holds, else they name
//     the failed check;
//   - a Go-only entry's lines (no Python entry pairs with it and no Python
//     line is in its group) take the entry's verdict.
func (c *rowComparer) lineClassifier(pyRows, goRows []Row) func(g string, d RowDiff) RowDiff {
	group := func(rows []Row, python bool) map[string][]Row {
		out := map[string][]Row{}
		for _, row := range rows {
			g := c.mapped("journal_lines", "entry_id", row, python)
			out[g] = append(out[g], row)
		}
		return out
	}
	pyGroups, goGroups := group(pyRows, true), group(goRows, false)
	return func(g string, d RowDiff) RowDiff {
		cls := c.cls
		if cls == nil {
			return d
		}
		if cls.paired[g] {
			if valueString(cls.entries[g]["source_type"]) == "PAYMENT" {
				why := cls.payGo[g]
				if why == "" {
					why = c.paymentLinesMatch(pyGroups[g], goGroups[g])
				}
				if why == "" {
					d.Classify = "PN 163"
				} else {
					d.Check = "PN 163 check failed: entry " + g + ": " + why
				}
			}
			return d
		}
		if d.Python == "" && d.Go != "" && len(pyGroups[g]) == 0 {
			v := cls.verdicts[g]
			d.Classify, d.Check = v.note, v.check
		}
		return d
	}
}

// paymentLinesMatch is the PN 163 comparison of one paired PAYMENT entry's
// two sides ("" when it holds, else why not). Go relieves AR at the
// invoice's issue basis (checked on Go's side by goPaymentLines), so only
// the receivable's base and rate may differ, and realised FX takes up the
// difference:
//   - both sides have lines and balance in base;
//   - the lines other than 4900/5900 are equal as a multiset, with
//     base_amount_debit, base_amount_credit and fx_rate dropped on the 1100
//     lines only (the bank and withholding lines keep their bases and rates);
//   - the change in the FX lines' net base (debit − credit) equals the
//     change in the receivable's base credit (Go − Python). Given the first
//     two, this always holds; it is kept as a named check.
func (c *rowComparer) paymentLinesMatch(py, gov []Row) string {
	if len(py) == 0 || len(gov) == 0 {
		return "a side has no lines"
	}
	cls := c.cls
	type sums struct{ fx, ar decimal.Decimal }
	sideOf := func(rows []Row, python bool) (map[string]int, sums, string) {
		texts := map[string]int{}
		var s sums
		var dr, cr decimal.Decimal
		for _, l := range rows {
			code := cls.goAccount(l).code
			if python {
				code = cls.pyCodes[valueString(l["account_id"])]
			}
			bdr, bcr := baseDebit(l), baseCredit(l)
			dr, cr = dr.Add(bdr), cr.Add(bcr)
			switch code {
			case codeFXGain, codeFXLoss:
				s.fx = s.fx.Add(bdr).Sub(bcr)
				continue
			case codeReceivable:
				s.ar = s.ar.Add(bcr).Sub(bdr)
				texts[c.canonical("journal_lines", l, python, receivableIgnored)]++
			default:
				texts[c.canonical("journal_lines", l, python, lineIgnored)]++
			}
		}
		if !dr.Equal(cr) {
			return nil, s, fmt.Sprintf("base debits %s ≠ base credits %s", dr, cr)
		}
		return texts, s, ""
	}
	p, ps, why := sideOf(py, true)
	if why != "" {
		return "python " + why
	}
	g, gs, why := sideOf(gov, false)
	if why != "" {
		return "go " + why
	}
	if len(p) != len(g) {
		return "the lines other than the receivable's base and the 4900/5900 lines differ"
	}
	for text, n := range p {
		if g[text] != n {
			return "the lines other than the receivable's base and the 4900/5900 lines differ"
		}
	}
	if fx, ar := gs.fx.Sub(ps.fx), gs.ar.Sub(ps.ar); !fx.Equal(ar) {
		return fmt.Sprintf("the FX lines' net base changed by %s, the receivable's base by %s", fx, ar)
	}
	return ""
}

// lineIgnored is a journal line's primary key; receivableIgnored also drops
// the base amounts and the rate (PN 163).
var (
	lineIgnored       = map[string]bool{"line_id": true}
	receivableIgnored = map[string]bool{"line_id": true, "base_amount_debit": true, "base_amount_credit": true, "fx_rate": true}
)

// accounts is one database's chart of accounts by account id (as text).
func accounts(ctx context.Context, pool *pgxpool.Pool) (map[string]account, error) {
	rows, err := pool.Query(ctx, `SELECT account_id::text, code, type FROM chart_of_accounts`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]account{}
	for rows.Next() {
		var id string
		var a account
		if err := rows.Scan(&id, &a.code, &a.typ); err != nil {
			return nil, err
		}
		out[id] = a
	}
	return out, rows.Err()
}

// dec is a numeric column's value as a decimal; a value that is not a
// number is zero.
func dec(v any) decimal.Decimal {
	d, err := decimal.NewFromString(valueString(v))
	if err != nil {
		return decimal.Zero
	}
	return d
}
