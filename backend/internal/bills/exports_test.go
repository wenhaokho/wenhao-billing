package bills_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/LOQATO/gokit/db"

	"github.com/wenhaokho/wenhao-billing/backend/internal/bills"
	"github.com/wenhaokho/wenhao-billing/backend/internal/bills/billsdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

func dec4(s string) platform.Dec4 { return platform.NewDec4(decimal.RequireFromString(s)) }

func TestExportBillCreateGetList(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	vendor := insertVendor(t, a)
	var created bills.BillOut
	err := db.InTx(t.Context(), a.Pool, func(tx pgx.Tx) error {
		var err error
		created, err = bills.CreateBill(t.Context(), tx, bills.BillCreate{
			VendorID: vendor, Currency: "IDR",
			BillFields: bills.BillFields{
				BillNumber: platform.Optional[string]{Set: true, Value: "B-1"},
				Notes:      platform.Optional[string]{Set: true, Value: "first"},
			},
			LineItems: []bills.BillLineItemIn{
				{Description: "Work", Quantity: dec4("1"), UnitPrice: dec4("1000")},
			},
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Status != "DRAFT" || created.BillNumber == nil || *created.BillNumber != "B-1" {
		t.Errorf("created %+v", created)
	}
	if !created.Amount.Decimal().Equal(decimal.NewFromInt(1000)) {
		t.Errorf("amount %s, want 1000", created.Amount.Decimal())
	}
	got, err := bills.Get(t.Context(), billsdb.New(a.Pool), created.BillID)
	if err != nil {
		t.Fatal(err)
	}
	if got.BillID != created.BillID || len(got.LineItems) != 1 {
		t.Errorf("get %+v", got)
	}
	listed, err := bills.List(t.Context(), billsdb.New(a.Pool), bills.ListFilter{VendorID: &vendor})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].BillID != created.BillID {
		t.Errorf("list %+v", listed)
	}
	zero := int32(0)
	limited, err := bills.List(t.Context(), billsdb.New(a.Pool), bills.ListFilter{VendorID: &vendor, Limit: &zero})
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 0 {
		t.Errorf("limit 0 list %+v", limited)
	}
	open, err := bills.List(t.Context(), billsdb.New(a.Pool), bills.ListFilter{Statuses: []string{"OPEN"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 0 {
		t.Errorf("OPEN list %+v", open)
	}
}

func TestExportBillUpdate(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	vendor := insertVendor(t, a)
	created := post(t, a, body(vendor, nil))
	var updated bills.BillOut
	err := db.InTx(t.Context(), a.Pool, func(tx pgx.Tx) error {
		var err error
		updated, err = bills.UpdateBill(t.Context(), tx, base, created.BillID, bills.BillUpdate{
			BillFields: bills.BillFields{Notes: platform.Optional[string]{Set: true, Value: "second"}},
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Notes == nil || *updated.Notes != "second" {
		t.Errorf("updated %+v", updated)
	}
}

func TestExportBillDeleteDraft(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	vendor := insertVendor(t, a)
	created := post(t, a, body(vendor, nil))
	err := db.InTx(t.Context(), a.Pool, func(tx pgx.Tx) error {
		return bills.DeleteBill(t.Context(), tx, created.BillID)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bills.Get(t.Context(), billsdb.New(a.Pool), created.BillID); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("get after delete err = %v, want ErrNoRows", err)
	}
}

func TestExportBillDeleteNonDraftRefused(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	vendor := insertVendor(t, a)
	created := post(t, a, body(vendor, nil))
	setStatus(t, a, created.BillID, "OPEN")
	err := db.InTx(t.Context(), a.Pool, func(tx pgx.Tx) error {
		return bills.DeleteBill(t.Context(), tx, created.BillID)
	})
	if !errors.Is(err, bills.ErrBilling) {
		t.Fatalf("delete OPEN bill err = %v, want ErrBilling", err)
	}
	if err.Error() != "only DRAFT bills can be deleted (got OPEN)" {
		t.Errorf("sentence %q", err)
	}
	if _, err := bills.Get(t.Context(), billsdb.New(a.Pool), created.BillID); err != nil {
		t.Errorf("get after refused delete err = %v", err)
	}
}

func TestExportBillDeleteUnknown(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	err := db.InTx(t.Context(), a.Pool, func(tx pgx.Tx) error {
		return bills.DeleteBill(t.Context(), tx, uuid.New())
	})
	if !errors.Is(err, bills.ErrNotFound) {
		t.Errorf("delete unknown err = %v, want ErrNotFound", err)
	}
}
