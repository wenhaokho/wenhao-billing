// Package observe is logging and error reporting: JSON logs with a request
// id, and Sentry that is on only in production and sends no personal data.
package observe

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/getsentry/sentry-go"
)

// Logger is the process logger: JSON to stdout, with env on every line.
func Logger(env string) *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("env", env)
}

type loggerKey struct{}

// LoggerFrom is the request's logger (with its request_id), or the default.
func LoggerFrom(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey{}).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}

// RequestID reads X-Request-ID or makes one, echoes it in the response and
// puts a logger carrying it in the request's context.
func RequestID(base *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get("X-Request-ID")
			if id == "" || len(id) > 64 {
				b := make([]byte, 8)
				_, _ = rand.Read(b)
				id = hex.EncodeToString(b)
			}
			w.Header().Set("X-Request-ID", id)
			ctx := context.WithValue(r.Context(), loggerKey{}, base.With("request_id", id))
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// A phone, an account number or an amount: digits with the separators people
// write them with. Cut when six or more digits, except an ISO date.
var (
	number  = regexp.MustCompile(`\+?\d[\d \-.]*\d`)
	isoDate = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	email   = regexp.MustCompile(`[\w.+-]+@[\w-]+(?:\.[\w-]+)+`)
)

// Scrub cuts email addresses and long numbers out of text sent to Sentry.
func Scrub(s string) string {
	s = email.ReplaceAllString(s, "[email]")
	return number.ReplaceAllStringFunc(s, func(m string) string {
		digits := 0
		for _, r := range m {
			if unicode.IsDigit(r) {
				digits++
			}
		}
		if digits < 6 || isoDate.MatchString(m) {
			return m
		}
		return "[number]"
	})
}

// SentryConfig is where errors go.
type SentryConfig struct {
	DSN              string
	Environment      string
	Release          string
	TracesSampleRate float64
}

var enabled bool

// InitSentry turns reporting on only when DSN is set and Environment is
// exactly "production". Tracing stays at TracesSampleRate (0 by default):
// nothing scrubs a transaction's spans.
func InitSentry(c SentryConfig) (bool, error) {
	if c.DSN == "" || c.Environment != "production" {
		return false, nil
	}
	err := sentry.Init(sentry.ClientOptions{
		Dsn:              c.DSN,
		Environment:      c.Environment,
		Release:          c.Release,
		TracesSampleRate: c.TracesSampleRate,
		BeforeSend:       scrubEvent,
		BeforeBreadcrumb: scrubBreadcrumb,
	})
	if err != nil {
		return false, err
	}
	enabled = true
	return true, nil
}

// scrubEvent keeps a request's method and path only, drops the user, and
// scrubs every message and exception value.
func scrubEvent(ev *sentry.Event, _ *sentry.EventHint) *sentry.Event {
	if ev.Request != nil {
		url, _, _ := strings.Cut(ev.Request.URL, "?")
		url, _, _ = strings.Cut(url, "#")
		ev.Request = &sentry.Request{Method: ev.Request.Method, URL: url}
	}
	ev.User = sentry.User{}
	ev.Message = Scrub(ev.Message)
	for i := range ev.Exception {
		ev.Exception[i].Value = Scrub(ev.Exception[i].Value)
	}
	return ev
}

func scrubBreadcrumb(b *sentry.Breadcrumb, _ *sentry.BreadcrumbHint) *sentry.Breadcrumb {
	if b.Category == "redis" {
		return nil
	}
	b.Message = Scrub(b.Message)
	if url, ok := b.Data["url"].(string); ok {
		url, _, _ = strings.Cut(url, "?")
		b.Data["url"] = url
	}
	return b
}

// Report sends err with tags (component, stage) when Sentry is on. It never
// panics and does nothing for a nil error.
func Report(err error, tags map[string]string) {
	if !enabled || err == nil {
		return
	}
	sentry.WithScope(func(scope *sentry.Scope) {
		scope.SetTags(tags)
		sentry.CaptureException(err)
	})
}

// Flush waits up to timeout for queued reports, at shutdown.
func Flush(timeout time.Duration) {
	if enabled {
		sentry.Flush(timeout)
	}
}
