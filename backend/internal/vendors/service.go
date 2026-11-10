package vendors

import (
	"context"

	"github.com/google/uuid"

	"github.com/wenhaokho/wenhao-billing/backend/internal/vendors/vendorsdb"
)

// ListFilter narrows List; the zero value lists every vendor. Active
// filters when set (nil is every vendor, active and inactive); NameLike is
// a name substring, already escaped by the caller for LIKE (nil is no
// filter); Limit caps the page when set (nil is unlimited, 0 lists
// nothing).
type ListFilter struct {
	Active   *bool
	NameLike *string
	Limit    *int32
}

// Get is the vendor, read through q (a pool or a transaction). An unknown
// id is pgx.ErrNoRows, unwrapped: test it with
// errors.Is(err, pgx.ErrNoRows).
func Get(ctx context.Context, q *vendorsdb.Queries, id uuid.UUID) (VendorOut, error) {
	v, err := q.GetVendor(ctx, id)
	if err != nil {
		return VendorOut{}, err
	}
	return vendorOut(v), nil
}

// List is the vendors matching f, rendered as the routes render them,
// never nil. Exported for the MCP list_vendors and resolve_vendor tools
// (inventory §4).
func List(ctx context.Context, q *vendorsdb.Queries, f ListFilter) ([]VendorOut, error) {
	rows, err := q.ListVendors(ctx, vendorsdb.ListVendorsParams{
		Active: f.Active, NameLike: f.NameLike, RowLimit: f.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]VendorOut, 0, len(rows))
	for _, v := range rows {
		out = append(out, vendorOut(v))
	}
	return out, nil
}

// Create writes a new vendor through q (a pool or a transaction).
// Exported for the MCP create_vendor tool (inventory §4).
func Create(ctx context.Context, q *vendorsdb.Queries, b VendorCreate) (VendorOut, error) {
	v, err := q.CreateVendor(ctx, vendorsdb.CreateVendorParams{
		VendorID:         uuid.New(),
		Name:             b.Name,
		ContactEmail:     emailPtr(b.ContactEmail, nil),
		ContactName:      b.ContactName.Merge(nil),
		DefaultCurrency:  b.DefaultCurrency.Or("IDR"),
		PaymentTermsDays: b.PaymentTermsDays.Or(30),
		TaxID:            b.TaxID.Merge(nil),
		Notes:            b.Notes.Merge(nil),
		Active:           b.Active.Or(true),
	})
	if err != nil {
		return VendorOut{}, err
	}
	return vendorOut(v), nil
}

// Update is the PATCH pattern on q, a transaction: read the row locked,
// overlay the fields sent, and write every column back. An unknown id is
// pgx.ErrNoRows. Exported for the MCP update_vendor tool (inventory §4).
func Update(ctx context.Context, q *vendorsdb.Queries, id uuid.UUID, b VendorUpdate) (VendorOut, error) {
	cur, err := q.GetVendorForUpdate(ctx, id)
	if err != nil {
		return VendorOut{}, err
	}
	v, err := q.UpdateVendor(ctx, vendorsdb.UpdateVendorParams{
		VendorID:         cur.VendorID,
		Name:             b.Name.Or(cur.Name),
		ContactEmail:     emailPtr(b.ContactEmail, cur.ContactEmail),
		ContactName:      b.ContactName.Merge(cur.ContactName),
		DefaultCurrency:  b.DefaultCurrency.Or(cur.DefaultCurrency),
		PaymentTermsDays: b.PaymentTermsDays.Or(cur.PaymentTermsDays),
		TaxID:            b.TaxID.Merge(cur.TaxID),
		Notes:            b.Notes.Merge(cur.Notes),
		Active:           b.Active.Or(cur.Active),
	})
	if err != nil {
		return VendorOut{}, err
	}
	return vendorOut(v), nil
}
