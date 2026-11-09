// Package apierr is the one error type handlers return and the one shape
// every error response takes, in the request's language.
//
// A handler returns *Error. Install makes Huma render it, and every error
// Huma raises itself (validation, a bad body), in the chosen Format and the
// language the request names. NotFound and MethodNotAllowed do the same for
// the router. Any other error is a 500 whose body names nothing about it.
package apierr

import (
	"net/http"

	"github.com/LOQATO/gokit/i18n"
)

// Detail is one problem with one field of the request.
type Detail struct {
	Field   string
	Message i18n.Text
}

// Error is an error a client may read: a stable code, a sentence and field
// details. Its Error() is the English sentence, for logs.
type Error struct {
	Status  int
	Code    string
	Message i18n.Text
	Details []Detail
}

// New builds an Error.
func New(status int, code string, msg i18n.Text, details ...Detail) *Error {
	return &Error{Status: status, Code: code, Message: msg, Details: details}
}

func (e *Error) Error() string { return e.Message.String() }

// Format is the JSON shape an error response takes.
type Format int

const (
	// ProblemJSON is RFC 9457 (application/problem+json) with a "code"
	// member: {"title", "status", "detail", "code", "errors": [...]}.
	ProblemJSON Format = iota
	// Envelope is TAGARA's shape:
	// {"error": {"code", "message", "details": [{"field", "message"}]}}.
	Envelope
)

// Codes are the error codes for errors the framework raises rather than a
// handler. A project may replace entries before calling Install.
var Codes = map[int]string{
	http.StatusBadRequest:            "bad_request",
	http.StatusUnauthorized:          "unauthorized",
	http.StatusForbidden:             "forbidden",
	http.StatusNotFound:              "not_found",
	http.StatusMethodNotAllowed:      "method_not_allowed",
	http.StatusRequestEntityTooLarge: "payload_too_large",
	http.StatusUnsupportedMediaType:  "unsupported_media_type",
	http.StatusUnprocessableEntity:   "validation_error",
	http.StatusInternalServerError:   "internal_error",
}

// DefaultMessages are the sentences for errors the framework raises. A
// project may replace entries before calling Install; each needs a catalog
// entry for its Indonesian.
var DefaultMessages = map[int]i18n.Text{
	http.StatusBadRequest:            i18n.T("Bad request"),
	http.StatusUnauthorized:          i18n.T("Not authenticated"),
	http.StatusForbidden:             i18n.T("Forbidden"),
	http.StatusNotFound:              i18n.T("Not found"),
	http.StatusMethodNotAllowed:      i18n.T("Method not allowed"),
	http.StatusRequestEntityTooLarge: i18n.T("Request body too large"),
	http.StatusUnsupportedMediaType:  i18n.T("Unsupported media type"),
	http.StatusUnprocessableEntity:   i18n.T("Request validation failed"),
	http.StatusInternalServerError:   i18n.T("An unexpected error occurred"),
}

// Sentences are the templates of DefaultMessages, for a project's catalog
// check: i18ntest.Scan(t, root, catalog, apierr.Sentences()...).
func Sentences() []string {
	out := make([]string, 0, len(DefaultMessages))
	for _, m := range DefaultMessages {
		out = append(out, m.Template)
	}
	return out
}

func framework(status int) *Error {
	code, ok := Codes[status]
	if !ok {
		code = "error"
	}
	msg, ok := DefaultMessages[status]
	if !ok {
		msg = i18n.T(http.StatusText(status))
	}
	return &Error{Status: status, Code: code, Message: msg}
}
