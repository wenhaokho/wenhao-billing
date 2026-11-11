package reports_test

import (
	"net/http"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/apitest"
	"github.com/wenhaokho/wenhao-billing/backend/internal/bills"
	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices"
	"github.com/wenhaokho/wenhao-billing/backend/internal/payments"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/internal/reports"
)

// Plan 12 Task 6 (PORT-NOTES 162–165): the reports read the accrual
// postings right. Every book here is built through the HTTP routes:
// invoices issue at finalize, payments relieve AR at the issue basis, bills
// post at open and bill payments relieve AP.
//
// Rates USD→IDR: 15000 on 2025-01-01, 16000 on 2025-02-01, 14000 on
// 2025-03-01, and 17000 on accrualAsOf (the revaluation rate). Every
// document is issued on 2025-01-01, so its issue entry is posted at
// 2025-01-01 00:00, and every payment is dated accrualPaid, so it is posted
// at accrualPaid 00:00 (PORT-NOTES 167). The void's reversal is a new event,
// posted at the database's now (2026), so the whole book's reports are as of
// accrualAsOf, after it; TestTrialBalanceBalancesAfterAccrualFlow also reads
// the book as of accrualPaid, before the void. accrualAsOf is the only date
// with the revaluation rate.
const (
	accrualIssue  = "2025-01-01"
	accrualPaid   = "2025-02-01"
	accrualPaidQ  = "?as_of=" + accrualPaid
	accrualAsOf   = "2030-01-01"
	accrualAsOfQ  = "?as_of=" + accrualAsOf
	accrualRevalQ = "17000"
)

func accrualApp(t *testing.T) *apitest.App {
	t.Helper()
	cfg := platform.Config{BaseCurrency: "IDR"}
	svc := payments.Service{BaseCurrency: cfg.BaseCurrency, Hosting: payments.NoHosting{}}
	a := apitest.New(t, func(api huma.API, d apitest.Deps) {
		reports.Register(api, d.Pool, cfg, d.Clock)
		invoices.Register(api, d.Pool, d.Clock, cfg.BaseCurrency, nil)
		payments.Register(api, d.Pool, svc, d.Config, d.Clock, payments.ReceiptDeps{Sender: d.Mail, Renderer: payments.StubRenderer{}})
		bills.Register(api, d.Pool, d.Clock, cfg.BaseCurrency)
	})
	exec(t, a, `INSERT INTO fx_rates (from_currency, to_currency, rate, as_of_date, source) VALUES
('USD', 'IDR', 15000, '2025-01-01', 'TEST'), ('USD', 'IDR', 16000, '2025-02-01', 'TEST'),
('USD', 'IDR', 14000, '2025-03-01', 'TEST'), ('USD', 'IDR', `+accrualRevalQ+`, '`+accrualAsOf+`', 'TEST')`)
	return a
}

// issueInvoice creates a DRAFT invoice of one line through the API and
// finalizes it.
func issueInvoice(t *testing.T, a *apitest.App, customer uuid.UUID, cur, typ, price string) invoices.InvoiceOut {
	t.Helper()
	inv := apitest.Decode[invoices.InvoiceOut](t, a.Do(t, http.MethodPost, "/api/v1/invoices", map[string]any{
		"customer_id": customer, "currency": cur, "invoice_type": typ, "issue_date": accrualIssue, "wht_type": "NONE",
		"line_items": []any{map[string]any{"description": "Work", "quantity": "1", "unit_price": price}},
	}), http.StatusCreated)
	out := apitest.Decode[invoices.InvoiceOut](t, a.Do(t, http.MethodPost, "/api/v1/invoices/"+inv.InvoiceID.String()+"/finalize", nil), http.StatusOK)
	if out.Status != "OPEN" {
		t.Fatalf("finalized invoice status %s, want OPEN", out.Status)
	}
	return out
}

func payInvoice(t *testing.T, a *apitest.App, id uuid.UUID, amount string) {
	t.Helper()
	apitest.Decode[payments.PaymentOut](t, a.Do(t, http.MethodPost, "/api/v1/invoices/"+id.String()+"/record-payment",
		map[string]any{"amount": amount, "payment_date": accrualPaid, "payer_name": "Acme"}), http.StatusCreated)
}

func getInvoice(t *testing.T, a *apitest.App, id uuid.UUID) invoices.InvoiceOut {
	t.Helper()
	return apitest.Decode[invoices.InvoiceOut](t, a.Do(t, http.MethodGet, "/api/v1/invoices/"+id.String(), nil), http.StatusOK)
}

// openBill creates a bill of one line through the API and opens it.
func openBill(t *testing.T, a *apitest.App, vendor uuid.UUID, cur, price string) bills.BillOut {
	t.Helper()
	b := apitest.Decode[bills.BillOut](t, a.Do(t, http.MethodPost, "/api/v1/bills", map[string]any{
		"vendor_id": vendor, "currency": cur, "issue_date": accrualIssue,
		"line_items": []any{map[string]any{"description": "Hosting", "quantity": 1, "unit_price": price}},
	}), http.StatusCreated)
	out := apitest.Decode[bills.BillOut](t, a.Do(t, http.MethodPatch, "/api/v1/bills/"+b.BillID.String(), map[string]any{"status": "OPEN"}), http.StatusOK)
	if out.Status != "OPEN" {
		t.Fatalf("opened bill status %s, want OPEN", out.Status)
	}
	return out
}

func getBill(t *testing.T, a *apitest.App, id uuid.UUID) bills.BillOut {
	t.Helper()
	return apitest.Decode[bills.BillOut](t, a.Do(t, http.MethodGet, "/api/v1/bills/"+id.String(), nil), http.StatusOK)
}

// accrualBook is the brief's book:
//   - idrHalf: IDR 1,000,000 MILESTONE (4000), paid 500,000;
//   - usdPaid: USD 1000 RECURRING (4100) issued at 15000, paid in full at
//     16000 (4900 gain 1,000,000);
//   - usdOpen: USD 500 MILESTONE (4000) issued at 15000, open;
//   - idrBill: IDR 2,000,000 bill (6000), paid;
//   - usdBill: USD 200 bill (6000) opened at 15000, open;
//   - voided: IDR 300,000 MILESTONE (4000), finalized then voided.
type accrualBook struct {
	idrHalf, usdPaid, usdOpen, voided uuid.UUID
	idrBill, usdBill                  uuid.UUID
}

func buildAccrualBook(t *testing.T, a *apitest.App) accrualBook {
	t.Helper()
	c, v := newCustomer(t, a, "Acme"), newVendor(t, a, "Supplier")
	var b accrualBook
	b.idrHalf = issueInvoice(t, a, c, "IDR", "MILESTONE", "1000000").InvoiceID
	payInvoice(t, a, b.idrHalf, "500000")
	b.usdPaid = issueInvoice(t, a, c, "USD", "RECURRING", "1000").InvoiceID
	payInvoice(t, a, b.usdPaid, "1000")
	b.usdOpen = issueInvoice(t, a, c, "USD", "MILESTONE", "500").InvoiceID
	b.voided = issueInvoice(t, a, c, "IDR", "MILESTONE", "300000").InvoiceID
	apitest.Decode[invoices.InvoiceOut](t, a.Do(t, http.MethodPost, "/api/v1/invoices/"+b.voided.String()+"/void",
		map[string]any{"reason": "mistake"}), http.StatusOK)

	b.idrBill = openBill(t, a, v, "IDR", "2000000").BillID
	apitest.Decode[bills.BillOut](t, a.Do(t, http.MethodPost, "/api/v1/bills/"+b.idrBill.String()+"/record-payment",
		map[string]any{"amount": 2000000, "payment_date": accrualPaid}), http.StatusOK)
	b.usdBill = openBill(t, a, v, "USD", "200").BillID

	for id, want := range map[uuid.UUID]string{b.idrHalf: "PARTIAL", b.usdPaid: "PAID", b.usdOpen: "OPEN", b.voided: "VOID"} {
		if got := getInvoice(t, a, id).Status; got != want {
			t.Fatalf("invoice %s status %s, want %s", id, got, want)
		}
	}
	for id, want := range map[uuid.UUID]string{b.idrBill: "PAID", b.usdBill: "OPEN"} {
		if got := getBill(t, a, id).Status; got != want {
			t.Fatalf("bill %s status %s, want %s", id, got, want)
		}
	}
	return b
}

// tbFind is the trial balance row of code in cur, or nil.
func tbFind(r tbReport, code, cur string) *tbRow {
	for i := range r.Rows {
		if r.Rows[i].Code == code && r.Rows[i].Currency == cur {
			return &r.Rows[i]
		}
	}
	return nil
}

func bsFind(r bsReport, code string) *bsLine {
	for _, g := range r.Groups {
		for i := range g.Lines {
			if g.Lines[i].Code == code {
				return &g.Lines[i]
			}
		}
	}
	return nil
}

func bsGroupOf(r bsReport, code string) string {
	for _, g := range r.Groups {
		for _, l := range g.Lines {
			if l.Code == code {
				return g.Type
			}
		}
	}
	return ""
}

func wantEq(t *testing.T, what, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s = %s, want %s", what, got, want)
	}
}

func TestTrialBalanceBalancesAfterAccrualFlow(t *testing.T) {
	t.Parallel()
	a := accrualApp(t)
	b := buildAccrualBook(t, a)
	r := tb(t, a, accrualAsOfQ)

	if r.TotalDebitBase != r.TotalCreditBase {
		t.Errorf("Σ base debit %s ≠ Σ base credit %s", r.TotalDebitBase, r.TotalCreditBase)
	}
	wantEq(t, "Σ base debit", r.TotalDebitBase, "47600000.0000")

	// 1100 IDR face is the IDR open balance (the voided invoice nets to 0).
	idrOpen := getInvoice(t, a, b.idrHalf).BalanceDue.Decimal().StringFixed(4)
	wantEq(t, "IDR open balance", idrOpen, "500000.0000")
	ar := tbFind(r, "1100", "IDR")
	if ar == nil {
		t.Fatalf("no 1100 IDR row: %+v", r.Rows)
	}
	wantEq(t, "1100 IDR net", ar.Net, idrOpen)
	wantEq(t, "1100 IDR base net", ar.BaseNet, idrOpen)

	// Revenue: issued revenue less the voided invoice.
	rev4000 := tbFind(r, "4000", "IDR")
	rev4000USD := tbFind(r, "4000", "USD")
	rev4100 := tbFind(r, "4100", "USD")
	if rev4000 == nil || rev4000USD == nil || rev4100 == nil {
		t.Fatalf("missing revenue rows: %+v", r.Rows)
	}
	// 4000 IDR: 1,000,000 + 300,000 issued, 300,000 reversed by the void.
	wantEq(t, "4000 IDR base credit", rev4000.BaseCredit, "1300000.0000")
	wantEq(t, "4000 IDR base debit", rev4000.BaseDebit, "300000.0000")
	wantEq(t, "4000 IDR base net", rev4000.BaseNet, "-1000000.0000")
	// 4000 USD: 500 at 15000.
	wantEq(t, "4000 USD net", rev4000USD.Net, "-500.0000")
	wantEq(t, "4000 USD base net", rev4000USD.BaseNet, "-7500000.0000")
	// 4100 USD: 1000 at 15000, never relieved by its payment.
	wantEq(t, "4100 USD net", rev4100.Net, "-1000.0000")
	wantEq(t, "4100 USD base net", rev4100.BaseNet, "-15000000.0000")

	// As of accrualPaid every payment is in and the void is not yet: the
	// voided invoice's 300,000 is still receivable, and the book balances.
	paid := tb(t, a, accrualPaidQ)
	if paid.TotalDebitBase != paid.TotalCreditBase {
		t.Errorf("as of %s: Σ base debit %s ≠ Σ base credit %s", accrualPaid, paid.TotalDebitBase, paid.TotalCreditBase)
	}
	wantEq(t, "1100 IDR base net as of paid", tbBaseNet(paid, "1100", "IDR"), "800000.0000")
	wantEq(t, "1100 USD base net as of paid", tbBaseNet(paid, "1100", "USD"), "7500000.0000")
	// Bank: 500,000 IDR + 1000 USD at 16000 − 2,000,000 IDR bill.
	wantEq(t, "1000 IDR base net as of paid", tbBaseNet(paid, "1000", "IDR"), "-1500000.0000")
	wantEq(t, "1000 USD base net as of paid", tbBaseNet(paid, "1000", "USD"), "16000000.0000")
	wantEq(t, "4900 IDR base net as of paid", tbBaseNet(paid, "4900", "IDR"), "-1000000.0000")
}

func TestBalanceSheetARAPAtIssueBasis(t *testing.T) {
	t.Parallel()
	a := accrualApp(t)
	b := buildAccrualBook(t, a)
	r := bs(t, a, accrualAsOfQ)

	idrOpen := getInvoice(t, a, b.idrHalf).BalanceDue.Decimal()
	usdOpen := getInvoice(t, a, b.usdOpen).BalanceDue.Decimal()
	wantEq(t, "USD open balance", usdOpen.StringFixed(4), "500.0000")
	// AR = 500,000 + 500 × 15000.
	wantAR := idrOpen.Add(usdOpen.Mul(dec("15000"))).StringFixed(4)
	wantEq(t, "AR base (expected)", wantAR, "8000000.0000")
	if l := bsFind(r, "1100"); l == nil {
		t.Errorf("no 1100 line")
	} else {
		wantEq(t, "1100 base balance", l.BaseBalance, wantAR)
	}

	idrBill, usdBill := getBill(t, a, b.idrBill).BalanceDue.Decimal(), getBill(t, a, b.usdBill).BalanceDue.Decimal()
	// AP = 0 + 200 × 15000.
	wantAP := idrBill.Add(usdBill.Mul(dec("15000"))).StringFixed(4)
	wantEq(t, "AP base (expected)", wantAP, "3000000.0000")
	if l := bsFind(r, "2000"); l == nil {
		t.Errorf("no 2000 line")
	} else {
		wantEq(t, "2000 base balance", l.BaseBalance, wantAP)
	}

	// The realised gain (1000 USD at 16000 against 15000) is income.
	if l := bsFind(r, "4900"); l == nil || bsGroupOf(r, "4900") != "INCOME" {
		t.Errorf("4900 line %+v in group %q, want INCOME", l, bsGroupOf(r, "4900"))
	} else {
		wantEq(t, "4900 base balance", l.BaseBalance, "1000000.0000")
	}
	if l := bsFind(r, "5900"); l != nil {
		t.Errorf("5900 line %+v, want none (no loss in this book)", l)
	}
	// 4000 8,500,000 + 4100 15,000,000 + 4900 1,000,000.
	wantEq(t, "income total", r.IncomeTotalBase, "24500000.0000")
	// 6000: 2,000,000 + 200 × 15000.
	wantEq(t, "expense total", r.ExpenseTotalBase, "5000000.0000")
	// 1000: 500,000 + 1000 × 16000 − 2,000,000; plus AR.
	wantEq(t, "assets total", r.AssetsTotalBase, "22500000.0000")
	wantEq(t, "liabilities total", r.LiabilitiesTotalBase, "3000000.0000")

	// assets = liabilities + equity + income − expense.
	rhs := dec(r.LiabilitiesTotalBase).Add(dec(r.EquityTotalBase)).Add(dec(r.IncomeTotalBase)).Sub(dec(r.ExpenseTotalBase))
	if !dec(r.AssetsTotalBase).Equal(rhs) {
		t.Errorf("assets %s ≠ liabilities + equity + income − expense %s", r.AssetsTotalBase, rhs)
	}
}

func TestRevaluationPaidForeignInvoiceLeavesNoResidue(t *testing.T) {
	t.Parallel()
	t.Run("paid invoice alone", func(t *testing.T) {
		t.Parallel()
		a := accrualApp(t)
		c := newCustomer(t, a, "Acme")
		id := issueInvoice(t, a, c, "USD", "RECURRING", "1000").InvoiceID
		payInvoice(t, a, id, "1000")
		r, body := fxGet(t, a, accrualAsOfQ)
		for _, l := range r.Lines {
			if l.Code == "1100" {
				t.Errorf("1100 %s bucket from the paid invoice: %s", l.Currency, body)
			}
		}
	})
	t.Run("whole book", func(t *testing.T) {
		t.Parallel()
		a := accrualApp(t)
		buildAccrualBook(t, a)
		r, body := fxGet(t, a, accrualAsOfQ)
		// The 1100 USD bucket is the open invoice only: 500 at 15000.
		var found bool
		for _, l := range r.Lines {
			if l.Code == "1100" && l.Currency == "USD" {
				found = true
				wantEq(t, "1100 USD face", l.FaceBalance, "500.0000")
				wantEq(t, "1100 USD original base", l.OriginalBase, "7500000.0000")
			}
		}
		if !found {
			t.Errorf("no 1100 USD bucket: %s", body)
		}
	})
}

func TestRevaluationRevaluesOpenForeignARAndAP(t *testing.T) {
	t.Parallel()
	a := accrualApp(t)
	buildAccrualBook(t, a)
	r, body := fxGet(t, a, accrualAsOfQ)
	if len(r.MissingRates) != 0 {
		t.Fatalf("missing rates %v", r.MissingRates)
	}
	type want struct{ face, original, revalued, unrealised string }
	wants := map[string]want{
		// Bank: 1000 USD received at 16000.
		"1000": {"1000.0000", "16000000.0000", "17000000.0000", "1000000.0000"},
		// AR: 500 USD issued at 15000.
		"1100": {"500.0000", "7500000.0000", "8500000.0000", "1000000.0000"},
		// AP: 200 USD opened at 15000; a liability's rise is a loss.
		"2000": {"200.0000", "3000000.0000", "3400000.0000", "-400000.0000"},
	}
	if len(r.Lines) != len(wants) {
		t.Errorf("%d lines, want %d: %s", len(r.Lines), len(wants), body)
	}
	for _, l := range r.Lines {
		w, ok := wants[l.Code]
		if !ok || l.Currency != "USD" {
			t.Errorf("unexpected line %+v", l)
			continue
		}
		wantEq(t, l.Code+" face", l.FaceBalance, w.face)
		wantEq(t, l.Code+" original base", l.OriginalBase, w.original)
		wantEq(t, l.Code+" rate", str(l.Rate), accrualRevalQ+".00000000")
		wantEq(t, l.Code+" rate as of", str(l.RateAsOf), accrualAsOf)
		wantEq(t, l.Code+" revalued base", str(l.RevaluedBase), w.revalued)
		wantEq(t, l.Code+" unrealised", str(l.UnrealisedGainLoss), w.unrealised)
	}
	wantEq(t, "total gain", r.TotalUnrealisedGain, "2000000.0000")
	wantEq(t, "total loss", r.TotalUnrealisedLoss, "400000.0000")
}

// A small book of its own, so the whole book's totals stay put: two USD
// invoices of 1000 issued at 15000, one paid on 2025-02-01 at 16000 (4900
// gain 1,000,000), the other on 2025-03-01 at 14000 (5900 loss 1,000,000).
// The loss is an expense, posted on its payment date.
func TestBalanceSheetRealisedFXLossIsExpense(t *testing.T) {
	t.Parallel()
	a := accrualApp(t)
	c := newCustomer(t, a, "Acme")
	gain := issueInvoice(t, a, c, "USD", "RECURRING", "1000").InvoiceID
	recordInvoicePayment(t, a, gain, "1000", accrualPaid)
	loss := issueInvoice(t, a, c, "USD", "MILESTONE", "1000").InvoiceID
	recordInvoicePayment(t, a, loss, "1000", "2025-03-01")

	if l := bsFind(bs(t, a, "?as_of=2025-02-28"), "5900"); l != nil {
		t.Errorf("as of the day before the losing payment: 5900 line %+v, want none", l)
	}
	r := bs(t, a, "?as_of=2025-03-01")
	if l := bsFind(r, "5900"); l == nil || bsGroupOf(r, "5900") != "EXPENSE" {
		t.Errorf("5900 line %+v in group %q, want EXPENSE", l, bsGroupOf(r, "5900"))
	} else {
		wantEq(t, "5900 base balance", l.BaseBalance, "1000000.0000")
	}
	if l := bsFind(r, "4900"); l == nil {
		t.Errorf("no 4900 line")
	} else {
		wantEq(t, "4900 base balance", l.BaseBalance, "1000000.0000")
	}
	if l := bsFind(r, "1100"); l != nil && !dec(l.BaseBalance).IsZero() {
		t.Errorf("1100 line %+v, want nothing receivable", l)
	}
	// 4000 15,000,000 + 4100 15,000,000 + 4900 1,000,000.
	wantEq(t, "income total", r.IncomeTotalBase, "31000000.0000")
	wantEq(t, "expense total", r.ExpenseTotalBase, "1000000.0000")
	// 1000: 1000 × 16000 + 1000 × 14000.
	wantEq(t, "assets total", r.AssetsTotalBase, "30000000.0000")
	rhs := dec(r.LiabilitiesTotalBase).Add(dec(r.EquityTotalBase)).Add(dec(r.IncomeTotalBase)).Sub(dec(r.ExpenseTotalBase))
	if !dec(r.AssetsTotalBase).Equal(rhs) {
		t.Errorf("assets %s ≠ liabilities + equity + income − expense %s", r.AssetsTotalBase, rhs)
	}
}

// The issue entry is posted at the issue date: a trial balance as of the
// day before has no receivable, and one as of the issue date has it.
func TestTrialBalanceAsOfBeforeIssueExcludesInvoice(t *testing.T) {
	t.Parallel()
	a := accrualApp(t)
	c := newCustomer(t, a, "Acme")
	issueInvoice(t, a, c, "IDR", "MILESTONE", "1000000")
	issueInvoice(t, a, c, "USD", "MILESTONE", "500")

	before := tb(t, a, "?as_of=2024-12-31")
	for _, r := range before.Rows {
		if r.Code == "1100" {
			t.Errorf("as of the day before the issue date: 1100 row %+v", r)
		}
	}
	on := tb(t, a, "?as_of="+accrualIssue)
	idr, usd := tbFind(on, "1100", "IDR"), tbFind(on, "1100", "USD")
	if idr == nil || usd == nil {
		t.Fatalf("as of the issue date: 1100 rows IDR %+v USD %+v, want both", idr, usd)
	}
	wantEq(t, "1100 IDR base net", idr.BaseNet, "1000000.0000")
	wantEq(t, "1100 USD base net", usd.BaseNet, "7500000.0000")
}

func TestAgingUnchangedByPostings(t *testing.T) {
	t.Parallel()
	a := accrualApp(t)
	b := buildAccrualBook(t, a)

	ar, body := getAging(t, a, arPath, accrualAsOfQ)
	wantAR := map[string]string{
		"IDR": getInvoice(t, a, b.idrHalf).BalanceDue.Decimal().StringFixed(4),
		"USD": getInvoice(t, a, b.usdOpen).BalanceDue.Decimal().StringFixed(4),
	}
	if len(ar.TotalsByCurrency) != len(wantAR) {
		t.Errorf("AR totals %+v, want %v: %s", ar.TotalsByCurrency, wantAR, body)
	}
	for cur, w := range wantAR {
		wantEq(t, "AR aging "+cur, ar.TotalsByCurrency[cur].Total, w)
	}

	ap, body := getAging(t, a, apPath, accrualAsOfQ)
	if got := getBill(t, a, b.idrBill).BalanceDue.Decimal().StringFixed(4); got != "0.0000" {
		t.Errorf("paid IDR bill balance %s", got)
	}
	wantAP := map[string]string{"USD": getBill(t, a, b.usdBill).BalanceDue.Decimal().StringFixed(4)}
	if len(ap.TotalsByCurrency) != len(wantAP) {
		t.Errorf("AP totals %+v, want %v: %s", ap.TotalsByCurrency, wantAP, body)
	}
	for cur, w := range wantAP {
		wantEq(t, "AP aging "+cur, ap.TotalsByCurrency[cur].Total, w)
	}
}

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }
