// Package email sends the API's mail. Until SMTP is wired up, LogSender logs
// each message instead, as the Python backend did with no SMTP host.
package email

import (
	"context"
	"fmt"
	"log/slog"
)

// Message is one email, with a plain-text and an HTML body.
type Message struct {
	To, Subject, Text, HTML string
}

// Sender delivers a Message.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// LogSender logs every message instead of sending it. The text body is
// logged too: with email disabled, a reset link is read from the log.
type LogSender struct{ Logger *slog.Logger }

// Send logs m and never fails.
func (s LogSender) Send(ctx context.Context, m Message) error {
	logger := s.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.InfoContext(ctx, fmt.Sprintf("[email disabled] would send to=%s subject=%q", m.To, m.Subject), "body", m.Text)
	return nil
}
