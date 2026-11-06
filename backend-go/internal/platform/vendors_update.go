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

type vendorUpdateSchema struct {
	Name             *string `json:"name,omitempty"`
	ContactEmail     *string `json:"contact_email,omitempty" format:"email"`
	ContactName      *string `json:"contact_name,omitempty"`
	DefaultCurrency  *string `json:"default_currency,omitempty" minLength:"3" maxLength:"3"`
	PaymentTermsDays *int32  `json:"payment_terms_days,omitempty" minimum:"0" maximum:"365"`
	TaxID            *string `json:"tax_id,omitempty"`
	Notes            *string `json:"notes,omitempty"`
	Active           *bool   `json:"active,omitempty"`
}

func parseVendorUpdate(body []byte) (map[string]any, []validationError) {
	if len(body) == 0 {
		return nil, []validationError{{"missing", []any{"body"}, "Field required", nil, nil}}
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	var value any
	if err := d.Decode(&value); err != nil {
		return nil, itemUpdateJSONError(body, err, false)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return nil, itemUpdateJSONError(body, err, true)
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return nil, []validationError{{"model_attributes_type", []any{"body"}, "Input should be a valid dictionary or object to extract fields from", value, nil}}
	}
	filtered := map[string]any{"name": "placeholder"}
	result := map[string]any{}
	fields := []string{"name", "contact_email", "contact_name", "default_currency", "payment_terms_days", "tax_id", "notes", "active"}
	for _, k := range fields {
		if v, exists := obj[k]; exists {
			result[k] = v
			if k != "name" && v != nil {
				filtered[k] = v
			}
		}
	}
	b, _ := json.Marshal(filtered)
	p, errs := parseVendorCreate(b)
	if name, exists := result["name"]; exists && name != nil {
		if _, ok := name.(string); !ok {
			errs = append([]validationError{{"string_type", []any{"body", "name"}, "Input should be a valid string", name, nil}}, errs...)
		}
	}
	if len(errs) > 0 {
		return nil, errs
	}
	if v, exists := result["contact_email"]; exists && v != nil {
		result["contact_email"] = p.ContactEmail.String
	}
	if v, exists := result["payment_terms_days"]; exists && v != nil {
		result["payment_terms_days"] = p.PaymentTermsDays
	}
	if v, exists := result["active"]; exists && v != nil {
		result["active"] = p.Active
	}
	return result, nil
}

type vendorUpdateValidationDetail struct {
	Type  string         `json:"type"`
	Loc   []any          `json:"loc"`
	Msg   string         `json:"msg"`
	Input string         `json:"input" enum:"[REDACTED]"`
	Ctx   map[string]any `json:"ctx,omitempty"`
}
type vendorUpdateValidationErrorBody struct {
	Detail []vendorUpdateValidationDetail `json:"detail"`
}

func vendorUpdateValidationResponse(w http.ResponseWriter, errs []validationError) {
	for i := range errs {
		errs[i].Input = "[REDACTED]"
	}
	validationResponse(w, errs)
}

func (s sessionHandler) vendorUpdate(w http.ResponseWriter, r *http.Request) {
	body, ok := readItemBody(w, r)
	if !ok {
		return
	}
	isJSON := requestJSONMediaType(r)
	if len(body) > 0 && isJSON {
		d := json.NewDecoder(bytes.NewReader(body))
		d.UseNumber()
		var v any
		if err := d.Decode(&v); err != nil {
			vendorUpdateValidationResponse(w, itemUpdateJSONError(body, err, false))
			return
		}
		if err := d.Decode(&v); err != io.EOF {
			vendorUpdateValidationResponse(w, itemUpdateJSONError(body, err, true))
			return
		}
	}
	current, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	var data map[string]any
	var errs []validationError
	if len(body) > 0 && !isJSON {
		errs = []validationError{{"model_attributes_type", []any{"body"}, "Input should be a valid dictionary or object to extract fields from", "[REDACTED]", nil}}
	} else {
		data, errs = parseVendorUpdate(body)
	}
	raw := chi.URLParam(r, "vendor_id")
	id, valid := parsePathUUID(raw)
	if !valid {
		d := uuidErrorDetail(raw)
		errs = append([]validationError{{"uuid_parsing", []any{"path", "vendor_id"}, "Input should be a valid UUID, " + d, raw, map[string]string{"error": d}}}, errs...)
	}
	if len(errs) > 0 {
		vendorUpdateValidationResponse(w, errs)
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
	_, err = q.LockVendor(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		authError(w, 404, "vendor not found")
		return
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	b, err := json.Marshal(data)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	v, err := q.UpdateVendor(ctx, dbquery.UpdateVendorParams{Data: b, VendorID: id})
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	out, err := vendorJSON(v)
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
func registerVendorUpdate(api huma.API, s sessionHandler) {
	response := func(desc string, body any) *huma.Response {
		return &huma.Response{Description: desc, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(body), true, "")}}}
	}
	responses := map[string]*huma.Response{"401": response("Not authenticated", authErrorBody{}), "403": response("Admin required", authErrorBody{}), "404": response("Vendor not found", authErrorBody{}), "413": response("Request body too large", authErrorBody{}), "422": response("Validation error", vendorUpdateValidationErrorBody{}), "500": response("Internal server error", authErrorBody{})}
	huma.Register(api, huma.Operation{Method: "PATCH", Path: "/api/v1/vendors/{vendor_id}", OperationID: "update-vendor", SkipValidateBody: true, Security: []map[string][]string{{"sessionCookie": {}}}, Middlewares: huma.Middlewares{sessionMiddleware(s.vendorUpdate)}, Responses: responses}, func(context.Context, *struct {
		ID   string `path:"vendor_id" format:"uuid"`
		Body vendorUpdateSchema
	}) (*struct{ Body vendorOutputSchema }, error) {
		panic("middleware")
	})
	schema := api.OpenAPI().Components.Schemas.Map()["VendorUpdateSchema"]
	schema.AdditionalProperties = true
	for _, name := range []string{"name", "contact_email", "contact_name", "default_currency", "payment_terms_days", "tax_id", "notes", "active"} {
		base := schema.Properties[name]
		schema.Properties[name] = &huma.Schema{AnyOf: []*huma.Schema{base, {Type: "null"}}, Default: json.RawMessage("null")}
	}
}
