// Command api serves the HTTP API.
//
//	api                 serve (migrations run first, under a lock; refuses a
//	                    database that still has only Alembic's history)
//	api adopt-baseline  hand the Alembic-built database to goose without
//	                    running the baseline (once, before the first serve)
//	api healthcheck  exit 0 when the local server answers /health (for the image)
//	api openapi      print the OpenAPI document (for the frontend's types)
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/LOQATO/gokit/auth"
	"github.com/LOQATO/gokit/db"
	"github.com/LOQATO/gokit/observe"

	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

// register adds every feature's operations. A new feature adds its line here.
func register(_ huma.API, _ *pgxpool.Pool, _ auth.Tokens) {
	// Each feature adds its Register call here.
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
		}
	}
	if err := serve(); err != nil {
		slog.Error("api stopped", "err", err)
		os.Exit(1)
	}
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

	tokens := auth.Tokens{Secret: []byte(cfg.SessionSecret), AccessTTL: 15 * time.Minute, Leeway: 10 * time.Second}
	handler, api := platform.NewServer(cfg, platform.Deps{Pool: pool, Tokens: tokens, Logger: logger})
	register(api, pool, tokens)

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
	register(api, nil, auth.Tokens{})
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(api.OpenAPI()); err != nil {
		return 1
	}
	return 0
}
