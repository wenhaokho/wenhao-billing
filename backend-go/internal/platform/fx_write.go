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
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

type fxCreateSchema struct {
	FromCurrency string `json:"from_currency" minLength:"3" maxLength:"3"`
	ToCurrency   string `json:"to_currency" minLength:"3" maxLength:"3"`
	Rate         any    `json:"rate"`
	AsOfDate     string `json:"as_of_date" format:"date"`
	Source       string `json:"source,omitempty" minLength:"1" maxLength:"50" default:"MANUAL"`
}
type fxDeleteInput struct {
	RateID int64 `path:"rate_id"`
}

func registerFXWrites(api huma.API, s sessionHandler) {
	response := func(desc string, body any) *huma.Response {
		return &huma.Response{Description: desc, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(body), true, "")}}}
	}
	common := func() map[string]*huma.Response {
		return map[string]*huma.Response{"401": response("Not authenticated", authErrorBody{}), "403": response("Admin required", authErrorBody{}), "422": response("Validation error", vendorUpdateValidationErrorBody{}), "500": response("Internal server error", authErrorBody{})}
	}
	post := common()
	post["409"] = response("Duplicate rate", authErrorBody{})
	huma.Register(api, huma.Operation{Method: "POST", Path: "/api/v1/accounting/fx-rates", OperationID: "create-fx-rate", DefaultStatus: 201, Security: []map[string][]string{{"sessionCookie": {}}}, Middlewares: huma.Middlewares{sessionMiddleware(s.fxCreate)}, Responses: post}, func(context.Context, *struct{ Body fxCreateSchema }) (*struct{ Body fxOutput }, error) {
		panic("middleware")
	})
	del := common()
	del["404"] = response("Rate not found", authErrorBody{})
	del["500"].Content["text/plain"] = &huma.MediaType{Schema: &huma.Schema{Type: "string"}}
	zero := float64(0)
	prop := api.OpenAPI().Components.Schemas.Map()["FxCreateSchema"].Properties["rate"]
	*prop = huma.Schema{AnyOf: []*huma.Schema{{Type: "number", ExclusiveMinimum: &zero}, {Type: "string"}}}
	huma.Register(api, huma.Operation{Method: "DELETE", Path: "/api/v1/accounting/fx-rates/{rate_id}", OperationID: "delete-fx-rate", DefaultStatus: 204, Security: []map[string][]string{{"sessionCookie": {}}}, Middlewares: huma.Middlewares{sessionMiddleware(s.fxDelete)}, Responses: del}, func(context.Context, *fxDeleteInput) (*struct{}, error) { panic("middleware") })
}
func parseFXCreate(body []byte) (dbquery.CreateFXRateParams, []validationError) {
	p := dbquery.CreateFXRateParams{Source: "MANUAL"}
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
		required bool
	}{{"from_currency", 3, 3, true}, {"to_currency", 3, 3, true}, {"rate", 0, 0, true}, {"as_of_date", 0, 0, true}, {"source", 1, 50, false}} {
		v, exists := obj[f.name]
		loc := []any{"body", f.name}
		if !exists {
			if f.required {
				errs = append(errs, validationError{"missing", loc, "Field required", obj, nil})
			}
			continue
		}
		switch f.name {
		case "from_currency", "to_currency", "source":
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
			if n > f.max {
				errs = append(errs, validationError{"string_too_long", loc, fmt.Sprintf("String should have at most %d characters", f.max), v, map[string]int{"max_length": f.max}})
				continue
			}
			switch f.name {
			case "from_currency":
				p.FromCurrency = cases.Upper(language.Und).String(x)
			case "to_currency":
				p.ToCurrency = cases.Upper(language.Und).String(x)
			case "source":
				p.Source = x
			}
		case "rate":
			raw := ""
			switch x := v.(type) {
			case string:
				raw = strings.TrimSpace(x)
			case json.Number:
				raw = string(x)
			default:
				errs = append(errs, validationError{"decimal_type", loc, "Decimal input should be an integer, float, string or Decimal object", v, nil})
				continue
			}
			if strings.EqualFold(raw, "nan") || strings.EqualFold(raw, "inf") || strings.EqualFold(raw, "infinity") || strings.EqualFold(raw, "+inf") || strings.EqualFold(raw, "-inf") || strings.EqualFold(raw, "+infinity") || strings.EqualFold(raw, "-infinity") {
				errs = append(errs, validationError{"finite_number", loc, "Input should be a finite number", v, nil})
				continue
			}
			raw = strings.ReplaceAll(raw, "_", "")
			dec, e := decimal.NewFromString(raw)
			if e != nil || len(raw) > 1000 {
				errs = append(errs, validationError{"decimal_parsing", loc, "Input should be a valid decimal", v, nil})
				continue
			}
			if dec.Sign() <= 0 {
				errs = append(errs, validationError{"greater_than", loc, "Input should be greater than 0", v, map[string]int{"gt": 0}})
				continue
			}
			normalized := dec
			if dec.Exponent() > -1000 && dec.Exponent() < 1000 {
				normalized, _ = decimal.NewFromString(dec.String())
			}
			exp := normalized.Exponent()
			digits := len(normalized.Coefficient().String())
			fraction := 0
			if exp < 0 {
				fraction = int(-int64(exp))
			}
			whole := digits - fraction
			if whole < 0 {
				whole = 0
			}
			if exp > 0 {
				whole = digits + int(exp)
			}
			total := whole + fraction
			typ, msg := "", ""
			var ctx any
			switch {
			case total > 19:
				typ = "decimal_max_digits"
				msg = "Decimal input should have no more than 19 digits in total"
				ctx = map[string]int{"max_digits": 19}
			case fraction > 8:
				typ = "decimal_max_places"
				msg = "Decimal input should have no more than 8 decimal places"
				ctx = map[string]int{"decimal_places": 8}
			case whole > 11:
				typ = "decimal_whole_digits"
				msg = "Decimal input should have no more than 11 digits before the decimal point"
				ctx = map[string]int{"whole_digits": 11}
			}
			if typ != "" {
				errs = append(errs, validationError{typ, loc, msg, v, ctx})
				continue
			}
			p.Rate = pgtype.Numeric{Int: normalized.Coefficient(), Exp: normalized.Exponent(), Valid: true}
		case "as_of_date":
			var raw string
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
			p.AsOfDate = pgtype.Date{Time: date, Valid: true}
		}
	}
	return p, errs
}
func (s sessionHandler) fxCreate(w http.ResponseWriter, r *http.Request) {
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
	if len(body) > 0 && !isJSON {
		vendorUpdateValidationResponse(w, []validationError{{"model_attributes_type", []any{"body"}, "Input should be a valid dictionary or object to extract fields from", nil, nil}})
		return
	}
	p, errs := parseFXCreate(body)
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
	if err = q.LockFXSync(ctx); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	row, err := q.CreateFXRate(ctx, p)
	if err != nil {
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "23505" {
			authError(w, 409, "fx rate for this currency pair and date already exists")
		} else {
			authError(w, 500, "internal server error")
		}
		return
	}
	out, err := fxJSON(row)
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

var fxPathInteger = regexp.MustCompile(`^[+-]?[0-9]+(?:_[0-9]+)*(?:\.0+)?$`)

func (s sessionHandler) fxDelete(w http.ResponseWriter, r *http.Request) {
	current, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	raw := chi.URLParam(r, "rate_id")
	normalized := strings.TrimSpace(raw)
	if !fxPathInteger.MatchString(normalized) {
		vendorUpdateValidationResponse(w, []validationError{{"int_parsing", []any{"path", "rate_id"}, "Input should be a valid integer, unable to parse string as an integer", raw, nil}})
		return
	}
	normalized = strings.ReplaceAll(normalized, "_", "")
	if dot := strings.IndexByte(normalized, '.'); dot >= 0 {
		normalized = normalized[:dot]
	}
	id, err := strconv.ParseInt(normalized, 10, 64)
	if err != nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Internal Server Error"))
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
	if err = q.LockFXSync(ctx); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	_, err = q.DeleteFXRate(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		authError(w, 404, "rate not found")
		return
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if err = tx.Commit(ctx); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	w.WriteHeader(204)
}
