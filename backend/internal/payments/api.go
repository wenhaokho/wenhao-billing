// Package payments stores incoming payments and reconciles them: the intake
// pipeline (match, store, clear or hold), manual approval, reversal, and
// their routes. Clearing posts to the ledger and relieves the invoice in the
// caller's transaction, as Python's services/reconciliation.py does.
package payments

import (
	"errors"
	"fmt"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wenhaokho/wenhao-billing/backend/internal/payments/paymentsdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// PaymentOut is a payment as the routes answer it, in the field order of
// Python's PaymentOut (schemas/payment.py:11-28). Decimals print at the
// stored scale: amounts at 4 places, the confidence at 2.
type PaymentOut struct {
	PaymentID       uuid.UUID      `json:"payment_id"`
	InvoiceID       *uuid.UUID     `json:"invoice_id"`
	CustomerID      *uuid.UUID     `json:"customer_id"`
	Amount          platform.Dec4  `json:"amount"`
	WhtAmount       platform.Dec4  `json:"wht_amount"`
	Currency        string         `json:"currency"`
	PayerName       string         `json:"payer_name"`
	PayerReference  *string        `json:"payer_reference"`
	PaymentDate     platform.Date  `json:"payment_date"`
	IntakeSource    string         `json:"intake_source"`
	ExternalRef     *string        `json:"external_ref"`
	Status          string         `json:"status"`
	AdjustmentType  string         `json:"adjustment_type"`
	ConfidenceScore *platform.Dec2 `json:"confidence_score"`
	CreatedAt       platform.Time  `json:"created_at"`
}

// PaymentOutFrom renders a stored payment.
func PaymentOutFrom(p paymentsdb.Payment) PaymentOut {
	out := PaymentOut{
		PaymentID: p.PaymentID, InvoiceID: platform.UUIDPtr(p.InvoiceID), CustomerID: platform.UUIDPtr(p.CustomerID),
		Amount: platform.NewDec4(p.Amount), WhtAmount: platform.NewDec4(p.WhtAmount), Currency: p.Currency,
		PayerName: p.PayerName, PayerReference: p.PayerReference, PaymentDate: platform.Date{Time: p.PaymentDate.Time},
		IntakeSource: p.IntakeSource, ExternalRef: p.ExternalRef, Status: p.Status, AdjustmentType: p.AdjustmentType,
	}
	if p.ConfidenceScore.Valid {
		c := platform.NewDec2(p.ConfidenceScore.Decimal)
		out.ConfidenceScore = &c
	}
	// created_at is nullable in the schema but always has its default.
	if p.CreatedAt != nil {
		out.CreatedAt = platform.Time{Time: *p.CreatedAt}
	}
	return out
}

// Register adds the payment operations; every one is admin-only. svc is the
// reconciliation service the clearing routes run (Base currency, Hosting).
func Register(api huma.API, pool *pgxpool.Pool, svc Service) {
	r := routes{pool: pool, q: paymentsdb.New(pool), svc: svc}
	r.registerInvoice(api)
}

type routes struct {
	pool *pgxpool.Pool
	q    *paymentsdb.Queries
	svc  Service
}

// clientRefusal is a request the routes answer with a problem+json error:
// Python's sentence as the detail, a stable snake_case code.
type clientRefusal struct {
	status    int
	code, msg string
}

func (e clientRefusal) Error() string { return e.msg }

func clientRefuse(status int, code, format string, args ...any) error {
	return clientRefusal{status: status, code: code, msg: fmt.Sprintf(format, args...)}
}

// clientError is err as the client reads it; anything but a refusal is
// unchanged.
func clientError(err error) error {
	var e clientRefusal
	if errors.As(err, &e) {
		return platform.Err(e.status, e.code, e.msg)
	}
	return err
}
