package platform

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
)

type nullTarget struct {
	Zone string `json:"zone" minLength:"1"`
}

type nullBody struct {
	Note      *string                `json:"note"`
	Price     *Dec4                  `json:"price"`
	Target    *nullTarget            `json:"target"`
	MaybeDec  Optional[Dec4]         `json:"maybe_dec"`
	MaybeObj  Optional[nullTarget]   `json:"maybe_obj"`
	MaybeText Optional[string]       `json:"maybe_text" maxLength:"3"`
	Count     NotNull[int32]         `json:"count"`
	Lines     []nullTarget           `json:"lines,omitempty"`
	Skipped   *string                `json:"skipped,omitempty"`
	Plain     Dec4                   `json:"plain"`
	ByKey     map[string]*nullTarget `json:"by_key,omitempty"`
}

type nullIn struct{ Body nullBody }

type nullOut struct{ Body nullBody }

func nullServer(t *testing.T) (http.Handler, huma.API) {
	t.Helper()
	h, api := NewServer(Config{Environment: "test"}, Deps{})
	huma.Register(api, huma.Operation{OperationID: "nullable", Method: http.MethodPost, Path: "/nullable", Tags: []string{PublicTag}},
		func(_ context.Context, in *nullIn) (*nullOut, error) { return &nullOut{Body: in.Body}, nil })
	return h, api
}

// schemaAcceptsNull reads a schema as a client generator does, from JSON.
func schemaAcceptsNull(t *testing.T, s *huma.Schema) bool {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return jsonAcceptsNull(m)
}

func jsonAcceptsNull(m map[string]any) bool {
	switch typ := m["type"].(type) {
	case string:
		if typ == "null" {
			return true
		}
	case []any:
		for _, x := range typ {
			if x == "null" {
				return true
			}
		}
	}
	for _, k := range []string{"oneOf", "anyOf"} {
		branches, _ := m[k].([]any)
		for _, b := range branches {
			if bm, ok := b.(map[string]any); ok && jsonAcceptsNull(bm) {
				return true
			}
		}
	}
	return false
}

// Every pointer and Optional field's schema accepts null, whatever its
// schema's shape; NotNull, plain values and omitempty pointers do not; and
// no schema has an empty type.
func TestPointerAndOptionalSchemasAcceptNull(t *testing.T) {
	_, api := nullServer(t)
	reg := api.OpenAPI().Components.Schemas
	s := reg.SchemaFromRef(reg.Schema(reflect.TypeFor[nullBody](), true, "").Ref)
	for name, want := range map[string]bool{
		"note": true, "price": true, "target": true, "maybe_dec": true, "maybe_obj": true, "maybe_text": true,
		"count": false, "plain": false, "skipped": false,
	} {
		if got := schemaAcceptsNull(t, s.Properties[name]); got != want {
			b, _ := json.Marshal(s.Properties[name])
			t.Errorf("%s accepts null %v, want %v: %s", name, got, want, b)
		}
	}
	if !schemaAcceptsNull(t, s.Properties["by_key"].AdditionalProperties.(*huma.Schema)) {
		t.Error("map value pointer does not accept null")
	}
	doc, err := json.Marshal(api.OpenAPI())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(doc), `"type":[""`) || strings.Contains(string(doc), `"type":""`) {
		t.Errorf("empty type in %s", doc)
	}
}

// Marking a field nullable keeps its validation: nested and tagged errors
// are still reported, and null still passes.
func TestNullableFieldsStillValidate(t *testing.T) {
	h, _ := nullServer(t)
	for body, want := range map[string]int{
		`{"note":null,"price":null,"target":null,"maybe_dec":null,"maybe_obj":null,"maybe_text":null,"plain":1}`:    200,
		`{"price":"1.5","target":{"zone":"z"},"maybe_dec":2,"maybe_obj":{"zone":"z"},"maybe_text":"abc","plain":1}`: 200,
		`{"maybe_text":"abcd","plain":1}`:  422,
		`{"price":"x","plain":1}`:          422,
		`{"target":{"zone":""},"plain":1}`: 422,
		`{"target":{},"plain":1}`:          422,
		`{"maybe_obj":{},"plain":1}`:       422,
		`{"count":null,"plain":1}`:         422,
	} {
		if rec := do(t, h, http.MethodPost, "/nullable", body); rec.Code != want {
			t.Errorf("%s: %d, want %d: %s", body, rec.Code, want, rec.Body)
		}
	}
}

func TestOptionalMerge(t *testing.T) {
	cur := "cur"
	if got := (Optional[string]{}).Merge(&cur); got != &cur {
		t.Errorf("omitted: %v", got)
	}
	if got := (Optional[string]{Set: true, Null: true}).Merge(&cur); got != nil {
		t.Errorf("null: %v", *got)
	}
	got := (Optional[string]{Set: true, Value: "new"}).Merge(&cur)
	if got == nil || *got != "new" || cur != "cur" {
		t.Errorf("value: %v, cur %q", got, cur)
	}
}

func TestFieldError(t *testing.T) {
	h, api := NewServer(Config{Environment: "test"}, Deps{})
	huma.Register(api, huma.Operation{OperationID: "field-error", Method: http.MethodPost, Path: "/field-error", Tags: []string{PublicTag}},
		func(_ context.Context, _ *struct{}) (*struct{}, error) {
			return nil, FieldError("lines.0.amount", "value is too big")
		})
	rec := do(t, h, http.MethodPost, "/field-error", "")
	p := decodeProblem(t, rec.Body.Bytes())
	if rec.Code != 422 || p.Detail != "value is too big" || len(p.Errors) != 1 ||
		p.Errors[0].Location != "lines.0.amount" || p.Errors[0].Message != "value is too big" {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
}
