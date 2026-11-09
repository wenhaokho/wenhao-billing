package invoices

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LOQATO/gokit/db"

	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices/invoicesdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices/schedule"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// RecurringTrigger is the body of a manual trigger (schemas/invoice.py:22-25).
type RecurringTrigger struct {
	CycleKey string `json:"cycle_key" doc:"The cycle's first day, YYYY-MM-DD; it must be a cycle start of the template's schedule."`
}

// cycleRef is a generated child's billing_cycle_ref (invoicing.py:570-573).
type cycleRef struct {
	TemplateInvoiceID string `json:"template_invoice_id"`
	CycleKey          string `json:"cycle_key"`
}

// Trigger is trigger_recurring_cycle (invoicing.py:501-601) in tx: the
// generated DRAFT of template templateID's cycle cycleKey, created (true)
// unless it exists (false: returned unchanged). The refusals are Python's,
// in its order.
//
// Go changes this (port note): cycleKey must be exactly YYYY-MM-DD (Python's
// date.fromisoformat also took "20260301", stored raw, so two spellings made
// two invoices), and everything runs under pg_advisory_xact_lock keyed on
// the template (LockRecurringTemplate), taken before the template is read
// and held until tx ends, so a manual trigger and the scan cannot both
// create the cycle, and a template PUT or PATCH (which take it first too)
// is seen whole or not at all. tx must be READ COMMITTED (the default), so
// the reads after the lock see what a transaction holding it before has
// committed. today is taken as its UTC calendar date.
//
// Lock order, everywhere in this package: the template's advisory lock,
// then an invoice row FOR UPDATE, then the year's number lock
// (NextInvoiceNumber). A transaction never takes an earlier kind after a
// later one, so two of these paths cannot wait on each other in a cycle.
func Trigger(ctx context.Context, tx pgx.Tx, templateID uuid.UUID, cycleKey string, today time.Time) (InvoiceOut, bool, error) {
	today = utcDate(today)
	q := invoicesdb.New(tx)
	// First, before any read of the template: a PUT holding the lock
	// commits its lines and discount together before this reads either.
	if err := q.LockRecurringTemplate(ctx, templateID.String()); err != nil {
		return InvoiceOut{}, false, err
	}
	tpl, err := q.GetInvoice(ctx, templateID)
	if errors.Is(err, pgx.ErrNoRows) {
		return InvoiceOut{}, false, refuse("template_invoice_not_found", "template invoice %s not found", templateID)
	}
	if err != nil {
		return InvoiceOut{}, false, err
	}
	// Not is_template: a generated child fails on its schedule instead.
	if tpl.InvoiceType != "RECURRING" {
		return InvoiceOut{}, false, refuse("invoice_not_recurring", "invoice %s is not RECURRING", templateID)
	}
	sched, err := schedule.Parse(storedConfig(tpl.BillingCycleRef))
	if err != nil {
		return InvoiceOut{}, false, refuse("invalid_template_schedule", "template %s has an invalid schedule: %s", templateID, err)
	}
	cycleStart, ok := parseCycleKey(cycleKey)
	if !ok {
		return InvoiceOut{}, false, refuse("invalid_cycle_key", "cycle_key must be an ISO date (YYYY-MM-DD), got %s", platform.PyRepr(cycleKey))
	}
	// Paused or ended templates are not refused (recurring_schedule.py:185-186).
	if !sched.IsCycleStart(cycleStart) {
		return InvoiceOut{}, false, refuse("not_a_cycle_start", "cycle_key %s is not a valid cycle start for this schedule", platform.PyRepr(cycleKey))
	}

	existing, found, err := findCycleInvoice(ctx, q, templateID, cycleKey)
	if err != nil {
		return InvoiceOut{}, false, err
	}
	if found {
		out, err := Get(ctx, q, existing.InvoiceID)
		return out, false, err
	}
	out, err := createChild(ctx, tx, tpl, sched, cycleStart, cycleKey, today)
	return out, err == nil, err
}

// parseCycleKey is the date of a YYYY-MM-DD cycle key; false for any other
// spelling.
func parseCycleKey(key string) (time.Time, bool) {
	if len(key) != len(time.DateOnly) {
		return time.Time{}, false
	}
	d, err := time.Parse(time.DateOnly, key)
	if err != nil || d.Year() < 1 {
		return time.Time{}, false
	}
	return d, true
}

// findCycleInvoice is find_cycle_invoice (invoicing.py:477-498): the
// generated invoice of template's cycle key, if any.
func findCycleInvoice(ctx context.Context, q *invoicesdb.Queries, template uuid.UUID, key string) (invoicesdb.Invoice, bool, error) {
	inv, err := q.FindCycleInvoice(ctx, invoicesdb.FindCycleInvoiceParams{TemplateID: template.String(), CycleKey: key})
	if errors.Is(err, pgx.ErrNoRows) {
		return invoicesdb.Invoice{}, false, nil
	}
	return inv, err == nil, err
}

// createChild stores the DRAFT of tpl's cycle (invoicing.py:539-601): the
// template's fields and withholding snapshot copied (not re-normalised), its
// lines copied verbatim, the totals recomputed from them, the next number,
// and today's issue date (not the cycle's) with the terms' due date. With a
// hosting item on any line, the coverage is stamped (hosting.py:61-83).
func createChild(ctx context.Context, tx pgx.Tx, tpl invoicesdb.Invoice, sched schedule.Schedule,
	cycleStart time.Time, cycleKey string, today time.Time,
) (InvoiceOut, error) {
	q := invoicesdb.New(tx)
	due, err := DueAfterTerms(today, tpl.PaymentTerms)
	if err != nil {
		return InvoiceOut{}, err
	}
	lines, err := q.ListInvoiceLines(ctx, tpl.InvoiceID)
	if err != nil {
		return InvoiceOut{}, err
	}
	ins := make([]LineIn, 0, len(lines))
	for _, l := range lines {
		ins = append(ins, LineIn{Quantity: l.Quantity, UnitPrice: l.UnitPrice})
	}
	totals := ComputeTotals(ins, tpl.DiscountType, storedDec(tpl.DiscountValue), tpl.Currency,
		tpl.WhtType, storedDec(tpl.WhtRate), tpl.WhtGrossUp)
	number, err := NextInvoiceNumber(ctx, tx, today)
	if err != nil {
		return InvoiceOut{}, err
	}
	ref, err := json.Marshal(cycleRef{TemplateInvoiceID: tpl.InvoiceID.String(), CycleKey: cycleKey})
	if err != nil {
		return InvoiceOut{}, err
	}

	id := uuid.New()
	err = q.InsertInvoice(ctx, invoicesdb.InsertInvoiceParams{
		InvoiceID: id, CustomerID: tpl.CustomerID, ProjectID: tpl.ProjectID, InvoiceType: "RECURRING",
		InvoiceNumber: &number, PoSoNumber: tpl.PoSoNumber, Currency: tpl.Currency,
		Subtotal: nullDec(&totals.Subtotal), DiscountType: tpl.DiscountType, DiscountValue: tpl.DiscountValue,
		WhtType: tpl.WhtType, WhtRate: tpl.WhtRate, WhtGrossUp: tpl.WhtGrossUp, WhtBase: nullDec(&totals.WHT.Base),
		WhtAmount: totals.WHT.Amount, Amount: totals.Amount, BalanceDue: totals.Amount, Status: "DRAFT",
		BillingCycleRef: ref, IssueDate: pgDate(&today), DueDate: pgDate(&due), PaymentTerms: tpl.PaymentTerms,
		Notes: tpl.Notes, Footer: tpl.Footer, InternalNote: tpl.InternalNote,
	})
	if err != nil {
		return InvoiceOut{}, MapWriteError(err)
	}

	label, err := stampCoverage(ctx, q, tpl.InvoiceID, id, sched, cycleStart)
	if err != nil {
		return InvoiceOut{}, err
	}
	for _, l := range lines {
		description := l.Description
		if label != "" && !strings.Contains(description, label) {
			description += "\n" + label
		}
		err := q.InsertInvoiceLine(ctx, invoicesdb.InsertInvoiceLineParams{
			LineItemID: uuid.New(), InvoiceID: id, ItemID: l.ItemID, Position: l.Position,
			Description: description, Quantity: l.Quantity, UnitPrice: l.UnitPrice, Amount: l.Amount,
		})
		if err != nil {
			return InvoiceOut{}, err
		}
	}
	return Get(ctx, q, id)
}

// stampCoverage is apply_generated_invoice_hosting_fields (hosting.py:61-83)
// for child id of template: when any of the template's lines is a hosting
// item, the child covers its cycle start to the day before start + interval
// months (whatever the frequency), and the label every line must carry is
// returned; otherwise nothing is stamped and the label is "".
func stampCoverage(ctx context.Context, q *invoicesdb.Queries, template, id uuid.UUID,
	sched schedule.Schedule, cycleStart time.Time,
) (string, error) {
	hosting, err := q.InvoiceHasHostingItem(ctx, template)
	if err != nil || !hosting {
		return "", err
	}
	// sched.Interval stands for Python's cfg.get("interval") or 1
	// (hosting.py:74): the two are equal once schedule.Parse has accepted
	// the config, which fills a missing interval with 1 and refuses 0.
	end := schedule.AddMonths(cycleStart, sched.Interval).AddDate(0, 0, -1)
	err = q.SetInvoiceCoverage(ctx, invoicesdb.SetInvoiceCoverageParams{
		InvoiceID: id, CoverageStart: pgDate(&cycleStart), CoverageEnd: pgDate(&end),
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Coverage: %s to %s", cycleStart.Format(time.DateOnly), end.Format(time.DateOnly)), nil
}

// draftSummary is draft_summary (draft_notifications.py:25-35) of a new
// child: no customer name when it has no customer.
func draftSummary(ctx context.Context, q *invoicesdb.Queries, out InvoiceOut, cycleKey string) (DraftSummary, error) {
	s := DraftSummary{InvoiceID: out.InvoiceID, Currency: out.Currency, Amount: out.Amount.Decimal(), CycleDate: cycleKey}
	if out.InvoiceNumber != nil {
		s.InvoiceNumber = *out.InvoiceNumber
	}
	if out.CustomerID != nil {
		name, err := q.GetCustomerName(ctx, *out.CustomerID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return DraftSummary{}, err
		}
		if err == nil {
			s.CustomerName = &name
		}
	}
	return s, nil
}

// triggerAndSummarise runs Trigger in its own transaction and answers the
// invoice and, when it was created, its draft summary.
func triggerAndSummarise(ctx context.Context, pool *pgxpool.Pool, templateID uuid.UUID, cycleKey string, today time.Time) (InvoiceOut, *DraftSummary, error) {
	var out InvoiceOut
	var summary *DraftSummary
	err := db.InTx(ctx, pool, func(tx pgx.Tx) error {
		o, created, err := Trigger(ctx, tx, templateID, cycleKey, today)
		if err != nil {
			return err
		}
		out = o
		if created {
			s, err := draftSummary(ctx, invoicesdb.New(tx), o, cycleKey)
			if err != nil {
				return err
			}
			summary = &s
		}
		return nil
	})
	if err != nil {
		return InvoiceOut{}, nil, err
	}
	return out, summary, nil
}

type triggerIn struct {
	TemplateID uuid.UUID `path:"template_id"`
	Body       RecurringTrigger
}

// trigger is the route (invoices.py:365-388): 200 with the new or existing
// invoice; the new one's summary is notified after the commit.
func (r routes) trigger(ctx context.Context, in *triggerIn) (*invoiceBody, error) {
	out, summary, err := triggerAndSummarise(ctx, r.pool, in.TemplateID, in.Body.CycleKey, todayUTC(r.clock))
	if err != nil {
		return nil, clientError(err)
	}
	if summary != nil {
		notifyDrafts(ctx, r.notify, []DraftSummary{*summary})
	}
	return &invoiceBody{Body: out}, nil
}
