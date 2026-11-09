package items_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/LOQATO/gokit/db"

	"github.com/wenhaokho/wenhao-billing/backend/internal/items"
	"github.com/wenhaokho/wenhao-billing/backend/internal/items/itemsdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

func itemDec4(s string) platform.Dec4 { return platform.NewDec4(decimal.RequireFromString(s)) }

func TestExportItemCreate(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	out, err := items.Create(t.Context(), itemsdb.New(a.Pool), items.ItemCreate{
		Name:     "Export item",
		ItemType: "SERVICE",
		DefaultUnitPrice: platform.Optional[platform.Dec4]{
			Set: true, Value: itemDec4("2500"),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Name != "Export item" || out.DefaultCurrency != "IDR" || !out.Active || out.ItemID == uuid.Nil {
		t.Errorf("created %+v", out)
	}
}

func TestExportItemCreateMissingHostingFieldsRefused(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	_, err := items.Create(t.Context(), itemsdb.New(a.Pool), items.ItemCreate{
		Name:                     "Hosting",
		ItemType:                 "SERVICE",
		IsHosting:                platform.NotNull[bool]{Set: true, Value: true},
		HostingDomain:            platform.Optional[string]{Set: true, Value: "example.com"},
		HostingGraceDays:         platform.Optional[int32]{Set: true, Value: 7},
		HostingSuspensionEnabled: platform.Optional[bool]{Set: true, Value: true},
	})
	// cloudflare_target is missing, so the check must refuse.
	if err == nil {
		t.Fatal("create hosting without target err = nil, want refusal")
	}
}

func TestExportItemListNameLikeAndLimit(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	create(t, a, minimal(map[string]any{"name": "Alpha widget"}))
	create(t, a, minimal(map[string]any{"name": "Beta gadget"}))
	duck := "widget"
	all, err := items.List(t.Context(), itemsdb.New(a.Pool), itemsdb.ListItemsParams{NameLike: &duck})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].Name != "Alpha widget" {
		t.Errorf("namelike %+v", all)
	}
	one := int32(1)
	capped, err := items.List(t.Context(), itemsdb.New(a.Pool), itemsdb.ListItemsParams{RowLimit: &one})
	if err != nil {
		t.Fatal(err)
	}
	if len(capped) != 1 {
		t.Errorf("limit %+v", capped)
	}
}

func TestExportItemUpdate(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	created := create(t, a, minimal(map[string]any{"name": "Old name"}))
	var updated items.ItemOut
	err := db.InTx(t.Context(), a.Pool, func(tx pgx.Tx) error {
		var err error
		updated, err = items.Update(t.Context(), itemsdb.New(a.Pool).WithTx(tx), created.ItemID, items.ItemUpdate{
			Name: platform.NotNull[string]{Set: true, Value: "New name"},
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "New name" || updated.ItemType != "SERVICE" {
		t.Errorf("updated %+v", updated)
	}
}

func TestExportItemUpdateUnknown(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	_, err := items.Update(t.Context(), itemsdb.New(a.Pool), uuid.New(), items.ItemUpdate{
		Name: platform.NotNull[string]{Set: true, Value: "x"},
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("update unknown err = %v, want ErrNoRows", err)
	}
}
