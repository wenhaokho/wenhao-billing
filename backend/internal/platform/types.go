package platform

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/shopspring/decimal"

	"github.com/LOQATO/gokit/apierr"
	"github.com/LOQATO/gokit/i18n"
)

// The sentences a client reads for a request value that does not parse.
const (
	decimalSentence = "value is not a valid decimal"
	dateSentence    = "value is not a valid date (YYYY-MM-DD)"
	timeSentence    = "value is not a valid timestamp (RFC 3339)"
)

// A refused value must reach the client at its field, but Huma reports an
// UnmarshalJSON error at "body" with its bare text. So each parsed type
// remembers the refusal, and its Resolve (Huma runs it on every value in a
// request body, knowing the value's path) reports it. The report is an
// *apierr.Error, which the 422 shows in place of Huma's own messages for the
// same body: its detail is the sentence.

// errInvalid is UnmarshalJSON's error, for callers outside Huma.
func errInvalid(sentence string) error { return errors.New(sentence) }

// invalidAt is the 422 for a refused value at path ("body.lines[0].amount"),
// its field named as Huma's own errors are ("lines.0.amount").
func invalidAt(path *huma.PathBuffer, sentence string) []error {
	field, _ := strings.CutPrefix(path.String(), "body.")
	field = strings.NewReplacer("[", ".", "]", "").Replace(field)
	status := http.StatusUnprocessableEntity
	return []error{apierr.New(status, apierr.Codes[status], i18n.T(sentence), apierr.Detail{Field: field, Message: i18n.T(sentence)})}
}

// refusable is a value whose UnmarshalJSON may have refused its input;
// takeRefusal reports the sentence once.
type refusable interface{ takeRefusal() (string, bool) }

func resolveRefusal(v refusable, path *huma.PathBuffer) []error {
	if sentence, refused := v.takeRefusal(); refused {
		return invalidAt(path, sentence)
	}
	return nil
}

// refusal is embedded in the parsed types: the sentence UnmarshalJSON
// refused the input with, if it did.
type refusal struct{ sentence string }

func (r *refusal) refuse(sentence string) error {
	r.sentence = sentence
	return errInvalid(sentence)
}

func (r *refusal) takeRefusal() (string, bool) {
	s := r.sentence
	r.sentence = ""
	return s, s != ""
}

// Resolve reports a refused input at its field.
func (r *refusal) Resolve(_ huma.Context, path *huma.PathBuffer) []error {
	return resolveRefusal(r, path)
}

// decimalString reads a JSON number or string as a decimal.
func decimalString(b []byte) (decimal.Decimal, bool) {
	d, err := decimal.NewFromString(strings.Trim(string(b), `"`))
	return d, err == nil
}

// decimalSchema accepts what UnmarshalJSON accepts, a string or a number (the
// Vue app sends numbers); a string has at most places decimals. Responses
// always write the string. A fresh value per call: Huma keeps the schema it
// is given.
func decimalSchema(places, example string) *huma.Schema {
	str := &huma.Schema{Type: huma.TypeString, Pattern: `^-?[0-9]+(\.[0-9]{1,` + places + `})?$`, Examples: []any{example}}
	str.PrecomputeMessages()
	num := &huma.Schema{Type: huma.TypeNumber}
	num.PrecomputeMessages()
	s := &huma.Schema{OneOf: []*huma.Schema{str, num}}
	s.PrecomputeMessages()
	return s
}

// Dec4 is numeric(19,4) and computed money in JSON: a string with exactly
// four decimals ("1500.0000"), so no client parses it as a float.
type Dec4 struct {
	decimal.Decimal
	refusal
}

// MarshalJSON writes "1500.0000".
func (d Dec4) MarshalJSON() ([]byte, error) { return json.Marshal(d.StringFixed(4)) }

// UnmarshalJSON reads "12.5" or 12.5.
func (d *Dec4) UnmarshalJSON(b []byte) error {
	v, ok := decimalString(b)
	if !ok {
		return d.refuse(decimalSentence)
	}
	d.Decimal = v
	return nil
}

// Schema describes Dec4 to OpenAPI as a decimal string.
func (Dec4) Schema(huma.Registry) *huma.Schema { return decimalSchema("4", "1500000.0000") }

// Dec8 is numeric(19,8), the FX rates: eight decimals.
type Dec8 struct {
	decimal.Decimal
	refusal
}

// MarshalJSON writes "16000.50000000".
func (d Dec8) MarshalJSON() ([]byte, error) { return json.Marshal(d.StringFixed(8)) }

// UnmarshalJSON reads "12.5" or 12.5.
func (d *Dec8) UnmarshalJSON(b []byte) error {
	v, ok := decimalString(b)
	if !ok {
		return d.refuse(decimalSentence)
	}
	d.Decimal = v
	return nil
}

// Schema describes Dec8 to OpenAPI as a decimal string.
func (Dec8) Schema(huma.Registry) *huma.Schema { return decimalSchema("8", "16000.50000000") }

// Dec2 is numeric(5,2), the match confidence: two decimals.
type Dec2 struct {
	decimal.Decimal
	refusal
}

// MarshalJSON writes "97.50".
func (d Dec2) MarshalJSON() ([]byte, error) { return json.Marshal(d.StringFixed(2)) }

// UnmarshalJSON reads "12.5" or 12.5.
func (d *Dec2) UnmarshalJSON(b []byte) error {
	v, ok := decimalString(b)
	if !ok {
		return d.refuse(decimalSentence)
	}
	d.Decimal = v
	return nil
}

// Schema describes Dec2 to OpenAPI as a decimal string.
func (Dec2) Schema(huma.Registry) *huma.Schema { return decimalSchema("2", "97.50") }

// Date is a calendar date in JSON: "YYYY-MM-DD".
type Date struct {
	time.Time
	refusal
}

const dateLayout = "2006-01-02"

// MarshalJSON writes "2026-03-09".
func (d Date) MarshalJSON() ([]byte, error) { return json.Marshal(d.Format(dateLayout)) }

// UnmarshalJSON reads "2026-03-09".
func (d *Date) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return d.refuse(dateSentence)
	}
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		return d.refuse(dateSentence)
	}
	d.Time = t
	return nil
}

// Schema describes Date to OpenAPI.
func (Date) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{Type: huma.TypeString, Pattern: `^\d{4}-\d{2}-\d{2}$`, Examples: []any{"2026-03-09"}}
}

// Time is an instant in JSON: UTC with a Z, microsecond precision.
type Time struct {
	time.Time
	refusal
}

// Python's isoformat: six digits when the microseconds are not zero, none
// when they are.
const (
	timeLayout      = "2006-01-02T15:04:05.000000Z"
	timeLayoutWhole = "2006-01-02T15:04:05Z"
)

// MarshalJSON writes "2026-01-01T03:00:00.123456Z", or "...:00Z" on the second.
func (t Time) MarshalJSON() ([]byte, error) {
	u := t.UTC()
	layout := timeLayoutWhole
	if u.Nanosecond()/1000 != 0 {
		layout = timeLayout
	}
	return json.Marshal(u.Format(layout))
}

// UnmarshalJSON reads any RFC 3339 instant and keeps it in UTC.
func (t *Time) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return t.refuse(timeSentence)
	}
	p, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return t.refuse(timeSentence)
	}
	t.Time = p.UTC()
	return nil
}

// Schema describes Time to OpenAPI.
func (Time) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{Type: huma.TypeString, Format: "date-time", Examples: []any{"2026-01-01T03:00:00.123456Z"}}
}

// Optional is a PATCH field with three states: omitted (Set false), null
// (Set and Null) or a value (Set and Value).
type Optional[T any] struct {
	Set, Null bool
	Value     T
}

func (Optional[T]) optionalField() {}

// UnmarshalJSON runs only when the member is present.
func (o *Optional[T]) UnmarshalJSON(b []byte) error {
	o.Set = true
	if string(b) == "null" {
		o.Null = true
		return nil
	}
	return json.Unmarshal(b, &o.Value)
}

// MarshalJSON writes the value, or null.
func (o Optional[T]) MarshalJSON() ([]byte, error) {
	if !o.Set || o.Null {
		return []byte("null"), nil
	}
	return json.Marshal(o.Value)
}

// Resolve reports a refused Value at the Optional's own field, where Huma
// would name it "….value". It runs before the Value's own Resolve, which
// then finds nothing to report.
func (o *Optional[T]) Resolve(_ huma.Context, path *huma.PathBuffer) []error {
	if v, ok := any(&o.Value).(refusable); ok {
		return resolveRefusal(v, path)
	}
	return nil
}

// Schema describes Optional[T] as a nullable T.
func (Optional[T]) Schema(r huma.Registry) *huma.Schema {
	s := *r.Schema(reflect.TypeFor[T](), true, "")
	s.Nullable = true
	return &s
}

// NotNull is a request field for a NOT NULL column that may be left out: a
// PATCH field, or a create field with a default. Omitted is Set false; a value
// is Set and Value. Its schema is the plain, non-nullable T. Huma's
// validation lets a null through for a property that is not required (as if
// it were omitted), so Resolve refuses it at the field's own location, as a
// validation error listed with every other one.
type NotNull[T any] struct {
	Set   bool
	Value T
	null  bool // sent as null: Resolve refuses it
}

func (NotNull[T]) optionalField() {}

// nullSentence is the message of a refused null.
const nullSentence = "value cannot be null"

// UnmarshalJSON runs only when the member is present. A null is remembered,
// not returned as an error: Huma would report that error at "body".
func (n *NotNull[T]) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		n.null = true
		return nil
	}
	n.Set = true
	return json.Unmarshal(b, &n.Value)
}

// MarshalJSON writes the value, or null when it was left out.
func (n NotNull[T]) MarshalJSON() ([]byte, error) {
	if !n.Set {
		return []byte("null"), nil
	}
	return json.Marshal(n.Value)
}

// Resolve refuses a null at the field as Huma's own validation errors are
// reported (a field detail, not an *apierr.Error, so every refused field is
// listed), and reports a refused Value at the field, as Optional's does.
func (n *NotNull[T]) Resolve(_ huma.Context, path *huma.PathBuffer) []error {
	if n.null {
		return []error{&huma.ErrorDetail{Location: path.String(), Message: nullSentence}}
	}
	if v, ok := any(&n.Value).(refusable); ok {
		return resolveRefusal(v, path)
	}
	return nil
}

// Schema describes NotNull[T] as T, never null.
func (NotNull[T]) Schema(r huma.Registry) *huma.Schema {
	s := *r.Schema(reflect.TypeFor[T](), true, "")
	return &s
}

// Or is the value when it was sent, otherwise def.
func (n NotNull[T]) Or(def T) T {
	if !n.Set {
		return def
	}
	return n.Value
}

// OptionalParam is a query parameter that may be left out, as Python's
// `active: bool | None = None`: Huma refuses pointer parameters, and a plain
// bool cannot tell false from absent. Huma parses and documents it as a T.
type OptionalParam[T any] struct {
	Value T
	Set   bool
}

// Receiver is where Huma parses the parameter.
func (o *OptionalParam[T]) Receiver() reflect.Value { return reflect.ValueOf(&o.Value).Elem() }

// OnParamSet records whether the parameter was sent.
func (o *OptionalParam[T]) OnParamSet(isSet bool, _ any) { o.Set = isSet }

// Schema describes and validates the parameter as a T.
func (OptionalParam[T]) Schema(r huma.Registry) *huma.Schema {
	return r.Schema(reflect.TypeFor[T](), true, "")
}

// Ptr is the value, or nil when the parameter was left out (a sqlc.narg).
func (o OptionalParam[T]) Ptr() *T {
	if !o.Set {
		return nil
	}
	return &o.Value
}
