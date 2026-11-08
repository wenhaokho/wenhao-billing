package platform

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/shopspring/decimal"
)

// decimalString reads a JSON number or string as a decimal.
func decimalString(b []byte) (decimal.Decimal, error) {
	d, err := decimal.NewFromString(strings.Trim(string(b), `"`))
	if err != nil {
		return decimal.Decimal{}, errors.New("not an amount")
	}
	return d, nil
}

var decimalSchema = &huma.Schema{Type: huma.TypeString, Pattern: `^-?\d{1,15}(\.\d+)?$`, Examples: []any{"1500000.0000"}}

// Dec4 is numeric(19,4) and computed money in JSON: a string with exactly
// four decimals ("1500.0000"), so no client parses it as a float.
type Dec4 struct{ decimal.Decimal }

// MarshalJSON writes "1500.0000".
func (d Dec4) MarshalJSON() ([]byte, error) { return json.Marshal(d.StringFixed(4)) }

// UnmarshalJSON reads "12.5" or 12.5.
func (d *Dec4) UnmarshalJSON(b []byte) (err error) { d.Decimal, err = decimalString(b); return }

// Schema describes Dec4 to OpenAPI as a decimal string.
func (Dec4) Schema(huma.Registry) *huma.Schema { return decimalSchema }

// Dec8 is numeric(19,8), the FX rates: eight decimals.
type Dec8 struct{ decimal.Decimal }

// MarshalJSON writes "16000.50000000".
func (d Dec8) MarshalJSON() ([]byte, error) { return json.Marshal(d.StringFixed(8)) }

// UnmarshalJSON reads "12.5" or 12.5.
func (d *Dec8) UnmarshalJSON(b []byte) (err error) { d.Decimal, err = decimalString(b); return }

// Schema describes Dec8 to OpenAPI as a decimal string.
func (Dec8) Schema(huma.Registry) *huma.Schema { return decimalSchema }

// Dec2 is numeric(5,2), the match confidence: two decimals.
type Dec2 struct{ decimal.Decimal }

// MarshalJSON writes "97.50".
func (d Dec2) MarshalJSON() ([]byte, error) { return json.Marshal(d.StringFixed(2)) }

// UnmarshalJSON reads "12.5" or 12.5.
func (d *Dec2) UnmarshalJSON(b []byte) (err error) { d.Decimal, err = decimalString(b); return }

// Schema describes Dec2 to OpenAPI as a decimal string.
func (Dec2) Schema(huma.Registry) *huma.Schema { return decimalSchema }

// Date is a calendar date in JSON: "YYYY-MM-DD".
type Date struct{ time.Time }

const dateLayout = "2006-01-02"

// MarshalJSON writes "2026-03-09".
func (d Date) MarshalJSON() ([]byte, error) { return json.Marshal(d.Format(dateLayout)) }

// UnmarshalJSON reads "2026-03-09".
func (d *Date) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return errors.New("not a date")
	}
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		return errors.New("not a date")
	}
	d.Time = t
	return nil
}

// Schema describes Date to OpenAPI.
func (Date) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{Type: huma.TypeString, Pattern: `^\d{4}-\d{2}-\d{2}$`, Examples: []any{"2026-03-09"}}
}

// Time is an instant in JSON: UTC with a Z, microsecond precision.
type Time struct{ time.Time }

const timeLayout = "2006-01-02T15:04:05.999999Z"

// MarshalJSON writes "2026-01-01T03:00:00.123456Z".
func (t Time) MarshalJSON() ([]byte, error) { return json.Marshal(t.UTC().Format(timeLayout)) }

// UnmarshalJSON reads any RFC 3339 instant and keeps it in UTC.
func (t *Time) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return errors.New("not a timestamp")
	}
	p, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return errors.New("not a timestamp")
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

// Schema describes Optional[T] as a nullable T.
func (Optional[T]) Schema(r huma.Registry) *huma.Schema {
	s := *r.Schema(reflect.TypeFor[T](), true, "")
	s.Nullable = true
	return &s
}

// Page is a page of a list: every list in the API answers in this shape.
type Page[T any] struct {
	Items    []T   `json:"items"`
	Total    int64 `json:"total"`
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
	HasNext  bool  `json:"has_next"`
}

// PageQuery is the paging every list accepts. A page_size over 200 is
// refused (422), never clamped: a client that pages on has_next would loop.
// page stops at 100000, so the offset (at most 20 million) always fits the
// 32-bit OFFSET sqlc generates.
type PageQuery struct {
	Page     int `query:"page" default:"1" minimum:"1" maximum:"100000"`
	PageSize int `query:"page_size" default:"50" minimum:"1" maximum:"200"`
}

// Offset is the number of rows before this page.
func (q PageQuery) Offset() int { return (q.Page - 1) * q.PageSize }

// NewPage builds a Page from one page of items and the total.
func NewPage[T any](items []T, total int64, q PageQuery) Page[T] {
	if items == nil {
		items = []T{}
	}
	return Page[T]{Items: items, Total: total, Page: q.Page, PageSize: q.PageSize, HasNext: int64(q.Offset()+len(items)) < total}
}
