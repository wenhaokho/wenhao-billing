package platform

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

const configSecret = "0123456789abcdef0123456789abcdef" // 32 bytes

func baseVars(extra map[string]string) map[string]string {
	v := map[string]string{
		"DATABASE_URL": "postgresql://u:p@h:5432/d", "SESSION_SECRET": configSecret,
		"WEBHOOK_HMAC_SECRET": "a-real-webhook-secret", "EMAIL_WEBHOOK_TOKEN": "a-real-email-token",
		"MCP_CONFIRM_SECRET": "x" + configSecret, "MCP_TOKEN_SECRET": "y" + configSecret,
	}
	for k, val := range extra {
		v[k] = val
	}
	return v
}

func TestConfigReadsJSONCorsOrigins(t *testing.T) {
	t.Parallel()
	cfg, err := ParseConfig(baseVars(map[string]string{"CORS_ORIGINS": `["https://billing.wenhao.id"]`}))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"https://billing.wenhao.id"}; !reflect.DeepEqual(cfg.CORSOrigins, want) {
		t.Errorf("CORSOrigins = %v, want %v", cfg.CORSOrigins, want)
	}
}

func TestConfigReadsCommaCorsOrigins(t *testing.T) {
	t.Parallel()
	cfg, err := ParseConfig(baseVars(map[string]string{"CORS_ORIGINS": "http://a.test, http://b.test"}))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"http://a.test", "http://b.test"}; !reflect.DeepEqual(cfg.CORSOrigins, want) {
		t.Errorf("CORSOrigins = %v, want %v", cfg.CORSOrigins, want)
	}
}

func TestConfigRewritesPsycopgURL(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"postgresql+psycopg://u:p@h:5432/d": "postgresql://u:p@h:5432/d",
		"postgres://u:p@h:5432/d":           "postgresql://u:p@h:5432/d",
		"postgresql://u:p@h:5432/d":         "postgresql://u:p@h:5432/d",
	} {
		cfg, err := ParseConfig(baseVars(map[string]string{"DATABASE_URL": in}))
		if err != nil {
			t.Fatal(err)
		}
		if cfg.DatabaseURL != want {
			t.Errorf("%s -> %s, want %s", in, cfg.DatabaseURL, want)
		}
	}
}

func TestConfigRefusesShortSessionSecret(t *testing.T) {
	t.Parallel()
	_, err := ParseConfig(baseVars(map[string]string{"SESSION_SECRET": configSecret[:31]}))
	if err == nil || !strings.Contains(err.Error(), "SESSION_SECRET") {
		t.Errorf("err = %v, want one naming SESSION_SECRET", err)
	}
}

func TestConfigRequiresDatabaseURL(t *testing.T) {
	t.Parallel()
	_, err := ParseConfig(map[string]string{"SESSION_SECRET": configSecret})
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Errorf("err = %v, want one naming DATABASE_URL", err)
	}
}

func TestConfigDefaults(t *testing.T) {
	t.Parallel()
	cfg, err := ParseConfig(baseVars(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SessionMaxAge != 8*time.Hour || cfg.SessionRememberMaxAge != 30*24*time.Hour || cfg.Port != 8000 {
		t.Errorf("cfg = %+v", cfg)
	}
	if cfg.Environment != "production" {
		t.Errorf("Environment = %q", cfg.Environment)
	}
	if want := []string{"http://localhost:5173"}; !reflect.DeepEqual(cfg.CORSOrigins, want) {
		t.Errorf("CORSOrigins = %v", cfg.CORSOrigins)
	}
	if cfg.AppBaseURL != "http://localhost:5173" || cfg.SentryEnvironment != "development" || cfg.SentryTracesSampleRate != 0 {
		t.Errorf("cfg = %+v", cfg)
	}
}

func TestConfigDefaultsToProduction(t *testing.T) {
	t.Parallel()
	cfg, err := ParseConfig(baseVars(nil))
	if err != nil || !cfg.Production() {
		t.Errorf("Production() = false without ENVIRONMENT (err %v)", err)
	}
	cfg, err = ParseConfig(baseVars(map[string]string{"ENVIRONMENT": "development"}))
	if err != nil || cfg.Production() {
		t.Errorf("ENVIRONMENT=development counted as production (err %v)", err)
	}
}

func TestConfigEmptyCorsOriginsAllowsNone(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"", "  ", "[]", `["", " "]`} {
		cfg, err := ParseConfig(baseVars(map[string]string{"CORS_ORIGINS": v}))
		if err != nil || len(cfg.CORSOrigins) != 0 {
			t.Errorf("CORS_ORIGINS=%q -> %v, %v; want none", v, cfg.CORSOrigins, err)
		}
	}
}

func TestConfigMalformedJSONCorsOriginsErrors(t *testing.T) {
	t.Parallel()
	_, err := ParseConfig(baseVars(map[string]string{"CORS_ORIGINS": `["https://a"`}))
	if err == nil || !strings.Contains(err.Error(), "CORS_ORIGINS") {
		t.Errorf("err = %v, want one naming CORS_ORIGINS", err)
	}
}

func TestConfigFXDefaults(t *testing.T) {
	t.Parallel()
	cfg, err := ParseConfig(baseVars(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BaseCurrency != "IDR" || cfg.FXProviderBaseURL != "https://api.frankfurter.dev/v1" ||
		cfg.FXProviderTimeout != 15*time.Second || !reflect.DeepEqual(cfg.FXSyncCurrencies, []string{"USD", "SGD"}) {
		t.Errorf("defaults: %q %q %v %v", cfg.BaseCurrency, cfg.FXProviderBaseURL, cfg.FXProviderTimeout, cfg.FXSyncCurrencies)
	}
}

func TestConfigReadsJSONFXSyncCurrencies(t *testing.T) {
	t.Parallel()
	cfg, err := ParseConfig(baseVars(map[string]string{"FX_SYNC_CURRENCIES": `["usd","eur"]`}))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"USD", "EUR"}; !reflect.DeepEqual(cfg.FXSyncCurrencies, want) {
		t.Errorf("FXSyncCurrencies = %v, want %v", cfg.FXSyncCurrencies, want)
	}
	cfg, err = ParseConfig(baseVars(map[string]string{"FX_SYNC_CURRENCIES": "usd, eur", "BASE_CURRENCY": "sgd", "FX_PROVIDER_TIMEOUT_SECONDS": "2.5"}))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"USD", "EUR"}; !reflect.DeepEqual(cfg.FXSyncCurrencies, want) || cfg.BaseCurrency != "SGD" || cfg.FXProviderTimeout != 2500*time.Millisecond {
		t.Errorf("comma form: %v %q %v", cfg.FXSyncCurrencies, cfg.BaseCurrency, cfg.FXProviderTimeout)
	}
}

func TestConfigRefusesBadBaseCurrency(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"RUPIAH", "ID", "12R", ""} {
		_, err := ParseConfig(baseVars(map[string]string{"BASE_CURRENCY": bad}))
		if err == nil || !strings.Contains(err.Error(), "BASE_CURRENCY") {
			t.Errorf("%q: err = %v, want one naming BASE_CURRENCY", bad, err)
		}
	}
}

// 0 would mean no timeout at all, so it is refused with the negatives.
func TestConfigRefusesNonPositiveFXTimeout(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"0", "-1", "-0.5"} {
		_, err := ParseConfig(baseVars(map[string]string{"FX_PROVIDER_TIMEOUT_SECONDS": bad}))
		if err == nil || !strings.Contains(err.Error(), "FX_PROVIDER_TIMEOUT_SECONDS") {
			t.Errorf("%q: err = %v, want one naming FX_PROVIDER_TIMEOUT_SECONDS", bad, err)
		}
	}
}

func TestConfigRefusesBadFXSyncCurrency(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"US", `["usd","eurx"]`, "usd,12"} {
		_, err := ParseConfig(baseVars(map[string]string{"FX_SYNC_CURRENCIES": bad}))
		if err == nil || !strings.Contains(err.Error(), "FX_SYNC_CURRENCIES") {
			t.Errorf("%q: err = %v, want one naming FX_SYNC_CURRENCIES", bad, err)
		}
	}
}

func TestProductionRefusesDefaultIntakeSecrets(t *testing.T) {
	t.Parallel()
	const devSecret, devToken = "dev-webhook-secret-change-me", "dev-email-token-change-me"
	for name, tc := range map[string]struct {
		vars map[string]string
		want string // the variable the error names; "" accepts
	}{
		"default secret":       {map[string]string{"WEBHOOK_HMAC_SECRET": devSecret}, "WEBHOOK_HMAC_SECRET"},
		"empty secret":         {map[string]string{"WEBHOOK_HMAC_SECRET": ""}, "WEBHOOK_HMAC_SECRET"},
		"default token":        {map[string]string{"EMAIL_WEBHOOK_TOKEN": devToken}, "EMAIL_WEBHOOK_TOKEN"},
		"empty token":          {map[string]string{"EMAIL_WEBHOOK_TOKEN": ""}, "EMAIL_WEBHOOK_TOKEN"},
		"explicit production":  {map[string]string{"ENVIRONMENT": "production", "WEBHOOK_HMAC_SECRET": devSecret}, "WEBHOOK_HMAC_SECRET"},
		"prod":                 {map[string]string{"ENVIRONMENT": "prod", "WEBHOOK_HMAC_SECRET": devSecret}, "WEBHOOK_HMAC_SECRET"},
		"staging":              {map[string]string{"ENVIRONMENT": "staging", "EMAIL_WEBHOOK_TOKEN": devToken}, "EMAIL_WEBHOOK_TOKEN"},
		"real values":          {nil, ""},
		"test defaults":        {map[string]string{"ENVIRONMENT": "test", "WEBHOOK_HMAC_SECRET": devSecret, "EMAIL_WEBHOOK_TOKEN": devToken}, ""},
		"development defaults": {map[string]string{"ENVIRONMENT": "development", "WEBHOOK_HMAC_SECRET": devSecret, "EMAIL_WEBHOOK_TOKEN": devToken}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseConfig(baseVars(tc.vars))
			switch {
			case tc.want == "" && err != nil:
				t.Errorf("err = %v, want none", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Errorf("err = %v, want one naming %s", err, tc.want)
			}
		})
	}
}

func TestIntakeSecretsDefaultToPythonDevValues(t *testing.T) {
	t.Parallel()
	vars := baseVars(map[string]string{"ENVIRONMENT": "development"})
	delete(vars, "WEBHOOK_HMAC_SECRET")
	delete(vars, "EMAIL_WEBHOOK_TOKEN")
	cfg, err := ParseConfig(vars)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WebhookHMACSecret != "dev-webhook-secret-change-me" || cfg.EmailWebhookToken != "dev-email-token-change-me" {
		t.Errorf("defaults = %q, %q", cfg.WebhookHMACSecret, cfg.EmailWebhookToken)
	}
}

const (
	devMCPConfirm = "dev-mcp-confirm-secret-change-me"
	devMCPToken   = "dev-mcp-token-secret-change-me"
)

// Python's test_mcp_settings_have_secure_defaults_shape.
func TestMCPSettingsHaveSecureDefaultsShape(t *testing.T) {
	t.Parallel()
	cfg, err := ParseConfig(baseVars(nil))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.MCPEnabled || cfg.MCPAccessTokenTTL != 3600*time.Second || cfg.MCPRefreshTokenTTL != 2592000*time.Second {
		t.Errorf("cfg = %+v", cfg)
	}
	if cfg.MCPPublicBaseURL != "http://localhost:8000" {
		t.Errorf("MCPPublicBaseURL = %q", cfg.MCPPublicBaseURL)
	}
	if len(cfg.MCPConfirmSecret) < 32 || len(cfg.MCPTokenSecret) < 32 {
		t.Errorf("secrets %d, %d bytes", len(cfg.MCPConfirmSecret), len(cfg.MCPTokenSecret))
	}
}

func TestMCPEnvOverrides(t *testing.T) {
	t.Parallel()
	cfg, err := ParseConfig(baseVars(map[string]string{
		"MCP_ENABLED": "false", "MCP_ACCESS_TOKEN_TTL_SECONDS": "60", "MCP_REFRESH_TOKEN_TTL_SECONDS": "120",
		"MCP_PUBLIC_BASE_URL": "https://billing.wenhao.id",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MCPEnabled || cfg.MCPAccessTokenTTL != time.Minute || cfg.MCPRefreshTokenTTL != 2*time.Minute || cfg.MCPPublicBaseURL != "https://billing.wenhao.id" {
		t.Errorf("cfg = %+v", cfg)
	}
}

// Python's test_transport_allowlist_public_https_host.
func TestTransportAllowlistPublicHTTPSHost(t *testing.T) {
	t.Parallel()
	hosts, origins := TransportAllowlist("https://billing.wenhao.id")
	if want := []string{"billing.wenhao.id"}; !reflect.DeepEqual(hosts, want) { // hostname == netloc without a port
		t.Errorf("hosts = %v", hosts)
	}
	if want := []string{"https://billing.wenhao.id"}; !reflect.DeepEqual(origins, want) {
		t.Errorf("origins = %v", origins)
	}
}

// Python's test_transport_allowlist_includes_host_with_port.
func TestTransportAllowlistIncludesHostWithPort(t *testing.T) {
	t.Parallel()
	hosts, origins := TransportAllowlist("http://localhost:8000")
	if want := []string{"localhost", "localhost:8000"}; !reflect.DeepEqual(hosts, want) {
		t.Errorf("hosts = %v", hosts)
	}
	if want := []string{"http://localhost:8000"}; !reflect.DeepEqual(origins, want) {
		t.Errorf("origins = %v", origins)
	}
}

func TestTransportAllowlistLowercasesHost(t *testing.T) {
	t.Parallel()
	hosts, origins := TransportAllowlist("https://Billing.Wenhao.ID:8443")
	if want := []string{"billing.wenhao.id", "billing.wenhao.id:8443"}; !reflect.DeepEqual(hosts, want) || origins[0] != "https://billing.wenhao.id:8443" {
		t.Errorf("hosts = %v, origins = %v", hosts, origins)
	}
}

func TestMCPRefusesNonPositiveTTL(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"MCP_ACCESS_TOKEN_TTL_SECONDS", "MCP_REFRESH_TOKEN_TTL_SECONDS"} {
		for _, v := range []string{"0", "-1"} {
			t.Run(name+"="+v, func(t *testing.T) {
				t.Parallel()
				_, err := ParseConfig(baseVars(map[string]string{name: v}))
				if err == nil || !strings.Contains(err.Error(), name) {
					t.Errorf("err = %v, want one naming %s", err, name)
				}
			})
		}
	}
}

func TestProductionRefusesDefaultMcpSecrets(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		vars map[string]string
		want string // the variable the error names; "" accepts
	}{
		"default confirm":      {map[string]string{"MCP_CONFIRM_SECRET": devMCPConfirm}, "MCP_CONFIRM_SECRET"},
		"default token":        {map[string]string{"MCP_TOKEN_SECRET": devMCPToken}, "MCP_TOKEN_SECRET"},
		"empty confirm":        {map[string]string{"MCP_CONFIRM_SECRET": ""}, "MCP_CONFIRM_SECRET"},
		"empty token":          {map[string]string{"MCP_TOKEN_SECRET": ""}, "MCP_TOKEN_SECRET"},
		"15 byte confirm":      {map[string]string{"MCP_CONFIRM_SECRET": strings.Repeat("a", 15)}, "MCP_CONFIRM_SECRET"},
		"15 byte token":        {map[string]string{"MCP_TOKEN_SECRET": strings.Repeat("a", 15)}, "MCP_TOKEN_SECRET"},
		"15 byte in test":      {map[string]string{"ENVIRONMENT": "test", "MCP_TOKEN_SECRET": strings.Repeat("a", 15)}, "MCP_TOKEN_SECRET"},
		"16 byte":              {map[string]string{"MCP_TOKEN_SECRET": strings.Repeat("a", 16)}, ""},
		"explicit production":  {map[string]string{"ENVIRONMENT": "production", "MCP_TOKEN_SECRET": devMCPToken}, "MCP_TOKEN_SECRET"},
		"staging":              {map[string]string{"ENVIRONMENT": "staging", "MCP_CONFIRM_SECRET": devMCPConfirm}, "MCP_CONFIRM_SECRET"},
		"real values":          {nil, ""},
		"test defaults":        {map[string]string{"ENVIRONMENT": "test", "MCP_CONFIRM_SECRET": devMCPConfirm, "MCP_TOKEN_SECRET": devMCPToken}, ""},
		"development defaults": {map[string]string{"ENVIRONMENT": "development", "MCP_CONFIRM_SECRET": devMCPConfirm, "MCP_TOKEN_SECRET": devMCPToken}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseConfig(baseVars(tc.vars))
			switch {
			case tc.want == "" && err != nil:
				t.Errorf("err = %v, want none", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Errorf("err = %v, want one naming %s", err, tc.want)
			}
		})
	}
}

func TestMCPSecretsDefaultToPythonDevValues(t *testing.T) {
	t.Parallel()
	vars := baseVars(map[string]string{"ENVIRONMENT": "development"})
	delete(vars, "MCP_CONFIRM_SECRET")
	delete(vars, "MCP_TOKEN_SECRET")
	cfg, err := ParseConfig(vars)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MCPConfirmSecret != devMCPConfirm || cfg.MCPTokenSecret != devMCPToken {
		t.Errorf("defaults = %q, %q", cfg.MCPConfirmSecret, cfg.MCPTokenSecret)
	}
}

// test_resend_settings_defaults
// TestConfigSchedulerIsOffByDefault: the scheduler is off unless
// SCHEDULER_ENABLED says otherwise (Go change 8; Python's default was true).
func TestConfigSchedulerIsOffByDefault(t *testing.T) {
	t.Parallel()
	cfg, err := ParseConfig(baseVars(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SchedulerEnabled {
		t.Error("the scheduler is on with SCHEDULER_ENABLED unset, want off")
	}
}

// TestConfigSchedulerReadsTheVariable: SCHEDULER_ENABLED=true turns the
// scheduler on.
func TestConfigSchedulerReadsTheVariable(t *testing.T) {
	t.Parallel()
	cfg, err := ParseConfig(baseVars(map[string]string{"SCHEDULER_ENABLED": "true"}))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.SchedulerEnabled {
		t.Error("the scheduler is off with SCHEDULER_ENABLED=true, want on")
	}
	cfg, err = ParseConfig(baseVars(map[string]string{"SCHEDULER_ENABLED": "false"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SchedulerEnabled {
		t.Error("the scheduler is on with SCHEDULER_ENABLED=false, want off")
	}
}

func TestConfigResendDefaults(t *testing.T) {
	t.Parallel()
	cfg, err := ParseConfig(baseVars(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ResendAPIKey != "" || cfg.ResendFromEmail != "noreply@wenhao.id" || cfg.ResendFromName != "Wenhao Dev Billing" ||
		cfg.ResendBaseURL != "https://api.resend.com" || cfg.ResendTimeout != 15*time.Second || cfg.SMTPReplyTo != "wenhao.kho@gmail.com" {
		t.Errorf("defaults = %+v", cfg)
	}
}

// test_resend_api_key_from_env
func TestConfigResendKeyFromEnv(t *testing.T) {
	t.Parallel()
	cfg, err := ParseConfig(baseVars(map[string]string{
		"RESEND_API_KEY": "re_test_123", "RESEND_TIMEOUT_SECONDS": "2.5", "SMTP_REPLY_TO": "",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ResendAPIKey != "re_test_123" || cfg.ResendTimeout != 2500*time.Millisecond || cfg.SMTPReplyTo != "" {
		t.Errorf("config = %+v", cfg)
	}
}

func TestConfigRefusesZeroResendTimeout(t *testing.T) {
	t.Parallel()
	_, err := ParseConfig(baseVars(map[string]string{"RESEND_TIMEOUT_SECONDS": "0"}))
	if err == nil || !strings.Contains(err.Error(), "RESEND_TIMEOUT_SECONDS") {
		t.Errorf("err = %v", err)
	}
}

func TestConfigTrimsResendKey(t *testing.T) {
	t.Parallel()
	cfg, err := ParseConfig(baseVars(map[string]string{"RESEND_API_KEY": "  re_x \n"}))
	if err != nil || cfg.ResendAPIKey != "re_x" {
		t.Errorf("key = %q, err %v", cfg.ResendAPIKey, err)
	}
}

// Python's cloudflare settings (app/config.py:65-67).
func TestConfigCloudflareDefaults(t *testing.T) {
	t.Parallel()
	cfg, err := ParseConfig(baseVars(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CloudflareAPIToken != "" || cfg.CloudflareAPIBaseURL != "https://api.cloudflare.com/client/v4" || cfg.CloudflareTimeout != 15*time.Second {
		t.Errorf("defaults = %q %q %v", cfg.CloudflareAPIToken, cfg.CloudflareAPIBaseURL, cfg.CloudflareTimeout)
	}
	cfg, err = ParseConfig(baseVars(map[string]string{
		"CLOUDFLARE_API_TOKEN": " cf-token\n", "CLOUDFLARE_API_BASE_URL": "http://cf.test/v4", "CLOUDFLARE_TIMEOUT_SECONDS": "2.5",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CloudflareAPIToken != "cf-token" || cfg.CloudflareAPIBaseURL != "http://cf.test/v4" || cfg.CloudflareTimeout != 2500*time.Millisecond {
		t.Errorf("set = %q %q %v", cfg.CloudflareAPIToken, cfg.CloudflareAPIBaseURL, cfg.CloudflareTimeout)
	}
}

func TestConfigRefusesNonPositiveCloudflareTimeout(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"0", "-1"} {
		_, err := ParseConfig(baseVars(map[string]string{"CLOUDFLARE_TIMEOUT_SECONDS": bad}))
		if err == nil || !strings.Contains(err.Error(), "CLOUDFLARE_TIMEOUT_SECONDS") {
			t.Errorf("%q: err = %v", bad, err)
		}
	}
}
