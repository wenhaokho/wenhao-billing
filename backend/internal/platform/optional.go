package platform

import (
	"reflect"
	"strings"

	"github.com/danielgtaylor/huma/v2"
)

// optionalMarker is implemented by Optional[T] only.
type optionalMarker interface{ optionalField() }

var optionalType = reflect.TypeFor[optionalMarker]()

// relaxOptionalRequestFields is a huma OnAddOperation hook. Huma marks every
// struct field required unless it carries omitempty, so a PATCH field of type
// Optional[T], or a nullable *T request field, would be a 422 when omitted.
// This walks each request body's Go types and drops those fields from the
// schema's required list, so no author has to remember a tag.
func relaxOptionalRequestFields(oapi *huma.OpenAPI, op *huma.Operation) {
	if op.RequestBody == nil {
		return
	}
	seen := map[*huma.Schema]bool{}
	for _, mt := range op.RequestBody.Content {
		relax(oapi.Components.Schemas, mt.Schema, nil, seen)
	}
}

func relax(reg huma.Registry, s *huma.Schema, t reflect.Type, seen map[*huma.Schema]bool) {
	if s == nil {
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
	if t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
		relax(reg, s.Items, t.Elem(), seen)
		return
	}
	if t.Kind() == reflect.Map {
		if values, ok := s.AdditionalProperties.(*huma.Schema); ok {
			relax(reg, values, t.Elem(), seen)
		}
		return
	}
	if t.Kind() == reflect.Struct {
		optional := map[string]bool{}
		relaxStruct(reg, s, t, optional, seen)
		if len(optional) == 0 {
			return
		}
		kept := s.Required[:0:0]
		for _, name := range s.Required {
			if !optional[name] {
				kept = append(kept, name)
			}
		}
		s.Required = kept
		s.PrecomputeMessages() // rebuilds the required lookup
	}
}

func relaxStruct(reg huma.Registry, s *huma.Schema, t reflect.Type, optional map[string]bool, seen map[*huma.Schema]bool) {
	for f := range t.Fields() {
		// Huma flattens an embedded struct, by value or by pointer, into the
		// parent's properties.
		if et := f.Type; f.Anonymous {
			if et.Kind() == reflect.Pointer {
				et = et.Elem()
			}
			if et.Kind() == reflect.Struct {
				relaxStruct(reg, s, et, optional, seen)
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
		if name == "-" {
			continue
		}
		if f.Type.Kind() == reflect.Pointer || f.Type.Implements(optionalType) {
			optional[name] = true
		}
		relax(reg, s.Properties[name], f.Type, seen)
	}
}
