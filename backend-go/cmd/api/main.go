package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/wenhaokho/wenhao-billing/backend/internal/email"
	"log"
	"math"
	"net"
	"net/http"
	"net/mail"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) > 1 {
		return passwordCommand(os.Args[1:])
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	connectCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	pool, err := platform.OpenDB(connectCtx, os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := migrations.VerifyCurrent(connectCtx, os.Getenv("DATABASE_URL")); err != nil {
		return err
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8000"
	}
	origins, err := parseOrigins(os.LookupEnv("CORS_ORIGINS"))
	if err != nil {
		return err
	}

	sessionCfg, err := sessionConfigFromEnv()
	if err != nil {
		return err
	}
	sessionCfg.BaseURL = os.Getenv("APP_BASE_URL")
	if base, set := os.LookupEnv("BASE_CURRENCY"); set {
		if base == "" {
			return errors.New("invalid BASE_CURRENCY")
		}
		sessionCfg.BaseCurrency = base
	}
	sessionCfg.FXProviderURL = os.Getenv("FX_PROVIDER_BASE_URL")
	if raw, ok := os.LookupEnv("FX_SYNC_CURRENCIES"); ok {
		if err := json.Unmarshal([]byte(raw), &sessionCfg.FXSyncCurrencies); err != nil || sessionCfg.FXSyncCurrencies == nil {
			return errors.New("invalid FX_SYNC_CURRENCIES")
		}
	}
	if raw, ok := os.LookupEnv("FX_PROVIDER_TIMEOUT_SECONDS"); ok {
		seconds, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 || seconds > 3600 {
			return errors.New("invalid FX_PROVIDER_TIMEOUT_SECONDS")
		}
		sessionCfg.FXProviderTimeout = time.Duration(seconds * float64(time.Second))
	}
	sessionCfg.Sender, err = emailSenderFromEnv()
	if err != nil {
		return err
	}
	schedulerEnabled, err := schedulerEnabledFromEnv()
	if err != nil {
		return err
	}
	var jobs []workerJob
	var hostingProvider platform.HostingEnforcementProvider
	if schedulerEnabled {
		if os.Getenv("RESEND_API_KEY") != "" || os.Getenv("SMTP_HOST") != "" {
			jobs = append(jobs, func(ctx context.Context) error {
				return platform.DeliverRecurringDrafts(ctx, pool, loggedSender{sessionCfg.Sender, log.Default()}, sessionCfg.BaseURL)
			})
		} else {
			log.Print("recurring delivery worker unavailable: email is not configured")
		}
		if token := os.Getenv("CLOUDFLARE_API_TOKEN"); token != "" {
			timeout := 15 * time.Second
			if raw := os.Getenv("CLOUDFLARE_TIMEOUT_SECONDS"); raw != "" {
				seconds, e := strconv.ParseFloat(raw, 64)
				if e != nil || math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 || seconds > 60 {
					return errors.New("invalid CLOUDFLARE_TIMEOUT_SECONDS")
				}
				timeout = time.Duration(seconds * float64(time.Second))
			}
			provider, e := platform.NewCloudflareRestoreProvider(envDefault("CLOUDFLARE_API_BASE_URL", "https://api.cloudflare.com/client/v4"), token, timeout, nil)
			if e != nil {
				return errors.New("invalid Cloudflare configuration")
			}
			hostingProvider = provider
			jobs = append(jobs, func(ctx context.Context) error {
				return platform.ConsumeHostingRestoreIntents(ctx, pool, loggedHostingProvider{provider, log.Default()})
			})
		} else {
			log.Print("hosting restore and enforcement workers unavailable: Cloudflare is not configured")
		}
	}
	handler, err := platform.NewHandlerWithSessions(pool, origins, sessionCfg)
	if err != nil {
		return err
	}
	server := &http.Server{Addr: ":" + port, Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return err
	}
	workerCtx, stopWorkers := context.WithCancel(ctx)
	workers := startWorkers(workerCtx, schedulerEnabled, 30*time.Second, time.Minute, log.Default(), jobs...)
	daily := startDailyWorker(workerCtx, schedulerEnabled, time.Now, dailyTimer, log.Default(), func(ctx context.Context) error {
		count, err := platform.ScanRecurring(ctx, pool, time.Now())
		if err == nil {
			log.Printf("recurring scan finished: %d", count)
		}
		return err
	})
	if schedulerEnabled {
		log.Print("usage cutoff worker unavailable: usage meter is not configured")
	}
	usageDaily := startScheduledDailyWorker(workerCtx, schedulerEnabled, time.Now, dailyTimer, log.Default(), nextUsageScan, "usage cutoff scan", usageScanJob(log.Default(), func(ctx context.Context) (int, error) {
		return platform.ScanUsageCutoff(ctx, pool, time.Now(), nil)
	}))
	var hostingJob workerJob
	if hostingProvider != nil {
		hostingJob = func(ctx context.Context) error {
			counts, err := platform.RunHostingEnforcement(ctx, pool, hostingProvider, time.Now())
			if errors.Is(err, platform.ErrHostingEnforcementAlreadyRunning) {
				log.Print("hosting enforcement skipped: already running")
				return nil
			}
			if err == nil {
				log.Printf("hosting enforcement finished: suspended=%d restored=%d errors=%d", counts.Suspended, counts.Restored, counts.Errors)
			}
			return err
		}
	}
	hostingDaily := startScheduledDailyWorker(workerCtx, schedulerEnabled, time.Now, dailyTimer, log.Default(), nextHostingScan, "hosting enforcement", hostingJob)
	fxWeekly := startScheduledDailyWorker(workerCtx, schedulerEnabled, time.Now, dailyTimer, log.Default(), nextFXSync, "job=fx-rates-weekly-sync", weeklyFXJob(log.Default(), time.Minute, func(ctx context.Context) error {
		return platform.RunWeeklyFXSync(ctx, pool, sessionCfg)
	}))
	serveErr := serve(ctx, server, listener)
	stopWorkers()
	workers.Wait()
	daily.Wait()
	usageDaily.Wait()
	hostingDaily.Wait()
	fxWeekly.Wait()
	return serveErr
}

func serve(ctx context.Context, server *http.Server, listener net.Listener) error {
	errs := make(chan error, 1)
	go func() { errs <- server.Serve(listener) }()
	select {
	case err := <-errs:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	}
}

func parseOrigins(value string, set bool) ([]string, error) {
	if !set {
		return []string{"http://localhost:5173"}, nil
	}
	if strings.HasPrefix(strings.TrimSpace(value), "[") {
		var origins []string
		if json.Unmarshal([]byte(value), &origins) != nil {
			return nil, fmt.Errorf("invalid CORS_ORIGINS configuration")
		}
		return origins, nil
	}
	var origins []string
	for _, origin := range strings.Split(value, ",") {
		origins = append(origins, strings.TrimSpace(origin))
	}
	return origins, nil
}

func sessionConfigFromEnv() (platform.SessionConfig, error) {
	c := platform.SessionConfig{Secret: []byte(os.Getenv("SESSION_SIGNING_SECRET")), Path: os.Getenv("SESSION_COOKIE_PATH"), SameSite: http.SameSiteLaxMode, Secure: true}
	if c.Path == "" {
		c.Path = "/"
	}
	switch os.Getenv("SESSION_COOKIE_SECURE") {
	case "", "true":
	case "false":
		c.Secure = false
	default:
		return c, errors.New("invalid SESSION_COOKIE_SECURE")
	}
	switch strings.ToLower(os.Getenv("SESSION_COOKIE_SAMESITE")) {
	case "", "lax":
	case "strict":
		c.SameSite = http.SameSiteStrictMode
	case "none":
		c.SameSite = http.SameSiteNoneMode
	default:
		return c, errors.New("invalid SESSION_COOKIE_SAMESITE")
	}
	for _, key := range []string{"MCP_SECRET", "MCP_JWT_SECRET", "MCP_TOKEN_SECRET", "MCP_CONFIRM_SECRET"} {
		if v := os.Getenv(key); v != "" && v == string(c.Secret) {
			return c, errors.New("session signing secret must differ from MCP secrets")
		}
	}
	return c, nil
}

func envDefault(k, fallback string) string {
	if v, ok := os.LookupEnv(k); ok {
		return v
	}
	return fallback
}

func emailSenderFromEnv() (email.Sender, error) {
	var err error
	mailCfg := email.Config{ResendKey: os.Getenv("RESEND_API_KEY"), ResendURL: os.Getenv("RESEND_BASE_URL"), SMTPHost: os.Getenv("SMTP_HOST"), SMTPUsername: os.Getenv("SMTP_USERNAME"), SMTPPassword: os.Getenv("SMTP_PASSWORD"), ReplyTo: envDefault("SMTP_REPLY_TO", "wenhao.kho@gmail.com")}
	mailCfg.ResendFrom = (&mail.Address{Name: envDefault("RESEND_FROM_NAME", "Wenhao Dev Billing"), Address: envDefault("RESEND_FROM_EMAIL", "noreply@wenhao.id")}).String()
	mailCfg.SMTPFrom = (&mail.Address{Name: envDefault("SMTP_FROM_NAME", "wenhao-billing"), Address: envDefault("SMTP_FROM_EMAIL", "noreply@wenhao.id")}).String()
	if mailCfg.ResendKey == "" && mailCfg.SMTPHost != "" {
		mailCfg.SMTPPort = 587
		mailCfg.SMTPUseTLS = true
		if v := os.Getenv("SMTP_PORT"); v != "" {
			mailCfg.SMTPPort, err = strconv.Atoi(v)
			if err != nil {
				return nil, errors.New("invalid SMTP_PORT")
			}
		}
		if v := os.Getenv("SMTP_USE_TLS"); v != "" {
			mailCfg.SMTPUseTLS, err = strconv.ParseBool(v)
			if err != nil {
				return nil, errors.New("invalid SMTP_USE_TLS")
			}
		}
	}
	if v := os.Getenv("RESEND_TIMEOUT_SECONDS"); v != "" {
		n, e := strconv.ParseFloat(v, 64)
		if e != nil || n <= 0 || n > 3600 {
			return nil, errors.New("invalid RESEND_TIMEOUT_SECONDS")
		}
		mailCfg.Timeout = time.Duration(n * float64(time.Second))
	}
	sender, err := email.New(mailCfg)
	if err != nil {
		return nil, errors.New("invalid email configuration")
	}
	return sender, nil
}
