package compare

import (
	"context"
	"errors"
	"fmt"
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
// checks on the entry, its siblings, its lines and its document. A
// difference that fails a check stays unclassified and names the check
// (RowDiff.Check).

// accrualNotes is the note for a journal entry Go posts and Python never
// does, by source_type: an invoice's issue and void (PN 162), a bill's open,
// edit and void (PN 164), a bill payment and its reversal (PN 165), and a
// manual invoice payment and its reversal (PN 161; checkPayment makes it
// narrow). A reversal copies its original's source_type.
var accrualNotes = map[string]string{
	"INVOICE":      "PN 162",
	"BILL":         "PN 164",
	"BILL_PAYMENT": "PN 165",
	"PAYMENT":      "PN 161",
}

// controlLine is each source_type's control account and side on an entry
// that is not a reversal (a reversal mirrors it): an invoice debits AR, a
// bill credits AP, a bill payment debits AP, an invoice payment debits the
// bank.
var controlLine = map[string]struct {
	code  string
	debit bool
}{
	"INVOICE":      {"1100", true},
	"BILL":         {"2000", false},
	"BILL_PAYMENT": {"2000", true},
	"PAYMENT":      {"1000", true},
}

// Account codes the rules name.
const (
	codeReceivable = "1100"
	codeFXGain     = "4900"
	codeFXLoss     = "5900"
)

// patchVoidNote is the exact notes line Go appends when a PATCH sets a bill
// VOID (bills.updateBill → voidBill with this reason).
const patchVoidNote = "\n[voided] status set to VOID via update"

// classification is what the rules read beyond the two rows of a difference.
type classification struct {
	base             string
	pyCodes, goCodes map[string]string // account_id → code
	entries          map[string]Row    // every Go journal entry, by id
	lines            map[string][]Row  // every Go journal line, by entry id
	reversalOf       map[string]string // reversal entry id → the entry it reverses
	paired           map[string]bool   // Go entry ids a Python entry pairs with
	verdicts         map[string]verdict
	// reversedManual is each Go manual payment whose clearing and reversal
	// are both PN 161 entries, by Go payment id; restored sums them per Go
	// invoice id. They are the rows a manual payment's reversal changes on
	// Go only (Python refuses it, PN 161).
	reversedManual map[string]reversedPayment
	restored       map[string]reversedPayment
}

// reversedPayment is a reversed manual payment's amounts (or, in restored,
// an invoice's sum of them).
type reversedPayment struct {
	invoiceID   string
	amount, wht decimal.Decimal
}

// verdict is one Go-only entry's classification: a note, or the check it
// failed.
type verdict struct{ note, check string }

// prepareClassification reads what the rules need, after the pairs are
// derived, and decides every Go-only journal entry's verdict.
func (c *rowComparer) prepareClassification(ctx context.Context, in RowsInput, states map[string]*tableState) error {
	cls := &classification{
		base:       in.BaseCurrency,
		entries:    map[string]Row{},
		lines:      map[string][]Row{},
		reversalOf: map[string]string{},
		paired:     map[string]bool{},
		verdicts:   map[string]verdict{},
	}
	if cls.base == "" {
		cls.base = DefaultBaseCurrency
	}
	var err error
	if cls.pyCodes, err = accountCodes(ctx, in.Python); err != nil {
		return fmt.Errorf("python account codes: %w", err)
	}
	if cls.goCodes, err = accountCodes(ctx, in.Go); err != nil {
		return fmt.Errorf("go account codes: %w", err)
	}
	c.cls = cls
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
	for _, row := range je.goNew {
		id := valueString(row["entry_id"])
		if cls.paired[id] || accrualNotes[valueString(row["source_type"])] == "" {
			continue
		}
		v, err := c.entryVerdict(ctx, in, id, true)
		if err != nil {
			return err
		}
		cls.verdicts[id] = v
	}
	return cls.findReversedManual(ctx, in.Go)
}

// findReversedManual fills reversedManual and restored: a Go-only PAYMENT
// entry that is PN 161, is reversed, and whose reversal is PN 161 too.
func (cls *classification) findReversedManual(ctx context.Context, pool *pgxpool.Pool) error {
	cls.reversedManual = map[string]reversedPayment{}
	cls.restored = map[string]reversedPayment{}
	for id, v := range cls.verdicts {
		e := cls.entries[id]
		r := valueString(e["reversed_by_entry_id"])
		if v.note != "PN 161" || r == "" || cls.verdicts[r].note != "PN 161" {
			continue
		}
		if _, isReversal := cls.reversalOf[id]; isReversal {
			continue
		}
		pid := valueString(e["source_id"])
		var invoiceID, amount, wht string
		err := pool.QueryRow(ctx, `SELECT COALESCE(invoice_id::text, ''), amount::text, wht_amount::text FROM payments WHERE payment_id = $1`, pid).
			Scan(&invoiceID, &amount, &wht)
		if err != nil {
			return fmt.Errorf("reversed manual payment %s: %w", pid, err)
		}
		p := reversedPayment{invoiceID: invoiceID, amount: dec(amount), wht: dec(wht)}
		cls.reversedManual[pid] = p
		if invoiceID != "" {
			sum := cls.restored[invoiceID]
			sum.amount, sum.wht = sum.amount.Add(p.amount), sum.wht.Add(p.wht)
			cls.restored[invoiceID] = sum
		}
	}
	return nil
}

// DefaultBaseCurrency is the ledger's base currency when none is given.
const DefaultBaseCurrency = "IDR"

// entryVerdict checks one Go entry. A reversal is checked through the entry
// it reverses (which must pass) and must mirror its lines; any other entry
// must have at least two lines that balance, be the only active entry of its
// kind for its source, have exactly one control line on the right side, and
// match its document or payment.
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
	var own, active int
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
	ctl := controlLine[src]
	var control []Row
	for _, l := range lines {
		if cls.goCodes[valueString(l["account_id"])] == ctl.code {
			control = append(control, l)
		}
	}
	side := map[bool]string{true: "debit", false: "credit"}[ctl.debit]
	if len(control) != 1 || !dec(control[0][side]).IsPositive() {
		return fail("it needs exactly one %s line on %s, has %d", side, ctl.code, len(control))
	}
	switch src {
	case "INVOICE", "BILL":
		if valueString(e["reversed_by_entry_id"]) != "" {
			// A reversed issue entry (void, edit, return to draft) is
			// checked as a pair through its reversal; its document may have
			// changed since.
			return verdict{note: note}, nil
		}
		why, err := cls.checkDocument(ctx, in.Go, src, sourceID, control[0], side)
		if err != nil {
			return verdict{}, err
		}
		if why != "" {
			return fail("%s", why)
		}
	case "PAYMENT":
		why, err := c.checkPayment(ctx, in, sourceID, control[0])
		if err != nil {
			return verdict{}, err
		}
		if why != "" {
			return fail("%s", why)
		}
	}
	return verdict{note: note}, nil
}

// unbalanced is why lines do not balance: in base over every line, and in
// amount per currency over the lines other than realised FX ("" when they
// balance).
func (cls *classification) unbalanced(lines []Row) string {
	var dr, cr decimal.Decimal
	amounts := map[string]decimal.Decimal{}
	for _, l := range lines {
		dr = dr.Add(dec(l["base_amount_debit"]))
		cr = cr.Add(dec(l["base_amount_credit"]))
		if code := cls.goCodes[valueString(l["account_id"])]; code == codeFXGain || code == codeFXLoss {
			continue
		}
		ccy := valueString(l["currency"])
		amounts[ccy] = amounts[ccy].Add(dec(l["debit"])).Sub(dec(l["credit"]))
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

// checkDocument is why an active issue entry's control line does not match
// its document ("" when it does): the document exists, is not a DRAFT or a
// template, and the line is its amount and currency at the rate on its issue
// date (else its created_at date), the base rounded half-even to 4 places.
func (cls *classification) checkDocument(ctx context.Context, pool *pgxpool.Pool, src, id string, ctl Row, side string) (string, error) {
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
	if status == "DRAFT" || template {
		return fmt.Sprintf("its document is %s (template %v)", status, template), nil
	}
	want := dec(amount)
	if got := dec(ctl[side]); !got.Equal(want) || valueString(ctl["currency"]) != currency {
		return fmt.Sprintf("its control line is %s %s, the document %s %s", got, valueString(ctl["currency"]), want, currency), nil
	}
	rate := decimal.NewFromInt(1)
	if currency != cls.base {
		var text string
		err := pool.QueryRow(ctx, `SELECT rate::text FROM fx_rates WHERE from_currency = $1 AND to_currency = $2 AND as_of_date <= $3::date
			ORDER BY as_of_date DESC LIMIT 1`, currency, cls.base, day).Scan(&text)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Sprintf("no %s rate on or before %s", currency, day), nil
		}
		if err != nil {
			return "", err
		}
		rate = dec(text)
	}
	base := dec(ctl["base_amount_"+side])
	if !dec(ctl["fx_rate"]).Equal(rate) || !base.Equal(platform.RoundHalfEven4(want.Mul(rate))) {
		return fmt.Sprintf("its control line's rate %s and base %s are not the %s rate %s on %s", dec(ctl["fx_rate"]), base, currency, rate, day), nil
	}
	return "", nil
}

// checkPayment is why a Go-only PAYMENT entry is not a manual payment's
// (PN 161) ("" when it is): the payment is paired with exactly one Python
// payment, both have intake_source MANUAL, and the bank line is the
// payment's amount and currency.
func (c *rowComparer) checkPayment(ctx context.Context, in RowsInput, goID string, bank Row) (string, error) {
	var pyIDs []string
	for py, g := range c.maps["payments"] {
		if g == goID {
			pyIDs = append(pyIDs, py)
		}
	}
	if len(pyIDs) != 1 {
		return fmt.Sprintf("payment %s pairs with %d Python payments", goID, len(pyIDs)), nil
	}
	read := func(pool *pgxpool.Pool, id string) (source, amount, currency string, found bool, err error) {
		err = pool.QueryRow(ctx, `SELECT intake_source, amount::text, currency FROM payments WHERE payment_id = $1`, id).
			Scan(&source, &amount, &currency)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", "", false, nil
		}
		return source, amount, currency, err == nil, err
	}
	goSource, amount, currency, found, err := read(in.Go, goID)
	if err != nil {
		return "", err
	}
	if !found {
		return fmt.Sprintf("payment %s does not exist", goID), nil
	}
	pySource, _, _, found, err := read(in.Python, pyIDs[0])
	if err != nil {
		return "", err
	}
	if !found {
		return fmt.Sprintf("python payment %s does not exist", pyIDs[0]), nil
	}
	if goSource != "MANUAL" || pySource != "MANUAL" {
		return fmt.Sprintf("intake_source is %s on Go and %s on Python, not MANUAL", goSource, pySource), nil
	}
	if got := dec(bank["debit"]); !got.Equal(dec(amount)) || valueString(bank["currency"]) != currency {
		return fmt.Sprintf("its bank line is %s %s, the payment %s %s", got, valueString(bank["currency"]), dec(amount), currency), nil
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
//     patchVoidNote, and every other column is equal (PN 164).
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
		if pyRow == nil && goRow != nil && valueString(goRow["action"]) == "REVERSED" {
			if _, ok := c.cls.reversedManual[valueString(goRow["payment_id"])]; ok {
				d.Classify = "PN 161"
			}
		}
	case "invoices":
		if pyRow != nil && goRow != nil && c.invoiceRestored(pyRow, goRow) {
			d.Classify = "PN 161"
		}
	}
	return d
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

// invoiceRestored is the invoices rule for the same reversal (PN 161): Go is
// OPEN, its balance_due is Python's plus the reversed manual payments'
// amount and withholding, its wht_booked is Python's less their
// withholding, and every other column is equal.
func (c *rowComparer) invoiceRestored(py, gov Row) bool {
	r, ok := c.cls.restored[valueString(gov["invoice_id"])]
	if !ok || valueString(gov["status"]) != "OPEN" {
		return false
	}
	if !dec(gov["balance_due"]).Sub(dec(py["balance_due"])).Equal(r.amount.Add(r.wht)) {
		return false
	}
	if !dec(py["wht_booked"]).Sub(dec(gov["wht_booked"])).Equal(r.wht) {
		return false
	}
	rest := map[string]bool{"balance_due": true, "status": true, "wht_booked": true}
	return c.canonical("invoices", py, true, rest) == c.canonical("invoices", gov, false, rest)
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

// lineClassifier classifies journal_lines differences by group (the mapped
// entry id):
//   - a paired PAYMENT entry's lines are PN 163 when paymentLinesMatch holds,
//     else they name its failed check;
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
				if why := c.paymentLinesMatch(pyGroups[g], goGroups[g]); why == "" {
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

// paymentLinesMatch is the PN 163 rule for one paired PAYMENT entry's lines
// ("" when it holds, else why not). Go relieves AR at the invoice's issue
// basis, so only the receivable's base and rate may differ, and realised FX
// takes up the difference:
//   - both sides have lines and balance in base;
//   - the lines other than 4900/5900 are equal as a multiset, with
//     base_amount_debit, base_amount_credit and fx_rate dropped on the 1100
//     lines only (the bank and withholding lines keep their bases and rates);
//   - the change in the FX lines' net base (debit − credit) equals the
//     change in the receivable's base credit (Go − Python).
func (c *rowComparer) paymentLinesMatch(py, gov []Row) string {
	if len(py) == 0 || len(gov) == 0 {
		return "a side has no lines"
	}
	cls := c.cls
	type sums struct{ fx, ar decimal.Decimal }
	sideOf := func(rows []Row, python bool) (map[string]int, sums, string) {
		codes := cls.goCodes
		if python {
			codes = cls.pyCodes
		}
		texts := map[string]int{}
		var s sums
		var dr, cr decimal.Decimal
		for _, l := range rows {
			bdr, bcr := dec(l["base_amount_debit"]), dec(l["base_amount_credit"])
			dr, cr = dr.Add(bdr), cr.Add(bcr)
			switch codes[valueString(l["account_id"])] {
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

// accountCodes is one database's account codes by account id (as text).
func accountCodes(ctx context.Context, pool *pgxpool.Pool) (map[string]string, error) {
	rows, err := pool.Query(ctx, `SELECT account_id::text, code FROM chart_of_accounts`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, code string
		if err := rows.Scan(&id, &code); err != nil {
			return nil, err
		}
		out[id] = code
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
