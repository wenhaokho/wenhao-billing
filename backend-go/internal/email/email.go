// Package email provides synchronous, explicitly reported email delivery.
package email

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"net/url"
	"strconv"
	"strings"
	"time"

	resend "github.com/resend/resend-go/v2"
)

var ErrDisabled = errors.New("email delivery disabled")
var ErrDelivery = errors.New("email delivery failed")
var ErrInvalid = errors.New("invalid email configuration or message")

type Sender interface {
	Send(context.Context, Message) error
}
type Attachment struct {
	Filename    string
	Content     []byte
	ContentType string
}
type Message struct {
	To, CC              []string
	Subject, Text, HTML string
	Attachments         []Attachment
}
type Config struct {
	ResendKey, ResendURL, ResendFrom, ReplyTo string
	SMTPHost                                  string
	SMTPPort                                  int
	SMTPUsername, SMTPPassword, SMTPFrom      string
	SMTPUseTLS                                bool
	Timeout                                   time.Duration
}
type sender struct {
	c         Config
	resend    *resend.Client
	tlsConfig *tls.Config
}

func New(c Config) (Sender, error) {
	if c.Timeout == 0 {
		c.Timeout = 15 * time.Second
	}
	if c.Timeout < 0 {
		return nil, ErrInvalid
	}
	if c.ResendKey != "" {
		if c.ResendURL == "" {
			c.ResendURL = "https://api.resend.com"
		}
		u, e := url.Parse(c.ResendURL)
		if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || net.ParseIP(u.Hostname()) != nil && net.ParseIP(u.Hostname()).IsLoopback()))) {
			return nil, ErrInvalid
		}
		if !validAddress(c.ResendFrom) || (c.ReplyTo != "" && !validAddress(c.ReplyTo)) {
			return nil, ErrInvalid
		}
		u.Path = strings.TrimRight(u.Path, "/") + "/"
		cl := resend.NewCustomClient(&http.Client{Timeout: c.Timeout}, c.ResendKey)
		cl.BaseURL = u
		return &sender{c: c, resend: cl}, nil
	}
	if c.SMTPHost == "" {
		if c.SMTPUsername != "" || c.SMTPPassword != "" || c.SMTPPort != 0 || c.SMTPUseTLS {
			return nil, ErrInvalid
		}
		return &sender{c: c}, nil
	}
	if c.SMTPPort == 0 {
		c.SMTPPort = 587
	}
	if c.SMTPPort < 1 || c.SMTPPort > 65535 || !validAddress(c.SMTPFrom) || (c.ReplyTo != "" && !validAddress(c.ReplyTo)) || (c.SMTPUsername == "") != (c.SMTPPassword == "") || (c.SMTPUsername != "" && !c.SMTPUseTLS) {
		return nil, ErrInvalid
	}
	return &sender{c: c}, nil
}
func validAddress(s string) bool {
	a, e := mail.ParseAddress(s)
	return e == nil && a.Address != "" && !strings.ContainsAny(s, "\r\n")
}
func (s *sender) Send(ctx context.Context, m Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.resend == nil && s.c.SMTPHost == "" {
		return ErrDisabled
	}
	if len(m.To) == 0 || strings.ContainsAny(m.Subject, "\r\n") {
		return ErrInvalid
	}
	for _, a := range append(append([]string{}, m.To...), m.CC...) {
		if !validAddress(a) {
			return ErrInvalid
		}
	}
	for _, a := range m.Attachments {
		if a.Filename == "" || strings.ContainsAny(a.Filename, "\r\n") {
			return ErrInvalid
		}
	}
	ctx, cancel := context.WithTimeout(ctx, s.c.Timeout)
	defer cancel()
	if s.resend != nil {
		p := &resend.SendEmailRequest{From: s.c.ResendFrom, To: m.To, Cc: m.CC, ReplyTo: s.c.ReplyTo, Subject: m.Subject, Text: m.Text, Html: m.HTML}
		for _, a := range m.Attachments {
			p.Attachments = append(p.Attachments, &resend.Attachment{Filename: a.Filename, Content: a.Content})
		}
		_, err := s.resend.Emails.SendWithContext(ctx, p)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return ErrDelivery
		}
		return nil
	}
	if err := s.smtp(ctx, m); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
			return context.DeadlineExceeded
		}
		return ErrDelivery
	}
	return nil
}
func (s *sender) smtp(ctx context.Context, m Message) error {
	data, err := s.mime(m)
	if err != nil {
		return err
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(s.c.SMTPHost, strconv.Itoa(s.c.SMTPPort)))
	if err != nil {
		return err
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	cl, err := smtp.NewClient(conn, s.c.SMTPHost)
	if err != nil {
		return err
	}
	defer cl.Close()
	if s.c.SMTPUseTLS {
		tlsConfig := &tls.Config{ServerName: s.c.SMTPHost, MinVersion: tls.VersionTLS12}
		if s.tlsConfig != nil {
			tlsConfig = s.tlsConfig
		}
		if err = cl.StartTLS(tlsConfig); err != nil {
			return err
		}
	}
	if s.c.SMTPUsername != "" {
		if err = cl.Auth(smtp.PlainAuth("", s.c.SMTPUsername, s.c.SMTPPassword, s.c.SMTPHost)); err != nil {
			return err
		}
	}
	from, _ := mail.ParseAddress(s.c.SMTPFrom)
	if err = cl.Mail(from.Address); err != nil {
		return err
	}
	for _, v := range append(append([]string{}, m.To...), m.CC...) {
		a, _ := mail.ParseAddress(v)
		if err = cl.Rcpt(a.Address); err != nil {
			return err
		}
	}
	w, err := cl.Data()
	if err != nil {
		return err
	}
	if _, err = w.Write(data); err != nil {
		return err
	}
	if err = w.Close(); err != nil {
		return err
	}
	_ = cl.Quit()
	return nil
}
func (s *sender) mime(m Message) ([]byte, error) {
	var b bytes.Buffer
	fmt.Fprintf(&b, "From: %s\r\nTo: %s\r\n", formatAddress(s.c.SMTPFrom), formatAddresses(m.To))
	if len(m.CC) > 0 {
		fmt.Fprintf(&b, "Cc: %s\r\n", formatAddresses(m.CC))
	}
	if s.c.ReplyTo != "" {
		fmt.Fprintf(&b, "Reply-To: %s\r\n", formatAddress(s.c.ReplyTo))
	}
	fmt.Fprintf(&b, "Subject: %s\r\nMIME-Version: 1.0\r\n", encodeSubject(m.Subject))
	mixed := multipart.NewWriter(&b)
	fmt.Fprintf(&b, "Content-Type: multipart/mixed; boundary=%q\r\n\r\n", mixed.Boundary())
	h := textproto.MIMEHeader{}
	var alt bytes.Buffer
	aw := multipart.NewWriter(&alt)
	h.Set("Content-Type", fmt.Sprintf("multipart/alternative; boundary=%q", aw.Boundary()))
	p, e := mixed.CreatePart(h)
	if e != nil {
		return nil, e
	}
	for _, part := range []struct{ typ, body string }{{"text/plain", m.Text}, {"text/html", m.HTML}} {
		if part.typ == "text/html" && part.body == "" {
			continue
		}
		ph := textproto.MIMEHeader{}
		ph.Set("Content-Type", part.typ+"; charset=utf-8")
		ph.Set("Content-Transfer-Encoding", "base64")
		pw, e := aw.CreatePart(ph)
		if e != nil {
			return nil, e
		}
		writeBase64(pw, []byte(part.body))
	}
	aw.Close()
	p.Write(alt.Bytes())
	for _, a := range m.Attachments {
		h := textproto.MIMEHeader{}
		typ := a.ContentType
		if _, _, e := mime.ParseMediaType(typ); e != nil {
			typ = "application/octet-stream"
		}
		h.Set("Content-Type", typ)
		h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": a.Filename}))
		h.Set("Content-Transfer-Encoding", "base64")
		p, e := mixed.CreatePart(h)
		if e != nil {
			return nil, e
		}
		writeBase64(p, a.Content)
	}
	mixed.Close()
	for _, line := range bytes.Split(b.Bytes(), []byte("\r\n")) {
		if len(line) > 998 {
			return nil, ErrInvalid
		}
	}
	return b.Bytes(), nil
}

// encodeSubject keeps encoded words and physical lines short without splitting UTF-8.
func encodeSubject(subject string) string {
	if subject == "" {
		return ""
	}
	var words []string
	var chunk strings.Builder
	for _, r := range subject {
		if chunk.Len()+len(string(r)) > 30 {
			words = append(words, "=?utf-8?b?"+base64.StdEncoding.EncodeToString([]byte(chunk.String()))+"?=")
			chunk.Reset()
		}
		chunk.WriteRune(r)
	}
	if chunk.Len() > 0 {
		words = append(words, "=?utf-8?b?"+base64.StdEncoding.EncodeToString([]byte(chunk.String()))+"?=")
	}
	return strings.Join(words, "\r\n ")
}
func writeBase64(w interface{ Write([]byte) (int, error) }, data []byte) {
	for len(data) > 0 {
		n := 57
		if n > len(data) {
			n = len(data)
		}
		buf := make([]byte, base64.StdEncoding.EncodedLen(n))
		base64.StdEncoding.Encode(buf, data[:n])
		w.Write(buf)
		w.Write([]byte("\r\n"))
		data = data[n:]
	}
}

func formatAddress(value string) string {
	a, err := mail.ParseAddress(value)
	if err != nil {
		return value
	}
	return a.String()
}
func formatAddresses(values []string) string {
	result := make([]string, len(values))
	for i, v := range values {
		result[i] = formatAddress(v)
	}
	return strings.Join(result, ",\r\n ")
}
