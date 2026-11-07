package platform

import (
	"bytes"
	"errors"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopeduser"
	"net/http"
	"unicode/utf8"
)

func (h scopedProfileRouter) createUser(w http.ResponseWriter, r *http.Request) {
	m, password, ok := h.adminUserBody(w, r, true)
	if !ok {
		return
	}
	current, ok := h.admin(w, r)
	if !ok {
		return
	}
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	defer tx.Rollback(r.Context())
	admin, err := scopeduser.LockSessionUserByID(r.Context(), tx, h.scope, dbqueryv8.LockSessionUserByIDParams{UserID: current.UserID})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && admin.Role != "admin" {
		authError(w, 403, "admin required")
		return
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	id, err := itemUUID()
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	hash, err := (sessionHandler{config: h.cfg}).hashUserPassword(password)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	role := "admin"
	if v, ok := m["role"].(string); ok {
		role = v
	}
	var name pgtype.Text
	if v, ok := m["display_name"].(string); ok {
		name = pgtype.Text{String: v, Valid: true}
	}
	u, err := scopeduser.CreateUser(r.Context(), tx, h.scope, dbqueryv8.CreateUserParams{UserID: id, Email: m["email"].(string), PasswordHash: hash, Role: role, DisplayName: name})
	if err != nil {
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "23505" {
			authError(w, 409, "email already exists")
		} else {
			authError(w, 500, "internal server error")
		}
		return
	}
	if tx.Commit(r.Context()) != nil {
		authError(w, 500, "internal server error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(201)
	userJSON(w, u.UserID, u.Email, u.Role, u.DisplayName, u.CreatedAt)
}
func (h scopedProfileRouter) adminResetPassword(w http.ResponseWriter, r *http.Request) {
	_, password, ok := h.adminUserBody(w, r, false)
	if !ok {
		return
	}
	current, ok := h.admin(w, r)
	if !ok {
		return
	}
	raw := chi.URLParam(r, "user_id")
	id, valid := parsePathUUID(raw)
	if !valid {
		detail := uuidErrorDetail(raw)
		validationResponse(w, []validationError{{"uuid_parsing", []any{"path", "user_id"}, "Input should be a valid UUID, " + detail, raw, map[string]string{"error": detail}}})
		return
	}
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	defer tx.Rollback(r.Context())
	var admin, u dbqueryv8.LockSessionUserByIDRow
	var adminErr, targetErr error
	if bytes.Compare(current.UserID.Bytes[:], id.Bytes[:]) <= 0 {
		admin, adminErr = scopeduser.LockSessionUserByID(r.Context(), tx, h.scope, dbqueryv8.LockSessionUserByIDParams{UserID: current.UserID})
		if current.UserID == id {
			u, targetErr = admin, adminErr
		} else {
			u, targetErr = scopeduser.LockSessionUserByID(r.Context(), tx, h.scope, dbqueryv8.LockSessionUserByIDParams{UserID: id})
		}
	} else {
		u, targetErr = scopeduser.LockSessionUserByID(r.Context(), tx, h.scope, dbqueryv8.LockSessionUserByIDParams{UserID: id})
		admin, adminErr = scopeduser.LockSessionUserByID(r.Context(), tx, h.scope, dbqueryv8.LockSessionUserByIDParams{UserID: current.UserID})
	}
	if errors.Is(adminErr, pgx.ErrNoRows) || adminErr == nil && admin.Role != "admin" {
		authError(w, 403, "admin required")
		return
	}
	if adminErr != nil {
		authError(w, 500, "internal server error")
		return
	}
	if errors.Is(targetErr, pgx.ErrNoRows) {
		authError(w, 404, "user not found")
		return
	}
	if targetErr != nil {
		authError(w, 500, "internal server error")
		return
	}
	hash, err := (sessionHandler{config: h.cfg}).hashUserPassword(password)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if _, err = scopeduser.SetUserPassword(r.Context(), tx, h.scope, dbqueryv8.SetUserPasswordParams{UserID: id, PasswordHash: hash}); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if tx.Commit(r.Context()) != nil {
		authError(w, 500, "internal server error")
		return
	}
	userJSON(w, u.UserID, u.Email, u.Role, u.DisplayName, u.CreatedAt)
}

func (h scopedProfileRouter) adminUserBody(w http.ResponseWriter, r *http.Request, create bool) (map[string]any, string, bool) {
	obj, ok := parseChangePasswordBody(w, r)
	if !ok {
		return nil, "", false
	}
	if _, ok = h.admin(w, r); !ok {
		return nil, "", false
	}
	var errs []validationError
	if !create {
		raw := chi.URLParam(r, "user_id")
		_, valid := parsePathUUID(raw)
		if !valid {
			detail := uuidErrorDetail(raw)
			errs = append(errs, validationError{"uuid_parsing", []any{"path", "user_id"}, "Input should be a valid UUID, " + detail, "[REDACTED]", map[string]string{"error": detail}})
		}
	}
	if _, absent := obj.(absentChangePasswordBody); absent {
		errs = append(errs, validationError{"missing", []any{"body"}, "Field required", nil, nil})
		validationResponse(w, errs)
		return nil, "", false
	}
	m, ok := obj.(map[string]any)
	if !ok {
		errs = append(errs, validationError{"model_attributes_type", []any{"body"}, "Input should be a valid dictionary or object to extract fields from", "[REDACTED]", nil})
		validationResponse(w, errs)
		return nil, "", false
	}
	fields := []string{"password"}
	if create {
		fields = []string{"email", "password", "role", "display_name"}
	}
	for _, field := range fields {
		v, exists := m[field]
		if !exists {
			if field == "email" || field == "password" {
				errs = append(errs, validationError{"missing", []any{"body", field}, "Field required", "[REDACTED]", nil})
			}
			continue
		}
		if field == "display_name" && v == nil {
			continue
		}
		str, valid := v.(string)
		if !valid {
			errs = append(errs, validationError{"string_type", []any{"body", field}, "Input should be a valid string", "[REDACTED]", nil})
			continue
		}
		n := utf8.RuneCountInString(str)
		if field == "password" && n < 6 {
			errs = append(errs, validationError{"string_too_short", []any{"body", field}, "String should have at least 6 characters", "[REDACTED]", map[string]any{"min_length": 6}})
		}
		if field == "password" && n > 128 {
			errs = append(errs, validationError{"string_too_long", []any{"body", field}, "String should have at most 128 characters", "[REDACTED]", map[string]any{"max_length": 128}})
		}
		if field == "display_name" && n > 255 {
			errs = append(errs, validationError{"string_too_long", []any{"body", field}, "String should have at most 255 characters", "[REDACTED]", map[string]any{"max_length": 255}})
		}
		if field == "email" {
			normalized, reason := normalizeEmail(str)
			if reason != "" {
				errs = append(errs, validationError{"value_error", []any{"body", field}, "value is not a valid email address: " + reason, "[REDACTED]", map[string]string{"reason": reason}})
			} else {
				m[field] = normalized
			}
		}
	}
	if len(errs) > 0 {
		validationResponse(w, errs)
		return nil, "", false
	}
	return m, m["password"].(string), true
}
