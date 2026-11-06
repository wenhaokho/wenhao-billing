package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
)

type businessProfileUpdateSchema struct {
	Name           *string `json:"name,omitempty"`
	Address        *string `json:"address,omitempty"`
	ContactEmail   *string `json:"contact_email,omitempty"`
	ContactPhone   *string `json:"contact_phone,omitempty"`
	InvoiceTitle   *string `json:"invoice_title,omitempty"`
	InvoiceSummary *string `json:"invoice_summary,omitempty"`
	LogoURL        *string `json:"logo_url,omitempty"`
	DefaultNotes   *string `json:"default_notes,omitempty"`
	NPWP           *string `json:"npwp,omitempty"`
	NPWPName       *string `json:"npwp_name,omitempty"`
}

var profileFields = []string{"name", "address", "contact_email", "contact_phone", "invoice_title", "invoice_summary", "logo_url", "default_notes", "npwp", "npwp_name"}

func registerProfileUpdate(api huma.API, s sessionHandler) {
	response := func(desc string, body any) *huma.Response {
		return &huma.Response{Description: desc, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(body), true, "")}}}
	}
	errs := map[string]*huma.Response{"401": response("Not authenticated", authErrorBody{}), "403": response("Admin required", authErrorBody{}), "413": response("Request body too large", authErrorBody{}), "422": response("Validation error", validationErrorBody{}), "500": response("Internal server error", authErrorBody{})}
	huma.Register(api, huma.Operation{Method: "PUT", Path: "/api/v1/business-profile", OperationID: "update-business-profile", SkipValidateBody: true, Security: []map[string][]string{{"sessionCookie": {}}}, Middlewares: huma.Middlewares{sessionMiddleware(s.profileUpdate)}, Responses: errs}, func(context.Context, *struct{ Body businessProfileUpdateSchema }) (*struct{ Body businessProfileSchema }, error) {
		panic("middleware")
	})
	schema := api.OpenAPI().Components.Schemas.Map()["BusinessProfileUpdateSchema"]
	schema.AdditionalProperties = true
	for _, key := range profileFields {
		base := *schema.Properties[key]
		*schema.Properties[key] = huma.Schema{AnyOf: []*huma.Schema{&base, {Type: "null"}}, Default: json.RawMessage("null")}
	}
}

func (s sessionHandler) profileUpdate(w http.ResponseWriter, r *http.Request) {
	body, ok := readBodyWithSyntax(w, r)
	if !ok {
		return
	}
	current, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	if len(body) == 0 {
		validationResponse(w, []validationError{{"missing", []any{"body"}, "Field required", nil, nil}})
		return
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var raw any
	_ = dec.Decode(&raw)
	obj, ok := raw.(map[string]any)
	if !ok {
		validationResponse(w, []validationError{{"model_attributes_type", []any{"body"}, "Input should be a valid dictionary or object to extract fields from", raw, nil}})
		return
	}
	data := map[string]any{}
	var errs []validationError
	for _, f := range profileFields {
		v, exists := obj[f]
		if !exists {
			continue
		}
		if v != nil {
			if _, ok := v.(string); !ok {
				errs = append(errs, validationError{"string_type", []any{"body", f}, "Input should be a valid string", v, nil})
				continue
			}
		}
		data[f] = v
	}
	if len(errs) > 0 {
		validationResponse(w, errs)
		return
	}
	encoded, _ := json.Marshal(data)
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
	if err = q.EnsureBusinessProfile(ctx); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	p, err := q.ApplyBusinessProfileUpdate(ctx, encoded)
	if err != nil || !p.UpdatedAt.Valid {
		authError(w, 500, "internal server error")
		return
	}
	out := businessProfileOutput{p.Name, p.Address, p.ContactEmail, p.ContactPhone, p.InvoiceTitle, p.InvoiceSummary, p.LogoUrl, p.DefaultNotes, p.Npwp, p.NpwpName, pythonTimestamp(p.UpdatedAt)}
	response, err := json.Marshal(out)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if err = tx.Commit(ctx); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(append(response, '\n'))
}
