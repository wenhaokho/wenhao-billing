package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
)

type customerUpdateSchema customerCreateSchema

func registerCustomerUpdate(api huma.API, s sessionHandler) {
	response := func(desc string, body any) *huma.Response {
		return &huma.Response{Description: desc, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(body), true, "")}}}
	}
	responses := map[string]*huma.Response{"401": response("Not authenticated", authErrorBody{}), "403": response("Admin required", authErrorBody{}), "404": response("Customer not found", authErrorBody{}), "413": response("Request body too large", authErrorBody{}), "422": response("Validation error", validationErrorBody{}), "500": response("Internal server error", authErrorBody{})}
	huma.Register(api, huma.Operation{Method: "PATCH", Path: "/api/v1/customers/{customer_id}", OperationID: "update-customer", SkipValidateBody: true, Security: []map[string][]string{{"sessionCookie": {}}}, Middlewares: huma.Middlewares{sessionMiddleware(s.customerUpdate)}, Responses: responses}, func(context.Context, *struct {
		ID   string `path:"customer_id" format:"uuid"`
		Body customerUpdateSchema
	}) (*struct{ Body customerOutputSchema }, error) {
		panic("middleware")
	})
	schema := api.OpenAPI().Components.Schemas.Map()["CustomerUpdateSchema"]
	// Reuse the create contract's field constraints while allowing omission and null.
	source := api.OpenAPI().Components.Schemas.Map()["CustomerCreateSchema"]
	encoded, _ := json.Marshal(source)
	_ = json.Unmarshal(encoded, schema)
	schema.Required = nil
	schema.AdditionalProperties = true
	for _, name := range []string{"name", "matching_aliases"} {
		base := *schema.Properties[name]
		*schema.Properties[name] = huma.Schema{AnyOf: []*huma.Schema{&base, {Type: "null"}}, Default: json.RawMessage("null")}
	}

}

func (s sessionHandler) customerUpdate(w http.ResponseWriter, r *http.Request) {
	body, ok := readItemBody(w, r)
	if !ok {
		return
	}
	if len(body) > 0 {
		d := json.NewDecoder(bytes.NewReader(body))
		d.UseNumber()
		var v any
		if err := d.Decode(&v); err != nil {
			validationResponse(w, itemUpdateJSONError(body, err, false))
			return
		}
		if err := d.Decode(&v); err != io.EOF {
			validationResponse(w, itemUpdateJSONError(body, err, true))
			return
		}
	}
	current, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	_, errs, _ := parseCustomerUpdate(body, customerWithholding{"NONE", "", false})
	raw := chi.URLParam(r, "customer_id")
	id, valid := parsePathUUID(raw)
	if !valid {
		d := uuidErrorDetail(raw)
		errs = append([]validationError{{"uuid_parsing", []any{"path", "customer_id"}, "Input should be a valid UUID, " + d, raw, map[string]string{"error": d}}}, errs...)
	}
	if len(errs) > 0 {
		validationResponse(w, errs)
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
	c, err := q.LockCustomer(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		authError(w, 404, "customer not found")
		return
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	rate := ""
	if p := numericString(c.DefaultWhtRate); p != nil {
		rate = *p
	}
	input, errs, domain := parseCustomerUpdate(body, customerWithholding{c.DefaultWhtType, rate, c.DefaultWhtGrossUp})
	if len(errs) > 0 {
		validationResponse(w, errs)
		return
	}
	if domain != "" {
		authError(w, 422, domain)
		return
	}
	// JSON null arrays cannot satisfy CustomerOut; avoid committing an invalid response.
	if input.MatchingAliases.Null || input.QuotationCcEmails.Null || input.BillingCcEmails.Null {
		authError(w, 500, "internal server error")
		return
	}
	data, err := json.Marshal(input)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	c, err = q.ApplyCustomerUpdate(ctx, dbquery.ApplyCustomerUpdateParams{Data: data, CustomerID: id})
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	out, err := customerJSON(c)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if err = tx.Commit(ctx); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(append(encoded, '\n'))
}
