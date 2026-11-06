package platform

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"reflect"
	"unicode/utf8"

	"github.com/LOQATO/gokit/auth"
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
)

type createUserBody struct {
	Email       string  `json:"email" format:"email"`
	Password    string  `json:"password" format:"password" minLength:"6" maxLength:"128"`
	Role        string  `json:"role,omitempty" default:"admin"`
	DisplayName *string `json:"display_name,omitempty" maxLength:"255"`
}
type createUserRequest struct{ Body createUserBody }
type resetUserBody struct {
	Password string `json:"password" format:"password" minLength:"6" maxLength:"128"`
}
type resetUserRequest struct {
	UserID string `path:"user_id" format:"uuid"`
	Body   resetUserBody
}

func registerAdminUserWrites(api huma.API, s sessionHandler) {
	sec := []map[string][]string{{"sessionCookie": {}}}
	response := func(desc string, body any) *huma.Response {
		return &huma.Response{Description: desc, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(body), true, "")}}}
	}
	responses := func() map[string]*huma.Response {
		return map[string]*huma.Response{"401": response("Not authenticated", authErrorBody{}), "403": response("Admin required", authErrorBody{}), "422": response("Validation error", validationErrorBody{}), "500": response("Internal server error", authErrorBody{})}
	}
	createResponses := responses()
	createResponses["409"] = response("Email already exists", authErrorBody{})
	resetResponses := responses()
	resetResponses["404"] = response("User not found", authErrorBody{})
	huma.Register(api, huma.Operation{Method: "POST", Path: "/api/v1/users", OperationID: "create-user", DefaultStatus: 201, Responses: createResponses, Security: sec, SkipValidateBody: true, Middlewares: huma.Middlewares{sessionMiddleware(s.createUser)}}, func(context.Context, *createUserRequest) (*userResponse, error) { panic("middleware") })
	huma.Register(api, huma.Operation{Method: "POST", Path: "/api/v1/users/{user_id}/reset-password", OperationID: "admin-reset-password", Responses: resetResponses, Security: sec, SkipValidateBody: true, Middlewares: huma.Middlewares{sessionMiddleware(s.adminResetPassword)}}, func(context.Context, *resetUserRequest) (*userResponse, error) { panic("middleware") })
	api.OpenAPI().Components.Schemas.Map()["CreateUserBody"].AdditionalProperties = true
	api.OpenAPI().Components.Schemas.Map()["ResetUserBody"].AdditionalProperties = true
}
func (s sessionHandler) adminUserBody(w http.ResponseWriter, r *http.Request, create bool) (map[string]any, string, bool) {
	obj, ok := parseChangePasswordBody(w, r)
	if !ok {
		return nil, "", false
	}
	if _, ok = s.requireAdmin(w, r); !ok {
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
func (s sessionHandler) hashUserPassword(password string) (string, error) {
	p := auth.DefaultHashParams
	if s.config.hashParams != nil {
		p = *s.config.hashParams
	}
	return auth.Hash(password, p)
}
func (s sessionHandler) createUser(w http.ResponseWriter, r *http.Request) {
	m, password, ok := s.adminUserBody(w, r, true)
	if !ok {
		return
	}
	current, ok := s.requireAdmin(w, r)
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
	admin, err := q.LockSessionUserByID(r.Context(), current.UserID)
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
	hash, err := s.hashUserPassword(password)
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
	u, err := q.CreateUser(r.Context(), dbquery.CreateUserParams{UserID: id, Email: m["email"].(string), PasswordHash: hash, Role: role, DisplayName: name})
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
func (s sessionHandler) adminResetPassword(w http.ResponseWriter, r *http.Request) {
	_, password, ok := s.adminUserBody(w, r, false)
	if !ok {
		return
	}
	current, ok := s.requireAdmin(w, r)
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
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	defer tx.Rollback(r.Context())
	q := dbquery.New(tx)
	var admin, u dbquery.LockSessionUserByIDRow
	var adminErr, targetErr error
	if bytes.Compare(current.UserID.Bytes[:], id.Bytes[:]) <= 0 {
		admin, adminErr = q.LockSessionUserByID(r.Context(), current.UserID)
		if current.UserID == id {
			u, targetErr = admin, adminErr
		} else {
			u, targetErr = q.LockSessionUserByID(r.Context(), id)
		}
	} else {
		u, targetErr = q.LockSessionUserByID(r.Context(), id)
		admin, adminErr = q.LockSessionUserByID(r.Context(), current.UserID)
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
	hash, err := s.hashUserPassword(password)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if _, err = q.SetUserPassword(r.Context(), dbquery.SetUserPasswordParams{UserID: id, PasswordHash: hash}); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if tx.Commit(r.Context()) != nil {
		authError(w, 500, "internal server error")
		return
	}
	userJSON(w, u.UserID, u.Email, u.Role, u.DisplayName, u.CreatedAt)
}
