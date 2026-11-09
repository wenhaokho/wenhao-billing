// Package email sends the API's mail. Until Resend is wired up (plan 7),
// LogSender logs each message instead, as the Python backend did with no
// SMTP host.
package email

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

// Message is one email, with a plain-text and an HTML body. Cc is a list of
// copied addresses and Attachments the files sent with it; either may be nil.
type Message struct {
	To, Subject, Text, HTML string
	Cc                      []string
	Attachments             []Attachment
}

// Attachment is a file sent with a Message.
type Attachment struct {
	Filename, ContentType string
	Content               []byte
}

// Sender delivers a Message.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// LogSender logs every message instead of sending it: who it is for and its
// subject, CC addresses and attachment names, never its body or its files, which can hold a live reset link. ShowBodies logs
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
	if len(m.Cc) > 0 {
		msg += fmt.Sprintf(" cc=%s", strings.Join(m.Cc, ", "))
	}
	if len(m.Attachments) > 0 {
		names := make([]string, 0, len(m.Attachments))
		for _, a := range m.Attachments {
			names = append(names, a.Filename)
		}
		msg += fmt.Sprintf(" attachments=%s", strings.Join(names, ", "))
	}
	if s.ShowBodies {
		logger.InfoContext(ctx, msg, "body", m.Text)
		return nil
	}
	logger.InfoContext(ctx, msg)
	return nil
}
