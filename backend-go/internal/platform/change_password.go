package platform

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/LOQATO/gokit/auth"
	"github.com/jackc/pgx/v5"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
)

func (s sessionHandler) changePassword(w http.ResponseWriter, r *http.Request) {
	body, ok := parseChangePasswordBody(w, r)
	if !ok {
		return
	}
	id, ok := s.sessionID(w, r)
	if !ok {
		return
	}
	if s.db == nil {
		authError(w, 500, "internal server error")
		return
	}
	initial, err := dbquery.New(s.db).GetSessionUserByID(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && initial.Role != "admin" {
		authError(w, 403, "admin required")
		return
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	current, next, ok := validateChangePasswordBody(w, body)
	if !ok {
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	defer tx.Rollback(r.Context())
	q := dbquery.New(tx)
	user, err := q.LockSessionUserByID(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && user.Role != "admin" {
		authError(w, 403, "admin required")
		return
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	valid, err := auth.Verify(current, user.PasswordHash)
	if err != nil || !valid {
		authError(w, 400, "current password is incorrect")
		return
	}
	if current == next {
		authError(w, 400, "new password must differ from current password")
		return
	}
	params := auth.DefaultHashParams
	if s.config.hashParams != nil {
		params = *s.config.hashParams
	}
	hash, err := auth.Hash(next, params)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	_, err = q.SetUserPassword(r.Context(), dbquery.SetUserPasswordParams{UserID: id, PasswordHash: hash})
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	userJSON(w, user.UserID, user.Email, user.Role, user.DisplayName, user.CreatedAt)
}

type absentChangePasswordBody struct{}

func parseChangePasswordBody(w http.ResponseWriter, r *http.Request) (any, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		validationResponse(w, []validationError{{"json_invalid", []any{"body", 0}, "JSON decode error", map[string]any{}, map[string]string{"error": "Invalid JSON"}}})
		return nil, false
	}
	if len(body) == 0 {
		return absentChangePasswordBody{}, true
	}
	if !requestJSONMediaType(r) {
		return "[REDACTED]", true
	}
	var obj any
	if err := json.Unmarshal(body, &obj); err != nil {
		if strings.Contains(err.Error(), "unexpected end") {
			err = io.ErrUnexpectedEOF
		}
		validationResponse(w, itemUpdateJSONError(body, err, false))
		return nil, false
	}
	return obj, true
}

func validateChangePasswordBody(w http.ResponseWriter, obj any) (string, string, bool) {
	if _, absent := obj.(absentChangePasswordBody); absent {
		validationResponse(w, []validationError{{"missing", []any{"body"}, "Field required", nil, nil}})
		return "", "", false
	}
	m, ok := obj.(map[string]any)
	if !ok {
		validationResponse(w, []validationError{{"model_attributes_type", []any{"body"}, "Input should be a valid dictionary or object to extract fields from", "[REDACTED]", nil}})
		return "", "", false
	}
	var errs []validationError
	values := make([]string, 2)
	for i, field := range []string{"current_password", "new_password"} {
		value, exists := m[field]
		if !exists {
			errs = append(errs, validationError{"missing", []any{"body", field}, "Field required", "[REDACTED]", nil})
			continue
		}
		v, valid := value.(string)
		if !valid {
			errs = append(errs, validationError{"string_type", []any{"body", field}, "Input should be a valid string", "[REDACTED]", nil})
			continue
		}
		values[i] = v
		n := utf8.RuneCountInString(v)
		if field == "new_password" && n < 6 {
			errs = append(errs, validationError{"string_too_short", []any{"body", field}, "String should have at least 6 characters", "[REDACTED]", map[string]any{"min_length": 6}})
		}
		if field == "new_password" && n > 128 {
			errs = append(errs, validationError{"string_too_long", []any{"body", field}, "String should have at most 128 characters", "[REDACTED]", map[string]any{"max_length": 128}})
		}
	}
	if len(errs) > 0 {
		validationResponse(w, errs)
		return "", "", false
	}
	return values[0], values[1], true
}
