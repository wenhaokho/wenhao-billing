package recurringschedule

import (
	"regexp"
	"strconv"
	"time"
)

var isoWeekDate = regexp.MustCompile(`^(\d{4})(-?)W(\d{2})(?:(-?)(\d))?$`)

func ParseDate(key string) (time.Time, error) {
	if m := isoWeekDate.FindStringSubmatch(key); m != nil {
		y, _ := strconv.Atoi(m[1])
		w, _ := strconv.Atoi(m[3])
		day := 1
		if m[5] != "" {
			if m[4] != m[2] {
				return time.Time{}, &time.ParseError{}
			}
			day, _ = strconv.Atoi(m[5])
		}
		if y < 1 || w < 1 || w > 53 || day < 1 || day > 7 {
			return time.Time{}, &time.ParseError{}
		}
		jan4 := time.Date(y, 1, 4, 0, 0, 0, 0, time.UTC)
		offset := (int(jan4.Weekday()) + 6) % 7
		d := jan4.AddDate(0, 0, -offset+(w-1)*7+day-1)
		iy, iw := d.ISOWeek()
		if iy != y || iw != w || d.Year() > 9999 {
			return time.Time{}, &time.ParseError{}
		}
		return d, nil
	}
	layout := "2006-01-02"
	if len(key) == 8 {
		layout = "20060102"
	}
	d, err := time.Parse(layout, key)
	if err == nil && (d.Year() < 1 || d.Year() > 9999) {
		return time.Time{}, &time.ParseError{}
	}
	return d, err
}
