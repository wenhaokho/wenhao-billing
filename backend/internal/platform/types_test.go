package platform

import (
	"cmp"
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/shopspring/decimal"
)

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestDec4MarshalsFourPlaces(t *testing.T) {
	if got := mustJSON(t, Dec4{Decimal: decimal.NewFromInt(1500)}); got != `"1500.0000"` {
		t.Error(got)
	}
}

func TestDec4AcceptsNumberAndString(t *testing.T) {
	for _, in := range []string{`12.5`, `"12.5"`} {
		var d Dec4
		if err := json.Unmarshal([]byte(in), &d); err != nil || d.String() != "12.5" {
			t.Errorf("%s: %v %v", in, d, err)
		}
	}
	var d Dec4
	if json.Unmarshal([]byte(`"abc"`), &d) == nil {
		t.Error("garbage accepted")
	}
}

func TestDec8MarshalsEightPlaces(t *testing.T) {
	if got := mustJSON(t, Dec8{Decimal: decimal.RequireFromString("16000.5")}); got != `"16000.50000000"` {
		t.Error(got)
	}
}

func TestDec2MarshalsTwoPlaces(t *testing.T) {
	if got := mustJSON(t, Dec2{Decimal: decimal.RequireFromString("97.5")}); got != `"97.50"` {
		t.Error(got)
	}
}

func TestNullDecimalIsNull(t *testing.T) {
	type row struct {
		Amount *Dec4 `json:"amount"`
	}
	if got := mustJSON(t, row{}); got != `{"amount":null}` {
		t.Error(got)
	}
	var r row
	if err := json.Unmarshal([]byte(`{"amount":null}`), &r); err != nil || r.Amount != nil {
		t.Errorf("%v %v", r, err)
	}
}

// Constructors initialise slices; this pins the convention on a sample type.
func TestNilSliceField(t *testing.T) {
	type resp struct {
		Items []string `json:"items"`
	}
	newResp := func() resp { return resp{Items: []string{}} }
	if got := mustJSON(t, newResp()); got != `{"items":[]}` {
		t.Error(got)
	}
}

func TestDateRoundTrip(t *testing.T) {
	var d Date
	if err := json.Unmarshal([]byte(`"2026-03-09"`), &d); err != nil {
		t.Fatal(err)
	}
	if got := mustJSON(t, d); got != `"2026-03-09"` {
		t.Error(got)
	}
	if json.Unmarshal([]byte(`"09/03/2026"`), &d) == nil {
		t.Error("bad date accepted")
	}
}

func TestTimeIsUTCWithZ(t *testing.T) {
	loc := time.FixedZone("WIB", 7*3600)
	tm := Time{Time: time.Date(2026, 1, 1, 10, 0, 0, 123456000, loc)}
	if got := mustJSON(t, tm); got != `"2026-01-01T03:00:00.123456Z"` {
		t.Error(got)
	}
	for in, want := range map[time.Time]string{
		time.Date(2026, 1, 1, 10, 0, 0, 123000000, loc): `"2026-01-01T03:00:00.123000Z"`,
		time.Date(2026, 1, 1, 10, 0, 0, 0, loc):         `"2026-01-01T03:00:00Z"`,
		time.Date(2026, 1, 1, 10, 0, 0, 1000, loc):      `"2026-01-01T03:00:00.000001Z"`,
	} {
		if got := mustJSON(t, Time{Time: in}); got != want {
			t.Errorf("%v: %s, want %s", in, got, want)
		}
	}
	var back Time
	if err := json.Unmarshal([]byte(`"2026-01-01T10:00:00.123456+07:00"`), &back); err != nil || !back.Equal(tm.Time) || back.Location() != time.UTC {
		t.Errorf("%v %v", back, err)
	}
}

func TestOptionalThreeStates(t *testing.T) {
	type patch struct {
		Note Optional[string] `json:"note"`
	}
	cases := map[string]Optional[string]{
		`{}`:            {},
		`{"note":null}`: {Set: true, Null: true},
		`{"note":"x"}`:  {Set: true, Value: "x"},
	}
	for in, want := range cases {
		var p patch
		if err := json.Unmarshal([]byte(in), &p); err != nil || p.Note != want {
			t.Errorf("%s: %+v %v", in, p.Note, err)
		}
	}
}

type refuseNullIn struct {
	Body struct {
		Code   Optional[string] `json:"code"`
		Active Optional[bool]   `json:"active"`
	}
}

// RefuseNull answers a null as the client's 422 at the field; an omitted
// member or a value passes.
func TestOptionalRefuseNull(t *testing.T) {
	h, api := NewServer(Config{Environment: "test"}, Deps{})
	huma.Register(api, huma.Operation{OperationID: "refuse-null", Method: http.MethodPatch, Path: "/refuse-null", Tags: []string{PublicTag}},
		func(_ context.Context, in *refuseNullIn) (*struct{}, error) {
			return nil, cmp.Or(in.Body.Code.RefuseNull("code"), in.Body.Active.RefuseNull("active"))
		})
	for body, field := range map[string]string{
		`{"code":null}`:               "code",
		`{"active":null}`:             "active",
		`{"code":null,"active":null}`: "code",
		`{"code":"x","active":null}`:  "active",
	} {
		rec := do(t, h, http.MethodPatch, "/refuse-null", body)
		p := decodeProblem(t, rec.Body.Bytes())
		want := field + " cannot be null"
		if rec.Code != 422 || p.Detail != want || len(p.Errors) != 1 || p.Errors[0].Location != field || p.Errors[0].Message != want {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}
	for _, body := range []string{`{}`, `{"code":"x","active":false}`} {
		if rec := do(t, h, http.MethodPatch, "/refuse-null", body); rec.Code != 204 {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}
}

type optionalParamIn struct {
	Active OptionalParam[bool] `query:"active"`
}

type optionalParamOut struct {
	Body struct {
		Active *bool `json:"active"`
	}
}

// OptionalParam tells an absent parameter from false, and refuses a value
// that is not a T.
func TestOptionalParam(t *testing.T) {
	h, api := NewServer(Config{Environment: "test"}, Deps{})
	huma.Register(api, huma.Operation{OperationID: "optional-param", Method: http.MethodGet, Path: "/optional-param", Tags: []string{PublicTag}},
		func(_ context.Context, in *optionalParamIn) (*optionalParamOut, error) {
			out := &optionalParamOut{}
			out.Body.Active = in.Active.Ptr()
			return out, nil
		})
	for query, want := range map[string]string{
		"":              `{"active":null}`,
		"?active=false": `{"active":false}`,
		"?active=true":  `{"active":true}`,
	} {
		rec := get(t, h, "/optional-param"+query)
		if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != want {
			t.Errorf("%q: %d %s, want %s", query, rec.Code, rec.Body, want)
		}
	}
	if rec := get(t, h, "/optional-param?active=maybe"); rec.Code != 422 {
		t.Errorf("active=maybe: %d %s", rec.Code, rec.Body)
	}
	p := api.OpenAPI().Paths["/optional-param"].Get.Parameters[0]
	if p.Name != "active" || p.In != "query" || p.Required || p.Schema.Type != huma.TypeBoolean {
		t.Errorf("documented as %+v %+v", p, p.Schema)
	}
}

func TestDecimalSchemaPatternPerScale(t *testing.T) {
	_, api := NewServer(Config{Environment: "test"}, Deps{})
	reg := api.OpenAPI().Components.Schemas
	for typ, want := range map[reflect.Type]string{
		reflect.TypeFor[Dec4](): `^-?[0-9]+(\.[0-9]{1,4})?$`,
		reflect.TypeFor[Dec8](): `^-?[0-9]+(\.[0-9]{1,8})?$`,
		reflect.TypeFor[Dec2](): `^-?[0-9]+(\.[0-9]{1,2})?$`,
	} {
		s := reg.Schema(typ, false, "")
		var str *huma.Schema
		for _, b := range s.OneOf {
			if b.Type == huma.TypeString {
				str = b
			}
		}
		if str == nil || str.Pattern != want {
			t.Errorf("%v: string branch %+v, want pattern %s", typ, str, want)
		}
	}
}

type parsedIn struct {
	Body parsedBody
}

type parsedLine struct {
	Amount Dec4 `json:"amount"`
}

type parsedBody struct {
	Amount *Dec4           `json:"amount"`
	Rate   *Dec8           `json:"rate"`
	Day    *Date           `json:"day"`
	At     *Time           `json:"at"`
	Lines  []parsedLine    `json:"lines,omitempty"`
	Maybe  Optional[Dec4]  `json:"maybe"`
	When   Optional[Date]  `json:"when"`
	ByKey  map[string]Time `json:"by_key,omitempty"`
}

// Every parse failure of a decimal, date or time is a 422 at the field,
// with a sentence and never a Go type name.
func TestBadDecimalDateTimeIs422WithSentence(t *testing.T) {
	h, api := NewServer(Config{Environment: "test"}, Deps{})
	huma.Register(api, huma.Operation{OperationID: "parsed", Method: http.MethodPost, Path: "/parsed", Tags: []string{PublicTag}},
		func(_ context.Context, _ *parsedIn) (*struct{}, error) { return nil, nil })
	const (
		dec  = "value is not a valid decimal"
		date = "value is not a valid date (YYYY-MM-DD)"
		ts   = "value is not a valid timestamp (RFC 3339)"
	)
	for body, want := range map[string][2]string{
		`{"amount":"abc"}`:                          {"amount", dec},
		`{"rate":"x"}`:                              {"rate", dec},
		`{"day":"2026-13-01"}`:                      {"day", date},
		`{"at":"yesterday"}`:                        {"at", ts},
		`{"lines":[{"amount":1},{"amount":"abc"}]}`: {"lines.1.amount", dec},
		`{"maybe":"abc"}`:                           {"maybe", dec},
		`{"when":"2026-02-30"}`:                     {"when", date},
		`{"by_key":{"k":"nope"}}`:                   {"by_key.k", ts},
	} {
		rec := do(t, h, http.MethodPost, "/parsed", body)
		p := decodeProblem(t, rec.Body.Bytes())
		if rec.Code != 422 || p.Detail != want[1] || len(p.Errors) == 0 ||
			p.Errors[0].Location != want[0] || p.Errors[0].Message != want[1] {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body)
		}
		if strings.Contains(rec.Body.String(), "platform.") || strings.Contains(rec.Body.String(), "Go ") {
			t.Errorf("%s: names a Go type: %s", body, rec.Body)
		}
	}
	// Valid values and omitted ones still reach the handler.
	for _, body := range []string{`{}`, `{"amount":"1.5","day":"2026-02-28","at":"2026-01-01T00:00:00Z","maybe":null,"when":"2026-01-02"}`} {
		if rec := do(t, h, http.MethodPost, "/parsed", body); rec.Code != 204 {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}
}
