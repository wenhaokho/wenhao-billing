package invoices

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices/invoicesdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices/schedule"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// RecurringSchedule is a template's schedule as sent (schemas/invoice.py:99-105).
type RecurringSchedule struct {
	Frequency      string                           `json:"frequency" enum:"DAILY,WEEKLY,MONTHLY,YEARLY"`
	Interval       platform.NotNull[int]            `json:"interval" minimum:"1" maximum:"1000" doc:"Defaults to 1."`
	StartDate      platform.Date                    `json:"start_date" doc:"The first cycle's first day."`
	EndMode        platform.NotNull[string]         `json:"end_mode" enum:"NEVER,ON_DATE,AFTER_N" doc:"Defaults to NEVER."`
	EndDate        platform.Optional[platform.Date] `json:"end_date" doc:"Required with ON_DATE (the last day a cycle may start); stored whenever sent."`
	EndAfterCycles platform.Optional[int]           `json:"end_after_cycles" minimum:"1" maximum:"1000" doc:"Required with AFTER_N; stored whenever sent."`
}

// RecurringTemplateCreate is the body of a template create and of a PUT,
// which replaces every field (schemas/invoice.py:108-123).
type RecurringTemplateCreate struct {
	CustomerID    platform.Optional[uuid.UUID]     `json:"customer_id" doc:"It must exist."`
	ProjectID     platform.Optional[uuid.UUID]     `json:"project_id" doc:"It must exist."`
	Currency      string                           `json:"currency" minLength:"3" maxLength:"3"`
	PoSoNumber    platform.Optional[string]        `json:"po_so_number" maxLength:"100"`
	PaymentTerms  string                           `json:"payment_terms" maxLength:"30" doc:"May be empty."`
	Notes         platform.Optional[string]        `json:"notes"`
	Footer        platform.Optional[string]        `json:"footer"`
	InternalNote  platform.Optional[string]        `json:"internal_note" doc:"For the team only; never on the customer's PDF."`
	DiscountType  platform.Optional[string]        `json:"discount_type" enum:"PERCENT,AMOUNT"`
	DiscountValue platform.Optional[platform.Dec4] `json:"discount_value" doc:"At least 0, at most 4 decimal places; applies only with a discount_type."`
	WhtType       platform.Optional[string]        `json:"wht_type" enum:"NONE,PPH21,PPH23" doc:"On create, null takes the customer's default; on PUT, null or left out keeps the stored type."`
	WhtRate       platform.Optional[platform.Dec4] `json:"wht_rate" doc:"Percent, above 0 and below 100. Null takes the type's default (PPH21 2.5, PPH23 2)."`
	WhtGrossUp    platform.Optional[bool]          `json:"wht_gross_up"`
	// A slice, for the reason InvoiceCreate.LineItems is one.
	LineItems []LineItemIn      `json:"line_items,omitempty" doc:"At least one."`
	Schedule  RecurringSchedule `json:"schedule"`
}

// RecurringTemplateAction is the body of a template PATCH (invoices.py:320-321).
type RecurringTemplateAction struct {
	Action string `json:"action" doc:"PAUSE, RESUME or END_NOW, in any case."`
}

// RecurringTemplateRow is one template of the Recurring Invoices view
// (invoices.py:118-132).
type RecurringTemplateRow struct {
	TemplateID          uuid.UUID      `json:"template_id"`
	CustomerID          *uuid.UUID     `json:"customer_id"`
	CustomerName        *string        `json:"customer_name"`
	Currency            string         `json:"currency"`
	Amount              platform.Dec4  `json:"amount"`
	PaymentTerms        *string        `json:"payment_terms"`
	ScheduleDescription string         `json:"schedule_description" doc:"\"Schedule not configured\" when the stored schedule does not parse."`
	Frequency           *string        `json:"frequency"`
	StartDate           *platform.Date `json:"start_date"`
	EndMode             *string        `json:"end_mode"`
	NextRunDate         *platform.Date `json:"next_run_date" doc:"The cycle after the latest generated one (the first cycle when none was); null once the schedule has ended."`
	PreviousIssueDate   *platform.Date `json:"previous_issue_date" doc:"The cycle date of the generated invoice with the latest issue date (its issue date when it has no ISO cycle key)."`
	GeneratedCount      int            `json:"generated_count" doc:"Generated invoices that are not VOID."`
	Status              string         `json:"status" doc:"ACTIVE, PAUSED, ENDED (outranks PAUSED) or MISCONFIGURED."`
}

var (
	errTemplateNotFound = platform.Err(http.StatusNotFound, "recurring_template_not_found", "recurring template not found")
	errTemplateEnded    = platform.Err(http.StatusConflict, "recurring_template_ended", "recurring template has ended and is view-only")
)

// scheduleConfig is _schedule_to_json (invoicing.py:344-361): the stored
// schedule, checked by schedule.Parse; a refused one is an ErrInvoicing.
func scheduleConfig(s RecurringSchedule) (map[string]any, error) {
	cfg := map[string]any{
		"frequency": s.Frequency, "interval": s.Interval.Or(1),
		"start_date": s.StartDate.Format(time.DateOnly), "end_mode": s.EndMode.Or("NEVER"),
	}
	if d := s.EndDate.Merge(nil); d != nil {
		cfg["end_date"] = d.Format(time.DateOnly)
	}
	if n := s.EndAfterCycles.Merge(nil); n != nil {
		cfg["end_after_cycles"] = *n
	}
	if _, err := schedule.Parse(cfg); err != nil {
		return nil, refuse("invalid_schedule", "invalid schedule: %s", err)
	}
	return cfg, nil
}

// storedConfig is a stored billing_cycle_ref as Python reads it, its whole
// numbers kept as written; nil when it is null or not an object.
func storedConfig(raw json.RawMessage) map[string]any {
	var cfg map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&cfg); err != nil {
		return nil
	}
	return cfg
}

func templateLines(lines []LineItemIn) []LineIn {
	out := make([]LineIn, 0, len(lines))
	for _, ln := range lines {
		out = append(out, lineIn(ln))
	}
	return out
}

// createTemplate is Python's create_recurring_template (invoicing.py:364-412):
// a DRAFT RECURRING template with no number and no dates, its schedule in
// billing_cycle_ref.
func createTemplate(ctx context.Context, tx pgx.Tx, b RecurringTemplateCreate) (InvoiceOut, error) {
	q := invoicesdb.New(tx)
	if len(b.LineItems) == 0 {
		return InvoiceOut{}, refuse("template_line_items_required", "recurring template must have at least one line item")
	}
	cfg, err := scheduleConfig(b.Schedule)
	if err != nil {
		return InvoiceOut{}, err
	}
	ref, err := json.Marshal(cfg)
	if err != nil {
		return InvoiceOut{}, err
	}
	whtType, whtRate, grossUp, err := ResolveWHT(ctx, q, b.CustomerID.Merge(nil), b.WhtType.Merge(nil),
		decOf(b.WhtRate.Merge(nil)), b.WhtGrossUp.Merge(nil))
	if err != nil {
		return InvoiceOut{}, err
	}
	discountValue := decOf(b.DiscountValue.Merge(nil))
	totals := ComputeTotals(templateLines(b.LineItems), b.DiscountType.Merge(nil), discountValue, b.Currency, whtType, whtRate, grossUp)

	id := uuid.New()
	err = q.InsertInvoice(ctx, invoicesdb.InsertInvoiceParams{
		InvoiceID: id, CustomerID: pgUUID(b.CustomerID.Merge(nil)), ProjectID: pgUUID(b.ProjectID.Merge(nil)),
		InvoiceType: "RECURRING", PoSoNumber: b.PoSoNumber.Merge(nil), Currency: b.Currency,
		Subtotal: nullDec(&totals.Subtotal), DiscountType: b.DiscountType.Merge(nil), DiscountValue: nullDec(discountValue),
		WhtType: whtType, WhtRate: nullDec(whtRate), WhtGrossUp: grossUp, WhtBase: nullDec(&totals.WHT.Base),
		WhtAmount: totals.WHT.Amount, Amount: totals.Amount, BalanceDue: totals.Amount, Status: "DRAFT",
		BillingCycleRef: ref, PaymentTerms: &b.PaymentTerms,
		Notes: b.Notes.Merge(nil), Footer: b.Footer.Merge(nil), InternalNote: b.InternalNote.Merge(nil), IsTemplate: true,
	})
	if err != nil {
		return InvoiceOut{}, MapWriteError(err)
	}
	if err := insertLines(ctx, q, id, b.LineItems); err != nil {
		return InvoiceOut{}, err
	}
	return Get(ctx, q, id)
}

// updateTemplate is the PUT (invoices.py:299-317, invoicing.py:415-464): an
// ended template is refused (409) before anything else; then every field,
// the lines and the schedule are replaced, the pause flag kept, and the
// withholding merged as a PATCH merges it. It takes the template's advisory
// lock before reading anything, as Trigger does (see Trigger for the order).
func updateTemplate(ctx context.Context, tx pgx.Tx, id uuid.UUID, b RecurringTemplateCreate) (InvoiceOut, error) {
	q := invoicesdb.New(tx)
	if err := q.LockRecurringTemplate(ctx, id.String()); err != nil {
		return InvoiceOut{}, err
	}
	inv, err := q.GetInvoiceForUpdate(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !inv.IsTemplate) {
		return InvoiceOut{}, refuse("recurring_template_not_found", "recurring template %s not found", id)
	}
	if err != nil {
		return InvoiceOut{}, err
	}
	st, err := templateStateOf(ctx, q, inv)
	if err != nil {
		return InvoiceOut{}, err
	}
	if st.ended() {
		return InvoiceOut{}, errTemplateEnded
	}
	if len(b.LineItems) == 0 {
		return InvoiceOut{}, refuse("template_line_items_required", "recurring template must have at least one line item")
	}
	cfg, err := scheduleConfig(b.Schedule)
	if err != nil {
		return InvoiceOut{}, err
	}
	if schedule.IsPaused(storedConfig(inv.BillingCycleRef)) {
		cfg["paused"] = true
	}
	ref, err := json.Marshal(cfg)
	if err != nil {
		return InvoiceOut{}, err
	}
	whtType, whtRate, grossUp, err := MergeWHT(inv.WhtType, storedDec(inv.WhtRate), inv.WhtGrossUp, b.WhtType, b.WhtRate, b.WhtGrossUp)
	if err != nil {
		return InvoiceOut{}, err
	}
	discountValue := decOf(b.DiscountValue.Merge(nil))
	totals := ComputeTotals(templateLines(b.LineItems), b.DiscountType.Merge(nil), discountValue, b.Currency, whtType, whtRate, grossUp)

	err = q.UpdateTemplate(ctx, invoicesdb.UpdateTemplateParams{
		InvoiceID: id, CustomerID: pgUUID(b.CustomerID.Merge(nil)), ProjectID: pgUUID(b.ProjectID.Merge(nil)),
		Currency: b.Currency, PoSoNumber: b.PoSoNumber.Merge(nil), PaymentTerms: &b.PaymentTerms,
		Notes: b.Notes.Merge(nil), Footer: b.Footer.Merge(nil), InternalNote: b.InternalNote.Merge(nil),
		DiscountType: b.DiscountType.Merge(nil), DiscountValue: nullDec(discountValue), Subtotal: nullDec(&totals.Subtotal),
		WhtType: whtType, WhtRate: nullDec(whtRate), WhtGrossUp: grossUp, WhtBase: nullDec(&totals.WHT.Base),
		WhtAmount: totals.WHT.Amount, Amount: totals.Amount, BalanceDue: totals.Amount, BillingCycleRef: ref,
	})
	if err != nil {
		return InvoiceOut{}, MapWriteError(err)
	}
	if err := q.DeleteInvoiceLines(ctx, id); err != nil {
		return InvoiceOut{}, err
	}
	if err := insertLines(ctx, q, id, b.LineItems); err != nil {
		return InvoiceOut{}, err
	}
	return Get(ctx, q, id)
}

// actOnTemplate is the PATCH (invoices.py:324-362). PAUSE and RESUME set and
// clear the pause flag, and are refused on an ended template. END_NOW ends
// the schedule before every cycle not generated yet, unless it has already
// ended (it is idempotent), and always clears the pause flag. It takes the
// template's advisory lock first, as Trigger does.
func actOnTemplate(ctx context.Context, tx pgx.Tx, id uuid.UUID, action string, today time.Time) (InvoiceOut, error) {
	q := invoicesdb.New(tx)
	if err := q.LockRecurringTemplate(ctx, id.String()); err != nil {
		return InvoiceOut{}, err
	}
	inv, err := q.GetInvoiceForUpdate(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !inv.IsTemplate) {
		return InvoiceOut{}, errTemplateNotFound
	}
	if err != nil {
		return InvoiceOut{}, err
	}
	st, err := templateStateOf(ctx, q, inv)
	if err != nil {
		return InvoiceOut{}, err
	}
	cfg := storedConfig(inv.BillingCycleRef)
	if cfg == nil {
		cfg = map[string]any{}
	}
	upper := strings.ToUpper(action)
	if (upper == "PAUSE" || upper == "RESUME") && st.ended() {
		return InvoiceOut{}, errTemplateEnded
	}
	switch upper {
	case "PAUSE":
		cfg["paused"] = true
	case "RESUME":
		delete(cfg, "paused")
	case "END_NOW":
		if !st.ended() {
			cfg["end_mode"] = "ON_DATE"
			cfg["end_date"] = st.endNowDate(today).Format(time.DateOnly)
			delete(cfg, "end_after_cycles")
		}
		delete(cfg, "paused")
	default:
		return InvoiceOut{}, platform.Err(http.StatusBadRequest, "unknown_action", "unknown action: "+action)
	}
	ref, err := json.Marshal(cfg)
	if err != nil {
		return InvoiceOut{}, err
	}
	if err := q.SetBillingCycleRef(ctx, invoicesdb.SetBillingCycleRefParams{InvoiceID: id, BillingCycleRef: ref}); err != nil {
		return InvoiceOut{}, err
	}
	return Get(ctx, q, id)
}

// templateState is where a template's schedule stands: valid is false when
// its schedule does not parse; otherwise hasNext says whether a cycle is left
// to generate and next is that cycle's first day.
type templateState struct {
	sched   schedule.Schedule
	valid   bool
	next    time.Time
	hasNext bool
}

// ended is _template_has_ended (invoices.py:185-192): nothing left to
// generate. A schedule that does not parse has not ended.
func (s templateState) ended() bool { return s.valid && !s.hasNext }

// endNowDate is _end_now_date (invoices.py:195-212): yesterday, or the day
// before the next cycle not generated yet when that is earlier.
func (s templateState) endNowDate(today time.Time) time.Time {
	end := today.AddDate(0, 0, -1)
	if s.valid && s.hasNext {
		if before := s.next.AddDate(0, 0, -1); before.Before(end) {
			end = before
		}
	}
	return end
}

// stateFor is the state of a template whose stored schedule is cfg and whose
// latest generated cycle is previous (nil when none was generated).
func stateFor(cfg map[string]any, previous *time.Time) templateState {
	s, err := schedule.Parse(cfg)
	if err != nil {
		return templateState{}
	}
	// _next_run_for (invoices.py:175-182): the cycle after the previous one,
	// or the first cycle.
	anchor := s.Start.AddDate(0, 0, -1)
	if previous != nil {
		anchor = *previous
	}
	c, ok := s.NextAfter(anchor)
	return templateState{sched: s, valid: true, next: c.Start, hasNext: ok}
}

// templateStateOf reads the generated children of template inv.
func templateStateOf(ctx context.Context, q *invoicesdb.Queries, inv invoicesdb.Invoice) (templateState, error) {
	tid := inv.InvoiceID.String()
	children, err := q.ListLatestTemplateChildren(ctx, &tid)
	if err != nil {
		return templateState{}, err
	}
	var previous *time.Time
	if len(children) > 0 {
		previous = previousCycleDate(children[0])
	}
	return stateFor(storedConfig(inv.BillingCycleRef), previous), nil
}

// previousCycleDate is _previous_cycle_date (invoices.py:156-172) of the
// latest child: its cycle_key when that is an ISO date, else its issue date.
func previousCycleDate(latest invoicesdb.ListLatestTemplateChildrenRow) *time.Time {
	if latest.CycleKey != "" {
		if d, err := time.Parse(time.DateOnly, latest.CycleKey); err == nil {
			return &d
		}
	}
	if latest.IssueDate.Valid {
		d := latest.IssueDate.Time
		return &d
	}
	return nil
}

// templateRows is the rows view (invoices.py:215-284), every template's
// children read in one query.
func templateRows(ctx context.Context, q *invoicesdb.Queries) ([]RecurringTemplateRow, error) {
	templates, err := q.ListTemplateRows(ctx)
	if err != nil {
		return nil, err
	}
	children, err := q.ListLatestTemplateChildren(ctx, nil)
	if err != nil {
		return nil, err
	}
	latest := make(map[string]invoicesdb.ListLatestTemplateChildrenRow, len(children))
	for _, c := range children {
		latest[c.TemplateInvoiceID] = c
	}
	out := make([]RecurringTemplateRow, 0, len(templates))
	for _, t := range templates {
		row := RecurringTemplateRow{
			TemplateID: t.InvoiceID, CustomerID: uuidPtr(t.CustomerID), CustomerName: t.CustomerName,
			Currency: t.Currency, Amount: platform.NewDec4(t.Amount), PaymentTerms: t.PaymentTerms,
		}
		var previous *time.Time
		if c, ok := latest[t.InvoiceID.String()]; ok {
			row.GeneratedCount = int(c.Generated)
			previous = previousCycleDate(c)
		}
		row.PreviousIssueDate = dateOrNil(previous)
		cfg := storedConfig(t.BillingCycleRef)
		st := stateFor(cfg, previous)
		if !st.valid {
			row.ScheduleDescription, row.Status = "Schedule not configured", "MISCONFIGURED"
			out = append(out, row)
			continue
		}
		// ENDED outranks PAUSED: nothing is left to resume.
		switch {
		case !st.hasNext:
			row.Status = "ENDED"
		case schedule.IsPaused(cfg):
			row.Status = "PAUSED"
		default:
			row.Status = "ACTIVE"
		}
		if st.hasNext {
			row.NextRunDate = &platform.Date{Time: st.next}
		}
		row.ScheduleDescription = schedule.Describe(st.sched)
		row.Frequency, row.EndMode = &st.sched.Frequency, &st.sched.EndMode
		row.StartDate = &platform.Date{Time: st.sched.Start}
		out = append(out, row)
	}
	return out, nil
}

func dateOrNil(d *time.Time) *platform.Date {
	if d == nil {
		return nil
	}
	return &platform.Date{Time: *d}
}

type templateCreateIn struct{ Body RecurringTemplateCreate }

func (r routes) createTemplate(ctx context.Context, in *templateCreateIn) (*invoiceBody, error) {
	if err := checkTemplate(in.Body); err != nil {
		return nil, err
	}
	return r.inTx(ctx, func(tx pgx.Tx) (InvoiceOut, error) { return createTemplate(ctx, tx, in.Body) })
}

// checkTemplate is checkFields for a template: the negative discount value,
// quantity or price Huma's validation does not refuse.
func checkTemplate(b RecurringTemplateCreate) error {
	return checkFields(InvoiceFields{DiscountValue: b.DiscountValue}, b.LineItems)
}

func (r routes) listTemplates(ctx context.Context, _ *struct{}) (*listOut, error) {
	invs, err := r.q.ListTemplates(ctx)
	if err != nil {
		return nil, err
	}
	out, err := list(ctx, r.q, invs)
	if err != nil {
		return nil, err
	}
	return &listOut{Body: out}, nil
}

type templateRowsOut struct{ Body []RecurringTemplateRow }

func (r routes) templateRows(ctx context.Context, _ *struct{}) (*templateRowsOut, error) {
	out, err := templateRows(ctx, r.q)
	if err != nil {
		return nil, err
	}
	return &templateRowsOut{Body: out}, nil
}

type templateIDIn struct {
	TemplateID uuid.UUID `path:"template_id"`
}

func (r routes) getTemplate(ctx context.Context, in *templateIDIn) (*invoiceBody, error) {
	out, err := Get(ctx, r.q, in.TemplateID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !out.IsTemplate) {
		return nil, errTemplateNotFound
	}
	if err != nil {
		return nil, err
	}
	return &invoiceBody{Body: out}, nil
}

type templateUpdateIn struct {
	TemplateID uuid.UUID `path:"template_id"`
	Body       RecurringTemplateCreate
}

func (r routes) updateTemplate(ctx context.Context, in *templateUpdateIn) (*invoiceBody, error) {
	if err := checkTemplate(in.Body); err != nil {
		return nil, err
	}
	return r.inTx(ctx, func(tx pgx.Tx) (InvoiceOut, error) { return updateTemplate(ctx, tx, in.TemplateID, in.Body) })
}

type templateActionIn struct {
	TemplateID uuid.UUID `path:"template_id"`
	Body       RecurringTemplateAction
}

func (r routes) actOnTemplate(ctx context.Context, in *templateActionIn) (*invoiceBody, error) {
	today := todayUTC(r.clock)
	return r.inTx(ctx, func(tx pgx.Tx) (InvoiceOut, error) {
		return actOnTemplate(ctx, tx, in.TemplateID, in.Body.Action, today)
	})
}
