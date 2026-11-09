package projects

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"

	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices"
	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices/invoicesdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// ProjectSummary is the money picture of one project. The sums add every
// currency together as Python did, and are never rounded.
type ProjectSummary struct {
	ProjectID         uuid.UUID      `json:"project_id"`
	InvoicedTotal     platform.Dec4  `json:"invoiced_total"`
	PaidTotal         platform.Dec4  `json:"paid_total"`
	OutstandingTotal  platform.Dec4  `json:"outstanding_total"`
	ContractValue     *platform.Dec4 `json:"contract_value"`
	ContractRemaining *platform.Dec4 `json:"contract_remaining"`
	InvoiceCount      int            `json:"invoice_count"`
	BillTotal         platform.Dec4  `json:"bill_total"`
}

func registerSummary(api huma.API, r routes, prefix string, tags []string) {
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "projects-summary", Method: http.MethodGet, Path: prefix + "/{project_id}/summary", Tags: tags,
		Summary: "A project's invoiced, paid, outstanding and bill totals", Errors: []int{http.StatusNotFound},
	}), r.summary)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "projects-invoices", Method: http.MethodGet, Path: prefix + "/{project_id}/invoices", Tags: tags,
		Summary: "A project's invoices, templates excluded, newest first", Errors: []int{http.StatusNotFound},
	}), r.invoices)
}

type summaryOut struct{ Body ProjectSummary }

func (r routes) summary(ctx context.Context, in *idIn) (*summaryOut, error) {
	p, err := r.q.GetProject(ctx, in.ProjectID)
	if err != nil {
		return nil, notFound(err)
	}
	inv, err := r.q.ProjectInvoiceTotals(ctx, in.ProjectID)
	if err != nil {
		return nil, err
	}
	bills, err := r.q.ProjectBillTotal(ctx, in.ProjectID)
	if err != nil {
		return nil, err
	}
	s := ProjectSummary{
		ProjectID:        p.ProjectID,
		InvoicedTotal:    platform.NewDec4(inv.Invoiced),
		PaidTotal:        platform.NewDec4(inv.Invoiced.Sub(inv.Outstanding)),
		OutstandingTotal: platform.NewDec4(inv.Outstanding),
		InvoiceCount:     int(inv.InvoiceCount),
		BillTotal:        platform.NewDec4(bills),
	}
	if p.ContractValue.Valid {
		cv := platform.NewDec4(p.ContractValue.Decimal)
		rem := platform.NewDec4(p.ContractValue.Decimal.Sub(inv.Invoiced))
		s.ContractValue, s.ContractRemaining = &cv, &rem
	}
	return &summaryOut{Body: s}, nil
}

type invoicesOut struct{ Body []invoices.InvoiceOut }

func (r routes) invoices(ctx context.Context, in *idIn) (*invoicesOut, error) {
	if _, err := r.q.GetProject(ctx, in.ProjectID); err != nil {
		return nil, notFound(err)
	}
	out, err := invoices.ListBy(ctx, invoicesdb.New(r.pool), invoices.ListFilter{ProjectID: &in.ProjectID, ExcludeTemplates: true})
	if err != nil {
		return nil, err
	}
	return &invoicesOut{Body: out}, nil
}
