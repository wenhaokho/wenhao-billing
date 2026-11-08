package platform

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/shopspring/decimal"
)

// Money is an amount in JSON: a string with exactly two decimals
// ("1500000.50"), so no client parses it as a float. Input must be a string
// too (the schema says so, and Huma checks it before decoding). At most 12
// digits before the point: it fits numeric(14,2), the money column type, so
// an amount the column cannot hold is a 422, never a 500.
type Money struct{ decimal.Decimal }

// MarshalJSON writes "123.40".
func (m Money) MarshalJSON() ([]byte, error) {
	return json.Marshal(m.StringFixed(2))
}

// UnmarshalJSON reads "123.4" or 123.4.
func (m *Money) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	d, err := decimal.NewFromString(s)
	if err != nil {
		return errors.New("not an amount")
	}
	m.Decimal = d
	return nil
}

// Schema describes Money to OpenAPI as a decimal string.
func (Money) Schema(huma.Registry) *huma.Schema {
	return &huma.Schema{Type: huma.TypeString, Pattern: `^-?\d{1,12}(\.\d{1,2})?$`, Examples: []any{"1500000.50"}}
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
