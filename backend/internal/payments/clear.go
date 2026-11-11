package payments

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/ledger"
	"github.com/wenhaokho/wenhao-billing/backend/internal/matching"
	"github.com/wenhaokho/wenhao-billing/backend/internal/payments/paymentsdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/withholding"
)

// clear posts the CLEARED payment p to the ledger against the locked invoice
// inv and relieves inv in memory (Python's _clear_against_invoice, SR:82-107).
// The caller sets inv's status and saves it.
//
// The outstanding withholding is booked only when the payment settles the
// invoice: same currency and exactly the expected net cash, or any
// cross-currency payment (which settles the whole balance). Any other amount
// books none. The payment's wht_amount is set to what is booked, the ledger
// is posted before the balance changes (the cross-currency receivable is the
// balance), then wht_booked grows by it and the balance drops: by amount +
// withheld clamped at 0 in the same currency, to 0 across currencies.
//
// A ledger refusal or a missing rate comes back unchanged (errors.Is
// ledger.ErrLedger or fx.ErrRateMissing), with the payment's wht_amount
// already written: the caller rolls back.
func (s Service) clear(ctx context.Context, tx pgx.Tx, p paymentsdb.Payment, inv *paymentsdb.LockInvoiceRow) error {
	q := paymentsdb.New(tx)
	sameCurrency := p.Currency == inv.Currency
	outstanding := inv.WhtAmount.Sub(inv.WhtBooked)
	settles := !sameCurrency ||
		p.Amount.Sub(withholding.ExpectedCash(inv.BalanceDue, inv.WhtAmount, inv.WhtBooked)).Abs().LessThanOrEqual(matching.AmountEpsilon)
	withheld := decimal.Zero
	if settles {
		withheld = outstanding
	}

	if err := q.SetPaymentWithheld(ctx, paymentsdb.SetPaymentWithheldParams{PaymentID: p.PaymentID, WhtAmount: withheld}); err != nil {
		return fmt.Errorf("payments: set wht_amount: %w", err)
	}
	if err := s.postCleared(ctx, tx, p, *inv, withheld); err != nil {
		return err
	}
	inv.WhtBooked = inv.WhtBooked.Add(withheld)
	if sameCurrency {
		inv.BalanceDue = decimal.Max(decimal.Zero, inv.BalanceDue.Sub(p.Amount.Add(withheld)))
	} else {
		// The base-currency difference is in the FX gain or loss line.
		inv.BalanceDue = decimal.Zero
	}
	return nil
}

// postCleared posts the CLEARED payment p's journal entry in tx against inv
// as it stands before p relieves it, with withheld booked to Prepaid Tax. It
// is the one posting of a cleared payment: clear (intake and approval) and
// recordManual (record-payment) both call it, after locking the invoice, so
// the entry is written under the invoice lock and rolls back with the caller.
// A ledger refusal or a missing rate comes back unchanged.
func (s Service) postCleared(ctx context.Context, tx pgx.Tx, p paymentsdb.Payment, inv paymentsdb.LockInvoiceRow, withheld decimal.Decimal) error {
	_, err := ledger.PostPaymentCleared(ctx, tx, s.BaseCurrency,
		ledger.Payment{ID: p.PaymentID, Status: p.Status, Amount: p.Amount, Currency: p.Currency, Date: p.PaymentDate.Time},
		ledger.Invoice{ID: inv.InvoiceID, Currency: inv.Currency, BalanceDue: inv.BalanceDue},
		withheld)
	return err
}
