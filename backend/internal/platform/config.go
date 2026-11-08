package platform

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/caarlos0/env/v11"
)

// Config is the environment the API runs with, under the variable names the
// Python backend used. A missing required variable stops startup naming it.
type Config struct {
	Environment           string
	DatabaseURL           string // always "postgresql://": pgx does not read Python's driver prefix
	SessionSecret         string
	SessionMaxAge         time.Duration
	SessionRememberMaxAge time.Duration
	CORSOrigins           []string
	AppBaseURL            string
	SentryDSN             string
	SentryEnvironment     string
	// A sampling ratio, not money.
	SentryTracesSampleRate float64 //nolint:forbidigo // not money: Sentry's sample rate
	Port                   int
}

// rawConfig is the environment as written; Config holds it parsed.
type rawConfig struct {
	Environment            string  `env:"ENVIRONMENT" envDefault:"production"`
	DatabaseURL            string  `env:"DATABASE_URL,required"`
	SessionSecret          string  `env:"SESSION_SECRET,required"`
	SessionMaxAgeSeconds   int     `env:"SESSION_MAX_AGE_SECONDS" envDefault:"28800"`
	SessionRememberSeconds int     `env:"SESSION_REMEMBER_MAX_AGE_SECONDS" envDefault:"2592000"`
	CORSOrigins            string  `env:"CORS_ORIGINS"`
	AppBaseURL             string  `env:"APP_BASE_URL" envDefault:"http://localhost:5173"`
	SentryDSN              string  `env:"SENTRY_DSN"`
	SentryEnvironment      string  `env:"SENTRY_ENVIRONMENT" envDefault:"development"`
	SentryTracesSampleRate float64 `env:"SENTRY_TRACES_SAMPLE_RATE"` //nolint:forbidigo // not money: Sentry's sample rate
	Port                   int     `env:"PORT" envDefault:"8000"`
}

// LoadConfig reads the process environment.
func LoadConfig() (Config, error) {
	var r rawConfig
	if err := env.Parse(&r); err != nil {
		return Config{}, err
	}
	_, set := os.LookupEnv("CORS_ORIGINS")
	return r.build(set)
}

// ParseConfig reads vars instead of the process environment (for tests).
func ParseConfig(vars map[string]string) (Config, error) {
	var r rawConfig
	if err := env.ParseWithOptions(&r, env.Options{Environment: vars}); err != nil {
		return Config{}, err
	}
	_, set := vars["CORS_ORIGINS"]
	return r.build(set)
}

// Production reports whether the API runs in production.
func (c Config) Production() bool { return c.Environment == "production" }

// build checks and converts r; corsSet is whether CORS_ORIGINS was set at all,
// because blank means "no origins" while unset means the local default.
func (r rawConfig) build(corsSet bool) (Config, error) {
	// A short secret signs sessions anyone can forge.
	if len(r.SessionSecret) < 32 {
		return Config{}, fmt.Errorf("SESSION_SECRET must be at least 32 bytes")
	}
	origins := []string{"http://localhost:5173"}
	if corsSet {
		var err error
		if origins, err = parseOrigins(r.CORSOrigins); err != nil {
			return Config{}, err
		}
	}
	return Config{
		Environment:            r.Environment,
		DatabaseURL:            pgxURL(r.DatabaseURL),
		SessionSecret:          r.SessionSecret,
		SessionMaxAge:          time.Duration(r.SessionMaxAgeSeconds) * time.Second,
		SessionRememberMaxAge:  time.Duration(r.SessionRememberSeconds) * time.Second,
		CORSOrigins:            origins,
		AppBaseURL:             r.AppBaseURL,
		SentryDSN:              r.SentryDSN,
		SentryEnvironment:      r.SentryEnvironment,
		SentryTracesSampleRate: r.SentryTracesSampleRate,
		Port:                   r.Port,
	}, nil
}

// pgxURL rewrites the schemes Python deployments use to the one pgx reads.
func pgxURL(u string) string {
	for _, prefix := range []string{"postgresql+psycopg://", "postgres://"} {
		if rest, ok := strings.CutPrefix(u, prefix); ok {
			return "postgresql://" + rest
		}
	}
	return u
}

// parseOrigins accepts Python's JSON list or a comma-separated list.
func parseOrigins(s string) ([]string, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "[") {
		var out []string
		if err := json.Unmarshal([]byte(s), &out); err != nil {
			return nil, fmt.Errorf("CORS_ORIGINS is not a JSON list: %w", err)
		}
		var kept []string
		for _, o := range out {
			if o = strings.TrimSpace(o); o != "" {
				kept = append(kept, o)
			}
		}
		return kept, nil
	}
	var out []string
	for _, o := range strings.Split(s, ",") {
		if o = strings.TrimSpace(o); o != "" {
			out = append(out, o)
		}
	}
	return out, nil
}
