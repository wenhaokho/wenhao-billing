package platform

import (
	"encoding/json"
	"errors"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedaccount"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopeduser"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

func (h scopedProfileRouter) accountWrite(w http.ResponseWriter, r *http.Request) {
	var rawBody []byte
	if r.Method != "DELETE" {
		var ok bool
		rawBody, ok = readBodyWithSyntax(w, r)
		if !ok {
			return
		}
	}
	user, ok := h.admin(w, r)
	if !ok {
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
	tx, e := h.pool.Begin(ctx)
	if e != nil {
		authError(w, 500, "internal server error")
		return
	}
	defer tx.Rollback(ctx)
	locked, e := scopeduser.LockSessionUserByID(ctx, tx, h.scope, dbqueryv8.LockSessionUserByIDParams{UserID: user.UserID})
	if errors.Is(e, pgx.ErrNoRows) || e == nil && locked.Role != "admin" {
		authError(w, 403, "admin required")
		return
	}
	if e != nil {
		authError(w, 500, "internal server error")
		return
	}
	var a dbqueryv8.ChartOfAccount
	if r.Method == "POST" {
		active := true
		if v, ok := body["active"]; ok {
			active = v.(bool)
		}
		a, e = scopedaccount.Create(ctx, tx, h.scope, scopedaccount.Fields{Code: body["code"].(string), Name: body["name"].(string), Type: body["type"].(string), Active: active})
	} else {
		a, e = scopedaccount.Lock(ctx, tx, h.scope, int32(id))
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
			a, e = scopedaccount.Update(ctx, tx, h.scope, a.AccountID, scopedaccount.Fields{Code: a.Code, Name: a.Name, Type: a.Type, Active: a.Active})
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
	json.NewEncoder(w).Encode(accountOutput{a.Code, a.Name, a.Type, a.Active, a.AccountID})
}
