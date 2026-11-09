package email

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// capture is a fake Resend: it records the one POST and answers with status.
type capture struct {
	mu      sync.Mutex
	path    string
	headers http.Header
	body    map[string]any
	status  int
	reply   string
}

func (c *capture) handler(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	c.mu.Lock()
	c.path, c.headers = r.URL.Path, r.Header.Clone()
	c.body = nil
	_ = json.Unmarshal(raw, &c.body)
	status, reply := c.status, c.reply
	c.mu.Unlock()
	if status == 0 {
		status = 200
	}
	w.WriteHeader(status)
	_, _ = io.WriteString(w, reply)
}

func newResend(t *testing.T, c *capture, mutate func(*ResendConfig)) (Sender, *bytes.Buffer) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(c.handler))
	t.Cleanup(srv.Close)
	var logs bytes.Buffer
	cfg := ResendConfig{
		APIKey: "re_test_123", FromEmail: "noreply@wenhao.id", FromName: "Wenhao Dev Billing",
		BaseURL: srv.URL, ReplyTo: "wenhao.kho@gmail.com", Timeout: 5 * time.Second,
		Logger: slog.New(slog.NewTextHandler(&logs, nil)),
	}
	if mutate != nil {
		mutate(&cfg)
	}
	return NewResendSender(cfg, srv.Client()), &logs
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

// test_resend_used_when_key_set_and_smtp_untouched
func TestResendPostsTheMinimalPayload(t *testing.T) {
	t.Parallel()
	c := &capture{}
	s, _ := newResend(t, c, nil)
	if err := s.Send(context.Background(), Message{To: "client@example.com", Subject: "Hi", Text: "hello"}); err != nil {
		t.Fatal(err)
	}
	if c.path != "/emails" {
		t.Errorf("path = %q, want /emails", c.path)
	}
	if got := c.headers.Get("Authorization"); got != "Bearer re_test_123" {
		t.Errorf("Authorization = %q", got)
	}
	want := map[string]any{
		"from": "Wenhao Dev Billing <noreply@wenhao.id>", "to": []string{"client@example.com"},
		"subject": "Hi", "text": "hello", "reply_to": "wenhao.kho@gmail.com",
	}
	for k, v := range want {
		if got := mustJSON(c.body[k]); got != mustJSON(v) {
			t.Errorf("%s = %s, want %s", k, got, mustJSON(v))
		}
	}
	for _, k := range []string{"html", "cc", "attachments"} {
		if _, ok := c.body[k]; ok {
			t.Errorf("%q sent although empty: %v", k, c.body)
		}
	}
	if c.headers.Get("Idempotency-Key") != "" {
		t.Errorf("an Idempotency-Key was sent without one set")
	}
}

// test_resend_payload_includes_html_cc_and_attachments, and the split of
// several cc addresses (Message.Cc is already a list).
func TestResendPayloadIncludesHTMLCcAndAttachments(t *testing.T) {
	t.Parallel()
	c := &capture{}
	s, _ := newResend(t, c, nil)
	err := s.Send(context.Background(), Message{
		To: "client@example.com", Subject: "Invoice", Text: "see attached", HTML: "<p>see attached</p>",
		Cc:          []string{"a@example.com", "b@example.com"},
		Attachments: []Attachment{{Filename: "invoice.pdf", ContentType: "application/pdf", Content: []byte("%PDF-1.4")}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.body["html"] != "<p>see attached</p>" {
		t.Errorf("html = %v", c.body["html"])
	}
	if got := mustJSON(c.body["cc"]); got != `["a@example.com","b@example.com"]` {
		t.Errorf("cc = %s", got)
	}
	wantAtt := `[{"content":"` + base64.StdEncoding.EncodeToString([]byte("%PDF-1.4")) + `","filename":"invoice.pdf"}]`
	if got := mustJSON(c.body["attachments"]); got != wantAtt {
		t.Errorf("attachments = %s, want %s", got, wantAtt)
	}
}

// With no reply-to configured, none is sent (Python's "if smtp_reply_to").
func TestResendOmitsEmptyReplyTo(t *testing.T) {
	t.Parallel()
	c := &capture{}
	s, _ := newResend(t, c, func(cfg *ResendConfig) { cfg.ReplyTo = "" })
	if err := s.Send(context.Background(), Message{To: "a@example.com", Subject: "Hi", Text: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.body["reply_to"]; ok {
		t.Errorf("reply_to sent although empty: %v", c.body)
	}
}

// test_resend_raises_on_error_response and test_resend_error_body_is_logged
func TestResendErrorResponseFailsAndLogsTheBody(t *testing.T) {
	t.Parallel()
	c := &capture{status: 422, reply: `{"message": "The domain is not verified."}`}
	s, logs := newResend(t, c, nil)
	err := s.Send(context.Background(), Message{To: "client@example.com", Subject: "Hi", Text: "hello"})
	if err == nil {
		t.Fatal("a 422 was not an error")
	}
	out := logs.String()
	for _, want := range []string{"Resend rejected email to client@example.com: 422", "not verified"} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q: %s", want, out)
		}
	}
}

// The key is the document id and a hash of recipients, subject and body: a
// retry repeats it, and any change to what is sent changes it.
func TestIdempotencyKeyStableAndBodyBound(t *testing.T) {
	t.Parallel()
	base := Message{To: "a@example.com", Cc: []string{"c@example.com"}, Subject: "Invoice", Text: "body", HTML: "<p>body</p>"}
	key := IdempotencyKey("doc-1", base)
	if !strings.HasPrefix(key, "doc-1:") {
		t.Errorf("key %q does not start with the document id", key)
	}
	if again := IdempotencyKey("doc-1", base); again != key {
		t.Errorf("same message, keys %q and %q", key, again)
	}
	variants := map[string]Message{
		"to":      {To: "b@example.com", Cc: base.Cc, Subject: base.Subject, Text: base.Text, HTML: base.HTML},
		"cc":      {To: base.To, Subject: base.Subject, Text: base.Text, HTML: base.HTML},
		"subject": {To: base.To, Cc: base.Cc, Subject: "Invoice 2", Text: base.Text, HTML: base.HTML},
		"text":    {To: base.To, Cc: base.Cc, Subject: base.Subject, Text: "body 2", HTML: base.HTML},
		"html":    {To: base.To, Cc: base.Cc, Subject: base.Subject, Text: base.Text, HTML: "<p>x</p>"},
	}
	for name, m := range variants {
		if IdempotencyKey("doc-1", m) == key {
			t.Errorf("changing %s kept the key", name)
		}
	}
	if IdempotencyKey("doc-2", base) == key {
		t.Errorf("another document kept the key")
	}

	// The sender sends it as the Idempotency-Key header.
	c := &capture{}
	s, _ := newResend(t, c, nil)
	m := base
	m.IdempotencyKey = key
	if err := s.Send(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if got := c.headers.Get("Idempotency-Key"); got != key {
		t.Errorf("Idempotency-Key header = %q, want %q", got, key)
	}
}

// A long error body is logged cut to about 2 KiB.
func TestResendErrorBodyLogIsTruncated(t *testing.T) {
	t.Parallel()
	c := &capture{status: 500, reply: strings.Repeat("x", 100<<10)}
	s, logs := newResend(t, c, nil)
	if err := s.Send(context.Background(), Message{To: "a@example.com", Subject: "Hi", Text: "x"}); err == nil {
		t.Fatal("a 500 was not an error")
	}
	if n := logs.Len(); n > 4<<10 || !strings.Contains(logs.String(), "truncated") {
		t.Errorf("log is %d bytes, want it cut near 2 KiB", n)
	}
}

// A status just under 400 is a success.
func TestResendStatusBelow400Succeeds(t *testing.T) {
	t.Parallel()
	c := &capture{status: 399}
	s, _ := newResend(t, c, nil)
	if err := s.Send(context.Background(), Message{To: "a@example.com", Subject: "Hi", Text: "x"}); err != nil {
		t.Errorf("399: %v", err)
	}
	c2 := &capture{status: 400}
	s2, _ := newResend(t, c2, nil)
	if err := s2.Send(context.Background(), Message{To: "a@example.com", Subject: "Hi", Text: "x"}); err == nil {
		t.Errorf("400 was not an error")
	}
}

// A Resend that never answers fails the send at the configured timeout
// instead of holding the request.
func TestResendTimeout(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	t.Cleanup(func() { close(release); srv.Close() })
	var logs bytes.Buffer
	s := NewResendSender(ResendConfig{
		APIKey: "k", FromEmail: "f@x.test", FromName: "F", BaseURL: srv.URL, Timeout: 50 * time.Millisecond,
		Logger: slog.New(slog.NewTextHandler(&logs, nil)),
	}, srv.Client())
	// The parent deadline only stops a missing sender timeout from hanging
	// the test; the sender's own 50ms must fire first.
	parent, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	err := s.Send(parent, Message{To: "a@example.com", Subject: "Hi", Text: "x"})
	if err == nil {
		t.Fatal("a hung Resend did not fail the send")
	}
	if !errors.Is(err, context.DeadlineExceeded) || parent.Err() != nil {
		t.Errorf("err = %v (parent ctx err %v), want the sender's own deadline", err, parent.Err())
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("failed after %v, want about the 50ms timeout", d)
	}
	if !strings.Contains(logs.String(), "failed to send email via Resend") {
		t.Errorf("failure not logged: %s", logs.String())
	}
}
