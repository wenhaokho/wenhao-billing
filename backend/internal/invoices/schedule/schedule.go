// Package schedule is the pure recurring-invoice schedule math, ported line for
// line from Python's services/recurring_schedule.py (and the cycle-key bridge in
// workers/tasks/recurring.py). A recurring template stores its schedule as a
// JSON object in billing_cycle_ref; Parse turns it into a Schedule and the
// other functions answer "is a cycle due today" and "when is the next one".
//
// Dates are calendar dates, carried as time.Time at UTC midnight. Month
// arithmetic clamps the day to the target month and is always computed from
// Start (31 Jan, 28 Feb, 31 Mar), never from the previous cycle.
//
// Port differences from Python, both deliberate:
//   - start_date and end_date accept only YYYY-MM-DD; Python's
//     date.fromisoformat also takes forms such as 20260301 or 2026-W10-1.
//   - a whole-number float64 (what encoding/json yields for 2 and for 2.0
//     alike) is accepted as an integer, while a json.Number whose text has a
//     decimal point or exponent ("2.0", "1e2") is rejected exactly as Python
//     rejects the float json.loads gives for it.
package schedule

import (
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"
)

// ErrSchedule is matched (errors.Is) by every error Parse returns; the error
// text is Python's sentence.
var ErrSchedule = errors.New("schedule")

type scheduleError struct{ msg string }

func (e scheduleError) Error() string        { return e.msg }
func (e scheduleError) Is(target error) bool { return target == ErrSchedule }

func fail(msg string) error { return scheduleError{msg: msg} }

// Schedule is a parsed schedule config.
type Schedule struct {
	Frequency      string
	Interval       int
	Start          time.Time
	EndMode        string
	EndDate        *time.Time
	EndAfterCycles *int
}

// Cycle is one billing cycle: its 0-based index since Start and its first day.
type Cycle struct {
	Index int
	Start time.Time
}

const dateLayout = "2006-01-02"

var frequencies = map[string]bool{"DAILY": true, "WEEKLY": true, "MONTHLY": true, "YEARLY": true}

// Parse is parse_schedule (recurring_schedule.py:65-101).
func Parse(cfg map[string]any) (Schedule, error) {
	if len(cfg) == 0 {
		return Schedule{}, fail("schedule config is empty")
	}
	frequency, isStr := cfg["frequency"].(string)
	if !isStr || !frequencies[frequency] {
		return Schedule{}, fail("invalid frequency: " + repr(cfg["frequency"]))
	}

	interval := any(1)
	if v, present := cfg["interval"]; present {
		interval = v
	}
	n, ok := asInt(interval)
	if !ok || n < 1 {
		return Schedule{}, fail("interval must be a positive int, got " + repr(interval))
	}

	start, err := parseDate(cfg["start_date"], "start_date")
	if err != nil {
		return Schedule{}, err
	}

	endMode := any("NEVER")
	if v, present := cfg["end_mode"]; present {
		endMode = v
	}
	mode, isStr := endMode.(string)
	if !isStr || (mode != "NEVER" && mode != "ON_DATE" && mode != "AFTER_N") {
		return Schedule{}, fail("invalid end_mode: " + repr(endMode))
	}

	s := Schedule{Frequency: frequency, Interval: n, Start: start, EndMode: mode}
	switch mode {
	case "ON_DATE":
		if cfg["end_date"] == nil {
			return Schedule{}, fail("end_mode=ON_DATE requires end_date")
		}
		end, err := parseDate(cfg["end_date"], "end_date")
		if err != nil {
			return Schedule{}, err
		}
		s.EndDate = &end
	case "AFTER_N":
		c, ok := asInt(cfg["end_after_cycles"])
		if !ok || c < 1 {
			return Schedule{}, fail("end_mode=AFTER_N requires positive integer end_after_cycles")
		}
		s.EndAfterCycles = &c
	}
	return s, nil
}

// parseDate is _parse_date (:54-62), restricted to YYYY-MM-DD.
func parseDate(v any, field string) (time.Time, error) {
	switch x := v.(type) {
	case time.Time:
		return dateOf(x), nil
	case string:
		if t, ok := parseISODate(x); ok {
			return t, nil
		}
		return time.Time{}, fail("invalid ISO date for " + field + ": " + repr(x))
	}
	return time.Time{}, fail("missing or invalid " + field)
}

func parseISODate(s string) (time.Time, bool) {
	if len(s) != len(dateLayout) {
		return time.Time{}, false
	}
	t, err := time.Parse(dateLayout, s)
	if err != nil || t.Year() < 1 {
		return time.Time{}, false
	}
	return t, true
}

// asInt mirrors Python's isinstance(x, int): bool counts (true is 1), a float
// does not. See the package comment for the float64 and json.Number rule.
func asInt(v any) (int, bool) {
	switch x := v.(type) {
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	case int:
		return x, true
	case int32:
		return int(x), true
	case int64:
		return int(x), true
	case float64: //nolint:forbidigo // not money: a JSON number that must be a whole count
		if x != math.Trunc(x) || math.Abs(x) > 1<<53 {
			return 0, false
		}
		return int(x), true
	case json.Number: // an int only when its text has no fraction or exponent
		n, err := strconv.ParseInt(x.String(), 10, 64)
		if err != nil {
			return 0, false
		}
		return int(n), true
	}
	return 0, false
}

// repr is Python's repr for the JSON-shaped values a config can hold.
func repr(v any) string {
	switch x := v.(type) {
	case nil:
		return "None"
	case bool:
		if x {
			return "True"
		}
		return "False"
	case string:
		return ReprString(x)
	case int:
		return strconv.Itoa(x)
	case int32:
		return strconv.Itoa(int(x))
	case int64:
		return strconv.FormatInt(x, 10)
	case float64: //nolint:forbidigo // not money: Python repr of a JSON number
		return reprFloat(x)
	case json.Number: // an int when it has no fraction or exponent, else a float
		s := x.String()
		if _, err := strconv.ParseInt(s, 10, 64); err == nil {
			return s
		}
		if f, err := x.Float64(); err == nil {
			return reprFloat(f)
		}
		return s
	}
	return "<unsupported>"
}

func reprFloat(f float64) string { //nolint:forbidigo // not money: Python repr of a JSON number
	switch {
	case math.IsNaN(f):
		return "nan"
	case math.IsInf(f, 1):
		return "inf"
	case math.IsInf(f, -1):
		return "-inf"
	}
	a := math.Abs(f)
	var s string
	if a == 0 || (a >= 1e-4 && a < 1e16) {
		s = strconv.FormatFloat(f, 'f', -1, 64)
		if !strings.Contains(s, ".") {
			s += ".0"
		}
		return s
	}
	return strconv.FormatFloat(f, 'e', -1, 64)
}

// ReprString is repr(str): single quotes unless the text has a single quote and
// no double quote.
func ReprString(s string) string {
	q := byte('\'')
	if strings.Contains(s, "'") && !strings.Contains(s, `"`) {
		q = '"'
	}
	var b strings.Builder
	b.WriteByte(q)
	for _, r := range s {
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r == rune(q):
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			b.WriteString(`\x` + string("0123456789abcdef"[r>>4]) + string("0123456789abcdef"[r&15]))
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte(q)
	return b.String()
}

// ---------------------------------------------------------------------------
// cycle math
// ---------------------------------------------------------------------------

// dateOf is the calendar date of t in its own location, at UTC midnight.
func dateOf(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// daysBetween is whole calendar days from a to b (both at UTC midnight).
func daysBetween(a, b time.Time) int { return int((b.Unix() - a.Unix()) / 86400) }

// AddMonths is _add_months: d moved by whole months, its day clamped to the
// last day of the month it lands in.
func AddMonths(d time.Time, months int) time.Time {
	total := int(d.Month()) - 1 + months
	year := d.Year() + total/12
	month := total%12 + 1
	last := time.Date(year, time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC).Day()
	return time.Date(year, time.Month(month), min(d.Day(), last), 0, 0, 0, 0, time.UTC)
}

func (s Schedule) startForIndex(index int) time.Time {
	step := s.Interval * index
	switch s.Frequency {
	case "DAILY":
		return s.Start.AddDate(0, 0, step)
	case "WEEKLY":
		return s.Start.AddDate(0, 0, step*7)
	case "MONTHLY":
		return AddMonths(s.Start, step)
	default: // YEARLY
		return AddMonths(s.Start, step*12)
	}
}

// indexForDay is the largest index whose cycle start is on or before day
// (day must not precede Start).
func (s Schedule) indexForDay(day time.Time) int {
	switch s.Frequency {
	case "DAILY":
		return daysBetween(s.Start, day) / s.Interval
	case "WEEKLY":
		return daysBetween(s.Start, day) / (s.Interval * 7)
	}
	unit := 1
	if s.Frequency == "YEARLY" {
		unit = 12
	}
	months := (day.Year()-s.Start.Year())*12 + int(day.Month()) - int(s.Start.Month())
	est := months / (s.Interval * unit)
	for est > 0 && s.startForIndex(est).After(day) {
		est--
	}
	for !s.startForIndex(est + 1).After(day) {
		est++
	}
	return est
}

func (s Schedule) pastEnd(index int, start time.Time) bool {
	switch s.EndMode {
	case "ON_DATE":
		return start.After(*s.EndDate)
	case "AFTER_N":
		return index >= *s.EndAfterCycles
	}
	return false
}

// Current is current_cycle (:169-177): the cycle today falls inside, or false
// before the start or after the end.
func (s Schedule) Current(today time.Time) (Cycle, bool) {
	today = dateOf(today)
	if today.Before(s.Start) {
		return Cycle{}, false
	}
	index := s.indexForDay(today)
	start := s.startForIndex(index)
	if s.pastEnd(index, start) {
		return Cycle{}, false
	}
	return Cycle{Index: index, Start: start}, true
}

// IsCycleStart is is_cycle_start (:180-191): day is exactly the first day of
// one of the cycles. It ignores the end condition, by design.
func (s Schedule) IsCycleStart(day time.Time) bool {
	day = dateOf(day)
	if day.Before(s.Start) {
		return false
	}
	return s.startForIndex(s.indexForDay(day)).Equal(day)
}

// NextAfter is next_cycle_after (:194-204): the first cycle starting strictly
// after the given day, or false when it is past the end.
func (s Schedule) NextAfter(after time.Time) (Cycle, bool) {
	after = dateOf(after)
	next := 0
	if !after.Before(s.Start) {
		next = s.indexForDay(after) + 1
	}
	start := s.startForIndex(next)
	if s.pastEnd(next, start) {
		return Cycle{}, false
	}
	return Cycle{Index: next, Start: start}, true
}

// Key is cycle_key: the ISO date of the cycle's first day.
func Key(c Cycle) string { return c.Start.Format(dateLayout) }

// IsPaused is is_paused (:211-215): Python's bool(cfg.get("paused", False)).
func IsPaused(cfg map[string]any) bool {
	return len(cfg) != 0 && truthy(cfg["paused"])
}

func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case int:
		return x != 0
	case int32:
		return x != 0
	case int64:
		return x != 0
	case float64: //nolint:forbidigo // not money: Python truthiness of a JSON number
		return x != 0
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	case json.Number:
		f, err := x.Float64()
		return err != nil || f != 0
	}
	return true
}

// CycleKeyFor is _cycle_key_for (workers/tasks/recurring.py:29-44): the ISO
// cycle-start date if a cycle is due today. A paused, malformed or not-due
// template gives false, so one bad template cannot stop the scan.
func CycleKeyFor(cfg map[string]any, today time.Time) (string, bool) {
	if IsPaused(cfg) {
		return "", false
	}
	s, err := Parse(cfg)
	if err != nil {
		return "", false
	}
	c, ok := s.Current(today)
	if !ok {
		return "", false
	}
	return Key(c), true
}

// ---------------------------------------------------------------------------
// describe
// ---------------------------------------------------------------------------

var weekdayNames = [...]string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}

func ordinal(n int) string {
	suffix := "th"
	if r := n % 100; r < 10 || r > 20 {
		switch n % 10 {
		case 1:
			suffix = "st"
		case 2:
			suffix = "nd"
		case 3:
			suffix = "rd"
		}
	}
	return strconv.Itoa(n) + suffix
}

// Describe is describe (:242-262).
func Describe(s Schedule) string {
	i := strconv.Itoa(s.Interval)
	switch s.Frequency {
	case "DAILY":
		if s.Interval == 1 {
			return "Daily"
		}
		return "Every " + i + " days"
	case "WEEKLY":
		weekday := weekdayNames[(int(s.Start.Weekday())+6)%7]
		if s.Interval == 1 {
			return "Weekly on " + weekday
		}
		return "Every " + i + " weeks on " + weekday
	case "MONTHLY":
		day := ordinal(s.Start.Day())
		if s.Interval == 1 {
			return "Monthly on the " + day
		}
		return "Every " + i + " months on the " + day
	case "YEARLY":
		md := s.Start.Month().String() + " " + strconv.Itoa(s.Start.Day())
		if s.Interval == 1 {
			return "Yearly on " + md
		}
		return "Every " + i + " years on " + md
	}
	return "Custom"
}
