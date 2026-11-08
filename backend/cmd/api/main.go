// Command api serves the HTTP API.
//
//	api              serve (migrations run first, under a lock)
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

	if err := db.Migrate(ctx, cfg.DatabaseURL, migrations.FS); err != nil {
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
