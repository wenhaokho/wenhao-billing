// Package email sends the API's mail. Until Resend is wired up (plan 7),
// LogSender logs each message instead, as the Python backend did with no
// SMTP host.
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

// LogSender logs every message instead of sending it: who it is for and its
// subject, never its body, which can hold a live reset link. ShowBodies logs
// the text body too, so local development can read a reset link from the
// log; serve sets it only for a local environment.
type LogSender struct {
	Logger     *slog.Logger
	ShowBodies bool
}

// Send logs m and never fails.
func (s LogSender) Send(ctx context.Context, m Message) error {
	logger := s.Logger
	if logger == nil {
		logger = slog.Default()
	}
	msg := fmt.Sprintf("[email disabled] would send to=%s subject=%q", m.To, m.Subject)
	if s.ShowBodies {
		logger.InfoContext(ctx, msg, "body", m.Text)
		return nil
	}
	logger.InfoContext(ctx, msg)
	return nil
}
