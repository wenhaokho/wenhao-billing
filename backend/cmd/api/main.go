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
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LOQATO/gokit/db"
	"github.com/LOQATO/gokit/observe"

	"github.com/wenhaokho/wenhao-billing/backend/internal/accounts"
	"github.com/wenhaokho/wenhao-billing/backend/internal/auth"
	"github.com/wenhaokho/wenhao-billing/backend/internal/business"
	"github.com/wenhaokho/wenhao-billing/backend/internal/customers"
	"github.com/wenhaokho/wenhao-billing/backend/internal/email"
	"github.com/wenhaokho/wenhao-billing/backend/internal/fx"
	"github.com/wenhaokho/wenhao-billing/backend/internal/items"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
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
	// FXClient is the FX provider's HTTP client (cfg.FXProviderTimeout).
	FXClient *http.Client
}

// register adds every feature's operations. A new feature adds its line here.
func register(api huma.API, f features) {
	auth.Register(api, f.Pool, f.Sessions, f.Mail, f.Config, f.Clock)
	accounts.Register(api, f.Pool)
	vendors.Register(api, f.Pool)
	items.Register(api, f.Pool)
	customers.Register(api, f.Pool)
	business.Register(api, f.Pool, f.Clock)
	fx.Register(api, f.Pool, f.Config, f.FXClient)
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
	handler, api := platform.NewServer(cfg, platform.Deps{Pool: pool, Sessions: sessions, Logger: logger, Clock: clock})
	register(api, features{
		Pool: pool, Sessions: sessions, Mail: mailSender(cfg, logger), Config: cfg, Clock: clock,
		FXClient: &http.Client{Timeout: cfg.FXProviderTimeout},
	})

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
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdown); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
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
	register(api, features{})
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
