package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
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
	if got := mustJSON(t, NewDec4(decimal.NewFromInt(1500))); got != `"1500.0000"` {
		t.Error(got)
	}
}

func TestDec4AcceptsNumberAndString(t *testing.T) {
	for _, in := range []string{`12.5`, `"12.5"`} {
		var d Dec4
		if err := json.Unmarshal([]byte(in), &d); err != nil || d.Decimal().String() != "12.5" {
			t.Errorf("%s: %v %v", in, d, err)
		}
	}
	var d Dec4
	if json.Unmarshal([]byte(`"abc"`), &d) == nil {
		t.Error("garbage accepted")
	}
}

func TestDec8MarshalsEightPlaces(t *testing.T) {
	if got := mustJSON(t, NewDec8(decimal.RequireFromString("16000.5"))); got != `"16000.50000000"` {
		t.Error(got)
	}
}

func TestDec2MarshalsTwoPlaces(t *testing.T) {
	if got := mustJSON(t, NewDec2(decimal.RequireFromString("97.5"))); got != `"97.50"` {
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

type notNullLine struct {
	Memo NotNull[string] `json:"memo"`
}

type notNullIn struct {
	Body struct {
		Code   NotNull[string] `json:"code" minLength:"1"`
		Active NotNull[bool]   `json:"active"`
		Amount NotNull[Dec4]   `json:"amount"`
		Lines  []notNullLine   `json:"lines,omitempty"`
	}
}

type notNullOut struct {
	Body struct {
		Seen string `json:"seen"`
	}
}

// NotNull: omitted and a value pass; a null is a 422 at its own location,
// nested ones included, with every invalid field reported; the schema is
// the plain T.
func TestNotNull(t *testing.T) {
	h, api := NewServer(Config{Environment: "test"}, Deps{})
	huma.Register(api, huma.Operation{OperationID: "not-null", Method: http.MethodPatch, Path: "/not-null", Tags: []string{PublicTag}},
		func(_ context.Context, in *notNullIn) (*notNullOut, error) {
			b := in.Body
			out := &notNullOut{}
			out.Body.Seen = fmt.Sprintf("%v:%q %v:%v %v:%s", b.Code.Set, b.Code.Value, b.Active.Set, b.Active.Value, b.Amount.Set, b.Amount.Value.Decimal().StringFixed(4))
			return out, nil
		})
	for body, want := range map[string]string{
		`{}`: `false:"" false:false false:0.0000`,
		`{"code":"x","active":false,"amount":"1.5"}`: `true:"x" true:false true:1.5000`,
	} {
		rec := do(t, h, http.MethodPatch, "/not-null", body)
		var out struct{ Seen string }
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if rec.Code != 200 || out.Seen != want {
			t.Errorf("%s: %d %s, want %s", body, rec.Code, rec.Body, want)
		}
	}
	for body, want := range map[string][]string{
		`{"code":null}`: {"code"},
		`{"code":null,"active":null,"amount":null}`: {"active", "amount", "code"},
		`{"lines":[{"memo":"a"},{"memo":null}]}`:    {"lines.1.memo"},
		`{"code":"","active":null}`:                 {"active", "code"},
	} {
		rec := do(t, h, http.MethodPatch, "/not-null", body)
		p := decodeProblem(t, rec.Body.Bytes())
		var got []string
		for _, e := range p.Errors {
			got = append(got, e.Location)
			if emptyCode := strings.Contains(body, `"code":""`) && e.Location == "code"; !emptyCode && e.Message != "value cannot be null" {
				t.Errorf("%s: %s message %q", body, e.Location, e.Message)
			}
		}
		slices.Sort(got)
		if rec.Code != 422 || p.Detail != "Request validation failed" || !slices.Equal(got, want) {
			t.Errorf("%s: %d %s, want locations %v", body, rec.Code, rec.Body, want)
		}
	}
	// A bad decimal is still reported at the field, as Optional's is.
	rec := do(t, h, http.MethodPatch, "/not-null", `{"amount":"abc"}`)
	if p := decodeProblem(t, rec.Body.Bytes()); rec.Code != 422 || len(p.Errors) == 0 || p.Errors[0].Location != "amount" {
		t.Errorf("bad decimal: %d %s", rec.Code, rec.Body)
	}

	body := api.OpenAPI().Paths["/not-null"].Patch.RequestBody.Content["application/json"].Schema
	s := api.OpenAPI().Components.Schemas.SchemaFromRef(body.Ref)
	for _, name := range []string{"code", "active", "memo"} {
		p := s.Properties[name]
		if name == "memo" {
			p = api.OpenAPI().Components.Schemas.SchemaFromRef(s.Properties["lines"].Items.Ref).Properties["memo"]
		}
		if p == nil || p.Nullable {
			t.Errorf("%s: schema %+v is nullable", name, p)
		}
	}
	if s.Properties["code"].Type != huma.TypeString || *s.Properties["code"].MinLength != 1 || s.Properties["active"].Type != huma.TypeBoolean {
		t.Errorf("schemas %+v %+v", s.Properties["code"], s.Properties["active"])
	}
	if len(s.Required) != 0 {
		t.Errorf("required %v, want none", s.Required)
	}
	if (NotNull[bool]{}).Or(true) != true || (NotNull[bool]{Set: true}).Or(true) != false {
		t.Error("Or")
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

// A request decimal string has the plain decimal syntax; the bounds are the
// type's (and its description's), so a string and a number behave alike.
func TestDecimalRequestSchemaDescribesBounds(t *testing.T) {
	_, api := NewServer(Config{Environment: "test"}, Deps{})
	reg := api.OpenAPI().Components.Schemas
	for typ, want := range map[reflect.Type]string{
		reflect.TypeFor[Dec4](): "At most 4 decimal places and 15 digits before the point.",
		reflect.TypeFor[Dec8](): "At most 8 decimal places and 11 digits before the point.",
		reflect.TypeFor[Dec2](): "At most 2 decimal places and 3 digits before the point.",
	} {
		s := reg.Schema(typ, false, "")
		var str *huma.Schema
		for _, b := range s.OneOf {
			if b.Type == huma.TypeString {
				str = b
			}
		}
		if str == nil || str.Pattern != `^-?[0-9]+(\.[0-9]+)?([eE][-+]?[0-9]+)?$` || s.Description != want {
			t.Errorf("%v: %s", typ, mustJSON(t, s))
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

type boundsBody struct {
	D4 *Dec4 `json:"d4"`
	D8 *Dec8 `json:"d8"`
	D2 *Dec2 `json:"d2"`
}

type boundsIn struct{ Body boundsBody }

// boundsOutBody is its own type: a request and a response never share a
// component (the response one's decimals are strings only).
type boundsOutBody struct {
	D4    *Dec4            `json:"d4"`
	D8    *Dec8            `json:"d8"`
	D2    *Dec2            `json:"d2"`
	List  []Dec4           `json:"list"`
	ByKey map[string]*Dec4 `json:"by_key"`
	Doc   *Dec4            `json:"doc" doc:"Kept."`
}

type boundsOut struct{ Body boundsOutBody }

// boundsServer echoes the decimals it is sent.
func boundsServer(t *testing.T) http.Handler {
	t.Helper()
	h, _ := boundsAPI(t)
	return h
}

func boundsAPI(t *testing.T) (http.Handler, huma.API) {
	t.Helper()
	h, api := NewServer(Config{Environment: "test"}, Deps{})
	huma.Register(api, huma.Operation{OperationID: "bounds", Method: http.MethodPost, Path: "/bounds", Tags: []string{PublicTag}},
		func(_ context.Context, in *boundsIn) (*boundsOut, error) {
			b := in.Body
			return &boundsOut{Body: boundsOutBody{D4: b.D4, D8: b.D8, D2: b.D2, List: []Dec4{}, ByKey: map[string]*Dec4{}}}, nil
		})
	return h, api
}

// A request decimal is a string or a number; a response decimal is the
// string it is written as, with exactly its places, null for a pointer.
func TestDecimalRequestOneOfResponseString(t *testing.T) {
	_, api := boundsAPI(t)
	op := api.OpenAPI().Paths["/bounds"].Post
	reg := api.OpenAPI().Components.Schemas
	req := reg.SchemaFromRef(op.RequestBody.Content["application/json"].Schema.Ref)
	resp := reg.SchemaFromRef(op.Responses["200"].Content["application/json"].Schema.Ref)
	for name, places := range map[string]string{"d4": `^-?[0-9]{1,15}\.[0-9]{4}$`, "d8": `^-?[0-9]{1,11}\.[0-9]{8}$`, "d2": `^-?[0-9]{1,3}\.[0-9]{2}$`} {
		if r := req.Properties[name]; len(r.OneOf) != 3 || r.OneOf[0].Type != huma.TypeString || r.OneOf[1].Type != huma.TypeNumber || r.OneOf[2].Type != huma.TypeNull {
			t.Errorf("request %s: %s", name, mustJSON(t, r))
		}
		if r := resp.Properties[name]; r.OneOf != nil || r.Type != huma.TypeString || !r.Nullable || r.Pattern != places || r.Description != "" {
			t.Errorf("response %s: %s", name, mustJSON(t, r))
		}
	}
	// A response does not carry the request's bounds, but keeps a doc tag.
	if r := req.Properties["d4"]; r.Description != "At most 4 decimal places and 15 digits before the point." {
		t.Errorf("request d4 description: %s", mustJSON(t, r))
	}
	if r := resp.Properties["doc"]; r.Description != "Kept." || r.Type != huma.TypeString {
		t.Errorf("response doc: %s", mustJSON(t, r))
	}
	// A slice item is the plain string; a pointer map value also null.
	four := `^-?[0-9]{1,15}\.[0-9]{4}$`
	if r := resp.Properties["list"].Items; r.OneOf != nil || r.Type != huma.TypeString || r.Nullable || r.Pattern != four {
		t.Errorf("response list items: %s", mustJSON(t, r))
	}
	if r, _ := resp.Properties["by_key"].AdditionalProperties.(*huma.Schema); r == nil || r.OneOf != nil || r.Type != huma.TypeString || !r.Nullable || r.Pattern != four {
		t.Errorf("response by_key values: %s", mustJSON(t, r))
	}
}

// wantBound: body is a 422 whose one error is message at field, as Huma's
// own validation errors are reported. A string and a number get the same.
func wantBound(t *testing.T, h http.Handler, body, field, message string) {
	t.Helper()
	rec := do(t, h, http.MethodPost, "/bounds", body)
	p := decodeProblem(t, rec.Body.Bytes())
	if rec.Code != 422 || p.Detail != "Request validation failed" || len(p.Errors) != 1 ||
		p.Errors[0].Location != field || p.Errors[0].Message != message {
		t.Errorf("%s: %d %s, want only %q at %s", body, rec.Code, rec.Body, message, field)
	}
}

// wantEcho: body is accepted and the field is written back as want.
func wantEcho(t *testing.T, h http.Handler, body, field, want string) {
	t.Helper()
	rec := do(t, h, http.MethodPost, "/bounds", body)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != 200 || out[field] != want {
		t.Errorf("%s: %d %s, want %s %q", body, rec.Code, rec.Body, field, want)
	}
}

func TestDec4RefusesFivePlaces(t *testing.T) {
	h := boundsServer(t)
	for _, body := range []string{`{"d4":1.23456}`, `{"d4":"1.23456"}`, `{"d4":-0.00001}`} {
		wantBound(t, h, body, "d4", "value has too many decimal places (max 4)")
	}
	wantEcho(t, h, `{"d4":1.2345}`, "d4", "1.2345")
}

func TestDec4AllowsTrailingZeros(t *testing.T) {
	h := boundsServer(t)
	for _, body := range []string{`{"d4":1.50000}`, `{"d4":"1.50000"}`} {
		wantEcho(t, h, body, "d4", "1.5000")
	}
	wantEcho(t, h, `{"d4":999999999999999.99990000}`, "d4", "999999999999999.9999")
}

// Exponents, signs and leading zeros, as a number or a string.
func TestDec4Spellings(t *testing.T) {
	h := boundsServer(t)
	for body, want := range map[string]string{
		`{"d4":1e3}`:     "1000.0000",
		`{"d4":"1e3"}`:   "1000.0000",
		`{"d4":1.5E-2}`:  "0.0150",
		`{"d4":-0}`:      "0.0000",
		`{"d4":"-0.0"}`:  "0.0000",
		`{"d4":"007.5"}`: "7.5000",
	} {
		wantEcho(t, h, body, "d4", want)
	}
	wantBound(t, h, `{"d4":"1e-5"}`, "d4", "value has too many decimal places (max 4)")
	wantBound(t, h, `{"d4":"1e15"}`, "d4", "value has too many digits (max 15 before the point)")
}

// shopspring/decimal (v1.6.0) refuses an exponent beyond MaxDecodeExponent
// (10000) as unparseable, so today 1e-2000000000 is "not a valid decimal".
func TestDecimalExponentBeyondDecodeLimitIsInvalid(t *testing.T) {
	h := boundsServer(t)
	for _, body := range []string{`{"d4":1e-2000000000}`, `{"d4":"0e2000000000"}`} {
		rec := do(t, h, http.MethodPost, "/bounds", body)
		p := decodeProblem(t, rec.Body.Bytes())
		if rec.Code != 422 || p.Detail != decimalSentence || len(p.Errors) != 1 || p.Errors[0].Location != "d4" {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}
	for body, want := range map[string]string{`{"d4":0e10000}`: "0.0000", `{"d4":"0e-10000"}`: "0.0000"} {
		wantEcho(t, h, body, "d4", want)
	}
	wantBound(t, h, `{"d4":1e-10000}`, "d4", "value has too many decimal places (max 4)")
	wantBound(t, h, `{"d4":"1e10000"}`, "d4", "value has too many digits (max 15 before the point)")
}

// A huge exponent is decided without expanding it: each request finishes
// well within the deadline (rescaling 1e-2000000000 took minutes and
// hundreds of megabytes). The library's decode limit is lifted here (it is
// a global anyone may raise), so the bounds alone are what keep it cheap.
// Not parallel: no parallel test runs while a sequential one does.
func TestDec4HugeExponentsAreCheap(t *testing.T) {
	limit := decimal.MaxDecodeExponent
	decimal.MaxDecodeExponent = math.MaxInt32
	t.Cleanup(func() { decimal.MaxDecodeExponent = limit })
	h := boundsServer(t)
	// The request runs on its own goroutine, which never touches t; the
	// result comes back here, where it is checked. A request still running
	// at the deadline is abandoned.
	within := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		done := make(chan *httptest.ResponseRecorder, 1)
		start := time.Now()
		go func() {
			req := httptest.NewRequest(http.MethodPost, "/bounds", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			done <- rec
		}()
		select {
		case rec := <-done:
			t.Logf("%s: %v", body, time.Since(start))
			return rec
		case <-time.After(time.Second):
			t.Fatalf("%s: not done after 1s", body)
			return nil
		}
	}
	for body, message := range map[string]string{
		`{"d4":1e-2000000000}`:    "value has too many decimal places (max 4)",
		`{"d4":"1e-2000000000"}`:  "value has too many decimal places (max 4)",
		`{"d4":"-1e-2000000000"}`: "value has too many decimal places (max 4)",
		`{"d4":"1e2000000000"}`:   "value has too many digits (max 15 before the point)",
	} {
		rec := within(body)
		p := decodeProblem(t, rec.Body.Bytes())
		if rec.Code != 422 || len(p.Errors) != 1 || p.Errors[0].Location != "d4" || p.Errors[0].Message != message {
			t.Errorf("%s: %d %s, want %q", body, rec.Code, rec.Body, message)
		}
	}
	for _, body := range []string{`{"d4":0e2000000000}`, `{"d4":0e-2000000000}`, `{"d4":"0e2000000000"}`, `{"d4":"0e-9"}`} {
		rec := within(body)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if rec.Code != 200 || out["d4"] != "0.0000" {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}
}

// Decimal() is the only way to the value; for a refused value it panics
// (the recoverer answers 500), so a refused amount is never read as zero.
func TestRefusedDecimalCannotBeRead(t *testing.T) {
	panics := func(read func()) (p bool) {
		defer func() { p = recover() != nil }()
		read()
		return false
	}
	var d4 Dec4
	_ = json.Unmarshal([]byte(`1.23456`), &d4)
	var d8 Dec8
	_ = json.Unmarshal([]byte(`123456789012`), &d8)
	var d2 Dec2
	_ = json.Unmarshal([]byte(`"abc"`), &d2)
	for name, read := range map[string]func(){
		"Dec4": func() { d4.Decimal() }, "Dec8": func() { d8.Decimal() }, "Dec2": func() { d2.Decimal() },
	} {
		if !panics(read) {
			t.Errorf("%s: refused value read", name)
		}
	}
	var ok Dec4
	if err := json.Unmarshal([]byte(`1.5`), &ok); err != nil || panics(func() { ok.Decimal() }) || ok.Decimal().String() != "1.5" {
		t.Errorf("valid value: %v", err)
	}
	if got := NewDec8(decimal.RequireFromString("2.5")).Decimal().String(); got != "2.5" {
		t.Errorf("NewDec8: %s", got)
	}
}

// Outside Huma nothing reports the refusal, so a refused value fails closed:
// it never marshals or reaches the database as the zero it holds.
func TestRefusedDecimalFailsClosed(t *testing.T) {
	for _, in := range []string{`1.23456`, `1234567890123456`, `"abc"`} {
		var d Dec4
		_ = json.Unmarshal([]byte(in), &d)
		if b, err := json.Marshal(d); err == nil {
			t.Errorf("%s: marshalled as %s", in, b)
		}
		if v, err := d.Value(); err == nil {
			t.Errorf("%s: database value %v", in, v)
		}
	}
	var d8 Dec8
	_ = json.Unmarshal([]byte(`1.123456789`), &d8)
	var d2 Dec2
	_ = json.Unmarshal([]byte(`1000`), &d2)
	if _, err := json.Marshal(d8); err == nil {
		t.Error("Dec8 marshalled")
	}
	if _, err := d2.Value(); err == nil {
		t.Error("Dec2 valued")
	}
	// A value that fits, decoded into the same variable, clears it.
	var d Dec4
	_ = json.Unmarshal([]byte(`1.23456`), &d)
	if err := json.Unmarshal([]byte(`1.5`), &d); err != nil || mustJSON(t, d) != `"1.5000"` {
		t.Errorf("refit: %v", err)
	}
	if v, err := d.Value(); err != nil || v != "1.5" {
		t.Errorf("value %v %v", v, err)
	}
}

func TestDec4RefusesSixteenDigits(t *testing.T) {
	h := boundsServer(t)
	for _, body := range []string{`{"d4":1234567890123456}`, `{"d4":"1234567890123456"}`, `{"d4":-1000000000000000}`, `{"d4":1e15}`} {
		wantBound(t, h, body, "d4", "value has too many digits (max 15 before the point)")
	}
	wantEcho(t, h, `{"d4":"-999999999999999.9999"}`, "d4", "-999999999999999.9999")
}

func TestDec8RefusesNinePlacesAndTwelveDigits(t *testing.T) {
	h := boundsServer(t)
	for _, body := range []string{`{"d8":1.123456789}`, `{"d8":"1.123456789"}`} {
		wantBound(t, h, body, "d8", "value has too many decimal places (max 8)")
	}
	for _, body := range []string{`{"d8":123456789012}`, `{"d8":"123456789012"}`} {
		wantBound(t, h, body, "d8", "value has too many digits (max 11 before the point)")
	}
	wantEcho(t, h, `{"d8":"99999999999.12345678"}`, "d8", "99999999999.12345678")
	wantEcho(t, h, `{"d8":16000.500000000}`, "d8", "16000.50000000")
}

func TestDec2Bounds(t *testing.T) {
	h := boundsServer(t)
	wantEcho(t, h, `{"d2":999.99}`, "d2", "999.99")
	wantEcho(t, h, `{"d2":"97.5"}`, "d2", "97.50")
	for _, body := range []string{`{"d2":1000}`, `{"d2":"1000"}`} {
		wantBound(t, h, body, "d2", "value has too many digits (max 3 before the point)")
	}
	for _, body := range []string{`{"d2":1.234}`, `{"d2":"1.234"}`} {
		wantBound(t, h, body, "d2", "value has too many decimal places (max 2)")
	}
}

// Every refused bound is listed, each at its field, with the other fields'
// validation errors.
func TestDecimalBoundsListEveryField(t *testing.T) {
	h := boundsServer(t)
	rec := do(t, h, http.MethodPost, "/bounds", `{"d4":1.23456,"d8":123456789012,"d2":1000}`)
	p := decodeProblem(t, rec.Body.Bytes())
	var got []string
	for _, e := range p.Errors {
		got = append(got, e.Location)
	}
	slices.Sort(got)
	if rec.Code != 422 || !slices.Equal(got, []string{"d2", "d4", "d8"}) {
		t.Errorf("%d %s", rec.Code, rec.Body)
	}
}
