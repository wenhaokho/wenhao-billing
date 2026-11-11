package bills

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/LOQATO/gokit/db"

	"github.com/wenhaokho/wenhao-billing/backend/internal/bills/billsdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/ledger"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// Go's own (PORT-NOTES 165): a bill payment is a bill_payments row, posted
// Dr 2000 / Cr 1000 at the bill's issue basis with realised FX
// (ledger.PostBillPayment), listed, and reversible. Python kept no row and
// posted nothing (port note 8).

// The sources of a bill payment.
const (
	sourceManual       = "MANUAL"        // record-payment
	sourceStatusUpdate = "STATUS_UPDATE" // PATCH (or MCP update_bill) to PAID
)

// paidViaUpdate is the notes of the payment a PATCH to PAID records.
const paidViaUpdate = "status set to PAID via update"

// BillPaymentOut is a stored bill payment.
type BillPaymentOut struct {
	BillPaymentID  uuid.UUID      `json:"bill_payment_id"`
	BillID         uuid.UUID      `json:"bill_id"`
	Amount         platform.Dec4  `json:"amount"`
	Currency       string         `json:"currency" doc:"The bill's."`
	PaymentDate    platform.Date  `json:"payment_date"`
	PayerName      *string        `json:"payer_name"`
	Reference      *string        `json:"reference" doc:"record-payment's payer_reference."`
	Notes          *string        `json:"notes"`
	Status         string         `json:"status" enum:"CLEARED,REVERSED"`
	Source         string         `json:"source" enum:"MANUAL,STATUS_UPDATE,LEGACY_BACKPOST" doc:"MANUAL: record-payment; STATUS_UPDATE: a PATCH to PAID; LEGACY_BACKPOST: paid before the switch (the back-post)."`
	CreatedAt      platform.Time  `json:"created_at"`
	ReversedAt     *platform.Time `json:"reversed_at"`
	ReversalReason *string        `json:"reversal_reason"`
}

func billPaymentOut(p billsdb.BillPayment) BillPaymentOut {
	out := BillPaymentOut{
		BillPaymentID: p.BillPaymentID, BillID: p.BillID, Amount: platform.NewDec4(p.Amount), Currency: p.Currency,
		PaymentDate: platform.Date{Time: p.PaymentDate.Time}, PayerName: p.PayerName, Reference: p.Reference, Notes: p.Notes,
		Status: p.Status, Source: p.Source, CreatedAt: platform.Time{Time: p.CreatedAt}, ReversalReason: p.ReversalReason,
	}
	if p.ReversedAt != nil {
		out.ReversedAt = &platform.Time{Time: *p.ReversedAt}
	}
	return out
}

// ReverseBillPaymentRequest is the body of a bill payment reversal.
type ReverseBillPaymentRequest struct {
	Reason string `json:"reason" minLength:"1" maxLength:"490" doc:"Required. Kept on the payment and as the ledger memo \"Reversal: <reason>\"."`
}

type billPaymentsIn struct {
	BillID uuid.UUID `path:"bill_id"`
}

type billPaymentsOut struct{ Body []BillPaymentOut }

type reverseBillPaymentIn struct {
	BillID        uuid.UUID `path:"bill_id"`
	BillPaymentID uuid.UUID `path:"bill_payment_id"`
	Body          ReverseBillPaymentRequest
}

type billPaymentBody struct{ Body BillPaymentOut }

func (r routes) registerBillPayments(api huma.API, prefix string, tags []string) {
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "bills-list-payments", Method: http.MethodGet, Path: prefix + "/{bill_id}/payments", Tags: tags,
		Summary: "A bill's payments, oldest first, reversed ones included",
		Errors:  []int{http.StatusNotFound},
	}), r.listPayments)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "bills-reverse-payment", Method: http.MethodPost, Path: prefix + "/{bill_id}/payments/{bill_payment_id}/reverse", Tags: tags,
		Summary:     "Reverse a bill payment",
		Description: "The payment's ledger entry is reversed (memo \"Reversal: <reason>\"), the payment becomes REVERSED, and its amount is due again: the bill is OPEN when its balance is back to its amount, otherwise PARTIAL.",
		Errors:      []int{http.StatusBadRequest, http.StatusNotFound},
	}), r.reversePayment)
}

func (r routes) listPayments(ctx context.Context, in *billPaymentsIn) (*billPaymentsOut, error) {
	if _, err := r.q.GetBill(ctx, in.BillID); errors.Is(err, pgx.ErrNoRows) {
		return nil, notFound()
	} else if err != nil {
		return nil, err
	}
	rows, err := r.q.ListBillPayments(ctx, in.BillID)
	if err != nil {
		return nil, err
	}
	out := make([]BillPaymentOut, 0, len(rows))
	for _, p := range rows {
		out = append(out, billPaymentOut(p))
	}
	return &billPaymentsOut{Body: out}, nil
}

func (r routes) reversePayment(ctx context.Context, in *reverseBillPaymentIn) (*billPaymentBody, error) {
	var out BillPaymentOut
	err := db.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		var err error
		out, err = reverseBillPayment(ctx, tx, in.BillID, in.BillPaymentID, in.Body.Reason)
		return err
	})
	if err != nil {
		return nil, clientError(err)
	}
	return &billPaymentBody{Body: out}, nil
}

// errPaymentNotFound is a bill payment that is not the bill's: a 404.
var errPaymentNotFound = errors.New("bill payment not found")

// newPayment is a bill payment to record.
type newPayment struct {
	amount                decimal.Decimal
	date                  pgtype.Date
	payerName, ref, notes *string
	source                string
}

// payBill records p against cur, the bill read FOR UPDATE in tx and checked
// payable: it stores the row in the bill's currency, posts it
// (ledger.PostBillPayment), and lowers the balance by the amount: to 0 is
// PAID, otherwise PARTIAL. A ledger refusal or a missing rate rolls the
// caller's transaction back.
func payBill(ctx context.Context, tx pgx.Tx, base string, cur billsdb.Bill, p newPayment) error {
	q := billsdb.New(tx)
	row, err := q.InsertBillPayment(ctx, billsdb.InsertBillPaymentParams{
		BillPaymentID: uuid.New(), BillID: cur.BillID, Amount: p.amount, Currency: cur.Currency, PaymentDate: p.date,
		PayerName: p.payerName, Reference: p.ref, Notes: p.notes, Source: p.source,
	})
	if err != nil {
		return err
	}
	if _, err := ledger.PostBillPayment(ctx, tx, base, ledger.BillPayment{
		ID: row.BillPaymentID, Amount: row.Amount, Currency: row.Currency, Date: dateOf(row.PaymentDate),
		Memo: "Payment " + row.BillPaymentID.String() + " of bill " + billLabel(cur),
	}, cur.BillID); err != nil {
		return err
	}
	balance, status := cur.BalanceDue.Sub(p.amount), "PARTIAL"
	if balance.Sign() <= 0 {
		balance, status = decimal.Zero, "PAID"
	}
	return q.SetBillBalance(ctx, billsdb.SetBillBalanceParams{BillID: cur.BillID, BalanceDue: balance, Status: status})
}

// reverseBillPayment reverses bill payment pid of bill id in tx. Lock order:
// the payment FOR UPDATE, then the bill FOR UPDATE, then (in the ledger) its
// entry. A REVERSED payment is refused (400 bill_payment_not_reversible);
// one with no active entry is a ledger refusal. The bill's balance rises by
// the amount: OPEN when it is back to the bill's amount, else PARTIAL.
func reverseBillPayment(ctx context.Context, tx pgx.Tx, id, pid uuid.UUID, reason string) (BillPaymentOut, error) {
	q := billsdb.New(tx)
	p, err := q.GetBillPaymentForUpdate(ctx, billsdb.GetBillPaymentForUpdateParams{BillPaymentID: pid, BillID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		if _, err := q.GetBill(ctx, id); errors.Is(err, pgx.ErrNoRows) {
			return BillPaymentOut{}, errNotFound
		} else if err != nil {
			return BillPaymentOut{}, err
		}
		return BillPaymentOut{}, errPaymentNotFound
	}
	if err != nil {
		return BillPaymentOut{}, err
	}
	cur, err := lock(ctx, q, id)
	if err != nil {
		return BillPaymentOut{}, err
	}
	if p.Status != "CLEARED" {
		return BillPaymentOut{}, refuse("bill_payment_not_reversible", "bill payment is %s and cannot be reversed", p.Status)
	}
	reversed, err := ledger.ReverseIssued(ctx, tx, ledger.SourceBillPayment, pid, "Reversal: "+reason)
	if err != nil {
		return BillPaymentOut{}, err
	}
	if !reversed {
		return BillPaymentOut{}, ledger.Refuse("bill payment %s has no ledger entry — run api backpost-ledger", pid)
	}
	row, err := q.ReverseBillPayment(ctx, billsdb.ReverseBillPaymentParams{BillPaymentID: pid, Reason: &reason})
	if err != nil {
		return BillPaymentOut{}, err
	}
	balance, status := cur.BalanceDue.Add(p.Amount), "PARTIAL"
	if balance.GreaterThanOrEqual(cur.Amount) {
		status = "OPEN"
	}
	if err := q.SetBillBalance(ctx, billsdb.SetBillBalanceParams{BillID: id, BalanceDue: balance, Status: status}); err != nil {
		return BillPaymentOut{}, err
	}
	return billPaymentOut(row), nil
}

// hasActivePayments refuses (400 bill_has_payments, with sentence) when bill
// id has a payment that is not reversed. The caller holds the bill's lock,
// so no payment is recorded meanwhile.
func hasActivePayments(ctx context.Context, q *billsdb.Queries, id uuid.UUID, sentence string) error {
	n, err := q.CountActiveBillPayments(ctx, id)
	if err != nil {
		return err
	}
	if n > 0 {
		return refuse("bill_has_payments", "%s", sentence)
	}
	return nil
}

// payRest is a PATCH to PAID (PORT-NOTES 165): the bill, opened and edited
// as the PATCH asked and still locked, records a STATUS_UPDATE payment of its
// whole balance dated CURRENT_DATE. A bill with nothing due becomes PAID with
// no payment.
func payRest(ctx context.Context, tx pgx.Tx, base string, id uuid.UUID) error {
	q := billsdb.New(tx)
	cur, err := q.GetBill(ctx, id)
	if err != nil {
		return err
	}
	if !cur.BalanceDue.IsPositive() {
		return q.SetBillBalance(ctx, billsdb.SetBillBalanceParams{BillID: id, BalanceDue: decimal.Zero, Status: "PAID"})
	}
	today, err := q.CurrentDate(ctx)
	if err != nil {
		return err
	}
	notes := paidViaUpdate
	return payBill(ctx, tx, base, cur, newPayment{amount: cur.BalanceDue, date: today, notes: &notes, source: sourceStatusUpdate})
}

// billLabel is how a bill's ledger memos name it: its number, else its id.
func billLabel(b billsdb.Bill) string {
	if b.BillNumber != nil && *b.BillNumber != "" {
		return *b.BillNumber
	}
	return b.BillID.String()
}

// dateOf is a stored date at 00:00 UTC.
func dateOf(d pgtype.Date) time.Time {
	return time.Date(d.Time.Year(), d.Time.Month(), d.Time.Day(), 0, 0, 0, 0, time.UTC)
}
