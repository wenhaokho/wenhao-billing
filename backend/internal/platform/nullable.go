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
//   - anything else (a $ref, an object) becomes anyOf [s, null] when
//     objects is set, and is left as it is otherwise.
//
// Request bodies pass objects false. Huma's anyOf validation reports only
// "matched none" at the field, so a wrapped request object would lose its
// field errors ("cloudflare_target.record_id"); and Huma already lets a null
// through for a property that is not required. An object is never marked
// Nullable itself: Huma's validator would then let a null through for its
// required properties.
func nullable(s *huma.Schema, objects bool) *huma.Schema {
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
	case objects:
		out = &huma.Schema{AnyOf: []*huma.Schema{s, null}}
	default:
		return s
	}
	out.PrecomputeMessages()
	return out
}

// markNullablePointers is a huma OnAddOperation hook: in every request and
// response body, each pointer field's schema accepts null, except a request
// object's (see nullable). Optional[T] does the same in its own Schema; it
// is a request type.
func markNullablePointers(oapi *huma.OpenAPI, op *huma.Operation) {
	reg := oapi.Components.Schemas
	// One seen set per side: a component met in the request is still
	// walked, with objects wrapped, when the response meets it.
	if op.RequestBody != nil {
		seen := map[*huma.Schema]bool{}
		for _, mt := range op.RequestBody.Content {
			markNullable(reg, mt.Schema, nil, false, seen)
		}
	}
	seen := map[*huma.Schema]bool{}
	for _, resp := range op.Responses {
		for _, mt := range resp.Content {
			markNullable(reg, mt.Schema, nil, true, seen)
		}
	}
}

func markNullable(reg huma.Registry, s *huma.Schema, t reflect.Type, objects bool, seen map[*huma.Schema]bool) {
	if s == nil {
		return
	}
	if s.Ref == "" && s.Type == "" && s.AnyOf != nil {
		for _, b := range s.AnyOf {
			markNullable(reg, b, t, objects, seen)
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
			s.Items = nullable(items, objects)
		}
		markNullable(reg, items, t.Elem(), objects, seen)
	} else if k == reflect.Map {
		if values, ok := s.AdditionalProperties.(*huma.Schema); ok {
			if t.Elem().Kind() == reflect.Pointer {
				s.AdditionalProperties = nullable(values, objects)
			}
			markNullable(reg, values, t.Elem(), objects, seen)
		}
	} else if k == reflect.Struct {
		markNullableStruct(reg, s, t, objects, seen)
	}
}

func markNullableStruct(reg huma.Registry, s *huma.Schema, t reflect.Type, objects bool, seen map[*huma.Schema]bool) {
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
				markNullableStruct(reg, s, et, objects, seen)
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
			s.Properties[name] = nullable(p, objects)
		}
		markNullable(reg, p, f.Type, objects, seen)
	}
}

// nonNullableArrayBodies is a huma OnAddOperation hook: a request or
// response body that is a bare JSON array is never null. Huma makes every
// []T nullable (a nil slice encodes as null), but the handlers answer lists
// made non-nil, and a null request body is a list nobody sent: Huma's own
// validation then refuses it at 422, as Python's list[T] does.
func nonNullableArrayBodies(_ *huma.OpenAPI, op *huma.Operation) {
	var contents []map[string]*huma.MediaType
	if op.RequestBody != nil {
		contents = append(contents, op.RequestBody.Content)
	}
	for _, resp := range op.Responses {
		contents = append(contents, resp.Content)
	}
	for _, content := range contents {
		for _, mt := range content {
			if s := mt.Schema; s != nil && s.Ref == "" && s.Type == huma.TypeArray && s.Nullable {
				s.Nullable = false
				s.PrecomputeMessages()
			}
		}
	}
}
