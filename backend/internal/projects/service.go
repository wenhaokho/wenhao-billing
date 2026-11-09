package projects

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/internal/projects/projectsdb"
)

// ListFilter narrows List; the zero value lists every project. Statuses
// and CustomerID filter when set (an empty Statuses is no filter, as in
// Python); Active filters when set; NameLike is a name substring, already
// escaped by the caller for LIKE (nil is no filter); Limit caps the page
// when set (nil is unlimited, 0 lists nothing).
type ListFilter struct {
	Statuses   []string
	CustomerID *uuid.UUID
	Active     *bool
	NameLike   *string
	Limit      *int32
}

// Get is the project, read through q (a pool or a transaction). An unknown
// id is pgx.ErrNoRows, unwrapped: test it with
// errors.Is(err, pgx.ErrNoRows).
func Get(ctx context.Context, q *projectsdb.Queries, id uuid.UUID) (ProjectOut, error) {
	p, err := q.GetProject(ctx, id)
	if err != nil {
		return ProjectOut{}, err
	}
	return projectOut(p), nil
}

// List is the projects matching f, rendered as the routes render them,
// newest first with the id breaking ties, never nil. Exported for the MCP
// list_projects tool (inventory §4).
func List(ctx context.Context, q *projectsdb.Queries, f ListFilter) ([]ProjectOut, error) {
	var statuses []string
	if len(f.Statuses) > 0 {
		statuses = f.Statuses
	}
	var customer pgtype.UUID
	if f.CustomerID != nil {
		customer = pgtype.UUID{Bytes: *f.CustomerID, Valid: true}
	}
	rows, err := q.ListProjects(ctx, projectsdb.ListProjectsParams{
		Statuses: statuses, CustomerID: customer, Active: f.Active, NameLike: f.NameLike, RowLimit: f.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]ProjectOut, 0, len(rows))
	for _, p := range rows {
		out = append(out, projectOut(p))
	}
	return out, nil
}

// Create normalises the code, currency and budget, then writes the
// project through q (a pool or a transaction). Exported for the MCP
// create_project tool (inventory §4).
func Create(ctx context.Context, q *projectsdb.Queries, b ProjectCreate) (ProjectOut, error) {
	code, err := normCode(b.Code)
	if err != nil {
		return ProjectOut{}, err
	}
	currency, err := normCurrency(b.Currency)
	if err != nil {
		return ProjectOut{}, err
	}
	budget, err := contractValue(b.ContractValue, decimal.NullDecimal{})
	if err != nil {
		return ProjectOut{}, err
	}
	p, err := q.CreateProject(ctx, projectsdb.CreateProjectParams{
		ProjectID:     uuid.New(),
		CustomerID:    b.CustomerID,
		Code:          code,
		Name:          strings.TrimSpace(b.Name),
		Currency:      currency,
		ContractValue: budget,
		Status:        b.Status.Or("ACTIVE"),
		StartDate:     platform.MergeDate(b.StartDate, pgtype.Date{}),
		EndDate:       platform.MergeDate(b.EndDate, pgtype.Date{}),
		Notes:         b.Notes.Merge(nil),
	})
	if err != nil {
		return ProjectOut{}, mapWrite(err, code)
	}
	return projectOut(p), nil
}

// Update is the PATCH pattern on q, a transaction: read the row locked,
// overlay the fields sent, and write every column back. An unknown id is
// pgx.ErrNoRows. Exported for the MCP update_project tool (inventory §4).
func Update(ctx context.Context, q *projectsdb.Queries, id uuid.UUID, b ProjectUpdate) (ProjectOut, error) {
	cur, err := q.GetProjectForUpdate(ctx, id)
	if err != nil {
		return ProjectOut{}, err
	}
	code, currency := cur.Code, cur.Currency
	if b.Code.Set {
		if code, err = normCode(b.Code.Value); err != nil {
			return ProjectOut{}, err
		}
	}
	if b.Currency.Set {
		if currency, err = normCurrency(b.Currency.Value); err != nil {
			return ProjectOut{}, err
		}
	}
	budget, err := contractValue(b.ContractValue, cur.ContractValue)
	if err != nil {
		return ProjectOut{}, err
	}
	p, err := q.UpdateProject(ctx, projectsdb.UpdateProjectParams{
		ProjectID:     cur.ProjectID,
		CustomerID:    b.CustomerID.Or(cur.CustomerID),
		Code:          code,
		Name:          b.Name.Or(cur.Name),
		Currency:      currency,
		ContractValue: budget,
		Status:        b.Status.Or(cur.Status),
		StartDate:     platform.MergeDate(b.StartDate, cur.StartDate),
		EndDate:       platform.MergeDate(b.EndDate, cur.EndDate),
		Notes:         b.Notes.Merge(cur.Notes),
		Active:        b.Active.Or(cur.Active),
	})
	if err != nil {
		return ProjectOut{}, mapWrite(err, "")
	}
	return projectOut(p), nil
}
