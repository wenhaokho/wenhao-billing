package platform

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
)

type invoiceWriter struct {
	pool    transactionBeginner
	admin   func(http.ResponseWriter, *http.Request) (pgtype.UUID, bool)
	queries func(pgx.Tx) invoiceWriteQueries
	scoped  bool
}

type invoiceWriteQueries interface {
	lockAdminRole(context.Context, pgtype.UUID) (string, error)
	lockCustomer(context.Context, pgtype.UUID) error
	lockProject(context.Context, pgtype.UUID) error
	lockItem(context.Context, pgtype.UUID, bool) error
	LockMilestoneCustomer(context.Context, pgtype.UUID) (dbquery.LockMilestoneCustomerRow, error)
	LockMilestoneProject(context.Context, pgtype.UUID) (pgtype.UUID, error)
	InsertMilestoneInvoice(context.Context, dbquery.InsertMilestoneInvoiceParams) error
	LockInvoiceNumberAllocation(context.Context) error
	InvoiceNumbersForPrefix(context.Context, pgtype.Text) ([]pgtype.Text, error)
	CustomerWithholdingDefaults(context.Context, pgtype.UUID) (dbquery.CustomerWithholdingDefaultsRow, error)
	InsertDraftInvoice(context.Context, dbquery.InsertDraftInvoiceParams) error
	InsertDraftInvoiceLine(context.Context, dbquery.InsertDraftInvoiceLineParams) error
	MarkInvoiceTemplate(context.Context, dbquery.MarkInvoiceTemplateParams) error
	LockInvoice(context.Context, pgtype.UUID) (dbquery.Invoice, error)
	GetInvoice(context.Context, pgtype.UUID) (dbquery.Invoice, error)
	LockInvoiceLines(context.Context, pgtype.UUID) ([]dbquery.InvoiceLineItem, error)
	InvoiceLinesByIDs(context.Context, []pgtype.UUID) ([]dbquery.InvoiceLineItem, error)
	DeleteInvoiceLines(context.Context, pgtype.UUID) error
	DeleteInvoice(context.Context, pgtype.UUID) error
	TransitionInvoice(context.Context, dbquery.TransitionInvoiceParams) error
	lockInvoiceHostingItems(context.Context, pgtype.UUID) error
	UpdateDraftInvoice(context.Context, dbquery.UpdateDraftInvoiceParams) error
	LatestRecurringChild(context.Context, pgtype.UUID) (dbquery.LatestRecurringChildRow, error)
	UpdateTemplateSchedule(context.Context, dbquery.UpdateTemplateScheduleParams) error
}

func (s sessionHandler) invoiceWriter() invoiceWriter {
	return invoiceWriter{
		pool: s.db,
		admin: func(w http.ResponseWriter, r *http.Request) (pgtype.UUID, bool) {
			u, ok := s.requireAdmin(w, r)
			return u.UserID, ok
		},
		queries: func(tx pgx.Tx) invoiceWriteQueries {
			return legacyInvoiceWriteQueries{Queries: dbquery.New(tx), tx: tx}
		},
	}
}

func (s invoiceWriter) acceptReference(w http.ResponseWriter, internalError func(), err error) bool {
	if errors.Is(err, pgx.ErrNoRows) {
		if !s.scoped {
			return true
		}
		authError(w, 400, "referenced record not found")
		return false
	}
	if err != nil {
		internalError()
		return false
	}
	return true
}

func (s invoiceWriter) invoiceWriteError(w http.ResponseWriter, err error, internal func()) {
	var pgErr *pgconn.PgError
	if s.scoped && errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "uq_invoices_invoice_number" {
		authError(w, 409, "invoice number already exists")
		return
	}
	internal()
}

type legacyInvoiceWriteQueries struct {
	*dbquery.Queries
	tx pgx.Tx
}

func (q legacyInvoiceWriteQueries) lockAdminRole(ctx context.Context, id pgtype.UUID) (string, error) {
	user, err := q.LockSessionUserByID(ctx, id)
	return user.Role, err
}

func (q legacyInvoiceWriteQueries) lockCustomer(ctx context.Context, id pgtype.UUID) error {
	_, err := q.LockCustomer(ctx, id)
	return err
}

func (q legacyInvoiceWriteQueries) lockProject(ctx context.Context, id pgtype.UUID) error {
	_, err := q.LockProject(ctx, id)
	return err
}

func (q legacyInvoiceWriteQueries) lockItem(ctx context.Context, id pgtype.UUID, template bool) error {
	if template {
		_, err := q.LockItem(ctx, id)
		return err
	}
	_, err := q.tx.Exec(ctx, `SELECT item_id FROM items WHERE item_id=$1 FOR KEY SHARE`, id)
	return err
}

func (q legacyInvoiceWriteQueries) lockInvoiceHostingItems(ctx context.Context, id pgtype.UUID) error {
	_, err := q.tx.Exec(ctx, `SELECT i.item_id FROM items i WHERE i.is_hosting AND EXISTS (SELECT 1 FROM invoice_line_items l WHERE l.invoice_id=$1 AND l.item_id=i.item_id) ORDER BY i.item_id FOR UPDATE /* task68 invoice item lock */`, id)
	return err
}
