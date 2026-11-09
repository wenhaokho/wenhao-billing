package platform

import (
	"encoding/json"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
)

// Email is an email address in a request, as Python's EmailStr (pydantic and
// email-validator, deliverability off) took it: surrounding space trimmed,
// the domain lower-cased, the local part kept as written.
type Email string

const emailSentence = "value is not a valid email address"

// reservedTLDs are the special-use last labels email-validator refuses.
var reservedTLDs = []string{"arpa", "invalid", "local", "localhost", "onion", "test"}

// NormalizeEmail is s as Python stored it, and whether Python accepted it at
// all. It keeps the rules of email-validator the owner can hit: one @; a
// local part of 1 to 64 bytes without whitespace; a domain of at least two
// labels, each 1 to 63 letters (Unicode included), digits and inner hyphens,
// the last not a special-use name.
func NormalizeEmail(s string) (string, bool) {
	s = strings.TrimSpace(s)
	local, domain, ok := strings.Cut(s, "@")
	if !ok || strings.Contains(domain, "@") {
		return "", false
	}
	if local == "" || len(local) > 64 || strings.ContainsFunc(local, unicode.IsSpace) {
		return "", false
	}
	domain = strings.ToLower(domain)
	labels := strings.Split(domain, ".")
	if len(labels) < 2 || slices.ContainsFunc(labels, badLabel) {
		return "", false
	}
	if slices.Contains(reservedTLDs, labels[len(labels)-1]) {
		return "", false
	}
	return local + "@" + domain, true
}

func badLabel(l string) bool {
	if n := utf8.RuneCountInString(l); n < 1 || n > 63 || l[0] == '-' || l[len(l)-1] == '-' {
		return true
	}
	return strings.ContainsFunc(l, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' })
}

// UnmarshalJSON reads an address and normalises it; a bad one is the 422
// "value is not a valid email address" at its field.
func (e *Email) UnmarshalJSON(b []byte) error {
	var s string
	_ = json.Unmarshal(b, &s) // not a string: s stays "", which is refused
	n, ok := NormalizeEmail(s)
	if !ok {
		// A string type has no room for a flag: the refusal is marked in the
		// value, for Resolve. No address holds a NUL.
		*e = refusedEmail
		return errInvalid(emailSentence)
	}
	*e = Email(n)
	return nil
}

const refusedEmail Email = "\x00"

func (e *Email) takeRefusal() (refusal, bool) {
	if *e != refusedEmail {
		return refusal{}, false
	}
	*e = ""
	return refusal{sentence: emailSentence}, true
}

// Resolve reports a refused address at its field.
func (e *Email) Resolve(_ huma.Context, path *huma.PathBuffer) []error {
	return resolveRefusal(e, path)
}

// Schema describes Email to OpenAPI as a string. No "email" format: Huma
// would check it by RFC 5322, which is not Python's rule.
func (Email) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{Type: huma.TypeString, Examples: []any{"ap@example.com"}}
}

// ParseCcEmails is Python's CcEmails (schemas/email.py): a free-text list
// split on commas, semicolons and whitespace, blanks dropped, each address
// checked as an email, a repeat (any case) dropped so the first spelling
// stays, the rest joined with ", ". No address is nil. A bad one is the 422
// "invalid email address: <addr>" at cc_email.
func ParseCcEmails(s string) (*string, error) {
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == ';' || unicode.IsSpace(r) || (r >= 0x1c && r <= 0x1f)
	})
	var kept []string
	seen := map[string]bool{}
	for _, addr := range parts {
		if _, ok := NormalizeEmail(addr); !ok {
			return nil, FieldError("cc_email", "invalid email address: "+addr)
		}
		if k := strings.ToLower(addr); !seen[k] {
			seen[k] = true
			kept = append(kept, addr)
		}
	}
	if len(kept) == 0 {
		return nil, nil
	}
	joined := strings.Join(kept, ", ")
	return &joined, nil
}
