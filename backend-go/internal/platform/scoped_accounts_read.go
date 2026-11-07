package platform

import (
	"encoding/json"
	"errors"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedaccount"
	"net/http"
	"strconv"
	"strings"
)

func (h scopedProfileRouter) accountRead(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.admin(w, r); !ok {
		return
	}
	raw := chi.URLParam(r, "account_id")
	if raw != "" {
		normalized := strings.TrimSpace(raw)
		if !accountIntegerPattern.MatchString(normalized) {
			validationResponse(w, []validationError{{"int_parsing", []any{"path", "account_id"}, "Input should be a valid integer, unable to parse string as an integer", raw, nil}})
			return
		}
		normalized = strings.ReplaceAll(normalized, "_", "")
		if dot := strings.IndexByte(normalized, '.'); dot >= 0 {
			normalized = normalized[:dot]
		}
		id, err := strconv.ParseInt(normalized, 10, 64)
		if errors.Is(err, strconv.ErrRange) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(500)
			w.Write([]byte("Internal Server Error"))
			return
		}
		if err != nil {
			validationResponse(w, []validationError{{"int_parsing", []any{"path", "account_id"}, "Input should be a valid integer, unable to parse string as an integer", raw, nil}})
			return
		}
		if id < -2147483648 || id > 2147483647 {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(500)
			w.Write([]byte("Internal Server Error"))
			return
		}
		a, err := scopedaccount.Get(r.Context(), h.pool, h.scope, int32(id))
		if errors.Is(err, pgx.ErrNoRows) {
			authError(w, 404, "account not found")
			return
		}
		if err != nil {
			authError(w, 500, "internal server error")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(accountOutput{a.Code, a.Name, a.Type, a.Active, a.AccountID})
		return
	}
	active, ok := accountBoolFilter(w, r)
	if !ok {
		return
	}
	rows, err := scopedaccount.List(r.Context(), h.pool, h.scope, active)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	out := make([]accountOutput, 0, len(rows))
	for _, a := range rows {
		out = append(out, accountOutput{a.Code, a.Name, a.Type, a.Active, a.AccountID})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}
