package platform

import (
	"github.com/LOQATO/gokit/apierr"
	"github.com/LOQATO/gokit/i18n"
)

// Err is a handler's error: the HTTP status, a machine code, and Python's
// sentence verbatim as detail (the frontend shows it as is).
func Err(status int, code, detail string) error {
	return apierr.New(status, code, i18n.T(detail))
}

// Catalog is empty on purpose: the API speaks English only, so the sentence
// in every error is the one the Python backend sent, whatever the
// Accept-Language header says.
var Catalog = &i18n.Catalog{
	Sentences: map[string]string{},
	Words:     map[string]string{},
	Nouns:     map[string]string{},
}
