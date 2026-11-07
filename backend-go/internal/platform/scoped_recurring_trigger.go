package platform

import (
	"context"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedinvoices"
)

func (h scopedProfileRouter) recurringTrigger(w http.ResponseWriter, r *http.Request) {
	serveRecurringTrigger(w, r, h.invoiceWriter(), func(tx pgx.Tx) recurringTriggerQueries {
		return scopedInvoiceWriteQueries{tx: tx, scope: h.scope}
	})
}

func (q scopedInvoiceWriteQueries) FindRecurringCycle(ctx context.Context, p dbquery.FindRecurringCycleParams) (dbquery.Invoice, error) {
	row, err := scopedinvoices.FindRecurringCycle(ctx, q.tx, q.scope, dbqueryv8.FindRecurringCycleParams{
		TemplateID: p.TemplateID,
		CycleKey:   p.CycleKey,
	})
	return legacyInvoice(row), err
}

func (q scopedInvoiceWriteQueries) ListRecurringCycleCandidates(ctx context.Context, id pgtype.UUID) ([]dbquery.Invoice, error) {
	rows, err := scopedinvoices.ListRecurringCycleCandidates(ctx, q.tx, q.scope, dbqueryv8.ListRecurringCycleCandidatesParams{TemplateID: id})
	if err != nil {
		return nil, err
	}
	out := make([]dbquery.Invoice, 0, len(rows))
	for _, row := range rows {
		out = append(out, legacyInvoice(row))
	}
	return out, nil
}

func (q scopedInvoiceWriteQueries) ItemIsHosting(ctx context.Context, id pgtype.UUID) (bool, error) {
	return scopedinvoices.ItemIsHosting(ctx, q.tx, q.scope, dbqueryv8.ItemIsHostingParams{ItemID: id})
}

func (q scopedInvoiceWriteQueries) SetRecurringChildFields(ctx context.Context, p dbquery.SetRecurringChildFieldsParams) error {
	return scopedinvoices.SetRecurringChildFields(ctx, q.tx, q.scope, dbqueryv8.SetRecurringChildFieldsParams{
		InvoiceID:       p.InvoiceID,
		BillingCycleRef: p.BillingCycleRef,
		CoverageStart:   p.CoverageStart,
		CoverageEnd:     p.CoverageEnd,
	})
}

func (q scopedInvoiceWriteQueries) validateTemplateReferences(ctx context.Context, template dbquery.Invoice) error {
	if template.CustomerID.Valid {
		if err := q.lockCustomer(ctx, template.CustomerID); err != nil {
			return err
		}
	}
	if template.ProjectID.Valid {
		return q.lockProject(ctx, template.ProjectID)
	}
	return nil
}

func (q scopedInvoiceWriteQueries) enqueueRecurringDraft(ctx context.Context, invoice pgtype.UUID, key string) error {
	return q.enqueueScopedRecurringDraft(ctx, invoice, key, pgtype.UUID{}, nil)
}

func (q scopedInvoiceWriteQueries) enqueueScopedRecurringDraft(ctx context.Context, invoice pgtype.UUID, key string, batch pgtype.UUID, recipients *scopedRecurringRecipients) error {
	if err := q.scope.Validate(); err != nil {
		return err
	}
	if recipients != nil && recipients.scope != q.scope {
		return errors.New("recurring recipient snapshot belongs to a different business")
	}
	event, err := q.tx.Exec(ctx, `
		INSERT INTO recurring_draft_events(business_id, invoice_id, cycle_key, invoice_number, customer_name, amount, currency, scan_batch_id)
		SELECT i.business_id, i.invoice_id, $3, coalesce(i.invoice_number,''), coalesce(c.name,'(unknown customer)'), i.amount, i.currency, $4
		FROM invoices i
		LEFT JOIN customers c ON c.business_id=i.business_id AND c.customer_id=i.customer_id
		WHERE i.business_id=$1 AND i.invoice_id=$2`, q.scope.UUID(), invoice, key, batch)
	if err != nil {
		return err
	}
	if event.RowsAffected() != 1 {
		return pgx.ErrNoRows
	}
	if recipients != nil {
		_, err = q.tx.Exec(ctx, `
			INSERT INTO recurring_draft_deliveries(business_id, invoice_id, user_id, email)
			SELECT e.business_id, e.invoice_id, r.id, r.email
			FROM recurring_draft_events e
			CROSS JOIN unnest($3::uuid[], $4::text[]) AS r(id,email)
			WHERE e.business_id=$1 AND e.invoice_id=$2`, q.scope.UUID(), invoice, recipients.ids, recipients.emails)
		return err
	}
	_, err = q.tx.Exec(ctx, `
		INSERT INTO recurring_draft_deliveries(business_id, invoice_id, user_id, email)
		SELECT e.business_id, e.invoice_id, u.user_id, u.email
		FROM recurring_draft_events e
		JOIN users u ON u.business_id=e.business_id
		WHERE e.business_id=$1 AND e.invoice_id=$2`, q.scope.UUID(), invoice)
	return err
}
