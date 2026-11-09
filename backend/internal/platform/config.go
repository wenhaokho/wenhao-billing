package platform

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"slices"
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
	BaseCurrency           string        // BASE_CURRENCY: the ledger's functional currency, 3 upper-case letters
	FXProviderBaseURL      string        // FX_PROVIDER_BASE_URL
	FXProviderTimeout      time.Duration // FX_PROVIDER_TIMEOUT_SECONDS
	FXSyncCurrencies       []string      // FX_SYNC_CURRENCIES: foreign currencies each sync pulls a base rate for
	WebhookHMACSecret      string        // WEBHOOK_HMAC_SECRET: keys the HMAC of the payment webhook's body
	EmailWebhookToken      string        // EMAIL_WEBHOOK_TOKEN: the token the inbound-email route requires
	MCPEnabled             bool          // MCP_ENABLED: mounts the MCP connector and its OAuth server
	MCPConfirmSecret       string        // MCP_CONFIRM_SECRET: keys the HMAC of confirm tokens
	MCPTokenSecret         string        // MCP_TOKEN_SECRET: signs OAuth access, refresh and code tokens
	MCPAccessTokenTTL      time.Duration // MCP_ACCESS_TOKEN_TTL_SECONDS
	MCPRefreshTokenTTL     time.Duration // MCP_REFRESH_TOKEN_TTL_SECONDS
	MCPPublicBaseURL       string        // MCP_PUBLIC_BASE_URL: the origin the connector is served from
}

// The development values of the two intake secrets, Python's defaults. They
// are public, so a production API refuses them.
const (
	devWebhookHMACSecret = "dev-webhook-secret-change-me"     //nolint:gosec // the published development default, which production refuses
	devEmailWebhookToken = "dev-email-token-change-me"        //nolint:gosec // the published development default, which production refuses
	devMCPConfirmSecret  = "dev-mcp-confirm-secret-change-me" //nolint:gosec // the published development default, which production refuses
	devMCPTokenSecret    = "dev-mcp-token-secret-change-me"   //nolint:gosec // the published development default, which production refuses

	minMCPSecretBytes = 16 // Python's min_length on both MCP secrets
)

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
	BaseCurrency           string  `env:"BASE_CURRENCY"`
	FXProviderBaseURL      string  `env:"FX_PROVIDER_BASE_URL" envDefault:"https://api.frankfurter.dev/v1"`
	FXProviderSeconds      float64 `env:"FX_PROVIDER_TIMEOUT_SECONDS" envDefault:"15"` //nolint:forbidigo // not money: a timeout in seconds
	FXSyncCurrencies       string  `env:"FX_SYNC_CURRENCIES" envDefault:"USD,SGD"`
	WebhookHMACSecret      string  `env:"WEBHOOK_HMAC_SECRET" envDefault:"dev-webhook-secret-change-me"`
	EmailWebhookToken      string  `env:"EMAIL_WEBHOOK_TOKEN" envDefault:"dev-email-token-change-me"`
	MCPEnabled             bool    `env:"MCP_ENABLED" envDefault:"true"`
	MCPConfirmSecret       string  `env:"MCP_CONFIRM_SECRET" envDefault:"dev-mcp-confirm-secret-change-me"`
	MCPTokenSecret         string  `env:"MCP_TOKEN_SECRET" envDefault:"dev-mcp-token-secret-change-me"`
	MCPAccessTokenSeconds  int     `env:"MCP_ACCESS_TOKEN_TTL_SECONDS" envDefault:"3600"`
	MCPRefreshTokenSeconds int     `env:"MCP_REFRESH_TOKEN_TTL_SECONDS" envDefault:"2592000"`
	MCPPublicBaseURL       string  `env:"MCP_PUBLIC_BASE_URL" envDefault:"http://localhost:8000"`
}

// LoadConfig reads the process environment.
func LoadConfig() (Config, error) {
	var r rawConfig
	if err := env.Parse(&r); err != nil {
		return Config{}, err
	}
	return r.build(func(name string) bool { _, ok := os.LookupEnv(name); return ok })
}

// ParseConfig reads vars instead of the process environment (for tests).
func ParseConfig(vars map[string]string) (Config, error) {
	var r rawConfig
	if err := env.ParseWithOptions(&r, env.Options{Environment: vars}); err != nil {
		return Config{}, err
	}
	return r.build(func(name string) bool { _, ok := vars[name]; return ok })
}

// Production reports whether the API runs in production.
func (c Config) Production() bool { return c.Environment == "production" }

// LocalHTTP reports whether the API runs on local plain HTTP, the only case
// in which the session cookie is not Secure. Anything but exactly
// "development" or "test" (a typo such as "prod" included) keeps it Secure.
func (c Config) LocalHTTP() bool { return c.Environment == "development" || c.Environment == "test" }

// build checks and converts r; isSet says whether a variable was set at all:
// a blank CORS_ORIGINS means "no origins" while unset means the local default,
// and a blank BASE_CURRENCY is refused (Python's min_length=3), not defaulted.
func (r rawConfig) build(isSet func(name string) bool) (Config, error) {
	// A short secret signs sessions anyone can forge.
	if len(r.SessionSecret) < 32 {
		return Config{}, fmt.Errorf("SESSION_SECRET must be at least 32 bytes")
	}
	origins := []string{"http://localhost:5173"}
	if isSet("CORS_ORIGINS") {
		var err error
		if origins, err = parseList("CORS_ORIGINS", r.CORSOrigins); err != nil {
			return Config{}, err
		}
	}
	base := "IDR"
	if isSet("BASE_CURRENCY") {
		base = strings.ToUpper(strings.TrimSpace(r.BaseCurrency))
	}
	if !isCurrencyCode(base) {
		return Config{}, fmt.Errorf("BASE_CURRENCY must be 3 letters, got %q", r.BaseCurrency)
	}
	syncCurrencies, err := parseList("FX_SYNC_CURRENCIES", r.FXSyncCurrencies)
	if err != nil {
		return Config{}, err
	}
	for i, c := range syncCurrencies {
		if syncCurrencies[i] = strings.ToUpper(c); !isCurrencyCode(syncCurrencies[i]) {
			return Config{}, fmt.Errorf("FX_SYNC_CURRENCIES entry %q must be 3 letters", c)
		}
	}
	if r.FXProviderSeconds <= 0 { // 0 would mean no timeout at all
		return Config{}, fmt.Errorf("FX_PROVIDER_TIMEOUT_SECONDS must be greater than 0")
	}
	// The defaults are published in the source: anyone could sign a payment, so
	// only a local environment (as Config.LocalHTTP) may keep them.
	if r.Environment != "development" && r.Environment != "test" {
		if r.WebhookHMACSecret == "" || r.WebhookHMACSecret == devWebhookHMACSecret {
			return Config{}, fmt.Errorf("WEBHOOK_HMAC_SECRET must be set to a secret of your own outside development and test")
		}
		if r.EmailWebhookToken == "" || r.EmailWebhookToken == devEmailWebhookToken {
			return Config{}, fmt.Errorf("EMAIL_WEBHOOK_TOKEN must be set to a secret of your own outside development and test")
		}
	}
	// Short MCP secrets sign tokens anyone can forge, in every environment
	// (Python's min_length); the published defaults only outside production.
	for _, sec := range []struct{ name, val, dev string }{
		{"MCP_CONFIRM_SECRET", r.MCPConfirmSecret, devMCPConfirmSecret},
		{"MCP_TOKEN_SECRET", r.MCPTokenSecret, devMCPTokenSecret},
	} {
		if len(sec.val) < minMCPSecretBytes {
			return Config{}, fmt.Errorf("%s must be at least %d bytes", sec.name, minMCPSecretBytes)
		}
		if sec.val == sec.dev && r.Environment != "development" && r.Environment != "test" {
			return Config{}, fmt.Errorf("%s must be set to a secret of your own outside development and test", sec.name)
		}
	}
	for _, ttl := range []struct {
		name    string
		seconds int
	}{{"MCP_ACCESS_TOKEN_TTL_SECONDS", r.MCPAccessTokenSeconds}, {"MCP_REFRESH_TOKEN_TTL_SECONDS", r.MCPRefreshTokenSeconds}} {
		if ttl.seconds <= 0 { // zero would mint tokens that are already expired
			return Config{}, fmt.Errorf("%s must be greater than 0", ttl.name)
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
		BaseCurrency:           base,
		FXProviderBaseURL:      r.FXProviderBaseURL,
		FXProviderTimeout:      time.Duration(r.FXProviderSeconds * float64(time.Second)), //nolint:forbidigo // not money: a timeout
		FXSyncCurrencies:       syncCurrencies,
		WebhookHMACSecret:      r.WebhookHMACSecret,
		EmailWebhookToken:      r.EmailWebhookToken,
		MCPEnabled:             r.MCPEnabled,
		MCPConfirmSecret:       r.MCPConfirmSecret,
		MCPTokenSecret:         r.MCPTokenSecret,
		MCPAccessTokenTTL:      time.Duration(r.MCPAccessTokenSeconds) * time.Second,
		MCPRefreshTokenTTL:     time.Duration(r.MCPRefreshTokenSeconds) * time.Second,
		MCPPublicBaseURL:       r.MCPPublicBaseURL,
	}, nil
}

// TransportAllowlist derives the Host values and the origin the /mcp/ guard
// accepts from the public base URL (Python's _transport_allowlist): the bare
// hostname and host:port (one entry when they are equal), and scheme://host:port.
func TransportAllowlist(baseURL string) (hosts, origins []string) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, nil
	}
	// Lower-cased, as Python's urlparse hostname is, so a mixed-case base URL
	// still matches the lower-case Host header a client sends.
	host := strings.ToLower(u.Host)
	for _, h := range []string{strings.ToLower(u.Hostname()), host} {
		if h != "" && !slices.Contains(hosts, h) {
			hosts = append(hosts, h)
		}
	}
	if u.Scheme != "" && u.Host != "" {
		origins = []string{u.Scheme + "://" + host}
	}
	return hosts, origins
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

// isCurrencyCode reports whether s is three upper-case ASCII letters.
func isCurrencyCode(s string) bool {
	if len(s) != 3 {
		return false
	}
	for _, c := range s {
		if c < 'A' || c > 'Z' {
			return false
		}
	}
	return true
}

// parseList accepts Python's JSON list or a comma-separated list; name is the
// variable, for the error.
func parseList(name, s string) ([]string, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "[") {
		var out []string
		if err := json.Unmarshal([]byte(s), &out); err != nil {
			return nil, fmt.Errorf("%s is not a JSON list: %w", name, err)
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
