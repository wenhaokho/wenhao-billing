package pdf

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/wenhaokho/wenhao-billing/backend/internal/business/businessdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/customers/customersdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices/invoicesdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/payments/paymentsdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/quotations/quotationsdb"
)

// DBTX is a pool, a connection or a transaction (sqlc's own interface).
type DBTX interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

// ErrNoInvoice is a payment that is not applied to an invoice: it has no
// receipt.
var ErrNoInvoice = errors.New("payment has no invoice")

// LoadInvoice reads an invoice, its lines by position, its customer, the
// business profile and the payment accounts. A missing invoice is
// pgx.ErrNoRows. A missing customer or business row is nil (Python's db.get
// returned None), and the profile is never created here.
func LoadInvoice(ctx context.Context, q DBTX, id uuid.UUID) (InvoiceData, error) {
	iq := invoicesdb.New(q)
	inv, err := iq.GetInvoice(ctx, id)
	if err != nil {
		return InvoiceData{}, err
	}
	lines, err := iq.ListInvoiceLines(ctx, id)
	if err != nil {
		return InvoiceData{}, err
	}
	cust, err := loadCustomer(ctx, q, inv.CustomerID)
	if err != nil {
		return InvoiceData{}, err
	}
	biz, accounts, err := loadBusiness(ctx, q)
	if err != nil {
		return InvoiceData{}, err
	}
	return InvoiceData{Invoice: inv, Lines: lines, Customer: cust, Business: biz, Accounts: accounts}, nil
}

// LoadQuotation is LoadInvoice for a quotation.
func LoadQuotation(ctx context.Context, q DBTX, id uuid.UUID) (QuotationData, error) {
	qq := quotationsdb.New(q)
	quo, err := qq.GetQuotation(ctx, id)
	if err != nil {
		return QuotationData{}, err
	}
	lines, err := qq.ListQuotationLines(ctx, id)
	if err != nil {
		return QuotationData{}, err
	}
	cust, err := loadCustomer(ctx, q, pgtype.UUID{Bytes: quo.CustomerID, Valid: true})
	if err != nil {
		return QuotationData{}, err
	}
	biz, accounts, err := loadBusiness(ctx, q)
	if err != nil {
		return QuotationData{}, err
	}
	return QuotationData{Quotation: quo, Lines: lines, Customer: cust, Business: biz, Accounts: accounts}, nil
}

// LoadReceipt reads a payment, its invoice, the invoice's customer and the
// business profile, and the invoice's balance after the payment (BalanceAfter). A missing payment or invoice is pgx.ErrNoRows; a payment
// with no invoice is ErrNoInvoice.
func LoadReceipt(ctx context.Context, q DBTX, paymentID uuid.UUID) (ReceiptData, error) {
	pay, err := paymentsdb.New(q).GetPayment(ctx, paymentID)
	if err != nil {
		return ReceiptData{}, err
	}
	if !pay.InvoiceID.Valid {
		return ReceiptData{}, fmt.Errorf("%w: %s", ErrNoInvoice, paymentID)
	}
	inv, err := invoicesdb.New(q).GetInvoice(ctx, pay.InvoiceID.Bytes)
	if err != nil {
		return ReceiptData{}, err
	}
	cust, err := loadCustomer(ctx, q, inv.CustomerID)
	if err != nil {
		return ReceiptData{}, err
	}
	biz, _, err := loadBusiness(ctx, q)
	if err != nil {
		return ReceiptData{}, err
	}
	// The invoice's balance right after this payment, not the live one.
	siblings, err := paymentsdb.New(q).ListInvoicePayments(ctx, pay.InvoiceID)
	if err != nil {
		return ReceiptData{}, err
	}
	times, err := paymentsdb.New(q).ListInvoiceClearTimes(ctx, pay.InvoiceID)
	if err != nil {
		return ReceiptData{}, err
	}
	clearedAt := make(map[uuid.UUID]time.Time, len(times))
	for _, t := range times {
		clearedAt[t.PaymentID] = t.ClearedAt
	}
	bi := Invoice{Amount: inv.Amount, Currency: inv.Currency, BalanceDue: inv.BalanceDue, ClearedAt: clearedAt}
	return ReceiptData{
		Payment: pay, Invoice: inv, Customer: cust, Business: biz,
		BalanceBefore: BalanceBefore(bi, siblings, pay.PaymentID),
		BalanceAfter:  BalanceAfter(bi, siblings, pay.PaymentID),
	}, nil
}

// loadCustomer is nil for no customer id or a missing row.
func loadCustomer(ctx context.Context, q DBTX, id pgtype.UUID) (*customersdb.Customer, error) {
	if !id.Valid {
		return nil, nil //nolint:nilnil // no customer is a value: the document prints a dash
	}
	c, err := customersdb.New(q).GetCustomer(ctx, id.Bytes)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil //nolint:nilnil // as above
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// loadBusiness reads singleton row 1 as it is. It does not create it, as the
// profile GET does: a document renders with no business row. The accounts
// are those of the row, by currency.
func loadBusiness(ctx context.Context, q DBTX) (*businessdb.BusinessProfile, []businessdb.PaymentAccount, error) {
	bq := businessdb.New(q)
	b, err := bq.GetProfile(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	accounts, err := bq.ListPaymentAccounts(ctx)
	if err != nil {
		return nil, nil, err
	}
	return &b, accounts, nil
}
