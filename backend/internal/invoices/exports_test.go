package invoices

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/LOQATO/gokit/db"

	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices/invoicesdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

func exportDec4(s string) platform.Dec4 {
	return platform.NewDec4(decimal.RequireFromString(s))
}

func exportInvoiceLine() LineItemIn {
	return LineItemIn{Description: "Work", Quantity: exportDec4("1"), UnitPrice: exportDec4("500")}
}

func exportInvoiceCreate(customer uuid.UUID) InvoiceCreate {
	return InvoiceCreate{
		Currency: "IDR",
		InvoiceFields: InvoiceFields{
			CustomerID: platform.Optional[uuid.UUID]{Set: true, Value: customer},
		},
		LineItems: []LineItemIn{exportInvoiceLine()},
	}
}

func createExportInvoice(t *testing.T, pool *pgxpool.Pool, customer uuid.UUID) InvoiceOut {
	t.Helper()
	var out InvoiceOut
	err := db.InTx(t.Context(), pool, func(tx pgx.Tx) error {
		var err error
		out, err = CreateInvoice(t.Context(), tx, exportInvoiceCreate(customer), day("2026-01-01"))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestExportInvoiceCreateUpdate(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	customer := insertCustomer(t, a.Pool, "NONE", "", false)
	created := createExportInvoice(t, a.Pool, customer)
	if created.Status != "DRAFT" {
		t.Errorf("status %s", created.Status)
	}
	if !created.Amount.Decimal().Equal(decimal.NewFromInt(500)) {
		t.Errorf("amount %s, want 500", created.Amount.Decimal())
	}
	var updated InvoiceOut
	err := db.InTx(t.Context(), a.Pool, func(tx pgx.Tx) error {
		var err error
		updated, err = UpdateInvoice(t.Context(), tx, created.InvoiceID, InvoiceUpdate{
			InvoiceFields: InvoiceFields{
				Notes: platform.Optional[string]{Set: true, Value: "second"},
			},
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Notes == nil || *updated.Notes != "second" {
		t.Errorf("notes %+v", updated.Notes)
	}
}

func TestExportInvoiceFinalizeVoidDelete(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	customer := insertCustomer(t, a.Pool, "NONE", "", false)
	created := createExportInvoice(t, a.Pool, customer)
	var finalized InvoiceOut
	err := db.InTx(t.Context(), a.Pool, func(tx pgx.Tx) error {
		var err error
		finalized, err = FinalizeInvoice(t.Context(), tx, created.InvoiceID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if finalized.Status != "OPEN" {
		t.Errorf("status %s", finalized.Status)
	}
	var voided InvoiceOut
	err = db.InTx(t.Context(), a.Pool, func(tx pgx.Tx) error {
		var err error
		voided, err = VoidInvoice(t.Context(), tx, created.InvoiceID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if voided.Status != "VOID" {
		t.Errorf("status %s", voided.Status)
	}

	draft := createExportInvoice(t, a.Pool, customer)
	err = db.InTx(t.Context(), a.Pool, func(tx pgx.Tx) error {
		return DeleteInvoice(t.Context(), tx, draft.InvoiceID)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Get(t.Context(), invoicesdb.New(a.Pool), draft.InvoiceID); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("get after delete err = %v, want ErrNoRows", err)
	}
}

func TestExportInvoiceDeleteNonDraftRefused(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	id := insertWithStatus(t, a.Pool, "OPEN", "5")
	err := db.InTx(t.Context(), a.Pool, func(tx pgx.Tx) error {
		return DeleteInvoice(t.Context(), tx, id)
	})
	if !errors.Is(err, ErrInvoicing) {
		t.Fatalf("delete OPEN invoice err = %v, want ErrInvoicing", err)
	}
	if err.Error() != "only DRAFT invoices can be deleted (got OPEN)" {
		t.Errorf("sentence %q", err)
	}
}

func TestExportInvoiceCreateTemplateTrigger(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	customer := insertCustomer(t, a.Pool, "NONE", "", false)
	schedule := RecurringSchedule{
		Frequency: "MONTHLY",
		StartDate: platform.Date{Time: day("2026-01-01")},
		EndMode:   platform.NotNull[string]{Set: true, Value: "NEVER"},
		Interval:  platform.NotNull[int]{Set: true, Value: 1},
	}
	var template InvoiceOut
	err := db.InTx(t.Context(), a.Pool, func(tx pgx.Tx) error {
		var err error
		template, err = CreateTemplate(t.Context(), tx, RecurringTemplateCreate{
			CustomerID:   platform.Optional[uuid.UUID]{Set: true, Value: customer},
			Currency:     "IDR",
			PaymentTerms: "Net 14",
			LineItems:    []LineItemIn{exportInvoiceLine()},
			Schedule:     schedule,
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if !template.IsTemplate || template.Status != "DRAFT" {
		t.Errorf("template %+v", template)
	}
	out, summary, err := TriggerAndSummarise(t.Context(), a.Pool, template.InvoiceID, "2026-01-01", day("2026-01-15"))
	if err != nil {
		t.Fatal(err)
	}
	if out.InvoiceID == template.InvoiceID || summary == nil || summary.InvoiceID != out.InvoiceID {
		t.Errorf("trigger %+v summary %+v", out, summary)
	}
}

func TestExportInvoiceListByFilters(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	customer := insertCustomer(t, a.Pool, "NONE", "", false)
	other := insertCustomer(t, a.Pool, "NONE", "", false)
	createExportInvoice(t, a.Pool, customer)
	createExportInvoice(t, a.Pool, other)
	q := invoicesdb.New(a.Pool)
	found, err := ListBy(t.Context(), q, ListFilter{CustomerID: &customer})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 {
		t.Errorf("customer filter %+v", found)
	}
	open, err := ListBy(t.Context(), q, ListFilter{Statuses: []string{"OPEN"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 0 {
		t.Errorf("status filter %+v", open)
	}
	typed := "MILESTONE"
	typedOut, err := ListBy(t.Context(), q, ListFilter{InvoiceType: &typed})
	if err != nil {
		t.Fatal(err)
	}
	if len(typedOut) != 2 {
		t.Errorf("type filter %+v", typedOut)
	}
	one := int32(1)
	limited, err := ListBy(t.Context(), q, ListFilter{Limit: &one})
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 1 {
		t.Errorf("limit %+v", limited)
	}
}
