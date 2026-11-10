// Package mcp is the MCP connector. This file holds the stateless confirm
// tokens that gate emailing and deleting tools: a token binds an action, its
// canonical payload and an expiry, so it cannot be replayed for another
// action or a changed payload, and it dies after five minutes.
package mcp

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

// confirmTTL is a confirm token's lifetime (Python's _TTL_SECONDS).
const confirmTTL = 300 * time.Second

// ConfirmError is a confirm token that is malformed, expired or not for this
// action and payload. Its text is the sentence a tool result carries.
type ConfirmError struct{ msg string }

func (e *ConfirmError) Error() string { return e.msg }

// Confirmer makes and verifies confirm tokens under MCP_CONFIRM_SECRET.
type Confirmer struct{ secret []byte }

// NewConfirmer returns a Confirmer keyed with secret.
func NewConfirmer(secret string) *Confirmer { return &Confirmer{secret: []byte(secret)} }

// canonical is Python's json.dumps(sort_keys=True, separators=(",", ":")) with
// ensure_ascii: sorted keys, no spaces, every non-ASCII rune as \uXXXX, and
// no HTML escaping. Go sorts map keys by byte, which for UTF-8 is code point
// order, as Python does.
func canonical(action string, payload map[string]string, expiry int64) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	// Encoding strings and a map of strings cannot fail.
	_ = enc.Encode(map[string]any{"action": action, "payload": payload, "expiry": expiry})
	raw := bytes.TrimRight(buf.Bytes(), "\n")
	var out bytes.Buffer
	for _, r := range string(raw) {
		switch {
		case r < 0x80:
			out.WriteRune(r)
		case r >= 0x10000:
			hi, lo := utf16.EncodeRune(r)
			fmt.Fprintf(&out, "\\u%04x\\u%04x", hi, lo)
		default:
			fmt.Fprintf(&out, "\\u%04x", r)
		}
	}
	return out.Bytes()
}

func (c *Confirmer) sign(msg []byte) string {
	mac := hmac.New(sha256.New, c.secret)
	mac.Write(msg)
	return hex.EncodeToString(mac.Sum(nil))
}

// MakeConfirm returns "<expiry>.<hex HMAC-SHA256>" for the action and payload,
// valid for 300 seconds from now.
func (c *Confirmer) MakeConfirm(action string, payload map[string]string, now time.Time) string {
	expiry := now.Add(confirmTTL).Unix()
	return strconv.FormatInt(expiry, 10) + "." + c.sign(canonical(action, payload, expiry))
}

// VerifyConfirm checks token against the action and payload. The expiry is
// checked before the signature, as in Python, so a stale token always says
// "expired".
func (c *Confirmer) VerifyConfirm(token, action string, payload map[string]string, now time.Time) error {
	expiryStr, sig, ok := strings.Cut(token, ".")
	if !ok {
		return &ConfirmError{"malformed confirm_token"}
	}
	expiry, err := strconv.ParseInt(expiryStr, 10, 64)
	if err != nil {
		return &ConfirmError{"malformed confirm_token"}
	}
	if now.Unix() > expiry {
		return &ConfirmError{"confirm_token expired — re-request the action"}
	}
	expected := c.sign(canonical(action, payload, expiry))
	if !hmac.Equal([]byte(expected), []byte(sig)) {
		return &ConfirmError{"confirm_token does not match this action/payload"}
	}
	return nil
}

// spentKey is the canonical identity of a confirm token that verified, for
// the spent-token record (Go change 4): the hex SHA-256 of its decoded MAC
// bytes, and its expiry. Two spellings of one token share it: the expiry
// part parses leniently ("+1700000300", "01700000300" verify as
// "1700000300"), and the MAC binds the parsed expiry, so the MAC bytes
// alone identify the token. Hashing the raw text would let such a respelled
// token through a second time. A hex digest in either case decodes to the
// same bytes, so the key holds even if verification ever accepted upper case.
func spentKey(token string) (string, time.Time, error) {
	expiryStr, sig, ok := strings.Cut(token, ".")
	if !ok {
		return "", time.Time{}, &ConfirmError{"malformed confirm_token"}
	}
	expiry, err := strconv.ParseInt(expiryStr, 10, 64)
	if err != nil {
		return "", time.Time{}, &ConfirmError{"malformed confirm_token"}
	}
	mac, err := hex.DecodeString(sig)
	if err != nil {
		return "", time.Time{}, &ConfirmError{"malformed confirm_token"}
	}
	sum := sha256.Sum256(mac)
	return hex.EncodeToString(sum[:]), time.Unix(expiry, 0), nil
}
