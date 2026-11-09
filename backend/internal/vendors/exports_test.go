package vendors_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LOQATO/gokit/db"

	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/internal/vendors"
	"github.com/wenhaokho/wenhao-billing/backend/internal/vendors/vendorsdb"
)

func vendorQ(pool *pgxpool.Pool) *vendorsdb.Queries { return vendorsdb.New(pool) }

func TestExportVendorCreateGet(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out, err := vendors.Create(t.Context(), vendorQ(a.Pool), vendors.VendorCreate{
		Name:  "Export vendor",
		Notes: platform.Optional[string]{Set: true, Value: "n"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Name != "Export vendor" || out.DefaultCurrency != "IDR" || out.PaymentTermsDays != 30 || !out.Active || out.VendorID == uuid.Nil {
		t.Errorf("created %+v", out)
	}
	got, err := vendors.Get(t.Context(), vendorQ(a.Pool), out.VendorID)
	if err != nil {
		t.Fatal(err)
	}
	if got.VendorID != out.VendorID {
		t.Errorf("get %+v", got)
	}
	if _, err := vendors.Get(t.Context(), vendorQ(a.Pool), uuid.New()); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("get unknown err = %v, want ErrNoRows", err)
	}
}

func TestExportVendorListNameLikeLimit(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	create(t, a, map[string]any{"name": "Zed Plumbing"})
	create(t, a, map[string]any{"name": "Ana Electrical"})
	sub := "plumb"
	found, err := vendors.List(t.Context(), vendorQ(a.Pool), vendors.ListFilter{NameLike: &sub})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Name != "Zed Plumbing" {
		t.Errorf("namelike %+v", found)
	}
	one := int32(1)
	limited, err := vendors.List(t.Context(), vendorQ(a.Pool), vendors.ListFilter{Limit: &one})
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 1 {
		t.Errorf("limit %+v", limited)
	}
}

func TestExportVendorUpdate(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	created := create(t, a, map[string]any{"name": "Old", "tax_id": "T1"})
	var updated vendors.VendorOut
	err := db.InTx(t.Context(), a.Pool, func(tx pgx.Tx) error {
		var err error
		updated, err = vendors.Update(t.Context(), vendorsdb.New(a.Pool).WithTx(tx), created.VendorID, vendors.VendorUpdate{
			Name: platform.NotNull[string]{Set: true, Value: "New"},
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "New" || updated.TaxID == nil || *updated.TaxID != "T1" {
		t.Errorf("updated %+v", updated)
	}
	_, err = vendors.Update(t.Context(), vendorQ(a.Pool), uuid.New(), vendors.VendorUpdate{
		Name: platform.NotNull[string]{Set: true, Value: "x"},
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("update unknown err = %v, want ErrNoRows", err)
	}
}
