package projects

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LOQATO/gokit/db"

	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/internal/projects/projectsdb"
)

type routes struct {
	pool *pgxpool.Pool
	q    *projectsdb.Queries
}

// Register adds the project operations; every one is admin-only.
func Register(api huma.API, pool *pgxpool.Pool) {
	r := routes{pool: pool, q: projectsdb.New(pool)}
	const prefix = "/api/v1/projects"
	tags := []string{"projects"}

	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "projects-create", Method: http.MethodPost, Path: prefix, Tags: tags,
		Summary: "Add a project", DefaultStatus: http.StatusCreated,
		Errors: []int{http.StatusBadRequest},
	}), r.create)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "projects-list", Method: http.MethodGet, Path: prefix, Tags: tags,
		Summary: "List projects, newest first",
	}), r.list)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "projects-get", Method: http.MethodGet, Path: prefix + "/{project_id}", Tags: tags,
		Summary: "A project", Errors: []int{http.StatusNotFound},
	}), r.get)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "projects-update", Method: http.MethodPatch, Path: prefix + "/{project_id}", Tags: tags,
		Summary: "Change a project's fields", Errors: []int{http.StatusBadRequest, http.StatusNotFound},
	}), r.update)
	huma.Register(api, platform.Admin(huma.Operation{
		OperationID: "projects-delete", Method: http.MethodDelete, Path: prefix + "/{project_id}", Tags: tags,
		Summary: "Delete a project nothing references", DefaultStatus: http.StatusNoContent,
		Errors: []int{http.StatusBadRequest, http.StatusNotFound},
	}), r.delete)
	registerSummary(api, r, prefix, tags)
}

// notFound is Python's 404 for a missing row; any other error is unchanged.
func notFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return platform.Err(http.StatusNotFound, "project_not_found", "project not found")
	}
	return err
}

// mapWrite is a failed write of a project row as the client error it is. Only
// the code's unique key (a constraint and an index, both named) is the code
// sentence: with the stored code on create (Python's repr of it), without it
// on PATCH. An unknown customer is the 422 at customer_id (Python answered
// the code sentence for it, and for a null in a NOT NULL column). Any other
// error comes back unchanged.
func mapWrite(err error, createdCode string) error {
	var pg *pgconn.PgError
	if !errors.As(err, &pg) {
		return err
	}
	switch pg.ConstraintName {
	case "projects_code_key", "ix_projects_code":
		sentence := "project code already exists"
		if createdCode != "" {
			sentence = fmt.Sprintf("project code %s already exists", platform.PyRepr(createdCode))
		}
		return platform.Err(http.StatusBadRequest, "project_code_exists", sentence)
	case "projects_customer_id_fkey":
		return platform.FieldError("customer_id", "customer not found")
	}
	return err
}

type projectBody struct{ Body ProjectOut }

type createIn struct{ Body ProjectCreate }

func (r routes) create(ctx context.Context, in *createIn) (*projectBody, error) {
	out, err := Create(ctx, r.q, in.Body)
	if err != nil {
		return nil, err
	}
	return &projectBody{Body: out}, nil
}

type listIn struct {
	Status     []string                          `query:"status,explode" doc:"Repeat for several (status=ACTIVE&status=ON_HOLD)."`
	CustomerID platform.OptionalParam[uuid.UUID] `query:"customer_id"`
	Active     platform.OptionalParam[bool]      `query:"active"`
}

type listOut struct{ Body []ProjectOut }

func (r routes) list(ctx context.Context, in *listIn) (*listOut, error) {
	out, err := List(ctx, r.q, ListFilter{Statuses: in.Status, CustomerID: in.CustomerID.Ptr(), Active: in.Active.Ptr()})
	if err != nil {
		return nil, err
	}
	return &listOut{Body: out}, nil
}

type idIn struct {
	ProjectID uuid.UUID `path:"project_id"`
}

func (r routes) get(ctx context.Context, in *idIn) (*projectBody, error) {
	out, err := Get(ctx, r.q, in.ProjectID)
	if err != nil {
		return nil, notFound(err)
	}
	return &projectBody{Body: out}, nil
}

type updateIn struct {
	ProjectID uuid.UUID `path:"project_id"`
	Body      ProjectUpdate
}

// update is the PATCH pattern: the row locked, overlaid and written
// back in one transaction (Update).
func (r routes) update(ctx context.Context, in *updateIn) (*projectBody, error) {
	var out ProjectOut
	err := db.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		var err error
		out, err = Update(ctx, r.q.WithTx(tx), in.ProjectID, in.Body)
		return err
	})
	if err != nil {
		return nil, notFound(err)
	}
	return &projectBody{Body: out}, nil
}

// delete refuses a project anything points at, counted under the project
// row's lock (a new invoice, bill or quote takes a share lock on the row
// through its foreign key, so none slips in between the count and the
// delete). Python's sentence is kept though quotations are counted now.
func (r routes) delete(ctx context.Context, in *idIn) (*struct{}, error) {
	err := db.InTx(ctx, r.pool, func(tx pgx.Tx) error {
		q := r.q.WithTx(tx)
		if _, err := q.GetProjectForUpdate(ctx, in.ProjectID); err != nil {
			return notFound(err)
		}
		n, err := q.CountProjectReferences(ctx, in.ProjectID)
		if err != nil {
			return err
		}
		if n > 0 {
			return platform.Err(http.StatusBadRequest, "project_referenced",
				fmt.Sprintf("cannot delete: project is referenced by %d invoice(s)/bill(s)", n))
		}
		return q.DeleteProject(ctx, in.ProjectID)
	})
	if err != nil {
		return nil, err
	}
	return nil, nil
}
