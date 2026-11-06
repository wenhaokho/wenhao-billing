package platform

import (
	"context"
	"net/http"
	"reflect"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
)

type loginBody struct {
	Email    string `json:"email" format:"email"`
	Password string `json:"password" format:"password"`
	Remember bool   `json:"remember,omitempty" default:"false"`
}
type forgotPasswordBody struct {
	Email string `json:"email" format:"email"`
}
type forgotPasswordRequest struct{ Body forgotPasswordBody }
type forgotPasswordResponse struct {
	Body struct {
		Message    string  `json:"message"`
		ResetToken *string `json:"reset_token"`
		ResetLink  *string `json:"reset_link"`
	}
}
type resetPasswordBody struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password" format:"password" minLength:"6" maxLength:"128"`
}
type resetPasswordRequest struct{ Body resetPasswordBody }
type loginRequest struct{ Body loginBody }
type changePasswordBody struct {
	CurrentPassword string `json:"current_password" format:"password"`
	NewPassword     string `json:"new_password" format:"password" minLength:"6" maxLength:"128"`
}
type changePasswordRequest struct{ Body changePasswordBody }
type sessionUser struct {
	UserID      string    `json:"user_id" format:"uuid"`
	Email       string    `json:"email" format:"email"`
	Role        string    `json:"role"`
	DisplayName *string   `json:"display_name"`
	CreatedAt   time.Time `json:"created_at"`
}
type loginResponse struct {
	SetCookie string `header:"Set-Cookie"`
	Body      sessionUser
}
type userResponse struct{ Body sessionUser }
type logoutResponse struct {
	SetCookie string `header:"Set-Cookie"`
}
type authErrorBody struct {
	Detail string `json:"detail"`
}
type validationDetail struct {
	Type  string         `json:"type"`
	Loc   []any          `json:"loc"`
	Msg   string         `json:"msg"`
	Input any            `json:"input"`
	Ctx   map[string]any `json:"ctx,omitempty"`
}
type validationErrorBody struct {
	Detail []validationDetail `json:"detail"`
}

// Dispatch through Huma's operation middleware so the original raw body and
// response are preserved for the Python-compatible validator and cookies.
func sessionMiddleware(handler http.HandlerFunc) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, _ func(huma.Context)) { r, w := humachi.Unwrap(ctx); handler(w, r) }
}
func registerSessionOperations(api huma.API, s sessionHandler) {
	if api.OpenAPI().Components.SecuritySchemes == nil {
		api.OpenAPI().Components.SecuritySchemes = map[string]*huma.SecurityScheme{}
	}
	api.OpenAPI().Components.SecuritySchemes["sessionCookie"] = &huma.SecurityScheme{Type: "apiKey", In: "cookie", Name: "session"}
	response := func(desc string, body any) *huma.Response {
		return &huma.Response{Description: desc, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(body), true, "")}}}
	}
	forgotOp := huma.Operation{Method: http.MethodPost, Path: "/api/v1/auth/forgot-password", OperationID: "auth-forgot-password", SkipValidateBody: true, Middlewares: huma.Middlewares{sessionMiddleware(s.forgotPassword)}, Responses: map[string]*huma.Response{"422": response("Validation error", validationErrorBody{}), "500": response("Internal server error", authErrorBody{})}}
	huma.Register(api, forgotOp, func(context.Context, *forgotPasswordRequest) (*forgotPasswordResponse, error) {
		panic("session middleware did not dispatch")
	})
	resetOp := huma.Operation{Method: http.MethodPost, Path: "/api/v1/auth/reset-password", OperationID: "auth-reset-password", SkipValidateBody: true, Middlewares: huma.Middlewares{sessionMiddleware(s.resetPassword)}, Responses: map[string]*huma.Response{"400": response("Invalid or expired token", authErrorBody{}), "422": response("Validation error", validationErrorBody{}), "500": response("Internal server error", authErrorBody{})}}
	huma.Register(api, resetOp, func(context.Context, *resetPasswordRequest) (*userResponse, error) {
		panic("session middleware did not dispatch")
	})
	api.OpenAPI().Components.Schemas.Map()["ForgotPasswordBody"].AdditionalProperties = true
	api.OpenAPI().Components.Schemas.Map()["ResetPasswordBody"].AdditionalProperties = true
	loginOp := huma.Operation{Method: http.MethodPost, Path: "/api/v1/auth/login", OperationID: "auth-login", SkipValidateBody: true, Middlewares: huma.Middlewares{sessionMiddleware(s.login)}, Responses: map[string]*huma.Response{"401": response("Invalid credentials", authErrorBody{}), "422": response("Validation error", validationErrorBody{}), "500": response("Internal server error", authErrorBody{})}}
	huma.Register(api, loginOp, func(context.Context, *loginRequest) (*loginResponse, error) {
		panic("session middleware did not dispatch")
	})
	api.OpenAPI().Components.Schemas.Map()["LoginBody"].AdditionalProperties = true
	logoutOp := huma.Operation{Method: http.MethodPost, Path: "/api/v1/auth/logout", OperationID: "auth-logout", DefaultStatus: 204, Middlewares: huma.Middlewares{sessionMiddleware(s.logout)}}
	huma.Register(api, logoutOp, func(context.Context, *struct{}) (*logoutResponse, error) {
		panic("session middleware did not dispatch")
	})
	changeOp := huma.Operation{Method: http.MethodPost, Path: "/api/v1/auth/change-password", OperationID: "auth-change-password", SkipValidateBody: true, Security: []map[string][]string{{"sessionCookie": {}}}, Middlewares: huma.Middlewares{sessionMiddleware(s.changePassword)}, Responses: map[string]*huma.Response{"400": response("Invalid password", authErrorBody{}), "401": response("Not authenticated", authErrorBody{}), "403": response("Admin required", authErrorBody{}), "422": response("Validation error", validationErrorBody{}), "500": response("Internal server error", authErrorBody{})}}
	huma.Register(api, changeOp, func(context.Context, *changePasswordRequest) (*userResponse, error) {
		panic("session middleware did not dispatch")
	})
	api.OpenAPI().Components.Schemas.Map()["ChangePasswordBody"].AdditionalProperties = true
	meOp := huma.Operation{Method: http.MethodGet, Path: "/api/v1/auth/me", OperationID: "auth-me", Security: []map[string][]string{{"sessionCookie": {}}}, Middlewares: huma.Middlewares{sessionMiddleware(s.me)}, Responses: map[string]*huma.Response{"401": response("Not authenticated or expired", authErrorBody{}), "403": response("Admin required", authErrorBody{}), "500": response("Internal server error", authErrorBody{})}}
	huma.Register(api, meOp, func(context.Context, *struct{}) (*userResponse, error) { panic("session middleware did not dispatch") })
}
