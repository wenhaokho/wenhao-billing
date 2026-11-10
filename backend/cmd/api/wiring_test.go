package main

import (
	"strings"
	"testing"

	"github.com/wenhaokho/wenhao-billing/backend/internal/hosting"
	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// TestWireGivesMCPTheRoutesServices: serve's wiring hands the MCP connector
// the routes' own payments.Service (the real hosting restorer, never
// NoHosting), AfterPayment set (the hosting restore after
// resolve_reconciliation_match commits), the emailing draft notifier
// (trigger_recurring mails the digest the recurring routes mail), and the
// routes' mail sender.
func TestWireGivesMCPTheRoutesServices(t *testing.T) {
	t.Parallel()
	cfg, err := platform.ParseConfig(map[string]string{
		"DATABASE_URL": "postgresql://u:p@h:5432/d", "SESSION_SECRET": "wiring-test-secret-32-bytes-long!!",
		"WEBHOOK_HMAC_SECRET": "a-real-webhook-secret", "EMAIL_WEBHOOK_TOKEN": "a-real-email-token",
		"MCP_CONFIRM_SECRET": "a-real-mcp-confirm-secret", "MCP_TOKEN_SECRET": "a-real-mcp-token-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	w := wire(cfg, nil, platform.Sessions{}, platform.SystemClock{}, nil)
	d := w.mcp

	restorer, ok := d.Payments.Hosting.(hosting.Restorer)
	if !ok {
		t.Fatalf("MCP payments hosting is %T, want hosting.Restorer", d.Payments.Hosting)
	}
	if restorer.Client == nil {
		t.Error("MCP hosting restorer has no Cloudflare client")
	}
	if d.AfterPayment == nil {
		t.Error("MCP AfterPayment is nil: a payment resolve_reconciliation_match clears never restores hosting")
	}
	if w.features.Services == nil {
		t.Fatal("the routes' features carry no shared services")
	}
	if routes := w.features.Services.Payments; routes != d.Payments {
		t.Errorf("MCP payments %+v, routes payments %+v: want one shared service", d.Payments, routes)
	}
	if d.Payments.BaseCurrency != cfg.BaseCurrency {
		t.Errorf("MCP base currency %q, want %q", d.Payments.BaseCurrency, cfg.BaseCurrency)
	}

	notifier, ok := d.Notifier.(invoices.EmailNotifier)
	if !ok {
		t.Fatalf("MCP notifier is %T, want invoices.EmailNotifier", d.Notifier)
	}
	if notifier.Sender != w.features.Mail || d.Sender != w.features.Mail {
		t.Errorf("MCP senders %v / %v, routes sender %v: want one sender", notifier.Sender, d.Sender, w.features.Mail)
	}
	if w.features.Services.Notifier != d.Notifier {
		t.Error("MCP notifier is not the routes' notifier")
	}
}

// register refuses features without Services instead of building a second
// set the MCP connector would not share.
func TestRegisterRequiresServices(t *testing.T) {
	t.Parallel()
	_, api := platform.NewServer(platform.Config{Environment: "test"}, platform.Deps{Sessions: testSessions()})
	defer func() {
		r := recover()
		if msg, _ := r.(string); !strings.Contains(msg, "features.Services is nil") {
			t.Fatalf("register without Services: recovered %v, want its panic", r)
		}
	}()
	register(api, features{Sessions: testSessions()})
}
