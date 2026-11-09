package platform

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
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
	return []error{FieldError(field, sentence)}
}

// FieldError is the 422 for one refused request field: location names it as
// the 422s for Huma's own validation do ("lines.0.amount"), and the sentence
// is both the detail and the field's message.
func FieldError(location, sentence string) error {
	status := http.StatusUnprocessableEntity
	return apierr.New(status, apierr.Codes[status], i18n.T(sentence), apierr.Detail{Field: location, Message: i18n.T(sentence)})
}

// refusable is a value whose UnmarshalJSON may have refused its input;
// takeRefusal reports the refusal once.
type refusable interface{ takeRefusal() (refusal, bool) }

func resolveRefusal(v refusable, path *huma.PathBuffer) []error {
	r, refused := v.takeRefusal()
	switch {
	case !refused:
		return nil
	case r.bound:
		// Listed with every other validation error, as Huma's own are.
		return []error{&huma.ErrorDetail{Location: path.String(), Message: r.sentence}}
	default:
		return invalidAt(path, r.sentence)
	}
}

// refusal is embedded in the parsed types: the sentence UnmarshalJSON
// refused the input with, if it did. A bound is a value that parsed but
// does not fit the column: UnmarshalJSON returns no error for it (Huma
// would report one at "body"), so decoding goes on and every field's
// refusal is listed.
type refusal struct {
	sentence string
	bound    bool
}

func (r *refusal) refuse(sentence string) error {
	*r = refusal{sentence: sentence}
	return errInvalid(sentence)
}

func (r *refusal) refuseBound(sentence string) { *r = refusal{sentence: sentence, bound: true} }

func (r *refusal) takeRefusal() (refusal, bool) {
	out := *r
	*r = refusal{}
	return out, out.sentence != ""
}

// Resolve reports a refused input at its field.
func (r *refusal) Resolve(_ huma.Context, path *huma.PathBuffer) []error {
	return resolveRefusal(r, path)
}

// decimalBounds are a numeric(precision, places) column's: at most places
// decimals and whole = precision − places digits before the point.
type decimalBounds struct{ places, whole int32 }

var (
	dec2Bounds = decimalBounds{places: 2, whole: 3}  // numeric(5,2)
	dec4Bounds = decimalBounds{places: 4, whole: 15} // numeric(19,4)
	dec8Bounds = decimalBounds{places: 8, whole: 11} // numeric(19,8)
)

// fit is d as the column holds it, or why it does not fit. Trailing zeros
// are not places ("1.50000" fits four). The exponent is bounded before
// anything rescales: a JSON value as short as 1e-2000000000 would otherwise
// make Round build a power of ten with two billion digits.
func (b decimalBounds) fit(d decimal.Decimal) (decimal.Decimal, string) {
	if d.IsZero() {
		// 0e2000000000, 0e-9 and -0 are all zero.
		return decimal.Zero, ""
	}
	digits, exp := int64(d.NumDigits()), int64(d.Exponent())
	if digits+exp > int64(b.whole) {
		return d, fmt.Sprintf("value has too many digits (max %d before the point)", b.whole)
	}
	// The digits past the allowed places are the coefficient's last
	// extra = -exp-places. More of them than the coefficient has digits
	// cannot all be zeros, so the value needs more places: refused without
	// rounding. Otherwise Round rescales by at most the coefficient's digits.
	extra := -exp - int64(b.places)
	if extra > digits || (extra > 0 && !d.Equal(d.Round(b.places))) {
		return d, fmt.Sprintf("value has too many decimal places (max %d)", b.places)
	}
	return d, ""
}

// parse reads a JSON number or string into *into, or records why not. An
// out-of-bounds value is recorded, not returned: Huma would report an
// UnmarshalJSON error at "body" and stop decoding the other fields. So
// encoding/json alone does not refuse it (err is nil), and what is
// guaranteed is that a refused value cannot be used: while its refusal is
// pending, Decimal() panics (the recoverer answers 500) and MarshalJSON and
// Value fail. Huma's Resolve takes the refusal to report it at the field,
// and the request is then a 422 before any handler runs.
func (b decimalBounds) parse(raw []byte, into *decimal.Decimal, r *refusal) error {
	d, err := decimal.NewFromString(strings.Trim(string(raw), `"`))
	if err != nil {
		return r.refuse(decimalSentence)
	}
	d, sentence := b.fit(d)
	if sentence != "" {
		r.refuseBound(sentence)
		return nil
	}
	*r = refusal{}
	*into = d
	return nil
}

// pending is the error for a refusal no Resolve has reported, or nil.
func (r refusal) pending() error {
	if r.sentence == "" {
		return nil
	}
	return errInvalid(r.sentence)
}

// checked is d, or a panic for a refused value: a refused amount must never
// be read as the zero it holds.
func checked(d decimal.Decimal, r refusal) decimal.Decimal {
	if err := r.pending(); err != nil {
		panic(fmt.Sprintf("platform: read of a refused decimal: %v", err))
	}
	return d
}

// marshalFixed writes d with places decimals, or fails for a refused value.
func marshalFixed(d decimal.Decimal, places int32, r refusal) ([]byte, error) {
	if err := r.pending(); err != nil {
		return nil, err
	}
	return json.Marshal(d.StringFixed(places))
}

// valueOf is the database value of d, or fails for a refused value.
func valueOf(d decimal.Decimal, r refusal) (driver.Value, error) {
	if err := r.pending(); err != nil {
		return nil, err
	}
	return d.Value()
}

// decimalPattern is the decimal syntax a request string may use. The type,
// not the schema, decides the bounds, so a string and the same number are
// treated alike: "1.50000" fits four places as 1.50000 does, "1e3" is 1000,
// and "1.23456" gets the type's one readable refusal.
const decimalPattern = `^-?[0-9]+(\.[0-9]+)?([eE][-+]?[0-9]+)?$`

// description documents the bounds on a request schema.
func (b decimalBounds) description() string {
	return fmt.Sprintf("At most %d decimal places and %d digits before the point.", b.places, b.whole)
}

// requestSchema accepts what UnmarshalJSON accepts, a string or a number
// (the Vue app sends numbers). A fresh value per call: Huma keeps the schema
// it is given.
func (b decimalBounds) requestSchema(example string) *huma.Schema {
	str := &huma.Schema{Type: huma.TypeString, Pattern: decimalPattern, Examples: []any{example}}
	str.PrecomputeMessages()
	num := &huma.Schema{Type: huma.TypeNumber}
	num.PrecomputeMessages()
	s := &huma.Schema{OneOf: []*huma.Schema{str, num}, Description: b.description()}
	s.PrecomputeMessages()
	return s
}

// responseString is what a response writes: always the string, with
// exactly the column's places. The response walk (decimalResponse) puts it
// in place of the request schema.
func (b decimalBounds) responseString(example string) *huma.Schema {
	return &huma.Schema{
		Type:     huma.TypeString,
		Pattern:  fmt.Sprintf(`^-?[0-9]{1,%d}\.[0-9]{%d}$`, b.whole, b.places),
		Examples: []any{example},
	}
}

// decimalType is Dec2, Dec4 and Dec8: their response schema is a string,
// and their request schema's description is the bounds'.
type decimalType interface {
	responseString() *huma.Schema
	boundsDescription() string
}

// Dec4 is numeric(19,4) and computed money in JSON: a string with exactly
// four decimals ("1500.0000"), so no client parses it as a float. The value
// is read only through Decimal(), which refuses a refused value.
type Dec4 struct {
	d decimal.Decimal
	refusal
}

// NewDec4 is d as a Dec4 (a response value; its bounds are not checked).
func NewDec4(d decimal.Decimal) Dec4 { return Dec4{d: d} }

const dec4Example = "1500000.0000"

// Decimal is the value. It panics for a value UnmarshalJSON refused and no
// Resolve has reported (see decimalBounds.parse).
func (x Dec4) Decimal() decimal.Decimal { return checked(x.d, x.refusal) }

// MarshalJSON writes "1500.0000".
func (x Dec4) MarshalJSON() ([]byte, error) { return marshalFixed(x.d, 4, x.refusal) }

// Value is the database value; it fails for a refused value.
func (x Dec4) Value() (driver.Value, error) { return valueOf(x.d, x.refusal) }

// UnmarshalJSON reads "12.5" or 12.5, refusing more than 4 places or 15
// digits before the point (see decimalBounds.parse).
func (x *Dec4) UnmarshalJSON(b []byte) error { return dec4Bounds.parse(b, &x.d, &x.refusal) }

// Schema describes Dec4 to OpenAPI as a decimal string or number.
func (Dec4) Schema(huma.Registry) *huma.Schema { return dec4Bounds.requestSchema(dec4Example) }

func (Dec4) responseString() *huma.Schema { return dec4Bounds.responseString(dec4Example) }

func (Dec4) boundsDescription() string { return dec4Bounds.description() }

// Dec8 is numeric(19,8), the FX rates: eight decimals. The value is read
// only through Decimal(), which refuses a refused value.
type Dec8 struct {
	d decimal.Decimal
	refusal
}

// NewDec8 is d as a Dec8 (a response value; its bounds are not checked).
func NewDec8(d decimal.Decimal) Dec8 { return Dec8{d: d} }

const dec8Example = "16000.50000000"

// Decimal is the value. It panics for a value UnmarshalJSON refused and no
// Resolve has reported (see decimalBounds.parse).
func (x Dec8) Decimal() decimal.Decimal { return checked(x.d, x.refusal) }

// MarshalJSON writes "16000.50000000".
func (x Dec8) MarshalJSON() ([]byte, error) { return marshalFixed(x.d, 8, x.refusal) }

// Value is the database value; it fails for a refused value.
func (x Dec8) Value() (driver.Value, error) { return valueOf(x.d, x.refusal) }

// UnmarshalJSON reads "12.5" or 12.5, refusing more than 8 places or 11
// digits before the point (see decimalBounds.parse).
func (x *Dec8) UnmarshalJSON(b []byte) error { return dec8Bounds.parse(b, &x.d, &x.refusal) }

// Schema describes Dec8 to OpenAPI as a decimal string or number.
func (Dec8) Schema(huma.Registry) *huma.Schema { return dec8Bounds.requestSchema(dec8Example) }

func (Dec8) responseString() *huma.Schema { return dec8Bounds.responseString(dec8Example) }

func (Dec8) boundsDescription() string { return dec8Bounds.description() }

// Dec2 is numeric(5,2), the match confidence: two decimals. The value is
// read only through Decimal(), which refuses a refused value.
type Dec2 struct {
	d decimal.Decimal
	refusal
}

// NewDec2 is d as a Dec2 (a response value; its bounds are not checked).
func NewDec2(d decimal.Decimal) Dec2 { return Dec2{d: d} }

const dec2Example = "97.50"

// Decimal is the value. It panics for a value UnmarshalJSON refused and no
// Resolve has reported (see decimalBounds.parse).
func (x Dec2) Decimal() decimal.Decimal { return checked(x.d, x.refusal) }

// MarshalJSON writes "97.50".
func (x Dec2) MarshalJSON() ([]byte, error) { return marshalFixed(x.d, 2, x.refusal) }

// Value is the database value; it fails for a refused value.
func (x Dec2) Value() (driver.Value, error) { return valueOf(x.d, x.refusal) }

// UnmarshalJSON reads "12.5" or 12.5, refusing more than 2 places or 3
// digits before the point (see decimalBounds.parse).
func (x *Dec2) UnmarshalJSON(b []byte) error { return dec2Bounds.parse(b, &x.d, &x.refusal) }

// Schema describes Dec2 to OpenAPI as a decimal string or number.
func (Dec2) Schema(huma.Registry) *huma.Schema { return dec2Bounds.requestSchema(dec2Example) }

func (Dec2) responseString() *huma.Schema { return dec2Bounds.responseString(dec2Example) }

func (Dec2) boundsDescription() string { return dec2Bounds.description() }

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

// Schema describes Optional[T] as a T that may be null. Optional is a
// request type: an object T keeps its plain schema (see nullable), and Huma
// lets the null through as it does for any property that is not required.
func (Optional[T]) Schema(r huma.Registry) *huma.Schema {
	s := *r.Schema(reflect.TypeFor[T](), true, "")
	return nullable(&s, false)
}

// Merge is the column value of a nullable field: cur when the field was
// left out, nil when it was sent as null, otherwise the value sent.
func (o Optional[T]) Merge(cur *T) *T {
	if !o.Set {
		return cur
	}
	if o.Null {
		return nil
	}
	v := o.Value
	return &v
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
