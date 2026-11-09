package platform

import (
	"reflect"
	"strings"

	"github.com/danielgtaylor/huma/v2"
)

// Huma documents a pointer field as nullable only when its schema has a
// scalar type: it writes `"type": [T, "null"]`. A schema with no type (the
// decimals' oneOf of string and number) came out as `"type": ["", "null"]`,
// and a pointer to a struct (a $ref) as the bare $ref, so generated clients
// read those fields as never null. nullable and markNullablePointers fix both.

// acceptsNull reports whether s already lets a null through.
func acceptsNull(s *huma.Schema) bool {
	if s.Nullable || s.Type == huma.TypeNull {
		return true
	}
	for _, list := range [][]*huma.Schema{s.OneOf, s.AnyOf} {
		for _, b := range list {
			if b.Type == huma.TypeNull {
				return true
			}
		}
	}
	return false
}

// nullable is s, or a new schema that also accepts null:
//   - a scalar type gets Huma's own `"type": [T, "null"]`;
//   - a typeless oneOf (the decimals) gets a null branch;
//   - anything else (a $ref, an object) becomes anyOf [s, null].
//
// An object is never marked Nullable itself: Huma's validator would then let
// a null through for its required properties.
func nullable(s *huma.Schema) *huma.Schema {
	if s == nil || acceptsNull(s) {
		return s
	}
	null := &huma.Schema{Type: huma.TypeNull}
	null.PrecomputeMessages()
	var out *huma.Schema
	switch {
	case s.Ref == "" && s.Type != "" && s.Type != huma.TypeObject && s.Type != huma.TypeArray:
		c := *s
		c.Nullable = true
		out = &c
	case s.Ref == "" && s.Type == "" && s.OneOf != nil:
		c := *s
		c.OneOf = append(append([]*huma.Schema{}, s.OneOf...), null)
		out = &c
	default:
		out = &huma.Schema{AnyOf: []*huma.Schema{s, null}}
	}
	out.PrecomputeMessages()
	return out
}

// markNullablePointers is a huma OnAddOperation hook: in every request and
// response body, each pointer field's schema accepts null. Optional[T] does
// the same in its own Schema.
func markNullablePointers(oapi *huma.OpenAPI, op *huma.Operation) {
	reg := oapi.Components.Schemas
	seen := map[*huma.Schema]bool{}
	if op.RequestBody != nil {
		for _, mt := range op.RequestBody.Content {
			markNullable(reg, mt.Schema, nil, seen)
		}
	}
	for _, resp := range op.Responses {
		for _, mt := range resp.Content {
			markNullable(reg, mt.Schema, nil, seen)
		}
	}
}

func markNullable(reg huma.Registry, s *huma.Schema, t reflect.Type, seen map[*huma.Schema]bool) {
	if s == nil {
		return
	}
	if s.Ref == "" && s.Type == "" && s.AnyOf != nil {
		for _, b := range s.AnyOf {
			markNullable(reg, b, t, seen)
		}
		return
	}
	if s.Ref != "" {
		if rt := reg.TypeFromRef(s.Ref); rt != nil {
			t = rt
		}
		s = reg.SchemaFromRef(s.Ref)
		if s == nil {
			return
		}
	}
	if seen[s] {
		return
	}
	seen[s] = true
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil {
		return
	}
	// A scalar has no fields to mark.
	if k := t.Kind(); k == reflect.Slice || k == reflect.Array {
		items := s.Items
		if t.Elem().Kind() == reflect.Pointer {
			s.Items = nullable(items)
		}
		markNullable(reg, items, t.Elem(), seen)
	} else if k == reflect.Map {
		if values, ok := s.AdditionalProperties.(*huma.Schema); ok {
			if t.Elem().Kind() == reflect.Pointer {
				s.AdditionalProperties = nullable(values)
			}
			markNullable(reg, values, t.Elem(), seen)
		}
	} else if k == reflect.Struct {
		markNullableStruct(reg, s, t, seen)
	}
}

func markNullableStruct(reg huma.Registry, s *huma.Schema, t reflect.Type, seen map[*huma.Schema]bool) {
	if s.Properties == nil {
		return
	}
	for f := range t.Fields() {
		// Huma flattens an embedded struct into the parent's properties.
		if et := f.Type; f.Anonymous {
			if et.Kind() == reflect.Pointer {
				et = et.Elem()
			}
			if et.Kind() == reflect.Struct {
				markNullableStruct(reg, s, et, seen)
				continue
			}
		}
		if !f.IsExported() {
			continue
		}
		name := f.Name
		if j := f.Tag.Get("json"); j != "" {
			if n, _, _ := strings.Cut(j, ","); n != "" {
				name = n
			}
		}
		p, ok := s.Properties[name]
		if name == "-" || !ok {
			continue
		}
		// As Huma does, a pointer with omitempty is left out, never null.
		if f.Type.Kind() == reflect.Pointer && !strings.Contains(f.Tag.Get("json"), "omitempty") {
			s.Properties[name] = nullable(p)
		}
		markNullable(reg, p, f.Type, seen)
	}
}
