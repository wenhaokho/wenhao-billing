package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LOQATO/gokit/apierr"
	"github.com/LOQATO/gokit/auth"
	"github.com/LOQATO/gokit/i18n"
	"github.com/LOQATO/gokit/observe"
)

// The error codes clients read for 401 and 403. Set once, when the package
// loads: apierr reads Codes on every error, and a write per NewServer would
// race requests in parallel tests.
func init() {
	apierr.Codes[http.StatusUnauthorized] = "unauthenticated"
	apierr.Codes[http.StatusForbidden] = "permission_denied"
}

// Deps are what the server's routes need.
type Deps struct {
	Pool   *pgxpool.Pool
	Tokens auth.Tokens
	Logger *slog.Logger
}

// NewServer builds the HTTP handler and the Huma API the features register
// their operations on.
func NewServer(cfg Config, deps Deps) (http.Handler, huma.API) {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	apierr.Install(apierr.Envelope, Catalog, apierr.OnUnexpected(func(ctx context.Context, err error) {
		observe.LoggerFrom(ctx).Error("unexpected error", "err", err)
		observe.Report(err, map[string]string{"component": "api"})
	}))

	r := chi.NewMux()
	r.Use(observe.RequestID(logger))
	r.Use(recoverer)
	r.Use(securityHeaders)
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   cfg.CORSOrigins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Accept-Language", "Authorization", "Content-Type", "X-Request-ID"},
		AllowCredentials: true,
		MaxAge:           600,
	}))
	r.NotFound(apierr.NotFound(apierr.Envelope, Catalog))
	r.MethodNotAllowed(apierr.MethodNotAllowed(apierr.Envelope, Catalog))

	config := huma.DefaultConfig("API", "1.0.0")
	// No "$schema" member in response bodies.
	config.CreateHooks = nil
	api := humachi.New(r, config)
	api.UseMiddleware(authenticate(api, deps.Tokens))

	huma.Register(api, huma.Operation{
		OperationID: "health", Method: http.MethodGet, Path: "/health", Tags: []string{PublicTag},
	}, func(ctx context.Context, _ *struct{}) (*healthOut, error) {
		if err := deps.Pool.Ping(ctx); err != nil {
			return nil, err
		}
		out := &healthOut{}
		out.Body.Status = "ok"
		return out, nil
	})
	return r, api
}

type healthOut struct {
	Body struct {
		Status string `json:"status"`
	}
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		next.ServeHTTP(w, r)
	})
}

// recoverer turns a panic into a 500 in the error envelope (so the frontend
// can read it), logs it with its stack and reports it to Sentry. Huma does
// not recover; chi's own Recoverer writes a bare body and reports nothing.
func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			v := recover()
			if v == nil {
				return
			}
			if v == http.ErrAbortHandler { //nolint:errorlint // a sentinel compared as net/http does
				panic(v)
			}
			err := fmt.Errorf("panic: %v", v)
			observe.LoggerFrom(r.Context()).Error("panic", "err", err, "stack", string(debug.Stack()))
			observe.Report(err, map[string]string{"component": "api"})
			lang := i18n.Resolve(r.Header.Get("Accept-Language"))
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{
				"code":    apierr.Codes[http.StatusInternalServerError],
				"message": apierr.DefaultMessages[http.StatusInternalServerError].Render(lang, Catalog),
				"details": []any{},
			}})
		}()
		next.ServeHTTP(w, r)
	})
}
