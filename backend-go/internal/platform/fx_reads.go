package platform

import (
	"context"
	"encoding/json"
	"errors"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"math"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
)

type fxOutput struct {
	RateID       int64  `json:"rate_id"`
	FromCurrency string `json:"from_currency"`
	ToCurrency   string `json:"to_currency"`
	Rate         string `json:"rate"`
	AsOfDate     string `json:"as_of_date" format:"date"`
	Source       string `json:"source"`
	CreatedAt    string `json:"created_at" format:"date-time"`
}
type fxListInput struct {
	From string `query:"from_currency" minLength:"3" maxLength:"3" nullable:"true"`
	To   string `query:"to_currency" minLength:"3" maxLength:"3" nullable:"true"`
}
type fxLatestInput struct {
	From   string `query:"from_currency" minLength:"3" maxLength:"3" required:"true"`
	To     string `query:"to_currency" minLength:"3" maxLength:"3" nullable:"true"`
	OnDate string `query:"on_date" format:"date" nullable:"true"`
}
type baseCurrencyOutput struct {
	BaseCurrency string `json:"base_currency"`
}

func registerFXReads(api huma.API, s sessionHandler) {
	sec := []map[string][]string{{"sessionCookie": {}}}
	m := huma.Middlewares{sessionMiddleware(s.fxRead)}
	response := func(desc string, body any) *huma.Response {
		return &huma.Response{Description: desc, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(body), true, "")}}}
	}
	responses := func(latest bool) map[string]*huma.Response {
		r := map[string]*huma.Response{"401": response("Not authenticated", authErrorBody{}), "403": response("Admin required", authErrorBody{}), "422": response("Validation error", validationErrorBody{}), "500": response("Internal server error", authErrorBody{})}
		if latest {
			r["404"] = response("No rate found", authErrorBody{})
		}
		return r
	}
	huma.Register(api, huma.Operation{Method: "GET", Path: "/api/v1/accounting/base-currency", OperationID: "base-currency", Security: sec, Middlewares: m, Responses: responses(false)}, func(context.Context, *struct{}) (*struct{ Body baseCurrencyOutput }, error) { panic("middleware") })
	huma.Register(api, huma.Operation{Method: "GET", Path: "/api/v1/accounting/fx-rates", OperationID: "list-fx-rates", Security: sec, Middlewares: m, Responses: responses(false)}, func(context.Context, *fxListInput) (*struct {
		Body []fxOutput `nullable:"false"`
	}, error) {
		panic("middleware")
	})
	huma.Register(api, huma.Operation{Method: "GET", Path: "/api/v1/accounting/fx-rates/latest", OperationID: "latest-fx-rate", Security: sec, Middlewares: m, Responses: responses(true)}, func(context.Context, *fxLatestInput) (*struct{ Body fxOutput }, error) { panic("middleware") })
}

type invalidStoredFXRateError struct{}

func (invalidStoredFXRateError) Error() string { return "invalid stored rate" }

func fxJSON(r dbquery.FxRate) (fxOutput, error) {
	if !r.Rate.Valid || r.Rate.NaN || r.Rate.InfinityModifier != pgtype.Finite || r.Rate.Int == nil {
		return fxOutput{}, invalidStoredFXRateError{}
	}
	stamp := r.CreatedAt.Time.Format("2006-01-02T15:04:05")
	if r.CreatedAt.Time.Nanosecond() != 0 {
		stamp = r.CreatedAt.Time.Format("2006-01-02T15:04:05.000000")
	}
	return fxOutput{r.RateID, r.FromCurrency, r.ToCurrency, decimal.NewFromBigInt(r.Rate.Int, r.Rate.Exp).StringFixed(8), r.AsOfDate.Time.Format("2006-01-02"), r.Source, stamp}, nil
}
func (s sessionHandler) fxRead(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	base := s.config.BaseCurrency
	if base == "" {
		base = "IDR"
	}
	if strings.HasSuffix(r.URL.Path, "/base-currency") {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(baseCurrencyOutput{base})
		return
	}
	latest := strings.HasSuffix(r.URL.Path, "/latest")
	values := r.URL.Query()
	var errs []validationError
	get := func(key string, required bool) pgtype.Text {
		v, ok := values[key]
		if !ok {
			if required {
				errs = append(errs, validationError{"missing", []any{"query", key}, "Field required", nil, nil})
			}
			return pgtype.Text{}
		}
		x := v[len(v)-1]
		n := utf8.RuneCountInString(x)
		if n < 3 {
			errs = append(errs, validationError{"string_too_short", []any{"query", key}, "String should have at least 3 characters", x, map[string]any{"min_length": 3}})
		} else if n > 3 {
			errs = append(errs, validationError{"string_too_long", []any{"query", key}, "String should have at most 3 characters", x, map[string]any{"max_length": 3}})
		}
		return pgtype.Text{String: cases.Upper(language.Und).String(x), Valid: true}
	}
	from := get("from_currency", latest)
	to := get("to_currency", false)
	var on pgtype.Date
	if latest {
		if v, ok := values["on_date"]; ok {
			raw := v[len(v)-1]
			d, problem := fxDate(raw)
			if problem != nil {
				errs = append(errs, *problem)
			} else {
				on = pgtype.Date{Time: d, Valid: true}
			}

		}
	}
	if len(errs) > 0 {
		validationResponse(w, errs)
		return
	}
	q := dbquery.New(s.db)
	var rows []dbquery.FxRate
	var err error
	if latest {
		if !to.Valid {
			to = pgtype.Text{String: cases.Upper(language.Und).String(base), Valid: true}
		}
		var row dbquery.FxRate
		row, err = q.LatestFXRate(r.Context(), dbquery.LatestFXRateParams{FromCurrency: from.String, ToCurrency: to.String, OnDate: on})
		if errors.Is(err, pgx.ErrNoRows) {
			authError(w, 404, "no rate found")
			return
		}
		rows = []dbquery.FxRate{row}
	} else {
		rows, err = q.ListFXRates(r.Context(), dbquery.ListFXRatesParams{FromCurrency: from, ToCurrency: to})
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	out := make([]fxOutput, 0, len(rows))
	for _, row := range rows {
		o, e := fxJSON(row)
		if e != nil {
			authError(w, 500, "internal server error")
			return
		}
		out = append(out, o)
	}
	w.Header().Set("Content-Type", "application/json")
	if latest {
		json.NewEncoder(w).Encode(out[0])
	} else {
		json.NewEncoder(w).Encode(out)
	}
}

func fxDate(raw string) (time.Time, *validationError) {
	fail := func(reason string) (time.Time, *validationError) {
		return time.Time{}, &validationError{"date_from_datetime_parsing", []any{"query", "on_date"}, "Input should be a valid date or datetime, " + reason, raw, map[string]any{"error": reason}}
	}
	if n, err := strconv.ParseFloat(raw, 64); err == nil && !strings.ContainsAny(raw, "eE") && !math.IsNaN(n) && !math.IsInf(n, 0) && math.Abs(n) < 1e16 {
		if n > 20000000000 || n < -20000000000 {
			n /= 1000
		}
		// Pydantic rounds numeric Unix timestamps to microsecond precision.
		secs := math.Floor(n)
		micros := int64(math.Round((n - secs) * 1e6))
		dt := time.Unix(int64(secs), micros*int64(time.Microsecond)).UTC()
		if dt.Year() > 9999 {
			return fail("dates after 9999 are not supported as unix timestamps")
		}
		if dt.Year() < 0 {
			return fail("dates before 0000 are not supported as unix timestamps")
		}
		if dt.Hour() != 0 || dt.Minute() != 0 || dt.Second() != 0 || dt.Nanosecond() != 0 {
			return time.Time{}, &validationError{"date_from_datetime_inexact", []any{"query", "on_date"}, "Datetimes provided to dates should have zero time - e.g. be exact dates", raw, nil}
		}
		if dt.Year() == 0 {
			return time.Time{}, &validationError{"date_parsing", []any{"query", "on_date"}, "Input should be a valid date in the format YYYY-MM-DD, year 0 is out of range", raw, map[string]any{"error": "year 0 is out of range"}}
		}
		return dt, nil
	}
	if len(raw) < 10 {
		return fail("input is too short")
	}
	if raw[4] != '-' || raw[7] != '-' {
		return fail("invalid date separator, expected `-`")
	}
	year, e := strconv.Atoi(raw[:4])
	if e != nil {
		return fail("invalid character in year")
	}
	if year == 0 {
		return time.Time{}, &validationError{"date_parsing", []any{"query", "on_date"}, "Input should be a valid date in the format YYYY-MM-DD, year 0 is out of range", raw, map[string]any{"error": "year 0 is out of range"}}
	}
	if _, e := strconv.Atoi(raw[8:10]); e != nil {
		return fail("invalid character in day")
	}
	month, e := strconv.Atoi(raw[5:7])
	if e != nil {
		return fail("invalid character in month")
	}
	if month < 1 || month > 12 {
		return fail("month value is outside expected range of 1-12")
	}
	d, e := time.Parse("2006-01-02", raw[:10])
	if e != nil {
		return fail("day value is outside expected range")
	}
	if len(raw) == 10 {
		return d, nil
	}
	if !strings.ContainsRune("Tt_ ", rune(raw[10])) {
		return fail("invalid datetime separator, expected `T`, `t`, `_` or space")
	}
	if len(raw) >= 16 {
		for _, part := range []struct {
			start, end, max int
			name            string
		}{{11, 13, 23, "hour"}, {14, 16, 59, "minute"}, {17, 19, 59, "second"}} {
			if len(raw) < part.end || (part.name == "second" && raw[16] != ':') {
				continue
			}
			n, e := strconv.Atoi(raw[part.start:part.end])
			if e == nil && n > part.max {
				return fail(part.name + " value is outside expected range of 0-" + strconv.Itoa(part.max))
			}
		}
	}
	// speedate accepts compact offsets, but Go's RFC3339 parser accepts
	// out-of-range offset components. Validate them before normalizing.
	value := raw[:10] + "T" + raw[11:]
	zoneStart := strings.IndexAny(value[11:], "+-Zz")
	if zoneStart >= 0 {
		zoneStart += 11
		zone := value[zoneStart:]
		if zone[0] == 'Z' || zone[0] == 'z' {
			if len(zone) != 1 {
				return fail("unexpected extra characters at the end of the input")
			}
			value = value[:zoneStart] + "Z"
		} else {
			if len(zone) < 3 {
				return fail("invalid timezone hour")
			}
			hour, err := strconv.Atoi(zone[1:3])
			if err != nil {
				return fail("invalid timezone hour")
			}
			rest := zone[3:]
			if strings.HasPrefix(rest, ":") {
				rest = rest[1:]
			}
			if len(rest) < 2 {
				return fail("invalid timezone minute")
			}
			minute, err := strconv.Atoi(rest[:2])
			if err != nil {
				return fail("invalid timezone minute")
			}
			if minute > 59 {
				return fail("timezone minute value is outside expected range of 0-59")
			}
			if hour >= 24 {
				return fail("timezone offset must be less than 24 hours")
			}
			if len(rest) > 2 {
				return fail("unexpected extra characters at the end of the input")
			}
			value = value[:zoneStart] + zone[:3] + ":" + rest
		}
	}

	var dt time.Time
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04Z07:00", "2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05", "2006-01-02T15:04"} {
		dt, e = time.Parse(layout, value)
		if e == nil {
			break
		}
	}
	if e != nil {
		return fail("input is too short")
	}
	// Pydantic truncates datetime string fractions beyond six digits.
	if dt.Hour() != 0 || dt.Minute() != 0 || dt.Second() != 0 || dt.Nanosecond() >= int(time.Microsecond) {
		return time.Time{}, &validationError{"date_from_datetime_inexact", []any{"query", "on_date"}, "Datetimes provided to dates should have zero time - e.g. be exact dates", raw, nil}
	}
	return d, nil
}
