package invoices

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices/invoicesdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/internal/withholding"
)

// Get is the invoice id with its lines, or pgx.ErrNoRows when there is none.
func Get(ctx context.Context, q *invoicesdb.Queries, id uuid.UUID) (InvoiceOut, error) {
	inv, err := q.GetInvoice(ctx, id)
	if err != nil {
		return InvoiceOut{}, err
	}
	lines, err := q.ListInvoiceLines(ctx, id)
	if err != nil {
		return InvoiceOut{}, err
	}
	return invoiceOut(inv, lines)
}

// invoiceOut renders a stored invoice and its lines (in position,
// line_item_id order).
func invoiceOut(inv invoicesdb.Invoice, lines []invoicesdb.InvoiceLineItem) (InvoiceOut, error) {
	// created_at is nullable but always set by its default; Python's
	// response model refuses a NULL (a 500), and so does this.
	if inv.CreatedAt == nil {
		return InvoiceOut{}, fmt.Errorf("invoice %s has no created_at", inv.InvoiceID)
	}
	out := InvoiceOut{
		InvoiceID: inv.InvoiceID, CustomerID: uuidPtr(inv.CustomerID), ProjectID: uuidPtr(inv.ProjectID),
		SourceQuoteID: uuidPtr(inv.SourceQuoteID), InvoiceType: inv.InvoiceType, InvoiceNumber: inv.InvoiceNumber,
		PoSoNumber: inv.PoSoNumber, Currency: inv.Currency, Subtotal: dec4Ptr(inv.Subtotal),
		DiscountType: inv.DiscountType, DiscountValue: dec4Ptr(inv.DiscountValue),
		WhtType: inv.WhtType, WhtRate: dec4Ptr(inv.WhtRate), WhtGrossUp: inv.WhtGrossUp, WhtBase: dec4Ptr(inv.WhtBase),
		WhtAmount: platform.NewDec4(inv.WhtAmount), WhtBooked: platform.NewDec4(inv.WhtBooked),
		Amount: platform.NewDec4(inv.Amount), BalanceDue: platform.NewDec4(inv.BalanceDue), Status: inv.Status,
		BillingCycleRef: inv.BillingCycleRef,
		IssueDate:       datePtr(inv.IssueDate), DueDate: datePtr(inv.DueDate),
		CoverageStart: datePtr(inv.CoverageStart), CoverageEnd: datePtr(inv.CoverageEnd),
		PaymentTerms: inv.PaymentTerms, Notes: inv.Notes, Footer: inv.Footer, InternalNote: inv.InternalNote,
		IsTemplate: inv.IsTemplate, CreatedAt: platform.Time{Time: *inv.CreatedAt},
		LineItems:    make([]LineItemOut, 0, len(lines)),
		ExpectedCash: platform.NewDec4(withholding.ExpectedCash(inv.BalanceDue, inv.WhtAmount, inv.WhtBooked)),
	}
	for _, l := range lines {
		out.LineItems = append(out.LineItems, LineItemOut{
			LineItemID: l.LineItemID, ItemID: uuidPtr(l.ItemID), Description: l.Description,
			Quantity: platform.NewDec4(l.Quantity), UnitPrice: platform.NewDec4(l.UnitPrice),
			Amount: platform.NewDec4(l.Amount), Position: int(l.Position),
		})
	}
	return out, nil
}

func uuidPtr(u pgtype.UUID) *uuid.UUID {
	if !u.Valid {
		return nil
	}
	id := uuid.UUID(u.Bytes)
	return &id
}

func dec4Ptr(d decimal.NullDecimal) *platform.Dec4 {
	if !d.Valid {
		return nil
	}
	v := platform.NewDec4(d.Decimal)
	return &v
}

func datePtr(d pgtype.Date) *platform.Date {
	if !d.Valid {
		return nil
	}
	return &platform.Date{Time: d.Time}
}
