package recurringschedule

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

func day(s string) time.Time {
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return d
}
func TestMonthlyClampAndStrictNext(t *testing.T) {
	s, err := Parse([]byte(`{"frequency":"MONTHLY","start_date":"2024-01-31"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		at, start, next string
		index           int
	}{{"2024-02-29", "2024-02-29", "2024-03-31", 1}, {"2024-03-30", "2024-02-29", "2024-03-31", 1}, {"2024-03-31", "2024-03-31", "2024-04-30", 2}} {
		c, err := CurrentCycle(s, day(tc.at))
		if err != nil || c == nil || c.Start != day(tc.start) || c.Index != tc.index {
			t.Fatalf("%s: cycle=%+v err=%v", tc.at, c, err)
		}
		n, err := NextCycleAfter(s, day(tc.at))
		if err != nil || n == nil || *n != day(tc.next) {
			t.Fatalf("%s: next=%v err=%v", tc.at, n, err)
		}
	}
}

func TestValidationAndLimits(t *testing.T) {
	for _, raw := range []string{`{}`, `{"frequency":"DAILY","start_date":"2020-01-01","end_mode":null}`, `{"frequency":"DAILY","interval":0,"start_date":"2020-01-01"}`, `{"frequency":"DAILY","interval":999999999999999999999999,"start_date":"2020-01-01"}`, `{"frequency":"DAILY","start_date":"0000-01-01"}`, `{"frequency":"DAILY","start_date":"2020-01-01","end_mode":"AFTER_N","end_after_cycles":0}`} {
		if _, e := Parse([]byte(raw)); e == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	s, e := Parse([]byte(`{"frequency":"DAILY","start_date":"9999-12-31"}`))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = NextCycleAfter(s, day("9999-12-31")); e == nil {
		t.Fatal("expected overflow error")
	}
}
func TestEndBoundaryAndPause(t *testing.T) {
	s, e := Parse([]byte(`{"frequency":"MONTHLY","start_date":"2024-01-31","end_mode":"AFTER_N","end_after_cycles":2}`))
	if e != nil {
		t.Fatal(e)
	}
	if c, e := CurrentCycle(s, day("2024-03-31")); e != nil || c != nil {
		t.Fatalf("cycle %v %v", c, e)
	}
	if ok, e := IsCycleStart(s, day("2024-03-31")); e != nil || !ok {
		t.Fatalf("boundary %v %v", ok, e)
	}
	if !IsPaused([]byte(`{"paused":[1]}`)) || IsPaused([]byte(`{"paused":[]}`)) {
		t.Fatal("pause truthiness")
	}
}

func TestPythonDifferential(t *testing.T) {
	type item struct {
		Config map[string]any `json:"config"`
		Date   string         `json:"date"`
	}
	type result struct {
		Start       string `json:"start"`
		Index       int    `json:"index"`
		Next        string `json:"next"`
		Boundary    bool   `json:"boundary"`
		Description string `json:"description"`
		Paused      bool   `json:"paused"`
	}
	var items []item
	for _, f := range []string{"DAILY", "WEEKLY", "MONTHLY", "YEARLY"} {
		for _, interval := range []int{1, 2, 7} {
			for _, mode := range []string{"NEVER", "ON_DATE", "AFTER_N"} {
				for _, start := range []string{"2019-01-31", "2020-02-29", "2021-04-30"} {
					cfg := map[string]any{"frequency": f, "interval": interval, "start_date": start, "end_mode": mode, "end_date": "2023-06-30", "end_after_cycles": 5, "paused": true}
					for y := 2018; y <= 2025; y++ {
						for m := 1; m <= 12; m++ {
							for d := 1; d <= time.Date(y, time.Month(m)+1, 0, 0, 0, 0, 0, time.UTC).Day(); d++ {
								items = append(items, item{cfg, fmt.Sprintf("%04d-%02d-%02d", y, m, d)})
							}
						}
					}
				}
			}
		}
	}
	input, _ := json.Marshal(items)
	script := `import json,sys
from datetime import date
from app.services.recurring_schedule import *
out=[]
for x in json.load(sys.stdin):
 s=parse_schedule(x['config']); d=date.fromisoformat(x['date']); c=current_cycle(s,d); n=next_cycle_after(s,d)
 out.append(dict(start=c.start.isoformat() if c else '',index=c.index if c else 0,next=n.isoformat() if n else '',boundary=is_cycle_start(s,d),description=describe(s),paused=is_paused(x['config'])))
json.dump(out,sys.stdout)`
	cmd := exec.Command("python3", "-c", script)
	cmd.Stdin = bytes.NewReader(input)
	out, e := cmd.Output()
	if e != nil {
		t.Fatalf("Python oracle: %v", e)
	}
	var expected []result
	if e = json.Unmarshal(out, &expected); e != nil {
		t.Fatal(e)
	}
	for j, x := range items {
		raw, _ := json.Marshal(x.Config)
		s, e := Parse(raw)
		if e != nil {
			t.Fatal(e)
		}
		d := day(x.Date)
		c, e := CurrentCycle(s, d)
		if e != nil {
			t.Fatal(e)
		}
		n, e := NextCycleAfter(s, d)
		if e != nil {
			t.Fatal(e)
		}
		b, e := IsCycleStart(s, d)
		if e != nil {
			t.Fatal(e)
		}
		got := result{Boundary: b, Description: Describe(s), Paused: IsPaused(raw)}
		if c != nil {
			got.Start = CycleKey(*c)
			got.Index = c.Index
		}
		if n != nil {
			got.Next = n.Format("2006-01-02")
		}
		if got != expected[j] {
			t.Fatalf("%v on %s: got %+v want %+v", x.Config, x.Date, got, expected[j])
		}
	}
}

func TestPythonInvalidConfigDifferential(t *testing.T) {
	cases := []string{`{}`, `{"frequency":"BAD","start_date":"2020-01-01"}`, `{"frequency":"DAILY","interval":0,"start_date":"2020-01-01"}`, `{"frequency":"DAILY","interval":1.5,"start_date":"2020-01-01"}`, `{"frequency":"DAILY","start_date":"2020-02-30"}`, `{"frequency":"DAILY","start_date":"2020-01-01","end_mode":null}`, `{"frequency":"DAILY","start_date":"2020-01-01","end_mode":"ON_DATE"}`, `{"frequency":"DAILY","start_date":"2020-01-01","end_mode":"AFTER_N","end_after_cycles":0}`}
	script := `import json,sys
from app.services.recurring_schedule import parse_schedule,ScheduleError
for cfg in json.load(sys.stdin):
 try: parse_schedule(cfg); print('accepted')
 except ScheduleError: print('rejected')`
	var configs []json.RawMessage
	for _, x := range cases {
		configs = append(configs, json.RawMessage(x))
	}
	data, _ := json.Marshal(configs)
	cmd := exec.Command("python3", "-c", script)
	cmd.Stdin = bytes.NewReader(data)
	out, e := cmd.Output()
	if e != nil {
		t.Fatal(e)
	}
	lines := strings.Fields(string(out))
	if len(lines) != len(cases) {
		t.Fatal("oracle output", string(out))
	}
	for i, x := range cases {
		_, e := Parse([]byte(x))
		if (e == nil) != (lines[i] == "accepted") {
			t.Fatalf("%s: Go error %v, Python %s", x, e, lines[i])
		}
	}
}

func TestLargeJSONPausedAndOverflowParity(t *testing.T) {
	for _, raw := range []string{`{"paused":true,"unrelated":1e400}`, `{"paused":1e400}`} {
		if !IsPaused([]byte(raw)) {
			t.Errorf("paused lost for %s", raw)
		}
	}
	s, e := Parse([]byte(`{"frequency":"DAILY","start_date":"9999-12-31","end_mode":"AFTER_N","end_after_cycles":1}`))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = NextCycleAfter(s, day("9999-12-31")); e == nil {
		t.Error("expected Python date overflow before end check")
	}
}

func TestPythonPausedJSONDifferential(t *testing.T) {
	cases := []string{`{"paused":true,"unrelated":1e400}`, `{"paused":1e400}`, `{"paused":0e400}`, `{"paused":1e-400}`, `{"paused":-1e-400}`, `{"paused":5e-324}`, `{"paused":1e400}`, `{"paused":9999999999999999999999999999999999999999999999}`, `{"paused":-0.0}`, `{"paused":[]}`, `{"paused":{"n":1e400}}`, `{"paused":true`, `null`, `{}`}
	input, _ := json.Marshal(cases)
	script := `import json,sys
from app.services.recurring_schedule import is_paused
for raw in json.load(sys.stdin):
 try: cfg=json.loads(raw); print(int(is_paused(cfg)))
 except (ValueError,TypeError,AttributeError): print(0)`
	cmd := exec.Command("python3", "-c", script)
	cmd.Stdin = bytes.NewReader(input)
	out, e := cmd.Output()
	if e != nil {
		t.Fatal(e)
	}
	lines := strings.Fields(string(out))
	if len(lines) != len(cases) {
		t.Fatal(string(out))
	}
	for i, raw := range cases {
		if got := IsPaused([]byte(raw)); got != (lines[i] == "1") {
			t.Fatalf("%s: Go %v Python %s", raw, got, lines[i])
		}
	}
}

func TestPythonScheduleDateAndBooleanParsing(t *testing.T) {
	dates := []string{"20200101", "2020-01-01", "2020W011", "2020-W01-1", "2020W01", "2020-W01", "2020-W011", "2020W01-1", "2020-001", "202001-01", "2020-0101", "0001-W01-1", "9999-W52-7", "2021-W53-1", "2020-02-30"}
	configs := []map[string]any{}
	for _, d := range dates {
		configs = append(configs, map[string]any{"frequency": "DAILY", "start_date": d, "end_mode": "ON_DATE", "end_date": d})
	}
	for _, v := range []any{true, false, 1, 0, 1.0, "1", nil} {
		configs = append(configs, map[string]any{"frequency": "DAILY", "start_date": "2020-01-01", "interval": v}, map[string]any{"frequency": "DAILY", "start_date": "2020-01-01", "end_mode": "AFTER_N", "end_after_cycles": v})
	}
	input, _ := json.Marshal(configs)
	cmd := exec.Command("python", "-c", `import json,sys
from app.services.recurring_schedule import parse_schedule
out=[]
for cfg in json.load(sys.stdin):
 try:
  s=parse_schedule(cfg); out.append([s.start_date.isoformat(),s.end_date.isoformat() if s.end_date else None,int(s.interval),int(s.end_after_cycles) if s.end_after_cycles is not None else None])
 except Exception: out.append(None)
print(json.dumps(out))`)
	cmd.Stdin = bytes.NewReader(input)
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var expected []any
	if err = json.Unmarshal(out, &expected); err != nil {
		t.Fatal(err)
	}
	for i, cfg := range configs {
		raw, _ := json.Marshal(cfg)
		s, e := Parse(raw)
		if expected[i] == nil {
			if e == nil {
				t.Errorf("%s accepted", raw)
			}
			continue
		}
		if e != nil {
			t.Errorf("%s rejected: %v", raw, e)
			continue
		}
		var end any
		if s.EndDate != nil {
			end = s.EndDate.Format("2006-01-02")
		}
		var count any
		if s.EndMode == "AFTER_N" {
			count = float64(s.EndAfterCycles)
		}
		got := []any{s.StartDate.Format("2006-01-02"), end, float64(s.Interval), count}
		if !reflect.DeepEqual(got, expected[i]) {
			t.Errorf("%s got %v want %v", raw, got, expected[i])
		}
	}
}

func TestParseDateRejectsYearZero(t *testing.T) {
	if _, err := ParseDate("0000-01-01"); err == nil {
		t.Fatal("year zero is outside Python date range")
	}
}
