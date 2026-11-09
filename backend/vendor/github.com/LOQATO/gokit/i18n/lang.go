package i18n

import "strings"

// Lang is a language text is written in.
type Lang string

// The two languages TAGARA-shaped projects speak.
const (
	English    Lang = "en"
	Indonesian Lang = "id"
)

// Resolve reads an Accept-Language header. Only its first tag counts: "id"
// or "id-..." is Indonesian, anything else (including a missing header,
// "*", the retired code "in", garbage) is English. The header is untrusted.
func Resolve(acceptLanguage string) Lang {
	first, _, _ := strings.Cut(acceptLanguage, ",")
	first, _, _ = strings.Cut(first, ";")
	first = strings.ToLower(strings.TrimSpace(first))
	if first == "id" || strings.HasPrefix(first, "id-") {
		return Indonesian
	}
	return English
}
