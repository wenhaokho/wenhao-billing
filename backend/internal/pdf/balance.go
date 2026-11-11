package pdf

import (
	"bytes"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/payments/paymentsdb"
)

// statusCleared is the status of a payment that relieved its invoice.
const statusCleared = "CLEARED"

// BalanceAfter is an invoice's balance immediately after the payment upTo:
// what a receipt shows, so an old partial payment's receipt never reads
// "Paid in full" because later payments settled the invoice.
//
// It replays the CLEARED payments of the invoice in order (payment date, then
// created_at, then payment id) from invoiceAmount, the invoice's gross
// amount (what balance_due starts at when it is issued), and stops after
// upTo. A payment in the invoice's currency relieves it by amount +
// wht_amount, never below 0; a payment in another currency settles the whole
// balance (0). That is the rule of payments.Service.clear and of recordManual
// (internal/payments): change one, change this. Payments that are not CLEARED
// (a reversal leaves the payment FLAGGED) relieve nothing and are skipped.
// If upTo is not among the CLEARED payments, the result is the balance after
// all of them.
func BalanceAfter(invoiceAmount decimal.Decimal, invoiceCurrency string, payments []paymentsdb.Payment, upTo uuid.UUID) decimal.Decimal {
	cleared := make([]paymentsdb.Payment, 0, len(payments))
	for _, p := range payments {
		if p.Status == statusCleared {
			cleared = append(cleared, p)
		}
	}
	sort.SliceStable(cleared, func(i, j int) bool { return paymentBefore(cleared[i], cleared[j]) })

	balance := invoiceAmount
	for _, p := range cleared {
		if p.Currency == invoiceCurrency {
			balance = decimal.Max(decimal.Zero, balance.Sub(p.Amount.Add(p.WhtAmount)))
		} else {
			balance = decimal.Zero
		}
		if p.PaymentID == upTo {
			break
		}
	}
	return balance
}

// paymentBefore orders payments by date, creation time, then id.
func paymentBefore(a, b paymentsdb.Payment) bool {
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
