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
	if cfg.Environment != "development" || cfg.Production() {
		t.Errorf("Environment = %q", cfg.Environment)
	}
	if want := []string{"http://localhost:5173"}; !reflect.DeepEqual(cfg.CORSOrigins, want) {
		t.Errorf("CORSOrigins = %v", cfg.CORSOrigins)
	}
	if cfg.AppBaseURL != "http://localhost:5173" || cfg.SentryEnvironment != "development" || cfg.SentryTracesSampleRate != 0 {
		t.Errorf("cfg = %+v", cfg)
	}
}
