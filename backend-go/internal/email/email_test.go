package email

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"strings"
	"testing"
	"time"
)

func TestDisabled(t *testing.T) {
	s, e := New(Config{})
	if e != nil {
		t.Fatal(e)
	}
	if !errors.Is(s.Send(context.Background(), Message{}), ErrDisabled) {
		t.Fatal("disabled delivery must be explicit")
	}
}
func TestResendPrecedenceAndPayload(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/emails" || r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("request path/auth")
		}
		json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"id"}`))
	}))
	defer srv.Close()
	s, e := New(Config{ResendKey: "secret", ResendURL: srv.URL, ResendFrom: "Sender <sender@example.com>", ReplyTo: "reply@example.com", SMTPHost: "127.0.0.1", SMTPPort: 1, Timeout: time.Second})
	if e != nil {
		t.Fatal(e)
	}
	e = s.Send(context.Background(), Message{To: []string{"to@example.com"}, CC: []string{"cc@example.com"}, Subject: "subject", Text: "body", HTML: "<p>body</p>", Attachments: []Attachment{{Filename: "a.pdf", Content: []byte("pdf"), ContentType: "application/pdf"}}})
	if e != nil {
		t.Fatal(e)
	}
	if got["from"] != "Sender <sender@example.com>" || got["reply_to"] != "reply@example.com" || got["text"] != "body" || got["html"] != "<p>body</p>" || got["subject"] != "subject" {
		t.Fatalf("payload: %#v", got)
	}
	if got["to"].([]any)[0] != "to@example.com" || got["cc"].([]any)[0] != "cc@example.com" || len(got["attachments"].([]any)[0].(map[string]any)["content"].([]any)) != 3 {
		t.Fatalf("payload: %#v", got)
	}
}
func TestValidation(t *testing.T) {
	for _, c := range []Config{{SMTPHost: "host", SMTPPort: 25, SMTPUsername: "user", SMTPPassword: "pass", SMTPFrom: "a@example.com"}, {SMTPHost: "host", SMTPPort: 25, SMTPUseTLS: true, SMTPUsername: "user"}, {ResendKey: "key", ResendURL: "http://example.com", ResendFrom: "a@example.com"}, {SMTPHost: "host", SMTPPort: -1}} {
		if _, e := New(c); e == nil {
			t.Errorf("accepted invalid config: %#v", c)
		}
	}
}
func TestErrorRedactionAndCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(422)
		w.Write([]byte(`{"message":"secret body@example.com"}`))
	}))
	defer srv.Close()
	s, e := New(Config{ResendKey: "secret", ResendURL: srv.URL, ResendFrom: "a@example.com"})
	if e != nil {
		t.Fatal(e)
	}
	e = s.Send(context.Background(), Message{To: []string{"body@example.com"}, Text: "secret"})
	if e == nil || strings.Contains(e.Error(), "secret") || strings.Contains(e.Error(), "body@example.com") {
		t.Fatalf("unsafe error: %v", e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if !errors.Is(s.Send(ctx, Message{}), context.Canceled) {
		t.Fatal("cancellation not returned")
	}
}

func TestSMTPDelivery(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	data := make(chan string, 1)
	go func() {
		c, e := ln.Accept()
		if e != nil {
			return
		}
		defer c.Close()
		r := bufio.NewReader(c)
		fmt.Fprint(c, "220 local ESMTP\r\n")
		var message strings.Builder
		inData := false
		for {
			line, e := r.ReadString('\n')
			if e != nil {
				return
			}
			if inData {
				if line == ".\r\n" {
					inData = false
					fmt.Fprint(c, "250 queued\r\n")
					continue
				}
				message.WriteString(line)
				continue
			}
			switch {
			case strings.HasPrefix(line, "EHLO"):
				fmt.Fprint(c, "250-local\r\n250 OK\r\n")
			case strings.HasPrefix(line, "MAIL"), strings.HasPrefix(line, "RCPT"):
				fmt.Fprint(c, "250 OK\r\n")
			case strings.HasPrefix(line, "DATA"):
				inData = true
				fmt.Fprint(c, "354 go\r\n")
			case strings.HasPrefix(line, "QUIT"):
				fmt.Fprint(c, "221 bye\r\n")
				data <- message.String()
				return
			default:
				fmt.Fprint(c, "500 invalid\r\n")
			}
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	s, e := New(Config{SMTPHost: "127.0.0.1", SMTPPort: port, SMTPFrom: "Sender <sender@example.com>", ReplyTo: "reply@example.com"})
	if e != nil {
		t.Fatal(e)
	}
	e = s.Send(context.Background(), Message{To: []string{"to@example.com"}, CC: []string{"cc@example.com"}, Subject: "subject", Text: "body", HTML: "<p>body</p>", Attachments: []Attachment{{Filename: "test.pdf", Content: []byte("pdf"), ContentType: "application/pdf"}}})
	if e != nil {
		t.Fatal(e)
	}
	body := <-data
	for _, v := range []string{"reply@example.com", "cc@example.com", "text/html", "application/pdf", "cGRm"} {
		if !strings.Contains(body, v) {
			t.Errorf("missing %s", v)
		}
	}
}
func TestResendTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(100 * time.Millisecond) }))
	defer srv.Close()
	s, e := New(Config{ResendKey: "secret", ResendURL: srv.URL, ResendFrom: "a@example.com", Timeout: 10 * time.Millisecond})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Send(context.Background(), Message{To: []string{"a@example.com"}}); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatalf("timeout: %v", e)
	}
}

func TestSMTPStartTLSAuth(t *testing.T) {
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true}
	der, e := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	pool := x509.NewCertPool()
	parsed, e := x509.ParseCertificate(der)
	if e != nil {
		t.Fatal(e)
	}
	pool.AddCert(parsed)
	for _, mode := range []string{"success", "auth-reject", "cert-reject"} {
		t.Run(mode, func(t *testing.T) {
			ln, e := net.Listen("tcp", "127.0.0.1:0")
			if e != nil {
				t.Fatal(e)
			}
			defer ln.Close()
			auth := make(chan string, 1)
			go func() {
				c, e := ln.Accept()
				if e != nil {
					return
				}
				defer c.Close()
				fmt.Fprint(c, "220 local ESMTP\r\n")
				r := bufio.NewReader(c)
				for {
					line, e := r.ReadString('\n')
					if e != nil {
						return
					}
					switch {
					case strings.HasPrefix(line, "EHLO"):
						fmt.Fprint(c, "250-local\r\n250-STARTTLS\r\n250 AUTH PLAIN\r\n")
					case strings.HasPrefix(line, "STARTTLS"):
						fmt.Fprint(c, "220 ready\r\n")
						tc := tls.Server(c, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
						if tc.Handshake() != nil {
							return
						}
						c = tc
						r = bufio.NewReader(c)
					case strings.HasPrefix(line, "AUTH PLAIN"):
						auth <- line
						if mode == "auth-reject" {
							fmt.Fprint(c, "535 secret password\r\n")
							return
						}
						fmt.Fprint(c, "235 authenticated\r\n")
					case strings.HasPrefix(line, "MAIL"), strings.HasPrefix(line, "RCPT"):
						fmt.Fprint(c, "250 OK\r\n")
					case strings.HasPrefix(line, "DATA"):
						fmt.Fprint(c, "354 go\r\n")
						for {
							l, e := r.ReadString('\n')
							if e != nil {
								return
							}
							if l == ".\r\n" {
								break
							}
						}
						fmt.Fprint(c, "250 queued\r\n")
					case strings.HasPrefix(line, "QUIT"):
						fmt.Fprint(c, "221 bye\r\n")
						return
					default:
						fmt.Fprint(c, "500 invalid\r\n")
					}
				}
			}()
			s, e := New(Config{SMTPHost: "127.0.0.1", SMTPPort: ln.Addr().(*net.TCPAddr).Port, SMTPFrom: "a@example.com", SMTPUseTLS: true, SMTPUsername: "user", SMTPPassword: "password"})
			if e != nil {
				t.Fatal(e)
			}
			if mode != "cert-reject" {
				s.(*sender).tlsConfig = &tls.Config{RootCAs: pool, ServerName: "127.0.0.1", MinVersion: tls.VersionTLS12}
			}
			e = s.Send(context.Background(), Message{To: []string{"b@example.com"}, Text: "body"})
			if mode != "success" {
				if !errors.Is(e, ErrDelivery) || strings.Contains(e.Error(), "password") {
					t.Fatalf("unsafe error %v", e)
				}
			} else if e != nil {
				t.Fatal(e)
			}
			if mode == "cert-reject" {
				select {
				case <-auth:
					t.Fatal("auth sent before certificate verification")
				default:
				}
				return
			}
			a := <-auth
			if !strings.Contains(a, base64.StdEncoding.EncodeToString([]byte("\x00user\x00password"))) {
				t.Fatal("missing auth")
			}
		})
	}
}

func TestMIMERoundTripAndLineLengths(t *testing.T) {
	s := &sender{c: Config{SMTPFrom: "Sender <sender@example.com>", ReplyTo: "reply@example.com"}}
	pdf := bytes.Repeat([]byte("%PDF realistic attachment data\x00\xff"), 500)
	text := strings.Repeat("long text café ", 200)
	html := strings.Repeat("<p>hello</p>", 150)
	subject := strings.Repeat("Invoice subject ASCII ", 40)
	data, e := s.mime(Message{To: []string{"to@example.com"}, Subject: subject, Text: text, HTML: html, Attachments: []Attachment{{Filename: "invoice.pdf", Content: pdf, ContentType: "application/pdf"}}})
	if e != nil {
		t.Fatal(e)
	}
	for _, line := range strings.Split(string(data), "\r\n") {
		if len(line) > 998 {
			t.Fatalf("line exceeds 998: %d", len(line))
		}
	}
	msg, e := mail.ReadMessage(bytes.NewReader(data))
	if e != nil {
		t.Fatal(e)
	}
	decoded, e := new(mime.WordDecoder).DecodeHeader(msg.Header.Get("Subject"))
	if e != nil || decoded != subject {
		t.Fatalf("subject round trip: %q %v", decoded, e)
	}
	_, params, e := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if e != nil {
		t.Fatal(e)
	}
	mixed := multipart.NewReader(msg.Body, params["boundary"])
	alt, e := mixed.NextPart()
	if e != nil {
		t.Fatal(e)
	}
	_, params, e = mime.ParseMediaType(alt.Header.Get("Content-Type"))
	if e != nil {
		t.Fatal(e)
	}
	ar := multipart.NewReader(alt, params["boundary"])
	for _, want := range []string{text, html} {
		p, e := ar.NextPart()
		if e != nil {
			t.Fatal(e)
		}
		raw, e := io.ReadAll(p)
		if e != nil {
			t.Fatal(e)
		}
		for _, line := range strings.Split(string(raw), "\r\n") {
			if len(line) > 76 {
				t.Fatalf("base64 line exceeds 76: %d", len(line))
			}
		}
		got, e := io.ReadAll(base64.NewDecoder(base64.StdEncoding, bytes.NewReader(raw)))
		if e != nil || string(got) != want {
			t.Fatal("text round trip", e)
		}
	}
	p, e := mixed.NextPart()
	if e != nil {
		t.Fatal(e)
	}
	raw, e := io.ReadAll(p)
	if e != nil {
		t.Fatal(e)
	}
	for _, line := range strings.Split(string(raw), "\r\n") {
		if len(line) > 76 {
			t.Fatalf("attachment line exceeds 76: %d", len(line))
		}
	}
	got, e := io.ReadAll(base64.NewDecoder(base64.StdEncoding, bytes.NewReader(raw)))
	if e != nil || !bytes.Equal(got, pdf) {
		t.Fatal("attachment round trip", e)
	}
}
func TestSMTPQuitFailureAfterAcceptance(t *testing.T) {
	for _, drop := range []bool{false, true} {
		t.Run(fmt.Sprint(drop), func(t *testing.T) {
			ln, e := net.Listen("tcp", "127.0.0.1:0")
			if e != nil {
				t.Fatal(e)
			}
			defer ln.Close()
			go func() {
				c, e := ln.Accept()
				if e != nil {
					return
				}
				defer c.Close()
				r := bufio.NewReader(c)
				fmt.Fprint(c, "220 local\r\n")
				for {
					l, e := r.ReadString('\n')
					if e != nil {
						return
					}
					switch {
					case strings.HasPrefix(l, "EHLO"):
						fmt.Fprint(c, "250 local\r\n")
					case strings.HasPrefix(l, "MAIL"), strings.HasPrefix(l, "RCPT"):
						fmt.Fprint(c, "250 ok\r\n")
					case strings.HasPrefix(l, "DATA"):
						fmt.Fprint(c, "354 go\r\n")
						for {
							l, e = r.ReadString('\n')
							if e != nil {
								return
							}
							if l == ".\r\n" {
								break
							}
						}
						fmt.Fprint(c, "250 accepted\r\n")
					case strings.HasPrefix(l, "QUIT"):
						if !drop {
							fmt.Fprint(c, "500 failed\r\n")
						}
						return
					}
				}
			}()
			s, e := New(Config{SMTPHost: "127.0.0.1", SMTPPort: ln.Addr().(*net.TCPAddr).Port, SMTPFrom: "a@example.com"})
			if e != nil {
				t.Fatal(e)
			}
			if e = s.Send(context.Background(), Message{To: []string{"b@example.com"}}); e != nil {
				t.Fatalf("accepted message reported failure: %v", e)
			}
		})
	}
}

func TestSMTPTimeoutAndCancellation(t *testing.T) {
	for _, cancelEarly := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelEarly), func(t *testing.T) {
			ln, e := net.Listen("tcp", "127.0.0.1:0")
			if e != nil {
				t.Fatal(e)
			}
			defer ln.Close()
			accepted := make(chan struct{})
			go func() {
				c, e := ln.Accept()
				if e != nil {
					return
				}
				defer c.Close()
				close(accepted)
				io.Copy(io.Discard, c)
			}()
			s, e := New(Config{SMTPHost: "127.0.0.1", SMTPPort: ln.Addr().(*net.TCPAddr).Port, SMTPFrom: "a@example.com", Timeout: 100 * time.Millisecond})
			if e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if cancelEarly {
				go func() { <-accepted; cancel() }()
			}
			e = s.Send(ctx, Message{To: []string{"b@example.com"}})
			want := context.DeadlineExceeded
			if cancelEarly {
				want = context.Canceled
			}
			if !errors.Is(e, want) {
				t.Fatalf("got %v want %v", e, want)
			}
		})
	}
}

func TestSubjectUnicodeRoundTrip(t *testing.T) {
	want := strings.Repeat("Tagihan café 中文 ", 40)
	got, e := new(mime.WordDecoder).DecodeHeader(strings.ReplaceAll(encodeSubject(want), "\r\n ", " "))
	if e != nil || got != want {
		t.Fatalf("subject: %q %v", got, e)
	}
}
