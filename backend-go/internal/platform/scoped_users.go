package platform

import (
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopeduser"
	"net/http"
	"unicode/utf8"
)

func (h scopedProfileRouter) listUsers(w http.ResponseWriter, r *http.Request) {
	current, ok := h.admin(w, r)
	id := current.UserID
	if !ok {
		return
	}
	if h.pool == nil {
		authError(w, 500, "internal server error")
		return
	}
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	defer tx.Rollback(r.Context())
	user, err := scopeduser.LockSessionUserByID(r.Context(), tx, h.scope, dbqueryv8.LockSessionUserByIDParams{UserID: id})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && user.Role != "admin" {
		authError(w, 403, "admin required")
		return
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	rows, err := scopeduser.ListUsers(r.Context(), tx, h.scope)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	out := make([]sessionUser, 0, len(rows))
	for _, u := range rows {
		var name *string
		if u.DisplayName.Valid {
			name = &u.DisplayName.String
		}
		out = append(out, sessionUser{u.UserID.String(), u.Email, u.Role, name, u.CreatedAt.Time})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}
func (h scopedProfileRouter) updateUserProfile(w http.ResponseWriter, r *http.Request) {
	obj, ok := parseProfileBody(w, r)
	if !ok {
		return
	}
	current, ok := h.admin(w, r)
	if !ok {
		return
	}
	if _, absent := obj.(absentChangePasswordBody); absent {
		validationResponse(w, []validationError{{"missing", []any{"body"}, "Field required", nil, nil}})
		return
	}
	m, ok := obj.(map[string]any)
	if !ok {
		validationResponse(w, []validationError{{"model_attributes_type", []any{"body"}, "Input should be a valid dictionary or object to extract fields from", obj, nil}})
		return
	}
	var email, name *string
	var errs []validationError
	for _, field := range []string{"display_name", "email"} {
		v, exists := m[field]
		if !exists || v == nil {
			continue
		}
		str, valid := v.(string)
		if !valid {
			errs = append(errs, validationError{"string_type", []any{"body", field}, "Input should be a valid string", v, nil})
			continue
		}
		if field == "display_name" {
			if utf8.RuneCountInString(str) > 255 {
				errs = append(errs, validationError{"string_too_long", []any{"body", field}, "String should have at most 255 characters", v, map[string]any{"max_length": 255}})
			} else {
				name = &str
			}
		} else {
			normalized, reason := normalizeEmail(str)
			if reason != "" {
				errs = append(errs, validationError{"value_error", []any{"body", field}, "value is not a valid email address: " + reason, v, map[string]string{"reason": reason}})
			} else {
				email = &normalized
			}
		}
	}
	if len(errs) > 0 {
		validationResponse(w, errs)
		return
	}
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	defer tx.Rollback(r.Context())
	u, err := scopeduser.LockSessionUserByID(r.Context(), tx, h.scope, dbqueryv8.LockSessionUserByIDParams{UserID: current.UserID})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && u.Role != "admin" {
		authError(w, 403, "admin required")
		return
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if email != nil {
		u.Email = *email
	}
	if name != nil {
		u.DisplayName = pgtype.Text{String: *name, Valid: *name != ""}
	}
	updated, err := scopeduser.UpdateUserProfile(r.Context(), tx, h.scope, dbqueryv8.UpdateUserProfileParams{UserID: u.UserID, Email: u.Email, DisplayName: u.DisplayName})
	if err != nil {
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "23505" {
			authError(w, 409, "email already in use")
		} else {
			authError(w, 500, "internal server error")
		}
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	userJSON(w, updated.UserID, updated.Email, updated.Role, updated.DisplayName, updated.CreatedAt)
}
