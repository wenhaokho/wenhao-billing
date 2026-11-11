package payments

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/LOQATO/gokit/observe"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/fx"
	"github.com/wenhaokho/wenhao-billing/backend/internal/ledger"
	"github.com/wenhaokho/wenhao-billing/backend/internal/matching"
	"github.com/wenhaokho/wenhao-billing/backend/internal/payments/paymentsdb"
)

// Invoice statuses this package reads or writes.
const (
	invoiceOpen    = "OPEN"
	invoiceSent    = "SENT"
	invoicePartial = "PARTIAL"
	invoicePaid    = "PAID"
	invoiceDraft   = "DRAFT"
	invoiceVoid    = "VOID"
)

// sourcePayment is the journal entries' source_type for a payment.
const sourcePayment = "PAYMENT"

// ErrPayment is matched (errors.Is) by every refusal of ApproveManual and
// Reverse; the error's text is Python's sentence (the routes' 400 detail).
var ErrPayment = errors.New("payment refused")

// ErrDuplicate is a payment whose (intake_source, external_ref) is already
// stored. The unique violation has aborted the transaction.
var ErrDuplicate = errors.New("duplicate (intake_source, external_ref)")

type refusal struct{ msg string }

func (e refusal) Error() string        { return e.msg }
func (e refusal) Is(target error) bool { return target == ErrPayment }

func refuse(format string, args ...any) error { return refusal{fmt.Sprintf(format, args...)} }

// IntakePayload is a parsed incoming payment (webhook or email).
// PaymentDate's calendar date is stored.
type IntakePayload struct {
	Amount              decimal.Decimal
	Currency, PayerName string
	PayerReference      *string
	PaymentDate         time.Time
	IntakeSource        string
	ExternalRef         string
}

// Service reconciles payments in the caller's transaction; the caller
// commits, or rolls back on any error.
type Service struct {
	// BaseCurrency is the ledger's base currency.
	BaseCurrency string
	// Hosting stamps every invoice that becomes PAID in the payment's
	// transaction, and restores it once the caller committed (AfterCommit);
	// nil is NoHosting.
	Hosting HostingRestorer
}

func (s Service) hosting() HostingRestorer {
	if s.Hosting == nil {
		return NoHosting{}
	}
	return s.Hosting
}

// stampPaid tells Hosting, in tx, that invoiceID became PAID.
func (s Service) stampPaid(ctx context.Context, tx pgx.Tx, invoiceID uuid.UUID) error {
	if err := s.hosting().StampPaid(ctx, tx, invoiceID); err != nil {
		return fmt.Errorf("payments: hosting stamp: %w", err)
	}
	return nil
}

// AfterCommit finishes a payment that ProcessIncoming, ApproveManual or
// record-payment returned, once the caller's transaction has committed:
// when p is CLEARED against an invoice, Hosting restores that invoice's
// hosting, best effort (a Cloudflare failure never fails the payment, Go
// change 1). It must not be called inside the transaction, nor after a
// rollback. A HostingRestorer that panics (for example a nil Cloudflare
// client or a pgx panic) is recovered, logged and reported here, never
// re-panicked: the payment is already committed, so a panic would otherwise
// reach the platform recoverer and answer 500 for it.
func (s Service) AfterCommit(ctx context.Context, p paymentsdb.Payment) {
	if p.Status == matching.StatusCleared && p.InvoiceID.Valid {
		defer func() {
			if rec := recover(); rec != nil {
				err := fmt.Errorf("hosting restore after payment panicked: %v", rec)
				slog.ErrorContext(ctx, "hosting restore after payment panicked", "panic", rec, "invoice_id", uuid.UUID(p.InvoiceID.Bytes))
				observe.Report(err, map[string]string{"component": "payments", "stage": "restore_after_commit"})
			}
		}()
		s.hosting().RestoreAfterCommit(ctx, p.InvoiceID.Bytes)
	}
}

// ProcessIncoming matches an incoming payment against the open invoices in
// its currency, stores it, and clears it only when the engine says CLEARED
// (Python's process_incoming_payment, SR:124-164). The payment keeps the best
// candidate's invoice and customer even when held. A cleared payment posts
// to the ledger, sets the invoice's balance to 0 and its status to PAID, and
// stamps Hosting (the caller calls AfterCommit once committed). A HELD or
// CLEARED log row (no actor) records the reasons.
//
// Clearing runs in a savepoint after the payment row is written. When the
// posting fails with ledger.ErrLedger or fx.ErrRateMissing, or the invoice,
// read again under its lock, no longer clears (another payment settled it
// meanwhile), the savepoint is rolled back and the payment is stored as
// PENDING_MANUAL_REVIEW with that sentence added to its reasons: a payment is
// never lost to a posting failure. A repeated (intake_source, external_ref)
// is ErrDuplicate; any other error is returned as is.
func (s Service) ProcessIncoming(ctx context.Context, tx pgx.Tx, in IntakePayload) (paymentsdb.Payment, error) {
	q := paymentsdb.New(tx)
	cands, err := openCandidates(ctx, q, in.Currency)
	if err != nil {
		return paymentsdb.Payment{}, err
	}
	inc := matching.Incoming{
		Amount: in.Amount, Currency: in.Currency, PayerName: in.PayerName,
		PayerReference: in.PayerReference, PaymentDate: in.PaymentDate,
	}
	res := matching.Match(inc, cands)

	var invoiceID, customerID pgtype.UUID
	if res.Invoice != nil {
		invoiceID = pgtype.UUID{Bytes: res.Invoice.InvoiceID, Valid: true}
		customerID = pgtype.UUID{Bytes: res.Invoice.CustomerID, Valid: true}
	}
	ref := in.ExternalRef
	p, err := insertPayment(ctx, q, paymentsdb.InsertPaymentParams{
		PaymentID: uuid.New(), InvoiceID: invoiceID, CustomerID: customerID,
		Amount: in.Amount, Currency: in.Currency, PayerName: in.PayerName, PayerReference: in.PayerReference,
		PaymentDate: pgDate(in.PaymentDate), IntakeSource: in.IntakeSource, ExternalRef: &ref,
		Status: res.Status, AdjustmentType: res.AdjustmentType,
		ConfidenceScore: decimal.NullDecimal{Decimal: res.Confidence, Valid: true},
	})
	if err != nil {
		return paymentsdb.Payment{}, err
	}

	action := actionHeld
	reasons := append([]string(nil), res.Reasons...)
	if res.Status == matching.StatusCleared {
		held, err := s.clearIncoming(ctx, tx, inc, p)
		if err != nil {
			return paymentsdb.Payment{}, err
		}
		if held != "" {
			if err := q.SetPaymentStatus(ctx, paymentsdb.SetPaymentStatusParams{PaymentID: p.PaymentID, Status: matching.StatusPending}); err != nil {
				return paymentsdb.Payment{}, fmt.Errorf("payments: hold payment: %w", err)
			}
			reasons = append(reasons, held)
		} else {
			action = actionCleared
		}
	}
	if err := logAction(ctx, q, p.PaymentID, action, reasons, nil); err != nil {
		return paymentsdb.Payment{}, err
	}
	return getPayment(ctx, q, p.PaymentID)
}

// clearIncoming clears the stored CLEARED payment p in a savepoint. held is
// the sentence to hold it with when it cannot clear (the savepoint is rolled
// back); it is "" when the payment cleared.
func (s Service) clearIncoming(ctx context.Context, tx pgx.Tx, inc matching.Incoming, p paymentsdb.Payment) (held string, err error) {
	sp, err := tx.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("payments: savepoint: %w", err)
	}
	defer func() { _ = sp.Rollback(ctx) }() // no-op once committed

	q := paymentsdb.New(sp)
	inv, found, err := lockInvoice(ctx, q, p.InvoiceID.Bytes)
	if err != nil {
		return "", err
	}
	if !found {
		return "", errors.New("CLEARED result without invoice — engine invariant violated")
	}
	// The candidates were read without a lock: a payment that cleared this
	// invoice meanwhile has changed it. Clear only if it still would.
	if !stillClears(inc, inv) {
		return fmt.Sprintf("invoice %s changed before clearing", inv.InvoiceID), nil
	}
	if err := s.clear(ctx, sp, p, &inv); err != nil {
		if errors.Is(err, ledger.ErrLedger) || errors.Is(err, fx.ErrRateMissing) {
			if rbErr := sp.Rollback(ctx); rbErr != nil {
				return "", fmt.Errorf("payments: roll back savepoint: %w", rbErr)
			}
			return err.Error(), nil
		}
		return "", err
	}
	// Exact within AmountEpsilon: any sub-epsilon residue counts as settled.
	inv.BalanceDue = decimal.Zero
	inv.Status = invoicePaid
	if err := saveInvoice(ctx, q, inv); err != nil {
		return "", err
	}
	if err := s.stampPaid(ctx, sp, inv.InvoiceID); err != nil {
		return "", err
	}
	if err := sp.Commit(ctx); err != nil {
		return "", fmt.Errorf("payments: release savepoint: %w", err)
	}
	return "", nil
}

// stillClears reports whether the locked invoice is still open and an exact
// match for the payment.
func stillClears(inc matching.Incoming, inv paymentsdb.LockInvoiceRow) bool {
	switch inv.Status {
	case invoiceOpen, invoiceSent, invoicePartial:
	default:
		return false
	}
	return matching.IsExactMatch(inc, matching.Candidate{
		Currency: inv.Currency, BalanceDue: inv.BalanceDue, WhtOutstanding: inv.WhtAmount.Sub(inv.WhtBooked),
	})
}

// ApproveManual clears a payment that is not CLEARED (PENDING_MANUAL_REVIEW
// or FLAGGED) against the invoice an admin chose (Python's
// approve_manual_match, SR:167-213). It locks the payment, then the invoice.
// The payment takes the invoice and its customer, status CLEARED and adj
// (stored only). Same currency, the balance drops by the payment plus any
// withholding booked and the invoice is PAID at 0, PARTIAL otherwise; across
// currencies it is settled in full with an FX gain or loss. PAID stamps
// Hosting (the caller calls AfterCommit once committed). An OVERRIDDEN log
// row names the invoice; actor may be nil (MCP).
//
// Refusals are ErrPayment with Python's sentence, plus Go's own: a DRAFT or
// VOID invoice is "cannot approve payment against <STATUS> invoice", and a
// payment with no cash (a reversed withholding-only manual payment) is
// "payment <id> has no cash amount; record the withholding again". A PAID
// invoice, another currency and an amount above the balance are accepted as
// in Python. A ledger refusal or a missing rate is returned unchanged
// (errors.Is ledger.ErrLedger or fx.ErrRateMissing; its text is Python's
// sentence) for the route to answer 400. The caller rolls back on any error.
// An MCP caller must also call Service.AfterCommit after its commit.
func (s Service) ApproveManual(ctx context.Context, tx pgx.Tx, paymentID, invoiceID uuid.UUID, actor *uuid.UUID, adj string) (paymentsdb.Payment, error) {
	q := paymentsdb.New(tx)
	p, err := lockPayment(ctx, q, paymentID)
	if err != nil {
		return paymentsdb.Payment{}, err
	}
	if p.Status == matching.StatusCleared {
		return paymentsdb.Payment{}, refuse("payment %s already CLEARED", paymentID)
	}
	// Only a reversed withholding-only manual payment (PORT-NOTES 161) has
	// no cash; clear would post amount 0 without its withholding.
	if !p.Amount.IsPositive() {
		return paymentsdb.Payment{}, refuse("payment %s has no cash amount; record the withholding again", paymentID)
	}
	inv, found, err := lockInvoice(ctx, q, invoiceID)
	if err != nil {
		return paymentsdb.Payment{}, err
	}
	if !found {
		return paymentsdb.Payment{}, refuse("invoice %s not found", invoiceID)
	}
	if inv.Status == invoiceDraft || inv.Status == invoiceVoid {
		return paymentsdb.Payment{}, refuse("cannot approve payment against %s invoice", inv.Status)
	}

	p.InvoiceID = pgtype.UUID{Bytes: inv.InvoiceID, Valid: true}
	p.CustomerID = inv.CustomerID
	p.Status = matching.StatusCleared
	p.AdjustmentType = adj
	if err := q.SetPaymentMatch(ctx, paymentsdb.SetPaymentMatchParams{
		PaymentID: p.PaymentID, InvoiceID: p.InvoiceID, CustomerID: p.CustomerID, Status: p.Status, AdjustmentType: adj,
	}); err != nil {
		return paymentsdb.Payment{}, fmt.Errorf("payments: set match: %w", err)
	}
	if err := s.clear(ctx, tx, p, &inv); err != nil {
		return paymentsdb.Payment{}, err
	}
	paid := inv.BalanceDue.IsZero()
	inv.Status = invoicePartial
	if paid {
		inv.Status = invoicePaid
	}
	if err := saveInvoice(ctx, q, inv); err != nil {
		return paymentsdb.Payment{}, err
	}
	if paid {
		if err := s.stampPaid(ctx, tx, inv.InvoiceID); err != nil {
			return paymentsdb.Payment{}, err
		}
	}
	reasons := []string{fmt.Sprintf("manual approval against invoice %s", inv.InvoiceID)}
	if err := logAction(ctx, q, p.PaymentID, actionOverridden, reasons, actor); err != nil {
		return paymentsdb.Payment{}, err
	}
	return getPayment(ctx, q, p.PaymentID)
}

// Reverse undoes a CLEARED payment (Python's reverse_payment, SR:216-258):
// it reverses the payment's active journal entry (ledger.ActiveEntryFor: the
// newest that is neither reversed nor a reversal) with memo
// "Reversal: <reason>", flags the payment, and re-opens its invoice: the
// balance grows by amount + wht_amount, wht_booked shrinks by wht_amount,
// and the status is OPEN. The hosting restore is not undone. A REVERSED log
// row holds the reason. Locks: the payment, then the invoice, then (in the
// ledger) the entry.
//
// Refusals are ErrPayment with Python's sentence. A ledger refusal is
// returned unchanged (errors.Is ledger.ErrLedger) for the route's 400; a
// wht_booked that would go negative is a plain error (Python's RuntimeError).
// The caller rolls back on any error.
func (s Service) Reverse(ctx context.Context, tx pgx.Tx, paymentID uuid.UUID, actor *uuid.UUID, reason string) (paymentsdb.Payment, error) {
	q := paymentsdb.New(tx)
	p, err := lockPayment(ctx, q, paymentID)
	if err != nil {
		return paymentsdb.Payment{}, err
	}
	if p.Status != matching.StatusCleared {
		return paymentsdb.Payment{}, refuse("only CLEARED payments can be reversed (got %s)", p.Status)
	}
	entryID, ok, err := ledger.ActiveEntryFor(ctx, tx, sourcePayment, p.PaymentID)
	if err != nil {
		return paymentsdb.Payment{}, err
	}
	if !ok {
		return paymentsdb.Payment{}, refuse("no active journal entry to reverse for payment %s", paymentID)
	}
	var inv paymentsdb.LockInvoiceRow
	found := false
	if p.InvoiceID.Valid {
		if inv, found, err = lockInvoice(ctx, q, p.InvoiceID.Bytes); err != nil {
			return paymentsdb.Payment{}, err
		}
	}

	memo := "Reversal: " + reason
	if _, err := ledger.PostReversal(ctx, tx, s.BaseCurrency, entryID, &memo); err != nil {
		return paymentsdb.Payment{}, err
	}
	if err := q.SetPaymentStatus(ctx, paymentsdb.SetPaymentStatusParams{PaymentID: p.PaymentID, Status: matching.StatusFlagged}); err != nil {
		return paymentsdb.Payment{}, fmt.Errorf("payments: flag payment: %w", err)
	}
	if found {
		inv.BalanceDue = inv.BalanceDue.Add(p.Amount).Add(p.WhtAmount)
		inv.WhtBooked = inv.WhtBooked.Sub(p.WhtAmount)
		if inv.WhtBooked.IsNegative() {
			return paymentsdb.Payment{}, fmt.Errorf("invoice %s wht_booked went negative on reversal of payment %s — withholding bookkeeping invariant violated",
				inv.InvoiceID, p.PaymentID)
		}
		// Re-open as OPEN: whether the invoice was emailed is not stored.
		inv.Status = invoiceOpen
		if err := saveInvoice(ctx, q, inv); err != nil {
			return paymentsdb.Payment{}, err
		}
	}
	if err := logAction(ctx, q, p.PaymentID, actionReversed, []string{reason}, actor); err != nil {
		return paymentsdb.Payment{}, err
	}
	return getPayment(ctx, q, p.PaymentID)
}
