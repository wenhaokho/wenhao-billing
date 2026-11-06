package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
)

type projectUpdateSchema struct {
	CustomerID    any `json:"customer_id,omitempty"`
	Code          any `json:"code,omitempty"`
	Name          any `json:"name,omitempty"`
	Currency      any `json:"currency,omitempty"`
	ContractValue any `json:"contract_value,omitempty"`
	Status        any `json:"status,omitempty"`
	StartDate     any `json:"start_date,omitempty"`
	EndDate       any `json:"end_date,omitempty"`
	Notes         any `json:"notes,omitempty"`
	Active        any `json:"active,omitempty"`
}

func parseProjectUpdate(body []byte) (map[string]any, dbquery.CreateProjectParams, []validationError) {
	var empty dbquery.CreateProjectParams
	if len(body) == 0 {
		return nil, empty, []validationError{{"missing", []any{"body"}, "Field required", nil, nil}}
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	var v any
	if e := d.Decode(&v); e != nil {
		return nil, empty, itemUpdateJSONError(body, e, false)
	}
	var extra any
	if e := d.Decode(&extra); e != io.EOF {
		return nil, empty, itemUpdateJSONError(body, e, true)
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, empty, []validationError{{"model_attributes_type", []any{"body"}, "Input should be a valid dictionary or object to extract fields from", v, nil}}
	}
	synthesized := map[string]any{"customer_id": "00000000-0000-0000-0000-000000000000", "code": "x", "name": "x", "currency": "USD"}
	for _, k := range []string{"customer_id", "code", "name", "currency", "contract_value", "status", "start_date", "end_date", "notes"} {
		if value, exists := obj[k]; exists && value != nil {
			synthesized[k] = value
		}
	}
	raw, _ := json.Marshal(synthesized)
	p, errs := parseProjectCreate(raw)
	if a, exists := obj["active"]; exists && a != nil {
		if e := updateBooleanError(a, []any{"body", "active"}); e != nil {
			errs = append(errs, *e)
		}
	}
	return obj, p, errs
}
func (s sessionHandler) projectUpdate(w http.ResponseWriter, r *http.Request) {
	body, ok := readItemBody(w, r)
	if !ok {
		return
	}
	isJSON := requestJSONMediaType(r)
	if len(body) > 0 && isJSON {
		d := json.NewDecoder(bytes.NewReader(body))
		d.UseNumber()
		var v any
		if e := d.Decode(&v); e != nil {
			vendorUpdateValidationResponse(w, itemUpdateJSONError(body, e, false))
			return
		}
		if e := d.Decode(&v); e != io.EOF {
			vendorUpdateValidationResponse(w, itemUpdateJSONError(body, e, true))
			return
		}
	}
	current, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	var data map[string]any
	var p dbquery.CreateProjectParams
	var errs []validationError
	if len(body) > 0 && !isJSON {
		errs = []validationError{{"model_attributes_type", []any{"body"}, "Input should be a valid dictionary or object to extract fields from", nil, nil}}
	} else {
		data, p, errs = parseProjectUpdate(body)
	}
	raw := chi.URLParam(r, "project_id")
	id, valid := parsePathUUID(raw)
	if !valid {
		detail := uuidErrorDetail(raw)
		errs = append([]validationError{{"uuid_parsing", []any{"path", "project_id"}, "Input should be a valid UUID, " + detail, raw, map[string]string{"error": detail}}}, errs...)
	}
	if len(errs) > 0 {
		vendorUpdateValidationResponse(w, errs)
		return
	}
	ctx := r.Context()
	tx, e := s.db.Begin(ctx)
	if e != nil {
		authError(w, 500, "internal server error")
		return
	}
	defer tx.Rollback(ctx)
	q := dbquery.New(tx)
	user, e := q.LockSessionUserByID(ctx, current.UserID)
	if errors.Is(e, pgx.ErrNoRows) || e == nil && user.Role != "admin" {
		authError(w, 403, "admin required")
		return
	}
	if e != nil {
		authError(w, 500, "internal server error")
		return
	}
	row, e := q.LockProject(ctx, id)
	if errors.Is(e, pgx.ErrNoRows) {
		authError(w, 404, "project not found")
		return
	}
	if e != nil {
		authError(w, 500, "internal server error")
		return
	}
	for _, k := range []string{"customer_id", "code", "name", "currency", "status", "active"} {
		if v, exists := data[k]; exists && v == nil {
			authError(w, 400, "project code already exists")
			return
		}
	}
	u := dbquery.UpdateProjectParams{ProjectID: id, CustomerID: row.CustomerID, Code: row.Code, Name: row.Name, Currency: row.Currency, ContractValue: row.ContractValue, Status: row.Status, StartDate: row.StartDate, EndDate: row.EndDate, Notes: row.Notes, Active: row.Active}
	if _, ok := data["customer_id"]; ok {
		u.CustomerID = p.CustomerID
	}
	if _, ok := data["code"]; ok {
		u.Code = p.Code
	}
	if v, ok := data["name"]; ok {
		u.Name = v.(string)
	}
	if _, ok := data["currency"]; ok {
		u.Currency = p.Currency
	}
	if _, ok := data["status"]; ok {
		u.Status = p.Status
	}
	if _, ok := data["contract_value"]; ok {
		u.ContractValue = p.ContractValue
	}
	if _, ok := data["start_date"]; ok {
		u.StartDate = p.StartDate
	}
	if _, ok := data["end_date"]; ok {
		u.EndDate = p.EndDate
	}
	if _, ok := data["notes"]; ok {
		u.Notes = p.Notes
	}
	if a, ok := data["active"]; ok {
		switch v := a.(type) {
		case bool:
			u.Active = v
		case string:
			switch strings.ToLower(v) {
			case "true", "1", "on", "yes", "y", "t":
				u.Active = true
			default:
				u.Active = false
			}
		case json.Number:
			n, _ := boundedItemRat(string(v))
			u.Active = n != nil && n.Sign() != 0
		}
	}
	updated, e := q.UpdateProject(ctx, u)
	if e != nil {
		var pe *pgconn.PgError
		if errors.As(e, &pe) && strings.HasPrefix(pe.Code, "23") {
			authError(w, 400, "project code already exists")
		} else {
			authError(w, 500, "internal server error")
		}
		return
	}
	out, e := projectJSON(updated)
	if e != nil {
		authError(w, 500, "internal server error")
		return
	}
	encoded, e := json.Marshal(out)
	if e != nil {
		authError(w, 500, "internal server error")
		return
	}
	if e = tx.Commit(ctx); e != nil {
		authError(w, 500, "internal server error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	w.Write(append(encoded, '\n'))
}
func registerProjectUpdate(api huma.API, s sessionHandler) {
	response := func(desc string, body any) *huma.Response {
		return &huma.Response{Description: desc, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(body), true, "")}}}
	}
	responses := map[string]*huma.Response{"400": response("Project conflict", authErrorBody{}), "401": response("Not authenticated", authErrorBody{}), "403": response("Admin required", authErrorBody{}), "404": response("Project not found", authErrorBody{}), "413": response("Body too large", authErrorBody{}), "422": response("Validation error", vendorUpdateValidationErrorBody{}), "500": response("Internal server error", authErrorBody{})}
	huma.Register(api, huma.Operation{Method: "PATCH", Path: "/api/v1/projects/{project_id}", OperationID: "update-project", SkipValidateBody: true, Security: []map[string][]string{{"sessionCookie": {}}}, Middlewares: huma.Middlewares{sessionMiddleware(s.projectUpdate)}, Responses: responses}, func(context.Context, *struct {
		ID   string `path:"project_id" format:"uuid"`
		Body projectUpdateSchema
	}) (*struct{ Body projectOutputSchema }, error) {
		panic("middleware")
	})
	schema := api.OpenAPI().Components.Schemas.Map()["ProjectUpdateSchema"]
	schema.AdditionalProperties = true
	for _, name := range []string{"customer_id", "code", "name", "currency", "contract_value", "status", "start_date", "end_date", "notes", "active"} {
		prop := schema.Properties[name]
		var base *huma.Schema
		switch name {
		case "active":
			base = &huma.Schema{Type: "boolean"}
		case "contract_value":
			zero := float64(0)
			*prop = huma.Schema{AnyOf: []*huma.Schema{{Type: "number", Minimum: &zero}, {Type: "string"}, {Type: "null"}}}
			continue
		default:
			base = &huma.Schema{Type: "string"}
			if name == "customer_id" {
				base.Format = "uuid"
			}
			if name == "start_date" || name == "end_date" {
				base.Format = "date"
			}
			if name == "code" || name == "name" || name == "currency" {
				min, max := 1, 40
				if name == "name" {
					max = 255
				}
				if name == "currency" {
					min, max = 3, 3
				}
				base.MinLength, base.MaxLength = &min, &max
			}
			if name == "status" {
				base.Enum = []any{"ACTIVE", "ON_HOLD", "COMPLETED", "CANCELLED"}
			}
		}
		*prop = huma.Schema{AnyOf: []*huma.Schema{base, {Type: "null"}}}
	}
}
