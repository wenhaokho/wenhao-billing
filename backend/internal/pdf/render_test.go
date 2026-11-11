package pdf

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	lpdf "github.com/ledongthuc/pdf"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices/invoicesdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/payments/paymentsdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/quotations/quotationsdb"
)

var samples = flag.String("samples", "", "write sample invoice, quotation and receipt PDFs to this directory")

// rendered is a PDF and the text of each of its pages.
type rendered struct {
	raw   []byte
	pages []string
	title string
}

// all is every page's text.
func (r rendered) all() string { return strings.Join(r.pages, "\n") }

func (r rendered) has(t *testing.T, want ...string) {
	t.Helper()
	all := r.all()
	for _, w := range want {
		if !strings.Contains(all, w) {
			t.Errorf("PDF text has no %q; text:\n%s", w, all)
		}
	}
}

// hasFold is has, ignoring case: labels print in capitals.
func (r rendered) hasFold(t *testing.T, want ...string) {
	t.Helper()
	all := strings.ToLower(r.all())
	for _, w := range want {
		if !strings.Contains(all, strings.ToLower(w)) {
			t.Errorf("PDF text has no %q (any case); text:\n%s", w, r.all())
		}
	}
}

// hasNotFold is hasNot, ignoring case.
func (r rendered) hasNotFold(t *testing.T, unwanted ...string) {
	t.Helper()
	all := strings.ToLower(r.all())
	for _, w := range unwanted {
		if strings.Contains(all, strings.ToLower(w)) {
			t.Errorf("PDF text has %q (any case)", w)
		}
	}
}

func (r rendered) hasNot(t *testing.T, unwanted ...string) {
	t.Helper()
	all := r.all()
	for _, w := range unwanted {
		if strings.Contains(all, w) {
			t.Errorf("PDF text has %q", w)
		}
	}
}

// extract reads the PDF back: its pages' text and its title.
func extract(t *testing.T, b []byte) rendered {
	t.Helper()
	if !bytes.HasPrefix(b, []byte("%PDF-")) {
		t.Fatalf("not a PDF: %q", b[:min(len(b), 16)])
	}
	r, err := lpdf.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	out := rendered{raw: b, title: r.Trailer().Key("Info").Key("Title").Text()}
	for i := 1; i <= r.NumPage(); i++ {
		s, err := r.Page(i).GetPlainText(nil)
		if err != nil {
			t.Fatal(err)
		}
		out.pages = append(out.pages, s)
	}
	return out
}

type renderFunc func(context.Context, Document) ([]byte, error)

func renderPDF(t *testing.T, f renderFunc, d Document) rendered {
	t.Helper()
	b, err := f(t.Context(), d)
	if err != nil {
		t.Fatal(err)
	}
	return extract(t, b)
}

// receipt is test_receipt_pdf.py's _make_objects: USD 400 against a USD
// 1,000 invoice.
func receipt(balance string) ReceiptData {
	i := invoicesdb.Invoice{
		InvoiceNumber: sp("INV-0100"), Currency: "USD", Amount: dec("1000.0000"), BalanceDue: dec(balance), Status: "PARTIAL",
	}
	return ReceiptData{
		Payment: paymentsdb.Payment{
			PaymentID: uuid.MustParse("1a2b3c4d-0000-4000-8000-000000000000"), Amount: dec("400.0000"), Currency: "USD",
			PayerName: "Acme Pte Ltd", PayerReference: sp("TRF-991"), PaymentDate: date(2026, 8, 7),
			IntakeSource: "MANUAL", Status: "CLEARED",
		},
		// This payment relieves 400: the balance before it is 400 more.
		Invoice: i, Customer: cust(), Business: biz(), BalanceAfter: dec(balance), BalanceBefore: dec(balance).Add(dec("400")),
	}
}

// --- test_invoice_pdf.py ---------------------------------------------------

// fullInvoice is test_invoice_pdf.py's _invoice() with its full business and
// customer.
func fullInvoice(opts ...invOpt) InvoiceData {
	in := inv(append([]invOpt{func(i *invoicesdb.Invoice) {
		i.InvoiceNumber, i.PoSoNumber, i.Currency = sp("INV-2026-0042"), sp("PO-88913"), "SGD"
		i.Subtotal, i.Amount, i.BalanceDue, i.Status = nd("6350.00"), dec("6032.50"), dec("6032.50"), "SENT"
		i.Notes = sp("Thanks!")
	}}, opts...)...)
	in.Lines = []invoicesdb.InvoiceLineItem{
		{Position: 1, Description: "Website redesign", Quantity: dec("1"), UnitPrice: dec("2500.00"), Amount: dec("2500.00")},
		{Position: 2, Description: "Frontend implementation", Quantity: dec("40"), UnitPrice: dec("85.00"), Amount: dec("3400.00")},
	}
	return in
}

func TestRenderFullInvoiceProducesPDF(t *testing.T) {
	t.Parallel()
	r := renderPDF(t, RenderInvoice, InvoiceDocument(fullInvoice()))
	if len(r.raw) <= 1000 {
		t.Errorf("PDF is %d bytes", len(r.raw))
	}
	r.has(t, "Invoice", "INV-2026-0042", "Website redesign", "Frontend implementation", "3,400.00",
		"Acme Pte Ltd", "Wenhao Development Pte Ltd", "Discount (5%)", "6,032.50", "Thanks!")
}

func TestRenderWithoutCustomerOrBusiness(t *testing.T) {
	t.Parallel()
	in := fullInvoice()
	in.Customer, in.Business, in.Accounts = nil, nil, nil
	r := renderPDF(t, RenderInvoice, InvoiceDocument(in))
	r.has(t, "Invoice", "INV-2026-0042", "Reference", "SGD")
	// No party, no empty "Bill to" box and no dash standing in for a name.
	r.hasNot(t, "Bill to", "—")
}

func TestRenderWithoutDiscount(t *testing.T) {
	t.Parallel()
	r := renderPDF(t, RenderInvoice, InvoiceDocument(fullInvoice(func(i *invoicesdb.Invoice) {
		i.DiscountType, i.DiscountValue = nil, nd("0")
		i.DiscountValue.Valid = false
	})))
	r.has(t, "Subtotal")
	r.hasNot(t, "Discount")
}

// special is text that broke reportlab's XML parser, and PDF string syntax.
const special = `& <bros> "q" (p) \b`

func TestRenderInvoicePDFEscapesXMLSpecialCharsInNames(t *testing.T) {
	t.Parallel()
	in := fullInvoice(func(i *invoicesdb.Invoice) {
		i.InvoiceNumber, i.PoSoNumber = sp("INV <draft 2026"), sp("PO <2026 & co")
		i.PaymentTerms = sp("50% upfront <br> balance")
		i.Notes = sp("Scope: design & build\n<subject to change")
		i.Footer = sp("Thanks & regards\n<see terms")
	})
	in.Business.Name, in.Business.Address = sp("Smith & Co <bros Ltd"), sp("R&D <lab, 1 Raffles Ave\nSingapore 048619")
	in.Business.ContactEmail, in.Business.InvoiceTitle = sp("ap<br>illing@smith.example"), sp("Tax <invoice")
	in.Customer.Name, in.Customer.BillingAddress1 = "Jones & Sons <holdings", sp("2 Cecil <st")
	in.Customer.ContactEmail = sp("ap<br>finance@jones.example")
	in.Lines[0].Description = "Design & build <landing page"
	r := renderPDF(t, RenderInvoice, InvoiceDocument(in))
	r.has(t, "Smith & Co <bros Ltd", "R&D <lab, 1 Raffles Ave", "ap<br>illing@smith.example", "Tax <invoice",
		"Jones & Sons <holdings", "2 Cecil <st", "ap<br>finance@jones.example", "INV <draft 2026", "PO <2026 & co",
		"Scope: design & build", "<subject to change", "Thanks & regards", "<see terms", "Design & build <landing page")
}

func TestRenderEscapesSpecialCharacters(t *testing.T) {
	t.Parallel()
	in := fullInvoice(func(i *invoicesdb.Invoice) { i.Notes = sp(special) })
	in.Customer.Name = special
	in.Lines[0].Description = special
	q := quote()
	q.Customer.Name, q.Quotation.Notes, q.Lines[0].Description = special, sp(special), special
	rc := receipt("600")
	rc.Customer, rc.Payment.PayerName, rc.Payment.PayerReference = nil, special, sp(special)
	for name, c := range map[string]struct {
		f renderFunc
		d Document
	}{
		"invoice":   {RenderInvoice, InvoiceDocument(in)},
		"quotation": {RenderQuotation, QuotationDocument(q)},
		"receipt":   {RenderReceipt, ReceiptDocument(rc)},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := renderPDF(t, c.f, c.d)
			if n := strings.Count(r.all(), special); n < 2 {
				t.Errorf("%q printed %d times, want at least 2:\n%s", special, n, r.all())
			}
		})
	}
}

func TestNotesFallBackToBusinessDefaultWhenInvoiceHasNone(t *testing.T) {
	t.Parallel()
	in := fullInvoice(func(i *invoicesdb.Invoice) { i.Notes = nil })
	in.Business.DefaultNotes = sp("Payable within 30 days.")
	renderPDF(t, RenderInvoice, InvoiceDocument(in)).has(t, "Payable within 30 days.")

	in2 := fullInvoice(func(i *invoicesdb.Invoice) { i.Notes = sp("Invoice-specific note.") })
	in2.Business.DefaultNotes = sp("Payable within 30 days.")
	r := renderPDF(t, RenderInvoice, InvoiceDocument(in2))
	r.has(t, "Invoice-specific note.")
	r.hasNot(t, "Payable within 30 days.")
}

func TestRenderWHTInvoiceHeroIsAmountToTransfer(t *testing.T) {
	t.Parallel()
	d := InvoiceDocument(inv(grossUp(func(i *invoicesdb.Invoice) { i.BalanceDue = dec("10204082") })...))
	eq(t, "HeroLabel", d.HeroLabel, "Amount to Transfer")
	r := renderPDF(t, RenderInvoice, d)
	// The design prints the hero label in capitals.
	r.has(t, "AMOUNT TO TRANSFER", "10,000,000", "PPh 23 withholding (2%)", "−204,082", "PPh 23 (2%)", "10,204,082")
	r.hasNot(t, "TOTAL DUE")
}

func TestRenderNoWHTInvoiceHeroIsTotalDue(t *testing.T) {
	t.Parallel()
	r := renderPDF(t, RenderInvoice, InvoiceDocument(fullInvoice()))
	r.has(t, "TOTAL DUE")
	r.hasNot(t, "withholding", "AMOUNT TO TRANSFER")
}

// --- test_pdf_render_html.py -----------------------------------------------

func TestInvoiceIsSelfContainedAndPopulated(t *testing.T) {
	t.Parallel()
	in := inv()
	in.Lines[0].Description = "Frontend engineering"
	r := renderPDF(t, RenderInvoice, InvoiceDocument(in))
	r.has(t, "WD-2026-0042", "TOTAL DUE", "Frontend engineering", "DBS 012-345678-9")
	// The three families are embedded TTF, and nothing points off the page.
	for _, f := range []string{"/FontFile2", "/BaseFont /utf8" + fontBody, "/BaseFont /utf8" + fontDisplay, "/BaseFont /utf8" + fontMono, "/Subtype /Image"} {
		if !bytes.Contains(r.raw, []byte(f)) {
			t.Errorf("PDF has no %s", f)
		}
	}
	for _, u := range []string{"http://", "https://", "/URI"} {
		if bytes.Contains(r.raw, []byte(u)) {
			t.Errorf("PDF has %s", u)
		}
	}
}

func TestQuotationRendersTotal(t *testing.T) {
	t.Parallel()
	q := quote()
	q.Quotation.Notes = nil
	r := renderPDF(t, RenderQuotation, QuotationDocument(q))
	r.has(t, "Quotation", "Valid until", "5,000.00", "Q-2026-0007", "Net 14")
}

func TestInvoicePutsNPWPInPaymentPanelWhenWithholding(t *testing.T) {
	t.Parallel()
	in := inv(func(i *invoicesdb.Invoice) {
		i.Currency, i.DiscountType, i.DiscountValue = "IDR", nil, nd("0")
		i.DiscountValue.Valid = false
		i.Subtotal, i.WhtBase = nd("10000000"), nd("10000000")
		i.Amount, i.BalanceDue = dec("10256410"), dec("10256410")
		i.WhtType, i.WhtRate, i.WhtGrossUp, i.WhtAmount = "PPH21", nd("2.5"), true, dec("256410")
	})
	in.Accounts[0].Currency, in.Accounts[1].Currency = "IDR", "SGD"
	in.Accounts[0].Instructions = "DBS 012-345678-9"
	in.Business.Npwp, in.Business.NpwpName = sp("2102060605910001"), sp("Kardono Wijaya")
	r := renderPDF(t, RenderInvoice, InvoiceDocument(in))
	page := r.pages[0]
	label := strings.Index(page, "PAYMENT INSTRUCTIONS")
	instr := strings.Index(page, "DBS 012-345678-9")
	npwp := strings.Index(page, "NPWP 2102060605910001 a.n. Kardono Wijaya")
	summary := strings.Index(page, "Subtotal")
	if label < 0 || label >= instr || instr >= npwp || npwp >= summary {
		t.Errorf("pay panel order: label %d, instructions %d, npwp %d, summary %d\n%s", label, instr, npwp, summary, page)
	}
}

// --- test_quotation_pdf.py -------------------------------------------------

func quotation() QuotationData {
	q := quote()
	q.Quotation.QuotationNumber, q.Quotation.Status = sp("QUO-0042"), "DRAFT"
	q.Quotation.Subtotal, q.Quotation.Amount = nd("1000.0000"), dec("1000.0000")
	q.Quotation.PaymentTerms, q.Quotation.Notes = nil, nil
	q.Lines = []quotationsdb.QuotationLineItem{{Position: 1, Description: "Website redesign", Quantity: dec("1"), UnitPrice: dec("1000.0000"), Amount: dec("1000.0000")}}
	return q
}

func TestRenderQuotationPDFReturnsPDFBytes(t *testing.T) {
	t.Parallel()
	renderPDF(t, RenderQuotation, QuotationDocument(quotation())).has(t, "Quotation", "QUO-0042", "Website redesign", "1,000.00")
}

func TestRenderQuotationPDFWithoutCustomer(t *testing.T) {
	t.Parallel()
	q := quotation()
	q.Customer = nil
	r := renderPDF(t, RenderQuotation, QuotationDocument(q))
	r.has(t, "QUO-0042", "From", "Wenhao Development Pte Ltd")
	r.hasNot(t, "Bill to", "—")
}

func TestRenderQuotationPDFEscapesXMLSpecialCharsInNames(t *testing.T) {
	t.Parallel()
	q := quotation()
	q.Customer.Name, q.Customer.BillingAddress1 = "Smith & Co <bros Ltd", sp("R&D <lab, 1 Raffles Ave")
	q.Customer.ContactEmail = sp("ap<br>illing@smith.example")
	renderPDF(t, RenderQuotation, QuotationDocument(q)).has(t, "Smith & Co <bros Ltd", "R&D <lab, 1 Raffles Ave", "ap<br>illing@smith.example")
}

func TestRenderQuotationPDFEscapesXMLSpecialCharsInFreeText(t *testing.T) {
	t.Parallel()
	q := quotation()
	q.Quotation.PaymentTerms = sp("50% upfront <br> balance")
	q.Quotation.Notes = sp("Scope: design & build\n<subject to change")
	q.Quotation.Footer = sp("Thanks & regards\n<see terms")
	renderPDF(t, RenderQuotation, QuotationDocument(q)).has(t, "50% upfront <br> balance", "Scope: design & build",
		"<subject to change", "Thanks & regards", "<see terms")
}

// --- test_receipt_pdf.py ---------------------------------------------------

func TestRenderReceiptPDFReturnsPDFBytes(t *testing.T) {
	t.Parallel()
	r := renderPDF(t, RenderReceipt, ReceiptDocument(receipt("600.0000")))
	r.has(t, "Payment Receipt", "RCT-1A2B3C4D", "Invoice", "INV-0100", "Manual entry", "TRF-991", "400.00",
		"AMOUNT RECEIVED", "1,000.00 USD", "600.00 USD", "Remaining balance: 600.00 USD.", "Thank you for your payment.",
		"Received from", "Issued by")
}

func TestRenderReceiptPDFWithoutCustomer(t *testing.T) {
	t.Parallel()
	rc := receipt("0")
	rc.Customer = nil
	r := renderPDF(t, RenderReceipt, ReceiptDocument(rc))
	r.has(t, "RCT-1A2B3C4D", "Acme Pte Ltd")
	r.hasFold(t, "Paid in full")
}

func TestRenderReceiptPDFEscapesXMLSpecialCharsInNames(t *testing.T) {
	t.Parallel()
	rc := receipt("600.0000")
	rc.Customer.Name, rc.Customer.BillingAddress1 = "Smith & Co <Pte> Ltd", sp("1 Raffles & Robinson Ave")
	rc.Payment.PayerName = "Smith & Co <Pte> Ltd"
	renderPDF(t, RenderReceipt, ReceiptDocument(rc)).has(t, "Smith & Co <Pte> Ltd", "1 Raffles & Robinson Ave")
}

// --- test_invoice_internal_note.py -----------------------------------------

func TestInternalNoteNeverReachesThePDF(t *testing.T) {
	t.Parallel()
	p := pool(t)
	f := seed(t, p)
	exec(t, p, `UPDATE invoices SET notes = 'Customer-facing note', internal_note = 'Internal: chase via WhatsApp' WHERE invoice_id = $1`, f.invoice)
	in, err := LoadInvoice(t.Context(), p, f.invoice)
	if err != nil {
		t.Fatal(err)
	}
	d := InvoiceDocument(in)
	d.LogoURL = "" // seed's logo host does not exist
	r := renderPDF(t, RenderInvoice, d)
	r.has(t, "Customer-facing note")
	r.hasNot(t, "Internal: chase via WhatsApp")
}

// --- Go additions ----------------------------------------------------------

func TestInvoiceAndQuotationShowFooterAndPOSO(t *testing.T) {
	t.Parallel()
	in := fullInvoice(func(i *invoicesdb.Invoice) { i.Footer = sp("Registered in Singapore") })
	renderPDF(t, RenderInvoice, InvoiceDocument(in)).has(t, "PO / SO", "PO-88913", "Registered in Singapore")
	q := quotation()
	q.Quotation.PoSoNumber, q.Quotation.Footer = sp("SO-7"), sp("Quote footer")
	renderPDF(t, RenderQuotation, QuotationDocument(q)).has(t, "PO / SO", "SO-7", "Quote footer")
}

func TestPdfTitleMetadata(t *testing.T) {
	t.Parallel()
	eq(t, "invoice", renderPDF(t, RenderInvoice, InvoiceDocument(fullInvoice())).title, "Invoice INV-2026-0042")
	tax := fullInvoice()
	tax.Business.InvoiceTitle = sp("tax invoice")
	eq(t, "titled invoice", renderPDF(t, RenderInvoice, InvoiceDocument(tax)).title, "Tax Invoice INV-2026-0042")
	eq(t, "quotation", renderPDF(t, RenderQuotation, QuotationDocument(quotation())).title, "Quotation QUO-0042")
	eq(t, "receipt", renderPDF(t, RenderReceipt, ReceiptDocument(receipt("0"))).title, "Payment Receipt RCT-1A2B3C4D")
}

// longInvoice has n lines whose descriptions run to three lines each.
func longInvoice(n int) InvoiceData {
	in := fullInvoice()
	in.Lines = nil
	for i := range n {
		in.Lines = append(in.Lines, invoicesdb.InvoiceLineItem{
			Position:    int32(i + 1),
			Description: fmt.Sprintf("Item %02d first\nItem %02d second\nItem %02d third", i+1, i+1, i+1),
			Quantity:    dec("1"), UnitPrice: dec(fmt.Sprintf("%d.25", 1000+i)), Amount: dec(fmt.Sprintf("%d.25", 1000+i)),
		})
	}
	return in
}

func TestLongInvoiceRepeatsHeaderAndFooter(t *testing.T) {
	t.Parallel()
	in := fullInvoice()
	for i := range 60 {
		in.Lines = append(in.Lines, invoicesdb.InvoiceLineItem{
			Position: int32(i + 3), Description: fmt.Sprintf("Line %02d", i+1), Quantity: dec("1"), UnitPrice: dec("10"), Amount: dec("10"),
		})
	}
	for name, c := range map[string]struct {
		in   InvoiceData
		want []string
	}{
		"60 short lines": {in, []string{"Line 01", "Line 60", "TOTAL DUE"}},
		"40 long lines":  {longInvoice(40), []string{"Item 01 first", "Item 40 third", "TOTAL DUE"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := renderPDF(t, RenderInvoice, InvoiceDocument(c.in))
			n := len(r.pages)
			if n < 2 {
				t.Fatalf("%d page(s), want at least 2", n)
			}
			for i, p := range r.pages {
				// The continuation strip (or page 1's band) and the footer.
				for _, w := range []string{"Invoice", "INV-2026-0042", "Wenhao Development Pte Ltd", "accounts@wenhao.dev", fmt.Sprintf("Page %d of %d", i+1, n)} {
					if !strings.Contains(p, w) {
						t.Errorf("page %d has no %q", i+1, w)
					}
				}
				// The number is in the page's top (page 1's band or the
				// continuation strip) as well as in its footer.
				if got := strings.Count(p, "INV-2026-0042"); got < 2 {
					t.Errorf("page %d has the number %d time(s), want the top and the footer", i+1, got)
				}
				// The parties band is page 1's alone.
				if got := strings.Contains(p, "Bill to"); got != (i == 0) {
					t.Errorf("page %d: has \"Bill to\" = %v", i+1, got)
				}
			}
			r.has(t, c.want...)
		})
	}
}

func TestItemRowsNeverSplit(t *testing.T) {
	t.Parallel()
	in := longInvoice(40)
	r := renderPDF(t, RenderInvoice, InvoiceDocument(in))
	if len(r.pages) < 2 {
		t.Fatalf("%d page(s), want at least 2", len(r.pages))
	}
	pageOf := func(s string) []int {
		var at []int
		for i, p := range r.pages {
			if strings.Contains(p, s) {
				at = append(at, i)
			}
		}
		return at
	}
	for i := range 40 {
		n := i + 1
		first := pageOf(fmt.Sprintf("Item %02d first", n))
		for _, part := range []string{fmt.Sprintf("Item %02d second", n), fmt.Sprintf("Item %02d third", n), fmt.Sprintf("1,%03d.25", i)} {
			if got := pageOf(part); len(first) != 1 || !slices.Equal(got, first) {
				t.Errorf("item %d: first line on pages %v, %q on %v", n, first, part, got)
			}
		}
	}
	// The table's header row starts every page that has items.
	for i, p := range r.pages {
		if strings.Contains(p, "Item ") && !strings.Contains(p, "DESCRIPTION") {
			t.Errorf("page %d has items and no table header", i+1)
		}
	}
}

func TestOnlyLogoUrlIsFetched(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		paths = append(paths, req.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(testPNG(t, 4, 3))
	}))
	t.Cleanup(srv.Close)
	in := fullInvoice(func(i *invoicesdb.Invoice) {
		i.Notes = sp("See " + srv.URL + "/notes.png")
		i.Footer = sp(srv.URL + "/footer.png")
	})
	in.Lines[0].Description = `<img src="` + srv.URL + `/item.png">`
	in.Customer.Name = "url(" + srv.URL + "/customer.png)"
	in.Business.LogoUrl = sp(srv.URL + "/logo-" + uuid.NewString() + ".png")
	r := renderPDF(t, RenderInvoice, InvoiceDocument(in))
	mu.Lock()
	defer mu.Unlock()
	eq(t, "fetched", paths, []string{strings.TrimPrefix(*in.Business.LogoUrl, srv.URL)})
	r.has(t, srv.URL+"/notes.png")
}

func TestFontsEmbedAllFamilies(t *testing.T) {
	t.Parallel()
	fonts, err := customFonts()
	if err != nil {
		t.Fatal(err)
	}
	families := map[string]int{}
	for _, f := range fonts {
		families[f.GetFamily()]++
		if !bytes.HasPrefix(f.GetBytes(), []byte{0, 1, 0, 0}) {
			t.Errorf("%s %q is not a TrueType font", f.GetFamily(), f.GetStyle())
		}
	}
	eq(t, "families", families, map[string]int{"dmsans": 2, "manrope": 2, "mono": 2})
	eq(t, "family constants", []string{fontBody, fontDisplay, fontMono}, []string{"dmsans", "manrope", "mono"})

	// A rendered PDF carries Manrope, DM Sans and JetBrains Mono, and no Inter.
	r := renderPDF(t, RenderInvoice, InvoiceDocument(inv()))
	for _, f := range []string{"manrope", "dmsans", "mono"} {
		if !bytes.Contains(r.raw, []byte("/BaseFont /utf8"+f)) {
			t.Errorf("PDF has no %s font", f)
		}
	}
	if bytes.Contains(r.raw, []byte("/BaseFont /utf8inter")) {
		t.Error("PDF still references Inter")
	}
	for _, f := range fontFiles {
		if strings.Contains(f.file, "Inter") {
			t.Errorf("Inter file %s still registered", f.file)
		}
	}
}

func TestChipColoursCoverEveryClass(t *testing.T) {
	t.Parallel()
	want := map[string][2]string{
		"paid":    {"2f6b4f", "e4efe7"},
		"sent":    {"3b5a7a", "e3e9ef"},
		"partial": {"8a5a12", "f6ecd6"},
		"overdue": {"a51c30", "f3dfe1"},
		"draft":   {"75665f", "ede7de"},
	}
	for class, w := range want {
		t.Run(class, func(t *testing.T) {
			t.Parallel()
			fg, bg := chipColours(class)
			eq(t, "fg", fg, hexColor(w[0]))
			eq(t, "bg", bg, hexColor(w[1]))
		})
	}
	t.Run("unknown", func(t *testing.T) {
		t.Parallel()
		fg, bg := chipColours("nonsense")
		dfg, dbg := chipColours("draft")
		if fg == nil || bg == nil {
			t.Fatal("unknown class returned nil")
		}
		eq(t, "fg", fg, dfg)
		eq(t, "bg", bg, dbg)
	})
}

func TestDefaultLogoIsPNG(t *testing.T) {
	t.Parallel()
	l := DefaultLogo()
	if !bytes.HasPrefix(l, []byte("\x89PNG\r\n\x1a\n")) || len(l) < 100 {
		t.Errorf("default logo is not a PNG (%d bytes)", len(l))
	}
	// Callers get a copy.
	l[0] = 0
	if DefaultLogo()[0] != 0x89 {
		t.Error("DefaultLogo returned the embedded bytes themselves")
	}
}

func TestRenderIsReproducible(t *testing.T) {
	t.Parallel()
	for name, d := range map[string]Document{
		"long invoice": InvoiceDocument(longInvoice(30)),
		"receipt":      ReceiptDocument(receipt("600")),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			a, err := RenderInvoice(t.Context(), d)
			if err != nil {
				t.Fatal(err)
			}
			b, err := RenderInvoice(t.Context(), d)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(a, b) {
				t.Errorf("two renders differ (%d and %d bytes)", len(a), len(b))
			}
		})
	}
	dateOf := func(b []byte) string {
		r, err := lpdf.NewReader(bytes.NewReader(b), int64(len(b)))
		if err != nil {
			t.Fatal(err)
		}
		return r.Trailer().Key("Info").Key("CreationDate").Text()
	}
	b, err := RenderInvoice(t.Context(), InvoiceDocument(fullInvoice()))
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "issue date", dateOf(b), "D:20260723000000")
	b, err = RenderReceipt(t.Context(), Document{DocTitle: "RECEIPT", Ref: "RCT-1"})
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "no date", dateOf(b), "D:20000101000000")
}

// TestWriteSamples writes the owner's review PDFs: go test -run
// TestWriteSamples -args -samples=DIR.
//
// It is a utility, not a contract test: without a -samples directory it has
// nothing to do and passes (it never skips, so the project's check script
// does not see a skipped test).
func TestWriteSamples(t *testing.T) {
	t.Parallel()
	if *samples == "" {
		return
	}
	// IDR, PPh 23 grossed up, 4,000,000 paid.
	in := inv(grossUp(func(i *invoicesdb.Invoice) {
		i.InvoiceNumber, i.PoSoNumber, i.Status = sp("WH20260042"), sp("PO-88913"), "PARTIALLY_PAID"
		i.BalanceDue = dec("6204082")
		i.Notes = sp("Thank you for your business.\nPlease quote the invoice number with your transfer.")
		i.Footer = sp("Wenhao Development · NPWP registered")
	})...)
	in.Business.Npwp, in.Business.NpwpName = sp("2102060605910001"), sp("Kardono Wijaya")
	in.Accounts[1].Instructions = "Bank OCBC NISP\nAccount 5458 0000 1234\na.n. PT Wenhao Development"
	in.Lines = []invoicesdb.InvoiceLineItem{
		{Position: 1, Description: "Backend platform development\nMilestone 2: payments and reconciliation", Quantity: dec("1"), UnitPrice: dec("7500000"), Amount: dec("7500000")},
		{Position: 2, Description: "Hosting and maintenance (Nov 2026)", Quantity: dec("1"), UnitPrice: dec("2500000"), Amount: dec("2500000")},
	}
	q := quote()
	q.Quotation.PoSoNumber = sp("SO-2026-11")
	q.Lines = append(q.Lines, quotationsdb.QuotationLineItem{Position: 2, Description: "Design system", Quantity: dec("2.5"), UnitPrice: dec("800"), Amount: dec("2000")})
	q.Quotation.Subtotal, q.Quotation.Amount = nd("7000.00"), dec("7000.00")
	q.Lines[0].Amount, q.Lines[0].UnitPrice = dec("5000"), dec("5000")
	rc := receipt("600.0000")
	for name, f := range map[string]func() ([]byte, error){
		"invoice.pdf":   func() ([]byte, error) { return RenderInvoice(t.Context(), InvoiceDocument(in)) },
		"quotation.pdf": func() ([]byte, error) { return RenderQuotation(t.Context(), QuotationDocument(q)) },
		"receipt.pdf":   func() ([]byte, error) { return RenderReceipt(t.Context(), ReceiptDocument(rc)) },
		"receipt-idr-pph.pdf": func() ([]byte, error) {
			r := idrReceipt("0")
			r.Business.Address = sp("Lily Garden Residence, Bromelia No. 22\nBatam · Indonesia")
			return RenderReceipt(t.Context(), ReceiptDocument(r))
		},
		"receipt-partial.pdf": func() ([]byte, error) { return RenderReceipt(t.Context(), ReceiptDocument(idrReceipt("5588697"))) },
		"receipt-second.pdf": func() ([]byte, error) {
			// The second of three: 2,000,000 paid before, 3,588,697 left after.
			r := idrReceipt("3588697")
			r.Payment.PaymentDate = date(2026, 11, 20)
			return RenderReceipt(t.Context(), ReceiptDocument(r))
		},
		"receipt-cross.pdf": func() ([]byte, error) {
			return RenderReceipt(t.Context(), ReceiptDocument(crossReceipt()))
		},
		"long.pdf": func() ([]byte, error) { return RenderInvoice(t.Context(), InvoiceDocument(longInvoice(40))) },
		"invoice-sgd.pdf": func() ([]byte, error) {
			return RenderInvoice(t.Context(), InvoiceDocument(fullInvoice(func(i *invoicesdb.Invoice) { i.Footer = sp("Registered in Singapore") })))
		},
	} {
		b, err := f()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(*samples, name), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// --- brand layout ----------------------------------------------------------

func TestInvoiceRowsAreNumbered(t *testing.T) {
	t.Parallel()
	page := renderPDF(t, RenderInvoice, InvoiceDocument(fullInvoice())).pages[0]
	for no, desc := range map[string]string{"01": "Website redesign", "02": "Frontend implementation"} {
		at := strings.Index(page, desc)
		if at < 0 {
			t.Fatalf("no %q on page 1:\n%s", desc, page)
		}
		if before := strings.TrimSpace(page[:at]); !strings.HasSuffix(before, no) {
			t.Errorf("%q is not preceded by %q: ...%q", desc, no, before[max(len(before)-20, 0):])
		}
	}
}

func TestTitlesPrintAsTyped(t *testing.T) {
	t.Parallel()
	def := fullInvoice()
	def.Business.InvoiceTitle = nil
	r := renderPDF(t, RenderInvoice, InvoiceDocument(def))
	r.has(t, "Invoice")
	r.hasNot(t, "INVOICE")

	tax := fullInvoice()
	tax.Business.InvoiceTitle = sp("Tax Invoice")
	r = renderPDF(t, RenderInvoice, InvoiceDocument(tax))
	r.has(t, "Tax Invoice")
	r.hasNot(t, "TAX INVOICE")

	r = renderPDF(t, RenderQuotation, QuotationDocument(quotation()))
	r.has(t, "Quotation")
	r.hasNot(t, "QUOTATION")
}

func TestLocationLinePrintsUnderBusinessName(t *testing.T) {
	t.Parallel()
	in := fullInvoice()
	in.Business.Address = sp("Jl. Raya 1\nBatam · Indonesia\n")
	renderPDF(t, RenderInvoice, InvoiceDocument(in)).has(t, "BATAM · INDONESIA")

	// No address: no location line, and nothing else (the contact line the
	// From column ends with) stands in for it.
	none := fullInvoice()
	none.Business.Address = nil
	r := renderPDF(t, RenderInvoice, InvoiceDocument(none))
	r.has(t, "Wenhao Development Pte Ltd", "accounts@wenhao.dev")
	r.hasNot(t, "ACCOUNTS@WENHAO.DEV")
}

func TestLongNamesWrapInsideColumns(t *testing.T) {
	t.Parallel()
	t.Run("unbroken token", func(t *testing.T) {
		t.Parallel()
		longUnbrokenTokenBreaksInsideItsColumn(t)
	})
	var name, addr []string
	for i := range 12 {
		name = append(name, fmt.Sprintf("Name%02dwxyz", i)) // 12 x 10 + 11 spaces = 131 chars
	}
	for i := range 22 {
		addr = append(addr, fmt.Sprintf("Addr%02dwxyz", i)) // 22 x 10 + 21 spaces = 241 chars
	}
	in := fullInvoice()
	in.Customer.Name = strings.Join(name, " ")
	in.Customer.BillingAddress1 = sp(strings.Join(addr, " "))
	if len(in.Customer.Name) < 120 || len(*in.Customer.BillingAddress1) < 200 {
		t.Fatalf("fixture too short: %d, %d", len(in.Customer.Name), len(*in.Customer.BillingAddress1))
	}
	r := renderPDF(t, RenderInvoice, InvoiceDocument(in))
	r.has(t, append(name, addr...)...)
}

// The header's status chip takes its colours from the status's class.
func TestStatusChipUsesItsClassColours(t *testing.T) {
	t.Parallel()
	check := func(t *testing.T, d Document, status, class string) {
		t.Helper()
		c := statusChip(d)
		fg, bg := chipColours(class)
		eq(t, "fg", c.fg, fg)
		eq(t, "bg", c.bg, bg)
		eq(t, "label", c.label, strings.ToUpper(StatusLabel(status)))
	}
	for status, class := range map[string]string{
		"DRAFT": "draft", "OPEN": "sent", "SENT": "sent", "PAID": "paid", "PARTIALLY_PAID": "partial",
		"PARTIAL": "partial", "AWAITING_FINALIZATION": "partial", "OVERDUE": "overdue", "VOID": "draft",
	} {
		t.Run("invoice "+status, func(t *testing.T) {
			t.Parallel()
			check(t, InvoiceDocument(fullInvoice(func(i *invoicesdb.Invoice) { i.Status = status })), status, class)
		})
	}
	for status, class := range map[string]string{
		"DRAFT": "draft", "SENT": "sent", "ACCEPTED": "paid", "DECLINED": "overdue", "EXPIRED": "overdue",
		"INVOICED": "sent", "VOID": "draft",
	} {
		t.Run("quotation "+status, func(t *testing.T) {
			t.Parallel()
			q := quotation()
			q.Quotation.Status = status
			check(t, QuotationDocument(q), status, class)
		})
	}
	t.Run("receipt", func(t *testing.T) {
		t.Parallel()
		check(t, ReceiptDocument(receipt("0")), "RECEIVED", "paid")
	})
}

// --- receipt ---------------------------------------------------------------

// idrReceipt is an IDR 4,523,077 payment on the grossed-up PPh 23 invoice
// (IDR 10,204,082), the payer withholding 92,308.
func idrReceipt(balance string) ReceiptData {
	in := inv(grossUp(func(i *invoicesdb.Invoice) {
		i.InvoiceNumber, i.BalanceDue, i.Status = sp("WH20260042"), dec(balance), "PARTIALLY_PAID"
	})...)
	return ReceiptData{
		Payment: paymentsdb.Payment{
			PaymentID: uuid.MustParse("5e6f7a8b-0000-4000-8000-000000000000"), Amount: dec("4523077"), Currency: "IDR",
			PayerName: "PT Citra Beton", PayerReference: sp("BCA 8520198131"), PaymentDate: date(2026, 11, 9),
			IntakeSource: "MANUAL", Status: "CLEARED", WhtAmount: dec("92308"),
		},
		// This payment relieves 4,615,385 (cash and withholding).
		Invoice: in.Invoice, Customer: cust(), Business: biz(),
		BalanceAfter: dec(balance), BalanceBefore: dec(balance).Add(dec("4615385")),
	}
}

// crossReceipt is an SGD 2,000 payment that settles the remaining IDR
// 1,600,000 of an IDR 3,600,000 invoice: a cross-currency payment clears the
// invoice to 0.
func crossReceipt() ReceiptData {
	r := idrReceipt("0")
	r.Invoice.Amount, r.Invoice.WhtType, r.Invoice.WhtAmount = dec("3600000"), "NONE", dec("0")
	r.Payment.Amount, r.Payment.Currency, r.Payment.WhtAmount = dec("2000"), "SGD", dec("0")
	r.BalanceBefore = dec("1600000")
	return r
}

func TestReceiptLayout(t *testing.T) {
	t.Parallel()
	d := ReceiptDocument(receipt("600.0000"))
	eq(t, "chip", statusChip(d).label, "RECEIVED")
	eq(t, "chip fg", statusChip(d).fg, cPaidFg)
	r := renderPDF(t, RenderReceipt, d)
	r.has(t, "Payment Receipt", "RCT-1A2B3C4D", "RECEIVED", "USD 400.00",
		"Four hundred US dollars", "INV-0100", "Issued by", "Received from",
		"Method", "Manual entry", "Bank ref.", "TRF-991")
	r.hasFold(t, "Amount received", "Applied to", "Balance due")
	r.hasNot(t, "RECEIPT ", "Description")
}

// The bank reference is printed only when the payment stores one.
func TestReceiptBankReferenceOnlyWhenStored(t *testing.T) {
	t.Parallel()
	rc := receipt("600.0000")
	rc.Payment.PayerReference = nil
	renderPDF(t, RenderReceipt, ReceiptDocument(rc)).hasNot(t, "Bank ref.")
}

func TestReceiptShowsWithholdingOnlyWhenPaymentHasIt(t *testing.T) {
	t.Parallel()
	r := renderPDF(t, RenderReceipt, ReceiptDocument(idrReceipt("5588697")))
	r.has(t, "of which PPh 23 (2%) withheld", "92,308 IDR", "PPh 23 (2%) of 92,308 IDR was withheld",
		"bukti potong", "accounts@wenhao.dev",
		"IDR 4,523,077", "Four million five hundred twenty-three thousand seventy-seven rupiah",
		// This payment is the cash and the withholding it settles.
		"4,615,385")
	r.hasFold(t, "Withholding tax (PPh 23)")

	none := idrReceipt("5588697")
	none.Payment.WhtAmount = dec("0")
	none.BalanceBefore = dec("10204082")
	r = renderPDF(t, RenderReceipt, ReceiptDocument(none))
	r.hasNot(t, "withheld", "92,308", "bukti potong", "4,615,385")
	r.has(t, "4,523,077")
}

// No rate on the invoice (or 0): the PPh label has none, never "(0%)".
func TestReceiptWithholdingWithoutRate(t *testing.T) {
	t.Parallel()
	for name, rate := range map[string]decimal.NullDecimal{"absent": {}, "zero": nd("0")} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			rc := idrReceipt("5588697")
			rc.Invoice.WhtRate = rate
			r := renderPDF(t, RenderReceipt, ReceiptDocument(rc))
			r.has(t, "of which PPh 23 withheld", "PPh 23 of 92,308 IDR was withheld")
			r.hasNot(t, "(0%)", "()")
		})
	}
}

// "Paid in full" prints once: the pill at a zero balance, else the balance
// note.
func TestReceiptPaidPillOnlyAtZeroBalance(t *testing.T) {
	t.Parallel()
	paid := renderPDF(t, RenderReceipt, ReceiptDocument(receipt("0")))
	paid.has(t, "USD 0.00")
	paid.hasFold(t, "Paid in full")
	if n := strings.Count(strings.ToLower(paid.all()), "paid in full"); n != 1 {
		t.Errorf("paid in full printed %d times, want once (the pill)", n)
	}
	paid.hasNot(t, "Remaining balance")

	open := renderPDF(t, RenderReceipt, ReceiptDocument(receipt("600.0000")))
	open.hasNotFold(t, "Paid in full")
	open.has(t, "Remaining balance: 600.00 USD.", "USD 600.00")
}

// summaryValue is the number in the box row labelled label, as a decimal
// (its sign, minus sign and currency code dropped); ok false without one.
func summaryValue(d Document, label string) (v decimal.Decimal, ok bool) {
	for _, r := range d.Summary {
		if r.Label == label {
			f := strings.Fields(strings.ReplaceAll(strings.TrimPrefix(r.Value, Minus), ",", ""))
			return decimal.RequireFromString(f[0]), true
		}
	}
	return decimal.Zero, false
}

// The box of a receipt after an earlier payment adds up: Invoice total −
// Previously paid − This payment = Balance due.
func TestReceiptBoxReconciles(t *testing.T) {
	t.Parallel()
	for name, rc := range map[string]ReceiptData{
		"second of two, paid in full": idrReceipt("0"),
		"second of three":             idrReceipt("3588697"),
		"first":                       idrReceipt("5588697"),
		"usd, earlier payment":        receipt("0"),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			d := ReceiptDocument(rc)
			total, _ := summaryValue(d, "Invoice total")
			prev, hasPrev := summaryValue(d, "Previously paid")
			this, _ := summaryValue(d, "This payment")
			if got := total.Sub(prev).Sub(this); !got.Equal(rc.BalanceAfter) {
				t.Errorf("%s − %s − %s = %s, but the balance due is %s; rows %v", total, prev, this, got, rc.BalanceAfter, d.Summary)
			}
			eq(t, "previously paid shown", hasPrev, !rc.Invoice.Amount.Equal(rc.BalanceBefore))
		})
	}
	r := renderPDF(t, RenderReceipt, ReceiptDocument(idrReceipt("0")))
	r.has(t, "Previously paid", Minus+"5,588,697 IDR", "This payment", Minus+"4,615,385 IDR", "IDR 0")
}

// A cross-currency payment settles the invoice: the box shows the balance
// before it in the invoice's currency and the amount received in the
// payment's, never a converted figure.
func TestCrossCurrencyReceiptShowsBothCurrencies(t *testing.T) {
	t.Parallel()
	d := ReceiptDocument(crossReceipt())
	eq(t, "Summary", d.Summary, []SummaryRow{
		{"Balance before this payment", "1,600,000 IDR", "subtotal"},
		{"Amount received", "2,000.00 SGD", "total"},
		{"Received in SGD, settles the balance", "", "note"},
	})
	r := renderPDF(t, RenderReceipt, d)
	r.has(t, "SGD 2,000.00", "Two thousand Singapore dollars", "3,600,000", "1,600,000 IDR", "IDR 0",
		"settles the balance")
	r.hasFold(t, "Invoice total · IDR", "This payment · SGD", "Paid in full")
	// Nothing converted: no SGD figure in IDR, no IDR figure in SGD.
	r.hasNot(t, "2,000 IDR", "IDR 2,000", "3,600,000.00", "SGD 3,600,000", "SGD 1,600,000", "1,600,000.00",
		"Previously paid")
}

// The footer's contact line wraps inside the space the footer reserves:
// nothing prints below the page, and every word of it is printed.
func TestLongFooterContactsStayOnThePage(t *testing.T) {
	t.Parallel()
	in := fullInvoice()
	in.Business.Name = sp("Wenhao Development and Consulting Services International Holdings Pte Ltd")
	in.Business.ContactEmail = sp("accounts.receivable.department@wenhao-development-consulting.example")
	in.Business.ContactPhone = sp("+65 6123 4567 ext 8910 / +62 821 2155 2288 / +60 12 345 6789")
	b, err := RenderInvoice(t.Context(), InvoiceDocument(in))
	if err != nil {
		t.Fatal(err)
	}
	r := extract(t, b)
	r.has(t, "+60 12 345 6789", "Page 1 of 1")
	lowest := lowestText(t, b)
	// A4 is 841.89 pt tall; y counts up from the page's bottom edge.
	if lowest < 2 {
		t.Errorf("text prints %.1f pt from the page bottom: off the page", lowest)
	}
}

// lowestText is the smallest baseline y, in pt from the bottom, of any text
// on any page.
func lowestText(t *testing.T, b []byte) float64 {
	t.Helper()
	r, err := lpdf.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	low := 1e9
	for i := 1; i <= r.NumPage(); i++ {
		for _, tx := range r.Page(i).Content().Text {
			low = min(low, tx.Y)
		}
	}
	return low
}

// textRun is one line of text as the PDF draws it: where it starts, in pt
// (x from the left, y up from the bottom), and its characters.
type textRun struct {
	x, y float64
	s    string
}

// textRuns is page's lines of text. The reader gives each character the
// position of its line, so consecutive characters at one position are one
// line.
func textRuns(t *testing.T, b []byte, page int) []textRun {
	t.Helper()
	r, err := lpdf.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	var out []textRun
	for _, tx := range r.Page(page).Content().Text {
		if n := len(out); n > 0 && out[n-1].x == tx.X && out[n-1].y == tx.Y {
			out[n-1].s += tx.S
			continue
		}
		out = append(out, textRun{tx.X, tx.Y, tx.S})
	}
	return out
}

const ptPerMM = 72 / 25.4

// A word too long for its column (an email, a reference) breaks inside the
// column instead of running past it (TestLongNamesWrapInsideColumns).
func longUnbrokenTokenBreaksInsideItsColumn(t *testing.T) {
	t.Helper()
	po := "PO-" + strings.Repeat("0123456789", 6)[:57] // 60 characters, no space
	email := "accounts.payable.department.of.acme@very-long-subsidiary.example"
	in := fullInvoice(func(i *invoicesdb.Invoice) { i.PoSoNumber = sp(po) })
	in.Customer.ContactEmail = sp(email)
	if len(po) != 60 || len(email) < 60 {
		t.Fatalf("fixture: %d, %d", len(po), len(email))
	}
	b, err := RenderInvoice(t.Context(), InvoiceDocument(in))
	if err != nil {
		t.Fatal(err)
	}
	runs := textRuns(t, b, 1)
	// The PO number is in the Reference column, the last: it ends at the
	// content's right edge. Its pieces are printed in DM Sans 8 pt.
	edge := float64(padX+contentW) * ptPerMM
	var pieces string
	for _, r := range runs {
		if len(r.s) < 4 || !strings.Contains(po, r.s) {
			continue
		}
		pieces += r.s
		if right := r.x + textWidth(style(fontBody, false, 8, nil), r.s)*ptPerMM; right > edge+0.5 {
			t.Errorf("%q ends at %.1f pt, past the column edge %.1f pt", r.s, right, edge)
		}
	}
	eq(t, "PO pieces", pieces, po)
	// The email in Bill to (the middle column) breaks too, and every
	// character is printed.
	mid := float64(padX+53+8+52) * ptPerMM
	var mail string
	for _, r := range runs {
		if len(r.s) < 4 || !strings.Contains(email, r.s) {
			continue
		}
		mail += r.s
		if right := r.x + textWidth(style(fontBody, false, 8, nil), r.s)*ptPerMM; right > mid+0.5 {
			t.Errorf("%q ends at %.1f pt, past the column edge %.1f pt", r.s, right, mid)
		}
	}
	eq(t, "email pieces", mail, email)
}

func TestWrapText(t *testing.T) {
	t.Parallel()
	st := style(fontBody, false, 9, nil)
	w := func(s string) float64 { return textWidth(st, s) }
	for name, c := range map[string]struct {
		s     string
		width float64
		want  []string
	}{
		"fits":                 {"alpha beta", w("alpha beta"), []string{"alpha beta"}},
		"exact fit of a line":  {"alpha beta gamma", w("alpha beta"), []string{"alpha beta", "gamma"}},
		"breaks at the space":  {"alpha beta gamma", w("alpha beta") - 0.1, []string{"alpha", "beta", "gamma"}},
		"double spaces":        {"alpha  beta  gamma", w("alpha beta"), []string{"alpha beta", "gamma"}},
		"blank":                {"   ", 10, []string{"   "}},
		"long word breaks":     {"abcdefghij", w("abcde"), []string{"abcde", "fghij"}},
		"word then long word":  {"ab abcdefghij", w("abcde"), []string{"ab", "abcde", "fghij"}},
		"no width keeps text":  {"alpha beta", 0, []string{"alpha beta"}},
		"narrower than a char": {"abc", 0.01, []string{"a", "b", "c"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			eq(t, "lines", wrapText(st, c.s, c.width), c.want)
		})
	}
}
