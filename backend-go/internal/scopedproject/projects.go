// Package scopedproject provides development-only v8 project access.
package scopedproject

import (
	"context"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wenhaokho/wenhao-billing/backend/internal/business"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
)

func CountProjectReferences(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, arg dbqueryv8.CountProjectReferencesParams) (int64, error) {
	if err := scope.Validate(); err != nil {
		return 0, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).CountProjectReferences(ctx, arg)
}
func CreateProject(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, arg dbqueryv8.CreateProjectParams) (dbqueryv8.Project, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.Project{}, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).CreateProject(ctx, arg)
}
func DeleteProject(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, arg dbqueryv8.DeleteProjectParams) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).DeleteProject(ctx, arg)
}
func GetProject(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, arg dbqueryv8.GetProjectParams) (dbqueryv8.Project, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.Project{}, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).GetProject(ctx, arg)
}
func ListProjects(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, arg dbqueryv8.ListProjectsParams) ([]dbqueryv8.Project, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).ListProjects(ctx, arg)
}
func LockProject(ctx context.Context, db pgx.Tx, scope business.ID, arg dbqueryv8.LockProjectParams) (dbqueryv8.Project, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.Project{}, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).LockProject(ctx, arg)
}
func ProjectBillStats(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, arg dbqueryv8.ProjectBillStatsParams) (pgtype.Numeric, error) {
	if err := scope.Validate(); err != nil {
		return pgtype.Numeric{}, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).ProjectBillStats(ctx, arg)
}
func ProjectInvoiceLines(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, arg dbqueryv8.ProjectInvoiceLinesParams) ([]dbqueryv8.InvoiceLineItem, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).ProjectInvoiceLines(ctx, arg)
}
func ProjectInvoiceStats(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, arg dbqueryv8.ProjectInvoiceStatsParams) (dbqueryv8.ProjectInvoiceStatsRow, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.ProjectInvoiceStatsRow{}, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).ProjectInvoiceStats(ctx, arg)
}
func ProjectInvoices(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, arg dbqueryv8.ProjectInvoicesParams) ([]dbqueryv8.Invoice, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).ProjectInvoices(ctx, arg)
}
func UpdateProject(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, arg dbqueryv8.UpdateProjectParams) (dbqueryv8.Project, error) {
	if err := scope.Validate(); err != nil {
		return dbqueryv8.Project{}, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).UpdateProject(ctx, arg)
}

func ProjectReportHasDamagedReferences(ctx context.Context, db dbqueryv8.DBTX, scope business.ID, arg dbqueryv8.ProjectReportHasDamagedReferencesParams) (bool, error) {
	if err := scope.Validate(); err != nil {
		return false, err
	}
	arg.BusinessID = scope.UUID()
	return dbqueryv8.New(db).ProjectReportHasDamagedReferences(ctx, arg)
}
