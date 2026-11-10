// Command api serves the HTTP API.
//
//	api                 serve (migrations run first, under a lock; refuses a
//	                    database that still has only Alembic's history)
//	api adopt-baseline  hand the Alembic-built database to goose without
//	                    running the baseline (once, before the first serve)
//	api healthcheck  exit 0 when the local server answers /health (for the image)
//	api openapi      print the OpenAPI document (for the frontend's types)
//	api set-password <email>
//	                 read a password from stdin (one line) and give it to the
//	                 user, signing out every session of theirs; the way a
//	                 Python-era (bcrypt) account gets back in
//	api issue-session <email>
//	                 print a session token for the admin (8 hours; writes
//	                 nothing), for scripts against a running API
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LOQATO/gokit/db"
	"github.com/LOQATO/gokit/observe"

	"github.com/wenhaokho/wenhao-billing/backend/internal/accounts"
	"github.com/wenhaokho/wenhao-billing/backend/internal/auth"
	"github.com/wenhaokho/wenhao-billing/backend/internal/bills"
	"github.com/wenhaokho/wenhao-billing/backend/internal/business"
	"github.com/wenhaokho/wenhao-billing/backend/internal/customers"
	"github.com/wenhaokho/wenhao-billing/backend/internal/email"
	"github.com/wenhaokho/wenhao-billing/backend/internal/fx"
	"github.com/wenhaokho/wenhao-billing/backend/internal/hosting"
	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices"
	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices/invoicesdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/items"
	"github.com/wenhaokho/wenhao-billing/backend/internal/jobs"
	"github.com/wenhaokho/wenhao-billing/backend/internal/mcp"
	"github.com/wenhaokho/wenhao-billing/backend/internal/oauth"
	"github.com/wenhaokho/wenhao-billing/backend/internal/payments"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/internal/projects"
	"github.com/wenhaokho/wenhao-billing/backend/internal/quotations"
	"github.com/wenhaokho/wenhao-billing/backend/internal/reports"
	"github.com/wenhaokho/wenhao-billing/backend/internal/stats"
	"github.com/wenhaokho/wenhao-billing/backend/internal/vendors"
	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

// features are what the features' Register functions take.
type features struct {
	Pool     *pgxpool.Pool
	Sessions platform.Sessions
	Mail     email.Sender
	Config   platform.Config
	Clock    platform.Clock
	// Logger is the API's logger (nil: slog's default).
	Logger *slog.Logger
	// FXClient is the FX provider's HTTP client (fx.NewClient).
	FXClient *http.Client
	// Services are the services the routes share with the MCP connector.
	// Required: register panics without them, so the routes can never build
	// a second set the connector does not share. wire builds them once;
	// callers with no connector use withServices.
	Services *services
}

// services are the feature services built once per process and shared by
// the API's routes and the MCP connector, so a payment the connector clears
// stamps and restores hosting exactly as one the routes clear, and
// trigger_recurring mails the same draft digest the recurring routes do.
type services struct {
	Payments payments.Service
	Notifier invoices.DraftNotifier
}

// newServices builds the shared services: payments with the real hosting
// restorer (Cloudflare from CLOUDFLARE_*), and the emailing draft notifier.
func newServices(cfg platform.Config, pool *pgxpool.Pool, logger *slog.Logger, mail email.Sender) services {
	return services{
		Payments: payments.Service{BaseCurrency: cfg.BaseCurrency, Hosting: hosting.Restorer{
			Pool: pool, Client: hosting.NewClient(cfg), Logger: logger,
		}},
		Notifier: invoices.EmailNotifier{
			Q: invoicesdb.New(pool), Sender: mail, AppBaseURL: cfg.AppBaseURL, Logger: logger,
		},
	}
}

// withServices is f with Services built from its own fields, for callers
// that serve no MCP connector (printOpenAPI, tests); serve uses wire.
func (f features) withServices() features {
	svc := newServices(f.Config, f.Pool, f.Logger, f.Mail)
	f.Services = &svc
	return f
}

// register adds every feature's operations. A new feature adds its line here.
// f.Services must be set (wire, or withServices).
func register(api huma.API, f features) {
	if f.Services == nil {
		panic("register: features.Services is nil; build them once with wire (or withServices) so the routes and the MCP connector share them")
	}
	svc := *f.Services
	auth.Register(api, f.Pool, f.Sessions, f.Mail, f.Config, f.Clock)
	accounts.Register(api, f.Pool)
	vendors.Register(api, f.Pool)
	projects.Register(api, f.Pool)
	items.Register(api, f.Pool)
	customers.Register(api, f.Pool)
	business.Register(api, f.Pool, f.Clock)
	fx.Register(api, f.Pool, f.Config, f.FXClient)
	invoices.Register(api, f.Pool, f.Clock, svc.Notifier)
	quotations.Register(api, f.Pool, f.Clock)
	quotations.RegisterDocuments(api, f.Pool, f.Clock, f.Mail)
	invoices.RegisterDocuments(api, f.Pool, f.Clock, f.Mail)
	bills.Register(api, f.Pool, f.Clock)
	payments.Register(api, f.Pool, svc.Payments, f.Config, f.Clock,
		payments.ReceiptDeps{Sender: f.Mail, Renderer: payments.PDFReceiptRenderer{DB: f.Pool}})
	reports.Register(api, f.Pool, f.Config, f.Clock)
	stats.Register(api, f.Pool, f.Config, f.Clock)
}

// wiring is what serve builds once: the routes' features and the MCP
// connector's deps, sharing one mail sender and one set of services.
type wiring struct {
	features features
	mcp      mcp.Deps
}

// wire builds serve's wiring. The MCP connector gets the routes' own
// payments.Service (the real hosting restorer) with AfterPayment set to its
// AfterCommit, the routes' draft notifier, and the routes' mail sender.
func wire(cfg platform.Config, pool *pgxpool.Pool, sessions platform.Sessions, clock platform.Clock, logger *slog.Logger) wiring {
	mail := pickSender(cfg, logger)
	svc := newServices(cfg, pool, logger, mail)
	return wiring{
		features: features{
			Pool: pool, Sessions: sessions, Mail: mail, Config: cfg, Clock: clock,
			Logger:   logger,
			FXClient: fx.NewClient(cfg),
			Services: &svc,
		},
		mcp: mcp.Deps{
			Pool: pool, Clock: clock, Cfg: cfg,
			Sender:       mail,
			Notifier:     svc.Notifier,
			Payments:     svc.Payments,
			AfterPayment: svc.Payments.AfterCommit,
		},
	}
}

// oauthRoot mounts the MCP connector's OAuth server and the /mcp transport
// at the root, only when MCP_ENABLED (nil mounts nothing, and its paths
// answer 404). d is the connector's deps (wire's), whose Pool and Clock the
// OAuth server uses too.
func oauthRoot(cfg platform.Config, sessions platform.Sessions, d mcp.Deps) func(chi.Router) {
	if !cfg.MCPEnabled {
		return nil
	}
	return func(r chi.Router) {
		oauth.Mount(r, oauth.Deps{
			Pool: d.Pool, PublicBaseURL: cfg.MCPPublicBaseURL, AppBaseURL: cfg.AppBaseURL,
			Sessions: sessions, Clock: d.Clock,
			Tokens: oauth.NewTokens(cfg.MCPTokenSecret, cfg.MCPAccessTokenTTL, cfg.MCPRefreshTokenTTL),
		})
		handler := mcp.NewHandler(d)
		r.Handle("/mcp", handler)
		r.Handle("/mcp/*", handler)
	}
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "healthcheck":
			os.Exit(healthcheck())
		case "openapi":
			os.Exit(printOpenAPI())
		case "adopt-baseline":
			if err := adoptBaseline(); err != nil {
				slog.Error("adopt-baseline failed", "err", err)
				os.Exit(1)
			}
			slog.Info("adopt-baseline: goose now owns the database at the baseline; the next serve applies the later migrations")
			os.Exit(0)
		case "set-password", "issue-session":
			if len(os.Args) != 3 {
				slog.Error("usage: api set-password|issue-session <email>")
				os.Exit(2)
			}
			if err := runCommand(os.Args[1], os.Args[2]); err != nil {
				slog.Error("command failed", "command", os.Args[1], "err", err)
				os.Exit(1)
			}
			os.Exit(0)
		}
	}
	if err := serve(); err != nil {
		slog.Error("api stopped", "err", err)
		os.Exit(1)
	}
}

// mailSender is the API's mail: logged, with bodies (live reset links)
// shown only in a local environment.
func mailSender(cfg platform.Config, logger *slog.Logger) email.LogSender {
	return email.LogSender{Logger: logger, ShowBodies: cfg.LocalHTTP()}
}

// pickSender is Resend when RESEND_API_KEY is set, else the logging sender
// (Python's send_email with no key and no SMTP host).
func pickSender(cfg platform.Config, logger *slog.Logger) email.Sender {
	if cfg.ResendAPIKey == "" {
		if !cfg.LocalHTTP() && logger != nil {
			logger.Warn("RESEND_API_KEY is empty: mail is logged, not sent", "environment", cfg.Environment)
		}
		return mailSender(cfg, logger)
	}
	return email.NewResendSender(email.ResendConfig{
		APIKey: cfg.ResendAPIKey, FromEmail: cfg.ResendFromEmail, FromName: cfg.ResendFromName,
		BaseURL: cfg.ResendBaseURL, ReplyTo: cfg.SMTPReplyTo, Timeout: cfg.ResendTimeout, Logger: logger,
	}, &http.Client{Timeout: cfg.ResendTimeout + time.Second})
}

func serve() error {
	cfg, err := platform.LoadConfig()
	if err != nil {
		return err
	}
	logger := observe.Logger(cfg.Environment)
	slog.SetDefault(logger)
	if _, err := observe.InitSentry(observe.SentryConfig{DSN: cfg.SentryDSN, Environment: cfg.SentryEnvironment, TracesSampleRate: cfg.SentryTracesSampleRate}); err != nil {
		return fmt.Errorf("sentry: %w", err)
	}
	defer observe.Flush(2 * time.Second)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := prepareDatabase(ctx, cfg.DatabaseURL); err != nil {
		return err
	}
	pool, err := db.Open(ctx, db.PoolConfig{URL: cfg.DatabaseURL, StatementTimeout: 30 * time.Second})
	if err != nil {
		return err
	}
	defer pool.Close()

	clock := platform.SystemClock{}
	sessions := platform.NewSessions(cfg)
	// One sender and one set of services for the whole process: the API's
	// routes and the MCP connector share them.
	w := wire(cfg, pool, sessions, clock, logger)
	handler, api := platform.NewServer(cfg, platform.Deps{Pool: pool, Sessions: sessions, Logger: logger, Clock: clock, Root: oauthRoot(cfg, sessions, w.mcp)})
	register(api, w.features)
	_, waitScheduler := startScheduler(ctx, pool, cfg, clock, logger, w.features.Mail)

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	errs := make(chan error, 1)
	go func() { errs <- srv.ListenAndServe() }()
	logger.Info("listening", "port", cfg.Port)
	select {
	case err := <-errs:
		stop()
		if waitScheduler != nil {
			waitScheduler()
		}
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil && !errors.Is(err, http.ErrServerClosed) {
		if waitScheduler != nil {
			waitScheduler()
		}
		return err
	}
	if waitScheduler != nil {
		waitScheduler()
	}
	return nil
}

// startScheduler starts the scheduled jobs, if they are on. The scheduler is
// off unless SCHEDULER_ENABLED says otherwise (Go change 8): the Python
// backend runs the jobs until the switch-over.
//
// It returns the jobs it started and a wait function that blocks until the
// scheduler's goroutine has stopped (nil when the scheduler is off). serve
// calls the wait function before it returns, and the database pool is
// closed (serve's defer) only after that, so a job running when the process
// stops finishes before its pool is closed under it.
func startScheduler(ctx context.Context, pool *pgxpool.Pool, cfg platform.Config, clock platform.Clock, logger *slog.Logger, mail email.Sender) ([]jobs.Job, func()) {
	if !cfg.SchedulerEnabled {
		logger.Info("scheduler is off (SCHEDULER_ENABLED)")
		return nil, nil
	}
	jobList := scheduledJobs(pool, cfg, logger, mail)
	wait := jobs.Start(ctx, pool, clock, func(err error) {
		logger.Error("scheduled job failed", "err", err)
	}, jobList...)
	logger.Info("scheduler started", "jobs", len(jobList))
	return jobList, wait
}

// scheduledJobs is the job list: the jobs' work is built from the
// configuration here (the hosting job's Cloudflare client from CLOUDFLARE_*).
// It is a variable so tests can replace the jobs' Run functions with no-ops.
var scheduledJobs = func(pool *pgxpool.Pool, cfg platform.Config, logger *slog.Logger, mail email.Sender) []jobs.Job {
	return []jobs.Job{
		jobs.RecurringScan(pool, invoices.EmailNotifier{
			Q: invoicesdb.New(pool), Sender: mail, AppBaseURL: cfg.AppBaseURL, Logger: logger,
		}),
		jobs.UsageLockScan(pool),
		jobs.HostingEnforcement(func(ctx context.Context, slot time.Time) error {
			_, err := hosting.Enforce(ctx, pool, hosting.NewClient(cfg), slot)
			return err
		}),
		jobs.FXRatesSync(pool, cfg, fx.NewClient(cfg)),
	}
}

// prepareDatabase is serve's database step: refuse a database goose does not
// own yet (migrations.CheckAdopted), then migrate. The check uses its own
// short-lived pool, closed before goose runs, so the server's pool is opened
// on the migrated schema and the check never shares a connection with goose.
func prepareDatabase(ctx context.Context, url string) error {
	pool, err := db.Open(ctx, db.PoolConfig{URL: url})
	if err != nil {
		return err
	}
	err = migrations.CheckAdopted(ctx, pool)
	pool.Close()
	if err != nil {
		return err
	}
	return db.Migrate(ctx, url, migrations.FS)
}

// adoptBaseline runs migrations.Adopt against DATABASE_URL. It never migrates:
// the next serve does that, once its own check passes.
func adoptBaseline() error {
	cfg, err := platform.LoadConfig()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	pool, err := db.Open(ctx, db.PoolConfig{URL: cfg.DatabaseURL})
	if err != nil {
		return err
	}
	defer pool.Close()
	return migrations.Adopt(ctx, pool)
}

func healthcheck() int {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8000"
	}
	client := http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/health") //nolint:noctx,gosec // a one-shot probe of this process, on loopback
	if err != nil {
		return 1
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}

// printOpenAPI builds the API with no database: routes are only described.
func printOpenAPI() int {
	_, api := platform.NewServer(platform.Config{Environment: "openapi"}, platform.Deps{})
	register(api, features{}.withServices())
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(api.OpenAPI()); err != nil {
		return 1
	}
	return 0
}

// runCommand runs set-password or issue-session against DATABASE_URL.
func runCommand(name, addr string) error {
	cfg, err := platform.LoadConfig()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	pool, err := db.Open(ctx, db.PoolConfig{URL: cfg.DatabaseURL})
	if err != nil {
		return err
	}
	defer pool.Close()
	if name == "set-password" {
		if err := setPasswordCmd(ctx, pool, addr, os.Stdin); err != nil {
			return err
		}
		slog.Info("set-password: password set; every session of the user is signed out", "email", addr)
		return nil
	}
	return issueSessionCmd(ctx, pool, platform.NewSessions(cfg), addr, os.Stdout)
}

// setPasswordCmd reads one line from stdin (a trailing newline, CRLF too, is
// not part of the password) and sets it as addr's password.
func setPasswordCmd(ctx context.Context, pool *pgxpool.Pool, addr string, stdin io.Reader) error {
	line, err := bufio.NewReader(stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return auth.SetPassword(ctx, pool, addr, strings.TrimRight(line, "\r\n"))
}

// issueSessionCmd prints a session token for the admin addr.
func issueSessionCmd(ctx context.Context, pool *pgxpool.Pool, s platform.Sessions, addr string, stdout io.Writer) error {
	token, err := auth.IssueSession(ctx, pool, s, addr)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, token)
	return err
}
