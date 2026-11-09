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
	Note      *string              `json:"note"`
	Price     *Dec4                `json:"price"`
	Target    *nullTarget          `json:"target"`
	MaybeDec  Optional[Dec4]       `json:"maybe_dec"`
	MaybeObj  Optional[nullTarget] `json:"maybe_obj"`
	MaybeText Optional[string]     `json:"maybe_text" maxLength:"3"`
	Count     NotNull[int32]       `json:"count"`
	Lines     []nullTarget         `json:"lines,omitempty"`
	Skipped   *string              `json:"skipped,omitempty"`
	Plain     Dec4                 `json:"plain"`
}

type nullOutBody struct {
	Note    *string                `json:"note"`
	Price   *Dec4                  `json:"price"`
	Target  *nullTarget            `json:"target"`
	Targets []*nullTarget          `json:"targets"`
	ByKey   map[string]*nullTarget `json:"by_key"`
	Plain   Dec4                   `json:"plain"`
}

type nullIn struct{ Body nullBody }

type nullOut struct{ Body nullOutBody }

func nullServer(t *testing.T) (http.Handler, huma.API) {
	t.Helper()
	h, api := NewServer(Config{Environment: "test"}, Deps{})
	huma.Register(api, huma.Operation{OperationID: "nullable", Method: http.MethodPost, Path: "/nullable", Tags: []string{PublicTag}},
		func(_ context.Context, in *nullIn) (*nullOut, error) {
			return &nullOut{Body: nullOutBody{Note: in.Body.Note, Price: in.Body.Price, Target: in.Body.Target, Plain: in.Body.Plain}}, nil
		})
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

// In a response, every pointer field's schema accepts null, whatever its
// shape (a scalar, a decimal's oneOf, a $ref), as do pointer slice items and
// map values. In a request, scalars and decimals do; an object keeps its
// plain $ref, so its field errors survive (Huma lets the null through for a
// property that is not required). NotNull, plain values and omitempty
// pointers never do; and no schema has an empty type.
func TestPointerAndOptionalSchemasAcceptNull(t *testing.T) {
	t.Parallel()
	_, api := nullServer(t)
	reg := api.OpenAPI().Components.Schemas
	check := func(s *huma.Schema, want map[string]bool) {
		t.Helper()
		for name, w := range want {
			if got := schemaAcceptsNull(t, s.Properties[name]); got != w {
				b, _ := json.Marshal(s.Properties[name])
				t.Errorf("%s accepts null %v, want %v: %s", name, got, w, b)
			}
		}
	}
	req := reg.SchemaFromRef(reg.Schema(reflect.TypeFor[nullBody](), true, "").Ref)
	check(req, map[string]bool{
		"note": true, "price": true, "maybe_dec": true, "maybe_text": true,
		"target": false, "maybe_obj": false, "count": false, "plain": false, "skipped": false,
	})
	if req.Properties["target"].Ref == "" || req.Properties["maybe_obj"].Ref == "" {
		t.Errorf("request objects are not plain $refs: %+v %+v", req.Properties["target"], req.Properties["maybe_obj"])
	}
	resp := reg.SchemaFromRef(reg.Schema(reflect.TypeFor[nullOutBody](), true, "").Ref)
	check(resp, map[string]bool{"note": true, "price": true, "target": true, "plain": false})
	if !schemaAcceptsNull(t, resp.Properties["targets"].Items) {
		t.Error("slice item pointer does not accept null")
	}
	if !schemaAcceptsNull(t, resp.Properties["by_key"].AdditionalProperties.(*huma.Schema)) {
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

// Nullable request fields keep their validation: null passes, and tagged,
// decimal and nested errors are reported at their own fields.
func TestNullableFieldsStillValidate(t *testing.T) {
	t.Parallel()
	h, _ := nullServer(t)
	for _, body := range []string{
		`{"note":null,"price":null,"target":null,"maybe_dec":null,"maybe_obj":null,"maybe_text":null,"plain":1}`,
		`{"price":"1.5","target":{"zone":"z"},"maybe_dec":2,"maybe_obj":{"zone":"z"},"maybe_text":"abc","plain":1}`,
	} {
		if rec := do(t, h, http.MethodPost, "/nullable", body); rec.Code != 200 {
			t.Errorf("%s: %d: %s", body, rec.Code, rec.Body)
		}
	}
	for body, location := range map[string]string{
		`{"maybe_text":"abcd","plain":1}`:   "maybe_text",
		`{"price":"x","plain":1}`:           "price",
		`{"maybe_dec":"1.23456","plain":1}`: "maybe_dec",
		`{"target":{"zone":""},"plain":1}`:  "target.zone",
		`{"target":{},"plain":1}`:           "target.zone",
		`{"maybe_obj":{},"plain":1}`:        "maybe_obj.zone",
		`{"count":null,"plain":1}`:          "count",
	} {
		rec := do(t, h, http.MethodPost, "/nullable", body)
		p := decodeProblem(t, rec.Body.Bytes())
		if rec.Code != 422 || len(p.Errors) == 0 || p.Errors[0].Location != location {
			t.Errorf("%s: %d, want 422 at %s: %s", body, rec.Code, location, rec.Body)
		}
	}
}

func TestOptionalMerge(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
