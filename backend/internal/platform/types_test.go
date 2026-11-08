package platform

import (
	"encoding/json"
	"testing"
	"time"

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
	if got := mustJSON(t, Dec4{decimal.NewFromInt(1500)}); got != `"1500.0000"` {
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
	if got := mustJSON(t, Dec8{decimal.RequireFromString("16000.5")}); got != `"16000.50000000"` {
		t.Error(got)
	}
}

func TestDec2MarshalsTwoPlaces(t *testing.T) {
	if got := mustJSON(t, Dec2{decimal.RequireFromString("97.5")}); got != `"97.50"` {
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
	tm := Time{time.Date(2026, 1, 1, 10, 0, 0, 123456000, loc)}
	if got := mustJSON(t, tm); got != `"2026-01-01T03:00:00.123456Z"` {
		t.Error(got)
	}
	for in, want := range map[time.Time]string{
		time.Date(2026, 1, 1, 10, 0, 0, 123000000, loc): `"2026-01-01T03:00:00.123000Z"`,
		time.Date(2026, 1, 1, 10, 0, 0, 0, loc):         `"2026-01-01T03:00:00Z"`,
		time.Date(2026, 1, 1, 10, 0, 0, 1000, loc):      `"2026-01-01T03:00:00.000001Z"`,
	} {
		if got := mustJSON(t, Time{in}); got != want {
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
