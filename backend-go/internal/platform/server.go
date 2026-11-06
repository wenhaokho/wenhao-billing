package platform

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	kitdb "github.com/LOQATO/gokit/db"
	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// OpenDB validates the required database URL and opens a checked kit pool.
// Errors deliberately omit driver details, which can contain credentials.
func OpenDB(ctx context.Context, raw string) (*pgxpool.Pool, error) {
	u, err := url.Parse(raw)
	if err != nil || u == nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Hostname() == "" || strings.Trim(u.Path, "/") == "" {
		return nil, errors.New("invalid or missing DATABASE_URL: a PostgreSQL URL is required")
	}
	// Every pooled connection must use the same schema admitted at startup.
	// libpq options can contain additional server -c overrides; reject them.
	params := u.Query()
	if params.Has("options") {
		return nil, errors.New("invalid DATABASE_URL: options are not supported")
	}
	params.Set("search_path", "public")
	u.RawQuery = params.Encode()
	p, err := kitdb.Open(ctx, kitdb.PoolConfig{URL: u.String()})
	if err != nil {
		return nil, errors.New("could not connect to configured database")
	}
	return p, nil
}

// NewHandler builds the application router. A nil pool reports unhealthy.
func NewHandler(pool *pgxpool.Pool, origins []string) http.Handler {
	return newHandler(pool, origins, nil)
}

// NewHandlerWithSessions configures the authenticated API.
func NewHandlerWithSessions(pool *pgxpool.Pool, origins []string, cfg SessionConfig) (http.Handler, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return newHandler(pool, origins, &cfg), nil
}

func newHandler(pool *pgxpool.Pool, origins []string, cfg *SessionConfig) http.Handler {
	r := chi.NewRouter()

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"detail": "Not Found"})
	})
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if pool == nil || pool.Ping(ctx) != nil {
			http.Error(w, "database unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"ok"}`))
	})
	humaCfg := huma.DefaultConfig("Billing API", "1.0.0")
	api := humachi.New(r, humaCfg)
	if cfg != nil {
		registerSessionOperations(api, sessionHandler{db: pool, config: *cfg})
		registerUsers(api, sessionHandler{db: pool, config: *cfg})
		registerItemReads(api, sessionHandler{db: pool, config: *cfg})
		registerVendorReads(api, sessionHandler{db: pool, config: *cfg})
		registerVendorCreate(api, sessionHandler{db: pool, config: *cfg})
		registerVendorUpdate(api, sessionHandler{db: pool, config: *cfg})
		registerVendorDelete(api, sessionHandler{db: pool, config: *cfg})
		registerCustomerReads(api, sessionHandler{db: pool, config: *cfg})
		registerProjectReads(api, sessionHandler{db: pool, config: *cfg})
		registerProfileReads(api, sessionHandler{db: pool, config: *cfg})
		registerProfileUpdate(api, sessionHandler{db: pool, config: *cfg})
		registerPaymentAccountsUpdate(api, sessionHandler{db: pool, config: *cfg})
		registerBillReads(api, sessionHandler{db: pool, config: *cfg})
		registerBillCreate(api, sessionHandler{db: pool, config: *cfg})
		registerBillUpdate(api, sessionHandler{db: pool, config: *cfg})
		registerBillPaymentVoid(api, sessionHandler{db: pool, config: *cfg})
		registerPaymentReads(api, sessionHandler{db: pool, config: *cfg})
		registerPaymentAudit(api, sessionHandler{db: pool, config: *cfg})
		registerQuotationReads(api, sessionHandler{db: pool, config: *cfg})
		registerQuotationCreate(api, sessionHandler{db: pool, config: *cfg})
		registerQuotationUpdate(api, sessionHandler{db: pool, config: *cfg})
		registerProjectCreate(api, sessionHandler{db: pool, config: *cfg})
		registerProjectUpdate(api, sessionHandler{db: pool, config: *cfg})
		registerProjectDelete(api, sessionHandler{db: pool, config: *cfg})
		registerProjectSummary(api, sessionHandler{db: pool, config: *cfg})
		registerCustomerBalances(api, sessionHandler{db: pool, config: *cfg})
		registerCustomerSummary(api, sessionHandler{db: pool, config: *cfg})
		registerInvoiceReads(api, sessionHandler{db: pool, config: *cfg})
		registerRecurringAction(api, sessionHandler{db: pool, config: *cfg})
		registerRecurringTrigger(api, sessionHandler{db: pool, config: *cfg})
		registerInvoiceCreate(api, sessionHandler{db: pool, config: *cfg})
		registerMilestoneCreate(api, sessionHandler{db: pool, config: *cfg})
		registerInvoiceUpdate(api, sessionHandler{db: pool, config: *cfg})
		registerRecurringTemplateUpdate(api, sessionHandler{db: pool, config: *cfg})
		registerInvoiceDelete(api, sessionHandler{db: pool, config: *cfg})
		registerInvoicePayments(api, sessionHandler{db: pool, config: *cfg})
		registerInvoiceRecordPayment(api, sessionHandler{db: pool, config: *cfg})
		registerInvoiceTransitions(api, sessionHandler{db: pool, config: *cfg})
		registerUsageLock(api, sessionHandler{db: pool, config: *cfg})
		registerCustomerCreate(api, sessionHandler{db: pool, config: *cfg})
		registerCustomerUpdate(api, sessionHandler{db: pool, config: *cfg})
		registerAccountReads(api, sessionHandler{db: pool, config: *cfg})
		registerFXReads(api, sessionHandler{db: pool, config: *cfg})
		registerFXWrites(api, sessionHandler{db: pool, config: *cfg})
		registerFXSync(api, sessionHandler{db: pool, config: *cfg})
		registerAccountWrites(api, sessionHandler{db: pool, config: *cfg})
		registerItemCreate(api, sessionHandler{db: pool, config: *cfg})
		registerItemUpdate(api, sessionHandler{db: pool, config: *cfg})
	}
	return NoStore(cors(r, origins))
}

// NoStore applies the same path-prefix policy as the Python server.
func NoStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/oauth/") || strings.HasPrefix(p, "/mcp") || strings.HasPrefix(p, "/.well-known/") {
			w.Header().Set("Cache-Control", "no-store")
			defer w.Header().Set("Cache-Control", "no-store")
			w = &noStoreWriter{ResponseWriter: w}
		}
		next.ServeHTTP(w, r)
	})
}

type noStoreWriter struct {
	http.ResponseWriter
	wrote bool
}

func (w *noStoreWriter) WriteHeader(code int) {
	if !w.wrote {
		w.Header().Set("Cache-Control", "no-store")
		if code < 100 || code >= 200 || code == http.StatusSwitchingProtocols {
			w.wrote = true
		}
		w.ResponseWriter.WriteHeader(code)
	}
}
func (w *noStoreWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}
func (w *noStoreWriter) Flush() {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	http.NewResponseController(w.ResponseWriter).Flush()
}
func (w *noStoreWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func cors(next http.Handler, origins []string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		allowed := false
		for _, o := range origins {
			if origin != "" && o == origin {
				allowed = true
				break
			}
		}
		if allowed {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Add("Vary", "Origin")
		}
		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			w.Header().Add("Vary", "Origin")
			if !allowed {
				http.Error(w, "Disallowed CORS origin", http.StatusBadRequest)
				return
			}
			w.Header().Set("Access-Control-Allow-Methods", "DELETE, GET, HEAD, OPTIONS, PATCH, POST, PUT")
			w.Header().Set("Access-Control-Allow-Headers", r.Header.Get("Access-Control-Request-Headers"))
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}
