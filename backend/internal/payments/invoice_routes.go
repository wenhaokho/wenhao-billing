package payments

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/LOQATO/gokit/db"

	"github.com/wenhaokho/wenhao-billing/backend/internal/payments/paymentsdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

const (
	negativeSentence = "Input should be greater than or equal to 0"
	// manualPayer is the payer of a manual payment that names none.
	manualPayer = "(manual entry)"
	// maxNotesRunes is how much of the notes the log reason keeps (Python's
	// notes[:200]).
	maxNotesRunes = 200
)

// RecordPaymentRequest is the body of record-payment, Python's
// InvoiceRecordPaymentRequest (schemas/payment.py:59-66).
type RecordPaymentRequest struct {
	Amount         paymentAmount                   `json:"amount" doc:"At least 0, at most 4 decimal places."`
	WhtAmount      platform.NotNull[paymentAmount] `json:"wht_amount" doc:"Withholding tax the payer kept back; at least 0, at most 4 decimal places. Defaults to 0."`
	PaymentDate    platform.Date                   `json:"payment_date"`
	PayerName      platform.Optional[string]       `json:"payer_name" maxLength:"255" doc:"Empty or null is \"(manual entry)\"."`
	PayerReference platform.Optional[string]       `json:"payer_reference" maxLength:"255"`
	Notes          platform.Optional[string]       `json:"notes" doc:"Only the first 200 characters are kept, as a reconciliation log reason."`
}

// paymentAmount is a Dec4 and the JSON text it was sent as, which decides how
// Python printed it in a refusal (PORT-NOTES 73: FastAPI hands pydantic a JSON
// number with a fraction or exponent as a float).
type paymentAmount struct {
	platform.Dec4
	raw string
}

// UnmarshalJSON keeps the text and parses it as Dec4 does.
func (a *paymentAmount) UnmarshalJSON(b []byte) error {
	a.raw = string(b)
	return a.Dec4.UnmarshalJSON(b)
}

// python is Decimal(payload.value) as Python held it: a JSON number with a
// fraction or exponent went through float, so 600.00 is 600.0; an integer
// keeps its digits; a string keeps the literal's digits and scale.
func (a paymentAmount) python() decimal.Decimal {
	d := a.Decimal()
	if a.raw == "" || strings.HasPrefix(a.raw, `"`) || !strings.ContainsAny(a.raw, ".eE") {
		return d
	}
	f, err := strconv.ParseFloat(a.raw, 64)
	if err != nil {
		return d // unreachable: Dec4 parsed the same text
	}
	r, err := decimal.NewFromString(platform.PyFloatRepr(f))
	if err != nil {
		return d // unreachable: a finite repr parses
	}
	return r
}

func (r routes) registerInvoice(api huma.API) {
	const prefix = "/api/v1/invoices/{invoice_id}"
	tags := []string{"payments"}
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "invoices-record-payment", Method: http.MethodPost, Path: prefix + "/record-payment", Tags: tags,
		Summary: "Record a payment received against an invoice",
		Description: "A CLEARED MANUAL payment in the invoice's currency. The balance falls by the amount plus the withheld amount: to 0 is PAID, " +
			"otherwise PARTIAL. A DRAFT or VOID invoice, both amounts 0, more than the balance, or more withholding than is outstanding is refused. " +
			"The payment's journal entry is posted as an approved payment's is, so it can be reversed; a ledger refusal or a missing FX rate is refused " +
			"and nothing is kept.",
		DefaultStatus: http.StatusCreated,
		Errors:        []int{http.StatusBadRequest, http.StatusNotFound},
	}), r.recordPayment)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "invoices-payments", Method: http.MethodGet, Path: prefix + "/payments", Tags: tags,
		Summary: "The invoice's CLEARED payments, newest first",
		Errors:  []int{http.StatusNotFound},
	}), r.listForInvoice)
}

func invoiceNotFound() error {
	return clientRefuse(http.StatusNotFound, "invoice_not_found", "invoice not found")
}

type recordIn struct {
	InvoiceID uuid.UUID `path:"invoice_id"`
	Body      RecordPaymentRequest
}

type paymentBody struct{ Body PaymentOut }

func (r routes) recordPayment(ctx context.Context, in *recordIn) (*paymentBody, error) {
	var wht paymentAmount
	if in.Body.WhtAmount.Set {
		wht = in.Body.WhtAmount.Value
	}
	if in.Body.Amount.Decimal().Sign() < 0 {
		return nil, platform.FieldError("amount", negativeSentence)
	}
	if wht.Decimal().Sign() < 0 {
		return nil, platform.FieldError("wht_amount", negativeSentence)
	}
	req := manualPayment{
		invoiceID: in.InvoiceID, amount: in.Body.Amount, wht: wht, date: in.Body.PaymentDate,
		payerName: stringOrEmpty(in.Body.PayerName), payerRef: in.Body.PayerReference.Merge(nil),
		notes: stringOrEmpty(in.Body.Notes), actor: platform.PrincipalFrom(ctx).UserID,
	}
	var p paymentsdb.Payment
	err := db.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		var err error
		p, err = r.svc.recordManual(ctx, tx, req)
		return err
	})
	if err != nil {
		// A ledger refusal or a missing rate is 400 with its sentence, as
		// approval answers; the transaction has rolled back.
		return nil, refusalError(clientError(err))
	}
	r.svc.AfterCommit(ctx, p)
	return &paymentBody{Body: PaymentOutFrom(p)}, nil
}

func stringOrEmpty(o platform.Optional[string]) string {
	if v := o.Merge(nil); v != nil {
		return *v
	}
	return ""
}

type manualPayment struct {
	invoiceID        uuid.UUID
	amount, wht      paymentAmount
	date             platform.Date
	payerName, notes string
	payerRef         *string
	actor            uuid.UUID
}

// recordManual is Python's record_payment (invoices.py:510-594) in tx. The
// invoice is read locked (lockInvoice) before the checks, so two payments at
// once see each other's balance (Python lost an update). Refusals, in order:
// unknown invoice (404), DRAFT or VOID, both amounts 0, more than the
// balance, more withholding than is outstanding. A PAID invoice has balance 0
// and so falls to the balance sentence. The payment is CLEARED, MANUAL and in
// the invoice's currency. Unlike Python (PORT-NOTES 161), its journal entry
// is posted here with postCleared, the posting intake and approval use, so
// Reverse can undo it; a ledger refusal or a missing rate comes back
// unchanged (errors.Is ledger.ErrLedger or fx.ErrRateMissing) and the caller
// rolls back, leaving no payment, log row or entry. A PAID result
// stamps Hosting (the route calls AfterCommit once committed). A CLEARED log
// row names the actor and holds the reasons "manual-entry" and the notes'
// first 200 characters. The caller rolls back on any error.
func (s Service) recordManual(ctx context.Context, tx pgx.Tx, m manualPayment) (paymentsdb.Payment, error) {
	q := paymentsdb.New(tx)
	inv, found, err := lockInvoice(ctx, q, m.invoiceID)
	if err != nil {
		return paymentsdb.Payment{}, err
	}
	if !found {
		return paymentsdb.Payment{}, invoiceNotFound()
	}
	if inv.Status == invoiceDraft || inv.Status == invoiceVoid {
		return paymentsdb.Payment{}, clientRefuse(http.StatusBadRequest, "invoice_not_payable", "cannot record payment on %s invoice", inv.Status)
	}
	// The checks and all arithmetic use the exact decimals that the payment
	// row stores; python() only decides what the two sentences print.
	amount, wht := m.amount.Decimal(), m.wht.Decimal()
	settled := amount.Add(wht)
	if settled.IsZero() {
		return paymentsdb.Payment{}, clientRefuse(http.StatusBadRequest, "payment_zero", "amount and withheld cannot both be zero")
	}
	if settled.GreaterThan(inv.BalanceDue) {
		label := "amount"
		if wht.IsPositive() {
			label = "amount + withheld"
		}
		return paymentsdb.Payment{}, clientRefuse(http.StatusBadRequest, "payment_exceeds_balance", "%s %s exceeds balance due %s",
			label, platform.PyDecimalStr(m.amount.python().Add(m.wht.python())), inv.BalanceDue.StringFixed(4))
	}
	outstanding := inv.WhtAmount.Sub(inv.WhtBooked)
	if wht.GreaterThan(outstanding) {
		return paymentsdb.Payment{}, clientRefuse(http.StatusBadRequest, "withholding_exceeds_outstanding", "withheld %s exceeds outstanding withholding %s",
			platform.PyDecimalStr(m.wht.python()), outstanding.StringFixed(4))
	}

	name := m.payerName
	if name == "" {
		name = manualPayer
	}
	suffix := uuid.New()
	ref := "manual-" + hex.EncodeToString(suffix[:6])
	p, err := q.InsertManualPayment(ctx, paymentsdb.InsertManualPaymentParams{
		PaymentID: uuid.New(), InvoiceID: pgtype.UUID{Bytes: inv.InvoiceID, Valid: true}, CustomerID: inv.CustomerID,
		Amount: amount, WhtAmount: wht, Currency: inv.Currency,
		PayerName: name, PayerReference: m.payerRef, PaymentDate: pgDate(m.date.Time), ExternalRef: &ref,
	})
	if err != nil {
		return paymentsdb.Payment{}, fmt.Errorf("payments: insert manual payment: %w", err)
	}
	// Go's change (PORT-NOTES 161): the same posting as an approved payment,
	// under the invoice lock and against the balance before this payment.
	if err := s.postCleared(ctx, tx, p, inv, wht); err != nil {
		return paymentsdb.Payment{}, err
	}

	inv.BalanceDue = inv.BalanceDue.Sub(settled)
	inv.WhtBooked = inv.WhtBooked.Add(wht)
	paid := !inv.BalanceDue.IsPositive()
	if paid {
		inv.BalanceDue = decimal.Zero
		inv.Status = invoicePaid
	} else {
		inv.Status = invoicePartial
	}
	if err := saveInvoice(ctx, q, inv); err != nil {
		return paymentsdb.Payment{}, err
	}
	if paid {
		if err := s.stampPaid(ctx, tx, inv.InvoiceID); err != nil {
			return paymentsdb.Payment{}, err
		}
	}

	reasons := []string{"manual-entry"}
	if m.notes != "" {
		reasons = append(reasons, cutRunes(m.notes, maxNotesRunes))
	}
	if err := logAction(ctx, q, p.PaymentID, actionCleared, reasons, &m.actor); err != nil {
		return paymentsdb.Payment{}, err
	}
	return p, nil
}

// cutRunes is the first n characters of s (Python's s[:n]).
func cutRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

type listIn struct {
	InvoiceID uuid.UUID `path:"invoice_id"`
}

type listOut struct{ Body []PaymentOut }

func (r routes) listForInvoice(ctx context.Context, in *listIn) (*listOut, error) {
	exists, err := r.q.InvoiceExists(ctx, in.InvoiceID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, clientError(invoiceNotFound())
	}
	rows, err := r.q.ListInvoicePayments(ctx, pgUUID(&in.InvoiceID))
	if err != nil {
		return nil, err
	}
	out := make([]PaymentOut, 0, len(rows))
	for _, p := range rows {
		out = append(out, PaymentOutFrom(p))
	}
	return &listOut{Body: out}, nil
}
