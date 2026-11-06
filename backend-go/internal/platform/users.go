package platform

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
)

type profileBody struct {
	DisplayName *string `json:"display_name,omitempty" maxLength:"255"`
	Email       *string `json:"email,omitempty" format:"email"`
}
type profileRequest struct{ Body profileBody }

func registerUsers(api huma.API, s sessionHandler) {
	registerAdminUserWrites(api, s)
	response := func(desc string, body any) *huma.Response {
		return &huma.Response{Description: desc, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(body), true, "")}}}
	}
	responses := func() map[string]*huma.Response {
		return map[string]*huma.Response{"401": response("Not authenticated", authErrorBody{}), "403": response("Admin required", authErrorBody{}), "500": response("Internal server error", authErrorBody{})}
	}
	security := []map[string][]string{{"sessionCookie": {}}}
	huma.Register(api, huma.Operation{Method: "GET", Path: "/api/v1/users", OperationID: "list-users", Security: security, Middlewares: huma.Middlewares{sessionMiddleware(s.listUsers)}, Responses: responses()}, func(context.Context, *struct{}) (*struct {
		Body []sessionUser `nullable:"false"`
	}, error) {
		panic("middleware")
	})
	rs := responses()
	rs["409"] = response("Email already in use", authErrorBody{})
	rs["422"] = response("Validation error", validationErrorBody{})
	huma.Register(api, huma.Operation{Method: "PATCH", Path: "/api/v1/users/me", OperationID: "update-profile", Security: security, SkipValidateBody: true, Middlewares: huma.Middlewares{sessionMiddleware(s.updateProfile)}, Responses: rs}, func(context.Context, *profileRequest) (*userResponse, error) { panic("middleware") })
	api.OpenAPI().Components.Schemas.Map()["ProfileBody"].AdditionalProperties = true
}
func (s sessionHandler) listUsers(w http.ResponseWriter, r *http.Request) {
	id, ok := s.sessionID(w, r)
	if !ok {
		return
	}
	if s.db == nil {
		authError(w, 500, "internal server error")
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
	rows, err := q.ListUsers(r.Context())
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
func (s sessionHandler) updateProfile(w http.ResponseWriter, r *http.Request) {
	obj, ok := parseProfileBody(w, r)
	if !ok {
		return
	}
	current, ok := s.requireAdmin(w, r)
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
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	defer tx.Rollback(r.Context())
	q := dbquery.New(tx)
	u, err := q.LockSessionUserByID(r.Context(), current.UserID)
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
	updated, err := q.UpdateUserProfile(r.Context(), dbquery.UpdateUserProfileParams{UserID: u.UserID, Email: u.Email, DisplayName: u.DisplayName})
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

func parseProfileBody(w http.ResponseWriter, r *http.Request) (any, bool) {
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
	if err = json.Unmarshal(body, &obj); err != nil {
		pos := len(body)
		reason := "Expecting value"
		if e, ok := err.(*json.SyntaxError); ok && int(e.Offset) <= len(body) {
			pos = int(e.Offset) - 1
			if strings.Contains(e.Error(), "unexpected end") {
				pos = len(body)
			}
		}
		trimmed := strings.TrimSpace(string(body))
		if trimmed == "{" {
			reason = "Expecting property name enclosed in double quotes"
		}
		validationResponse(w, []validationError{{"json_invalid", []any{"body", pos}, "JSON decode error", map[string]any{}, map[string]string{"error": reason}}})
		return nil, false
	}
	return obj, true
}
