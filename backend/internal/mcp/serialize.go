package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

// Object is a tool's JSON object result with its keys in the order the tool
// built them, as Python's dict (and so its structuredContent and text) keeps
// them. The zero value is {}. Tools build one with pick, or with set for a
// value they compute.
type Object struct {
	keys []string
	vals []json.RawMessage
}

// set appends key with v's JSON.
func (o *Object) set(key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("object key %q: %w", key, err)
	}
	o.keys = append(o.keys, key)
	o.vals = append(o.vals, raw)
	return nil
}

// MarshalJSON writes the keys in order.
func (o Object) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		b.Write(kb)
		b.WriteByte(':')
		b.Write(o.vals[i])
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// pick is Python's to_dict(obj, fields): the named keys of v's JSON object,
// in the order given, each value exactly as v's types print it (money as
// strings at the column's scale, dates as ISO, ids as UUID strings). v is an
// existing response type; a name it has no key for is a bug in the field
// list, so it is an error rather than a silent gap.
func pick(v any, fields []string) (Object, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return Object{}, fmt.Errorf("marshal: %w", err)
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(raw, &all); err != nil {
		return Object{}, fmt.Errorf("decode: %w", err)
	}
	o := Object{keys: make([]string, 0, len(fields)), vals: make([]json.RawMessage, 0, len(fields))}
	for _, f := range fields {
		val, ok := all[f]
		if !ok {
			return Object{}, fmt.Errorf("%T has no field %q", v, f)
		}
		o.keys = append(o.keys, f)
		o.vals = append(o.vals, val)
	}
	return o, nil
}

// pickAll is pick over every element of rows, never nil.
func pickAll[T any](rows []T, fields []string) ([]Object, error) {
	out := make([]Object, 0, len(rows))
	for _, r := range rows {
		o, err := pick(r, fields)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, nil
}

// isoNaive is Python's isoformat() of a naive datetime (the timestamp
// columns carry no zone): six fractional digits when the microseconds are
// not zero, none when they are.
func isoNaive(t time.Time) string {
	if t.Nanosecond()/1000 != 0 {
		return t.Format("2006-01-02T15:04:05.000000")
	}
	return t.Format("2006-01-02T15:04:05")
}
