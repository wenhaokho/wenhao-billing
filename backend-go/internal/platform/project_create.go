package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

type projectCreateSchema struct {
	CustomerID    string  `json:"customer_id" format:"uuid"`
	Code          string  `json:"code" minLength:"1" maxLength:"40"`
	Name          string  `json:"name" minLength:"1" maxLength:"255"`
	Currency      string  `json:"currency" minLength:"3" maxLength:"3"`
	ContractValue any     `json:"contract_value,omitempty"`
	Status        string  `json:"status,omitempty" enum:"ACTIVE,ON_HOLD,COMPLETED,CANCELLED" default:"ACTIVE"`
	StartDate     *string `json:"start_date,omitempty" format:"date"`
	EndDate       *string `json:"end_date,omitempty" format:"date"`
	Notes         *string `json:"notes,omitempty"`
}

func parseProjectCreate(body []byte) (dbquery.CreateProjectParams, []validationError) {
	p := dbquery.CreateProjectParams{Status: "ACTIVE"}
	if len(body) == 0 {
		return p, []validationError{{"missing", []any{"body"}, "Field required", nil, nil}}
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	var value any
	if e := d.Decode(&value); e != nil {
		return p, itemUpdateJSONError(body, e, false)
	}
	var extra any
	if e := d.Decode(&extra); e != io.EOF {
		return p, itemUpdateJSONError(body, e, true)
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return p, []validationError{{"model_attributes_type", []any{"body"}, "Input should be a valid dictionary or object to extract fields from", value, nil}}
	}
	var errs []validationError
	for _, f := range []struct {
		name               string
		required, nullable bool
		min, max           int
	}{{"customer_id", true, false, 0, 0}, {"code", true, false, 1, 40}, {"name", true, false, 1, 255}, {"currency", true, false, 3, 3}, {"contract_value", false, true, 0, 0}, {"status", false, false, 0, 0}, {"start_date", false, true, 0, 0}, {"end_date", false, true, 0, 0}, {"notes", false, true, 0, 0}} {
		v, exists := obj[f.name]
		loc := []any{"body", f.name}
		if !exists {
			if f.required {
				errs = append(errs, validationError{"missing", loc, "Field required", obj, nil})
			}
			continue
		}
		if v == nil && f.nullable {
			continue
		}
		switch f.name {
		case "customer_id":
			x, ok := v.(string)
			if !ok {
				errs = append(errs, validationError{"uuid_type", loc, "UUID input should be a string, bytes or UUID object", v, nil})
				continue
			}
			id, ok := parsePathUUID(x)
			if !ok {
				detail := uuidErrorDetail(x)
				errs = append(errs, validationError{"uuid_parsing", loc, "Input should be a valid UUID, " + detail, v, map[string]string{"error": detail}})
				continue
			}
			p.CustomerID = id
		case "code", "name", "currency", "status", "notes":
			if f.name == "status" {
				if x, ok := v.(string); !ok || (x != "ACTIVE" && x != "ON_HOLD" && x != "COMPLETED" && x != "CANCELLED") {
					errs = append(errs, validationError{"literal_error", loc, "Input should be 'ACTIVE', 'ON_HOLD', 'COMPLETED' or 'CANCELLED'", v, map[string]string{"expected": "'ACTIVE', 'ON_HOLD', 'COMPLETED' or 'CANCELLED'"}})
					continue
				}
			}
			x, ok := v.(string)
			if !ok {
				errs = append(errs, validationError{"string_type", loc, "Input should be a valid string", v, nil})
				continue
			}
			n := utf8.RuneCountInString(x)
			if n < f.min {
				errs = append(errs, validationError{"string_too_short", loc, fmt.Sprintf("String should have at least %d character%s", f.min, map[bool]string{true: "s"}[f.min != 1]), v, map[string]int{"min_length": f.min}})
				continue
			}
			if f.max > 0 && n > f.max {
				errs = append(errs, validationError{"string_too_long", loc, fmt.Sprintf("String should have at most %d characters", f.max), v, map[string]int{"max_length": f.max}})
				continue
			}
			switch f.name {
			case "code":
				p.Code = cases.Upper(language.Und).String(strings.TrimSpace(x))
			case "name":
				p.Name = strings.TrimSpace(x)
			case "currency":
				p.Currency = cases.Upper(language.Und).String(x)
			case "notes":
				p.Notes = pgtype.Text{String: x, Valid: true}
			case "status":
				if x != "ACTIVE" && x != "ON_HOLD" && x != "COMPLETED" && x != "CANCELLED" {
					errs = append(errs, validationError{"literal_error", loc, "Input should be 'ACTIVE', 'ON_HOLD', 'COMPLETED' or 'CANCELLED'", v, map[string]string{"expected": "'ACTIVE', 'ON_HOLD', 'COMPLETED' or 'CANCELLED'"}})
				} else {
					p.Status = x
				}
			}
		case "contract_value":
			if x, ok := v.(string); ok {
				stripped := strings.TrimSpace(x)
				lower := strings.ToLower(stripped)
				switch lower {
				case "nan", "+nan", "-nan", "inf", "+inf", "-inf", "infinity", "+infinity", "-infinity":
					errs = append(errs, validationError{"finite_number", loc, "Input should be a finite number", v, nil})
					continue
				}
				v = strings.ReplaceAll(stripped, "_", "")
			}
			raw, _ := json.Marshal(v)
			n, ok := itemMoney(raw)
			if !ok {
				errs = append(errs, itemMoneyError(f.name, v))
				continue
			}
			p.ContractValue = n
		case "start_date", "end_date":
			raw := ""
			switch x := v.(type) {
			case string:
				raw = x
			case json.Number:
				raw = string(x)
			default:
				errs = append(errs, validationError{"date_type", loc, "Input should be a valid date", v, nil})
				continue
			}
			date, problem := fxDate(raw)
			if problem != nil {
				problem.Loc = loc
				problem.Input = v
				errs = append(errs, *problem)
				continue
			}
			if f.name == "start_date" {
				p.StartDate = pgtype.Date{Time: date, Valid: true}
			} else {
				p.EndDate = pgtype.Date{Time: date, Valid: true}
			}
		}
	}
	return p, errs
}
func (s sessionHandler) projectCreate(w http.ResponseWriter, r *http.Request) {
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
	if len(body) > 0 && !isJSON {
		vendorUpdateValidationResponse(w, []validationError{{"model_attributes_type", []any{"body"}, "Input should be a valid dictionary or object to extract fields from", nil, nil}})
		return
	}
	p, errs := parseProjectCreate(body)
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
	row, e := q.CreateProject(ctx, p)
	if e != nil {
		var pe *pgconn.PgError
		if errors.As(e, &pe) && strings.HasPrefix(pe.Code, "23") {
			authError(w, 400, fmt.Sprintf("project code %s already exists", pythonRepr(p.Code)))
		} else {
			authError(w, 500, "internal server error")
		}
		return
	}
	out, e := projectJSON(row)
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
	w.WriteHeader(201)
	w.Write(append(encoded, '\n'))
}
func registerProjectCreate(api huma.API, s sessionHandler) {
	response := func(desc string, body any) *huma.Response {
		return &huma.Response{Description: desc, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(body), true, "")}}}
	}
	responses := map[string]*huma.Response{"400": response("Project conflict", authErrorBody{}), "401": response("Not authenticated", authErrorBody{}), "403": response("Admin required", authErrorBody{}), "413": response("Body too large", authErrorBody{}), "422": response("Validation error", vendorUpdateValidationErrorBody{}), "500": response("Internal server error", authErrorBody{})}
	huma.Register(api, huma.Operation{Method: "POST", Path: "/api/v1/projects", OperationID: "create-project", DefaultStatus: 201, SkipValidateBody: true, Security: []map[string][]string{{"sessionCookie": {}}}, Middlewares: huma.Middlewares{sessionMiddleware(s.projectCreate)}, Responses: responses}, func(context.Context, *struct{ Body projectCreateSchema }) (*struct{ Body projectOutputSchema }, error) {
		panic("middleware")
	})
	schema := api.OpenAPI().Components.Schemas.Map()["ProjectCreateSchema"]
	schema.AdditionalProperties = true
	zero := float64(0)
	schema.Properties["contract_value"].AnyOf = []*huma.Schema{{Type: "number", Minimum: &zero}, {Type: "string"}, {Type: "null"}}
	for _, name := range []string{"start_date", "end_date", "notes"} {
		prop := schema.Properties[name]
		base := &huma.Schema{Type: "string"}
		if name != "notes" {
			base.Format = "date"
		}
		*prop = huma.Schema{AnyOf: []*huma.Schema{base, {Type: "null"}}, Default: json.RawMessage("null")}
	}
}

func pythonRepr(s string) string {
	q := strconv.Quote(s)
	inner := q[1 : len(q)-1]
	if strings.Contains(s, "'") && !strings.Contains(s, "\"") {
		return q
	}
	inner = strings.ReplaceAll(inner, "\\\"", "\"")
	inner = strings.ReplaceAll(inner, "'", "\\'")
	return "'" + inner + "'"
}
