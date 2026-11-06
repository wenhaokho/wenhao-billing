// Package recurringschedule implements the pure recurring invoice calendar.
// Integers beyond Go's int range are rejected. Dates follow Python ISO date syntax.
// Out-of-range cycle dates return an error instead of overflowing or looping.
package recurringschedule

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Schedule struct {
	Frequency      string
	Interval       int
	StartDate      time.Time
	EndMode        string
	EndDate        *time.Time
	EndAfterCycles int
}
type Cycle struct {
	Start time.Time
	Index int
}

var errDate = errors.New("date outside Python range")

func validDate(d time.Time) bool {
	return d.Year() >= 1 && d.Year() <= 9999 && d.Hour() == 0 && d.Minute() == 0 && d.Second() == 0 && d.Nanosecond() == 0 && d.Location() == time.UTC
}
func Parse(data []byte) (Schedule, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil || len(m) == 0 {
		return Schedule{}, errors.New("schedule config is empty or invalid")
	}
	var s Schedule
	if err := json.Unmarshal(m["frequency"], &s.Frequency); err != nil || !strings.Contains("|DAILY|WEEKLY|MONTHLY|YEARLY|", "|"+s.Frequency+"|") {
		return s, errors.New("invalid frequency")
	}
	s.Interval = 1
	if v, ok := m["interval"]; ok {
		if string(v) == "true" {
			s.Interval = 1
		} else if err := json.Unmarshal(v, &s.Interval); err != nil || s.Interval < 1 || string(v) == "null" {
			return s, errors.New("invalid interval")
		}
	}
	parseDate := func(v json.RawMessage) (time.Time, error) {
		var str string
		if json.Unmarshal(v, &str) != nil {
			return time.Time{}, errors.New("invalid date")
		}
		d, e := ParseDate(str)
		if e != nil || !validDate(d) {
			return time.Time{}, errors.New("invalid date")
		}
		return d, nil
	}
	var err error
	s.StartDate, err = parseDate(m["start_date"])
	if err != nil {
		return s, err
	}
	s.EndMode = "NEVER"
	if v, ok := m["end_mode"]; ok {
		if string(v) == "null" || json.Unmarshal(v, &s.EndMode) != nil {
			return s, errors.New("invalid end mode")
		}
	}
	switch s.EndMode {
	case "NEVER":
	case "ON_DATE":
		d, e := parseDate(m["end_date"])
		if e != nil {
			return s, e
		}
		s.EndDate = &d
	case "AFTER_N":
		if string(m["end_after_cycles"]) == "true" {
			s.EndAfterCycles = 1
		} else if json.Unmarshal(m["end_after_cycles"], &s.EndAfterCycles) != nil || s.EndAfterCycles < 1 {
			return s, errors.New("invalid end count")
		}
	default:
		return s, errors.New("invalid end mode")
	}
	return s, nil
}
func cycleStart(s Schedule, index int) (time.Time, error) {
	if index < 0 || s.Interval < 1 || !validDate(s.StartDate) {
		return time.Time{}, errDate
	}
	// Limit before multiplication; no valid date can be more than this many days or months away.
	multiplier := 1
	switch s.Frequency {
	case "DAILY":
	case "WEEKLY":
		multiplier = 7
	case "MONTHLY":
	case "YEARLY":
		multiplier = 12
	default:
		return time.Time{}, errors.New("invalid frequency")
	}
	limit := 3652059
	if s.Frequency == "MONTHLY" || s.Frequency == "YEARLY" {
		limit = 119988
	}
	if s.Interval > limit/multiplier || index > limit/(s.Interval*multiplier) {
		if index == 0 {
			return s.StartDate, nil
		}
		return time.Time{}, errDate
	}
	step := s.Interval * index * multiplier
	if s.Frequency == "DAILY" || s.Frequency == "WEEKLY" {
		d := s.StartDate.AddDate(0, 0, step)
		if !validDate(d) {
			return time.Time{}, errDate
		}
		return d, nil
	}
	total := (s.StartDate.Year()-1)*12 + int(s.StartDate.Month()) - 1 + step
	if total >= 119988 {
		return time.Time{}, errDate
	}
	y, m := total/12+1, time.Month(total%12+1)
	last := time.Date(y, m+1, 0, 0, 0, 0, 0, time.UTC).Day()
	d := s.StartDate.Day()
	if d > last {
		d = last
	}
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC), nil
}
func indexFor(s Schedule, d time.Time) (int, error) {
	if !validDate(d) || !validDate(s.StartDate) || d.Before(s.StartDate) || s.Interval < 1 {
		return 0, errDate
	}
	var n int
	switch s.Frequency {
	case "DAILY", "WEEKLY":
		days := ordinal(d) - ordinal(s.StartDate)
		div := s.Interval
		if s.Frequency == "WEEKLY" {
			if div > 3652059/7 {
				return 0, nil
			}
			div *= 7
		}
		n = days / div
	case "MONTHLY", "YEARLY":
		months := (d.Year()-s.StartDate.Year())*12 + int(d.Month()-s.StartDate.Month())
		div := s.Interval
		if s.Frequency == "YEARLY" {
			if div > 119988/12 {
				return 0, nil
			}
			div *= 12
		}
		n = months / div
		start, e := cycleStart(s, n)
		if e != nil {
			return 0, e
		}
		if start.After(d) {
			n--
		}
	default:
		return 0, errors.New("invalid frequency")
	}
	return n, nil
}
func ordinal(d time.Time) int { y := d.Year() - 1; return 365*y + y/4 - y/100 + y/400 + d.YearDay() }
func past(s Schedule, i int, d time.Time) bool {
	return s.EndMode == "ON_DATE" && s.EndDate != nil && d.After(*s.EndDate) || s.EndMode == "AFTER_N" && i >= s.EndAfterCycles
}
func CurrentCycle(s Schedule, d time.Time) (*Cycle, error) {
	if !validDate(d) {
		return nil, errDate
	}
	if d.Before(s.StartDate) {
		return nil, nil
	}
	i, e := indexFor(s, d)
	if e != nil {
		return nil, e
	}
	start, e := cycleStart(s, i)
	if e != nil {
		return nil, e
	}
	if past(s, i, start) {
		return nil, nil
	}
	return &Cycle{start, i}, nil
}
func IsCycleStart(s Schedule, d time.Time) (bool, error) {
	if !validDate(d) {
		return false, errDate
	}
	if d.Before(s.StartDate) {
		return false, nil
	}
	i, e := indexFor(s, d)
	if e != nil {
		return false, e
	}
	start, e := cycleStart(s, i)
	return e == nil && start.Equal(d), e
}
func NextCycleAfter(s Schedule, d time.Time) (*time.Time, error) {
	if !validDate(d) {
		return nil, errDate
	}
	i := 0
	if !d.Before(s.StartDate) {
		n, e := indexFor(s, d)
		if e != nil {
			return nil, e
		}
		i = n + 1
	}
	start, e := cycleStart(s, i)
	if e != nil {
		return nil, e
	}
	if past(s, i, start) {
		return nil, nil
	}
	return &start, nil
}
func CycleKey(c Cycle) string { return c.Start.Format("2006-01-02") }
func IsPaused(data []byte) bool {
	var m map[string]json.RawMessage
	if json.Unmarshal(data, &m) != nil {
		return false
	}
	var v any
	decoder := json.NewDecoder(bytes.NewReader(m["paused"]))
	decoder.UseNumber()
	if decoder.Decode(&v) != nil {
		return false
	}
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case json.Number:
		number := string(x)
		if strings.ContainsAny(number, ".eE") {
			f, _ := strconv.ParseFloat(number, 64)
			return f != 0
		}
		return strings.ContainsAny(number, "123456789")
	case string:
		return x != ""
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	}
	return false
}
func Describe(s Schedule) string {
	i := s.Interval
	switch s.Frequency {
	case "DAILY":
		if i == 1 {
			return "Daily"
		}
		return fmt.Sprintf("Every %d days", i)
	case "WEEKLY":
		if i == 1 {
			return "Weekly on " + s.StartDate.Weekday().String()
		}
		return fmt.Sprintf("Every %d weeks on %s", i, s.StartDate.Weekday())
	case "MONTHLY":
		n := s.StartDate.Day()
		suffix := "th"
		if n%100 < 10 || n%100 > 20 {
			switch n % 10 {
			case 1:
				suffix = "st"
			case 2:
				suffix = "nd"
			case 3:
				suffix = "rd"
			}
		}
		d := strconv.Itoa(n) + suffix
		if i == 1 {
			return "Monthly on the " + d
		}
		return fmt.Sprintf("Every %d months on the %s", i, d)
	case "YEARLY":
		if i == 1 {
			return fmt.Sprintf("Yearly on %s %d", s.StartDate.Month(), s.StartDate.Day())
		}
		return fmt.Sprintf("Every %d years on %s %d", i, s.StartDate.Month(), s.StartDate.Day())
	}
	return "Custom"
}
