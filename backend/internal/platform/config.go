package platform

import (
	"errors"

	"github.com/caarlos0/env/v11"
)

// Config is the environment the API runs with. A missing required variable
// stops startup naming it.
type Config struct {
	Environment   string   `env:"ENVIRONMENT,required"`
	DatabaseURL   string   `env:"DATABASE_URL,required"`
	RedisURL      string   `env:"REDIS_URL"`
	JWTSecret     string   `env:"JWT_SECRET,required"`
	CORSOrigins   []string `env:"CORS_ORIGINS" envSeparator:","`
	SentryDSN     string   `env:"SENTRY_DSN"`
	SentryRelease string   `env:"SENTRY_RELEASE"`
	Port          int      `env:"PORT" envDefault:"8000"`
}

// LoadConfig reads the process environment.
func LoadConfig() (Config, error) {
	var c Config
	if err := env.Parse(&c); err != nil {
		return Config{}, err
	}
	return c, c.check()
}

// ParseConfig reads vars instead of the process environment (for tests).
func ParseConfig(vars map[string]string) (Config, error) {
	var c Config
	if err := env.ParseWithOptions(&c, env.Options{Environment: vars}); err != nil {
		return Config{}, err
	}
	return c, c.check()
}

func (c Config) check() error {
	// A short secret signs tokens anyone can forge.
	if len(c.JWTSecret) < 32 {
		return errors.New("JWT_SECRET must be at least 32 bytes")
	}
	return nil
}
