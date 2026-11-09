package platform

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

const configSecret = "0123456789abcdef0123456789abcdef" // 32 bytes

func baseVars(extra map[string]string) map[string]string {
	v := map[string]string{"DATABASE_URL": "postgresql://u:p@h:5432/d", "SESSION_SECRET": configSecret}
	for k, val := range extra {
		v[k] = val
	}
	return v
}

func TestConfigReadsJSONCorsOrigins(t *testing.T) {
	cfg, err := ParseConfig(baseVars(map[string]string{"CORS_ORIGINS": `["https://billing.wenhao.id"]`}))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"https://billing.wenhao.id"}; !reflect.DeepEqual(cfg.CORSOrigins, want) {
		t.Errorf("CORSOrigins = %v, want %v", cfg.CORSOrigins, want)
	}
}

func TestConfigReadsCommaCorsOrigins(t *testing.T) {
	cfg, err := ParseConfig(baseVars(map[string]string{"CORS_ORIGINS": "http://a.test, http://b.test"}))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"http://a.test", "http://b.test"}; !reflect.DeepEqual(cfg.CORSOrigins, want) {
		t.Errorf("CORSOrigins = %v, want %v", cfg.CORSOrigins, want)
	}
}

func TestConfigRewritesPsycopgURL(t *testing.T) {
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
	_, err := ParseConfig(baseVars(map[string]string{"SESSION_SECRET": configSecret[:31]}))
	if err == nil || !strings.Contains(err.Error(), "SESSION_SECRET") {
		t.Errorf("err = %v, want one naming SESSION_SECRET", err)
	}
}

func TestConfigRequiresDatabaseURL(t *testing.T) {
	_, err := ParseConfig(map[string]string{"SESSION_SECRET": configSecret})
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Errorf("err = %v, want one naming DATABASE_URL", err)
	}
}

func TestConfigDefaults(t *testing.T) {
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
	for _, v := range []string{"", "  ", "[]", `["", " "]`} {
		cfg, err := ParseConfig(baseVars(map[string]string{"CORS_ORIGINS": v}))
		if err != nil || len(cfg.CORSOrigins) != 0 {
			t.Errorf("CORS_ORIGINS=%q -> %v, %v; want none", v, cfg.CORSOrigins, err)
		}
	}
}

func TestConfigMalformedJSONCorsOriginsErrors(t *testing.T) {
	_, err := ParseConfig(baseVars(map[string]string{"CORS_ORIGINS": `["https://a"`}))
	if err == nil || !strings.Contains(err.Error(), "CORS_ORIGINS") {
		t.Errorf("err = %v, want one naming CORS_ORIGINS", err)
	}
}

func TestConfigFXDefaults(t *testing.T) {
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
	for _, bad := range []string{"RUPIAH", "ID", "12R", ""} {
		_, err := ParseConfig(baseVars(map[string]string{"BASE_CURRENCY": bad}))
		if err == nil || !strings.Contains(err.Error(), "BASE_CURRENCY") {
			t.Errorf("%q: err = %v, want one naming BASE_CURRENCY", bad, err)
		}
	}
}

// 0 would mean no timeout at all, so it is refused with the negatives.
func TestConfigRefusesNonPositiveFXTimeout(t *testing.T) {
	for _, bad := range []string{"0", "-1", "-0.5"} {
		_, err := ParseConfig(baseVars(map[string]string{"FX_PROVIDER_TIMEOUT_SECONDS": bad}))
		if err == nil || !strings.Contains(err.Error(), "FX_PROVIDER_TIMEOUT_SECONDS") {
			t.Errorf("%q: err = %v, want one naming FX_PROVIDER_TIMEOUT_SECONDS", bad, err)
		}
	}
}

func TestConfigRefusesBadFXSyncCurrency(t *testing.T) {
	for _, bad := range []string{"US", `["usd","eurx"]`, "usd,12"} {
		_, err := ParseConfig(baseVars(map[string]string{"FX_SYNC_CURRENCIES": bad}))
		if err == nil || !strings.Contains(err.Error(), "FX_SYNC_CURRENCIES") {
			t.Errorf("%q: err = %v, want one naming FX_SYNC_CURRENCIES", bad, err)
		}
	}
}
