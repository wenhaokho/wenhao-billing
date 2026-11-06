package main

import (
	"context"
	"errors"
	"github.com/wenhaokho/wenhao-billing/backend/internal/email"
	"testing"
)

func TestRecoveryMailConfig(t *testing.T) {
	for _, k := range []string{"RESEND_API_KEY", "RESEND_BASE_URL", "SMTP_HOST", "SMTP_USERNAME", "SMTP_PASSWORD", "SMTP_PORT", "SMTP_USE_TLS", "SMTP_REPLY_TO", "SMTP_FROM_EMAIL", "SMTP_FROM_NAME", "RESEND_FROM_EMAIL", "RESEND_FROM_NAME", "RESEND_TIMEOUT_SECONDS"} {
		t.Setenv(k, "")
	}
	// An empty explicit reply-to is permitted; defaults for sender fields require unset values.
	t.Setenv("SMTP_FROM_EMAIL", "noreply@wenhao.id")
	t.Setenv("RESEND_FROM_EMAIL", "noreply@wenhao.id")
	s, err := emailSenderFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Send(context.Background(), email.Message{}); !errors.Is(err, email.ErrDisabled) {
		t.Fatalf("disabled: %v", err)
	}
	t.Setenv("SMTP_HOST", "smtp.example.com")
	t.Setenv("SMTP_PORT", "bad")
	if _, err = emailSenderFromEnv(); err == nil {
		t.Fatal("invalid port accepted")
	}
	t.Setenv("SMTP_PORT", "587")
	t.Setenv("SMTP_USE_TLS", "bad")
	if _, err = emailSenderFromEnv(); err == nil {
		t.Fatal("invalid TLS accepted")
	}
	t.Setenv("SMTP_USE_TLS", "true")
	if _, err = emailSenderFromEnv(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RESEND_API_KEY", "test-key")
	t.Setenv("RESEND_BASE_URL", "https://api.resend.com")
	t.Setenv("SMTP_USERNAME", "orphan")
	t.Setenv("SMTP_PORT", "stale-invalid-port")
	t.Setenv("SMTP_USE_TLS", "stale-invalid-tls")
	if _, err = emailSenderFromEnv(); err != nil {
		t.Fatal("Resend should take precedence", err)
	}
	t.Setenv("RESEND_BASE_URL", "http://example.com")
	if _, err = emailSenderFromEnv(); err == nil {
		t.Fatal("invalid Resend URL accepted")
	}
}
