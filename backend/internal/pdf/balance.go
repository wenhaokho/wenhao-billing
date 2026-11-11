package pdf

import (
	"bytes"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/matching"
	"github.com/wenhaokho/wenhao-billing/backend/internal/payments/paymentsdb"
)

// statusCleared is the status of a payment that relieved its invoice.
const statusCleared = "CLEARED"

// Invoice is what BalanceAfter needs of the invoice: its gross Amount (what
// balance_due starts at when it is issued), its Currency, its stored
// BalanceDue and, per payment, when it was cleared (ClearedAt, from
// paymentsdb.ListInvoiceClearTimes; payments with no entry sort first).
type Invoice struct {
	Amount     decimal.Decimal
	Currency   string
	BalanceDue decimal.Decimal
	ClearedAt  map[uuid.UUID]time.Time
}

// BalanceAfter is an invoice's balance immediately after the payment upTo:
// what a receipt shows, so an old partial payment's receipt never reads
// "Paid in full" because later payments settled the invoice.
//
// It replays the CLEARED payments of the invoice from inv.Amount in the order
// they were cleared (inv.ClearedAt, the log row that cleared each, then
// payment date, created_at, payment id), and stops after upTo. A payment in
// the invoice's currency relieves it by amount + wht_amount, never below 0; a
// payment in another currency settles the whole balance (0); a balance within
// matching.AmountEpsilon of 0 is 0 (auto-clear settles such a residue). That
// is the rule of payments.Service.clear and of recordManual
// (internal/payments): change one, change this. Payments that are not CLEARED
// (a reversal leaves the payment FLAGGED) relieve nothing and are skipped.
//
// The newest CLEARED payment (and an upTo that is not among them) gets the
// stored inv.BalanceDue, which is authoritative: a reversal re-derives it
// from the ledger, not from the payments.
//
// Limitation: a reversal on an invoice that also had a cross-currency payment
// is re-derived from the ledger, so the replayed balance of an OLDER receipt
// can differ from what it showed when it was issued (PORT-NOTES 171).
func BalanceAfter(inv Invoice, payments []paymentsdb.Payment, upTo uuid.UUID) decimal.Decimal {
	cleared := clearedInOrder(inv, payments)
	if n := len(cleared); n == 0 {
		return inv.Amount
	} else if cleared[n-1].PaymentID == upTo {
		return inv.BalanceDue
	}
	return replay(inv, cleared, upTo)
}

// BalanceBefore is an invoice's balance immediately before the payment upTo:
// the balance after the payment cleared just before it (inv.Amount for the
// first), so a receipt's box reads Invoice total − Previously paid − This
// payment = Balance due.
//
// For the newest CLEARED payment in the invoice's currency it is
// BalanceAfter's stored inv.BalanceDue plus this payment's relief (amount +
// wht_amount), so before − relief = after even when the stored balance and
// the replay differ (a reversal re-derived it); when that balance is 0 the
// relief may have been clamped, and the replay's previous balance is used.
// An upTo that is not CLEARED relieved nothing: its before is its after.
func BalanceBefore(inv Invoice, payments []paymentsdb.Payment, upTo uuid.UUID) decimal.Decimal {
	cleared := clearedInOrder(inv, payments)
	at := -1
	for i, p := range cleared {
		if p.PaymentID == upTo {
			at = i
		}
	}
	if at < 0 {
		return BalanceAfter(inv, payments, upTo)
	}
	if p := cleared[at]; at == len(cleared)-1 && p.Currency == inv.Currency && inv.BalanceDue.IsPositive() {
		return inv.BalanceDue.Add(p.Amount).Add(p.WhtAmount)
	}
	if at == 0 {
		return inv.Amount
	}
	return replay(inv, cleared, cleared[at-1].PaymentID)
}

// Replay is BalanceAfter's replay alone, with no stored-balance shortcut for
// the newest payment: tests compare it with the stored balance_due to catch
// drift from payments.Service.clear and recordManual.
func Replay(inv Invoice, payments []paymentsdb.Payment, upTo uuid.UUID) decimal.Decimal {
	return replay(inv, clearedInOrder(inv, payments), upTo)
}

func clearedInOrder(inv Invoice, payments []paymentsdb.Payment) []paymentsdb.Payment {
	cleared := make([]paymentsdb.Payment, 0, len(payments))
	for _, p := range payments {
		if p.Status == statusCleared {
			cleared = append(cleared, p)
		}
	}
	sort.SliceStable(cleared, func(i, j int) bool { return paymentBefore(inv.ClearedAt, cleared[i], cleared[j]) })
	return cleared
}

func replay(inv Invoice, cleared []paymentsdb.Payment, upTo uuid.UUID) decimal.Decimal {
	balance := inv.Amount
	for _, p := range cleared {
		if p.Currency == inv.Currency {
			balance = decimal.Max(decimal.Zero, balance.Sub(p.Amount.Add(p.WhtAmount)))
		} else {
			balance = decimal.Zero
		}
		if balance.LessThanOrEqual(matching.AmountEpsilon) {
			balance = decimal.Zero
		}
		if p.PaymentID == upTo {
			break
		}
	}
	return balance
}

// paymentBefore orders payments by clearing time, date, creation time, then id.
func paymentBefore(clearedAt map[uuid.UUID]time.Time, a, b paymentsdb.Payment) bool {
	if ca, cb := clearedAt[a.PaymentID], clearedAt[b.PaymentID]; !ca.Equal(cb) {
		return ca.Before(cb)
	}
	if !a.PaymentDate.Time.Equal(b.PaymentDate.Time) {
		return a.PaymentDate.Time.Before(b.PaymentDate.Time)
	}
	if ta, tb := createdAt(a), createdAt(b); !ta.Equal(tb) {
		return ta.Before(tb)
	}
	return bytes.Compare(a.PaymentID[:], b.PaymentID[:]) < 0
}

func createdAt(p paymentsdb.Payment) time.Time {
	if p.CreatedAt == nil {
		return time.Time{}
	}
	return *p.CreatedAt
}
