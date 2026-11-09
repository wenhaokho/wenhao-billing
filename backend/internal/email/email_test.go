package email

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

// A reset email carries a live link: outside local development it must
// never reach the log, only who it was for and what it was about.
func TestLogSenderHidesBodiesOutsideLocal(t *testing.T) {
	t.Parallel()
	const link = "http://app.test/reset-password?token=secret-token-value"
	m := Message{To: "a@example.com", Subject: "Reset your password", Text: "Open " + link, HTML: `<a href="` + link + `">reset</a>`}
	for _, show := range []bool{false, true} {
		var buf bytes.Buffer
		s := LogSender{Logger: slog.New(slog.NewTextHandler(&buf, nil)), ShowBodies: show}
		if err := s.Send(context.Background(), m); err != nil {
			t.Fatal(err)
		}
		out := buf.String()
		if !strings.Contains(out, "a@example.com") || !strings.Contains(out, "Reset your password") {
			t.Errorf("ShowBodies=%v: no recipient or subject in %q", show, out)
		}
		if got := strings.Contains(out, "secret-token-value"); got != show {
			t.Errorf("ShowBodies=%v: token logged=%v in %q", show, got, out)
		}
	}
}

// CC addresses and attachment names are logged; attachment bytes never are.
func TestLogSenderLogsCcAndAttachmentNames(t *testing.T) {
	t.Parallel()
	m := Message{
		To: "a@example.com", Subject: "Payment receipt", Cc: []string{"b@example.com", "c@example.com"},
		Attachments: []Attachment{{Filename: "receipt-RCT-0A1B2C3D.pdf", ContentType: "application/pdf", Content: []byte("%PDF-secret-bytes")}},
	}
	var buf bytes.Buffer
	s := LogSender{Logger: slog.New(slog.NewTextHandler(&buf, nil)), ShowBodies: true}
	if err := s.Send(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"b@example.com, c@example.com", "receipt-RCT-0A1B2C3D.pdf"} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in %q", want, out)
		}
	}
	if strings.Contains(out, "secret-bytes") {
		t.Errorf("attachment content logged: %q", out)
	}
}
