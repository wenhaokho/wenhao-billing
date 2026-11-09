package projects_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LOQATO/gokit/db"

	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/internal/projects"
	"github.com/wenhaokho/wenhao-billing/backend/internal/projects/projectsdb"
)

func projectQ(pool *pgxpool.Pool) *projectsdb.Queries { return projectsdb.New(pool) }

func TestExportProjectCreateGet(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	customer := insertCustomer(t, a)
	out, err := projects.Create(t.Context(), projectQ(a.Pool), projects.ProjectCreate{
		CustomerID: customer, Code: "exp-1", Name: "Export project", Currency: "idr",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Code != "EXP-1" || out.Currency != "IDR" || out.Status != "ACTIVE" || out.ProjectID == uuid.Nil {
		t.Errorf("created %+v", out)
	}
	got, err := projects.Get(t.Context(), projectQ(a.Pool), out.ProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProjectID != out.ProjectID {
		t.Errorf("get %+v", got)
	}
}

func TestExportProjectListCustomerLimit(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	customer := insertCustomer(t, a)
	other := insertCustomer(t, a)
	create(t, a, body(customer, "P-1", nil))
	create(t, a, body(other, "P-2", nil))
	found, err := projects.List(t.Context(), projectQ(a.Pool), projects.ListFilter{CustomerID: &customer})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].CustomerID != customer {
		t.Errorf("customer %+v", found)
	}
	one := int32(1)
	limited, err := projects.List(t.Context(), projectQ(a.Pool), projects.ListFilter{Limit: &one})
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 1 {
		t.Errorf("limit %+v", limited)
	}
}

func TestExportProjectUpdate(t *testing.T) {
	t.Parallel()
	a := newApp(t)
	customer := insertCustomer(t, a)
	created := create(t, a, body(customer, "P-9", nil))
	var updated projects.ProjectOut
	err := db.InTx(t.Context(), a.Pool, func(tx pgx.Tx) error {
		var err error
		updated, err = projects.Update(t.Context(), projectsdb.New(a.Pool).WithTx(tx), created.ProjectID, projects.ProjectUpdate{
			Name: platform.NotNull[string]{Set: true, Value: "Renamed project"},
		})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "Renamed project" || updated.Code != "P-9" {
		t.Errorf("updated %+v", updated)
	}
	_, err = projects.Update(t.Context(), projectQ(a.Pool), uuid.New(), projects.ProjectUpdate{
		Name: platform.NotNull[string]{Set: true, Value: "x"},
	})
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("update unknown err = %v, want ErrNoRows", err)
	}
}
