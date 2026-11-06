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
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
)

type accountCreateSchema struct {
	Code   string `json:"code" minLength:"1" maxLength:"10"`
	Name   string `json:"name" minLength:"1" maxLength:"100"`
	Type   string `json:"type" enum:"ASSET,LIABILITY,EQUITY,INCOME,EXPENSE,COGS"`
	Active bool   `json:"active,omitempty" default:"true"`
}
type accountUpdateSchema struct {
	Code   *string `json:"code,omitempty" minLength:"1" maxLength:"10"`
	Name   *string `json:"name,omitempty" minLength:"1" maxLength:"100"`
	Type   *string `json:"type,omitempty" enum:"ASSET,LIABILITY,EQUITY,INCOME,EXPENSE,COGS"`
	Active *bool   `json:"active,omitempty"`
}

func registerAccountWrites(api huma.API, s sessionHandler) {
	sec := []map[string][]string{{"sessionCookie": {}}}
	m := huma.Middlewares{sessionMiddleware(s.accountWrite)}
	response := func(desc string, body any) *huma.Response {
		return &huma.Response{Description: desc, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(body), true, "")}}}
	}
	responses := func(plain bool) map[string]*huma.Response {
		result := map[string]*huma.Response{"401": response("Not authenticated", authErrorBody{}), "403": response("Admin required", authErrorBody{}), "404": response("Account not found", authErrorBody{}), "409": response("Account conflict", authErrorBody{}), "422": response("Validation error", validationErrorBody{}), "500": response("Internal server error", authErrorBody{})}
		if plain {
			result["500"].Content["text/plain"] = &huma.MediaType{Schema: &huma.Schema{Type: "string"}}
		}
		return result
	}
	huma.Register(api, huma.Operation{Method: "POST", Path: "/api/v1/accounting/chart-of-accounts", OperationID: "create-account", DefaultStatus: 201, SkipValidateBody: true, Security: sec, Middlewares: m, Responses: responses(false)}, func(context.Context, *struct{ Body accountCreateSchema }) (*struct{ Body accountOutput }, error) {
		panic("middleware")
	})
	huma.Register(api, huma.Operation{Method: "PATCH", Path: "/api/v1/accounting/chart-of-accounts/{account_id}", OperationID: "update-account", SkipValidateBody: true, Security: sec, Middlewares: m, Responses: responses(true)}, func(context.Context, *struct {
		ID   int `path:"account_id"`
		Body accountUpdateSchema
	}) (*struct{ Body accountOutput }, error) {
		panic("middleware")
	})
	huma.Register(api, huma.Operation{Method: "DELETE", Path: "/api/v1/accounting/chart-of-accounts/{account_id}", OperationID: "deactivate-account", Security: sec, Middlewares: m, Responses: responses(true)}, func(context.Context, *accountGetInput) (*struct{ Body accountOutput }, error) { panic("middleware") })
	schema := api.OpenAPI().Components.Schemas.Map()["AccountUpdateSchema"]
	for _, key := range []string{"code", "name", "type", "active"} {
		schema.Properties[key].Nullable = true
	}
	schema.AdditionalProperties = true
	api.OpenAPI().Components.Schemas.Map()["AccountCreateSchema"].AdditionalProperties = true
	schema.Properties["type"].Enum = append(schema.Properties["type"].Enum, nil)
}
func parseAccountBody(body []byte, create bool) (map[string]any, []validationError) {
	if len(body) == 0 {
		return nil, []validationError{{"missing", []any{"body"}, "Field required", nil, nil}}
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	var raw any
	if e := d.Decode(&raw); e != nil {
		return nil, itemUpdateJSONError(body, e, false)
	}
	var extra any
	if e := d.Decode(&extra); e != io.EOF {
		return nil, itemUpdateJSONError(body, e, true)
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		return nil, []validationError{{"model_attributes_type", []any{"body"}, "Input should be a valid dictionary or object to extract fields from", raw, nil}}
	}
	var errs []validationError
	for _, f := range []struct {
		name string
		max  int
	}{{"code", 10}, {"name", 100}, {"type", 0}, {"active", 0}} {
		v, exists := obj[f.name]
		loc := []any{"body", f.name}
		if !exists {
			if create && f.name != "active" {
				errs = append(errs, validationError{"missing", loc, "Field required", obj, nil})
			}
			continue
		}
		if v == nil && !create {
			continue
		}
		if f.name == "active" {
			if e := updateBooleanError(v, loc); e != nil {
				errs = append(errs, *e)
			} else {
				switch x := v.(type) {
				case bool:
					obj[f.name] = x
				case string:
					obj[f.name] = strings.Contains(" true 1 on yes y t ", " "+strings.ToLower(x)+" ")
				case json.Number:
					r, _ := boundedItemRat(string(x))
					obj[f.name] = r.Sign() != 0
				}
			}
			continue
		}
		if f.name == "type" {
			s, ok := v.(string)
			if !ok || !strings.Contains("|ASSET|LIABILITY|EQUITY|INCOME|EXPENSE|COGS|", "|"+s+"|") {
				errs = append(errs, validationError{"literal_error", loc, "Input should be 'ASSET', 'LIABILITY', 'EQUITY', 'INCOME', 'EXPENSE' or 'COGS'", v, map[string]string{"expected": "'ASSET', 'LIABILITY', 'EQUITY', 'INCOME', 'EXPENSE' or 'COGS'"}})
			}
			continue
		}
		s, ok := v.(string)
		if !ok {
			errs = append(errs, validationError{"string_type", loc, "Input should be a valid string", v, nil})
			continue
		}
		n := utf8.RuneCountInString(s)
		if n < 1 {
			errs = append(errs, validationError{"string_too_short", loc, "String should have at least 1 character", v, map[string]int{"min_length": 1}})
		} else if n > f.max {
			errs = append(errs, validationError{"string_too_long", loc, fmt.Sprintf("String should have at most %d characters", f.max), v, map[string]int{"max_length": f.max}})
		}
	}
	return obj, errs
}
func (s sessionHandler) accountWrite(w http.ResponseWriter, r *http.Request) {
	var rawBody []byte
	if r.Method != "DELETE" {
		var ok bool
		rawBody, ok = readBodyWithSyntax(w, r)
		if !ok {
			return
		}
	}
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	ctx := r.Context()
	var id int64
	if r.Method != "POST" {
		raw := chi.URLParam(r, "account_id")
		var e error
		text := strings.TrimSpace(raw)
		if !regexp.MustCompile(`^[+-]?[0-9](?:_?[0-9])*(?:\.0+)?$`).MatchString(text) {
			errs := []validationError{{"int_parsing", []any{"path", "account_id"}, "Input should be a valid integer, unable to parse string as an integer", raw, nil}}
			if r.Method != "DELETE" {
				_, bodyErrs := parseAccountBody(rawBody, false)
				errs = append(errs, bodyErrs...)
			}
			validationResponse(w, errs)
			return
		}
		text = strings.ReplaceAll(text, "_", "")
		if i := strings.IndexByte(text, '.'); i >= 0 {
			text = text[:i]
		}
		id, e = strconv.ParseInt(text, 10, 64)
		if e != nil {
			errs := []validationError{{"int_parsing", []any{"path", "account_id"}, "Input should be a valid integer, unable to parse string as an integer", raw, nil}}
			if r.Method != "DELETE" {
				_, bodyErrs := parseAccountBody(rawBody, false)
				errs = append(errs, bodyErrs...)
			}
			validationResponse(w, errs)
			return
		}
		if id < -2147483648 || id > 2147483647 {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(500)
			w.Write([]byte("Internal Server Error"))
			return
		}
	}
	var body map[string]any
	if r.Method != "DELETE" {
		var errs []validationError
		body, errs = parseAccountBody(rawBody, r.Method == "POST")
		if len(errs) > 0 {
			validationResponse(w, errs)
			return
		}
	}
	tx, e := s.db.Begin(ctx)
	if e != nil {
		authError(w, 500, "internal server error")
		return
	}
	defer tx.Rollback(ctx)
	q := dbquery.New(tx)
	var a dbquery.ChartOfAccount
	if r.Method == "POST" {
		active := true
		if v, ok := body["active"]; ok {
			active = v.(bool)
		}
		a, e = q.CreateAccount(ctx, dbquery.CreateAccountParams{Code: body["code"].(string), Name: body["name"].(string), Type: body["type"].(string), Active: active})
	} else {
		a, e = q.LockAccount(ctx, int32(id))
		if errors.Is(e, pgx.ErrNoRows) {
			authError(w, 404, "account not found")
			return
		}
		if e == nil {
			if r.Method == "DELETE" {
				a.Active = false
			} else {
				for _, k := range []string{"code", "name", "type", "active"} {
					if v, ok := body[k]; ok {
						if v == nil {
							authError(w, 409, "account code already exists")
							return
						}
						switch k {
						case "code":
							a.Code = v.(string)
						case "name":
							a.Name = v.(string)
						case "type":
							a.Type = v.(string)
						case "active":
							a.Active = v.(bool)
						}
					}
				}
			}
			a, e = q.UpdateAccount(ctx, dbquery.UpdateAccountParams{AccountID: a.AccountID, Code: a.Code, Name: a.Name, Type: a.Type, Active: a.Active})
		}
	}
	if e != nil {
		var pe *pgconn.PgError
		if errors.As(e, &pe) && strings.HasPrefix(pe.Code, "23") {
			authError(w, 409, "account code already exists")
		} else {
			authError(w, 500, "internal server error")
		}
		return
	}
	if e = tx.Commit(ctx); e != nil {
		authError(w, 500, "internal server error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if r.Method == "POST" {
		w.WriteHeader(201)
	}
	json.NewEncoder(w).Encode(accountJSON(a))
}
