package mcp_test

import (
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wenhaokho/wenhao-billing/backend/internal/mcp"
)

const confirmSecret = "unit-test-confirm-secret-0123456789"

func payload() map[string]string {
	return map[string]string{"target_id": "abc", "amount": "100.0000", "currency": "USD", "recipient": "a@b.com"}
}

func newConfirmer() *mcp.Confirmer { return mcp.NewConfirmer(confirmSecret) }

func at(sec int64) time.Time { return time.Unix(sec, 0) }

func wantErr(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("want error %q, got nil", want)
	}
	var ce *mcp.ConfirmError
	if !errors.As(err, &ce) {
		t.Fatalf("want *ConfirmError, got %T", err)
	}
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}

const (
	errMismatch = "confirm_token does not match this action/payload"
	errExpired  = "confirm_token expired — re-request the action"
)

func TestConfirmRoundTripOK(t *testing.T) {
	t.Parallel()
	c := newConfirmer()
	tok := c.MakeConfirm("send_invoice_email", payload(), at(1000))
	if err := c.VerifyConfirm(tok, "send_invoice_email", payload(), at(1100)); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tok, "1300.") {
		t.Fatalf("token %q does not carry expiry now+300", tok)
	}
}

func TestConfirmRejectsWrongAction(t *testing.T) {
	t.Parallel()
	c := newConfirmer()
	tok := c.MakeConfirm("send_invoice_email", payload(), at(1000))
	wantErr(t, c.VerifyConfirm(tok, "delete_invoice", payload(), at(1100)), errMismatch)
}

func TestConfirmRejectsMutatedPayload(t *testing.T) {
	t.Parallel()
	c := newConfirmer()
	tok := c.MakeConfirm("send_invoice_email", payload(), at(1000))
	mutated := payload()
	mutated["amount"] = "999.0000"
	wantErr(t, c.VerifyConfirm(tok, "send_invoice_email", mutated, at(1100)), errMismatch)
}

func TestConfirmRejectsExpired(t *testing.T) {
	t.Parallel()
	c := newConfirmer()
	tok := c.MakeConfirm("send_invoice_email", payload(), at(1000))
	wantErr(t, c.VerifyConfirm(tok, "send_invoice_email", payload(), at(11_000)), errExpired)
	// The expiry second itself is still good (Python: now > expiry).
	if err := c.VerifyConfirm(tok, "send_invoice_email", payload(), at(1300)); err != nil {
		t.Fatal(err)
	}
	wantErr(t, c.VerifyConfirm(tok, "send_invoice_email", payload(), at(1301)), errExpired)
}

// A token whose expiry has passed says "expired" even when its signature is
// also wrong: the expiry check comes first, as in Python.
func TestConfirmExpiredBeforeSignature(t *testing.T) {
	t.Parallel()
	c := newConfirmer()
	bad := strings.Repeat("0", 64)
	wantErr(t, c.VerifyConfirm("1000."+bad, "a", payload(), at(5000)), errExpired)
	wantErr(t, c.VerifyConfirm("9999999999."+bad, "a", payload(), at(5000)), errMismatch)
}

func TestConfirmMalformed(t *testing.T) {
	t.Parallel()
	for _, tok := range []string{"", "nodot", "abc.def", ".sig", "12x.sig"} {
		t.Run(tok, func(t *testing.T) {
			t.Parallel()
			wantErr(t, newConfirmer().VerifyConfirm(tok, "a", payload(), at(1)), "malformed confirm_token")
		})
	}
}

func TestConfirmWrongSecretRejected(t *testing.T) {
	t.Parallel()
	tok := mcp.NewConfirmer("some-other-secret-0123456789ab").MakeConfirm("a", payload(), at(1000))
	wantErr(t, newConfirmer().VerifyConfirm(tok, "a", payload(), at(1001)), errMismatch)
}

func TestConfirmPayloadOrderAndNonASCII(t *testing.T) {
	t.Parallel()
	c := newConfirmer()
	tok := c.MakeConfirm("x", map[string]string{"a": "1", "b": "é<&> 😀"}, at(1))
	if err := c.VerifyConfirm(tok, "x", map[string]string{"b": "é<&> 😀", "a": "1"}, at(2)); err != nil {
		t.Fatal(err)
	}
	wantErr(t, c.VerifyConfirm(tok, "x", map[string]string{"a": "1", "b": "e<&> 😀"}, at(2)), errMismatch)
}

func TestPythonMintedConfirmVerifies(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("testdata/python_tokens.json")
	if err != nil {
		t.Fatal(err)
	}
	var g struct {
		Secret  string            `json:"confirm_secret"`
		Confirm string            `json:"confirm"`
		Action  string            `json:"confirm_action"`
		Payload map[string]string `json:"confirm_payload"`
		Now     int64             `json:"confirm_now"`
	}
	if err := json.Unmarshal(raw, &g); err != nil {
		t.Fatal(err)
	}
	c := mcp.NewConfirmer(g.Secret)
	if err := c.VerifyConfirm(g.Confirm, g.Action, g.Payload, at(g.Now+10)); err != nil {
		t.Fatal(err)
	}
	// The payload is ASCII, so Go's canonical JSON equals Python's byte for byte.
	if got := c.MakeConfirm(g.Action, g.Payload, at(g.Now)); got != g.Confirm {
		t.Fatalf("Go minted %q, Python %q", got, g.Confirm)
	}
	if exp := strings.SplitN(g.Confirm, ".", 2)[0]; exp != strconv.FormatInt(g.Now+300, 10) {
		t.Fatalf("expiry %s", exp)
	}
}
