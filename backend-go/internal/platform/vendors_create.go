package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
)

type vendorCreateSchema struct {
	Name             string  `json:"name" minLength:"1" maxLength:"255"`
	ContactEmail     *string `json:"contact_email,omitempty" format:"email"`
	ContactName      *string `json:"contact_name,omitempty"`
	DefaultCurrency  string  `json:"default_currency,omitempty" minLength:"3" maxLength:"3" default:"IDR"`
	PaymentTermsDays int32   `json:"payment_terms_days,omitempty" minimum:"0" maximum:"365" default:"30"`
	TaxID            *string `json:"tax_id,omitempty"`
	Notes            *string `json:"notes,omitempty"`
	Active           bool    `json:"active,omitempty" default:"true"`
}

func parseVendorCreate(body []byte) (dbquery.CreateVendorParams, []validationError) {
	p := dbquery.CreateVendorParams{DefaultCurrency: "IDR", PaymentTermsDays: 30, Active: true}
	if len(body) == 0 {
		return p, []validationError{{"missing", []any{"body"}, "Field required", nil, nil}}
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	var value any
	if err := d.Decode(&value); err != nil {
		return p, itemUpdateJSONError(body, err, false)
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return p, itemUpdateJSONError(body, err, true)
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return p, []validationError{{"model_attributes_type", []any{"body"}, "Input should be a valid dictionary or object to extract fields from", value, nil}}
	}
	var errs []validationError
	for _, f := range []struct {
		name     string
		min, max int
		nullable bool
	}{{"name", 1, 255, false}, {"contact_email", 0, 0, true}, {"contact_name", 0, 0, true}, {"default_currency", 3, 3, false}, {"tax_id", 0, 0, true}, {"notes", 0, 0, true}} {
		v, exists := obj[f.name]
		loc := []any{"body", f.name}
		if !exists {
			if f.name == "name" {
				errs = append(errs, validationError{"missing", loc, "Field required", obj, nil})
			}
			continue
		}
		if v == nil && f.nullable {
			continue
		}
		s, ok := v.(string)
		if !ok {
			errs = append(errs, validationError{"string_type", loc, "Input should be a valid string", v, nil})
			continue
		}
		n := utf8.RuneCountInString(s)
		if n < f.min {
			errs = append(errs, validationError{"string_too_short", loc, fmt.Sprintf("String should have at least %d character%s", f.min, map[bool]string{true: "s"}[f.min != 1]), v, map[string]int{"min_length": f.min}})
			continue
		}
		if f.max > 0 && n > f.max {
			errs = append(errs, validationError{"string_too_long", loc, fmt.Sprintf("String should have at most %d characters", f.max), v, map[string]int{"max_length": f.max}})
			continue
		}
		if f.name == "contact_email" {
			normalized, reason := normalizeEmail(s)
			if reason != "" {
				errs = append(errs, validationError{"value_error", loc, "value is not a valid email address: " + reason, v, map[string]string{"reason": reason}})
				continue
			}
			s = normalized
		}
		switch f.name {
		case "name":
			p.Name = s
		case "contact_email":
			p.ContactEmail = textValue(&s)
		case "contact_name":
			p.ContactName = textValue(&s)
		case "default_currency":
			p.DefaultCurrency = s
		case "tax_id":
			p.TaxID = textValue(&s)
		case "notes":
			p.Notes = textValue(&s)
		}
	}
	if v, exists := obj["payment_terms_days"]; exists {
		loc := []any{"body", "payment_terms_days"}
		s := fmt.Sprint(v)
		if b, ok := v.(bool); ok {
			if b {
				s = "1"
			} else {
				s = "0"
			}
		}
		s = strings.TrimSpace(s)
		rat, ok := boundedItemRat(s)
		if _, isString := v.(string); isString && !itemIntegerString.MatchString(s) {
			ok = false
		}
		if !ok || !rat.IsInt() {
			typ, msg := "int_type", "Input should be a valid integer"
			if _, yes := v.(string); yes {
				typ, msg = "int_parsing", "Input should be a valid integer, unable to parse string as an integer"
			} else if _, yes := v.(json.Number); yes && rat != nil && !rat.IsInt() {
				typ, msg = "int_from_float", "Input should be a valid integer, got a number with a fractional part"
			}
			errs = append(errs, validationError{typ, loc, msg, v, nil})
		} else if rat.Sign() < 0 {
			errs = append(errs, validationError{"greater_than_equal", loc, "Input should be greater than or equal to 0", v, map[string]int{"ge": 0}})
		} else if rat.Cmp(big.NewRat(365, 1)) > 0 {
			errs = append(errs, validationError{"less_than_equal", loc, "Input should be less than or equal to 365", v, map[string]int{"le": 365}})
		} else {
			p.PaymentTermsDays = int32(rat.Num().Int64())
		}
	}
	if v, exists := obj["active"]; exists {
		loc := []any{"body", "active"}
		if e := updateBooleanError(v, loc); e != nil {
			errs = append(errs, *e)
		} else {
			switch x := v.(type) {
			case bool:
				p.Active = x
			case string:
				p.Active = strings.Contains("|true|1|on|yes|y|t|", "|"+strings.ToLower(x)+"|")
			case json.Number:
				r, _ := boundedItemRat(string(x))
				p.Active = r.Sign() != 0
			}
		}
	}
	// Match Pydantic field order.
	order := map[string]int{"name": 0, "contact_email": 1, "contact_name": 2, "default_currency": 3, "payment_terms_days": 4, "tax_id": 5, "notes": 6, "active": 7}
	for i := 1; i < len(errs); i++ {
		for j := i; j > 0 && order[errs[j].Loc[1].(string)] < order[errs[j-1].Loc[1].(string)]; j-- {
			errs[j], errs[j-1] = errs[j-1], errs[j]
		}
	}
	return p, errs
}
func (s sessionHandler) vendorCreate(w http.ResponseWriter, r *http.Request) {
	body, ok := readItemBody(w, r)
	if !ok {
		return
	}
	// FastAPI parses JSON only for application/json and application/*+json.
	isJSON := requestJSONMediaType(r)
	if len(body) > 0 && isJSON {
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
	if len(body) > 0 && !isJSON {
		// Unparsed content may contain credentials in an arbitrary encoding.
		validationResponse(w, []validationError{{"model_attributes_type", []any{"body"}, "Input should be a valid dictionary or object to extract fields from", "[REDACTED]", nil}})
		return
	}
	p, errs := parseVendorCreate(body)
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
	v, err := q.CreateVendor(ctx, p)
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
	w.WriteHeader(201)
	w.Write(append(encoded, '\n'))
}
func registerVendorCreate(api huma.API, s sessionHandler) {
	response := func(desc string, body any) *huma.Response {
		return &huma.Response{Description: desc, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(body), true, "")}}}
	}
	responses := map[string]*huma.Response{"401": response("Not authenticated", authErrorBody{}), "403": response("Admin required", authErrorBody{}), "413": response("Request body too large", authErrorBody{}), "422": response("Validation error", validationErrorBody{}), "500": response("Internal server error", authErrorBody{})}
	huma.Register(api, huma.Operation{Method: "POST", Path: "/api/v1/vendors", OperationID: "create-vendor", DefaultStatus: 201, SkipValidateBody: true, Security: []map[string][]string{{"sessionCookie": {}}}, Middlewares: huma.Middlewares{sessionMiddleware(s.vendorCreate)}, Responses: responses}, func(context.Context, *struct{ Body vendorCreateSchema }) (*struct{ Body vendorOutputSchema }, error) {
		panic("middleware")
	})
	output := api.OpenAPI().Components.Schemas.Map()["VendorOutputSchema"]
	output.Required = []string{"name", "vendor_id", "created_at"}
	output.AdditionalProperties = true
	for _, name := range []string{"contact_email", "contact_name", "tax_id", "notes"} {
		output.Properties[name].Default = json.RawMessage("null")
	}
	output.Properties["default_currency"].Default = "IDR"
	output.Properties["payment_terms_days"].Default = 30
	output.Properties["active"].Default = true
	schema := api.OpenAPI().Components.Schemas.Map()["VendorCreateSchema"]
	schema.AdditionalProperties = true
	for _, name := range []string{"contact_email", "contact_name", "tax_id", "notes"} {
		base := &huma.Schema{Type: "string"}
		if name == "contact_email" {
			base.Format = "email"
		}
		*schema.Properties[name] = huma.Schema{AnyOf: []*huma.Schema{base, {Type: "null"}}, Default: json.RawMessage("null")}
	}
}
