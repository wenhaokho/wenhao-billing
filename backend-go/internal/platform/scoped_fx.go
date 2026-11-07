package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedfx"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopeduser"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"
)

func legacyFX(v dbqueryv8.FxRate) dbquery.FxRate {
	return dbquery.FxRate{RateID: v.RateID, FromCurrency: v.FromCurrency, ToCurrency: v.ToCurrency, Rate: v.Rate, AsOfDate: v.AsOfDate, Source: v.Source, CreatedAt: v.CreatedAt}
}
func (h scopedProfileRouter) fxRead(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.admin(w, r); !ok {
		return
	}
	base := h.cfg.BaseCurrency
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
	var rows []dbqueryv8.FxRate
	var err error
	if latest {
		if !to.Valid {
			to = pgtype.Text{String: cases.Upper(language.Und).String(base), Valid: true}
		}
		var row dbqueryv8.FxRate
		row, err = scopedfx.Latest(r.Context(), h.pool, h.scope, from.String, to.String, on)
		if errors.Is(err, pgx.ErrNoRows) {
			authError(w, 404, "no rate found")
			return
		}
		rows = []dbqueryv8.FxRate{row}
	} else {
		rows, err = scopedfx.List(r.Context(), h.pool, h.scope, from, to)
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	out := make([]fxOutput, 0, len(rows))
	for _, row := range rows {
		o, e := fxJSON(legacyFX(row))
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

func (h scopedProfileRouter) fxCreate(w http.ResponseWriter, r *http.Request) {
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
	current, ok := h.admin(w, r)
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
	ctx, cancel := context.WithTimeout(r.Context(), scopedFXWriteTimeout)
	defer cancel()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	defer rollbackScopedFX(tx)
	user, err := scopeduser.LockSessionUserByID(ctx, tx, h.scope, dbqueryv8.LockSessionUserByIDParams{UserID: current.UserID})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && user.Role != "admin" {
		authError(w, 403, "admin required")
		return
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if err = scopedfx.LockSync(ctx, tx, h.scope); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	row, err := scopedfx.Create(ctx, tx, h.scope, scopedfx.Rate{From: p.FromCurrency, To: p.ToCurrency, Value: p.Rate, Date: p.AsOfDate, Source: p.Source})
	if err != nil {
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "23505" {
			authError(w, 409, "fx rate for this currency pair and date already exists")
		} else {
			authError(w, 500, "internal server error")
		}
		return
	}
	out, err := fxJSON(legacyFX(row))
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

func (h scopedProfileRouter) fxDelete(w http.ResponseWriter, r *http.Request) {
	current, ok := h.admin(w, r)
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
	ctx, cancel := context.WithTimeout(r.Context(), scopedFXWriteTimeout)
	defer cancel()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	defer rollbackScopedFX(tx)
	user, err := scopeduser.LockSessionUserByID(ctx, tx, h.scope, dbqueryv8.LockSessionUserByIDParams{UserID: current.UserID})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && user.Role != "admin" {
		authError(w, 403, "admin required")
		return
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if err = scopedfx.LockSync(ctx, tx, h.scope); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	_, err = scopedfx.Delete(ctx, tx, h.scope, id)
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
