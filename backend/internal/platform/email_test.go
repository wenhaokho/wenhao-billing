package platform

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
)

// The values were observed from the frozen Python (pydantic EmailStr,
// deliverability off) on 2026-11-09.
func TestNormalizeEmail(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"AP@Beta.COM":   "AP@beta.com",
		" a@b.co ":      "a@b.co", //nolint:gocritic // the surrounding space is the case
		"x@exämple.com": "x@exämple.com",
		"Ä@example.com": "Ä@example.com",
	} {
		if got, ok := NormalizeEmail(in); !ok || got != want {
			t.Errorf("%q: %q %v, want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"a@localhost", "a@b", "a@example.test", "not-an-email", "a b@x.com", ""} {
		if got, ok := NormalizeEmail(in); ok {
			t.Errorf("%q accepted as %q", in, got)
		}
	}
}

type emailIn struct {
	Body emailBody
}

type emailBody struct {
	Email Email `json:"email"`
}

type emailOut struct {
	Body emailBody
}

type problem struct {
	Detail string `json:"detail"`
	Errors []struct {
		Location string `json:"location"`
		Message  string `json:"message"`
	} `json:"errors"`
}

func decodeProblem(t *testing.T, b []byte) problem {
	t.Helper()
	var p problem
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatalf("%v: %s", err, b)
	}
	return p
}

func TestEmailFieldIs422(t *testing.T) {
	t.Parallel()
	h, api := NewServer(Config{Environment: "test"}, Deps{})
	huma.Register(api, huma.Operation{OperationID: "email", Method: http.MethodPost, Path: "/email", Tags: []string{PublicTag}},
		func(_ context.Context, in *emailIn) (*emailOut, error) {
			return &emailOut{Body: in.Body}, nil
		})
	rec := do(t, h, http.MethodPost, "/email", `{"email":"nope"}`)
	if p := decodeProblem(t, rec.Body.Bytes()); rec.Code != 422 || p.Detail != "value is not a valid email address" {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
	rec = do(t, h, http.MethodPost, "/email", `{"email":"A@X.COM"}`)
	if rec.Code != 200 || rec.Body.String() != `{"email":"A@x.com"}`+"\n" {
		t.Errorf("%d %q", rec.Code, rec.Body)
	}
}

// Python's CcEmails (schemas/email.py) on the values of tests/integration/
// test_customer_cc.py and the split rule.
func TestParseCcEmails(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		" a@example.com;b@example.com,, A@example.com ": "a@example.com, b@example.com",
		"a@example.com b@example.com	c@example.com":     "a@example.com, b@example.com, c@example.com",
		"Me@Example.com":       "Me@Example.com",
		"x@y.co,X@Y.CO,x@y.co": "x@y.co",
	} {
		t.Run(in, func(t *testing.T) {
			t.Parallel()
			got, err := ParseCcEmails(in)
			if err != nil || got == nil || *got != want {
				t.Errorf("%q: %v %v, want %q", in, got, err, want)
			}
		})
	}
	for _, in := range []string{"", "  ", " ,; "} {
		t.Run("blank "+in, func(t *testing.T) {
			t.Parallel()
			if got, err := ParseCcEmails(in); err != nil || got != nil {
				t.Errorf("%q: %v %v, want nil", in, got, err)
			}
		})
	}
}

func TestParseCcEmailsRefusesTheFirstBadAddress(t *testing.T) {
	t.Parallel()
	_, err := ParseCcEmails("a@example.com, nope, worse")
	if err == nil || !strings.Contains(err.Error(), "invalid email address: nope") {
		t.Errorf("error %v", err)
	}
}
