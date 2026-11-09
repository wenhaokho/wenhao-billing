package invoices

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LOQATO/gokit/db"

	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices/invoicesdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

type routes struct {
	pool  *pgxpool.Pool
	q     *invoicesdb.Queries
	clock platform.Clock
	// notify hears of the drafts the recurring trigger generates (Task 6).
	notify DraftNotifier
}

// Register adds the invoice operations; every one is admin-only. notify
// hears of generated recurring drafts; nil logs them (LogNotifier).
//
// The literal paths (/milestone, /awaiting-finalization, /recurring-template,
// /recurring-templates, /recurring-templates/rows) are their own routes: chi
// matches a literal segment before {invoice_id} or {template_id}, whatever
// the registration order.
func Register(api huma.API, pool *pgxpool.Pool, clock platform.Clock, notify DraftNotifier) {
	if notify == nil {
		notify = LogNotifier{}
	}
	r := routes{pool: pool, q: invoicesdb.New(pool), clock: clock, notify: notify}
	const prefix = "/api/v1/invoices"
	tags := []string{"invoices"}

	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "invoices-create", Method: http.MethodPost, Path: prefix, Tags: tags,
		Summary: "Add a DRAFT invoice", DefaultStatus: http.StatusCreated,
		Errors: []int{http.StatusBadRequest, http.StatusConflict},
	}), r.create)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "invoices-create-milestone", Method: http.MethodPost, Path: prefix + "/milestone", Tags: tags,
		Summary: "Add a DRAFT milestone invoice (an amount, no lines)", DefaultStatus: http.StatusCreated,
		Errors: []int{http.StatusBadRequest},
	}), r.createMilestone)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "invoices-list", Method: http.MethodGet, Path: prefix, Tags: tags,
		Summary: "List invoices (never templates), newest first",
	}), r.list)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "invoices-awaiting-finalization", Method: http.MethodGet, Path: prefix + "/awaiting-finalization", Tags: tags,
		Summary: "List the DRAFT invoices (never templates), newest first",
	}), r.awaitingFinalization)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "invoices-get", Method: http.MethodGet, Path: prefix + "/{invoice_id}", Tags: tags,
		Summary: "An invoice or recurring template", Errors: []int{http.StatusNotFound},
	}), r.get)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "invoices-update", Method: http.MethodPatch, Path: prefix + "/{invoice_id}", Tags: tags,
		Summary: "Change a DRAFT invoice's fields and recompute its totals",
		Errors:  []int{http.StatusBadRequest, http.StatusConflict},
	}), r.update)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "invoices-delete", Method: http.MethodDelete, Path: prefix + "/{invoice_id}", Tags: tags,
		Summary: "Delete a DRAFT invoice and its lines", DefaultStatus: http.StatusNoContent,
		Errors: []int{http.StatusBadRequest, http.StatusConflict},
	}), r.delete)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "invoices-finalize", Method: http.MethodPost, Path: prefix + "/{invoice_id}/finalize", Tags: tags,
		Summary: "Issue a DRAFT invoice (OPEN, or PAID when nothing is owed)", Errors: []int{http.StatusBadRequest},
	}), r.finalize)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "invoices-void", Method: http.MethodPost, Path: prefix + "/{invoice_id}/void", Tags: tags,
		Summary: "Void any invoice that is not PAID", Errors: []int{http.StatusBadRequest},
	}), r.void)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "invoices-mark-paid", Method: http.MethodPost, Path: prefix + "/{invoice_id}/mark-paid", Tags: tags,
		Summary: "Settle an issued invoice with nothing outstanding to PAID", Errors: []int{http.StatusBadRequest},
	}), r.markPaid)

	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "invoices-create-recurring-template", Method: http.MethodPost, Path: prefix + "/recurring-template", Tags: tags,
		Summary: "Add a recurring template (a DRAFT RECURRING invoice with a schedule)", DefaultStatus: http.StatusCreated,
		Errors: []int{http.StatusBadRequest},
	}), r.createTemplate)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "invoices-list-recurring-templates", Method: http.MethodGet, Path: prefix + "/recurring-templates", Tags: tags,
		Summary: "List the recurring templates, newest first",
	}), r.listTemplates)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "invoices-recurring-template-rows", Method: http.MethodGet, Path: prefix + "/recurring-templates/rows", Tags: tags,
		Summary: "The recurring templates with their schedule, next and previous cycle and status, newest first",
	}), r.templateRows)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "invoices-get-recurring-template", Method: http.MethodGet, Path: prefix + "/recurring-templates/{template_id}", Tags: tags,
		Summary: "A recurring template", Errors: []int{http.StatusNotFound},
	}), r.getTemplate)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "invoices-update-recurring-template", Method: http.MethodPut, Path: prefix + "/recurring-templates/{template_id}", Tags: tags,
		Summary: "Replace a recurring template that has not ended (its pause is kept)",
		Errors:  []int{http.StatusBadRequest, http.StatusConflict},
	}), r.updateTemplate)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "invoices-act-on-recurring-template", Method: http.MethodPatch, Path: prefix + "/recurring-templates/{template_id}", Tags: tags,
		Summary: "Pause, resume or end a recurring template",
		Errors:  []int{http.StatusBadRequest, http.StatusNotFound, http.StatusConflict},
	}), r.actOnTemplate)
}

// clientError is err as the client reads it: an ErrInvoicing refusal is a
// 400 with Python's sentence; anything else is unchanged.
func clientError(err error) error {
	if errors.Is(err, ErrInvoicing) {
		return platform.Err(http.StatusBadRequest, refusalCode(err), err.Error())
	}
	return err
}

// inTx runs write in one transaction and answers its invoice.
func (r routes) inTx(ctx context.Context, write func(pgx.Tx) (InvoiceOut, error)) (*invoiceBody, error) {
	var out InvoiceOut
	err := db.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		var err error
		out, err = write(tx)
		return err
	})
	if err != nil {
		return nil, clientError(err)
	}
	return &invoiceBody{Body: out}, nil
}

type invoiceBody struct{ Body InvoiceOut }

type createIn struct{ Body InvoiceCreate }

func (r routes) create(ctx context.Context, in *createIn) (*invoiceBody, error) {
	if err := checkFields(in.Body.InvoiceFields, in.Body.LineItems); err != nil {
		return nil, err
	}
	today := todayUTC(r.clock)
	return r.inTx(ctx, func(tx pgx.Tx) (InvoiceOut, error) { return createInvoice(ctx, tx, in.Body, today) })
}

type milestoneIn struct{ Body MilestoneInvoiceCreate }

func (r routes) createMilestone(ctx context.Context, in *milestoneIn) (*invoiceBody, error) {
	if in.Body.Amount.Decimal().Sign() <= 0 {
		return nil, platform.FieldError("amount", notPositiveSentence)
	}
	today := todayUTC(r.clock)
	return r.inTx(ctx, func(tx pgx.Tx) (InvoiceOut, error) { return createMilestone(ctx, tx, in.Body, today) })
}

type listIn struct {
	Status      []string                          `query:"status,explode" doc:"Repeat for several (status=SENT&status=PARTIAL)."`
	CustomerID  platform.OptionalParam[uuid.UUID] `query:"customer_id"`
	InvoiceType string                            `query:"invoice_type" doc:"Empty lists every type."`
	Limit       int32                             `query:"limit" default:"200" minimum:"1" maximum:"500"`
}

type listOut struct{ Body []InvoiceOut }

// list filters as Python's truthiness does (invoices.py:444-449): no status
// or an empty invoice_type is no filter.
func (r routes) list(ctx context.Context, in *listIn) (*listOut, error) {
	var statuses []string
	if len(in.Status) > 0 {
		statuses = in.Status
	}
	var invoiceType *string
	if in.InvoiceType != "" {
		invoiceType = &in.InvoiceType
	}
	invs, err := r.q.ListInvoices(ctx, invoicesdb.ListInvoicesParams{
		Statuses: statuses, CustomerID: pgUUID(in.CustomerID.Ptr()), InvoiceType: invoiceType, RowLimit: in.Limit,
	})
	if err != nil {
		return nil, err
	}
	out, err := list(ctx, r.q, invs)
	if err != nil {
		return nil, err
	}
	return &listOut{Body: out}, nil
}

func (r routes) awaitingFinalization(ctx context.Context, _ *struct{}) (*listOut, error) {
	invs, err := r.q.ListAwaitingFinalization(ctx)
	if err != nil {
		return nil, err
	}
	out, err := list(ctx, r.q, invs)
	if err != nil {
		return nil, err
	}
	return &listOut{Body: out}, nil
}

type idIn struct {
	InvoiceID uuid.UUID `path:"invoice_id"`
}

func (r routes) get(ctx context.Context, in *idIn) (*invoiceBody, error) {
	out, err := Get(ctx, r.q, in.InvoiceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, platform.Err(http.StatusNotFound, "invoice_not_found", "invoice not found")
	}
	if err != nil {
		return nil, err
	}
	return &invoiceBody{Body: out}, nil
}

type updateIn struct {
	InvoiceID uuid.UUID `path:"invoice_id"`
	Body      InvoiceUpdate
}

func (r routes) update(ctx context.Context, in *updateIn) (*invoiceBody, error) {
	var lines []LineItemIn
	if in.Body.LineItems != nil {
		lines = *in.Body.LineItems
	}
	if err := checkFields(in.Body.InvoiceFields, lines); err != nil {
		return nil, err
	}
	return r.inTx(ctx, func(tx pgx.Tx) (InvoiceOut, error) { return updateInvoice(ctx, tx, in.InvoiceID, in.Body) })
}
