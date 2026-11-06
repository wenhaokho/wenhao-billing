package platform

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
)

func (s sessionHandler) projectDelete(w http.ResponseWriter, r *http.Request) {
	current, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	raw := chi.URLParam(r, "project_id")
	id, valid := parsePathUUID(raw)
	if !valid {
		d := uuidErrorDetail(raw)
		validationResponse(w, []validationError{{"uuid_parsing", []any{"path", "project_id"}, "Input should be a valid UUID, " + d, raw, map[string]string{"error": d}}})
		return
	}
	ctx := r.Context()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	defer tx.Rollback(ctx)
	q := dbquery.New(tx)
	user, err := q.LockSessionUserByID(ctx, current.UserID)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && user.Role != "admin" {
		authError(w, 403, "admin required")
		return
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	_, err = q.LockProject(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		authError(w, 404, "project not found")
		return
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	count, err := q.CountProjectReferences(ctx, id)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if count > 0 {
		authError(w, 400, fmt.Sprintf("cannot delete: project is referenced by %d invoice(s)/bill(s)", count))
		return
	}
	if err = q.DeleteProject(ctx, id); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if err = tx.Commit(ctx); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func registerProjectDelete(api huma.API, s sessionHandler) {
	response := func(desc string, body any) *huma.Response {
		return &huma.Response{Description: desc, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(body), true, "")}}}
	}
	responses := map[string]*huma.Response{"204": {Description: "Project deleted"}, "400": response("Project referenced", authErrorBody{}), "401": response("Not authenticated", authErrorBody{}), "403": response("Admin required", authErrorBody{}), "404": response("Project not found", authErrorBody{}), "422": response("Validation error", validationErrorBody{}), "500": response("Internal server error", authErrorBody{})}
	huma.Register(api, huma.Operation{Method: "DELETE", Path: "/api/v1/projects/{project_id}", OperationID: "delete-project", Security: []map[string][]string{{"sessionCookie": {}}}, Middlewares: huma.Middlewares{sessionMiddleware(s.projectDelete)}, Responses: responses}, func(context.Context, *struct {
		ID string `path:"project_id" format:"uuid"`
	}) (*struct{}, error) {
		panic("middleware")
	})
}
