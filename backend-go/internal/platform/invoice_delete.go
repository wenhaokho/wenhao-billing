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

func (s sessionHandler) invoiceDelete(w http.ResponseWriter, r *http.Request) {
	current, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	raw := chi.URLParam(r, "invoice_id")
	id, valid := parsePathUUID(raw)
	if !valid {
		d := uuidErrorDetail(raw)
		validationResponse(w, []validationError{{"uuid_parsing", []any{"path", "invoice_id"}, "Input should be a valid UUID, " + d, raw, map[string]string{"error": d}}})
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
	invoice, err := q.LockInvoice(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		authError(w, 400, fmt.Sprintf("invoice %s not found", id.String()))
		return
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if invoice.Status != "DRAFT" {
		authError(w, 400, fmt.Sprintf("only DRAFT invoices can be deleted (got %s)", invoice.Status))
		return
	}
	if err = q.DeleteInvoice(ctx, id); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if err = tx.Commit(ctx); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func registerInvoiceDelete(api huma.API, s sessionHandler) {
	response := func(desc string, body any) *huma.Response {
		return &huma.Response{Description: desc, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(body), true, "")}}}
	}
	responses := map[string]*huma.Response{"204": {Description: "Invoice deleted"}, "400": response("Invoice cannot be deleted", authErrorBody{}), "401": response("Not authenticated", authErrorBody{}), "403": response("Admin required", authErrorBody{}), "404": response("Invoice not found", authErrorBody{}), "422": response("Validation error", validationErrorBody{}), "500": response("Internal server error", authErrorBody{})}
	huma.Register(api, huma.Operation{Method: "DELETE", Path: "/api/v1/invoices/{invoice_id}", OperationID: "delete-invoice", Security: []map[string][]string{{"sessionCookie": {}}}, Middlewares: huma.Middlewares{sessionMiddleware(s.invoiceDelete)}, Responses: responses}, func(context.Context, *struct {
		ID string `path:"invoice_id" format:"uuid"`
	}) (*struct{}, error) {
		panic("middleware")
	})
}
