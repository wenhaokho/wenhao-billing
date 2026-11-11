package ledger

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/ledger/ledgerdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// Basis is an invoice's receivable as the ledger holds it (PORT-NOTES 163):
// Face in the invoice's currency and Base in the base currency, summed over
// the receivable lines of its issue entries and of its payments' entries,
// reversals included. Issued is whether it has an active issue entry.
type Basis struct {
	Issued     bool
	Face, Base decimal.Decimal
}

// InvoiceBasis is the invoice's receivable on the ledger. Face is Σ(debit −
// credit) and Base Σ(base_debit − base_credit) over the 1100 lines of the
// entries sourced from the invoice (INVOICE) or from any payment whose
// invoice_id is it (PAYMENT).
func InvoiceBasis(ctx context.Context, tx pgx.Tx, invoiceID uuid.UUID) (Basis, error) {
	row, err := ledgerdb.New(tx).InvoiceARBasis(ctx, invoiceID)
	if err != nil {
		return Basis{}, fmt.Errorf("ledger: invoice basis: %w", err)
	}
	_, issued, err := ActiveEntryFor(ctx, tx, SourceInvoice, invoiceID)
	if err != nil {
		return Basis{}, err
	}
	return Basis{Issued: issued, Face: row.Face, Base: row.Base}, nil
}

// reliefBase is the base amount a relief of relief (in the receivable's
// currency) takes off b. While the receivable is above 0 it is relieved at
// its basis: a relief of the whole face (or more) takes the whole base, any
// excess over the face converted by convertExcess (at the payment date's
// rate); a smaller relief takes its share, RoundHalfEven4(relief × Base /
// Face). A receivable at or below 0 (already over-relieved) has no basis
// left: the whole relief is converted. A face above 0 with a base at or
// below 0 is not a receivable the ledger can have booked: refused, rather
// than relieved at a zero or negative rate. what names it in the refusal
// ("invoice <id> receivable", "bill <id> payable"); a bill's payable is
// relieved the same way (PORT-NOTES 165).
func reliefBase(what string, b Basis, relief decimal.Decimal, convertExcess func(decimal.Decimal) (decimal.Decimal, error)) (decimal.Decimal, error) {
	if b.Face.IsPositive() && !b.Base.IsPositive() {
		return decimal.Decimal{}, refuse("%s basis is inconsistent (face %s, base %s)",
			what, b.Face.StringFixed(4), b.Base.StringFixed(4))
	}
	if !b.Face.IsPositive() {
		return convertExcess(relief)
	}
	if relief.GreaterThanOrEqual(b.Face) {
		if !relief.GreaterThan(b.Face) {
			return b.Base, nil
		}
		excess, err := convertExcess(relief.Sub(b.Face))
		if err != nil {
			return decimal.Decimal{}, err
		}
		return b.Base.Add(excess), nil
	}
	return platform.RoundHalfEven4(relief.Mul(b.Base).Div(b.Face)), nil
}
