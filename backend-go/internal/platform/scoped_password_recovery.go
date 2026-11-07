package platform

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"github.com/LOQATO/gokit/auth"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
	"github.com/wenhaokho/wenhao-billing/backend/internal/email"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopeduser"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

func (h scopedProfileRouter) resetPassword(w http.ResponseWriter, r *http.Request) {
	obj, ok := parseChangePasswordBody(w, r)
	if !ok {
		return
	}
	if _, absent := obj.(absentChangePasswordBody); absent {
		validationResponse(w, []validationError{{"missing", []any{"body"}, "Field required", nil, nil}})
		return
	}
	m, ok := obj.(map[string]any)
	if !ok {
		validationResponse(w, []validationError{{"model_attributes_type", []any{"body"}, "Input should be a valid dictionary or object to extract fields from", "[REDACTED]", nil}})
		return
	}
	values := make([]string, 2)
	var errs []validationError
	for i, field := range []string{"token", "new_password"} {
		v, exists := m[field]
		if !exists {
			errs = append(errs, validationError{"missing", []any{"body", field}, "Field required", "[REDACTED]", nil})
			continue
		}
		str, valid := v.(string)
		if !valid {
			errs = append(errs, validationError{"string_type", []any{"body", field}, "Input should be a valid string", "[REDACTED]", nil})
			continue
		}
		values[i] = str
		n := utf8.RuneCountInString(str)
		if i == 1 && n < 6 {
			errs = append(errs, validationError{"string_too_short", []any{"body", field}, "String should have at least 6 characters", "[REDACTED]", map[string]any{"min_length": 6}})
		}
		if i == 1 && n > 128 {
			errs = append(errs, validationError{"string_too_long", []any{"body", field}, "String should have at most 128 characters", "[REDACTED]", map[string]any{"max_length": 128}})
		}
	}
	if len(errs) > 0 {
		validationResponse(w, errs)
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
	user, err := scopeduser.LockUserByResetToken(r.Context(), tx, h.scope, dbqueryv8.LockUserByResetTokenParams{PasswordResetToken: pgtype.Text{String: values[0], Valid: true}})
	if errors.Is(err, pgx.ErrNoRows) {
		authError(w, 400, "invalid or expired reset token")
		return
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if !user.PasswordResetExpires.Valid || user.PasswordResetExpires.Time.Before(time.Now().UTC()) {
		if scopeduser.ClearResetToken(r.Context(), tx, h.scope, dbqueryv8.ClearResetTokenParams{UserID: user.UserID}) != nil || tx.Commit(r.Context()) != nil {
			authError(w, 500, "internal server error")
			return
		}
		authError(w, 400, "invalid or expired reset token")
		return
	}
	params := auth.DefaultHashParams
	if h.cfg.hashParams != nil {
		params = *h.cfg.hashParams
	}
	hash, err := auth.Hash(values[1], params)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	_, err = scopeduser.SetUserPassword(r.Context(), tx, h.scope, dbqueryv8.SetUserPasswordParams{UserID: user.UserID, PasswordHash: hash})
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if tx.Commit(r.Context()) != nil {
		authError(w, 500, "internal server error")
		return
	}
	userJSON(w, user.UserID, user.Email, user.Role, user.DisplayName, user.CreatedAt)
}

func (h scopedProfileRouter) forgotPassword(w http.ResponseWriter, r *http.Request) {
	obj, ok := parseChangePasswordBody(w, r)
	if !ok {
		return
	}
	if _, absent := obj.(absentChangePasswordBody); absent {
		validationResponse(w, []validationError{{"missing", []any{"body"}, "Field required", nil, nil}})
		return
	}
	m, ok := obj.(map[string]any)
	if !ok {
		validationResponse(w, []validationError{{"model_attributes_type", []any{"body"}, "Input should be a valid dictionary or object to extract fields from", "[REDACTED]", nil}})
		return
	}
	v, exists := m["email"]
	if !exists {
		validationResponse(w, []validationError{{"missing", []any{"body", "email"}, "Field required", "[REDACTED]", nil}})
		return
	}
	addr, ok := v.(string)
	if !ok {
		validationResponse(w, []validationError{{"string_type", []any{"body", "email"}, "Input should be a valid string", "[REDACTED]", nil}})
		return
	}
	addr, reason := normalizeEmail(addr)
	if reason != "" {
		validationResponse(w, []validationError{{"value_error", []any{"body", "email"}, "value is not a valid email address: " + reason, "[REDACTED]", map[string]string{"reason": reason}}})
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
	user, err := scopeduser.FindUserByEmail(r.Context(), tx, h.scope, dbqueryv8.FindUserByEmailParams{Email: addr})
	if errors.Is(err, pgx.ErrNoRows) {
		recoveryResponse(w)
		return
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	b := make([]byte, 32)
	if _, err = rand.Read(b); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	err = scopeduser.SetResetToken(r.Context(), tx, h.scope, dbqueryv8.SetResetTokenParams{UserID: user.UserID, PasswordResetToken: pgtype.Text{String: token, Valid: true}, PasswordResetExpires: pgtype.Timestamp{Time: time.Now().UTC().Add(time.Hour), Valid: true}})
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	base := h.cfg.BaseURL
	if base == "" {
		base = "http://localhost:5173"
	}
	u, err := url.Parse(base)
	if err == nil && u.Host != "" && (u.Scheme == "http" || u.Scheme == "https") && u.User == nil && u.RawQuery == "" && u.Fragment == "" {
		u.Path = strings.TrimRight(u.Path, "/") + "/reset-password"
		query := u.Query()
		query.Set("token", token)
		u.RawQuery = query.Encode()
		if h.cfg.Sender != nil {
			if err := h.cfg.Sender.Send(r.Context(), email.Message{To: []string{user.Email}, Subject: "Reset your password", Text: "Reset your password: " + u.String()}); err != nil {
				log.Print("password recovery email delivery unavailable")
			}
		}
	}
	recoveryResponse(w)
}
