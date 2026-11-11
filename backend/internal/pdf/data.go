package pdf

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/business/businessdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/customers/customersdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices/invoicesdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/payments/paymentsdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/quotations/quotationsdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/withholding"
)

// MetaCell is one key and value about the document: its number, dates and
// PO/SO (the layout prints the number and dates in the header band, the rest
// in the Reference column).
type MetaCell struct{ Key, Value string }

// SummaryRow is one row of the summary box. Class picks its colour: subtotal,
// discount, grossup, total, wht or paid.
type SummaryRow struct{ Label, Value, Class string }

// Item is one row of the items table; Sub is the grey line under the
// description.
type Item struct{ Desc, Sub, Qty, Rate, Amount string }

// Document is everything a renderer prints, for an invoice, a quotation or a
// receipt (Python's context dict of pdf/context.py). Every text value is
// final: formatted, never to be parsed again. "" is Python's None.
type Document struct {
	// DocTitle prints as typed: "Invoice" (or the business's own title),
	// "Quotation", "Payment Receipt".
	DocTitle string
	// Location is the header's line under the business name: the last
	// non-empty line of the business address, "" when there is none.
	Location    string
	Ref         string
	StatusLabel string
	StatusClass string
	Meta        []MetaCell

	FromLabel string
	ToLabel   string
	FromName  string
	FromLines []string
	ToName    string
	ToLines   []string

	Items    []Item
	Currency string

	Summary    []SummaryRow
	HeroLabel  string
	HeroAmount string

	PaymentInstructions string
	NPWP                string
	Notes               string
	Terms               string
	// Footer is the document's own footer text (Go change 10: the HTML
	// design dropped it, the reportlab one showed it).
	Footer string

	FootName string
	FootMeta []string
	// LogoURL is the business's logo_url as stored; the renderer resolves it.
	LogoURL string
	// Date is the PDF's creation date: the issue date (the payment date on
	// a receipt). The zero value prints a fixed epoch, so the same
	// document always renders to the same bytes.
	Date time.Time

	// Receipt is what only a payment receipt prints; nil on an invoice or
	// a quotation. A receipt has no items table: its hero is the amount
	// received, and its summary box ends in the balance due.
	Receipt *ReceiptBody
}

// ReceiptBody is a receipt's own part. Every figure is a stored value in its
// own currency, never converted (spec "Receipt amounts").
type ReceiptBody struct {
	// Payment is the parties band's Payment column: Method, Bank ref. (only
	// when the payment stores one), Received.
	Payment []MetaCell
	// Words is the amount received in words.
	Words string
	// PaidInFull is the balance after this payment being zero or less.
	PaidInFull bool
	// The "Applied to" row: the invoice, its issue date, its total in the
	// invoice's currency, and this payment (cash plus the withholding it
	// settles) in the payment's.
	Invoice, Issued               string
	InvoiceCurrency, InvoiceTotal string
	ThisPayment                   string
	// WithholdingLabel heads the bukti potong note ("PPh 23"); both are ""
	// when the payment carries no withholding.
	WithholdingLabel, WithholdingNote string
	// BalanceDue is the bar's amount: the invoice's balance after this
	// payment, with its currency ("IDR 0").
	BalanceDue string
}

// InvoiceData is an invoice with what its document shows. Customer and
// Business are nil when the row is missing; Accounts are the business's
// payment accounts ordered by currency.
type InvoiceData struct {
	Invoice  invoicesdb.Invoice
	Lines    []invoicesdb.InvoiceLineItem
	Customer *customersdb.Customer
	Business *businessdb.BusinessProfile
	Accounts []businessdb.PaymentAccount
}

// QuotationData is a quotation with what its document shows.
type QuotationData struct {
	Quotation quotationsdb.Quotation
	Lines     []quotationsdb.QuotationLineItem
	Customer  *customersdb.Customer
	Business  *businessdb.BusinessProfile
	Accounts  []businessdb.PaymentAccount
}

// ReceiptData is a payment, the invoice it is applied to, and what the
// receipt shows. Python's receipt takes no payment accounts.
type ReceiptData struct {
	Payment  paymentsdb.Payment
	Invoice  invoicesdb.Invoice
	Customer *customersdb.Customer
	Business *businessdb.BusinessProfile
	// BalanceAfter is the invoice balance right after Payment (BalanceAfter), not
	// the live balance: the receipt of an old partial payment must not read paid.
	BalanceAfter decimal.Decimal
}

// ReceiptReference is the receipt's number: RCT- and the first 8 hex
// characters of the payment id, upper-cased (Python receipt_reference).
func ReceiptReference(paymentID uuid.UUID) string {
	return "RCT-" + strings.ToUpper(strings.ReplaceAll(paymentID.String(), "-", "")[:8])
}

// BalanceNote is the one-line balance summary of the receipt (Python
// balance_note): paid in full at a balance of 0 or less.
func BalanceNote(balance decimal.Decimal, currency string) string {
	if !balance.IsPositive() {
		return "This invoice is now paid in full."
	}
	return "Remaining balance: " + FmtAmount(balance, currency) + "."
}

// MethodLabel is how a payment arrived (Python _method_label): "Manual entry",
// else the intake source title-cased ("Webhook", "Email Parser").
func MethodLabel(intakeSource string) string {
	if intakeSource == "MANUAL" {
		return "Manual entry"
	}
	return StatusLabel(intakeSource)
}

// ResolvePaymentInstructions picks the block for currency: the first account
// in that exact currency, else the first default account, else nil.
func ResolvePaymentInstructions(accounts []businessdb.PaymentAccount, currency string) *string {
	for _, a := range accounts {
		if a.Currency == currency {
			return &a.Instructions
		}
	}
	for _, a := range accounts {
		if a.IsDefault {
			return &a.Instructions
		}
	}
	return nil
}

// splitLines is Python's str.splitlines (no trailing empty line; blank lines
// in the middle are kept).
func splitLines(s string) []string {
	var out []string
	start := 0
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		switch rs[i] {
		case '\r':
			out = append(out, string(rs[start:i]))
			if i+1 < len(rs) && rs[i+1] == '\n' {
				i++
			}
			start = i + 1
		case '\n', '\v', '\f', 0x1c, 0x1d, 0x1e, 0x85, 0x2028, 0x2029:
			out = append(out, string(rs[start:i]))
			start = i + 1
		}
	}
	if start < len(rs) {
		out = append(out, string(rs[start:]))
	}
	return out
}

// join is the non-empty parts joined by sep.
func join(sep string, parts ...*string) string {
	var kept []string
	for _, p := range parts {
		if p != nil && *p != "" {
			kept = append(kept, *p)
		}
	}
	return strings.Join(kept, sep)
}

func addrLines(c *customersdb.Customer) []string {
	if c == nil {
		return nil
	}
	var lines []string
	for _, v := range []*string{c.BillingAddress1, c.BillingAddress2} {
		if v != nil && *v != "" {
			lines = append(lines, *v)
		}
	}
	if region := join(", ", c.BillingState, c.BillingPostalCode); region != "" {
		lines = append(lines, region)
	}
	if cc := join(", ", c.BillingCity, c.BillingCountry); cc != "" {
		lines = append(lines, cc)
	}
	if len(lines) == 0 && c.BillingAddress != nil && *c.BillingAddress != "" {
		lines = splitLines(*c.BillingAddress)
	}
	return lines
}

func customerLines(c *customersdb.Customer) []string {
	lines := addrLines(c)
	if c == nil {
		return lines
	}
	if contact := join(" · ", c.ContactEmail, c.ContactPhone); contact != "" {
		lines = append(lines, contact)
	}
	return lines
}

func businessLines(b *businessdb.BusinessProfile) []string {
	if b == nil {
		return nil
	}
	var lines []string
	if b.Address != nil {
		for _, ln := range splitLines(*b.Address) {
			if strings.TrimSpace(ln) != "" {
				lines = append(lines, ln)
			}
		}
	}
	if contact := join(" · ", b.ContactEmail, b.ContactPhone); contact != "" {
		lines = append(lines, contact)
	}
	return lines
}

// location is the last non-empty line of the business's free-text address,
// trimmed (there are no city or country fields).
func location(b *businessdb.BusinessProfile) string {
	if b == nil || b.Address == nil {
		return ""
	}
	lines := splitLines(*b.Address)
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return ""
}

func orDash(s *string) string {
	if s == nil || *s == "" {
		return Dash
	}
	return *s
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func businessName(b *businessdb.BusinessProfile) *string {
	if b == nil {
		return nil
	}
	return b.Name
}

func customerName(c *customersdb.Customer) *string {
	if c == nil {
		return nil
	}
	return &c.Name
}

// footMeta is the business's contact email and phone, those that are set.
func footMeta(b *businessdb.BusinessProfile) []string {
	if b == nil {
		return nil
	}
	var out []string
	for _, p := range []*string{b.ContactEmail, b.ContactPhone} {
		if p != nil && *p != "" {
			out = append(out, *p)
		}
	}
	return out
}

// common is Python's _common: what an invoice and a quotation share.
func common(status, currency string, notes, terms *string, c *customersdb.Customer, b *businessdb.BusinessProfile, accounts []businessdb.PaymentAccount) Document {
	d := Document{
		StatusLabel: StatusLabel(status),
		StatusClass: StatusBadgeClass(status),
		Location:    location(b),
		FromLabel:   "From",
		ToLabel:     "Bill to",
		FromName:    orDash(businessName(b)),
		FromLines:   businessLines(b),
		ToName:      orDash(customerName(c)),
		ToLines:     customerLines(c),
		Currency:    currency,
		FootName:    deref(businessName(b)),
		FootMeta:    footMeta(b),
	}
	if p := ResolvePaymentInstructions(accounts, currency); p != nil {
		d.PaymentInstructions = *p
	}
	d.Notes = deref(notes)
	if d.Notes == "" && b != nil {
		d.Notes = deref(b.DefaultNotes)
	}
	d.Terms = deref(terms)
	if b != nil {
		d.LogoURL = deref(b.LogoUrl)
	}
	return d
}

func discountRow(typ *string, value, subtotal decimal.NullDecimal, currency string) (SummaryRow, bool) {
	disc, ok := DiscountAmount(typ, value, subtotal)
	if !ok {
		return SummaryRow{}, false
	}
	suffix := ""
	if *typ == "PERCENT" {
		suffix = " (" + FmtPct(value) + "%)"
	}
	return SummaryRow{Label: "Discount" + suffix, Value: Minus + FmtNum(disc, currency), Class: "discount"}, true
}

// dateOf is a date column as a time, zero when NULL.
func dateOf(d pgtype.Date) time.Time {
	if !d.Valid {
		return time.Time{}
	}
	return d.Time
}

// meta adds the PO/SO number as a fourth cell when it is set (Go change 10).
func meta(cells []MetaCell, poSo *string) []MetaCell {
	if poSo != nil && *poSo != "" {
		cells = append(cells, MetaCell{"PO / SO", *poSo})
	}
	return cells
}

// InvoiceDocument is Python's build_invoice_context.
func InvoiceDocument(in InvoiceData) Document {
	inv, cur := in.Invoice, in.Invoice.Currency
	d := common(inv.Status, cur, inv.Notes, nil, in.Customer, in.Business, in.Accounts)
	d.NPWP = NPWPLine(inv.WhtType, in.Business)
	for _, li := range in.Lines {
		d.Items = append(d.Items, Item{
			Desc: li.Description, Qty: FmtQty(li.Quantity),
			Rate: FmtNum(li.UnitPrice, cur), Amount: FmtNum(li.Amount, cur),
		})
	}

	rows := []SummaryRow{{"Subtotal", FmtNumN(inv.Subtotal, cur), "subtotal"}}
	if r, ok := discountRow(inv.DiscountType, inv.DiscountValue, inv.Subtotal, cur); ok {
		rows = append(rows, r)
	}
	wht := inv.WhtType != "" && inv.WhtType != "NONE"
	// balance_due also drops by booked PPh, which is not cash received.
	var paid decimal.Decimal
	var hasPaid bool
	if p := inv.Amount.Sub(inv.BalanceDue).Sub(inv.WhtBooked); p.IsPositive() {
		paid, hasPaid = p.RoundBank(2), true
	}
	switch {
	case wht:
		label, rate := withholding.Labels[inv.WhtType], FmtPct(inv.WhtRate)
		if inv.WhtGrossUp {
			rows = append(rows, SummaryRow{
				fmt.Sprintf("%s (%s%%)", label, rate),
				FmtNum(inv.Amount.Sub(inv.WhtBase.Decimal), cur), "grossup",
			})
		}
		rows = append(rows,
			SummaryRow{"Total", FmtNum(inv.Amount, cur), "total"},
			SummaryRow{fmt.Sprintf("%s withholding (%s%%)", label, rate), Minus + FmtNum(inv.WhtAmount, cur), "wht"})
		if hasPaid {
			rows = append(rows, SummaryRow{"Paid", Minus + FmtNum(paid, cur), "paid"})
		}
	case hasPaid:
		rows = append(rows,
			SummaryRow{"Total", FmtNum(inv.Amount, cur), "total"},
			SummaryRow{"Paid", Minus + FmtNum(paid, cur), "paid"})
	}

	d.DocTitle = "Invoice"
	if b := in.Business; b != nil && deref(b.InvoiceTitle) != "" {
		d.DocTitle = *b.InvoiceTitle
	}
	// "Net N" drives the due date, not a customer-facing term.
	d.Terms = ""
	d.Ref = orDash(inv.InvoiceNumber)
	d.Meta = meta([]MetaCell{
		{"Invoice No.", d.Ref},
		{"Issue Date", FmtDate(inv.IssueDate)},
		{"Due Date", FmtDate(inv.DueDate)},
	}, inv.PoSoNumber)
	d.Footer = deref(inv.Footer)
	d.Date = dateOf(inv.IssueDate)
	d.Summary = rows
	d.HeroLabel, d.HeroAmount = "Total Due", FmtNum(inv.BalanceDue, cur)
	if wht {
		d.HeroLabel = "Amount to Transfer"
		d.HeroAmount = FmtNum(withholding.ExpectedCash(inv.BalanceDue, inv.WhtAmount, inv.WhtBooked), cur)
	}
	return d
}

// QuotationDocument is Python's build_quotation_context.
func QuotationDocument(in QuotationData) Document {
	q, cur := in.Quotation, in.Quotation.Currency
	d := common(q.Status, cur, q.Notes, q.PaymentTerms, in.Customer, in.Business, in.Accounts)
	for _, li := range in.Lines {
		d.Items = append(d.Items, Item{
			Desc: li.Description, Qty: FmtQty(li.Quantity),
			Rate: FmtNum(li.UnitPrice, cur), Amount: FmtNum(li.Amount, cur),
		})
	}
	d.Summary = []SummaryRow{{"Subtotal", FmtNumN(q.Subtotal, cur), "subtotal"}}
	if r, ok := discountRow(q.DiscountType, q.DiscountValue, q.Subtotal, cur); ok {
		d.Summary = append(d.Summary, r)
	}
	d.DocTitle = "Quotation"
	d.Ref = orDash(q.QuotationNumber)
	d.Meta = meta([]MetaCell{
		{"Quote No.", d.Ref},
		{"Issue Date", FmtDate(q.IssueDate)},
		{"Valid Until", FmtDate(q.ValidUntil)},
	}, q.PoSoNumber)
	d.Footer = deref(q.Footer)
	d.Date = dateOf(q.IssueDate)
	d.HeroLabel, d.HeroAmount = "Total", FmtNum(q.Amount, cur)
	return d
}

// ReceiptDocument is Python's build_receipt_context in the brand layout. The
// summary values carry their own currency code (Go change 4): the invoice's,
// which can differ from the payment's that the amount received is in.
func ReceiptDocument(in ReceiptData) Document {
	pay, inv := in.Payment, in.Invoice
	cur := pay.Currency
	ref := ReceiptReference(pay.PaymentID)
	payment := []MetaCell{{"Method", MethodLabel(pay.IntakeSource)}}
	if r := deref(pay.PayerReference); r != "" {
		payment = append(payment, MetaCell{bankRefKey, r})
	}
	payment = append(payment, MetaCell{"Received", FmtDate(pay.PaymentDate)})
	body := &ReceiptBody{
		Payment:         payment,
		Words:           AmountInWords(pay.Amount, cur),
		PaidInFull:      !in.BalanceAfter.IsPositive(),
		Invoice:         orDash(inv.InvoiceNumber),
		Issued:          FmtDate(inv.IssueDate),
		InvoiceCurrency: inv.Currency,
		InvoiceTotal:    FmtNum(inv.Amount, inv.Currency),
		ThisPayment:     FmtNum(pay.Amount.Add(pay.WhtAmount), cur),
		BalanceDue:      inv.Currency + " " + FmtNum(in.BalanceAfter, inv.Currency),
	}
	summary := []SummaryRow{{"Invoice total", FmtAmount(inv.Amount, inv.Currency), "subtotal"}}
	if !pay.WhtAmount.IsZero() {
		label := withholding.Labels[inv.WhtType]
		if label == "" {
			label = "PPh"
		}
		rate := ""
		if inv.WhtRate.Valid {
			rate = " (" + FmtPct(inv.WhtRate) + "%)"
		}
		withheld := FmtAmount(pay.WhtAmount, cur)
		summary = append(summary, SummaryRow{label + " withheld" + rate, Minus + withheld, "wht"})
		to := ""
		if b := in.Business; b != nil && deref(b.ContactEmail) != "" {
			to = " to " + *b.ContactEmail
		}
		body.WithholdingLabel = label
		body.WithholdingNote = fmt.Sprintf("%s of %s%s was withheld by the payer and credited against the invoice "+
			"total. Please send the bukti potong%s if not yet provided.", label, withheld, rate, to)
	}
	summary = append(summary, SummaryRow{"Amount received", FmtAmount(pay.Amount, cur), "total"})
	d := Document{
		DocTitle:    "Payment Receipt",
		Ref:         ref,
		StatusLabel: "Received",
		StatusClass: "paid",
		Location:    location(in.Business),
		FromLabel:   "Issued by",
		ToLabel:     "Received from",
		FromName:    orDash(businessName(in.Business)),
		FromLines:   businessLines(in.Business),
		ToName:      Dash,
		ToLines:     customerLines(in.Customer),
		Currency:    cur,
		Meta: []MetaCell{
			{"Receipt No.", ref},
			{"Payment Date", FmtDate(pay.PaymentDate)},
		},
		Summary:    summary,
		HeroLabel:  "Amount received",
		HeroAmount: FmtNum(pay.Amount, cur),
		Notes:      BalanceNote(in.BalanceAfter, inv.Currency) + "\nThank you for your payment.",
		FootName:   deref(businessName(in.Business)),
		FootMeta:   footMeta(in.Business),
		Date:       dateOf(pay.PaymentDate),
		Receipt:    body,
	}
	d.ToName = firstNonEmpty(deref(customerName(in.Customer)), pay.PayerName, Dash)
	if in.Business != nil {
		d.LogoURL = deref(in.Business.LogoUrl)
	}
	return d
}

// bankRefKey is the Payment column's key for the payer's bank reference,
// which prints in the monospace.
const bankRefKey = "Bank ref."
