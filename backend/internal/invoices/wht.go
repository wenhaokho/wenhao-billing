package invoices

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices/invoicesdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/internal/withholding"
)

// normalizeWHT is withholding.Normalize with its refusal as an ErrInvoicing
// carrying withholding's sentence (invoicing.py:76-82), so the invoice
// routes answer it with a 400.
func normalizeWHT(whtType string, rate *decimal.Decimal, grossUp bool) (string, *decimal.Decimal, bool, error) {
	t, r, g, err := withholding.Normalize(whtType, rate, grossUp)
	if err != nil {
		return "", nil, false, refuse("invalid_withholding", "%s", err.Error())
	}
	return t, r, g, nil
}

// ResolveWHT is a new document's withholding (invoicing.py:85-103). A sent
// type wins, even NONE: it is normalized with the sent rate and gross-up
// (nil is false). Without one, the customer's defaults are normalized; with
// no customer (nil, or an id that matches none) it is NONE. A refused
// combination is an ErrInvoicing.
func ResolveWHT(ctx context.Context, q *invoicesdb.Queries, customerID *uuid.UUID, whtType *string, rate *decimal.Decimal, grossUp *bool) (string, *decimal.Decimal, bool, error) {
	if whtType != nil {
		return normalizeWHT(*whtType, rate, grossUp != nil && *grossUp)
	}
	if customerID == nil {
		return "NONE", nil, false, nil
	}
	c, err := q.GetCustomerWHTDefaults(ctx, *customerID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "NONE", nil, false, nil
	}
	if err != nil {
		return "", nil, false, err
	}
	var defaultRate *decimal.Decimal
	if c.DefaultWhtRate.Valid {
		defaultRate = &c.DefaultWhtRate.Decimal
	}
	return normalizeWHT(c.DefaultWhtType, defaultRate, c.DefaultWhtGrossUp)
}

// MergeWHT overlays a PATCH's withholding fields on the stored ones
// (invoicing.py:106-119). With none of the three sent, the stored values
// come back unchanged and are not normalized. Otherwise: a sent non-empty
// type replaces the stored one (null or "" keeps it, Python's `or`); the
// rate is the sent one, or the stored one when none was sent (or null) and
// the type is unchanged, so a new type with no rate takes its default; a
// sent non-null gross-up replaces the stored one. The result is then
// normalized, and a refusal is an ErrInvoicing.
func MergeWHT(storedType string, storedRate *decimal.Decimal, storedGrossUp bool,
	whtType platform.Optional[string], rate platform.Optional[platform.Dec4], grossUp platform.Optional[bool],
) (string, *decimal.Decimal, bool, error) {
	if !whtType.Set && !rate.Set && !grossUp.Set {
		return storedType, storedRate, storedGrossUp, nil
	}
	t := storedType
	if whtType.Set && !whtType.Null && whtType.Value != "" {
		t = whtType.Value
	}
	var r *decimal.Decimal
	if rate.Set && !rate.Null {
		v := rate.Value.Decimal()
		r = &v
	}
	if r == nil && t == storedType {
		r = storedRate
	}
	g := storedGrossUp
	if grossUp.Set && !grossUp.Null {
		g = grossUp.Value
	}
	return normalizeWHT(t, r, g)
}
