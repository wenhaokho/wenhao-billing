// Package ledgertest helps other packages' tests set up ledger state. It is
// imported by tests only.
package ledgertest

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/ledger"
)

// Base is the base currency the fixtures post in (apitest seeds X→IDR rates).
const Base = "IDR"

// IssueFixture posts the issue entry of an invoice a test inserted by SQL
// (PORT-NOTES 163: a payment needs one): Dr receivable / Cr 4000 for the
// row's amount in its currency, at the rate of, and posted on, its
// issue_date (else its created_at date).
func IssueFixture(t *testing.T, tx pgx.Tx, invoiceID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	var currency string
	var amount decimal.Decimal
	var on time.Time
	if err := tx.QueryRow(ctx, `SELECT currency, amount, COALESCE(issue_date, created_at::date)::timestamp
FROM invoices WHERE invoice_id = $1`, invoiceID).Scan(&currency, &amount, &on); err != nil {
		t.Fatalf("ledgertest: invoice %s: %v", invoiceID, err)
	}
	accounts, err := ledger.AccountsByCode(ctx, tx, []string{ledger.FixedFeeCode})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.PostIssued(ctx, tx, Base, ledger.Document{
		SourceType: ledger.SourceInvoice, ID: invoiceID, Currency: currency, Amount: amount,
		RateDate: on, IssueDate: on,
		Shares: []ledger.Share{{AccountID: accounts[ledger.FixedFeeCode].ID, Amount: amount}},
	}); err != nil {
		t.Fatalf("ledgertest: issue invoice %s: %v", invoiceID, err)
	}
}
