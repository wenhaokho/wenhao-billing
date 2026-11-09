package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LOQATO/gokit/apierr"
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
	Logger *slog.Logger
	Clock  Clock
	// Sessions reads the billing_session cookie on every admin operation.
	Sessions Sessions
}

// NewServer builds the HTTP handler and the Huma API the features register
// their operations on.
func NewServer(cfg Config, deps Deps) (http.Handler, huma.API) {
	if deps.Clock == nil {
		deps.Clock = SystemClock{}
	}
	if deps.Sessions.Clock == nil {
		deps.Sessions.Clock = deps.Clock
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	// FullPath names a field as pydantic did ("lines.0.amount"), a missing
	// required property at the property itself.
	apierr.Install(apierr.ProblemJSON, Catalog, apierr.FieldPath(func(*http.Request) apierr.PathStyle { return apierr.FullPath }), apierr.OnUnexpected(func(ctx context.Context, err error) {
		observe.LoggerFrom(ctx).Error("unexpected error", "err", err)
		observe.Report(err, map[string]string{"component": "api"})
	}))

	r := chi.NewMux()
	r.Use(observe.RequestID(logger))
	r.Use(recoverer)
	r.Use(securityHeaders)
	r.Use(noStore)
	// go-chi/cors treats an empty list as "*", so a blank CORS_ORIGINS
	// installs no CORS at all: no origin is allowed.
	if len(cfg.CORSOrigins) > 0 {
		r.Use(cors.Handler(cors.Options{
			AllowedOrigins: cfg.CORSOrigins,
			AllowedMethods: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
			// sentry-trace and baggage: Sentry's frontend adds them to /api calls.
			AllowedHeaders:   []string{"Accept", "Accept-Language", "Authorization", "Content-Type", "X-Request-ID", "Sentry-Trace", "Baggage"},
			AllowCredentials: true,
			MaxAge:           600,
		}))
	}
	r.NotFound(apierr.NotFound(apierr.ProblemJSON, Catalog))
	r.MethodNotAllowed(apierr.MethodNotAllowed(apierr.ProblemJSON, Catalog))

	config := huma.DefaultConfig("API", "1.0.0")
	// No "$schema" member in response bodies.
	config.CreateHooks = nil
	// The Python API ignored unknown body fields (the frontend still sends
	// some); Huma rejects them unless a schema allows additional properties.
	// Set here, every body struct allows them.
	config.AllowAdditionalPropertiesByDefault = true

	config.OnAddOperation = append(config.OnAddOperation, relaxOptionalRequestFields, markNullablePointers)
	api := humachi.New(r, config)
	api.UseMiddleware(authenticate(api, deps.Pool, deps.Sessions))

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

// noStore keeps browsers and proxies from caching API answers, 404s
// included: the Python API did the same for these prefixes.
func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/oauth/") || strings.HasPrefix(p, "/mcp") || strings.HasPrefix(p, "/.well-known/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
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

// recoverer turns a panic into a 500 in the error problem+json body (so the frontend
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
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(apierr.ProblemBody{
				Title:  http.StatusText(http.StatusInternalServerError),
				Status: http.StatusInternalServerError,
				Detail: apierr.DefaultMessages[http.StatusInternalServerError].Render(i18n.English, Catalog),
				Code:   apierr.Codes[http.StatusInternalServerError],
			})
		}()
		next.ServeHTTP(w, r)
	})
}
