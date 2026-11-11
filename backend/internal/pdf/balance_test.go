package pdf

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/payments/paymentsdb"
)

// bp is a payment on day (of Jan 2026) for the balance replay.
func bp(day int, amount, wht, currency, status string) paymentsdb.Payment {
	return paymentsdb.Payment{
		PaymentID: uuid.New(), Amount: dec(amount), WhtAmount: dec(wht), Currency: currency, Status: status,
		PaymentDate: pgtype.Date{Time: time.Date(2026, 1, day, 0, 0, 0, 0, time.UTC), Valid: true},
	}
}

func balanceAfter(t *testing.T, inv string, ps []paymentsdb.Payment, upTo int) string {
	t.Helper()
	return BalanceAfter(dec(inv), "IDR", ps, ps[upTo].PaymentID).String()
}

func TestBalanceAfter(t *testing.T) {
	t.Parallel()
	t.Run("one full payment is 0", func(t *testing.T) {
		t.Parallel()
		eq(t, "balance", balanceAfter(t, "1000", []paymentsdb.Payment{bp(1, "1000", "0", "IDR", "CLEARED")}, 0), "0")
	})
	t.Run("two partials", func(t *testing.T) {
		t.Parallel()
		ps := []paymentsdb.Payment{bp(1, "400", "0", "IDR", "CLEARED"), bp(2, "600", "0", "IDR", "CLEARED")}
		eq(t, "after first", balanceAfter(t, "1000", ps, 0), "600")
		eq(t, "after second", balanceAfter(t, "1000", ps, 1), "0")
	})
	t.Run("input order does not matter", func(t *testing.T) {
		t.Parallel()
		ps := []paymentsdb.Payment{bp(2, "600", "0", "IDR", "CLEARED"), bp(1, "400", "0", "IDR", "CLEARED")}
		eq(t, "after the 400 (index 1)", balanceAfter(t, "1000", ps, 1), "600")
	})
	t.Run("withholding relieves the balance too", func(t *testing.T) {
		t.Parallel()
		eq(t, "balance", balanceAfter(t, "500", []paymentsdb.Payment{bp(1, "475", "25", "IDR", "CLEARED")}, 0), "0")
	})
	t.Run("a reversed payment is skipped", func(t *testing.T) {
		t.Parallel()
		// Reverse leaves the payment FLAGGED: it no longer relieves the invoice.
		ps := []paymentsdb.Payment{bp(1, "400", "0", "IDR", "CLEARED"), bp(2, "300", "0", "IDR", "FLAGGED"), bp(3, "100", "0", "IDR", "CLEARED")}
		eq(t, "after third", balanceAfter(t, "1000", ps, 2), "500")
	})
	t.Run("cross-currency payment settles the whole balance", func(t *testing.T) {
		t.Parallel()
		ps := []paymentsdb.Payment{bp(1, "100", "0", "IDR", "CLEARED"), bp(2, "40", "0", "USD", "CLEARED")}
		eq(t, "after first", balanceAfter(t, "1000", ps, 0), "900")
		eq(t, "after second", balanceAfter(t, "1000", ps, 1), "0")
	})
	t.Run("overpayment clamps to 0", func(t *testing.T) {
		t.Parallel()
		eq(t, "balance", balanceAfter(t, "1000", []paymentsdb.Payment{bp(1, "1500", "0", "IDR", "CLEARED")}, 0), "0")
	})
	t.Run("same day orders by created_at then id", func(t *testing.T) {
		t.Parallel()
		a, b := bp(1, "400", "0", "IDR", "CLEARED"), bp(1, "600", "0", "IDR", "CLEARED")
		t0 := time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC)
		t1 := t0.Add(time.Minute)
		a.CreatedAt, b.CreatedAt = &t1, &t0 // b was created first
		ps := []paymentsdb.Payment{a, b}
		eq(t, "after b", BalanceAfter(dec("1000"), "IDR", ps, b.PaymentID).String(), "400")
		eq(t, "after a", BalanceAfter(dec("1000"), "IDR", ps, a.PaymentID).String(), "0")
	})
	t.Run("unknown upTo is the balance after every cleared payment", func(t *testing.T) {
		t.Parallel()
		ps := []paymentsdb.Payment{bp(1, "400", "0", "IDR", "CLEARED"), bp(2, "100", "0", "IDR", "FLAGGED")}
		got := BalanceAfter(dec("1000"), "IDR", ps, uuid.New())
		eq(t, "balance", got.String(), "600")
	})
	t.Run("no payments is the invoice amount", func(t *testing.T) {
		t.Parallel()
		eq(t, "balance", BalanceAfter(dec("1000"), "IDR", nil, uuid.New()).Equal(decimal.NewFromInt(1000)), true)
	})
}
