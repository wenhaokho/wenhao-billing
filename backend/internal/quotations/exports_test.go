package quotations_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/LOQATO/gokit/db"

	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/internal/quotations"
	"github.com/wenhaokho/wenhao-billing/backend/internal/quotations/quotationsdb"
)

func quotationDec4(s string) platform.Dec4 {
	return platform.NewDec4(decimal.RequireFromString(s))
}

func exportQuotationLine() quotations.QuotationLineItemIn {
	return quotations.QuotationLineItemIn{Description: "Work", Quantity: quotationDec4("1"), UnitPrice: quotationDec4("1000")}
}

var exportToday = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func TestExportQuotationCreateGetList(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	customer := insertCustomer(t, a)
	var created quotations.QuotationOut
	err := db.InTx(t.Context(), a.Pool, func(tx pgx.Tx) error {
		var err error
		created, err = quotations.CreateQuotation(t.Context(), tx, quotations.QuotationCreate{
			CustomerID: customer, Currency: "IDR",
			LineItems: []quotations.QuotationLineItemIn{exportQuotationLine()},
		}, exportToday)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Status != "DRAFT" || created.QuotationID == uuid.Nil {
		t.Errorf("created %+v", created)
	}
	if !created.Amount.Decimal().Equal(decimal.NewFromInt(1000)) {
		t.Errorf("amount %s, want 1000", created.Amount.Decimal())
	}
	got, err := quotations.Get(t.Context(), quotationsdb.New(a.Pool), created.QuotationID)
	if err != nil {
		t.Fatal(err)
	}
	if got.QuotationID != created.QuotationID || len(got.LineItems) != 1 {
		t.Errorf("get %+v", got)
	}
	listed, err := quotations.List(t.Context(), quotationsdb.New(a.Pool), quotations.ListFilter{CustomerID: &customer})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].QuotationID != created.QuotationID {
		t.Errorf("list %+v", listed)
	}
}

func TestExportQuotationUpdate(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	customer := insertCustomer(t, a)
	created := post(t, a, body(customer, nil))
	var updated quotations.QuotationOut
	err := db.InTx(t.Context(), a.Pool, func(tx pgx.Tx) error {
		var err error
		updated, err = quotations.UpdateQuotation(t.Context(), tx, created.QuotationID, quotations.QuotationUpdate{
			QuotationFields: quotations.QuotationFields{
				Notes: platform.Optional[string]{Set: true, Value: "second"},
			},
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

func TestExportQuotationDelete(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	customer := insertCustomer(t, a)
	created := post(t, a, body(customer, nil))
	err := db.InTx(t.Context(), a.Pool, func(tx pgx.Tx) error {
		return quotations.DeleteQuotation(t.Context(), tx, created.QuotationID)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := quotations.Get(t.Context(), quotationsdb.New(a.Pool), created.QuotationID); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("get after delete err = %v, want ErrNoRows", err)
	}
}
