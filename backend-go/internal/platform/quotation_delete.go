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
)

func (s quotationWriter) delete(w http.ResponseWriter, r *http.Request) {
	current, ok := s.admin(w, r)
	if !ok {
		return
	}
	raw := chi.URLParam(r, "quotation_id")
	id, valid := parsePathUUID(raw)
	if !valid {
		d := uuidErrorDetail(raw)
		validationResponse(w, []validationError{{"uuid_parsing", []any{"path", "quotation_id"}, "Input should be a valid UUID, " + d, raw, map[string]string{"error": d}}})
		return
	}
	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	defer tx.Rollback(ctx)
	q := s.queries(tx)
	role, err := q.lockAdminRole(ctx, current)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && role != "admin" {
		authError(w, 403, "admin required")
		return
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	quotation, err := q.LockQuotation(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		s.quotationNotFound(w, id)
		return
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if quotation.Status != "DRAFT" {
		authError(w, 400, fmt.Sprintf("only DRAFT quotations can be deleted (got %s)", quotation.Status))
		return
	}
	if err = q.DeleteQuotationLines(ctx, id); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if err = q.deleteQuotation(ctx, id); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if err = tx.Commit(ctx); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func registerQuotationDelete(api huma.API, s sessionHandler) {
	response := func(desc string, body any) *huma.Response {
		return &huma.Response{Description: desc, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(body), true, "")}}}
	}
	responses := map[string]*huma.Response{"204": {Description: "Quotation deleted"}, "400": response("Quotation cannot be deleted", authErrorBody{}), "401": response("Not authenticated", authErrorBody{}), "403": response("Admin required", authErrorBody{}), "422": response("Validation error", validationErrorBody{}), "500": response("Internal server error", authErrorBody{})}
	huma.Register(api, huma.Operation{Method: "DELETE", Path: "/api/v1/quotations/{quotation_id}", OperationID: "delete-quotation", Security: []map[string][]string{{"sessionCookie": {}}}, Middlewares: huma.Middlewares{sessionMiddleware(s.quotationWriter().delete)}, Responses: responses}, func(context.Context, *struct {
		ID string `path:"quotation_id" format:"uuid"`
	}) (*struct{}, error) {
		panic("middleware")
	})
}
