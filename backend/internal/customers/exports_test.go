package customers_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LOQATO/gokit/db"

	"github.com/wenhaokho/wenhao-billing/backend/internal/customers"
	"github.com/wenhaokho/wenhao-billing/backend/internal/customers/customersdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

func customerQ(pool *pgxpool.Pool) *customersdb.Queries { return customersdb.New(pool) }

func TestExportCustomerCreateGet(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out, err := customers.Create(t.Context(), customerQ(a.Pool), customers.CustomerCreate{Name: "Export customer"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Name != "Export customer" || out.DefaultCurrency != "IDR" || !out.Active || out.CustomerID == uuid.Nil {
		t.Errorf("created %+v", out)
	}
	got, err := customers.Get(t.Context(), customerQ(a.Pool), out.CustomerID)
	if err != nil {
		t.Fatal(err)
	}
	if got.CustomerID != out.CustomerID {
		t.Errorf("get %+v", got)
	}
}

func TestExportCustomerListNameLikeLimit(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	create(t, a, map[string]any{"name": "Zed Hardware"})
	create(t, a, map[string]any{"name": "Ana Soft"})
	sub := "soft"
	found, err := customers.List(t.Context(), customerQ(a.Pool), customers.ListFilter{NameLike: &sub})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Name != "Ana Soft" {
		t.Errorf("namelike %+v", found)
	}
	one := int32(1)
	limited, err := customers.List(t.Context(), customerQ(a.Pool), customers.ListFilter{Limit: &one})
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 1 {
		t.Errorf("limit %+v", limited)
	}
}

func TestExportCustomerUpdate(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	created := create(t, a, map[string]any{"name": "Old"})
	var updated customers.CustomerOut
	err := db.InTx(t.Context(), a.Pool, func(tx pgx.Tx) error {
		var err error
		updated, err = customers.Update(t.Context(), customersdb.New(a.Pool).WithTx(tx), created.CustomerID, customers.CustomerUpdate{
			Name: platform.NotNull[string]{Set: true, Value: "New"},
			CustomerFields: customers.CustomerFields{
				Notes: platform.Optional[string]{Set: true, Value: "n2"},
			},
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "New" || updated.Notes == nil || *updated.Notes != "n2" {
		t.Errorf("updated %+v", updated)
	}
	_, err = customers.Update(t.Context(), customerQ(a.Pool), uuid.New(), customers.CustomerUpdate{
		Name: platform.NotNull[string]{Set: true, Value: "x"},
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("update unknown err = %v, want ErrNoRows", err)
	}
}
