package apierr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/validation"

	"github.com/LOQATO/gokit/i18n"
)

// Option adjusts Install.
type Option func(*settings)

// OnUnexpected receives the cause of every 500 the client sees only as
// "internal_error": the place to log it and report it to Sentry.
func OnUnexpected(fn func(ctx context.Context, err error)) Option {
	return func(s *settings) { s.onUnexpected = fn }
}

// PathStyle is how a field path Huma reports is shown in a detail's "field".
type PathStyle int

const (
	// HumaPath is Huma's own path without its location prefix:
	// "body.lines[0].amount" -> "lines[0].amount". The default.
	HumaPath PathStyle = iota
	// FullPath is the whole path with dots only, as pydantic writes it:
	// "passengers.2.date_of_birth".
	FullPath
	// LastSegment is the last part of the path: "date_of_birth", or the
	// index ("2") when the path ends in a list item.
	LastSegment
)

// DetailsKey names the member holding the Envelope's field details (the
// default is "details"). A project that sets it also gets the member
// omitted when there are no details; without this option it is always
// present, "[]" when empty. The OpenAPI schema follows the key and whether it
// is required. It has no effect under ProblemJSON, which has no such member.
func DetailsKey(name string) Option {
	return func(s *settings) { s.detailsKey = name }
}

// FieldPath chooses, per request, how the fields of Huma's own validation
// errors are written. fn must not be nil and must be fast; it is not called
// for errors raised without a request, which use HumaPath. Details a handler
// builds itself are never changed.
func FieldPath(fn func(r *http.Request) PathStyle) Option {
	return func(s *settings) { s.fieldPath = fn }
}

type settings struct {
	format       Format
	catalog      *i18n.Catalog
	onUnexpected func(context.Context, error)
	detailsKey   string
	fieldPath    func(*http.Request) PathStyle
}

var (
	current   atomic.Pointer[settings]
	hooksOnce sync.Once
)

// Install makes Huma render every error in format f, in the language the
// request's Accept-Language names, with c's Indonesian (c may be nil). The
// settings are process-wide: the last call wins. Huma's error constructors
// are replaced once and read the settings atomically, so calling Install
// again (each test does) never races a request in flight.
func Install(f Format, c *i18n.Catalog, opts ...Option) {
	s := &settings{format: f, catalog: c}
	for _, o := range opts {
		o(s)
	}
	current.Store(s)
	hooksOnce.Do(func() {
		huma.NewErrorWithContext = func(ctx huma.Context, status int, _ string, errs ...error) huma.StatusError {
			return response(ctx.Context(), requestOf(ctx), current.Load(), i18n.Resolve(ctx.Header("Accept-Language")), status, errs)
		}
		huma.NewError = func(status int, _ string, errs ...error) huma.StatusError {
			return response(context.Background(), nil, current.Load(), i18n.English, status, errs)
		}
	})
}

// requestOf rebuilds the parts of the request FieldPath may look at.
func requestOf(ctx huma.Context) *http.Request {
	u := ctx.URL()
	r := &http.Request{Method: ctx.Method(), URL: &u, Header: http.Header{}}
	ctx.EachHeader(func(name, value string) { r.Header.Add(name, value) })
	return r.WithContext(ctx.Context())
}

// NotFound answers a request no route matches, for the router (chi's
// NotFound).
func NotFound(f Format, c *i18n.Catalog) http.HandlerFunc {
	return writer(f, c, http.StatusNotFound)
}

// MethodNotAllowed answers a known path asked with the wrong method.
func MethodNotAllowed(f Format, c *i18n.Catalog) http.HandlerFunc {
	return writer(f, c, http.StatusMethodNotAllowed)
}

func writer(f Format, c *i18n.Catalog, status int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		st := &settings{format: f, catalog: c}
		if cur := current.Load(); cur != nil {
			st.detailsKey = cur.detailsKey // the router's answers follow DetailsKey too
		}
		resp := response(r.Context(), r, st, i18n.Resolve(r.Header.Get("Accept-Language")), status, nil)
		w.Header().Set("Content-Type", resp.(interface{ ContentType(string) string }).ContentType(""))
		w.WriteHeader(resp.GetStatus())
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// response decides what the client reads. A handler's *Error is shown as it
// is; anything else becomes the framework error for status, and an error
// that is neither an *Error nor a field detail is never described. The
// result is the body itself (Huma serialises its fields), carrying its status
// and content type.
func response(ctx context.Context, req *http.Request, s *settings, lang i18n.Lang, status int, errs []error) huma.StatusError {
	var e *Error
	for _, err := range errs {
		if errors.As(err, &e) {
			break
		}
	}
	if e == nil {
		if status < 400 {
			status = http.StatusInternalServerError
		}
		e = framework(status)
		for _, err := range errs {
			var d huma.ErrorDetailer
			if errors.As(err, &d) {
				detail := d.ErrorDetail()
				// The message is data (it may hold a user's value), never a template.
				e.Details = append(e.Details, Detail{Field: field(detail, s.pathStyle(req)), Message: i18n.T("{message}", "message", detail.Message)})
			} else if e.Status >= 500 && s.onUnexpected != nil {
				s.onUnexpected(ctx, err)
			}
		}
	}
	if s.format == ProblemJSON {
		return problem(e, s.catalog, lang)
	}
	return envelope(e, s.catalog, lang, s.detailsKey)
}

func (s *settings) pathStyle(req *http.Request) PathStyle {
	if s.fieldPath == nil || req == nil {
		return HumaPath
	}
	return s.fieldPath(req)
}

// missingPrefix and missingSuffix surround the property name in Huma's
// required-property message (validation.MsgExpectedRequiredProperty). Huma
// reports it at the parent object and carries the name only in the message.
var missingPrefix, missingSuffix, _ = strings.Cut(validation.MsgExpectedRequiredProperty, "%s")

// missingProperty is the name a required-property error is about.
func missingProperty(message string) (string, bool) {
	name, ok := strings.CutPrefix(message, missingPrefix)
	if !ok {
		return "", false
	}
	name, ok = strings.CutSuffix(name, missingSuffix)
	return name, ok && name != ""
}

// locationPrefixes are Huma's location roots.
var locationPrefixes = []string{"body", "query", "path", "header"}

// field drops Huma's location prefix ("body.amount" -> "amount") and writes
// the rest in style. Under FullPath and LastSegment a missing required
// property is reported at the property itself, not at its parent.
func field(d *huma.ErrorDetail, style PathStyle) string {
	location := d.Location
	missing, isMissing := "", false
	if style != HumaPath {
		missing, isMissing = missingProperty(d.Message)
	}
	for _, prefix := range locationPrefixes {
		if rest, ok := strings.CutPrefix(location, prefix+"."); ok {
			location = rest
			break
		}
		if location == prefix && isMissing { // the root itself has no path
			location = ""
			break
		}
	}
	if style == HumaPath {
		return location
	}
	// "a[2].b" -> "a.2.b"
	location = strings.NewReplacer("[", ".", "]", "").Replace(location)
	if isMissing {
		if style == LastSegment || location == "" {
			return missing
		}
		return location + "." + missing
	}
	if style == LastSegment {
		if i := strings.LastIndexByte(location, '.'); i >= 0 {
			return location[i+1:]
		}
	}
	return location
}

// marshal is json.Marshal without HTML escaping, so the bytes match what
// Huma's own encoder writes for a field without a MarshalJSON.
func marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

type envelopeDetail struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// EnvelopeBody is the Envelope shape. It is exported only so Huma can
// describe it in OpenAPI.
type EnvelopeBody struct {
	Err    EnvelopeError `json:"error"`
	status int
}

// EnvelopeError is the inside of EnvelopeBody.
type EnvelopeError struct {
	Code    string           `json:"code"`
	Message string           `json:"message"`
	Details []envelopeDetail `json:"details"`
	key     string           // DetailsKey; empty = "details", always present
}

// Schema describes the member under the key Install was given, so the
// OpenAPI document says what the API sends: "details" required (the default)
// or the DetailsKey member, optional. Huma calls it while the operations are
// registered, after Install.
func (EnvelopeError) Schema(r huma.Registry) *huma.Schema {
	key, optional := "details", false
	if s := current.Load(); s != nil && s.detailsKey != "" && s.detailsKey != "details" {
		key, optional = s.detailsKey, true
	}
	str := &huma.Schema{Type: huma.TypeString}
	list := r.Schema(reflect.TypeFor[[]envelopeDetail](), true, "")
	if optional {
		list = &huma.Schema{Type: huma.TypeArray, Items: r.Schema(reflect.TypeFor[envelopeDetail](), true, "")}
	}
	required := []string{"code", "message"}
	if !optional {
		required = append(required, key)
	}
	r.Map()["EnvelopeError"] = &huma.Schema{
		Type:                 huma.TypeObject,
		AdditionalProperties: false,
		Properties:           map[string]*huma.Schema{"code": str, "message": str, key: list},
		Required:             required,
	}
	return &huma.Schema{Ref: "#/components/schemas/EnvelopeError"}
}

// MarshalJSON writes Details under the configured key, and leaves it out
// when empty if the key was set by DetailsKey.
func (e EnvelopeError) MarshalJSON() ([]byte, error) {
	if e.key == "" || e.key == "details" {
		type plain EnvelopeError // no methods: the default shape, as ever
		return marshal(plain(e))
	}
	out := struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}{e.Code, e.Message}
	head, err := marshal(out)
	if err != nil {
		return nil, err
	}
	if len(e.Details) == 0 {
		return head, nil
	}
	k, err := marshal(e.key)
	if err != nil {
		return nil, err
	}
	d, err := marshal(e.Details)
	if err != nil {
		return nil, err
	}
	head = append(head[:len(head)-1], ',')
	head = append(head, k...)
	head = append(head, ':')
	head = append(head, d...)
	return append(head, '}'), nil
}

// Error, GetStatus and ContentType make the body a huma.StatusError.
func (b *EnvelopeBody) Error() string { return b.Err.Message }

// GetStatus is the response status.
func (b *EnvelopeBody) GetStatus() int { return b.status }

// ContentType is application/json.
func (b *EnvelopeBody) ContentType(string) string { return "application/json" }

func envelope(e *Error, c *i18n.Catalog, lang i18n.Lang, detailsKey string) *EnvelopeBody {
	b := &EnvelopeBody{status: e.Status}
	b.Err.key = detailsKey
	b.Err.Code = e.Code
	b.Err.Message = e.Message.Render(lang, c)
	b.Err.Details = []envelopeDetail{}
	for _, d := range e.Details {
		b.Err.Details = append(b.Err.Details, envelopeDetail{Field: d.Field, Message: d.Message.Render(lang, c)})
	}
	return b
}

type problemDetail struct {
	Location string `json:"location"`
	Message  string `json:"message"`
}

// ProblemBody is the ProblemJSON shape (RFC 9457 with a "code" member).
type ProblemBody struct {
	Title  string          `json:"title"`
	Status int             `json:"status"`
	Detail string          `json:"detail"`
	Code   string          `json:"code"`
	Errors []problemDetail `json:"errors,omitempty"`
}

// Error, GetStatus and ContentType make the body a huma.StatusError.
func (b *ProblemBody) Error() string { return b.Detail }

// GetStatus is the response status.
func (b *ProblemBody) GetStatus() int { return b.Status }

// ContentType is application/problem+json.
func (b *ProblemBody) ContentType(string) string { return "application/problem+json" }

func problem(e *Error, c *i18n.Catalog, lang i18n.Lang) *ProblemBody {
	b := &ProblemBody{Title: http.StatusText(e.Status), Status: e.Status, Detail: e.Message.Render(lang, c), Code: e.Code}
	for _, d := range e.Details {
		b.Errors = append(b.Errors, problemDetail{Location: d.Field, Message: d.Message.Render(lang, c)})
	}
	return b
}
