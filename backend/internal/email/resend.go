package email

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// ResendConfig is what ResendSender needs, from the RESEND_* settings and
// SMTP_REPLY_TO.
type ResendConfig struct {
	APIKey    string
	FromEmail string
	FromName  string
	BaseURL   string
	ReplyTo   string        // empty: no reply_to is sent
	Timeout   time.Duration // zero: the client's own timeout only
	Logger    *slog.Logger
}

type resendSender struct {
	cfg    ResendConfig
	client *http.Client
}

// NewResendSender posts each message to Resend's /emails endpoint as
// _send_via_resend did (backend-python/app/services/email.py). It is a thin
// net/http client rather than resend-go, which is not vendored and would
// need a private-network vendoring step for one POST.
func NewResendSender(cfg ResendConfig, client *http.Client) Sender {
	if client == nil {
		client = http.DefaultClient
	}
	return resendSender{cfg: cfg, client: client}
}

// IdempotencyKey is the document id, a SHA-256 over everything sent (the
// recipients, the subject, the bodies and every attachment's filename, content
// type and bytes) and the 10-minute UTC window of now. A retry of an identical
// send inside the window repeats the key, so Resend delivers it once; a changed
// message, or the same one in a later window, gets a new key and goes out
// (Resend keeps keys for 24 hours). Fields are length-prefixed so no two
// different messages hash alike.
func IdempotencyKey(documentID string, m Message, now time.Time) string {
	h := sha256.New()
	put := func(s string) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(s)))
		h.Write(n[:])
		h.Write([]byte(s))
	}
	put(m.To)
	for _, c := range m.Cc {
		put(c)
	}
	put(m.Subject)
	put(m.Text)
	put(m.HTML)
	for _, a := range m.Attachments {
		put(a.Filename)
		put(a.ContentType)
		put(string(a.Content))
	}
	bucket := now.UTC().Unix() / int64(IdempotencyWindow/time.Second)
	return fmt.Sprintf("%s:%d:%s", documentID, bucket, hex.EncodeToString(h.Sum(nil)))
}

// IdempotencyWindow is how long an identical send is deduplicated.
const IdempotencyWindow = 10 * time.Minute

func (s resendSender) Send(ctx context.Context, m Message) error {
	logger := s.cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	payload := map[string]any{
		"from":    fmt.Sprintf("%s <%s>", s.cfg.FromName, s.cfg.FromEmail),
		"to":      []string{m.To},
		"subject": m.Subject,
		"text":    m.Text,
	}
	if m.HTML != "" {
		payload["html"] = m.HTML
	}
	if len(m.Cc) > 0 {
		payload["cc"] = m.Cc
	}
	if s.cfg.ReplyTo != "" {
		payload["reply_to"] = s.cfg.ReplyTo
	}
	if len(m.Attachments) > 0 {
		atts := make([]map[string]string, 0, len(m.Attachments))
		for _, a := range m.Attachments {
			atts = append(atts, map[string]string{"filename": a.Filename, "content": base64.StdEncoding.EncodeToString(a.Content)})
		}
		payload["attachments"] = atts
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if s.cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.cfg.Timeout)
		defer cancel()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(s.cfg.BaseURL, "/")+"/emails", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	if m.IdempotencyKey != "" {
		req.Header.Set("Idempotency-Key", m.IdempotencyKey)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		logger.ErrorContext(ctx, "failed to send email via Resend", "to", m.To, "err", err)
		return fmt.Errorf("send email via Resend: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		const maxLogged = 2 << 10 // the drain above is 1 MiB; the log keeps 2 KiB
		logged := respBody
		if len(logged) > maxLogged {
			logged = append(append([]byte{}, logged[:maxLogged]...), "...(truncated)"...)
		}
		logger.ErrorContext(ctx, fmt.Sprintf("Resend rejected email to %s: %d %s", m.To, resp.StatusCode, logged))
		return fmt.Errorf("resend returned %d", resp.StatusCode)
	}
	logger.InfoContext(ctx, "sent email via Resend", "to", m.To, "subject", m.Subject)
	return nil
}
