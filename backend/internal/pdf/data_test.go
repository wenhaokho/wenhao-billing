package pdf

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/business/businessdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/customers/customersdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices/invoicesdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/payments/paymentsdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/quotations/quotationsdb"
)

func date(y int, m time.Month, d int) pgtype.Date {
	return pgtype.Date{Time: time.Date(y, m, d, 0, 0, 0, 0, time.UTC), Valid: true}
}

// biz is tests/unit/test_pdf_context.py's _biz().
func biz() *businessdb.BusinessProfile {
	return &businessdb.BusinessProfile{
		Name: sp("Wenhao Development Pte Ltd"), Address: sp("1 Marina Boulevard\nSingapore 018989"),
		ContactEmail: sp("accounts@wenhao.dev"), ContactPhone: sp("+65 6123 4567"),
		InvoiceTitle: sp("Invoice"), DefaultNotes: sp("Default note."),
	}
}

func accounts() []businessdb.PaymentAccount {
	return []businessdb.PaymentAccount{
		{Currency: "SGD", Instructions: "DBS 012-345678-9"},
		{Currency: "IDR", Instructions: "OCBC NISP IDR", IsDefault: true},
	}
}

func cust() *customersdb.Customer {
	return &customersdb.Customer{
		Name: "Acme Pte Ltd", BillingAddress1: sp("12 Raffles Place"), BillingCity: sp("Singapore"),
		BillingPostalCode: sp("048619"), BillingCountry: sp("Singapore"), ContactEmail: sp("ap@acme.example"),
	}
}

type invOpt func(*invoicesdb.Invoice)

// inv is _inv(): an SGD invoice with a 5% discount and 5,000 paid.
func inv(opts ...invOpt) InvoiceData {
	i := invoicesdb.Invoice{
		InvoiceNumber: sp("WD-2026-0042"), Currency: "SGD",
		Subtotal: nd("11640.00"), DiscountType: sp("PERCENT"), DiscountValue: nd("5"),
		Amount: dec("11058.00"), BalanceDue: dec("6058.00"), Status: "PARTIALLY_PAID",
		IssueDate: date(2026, 7, 23), DueDate: date(2026, 8, 22), PaymentTerms: sp("Net 30"),
		WhtType: "NONE", WhtAmount: dec("0"), WhtBooked: dec("0"),
	}
	for _, o := range opts {
		o(&i)
	}
	return InvoiceData{
		Invoice: i, Customer: cust(), Business: biz(), Accounts: accounts(),
		Lines: []invoicesdb.InvoiceLineItem{{Position: 1, Description: "Work", Quantity: dec("1"), UnitPrice: dec("11640"), Amount: dec("11640")}},
	}
}

// grossUp is _gross_up_inv's fields: IDR 10,000,000 + PPh 23 2% grossed up.
func grossUp(extra ...invOpt) []invOpt {
	return append([]invOpt{func(i *invoicesdb.Invoice) {
		i.Currency, i.Subtotal, i.DiscountType, i.DiscountValue = "IDR", nd("10000000"), nil, decimal.NullDecimal{}
		i.Amount = dec("10204082")
		i.WhtType, i.WhtRate, i.WhtGrossUp, i.WhtBase, i.WhtAmount = "PPH23", nd("2.0000"), true, nd("10000000"), dec("204082")
	}}, extra...)
}

func rowsOf(d Document) (labels, values []string) {
	for _, r := range d.Summary {
		labels, values = append(labels, r.Label), append(values, r.Value)
	}
	return
}

func eq[T any](t *testing.T, name string, got, want T) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %#v, want %#v", name, got, want)
	}
}

func TestInvoiceDocumentDiscountAndPaid(t *testing.T) {
	t.Parallel()
	d := InvoiceDocument(inv())
	eq(t, "DocTitle", d.DocTitle, "Invoice")
	eq(t, "Ref", d.Ref, "WD-2026-0042")
	eq(t, "StatusClass", d.StatusClass, "partial")
	got := map[string]string{}
	for _, r := range d.Summary {
		got[r.Label] = r.Value
	}
	eq(t, "rows", got, map[string]string{
		"Subtotal": "11,640.00", "Discount (5%)": "−582.00", "Total": "11,058.00", "Paid": "−5,000.00",
	})
	eq(t, "HeroLabel", d.HeroLabel, "Total Due")
	eq(t, "HeroAmount", d.HeroAmount, "6,058.00")
	eq(t, "PaymentInstructions", d.PaymentInstructions, "DBS 012-345678-9")
	eq(t, "Notes", d.Notes, "Default note.") // falls back to the business
	eq(t, "Terms", d.Terms, "")              // "Net N" is not customer-facing
	eq(t, "Meta", d.Meta, []MetaCell{{"Invoice No.", "WD-2026-0042"}, {"Issue Date", "23 Jul 2026"}, {"Due Date", "22 Aug 2026"}})
	eq(t, "Items", d.Items, []Item{{Desc: "Work", Qty: "1", Rate: "11,640.00", Amount: "11,640.00"}})
}

func TestInvoiceDocumentNoPaymentsOmitsPaidAndTotal(t *testing.T) {
	t.Parallel()
	d := InvoiceDocument(inv(func(i *invoicesdb.Invoice) { i.BalanceDue = dec("11058.00") }))
	labels, _ := rowsOf(d)
	if slices.Contains(labels, "Paid") || slices.Contains(labels, "Total") {
		t.Errorf("rows %v", labels)
	}
	eq(t, "HeroAmount", d.HeroAmount, "11,058.00")
}

func TestInvoiceDocumentGrossUpRows(t *testing.T) {
	t.Parallel()
	d := InvoiceDocument(inv(grossUp(func(i *invoicesdb.Invoice) { i.Status, i.BalanceDue = "OPEN", i.Amount })...))
	eq(t, "rows", d.Summary, []SummaryRow{
		{"Subtotal", "10,000,000", "subtotal"},
		{"PPh 23 (2%)", "204,082", "grossup"},
		{"Total", "10,204,082", "total"},
		{"PPh 23 withholding (2%)", "−204,082", "wht"},
	})
	eq(t, "HeroLabel", d.HeroLabel, "Amount to Transfer")
	eq(t, "HeroAmount", d.HeroAmount, "10,000,000")
}

func TestInvoiceDocumentWHTWithoutGrossUp(t *testing.T) {
	t.Parallel()
	d := InvoiceDocument(inv(func(i *invoicesdb.Invoice) {
		i.Currency, i.Subtotal, i.DiscountType, i.DiscountValue = "IDR", nd("10000000"), nil, decimal.NullDecimal{}
		i.Amount, i.BalanceDue, i.Status = dec("10000000"), dec("10000000"), "OPEN"
		i.WhtType, i.WhtRate, i.WhtBase, i.WhtAmount = "PPH23", nd("2.0000"), nd("10000000"), dec("200000")
	}))
	labels, values := rowsOf(d)
	eq(t, "labels", labels, []string{"Subtotal", "Total", "PPh 23 withholding (2%)"})
	eq(t, "values", values, []string{"10,000,000", "10,000,000", "−200,000"})
	eq(t, "HeroAmount", d.HeroAmount, "9,800,000")
}

func TestInvoiceDocumentWHTWithPartialPayment(t *testing.T) {
	t.Parallel()
	d := InvoiceDocument(inv(func(i *invoicesdb.Invoice) {
		i.Currency, i.Subtotal, i.DiscountType, i.DiscountValue = "IDR", nd("10000000"), nil, decimal.NullDecimal{}
		i.Amount, i.BalanceDue = dec("10000000"), dec("6000000")
		i.WhtType, i.WhtRate, i.WhtBase, i.WhtAmount = "PPH23", nd("2.0000"), nd("10000000"), dec("200000")
	}))
	labels, values := rowsOf(d)
	eq(t, "labels", labels, []string{"Subtotal", "Total", "PPh 23 withholding (2%)", "Paid"})
	eq(t, "Paid", values[3], "−4,000,000")
	eq(t, "HeroAmount", d.HeroAmount, "5,800,000") // expected cash
}

func TestInvoiceDocumentPaidGrossUpExcludesBookedWHT(t *testing.T) {
	t.Parallel()
	d := InvoiceDocument(inv(grossUp(func(i *invoicesdb.Invoice) {
		i.BalanceDue, i.Status, i.WhtBooked = dec("0"), "PAID", dec("204082")
	})...))
	labels, values := rowsOf(d)
	eq(t, "labels", labels[len(labels)-3:], []string{"Total", "PPh 23 withholding (2%)", "Paid"})
	eq(t, "values", values[len(values)-3:], []string{"10,204,082", "−204,082", "−10,000,000"})
	eq(t, "HeroAmount", d.HeroAmount, "0")
}

func TestInvoiceDocumentPartialGrossUpExcludesBookedWHT(t *testing.T) {
	t.Parallel()
	// 5,000,000 cash + 204,082 PPh booked against 10,204,082.
	d := InvoiceDocument(inv(grossUp(func(i *invoicesdb.Invoice) {
		i.BalanceDue, i.Status, i.WhtBooked = dec("5000000"), "PARTIALLY_PAID", dec("204082")
	})...))
	last := d.Summary[len(d.Summary)-1]
	eq(t, "last row", last.Label+" "+last.Value, "Paid −5,000,000")
	eq(t, "HeroAmount", d.HeroAmount, "5,000,000")
}

func TestInvoiceDocumentNoWHTUnchanged(t *testing.T) {
	t.Parallel()
	d := InvoiceDocument(inv())
	labels, _ := rowsOf(d)
	eq(t, "labels", labels, []string{"Subtotal", "Discount (5%)", "Total", "Paid"})
	eq(t, "HeroLabel", d.HeroLabel, "Total Due")
}

func quote() QuotationData {
	return QuotationData{
		Quotation: quotationsdb.Quotation{
			QuotationNumber: sp("Q-2026-0007"), Currency: "SGD", Subtotal: nd("5000.00"),
			Amount: dec("5000.00"), Status: "SENT", IssueDate: date(2026, 7, 23), ValidUntil: date(2026, 8, 6),
			PaymentTerms: sp("Net 14"), Notes: sp("Quote note"),
		},
		Lines:    []quotationsdb.QuotationLineItem{{Position: 1, Description: "Scope", Quantity: dec("1"), UnitPrice: dec("5000"), Amount: dec("5000")}},
		Customer: cust(), Business: biz(), Accounts: accounts(),
	}
}

func TestQuotationDocumentTotalHero(t *testing.T) {
	t.Parallel()
	d := QuotationDocument(quote())
	eq(t, "DocTitle", d.DocTitle, "Quotation")
	eq(t, "HeroLabel", d.HeroLabel, "Total")
	eq(t, "HeroAmount", d.HeroAmount, "5,000.00")
	eq(t, "Meta[2]", d.Meta[2], MetaCell{"Valid Until", "06 Aug 2026"})
	eq(t, "Notes", d.Notes, "Quote note") // the document's note wins
	eq(t, "Terms", d.Terms, "Net 14")     // a quotation shows its terms
	eq(t, "Summary", d.Summary, []SummaryRow{{"Subtotal", "5,000.00", "subtotal"}})
}

// test_both_builders_share_key_set: Go has one struct, so the check is that
// both builders fill the same fields.
func TestBothBuildersFillTheSameFields(t *testing.T) {
	t.Parallel()
	q := quote()
	q.Quotation.PaymentTerms, q.Quotation.Notes = nil, nil
	filled := func(d Document) (names []string) {
		v := reflect.ValueOf(d)
		for i := range v.NumField() {
			if !v.Field(i).IsZero() {
				names = append(names, v.Type().Field(i).Name)
			}
		}
		return names
	}
	eq(t, "filled fields", filled(QuotationDocument(q)), filled(InvoiceDocument(inv())))
}

func TestQuotationNotesFallBackToBusinessDefault(t *testing.T) {
	t.Parallel()
	q := quote()
	q.Quotation.Notes = nil
	eq(t, "Notes", QuotationDocument(q).Notes, "Default note.")
}

func TestResolvePaymentInstructions(t *testing.T) {
	t.Parallel()
	get := func(accts []businessdb.PaymentAccount, cur string) string {
		if p := ResolvePaymentInstructions(accts, cur); p != nil {
			return *p
		}
		return "<nil>"
	}
	eq(t, "matches currency", get(accounts(), "SGD"), "DBS 012-345678-9")
	eq(t, "falls back to the default", get(accounts(), "USD"), "OCBC NISP IDR") // no USD account
	eq(t, "empty", get(nil, "SGD"), "<nil>")
	eq(t, "no default", get([]businessdb.PaymentAccount{{Currency: "SGD", Instructions: "x"}}, "USD"), "<nil>")
	// Exact, case-sensitive; the first of a currency wins.
	eq(t, "case", get([]businessdb.PaymentAccount{{Currency: "sgd", Instructions: "x"}, {Currency: "SGD", Instructions: "y", IsDefault: true}}, "SGD"), "y")
	eq(t, "first", get([]businessdb.PaymentAccount{{Currency: "SGD", Instructions: "a"}, {Currency: "SGD", Instructions: "b"}}, "SGD"), "a")
}

func TestInvoiceDocumentSelectsDefaultWhenCurrencyUnmatched(t *testing.T) {
	t.Parallel()
	d := InvoiceDocument(inv(func(i *invoicesdb.Invoice) { i.Currency = "USD" }))
	eq(t, "PaymentInstructions", d.PaymentInstructions, "OCBC NISP IDR")
}

func TestBillToJoinsCustomerEmailAndPhone(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		email, phone *string
		want         string
	}{
		"both":       {sp("ap@acme.example"), sp("+65 9123 4567"), "ap@acme.example · +65 9123 4567"},
		"phone only": {nil, sp("+65 9123 4567"), "+65 9123 4567"},
		"email only": {sp("ap@acme.example"), nil, "ap@acme.example"},
	} {
		in := inv()
		in.Customer.ContactEmail, in.Customer.ContactPhone = c.email, c.phone
		lines := InvoiceDocument(in).ToLines
		eq(t, name, lines[len(lines)-1], c.want)
	}
}

func TestReceiptReceivedFromIncludesCustomerPhone(t *testing.T) {
	t.Parallel()
	c := cust()
	c.ContactPhone = sp("+65 9123 4567")
	d := ReceiptDocument(ReceiptData{
		Payment: paymentsdb.Payment{
			PaymentID: uuid.New(), Amount: dec("400"), Currency: "SGD", PayerName: "Acme Pte Ltd",
			PaymentDate: date(2026, 8, 7), IntakeSource: "MANUAL",
		},
		Invoice: inv().Invoice, Customer: c, Business: biz(),
	})
	eq(t, "last line", d.ToLines[len(d.ToLines)-1], "ap@acme.example · +65 9123 4567")
}

func TestInvoiceDocumentOmitsBlockWhenNoAccounts(t *testing.T) {
	t.Parallel()
	in := inv()
	in.Accounts = nil
	eq(t, "PaymentInstructions", InvoiceDocument(in).PaymentInstructions, "")
}

func npwpInv(opts ...invOpt) InvoiceData {
	in := inv(opts...)
	in.Business.Npwp, in.Business.NpwpName = sp("2102060605910001"), sp("Kardono Wijaya")
	return in
}

func TestInvoiceFromBlockShowsNPWPWhenWithholding(t *testing.T) {
	t.Parallel()
	d := InvoiceDocument(npwpInv(grossUp(func(i *invoicesdb.Invoice) { i.BalanceDue = dec("10204082") })...))
	eq(t, "NPWP", d.NPWP, "NPWP 2102060605910001 a.n. Kardono Wijaya")
	for _, ln := range d.FromLines {
		if len(ln) >= 4 && ln[:4] == "NPWP" {
			t.Errorf("NPWP is in the From lines: %q", ln)
		}
	}
}

func TestInvoiceFromBlockOmitsNPWPWithoutWithholding(t *testing.T) {
	t.Parallel()
	eq(t, "NPWP", InvoiceDocument(npwpInv()).NPWP, "")
}

func TestInvoiceFromBlockOmitsNPWPWhenUnset(t *testing.T) {
	t.Parallel()
	in := inv(grossUp(func(i *invoicesdb.Invoice) { i.BalanceDue = dec("10204082") })...)
	eq(t, "NPWP", InvoiceDocument(in).NPWP, "")
}

// Go change 4 (inventory §5.7): the receipt's summary values name their own
// currency. Python printed bare numbers in the invoice's places under a
// heading in the payment's currency.
func TestReceiptSummaryLabelsOwnCurrency(t *testing.T) {
	t.Parallel()
	pid := uuid.MustParse("1a2b3c4d-0000-0000-0000-000000000000")
	in := inv(func(i *invoicesdb.Invoice) {
		i.Currency, i.Amount, i.BalanceDue = "IDR", dec("16000000"), dec("6400000")
	})
	// A USD payment on an IDR invoice (a cross-currency manual approval).
	d := ReceiptDocument(ReceiptData{
		Payment: paymentsdb.Payment{
			PaymentID: pid, Amount: dec("600.5"), Currency: "USD", PayerName: "Acme",
			PayerReference: sp("INV-1"), PaymentDate: date(2026, 8, 7), IntakeSource: "EMAIL_PARSER",
		},
		Invoice: in.Invoice, Customer: cust(), Business: biz(),
	})
	eq(t, "Summary", d.Summary, []SummaryRow{
		{"Invoice total", "16,000,000 IDR", "subtotal"},
		{"Remaining balance", "6,400,000 IDR", "total"},
	})
	eq(t, "Currency", d.Currency, "USD")
	eq(t, "HeroAmount", d.HeroAmount, "600.50")
	eq(t, "HeroLabel", d.HeroLabel, "Amount Received")
	eq(t, "Items", d.Items, []Item{{Desc: "Payment for invoice WD-2026-0042", Sub: "Email Parser · Ref: INV-1", Amount: "600.50"}})
	eq(t, "Ref", d.Ref, "RCT-1A2B3C4D")
	eq(t, "Meta", d.Meta, []MetaCell{{"Receipt No.", "RCT-1A2B3C4D"}, {"Payment Date", "07 Aug 2026"}, {"Invoice No.", "WD-2026-0042"}})
	eq(t, "labels", []string{d.DocTitle, d.StatusLabel, d.StatusClass, d.FromLabel, d.ToLabel}, []string{"RECEIPT", "Received", "paid", "Received By", "Received From"})
	eq(t, "Simple", d.Simple, true)
	eq(t, "Notes", d.Notes, "Remaining balance: 6,400,000 IDR.\nThank you for your payment.")
	eq(t, "Terms", d.Terms+d.PaymentInstructions+d.NPWP, "")
}

func TestReceiptDocumentNamesAndFallbacks(t *testing.T) {
	t.Parallel()
	pay := paymentsdb.Payment{PaymentID: uuid.New(), Amount: dec("1"), Currency: "SGD", PayerName: "Payer Co", IntakeSource: "MANUAL"}
	i := inv(func(i *invoicesdb.Invoice) { i.InvoiceNumber, i.BalanceDue = nil, dec("0") }).Invoice
	d := ReceiptDocument(ReceiptData{Payment: pay, Invoice: i})
	eq(t, "ToName", d.ToName, "Payer Co") // no customer: the payer
	eq(t, "FromName", d.FromName, "—")
	eq(t, "Items", d.Items, []Item{{Desc: "Payment received", Sub: "Manual entry", Amount: "1.00"}})
	eq(t, "Meta[1:]", d.Meta[1:], []MetaCell{{"Payment Date", "—"}, {"Invoice No.", "—"}})
	eq(t, "Notes", d.Notes, "This invoice is now paid in full.\nThank you for your payment.")
	pay.PayerName = ""
	eq(t, "ToName", ReceiptDocument(ReceiptData{Payment: pay, Invoice: i}).ToName, "—")
}

// Go change 10: the PO/SO number is a fourth meta cell and the footer is
// carried, on invoices and quotations alike (the HTML design dropped both).
func TestDocumentsCarryPOSONumberAndFooter(t *testing.T) {
	t.Parallel()
	d := InvoiceDocument(inv(func(i *invoicesdb.Invoice) { i.PoSoNumber, i.Footer = sp("PO-77"), sp("Thanks.\nPay on time.") }))
	eq(t, "invoice Meta[3]", d.Meta[3], MetaCell{"PO / SO", "PO-77"})
	eq(t, "invoice len(Meta)", len(d.Meta), 4)
	eq(t, "invoice Footer", d.Footer, "Thanks.\nPay on time.")
	q := quote()
	q.Quotation.PoSoNumber, q.Quotation.Footer = sp("SO-9"), sp("Valid subject to stock.")
	qd := QuotationDocument(q)
	eq(t, "quotation Meta[3]", qd.Meta[3], MetaCell{"PO / SO", "SO-9"})
	eq(t, "quotation Footer", qd.Footer, "Valid subject to stock.")

	// Not set (or empty): three cells, no footer.
	d = InvoiceDocument(inv(func(i *invoicesdb.Invoice) { i.PoSoNumber, i.Footer = sp(""), sp("") }))
	eq(t, "empty len(Meta)", len(d.Meta), 3)
	eq(t, "empty Footer", d.Footer, "")
}

func TestDocumentWithoutCustomerOrBusiness(t *testing.T) {
	t.Parallel()
	in := inv()
	in.Customer, in.Business, in.Accounts = nil, nil, nil
	d := InvoiceDocument(in)
	eq(t, "From/To", []string{d.FromName, d.ToName, d.FootName, d.DocTitle, d.Notes}, []string{"—", "—", "", "Invoice", ""})
	eq(t, "lines", [][]string{d.FromLines, d.ToLines, d.FootMeta}, [][]string{nil, nil, nil})
}

// Python's _addr_lines: region and city joins, and the free-text address
// (blank lines kept) only when no structured part is set.
func TestCustomerAndBusinessLines(t *testing.T) {
	t.Parallel()
	c := &customersdb.Customer{
		BillingAddress1: sp("A1"), BillingAddress2: sp(""), BillingState: sp("Jakarta"), BillingPostalCode: sp("10110"),
		BillingCity: sp("Jakarta"), BillingCountry: sp("ID"), BillingAddress: sp("ignored"), ContactPhone: sp("123"),
	}
	eq(t, "structured", customerLines(c), []string{"A1", "Jakarta, 10110", "Jakarta, ID", "123"})
	eq(t, "free text", customerLines(&customersdb.Customer{BillingAddress: sp("L1\n\nL3\r\nL4\n")}), []string{"L1", "", "L3", "L4"})
	eq(t, "city only", customerLines(&customersdb.Customer{BillingCity: sp("Bali")}), []string{"Bali"})
	eq(t, "business", businessLines(&businessdb.BusinessProfile{Address: sp(" a \n\n  \nb\n"), ContactEmail: sp("e@x")}), []string{" a ", "b", "e@x"})
}

// The gold location line in the header is the last non-empty line of the
// business's free-text address; nothing else stands in for it.
func TestLocationLineIsLastAddressLine(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		addr *string
		want string
	}{
		"last line":         {sp("Jl. Raya 1\nBatam · Indonesia\n"), "Batam · Indonesia"},
		"blank lines after": {sp("Jl. Raya 1\r\nBatam · Indonesia\n  \n"), "Batam · Indonesia"},
		"one line":          {sp("  Singapore  "), "Singapore"},
		"empty":             {sp(""), ""},
		"nil":               {nil, ""},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			in := inv()
			in.Business.Address = c.addr
			eq(t, "invoice Location", InvoiceDocument(in).Location, c.want)
			q := quote()
			q.Business.Address = c.addr
			eq(t, "quotation Location", QuotationDocument(q).Location, c.want)
		})
	}
	t.Run("no business", func(t *testing.T) {
		t.Parallel()
		in := inv()
		in.Business = nil
		eq(t, "Location", InvoiceDocument(in).Location, "")
	})
}
