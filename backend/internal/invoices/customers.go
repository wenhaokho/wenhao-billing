package invoices

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices/invoicesdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// The customer invoice views (customers.py:24-74, :137-219), served under
// /api/v1/customers beside the customers package's own routes. chi matches
// the literal /balances before /{customer_id}.

// CustomerBalanceRow is a customer's open receivables in one currency.
type CustomerBalanceRow struct {
	CustomerID uuid.UUID     `json:"customer_id"`
	Currency   string        `json:"currency"`
	Balance    platform.Dec4 `json:"balance" doc:"The balance due of the OPEN, SENT and PARTIAL invoices (never templates)."`
	Overdue    platform.Dec4 `json:"overdue" doc:"The part of the balance on invoices due before today."`
}

// CurrencyAmount is a total in one currency.
type CurrencyAmount struct {
	Currency string        `json:"currency"`
	Amount   platform.Dec4 `json:"amount"`
}

// BillingSummary is a customer's open invoices by type, what is owed and what
// was paid, and the latest invoice. Recurring templates are included in the
// lists and in total_unpaid, as Python does.
type BillingSummary struct {
	Milestone        []InvoiceOut     `json:"milestone" nullable:"false"`
	Recurring        []InvoiceOut     `json:"recurring" nullable:"false"`
	Usage            []InvoiceOut     `json:"usage" nullable:"false"`
	UnpaidInvoices   []InvoiceOut     `json:"unpaid_invoices" nullable:"false" doc:"The DRAFT, OPEN, SENT and PARTIAL invoices, due date first (none last)."`
	TotalUnpaid      []CurrencyAmount `json:"total_unpaid" nullable:"false" doc:"The balance due of unpaid_invoices, DRAFT included, by currency."`
	PaidLast12Months []CurrencyAmount `json:"paid_last_12_months" nullable:"false" doc:"The cash of CLEARED payments dated in the last 365 days, by currency."`

	LastInvoiceIssueDate *platform.Date `json:"last_invoice_issue_date"`
	LastInvoiceID        *uuid.UUID     `json:"last_invoice_id"`
}

// CustomerBalances is the open receivables per customer and currency, the
// overdue part being the invoices due before today; ordered by customer,
// currency. Never nil.
func CustomerBalances(ctx context.Context, q *invoicesdb.Queries, today time.Time) ([]CustomerBalanceRow, error) {
	rows, err := q.ListCustomerBalances(ctx, pgtype.Date{Time: today, Valid: true})
	if err != nil {
		return nil, err
	}
	out := make([]CustomerBalanceRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, CustomerBalanceRow{
			CustomerID: uuid.UUID(r.CustomerID.Bytes), Currency: r.Currency,
			Balance: platform.NewDec4(r.Balance), Overdue: platform.NewDec4(r.Overdue),
		})
	}
	return out, nil
}

func registerCustomerViews(api huma.API, r routes) {
	const prefix = "/api/v1/customers"
	tags := []string{"invoices"}
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "customers-balances", Method: http.MethodGet, Path: prefix + "/balances", Tags: tags,
		Summary: "Open receivables per customer and currency, with the overdue part",
	}), r.customerBalances)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "customers-billing-summary", Method: http.MethodGet, Path: prefix + "/{customer_id}/billing-summary", Tags: tags,
		Summary: "A customer's open invoices, totals owed and paid, and latest invoice", Errors: []int{http.StatusNotFound},
	}), r.billingSummary)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "customers-invoices", Method: http.MethodGet, Path: prefix + "/{customer_id}/invoices", Tags: tags,
		Summary: "Every invoice of a customer (templates too), latest issue date first", Errors: []int{http.StatusNotFound},
	}), r.customerInvoices)
}

type balancesOut struct{ Body []CustomerBalanceRow }

func (r routes) customerBalances(ctx context.Context, _ *struct{}) (*balancesOut, error) {
	rows, err := CustomerBalances(ctx, r.q, platform.TodayUTC(r.clock))
	if err != nil {
		return nil, err
	}
	return &balancesOut{Body: rows}, nil
}

type customerIn struct {
	CustomerID uuid.UUID `path:"customer_id"`
}

// requireCustomer is a 404 "customer not found" when there is no such customer.
func (r routes) requireCustomer(ctx context.Context, id uuid.UUID) error {
	if _, err := r.q.GetCustomerName(ctx, id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return platform.Err(http.StatusNotFound, "customer_not_found", "customer not found")
		}
		return err
	}
	return nil
}

type summaryOut struct{ Body BillingSummary }

func (r routes) billingSummary(ctx context.Context, in *customerIn) (*summaryOut, error) {
	if err := r.requireCustomer(ctx, in.CustomerID); err != nil {
		return nil, err
	}
	cust := pgtype.UUID{Bytes: in.CustomerID, Valid: true}
	open, err := r.q.ListOpenInvoicesOfCustomer(ctx, cust)
	if err != nil {
		return nil, err
	}
	invoices, err := list(ctx, r.q, open)
	if err != nil {
		return nil, err
	}
	s := BillingSummary{
		Milestone: []InvoiceOut{}, Recurring: []InvoiceOut{}, Usage: []InvoiceOut{}, UnpaidInvoices: invoices,
	}
	unpaid := map[string]decimal.Decimal{}
	for _, inv := range invoices {
		switch inv.InvoiceType {
		case "MILESTONE":
			s.Milestone = append(s.Milestone, inv)
		case "RECURRING":
			s.Recurring = append(s.Recurring, inv)
		case "USAGE":
			s.Usage = append(s.Usage, inv)
		}
		unpaid[inv.Currency] = unpaid[inv.Currency].Add(inv.BalanceDue.Decimal())
	}
	s.TotalUnpaid = currencyAmounts(unpaid)

	// A year is 365 days, not a calendar year (customers.py:172).
	since := platform.TodayUTC(r.clock).AddDate(0, 0, -365)
	paid, err := r.q.SumClearedPaymentsSince(ctx, invoicesdb.SumClearedPaymentsSinceParams{
		CustomerID: cust, Since: pgtype.Date{Time: since, Valid: true},
	})
	if err != nil {
		return nil, err
	}
	paidByCurrency := make(map[string]decimal.Decimal, len(paid))
	for _, p := range paid {
		paidByCurrency[p.Currency] = p.Amount
	}
	s.PaidLast12Months = currencyAmounts(paidByCurrency)

	latest, err := r.q.GetLatestInvoiceOfCustomer(ctx, cust)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
	case err != nil:
		return nil, err
	default:
		s.LastInvoiceIssueDate = datePtr(latest.IssueDate)
		s.LastInvoiceID = &latest.InvoiceID
	}
	return &summaryOut{Body: s}, nil
}

// currencyAmounts is the totals as a list sorted by currency code, never nil.
func currencyAmounts(totals map[string]decimal.Decimal) []CurrencyAmount {
	out := make([]CurrencyAmount, 0, len(totals))
	for c, a := range totals {
		out = append(out, CurrencyAmount{Currency: c, Amount: platform.NewDec4(a)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Currency < out[j].Currency })
	return out
}

func (r routes) customerInvoices(ctx context.Context, in *customerIn) (*listOut, error) {
	if err := r.requireCustomer(ctx, in.CustomerID); err != nil {
		return nil, err
	}
	invs, err := r.q.ListInvoicesOfCustomer(ctx, pgtype.UUID{Bytes: in.CustomerID, Valid: true})
	if err != nil {
		return nil, err
	}
	out, err := list(ctx, r.q, invs)
	if err != nil {
		return nil, err
	}
	return &listOut{Body: out}, nil
}
