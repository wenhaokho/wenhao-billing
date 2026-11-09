package schedule_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices/schedule"
)

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

// sch is the Python tests' _sch: a monthly schedule from 2026-05-01 with overrides.
func sch(t *testing.T, over map[string]any) schedule.Schedule {
	t.Helper()
	cfg := map[string]any{"frequency": "MONTHLY", "interval": 1, "start_date": "2026-05-01"}
	for k, v := range over {
		cfg[k] = v
	}
	s, err := schedule.Parse(cfg)
	require.NoError(t, err)
	return s
}

func cur(t *testing.T, s schedule.Schedule, d time.Time) schedule.Cycle {
	t.Helper()
	c, ok := s.Current(d)
	require.True(t, ok, "no cycle on", d)
	return c
}

func noCycle(t *testing.T, s schedule.Schedule, d time.Time) {
	t.Helper()
	_, ok := s.Current(d)
	assert.False(t, ok, "unexpected cycle on", d)
}

// ---- Parse ----

func TestParseMinimalMonthlySchedule(t *testing.T) {
	t.Parallel()
	s, err := schedule.Parse(map[string]any{"frequency": "MONTHLY", "interval": 1, "start_date": "2026-05-01"})
	require.NoError(t, err)
	assert.Equal(t, "MONTHLY", s.Frequency)
	assert.Equal(t, 1, s.Interval)
	assert.Equal(t, day(2026, 5, 1), s.Start)
	assert.Equal(t, "NEVER", s.EndMode)
	assert.Nil(t, s.EndDate)
	assert.Nil(t, s.EndAfterCycles)
}

func TestParseEndOnDate(t *testing.T) {
	t.Parallel()
	s, err := schedule.Parse(map[string]any{
		"frequency": "WEEKLY", "interval": 2, "start_date": "2026-01-05",
		"end_mode": "ON_DATE", "end_date": "2026-06-30",
	})
	require.NoError(t, err)
	assert.Equal(t, "ON_DATE", s.EndMode)
	require.NotNil(t, s.EndDate)
	assert.Equal(t, day(2026, 6, 30), *s.EndDate)
}

func TestParseEndAfterN(t *testing.T) {
	t.Parallel()
	s, err := schedule.Parse(map[string]any{
		"frequency": "MONTHLY", "interval": 1, "start_date": "2026-05-01",
		"end_mode": "AFTER_N", "end_after_cycles": 12,
	})
	require.NoError(t, err)
	assert.Equal(t, "AFTER_N", s.EndMode)
	require.NotNil(t, s.EndAfterCycles)
	assert.Equal(t, 12, *s.EndAfterCycles)
}

func TestParseRejectsBadFrequency(t *testing.T) {
	t.Parallel()
	_, err := schedule.Parse(map[string]any{"frequency": "HOURLY", "interval": 1, "start_date": "2026-05-01"})
	assert.ErrorIs(t, err, schedule.ErrSchedule)
}

func TestParseRejectsNonPositiveInterval(t *testing.T) {
	t.Parallel()
	_, err := schedule.Parse(map[string]any{"frequency": "MONTHLY", "interval": 0, "start_date": "2026-05-01"})
	assert.ErrorIs(t, err, schedule.ErrSchedule)
}

func TestParseRejectsMissingStartDate(t *testing.T) {
	t.Parallel()
	_, err := schedule.Parse(map[string]any{"frequency": "MONTHLY", "interval": 1})
	assert.ErrorIs(t, err, schedule.ErrSchedule)
}

func TestParseRejectsEndOnDateWithoutEndDate(t *testing.T) {
	t.Parallel()
	_, err := schedule.Parse(map[string]any{
		"frequency": "MONTHLY", "interval": 1, "start_date": "2026-05-01", "end_mode": "ON_DATE",
	})
	assert.ErrorIs(t, err, schedule.ErrSchedule)
}

func TestParseRejectsEndAfterNWithoutCount(t *testing.T) {
	t.Parallel()
	_, err := schedule.Parse(map[string]any{
		"frequency": "MONTHLY", "interval": 1, "start_date": "2026-05-01", "end_mode": "AFTER_N",
	})
	assert.ErrorIs(t, err, schedule.ErrSchedule)
}

func TestParseRejectsNoneConfig(t *testing.T) {
	t.Parallel()
	_, err := schedule.Parse(nil)
	assert.ErrorIs(t, err, schedule.ErrSchedule)
}

func TestParseErrorSentences(t *testing.T) {
	t.Parallel()
	base := func(over map[string]any) map[string]any {
		m := map[string]any{"frequency": "MONTHLY", "interval": 1, "start_date": "2026-05-01"}
		for k, v := range over {
			m[k] = v
		}
		return m
	}
	afterN := "end_mode=AFTER_N requires positive integer end_after_cycles"
	cases := []struct {
		name string
		cfg  map[string]any
		want string
	}{
		{"nil", nil, "schedule config is empty"},
		{"empty", map[string]any{}, "schedule config is empty"},
		{"frequency", base(map[string]any{"frequency": "HOURLY"}), "invalid frequency: 'HOURLY'"},
		{"frequency none", map[string]any{"interval": 1, "start_date": "2026-05-01"}, "invalid frequency: None"},
		{"frequency number", base(map[string]any{"frequency": 5}), "invalid frequency: 5"},
		{"frequency lowercase", base(map[string]any{"frequency": "monthly"}), "invalid frequency: 'monthly'"},
		{"frequency quote", base(map[string]any{"frequency": "it's"}), `invalid frequency: "it's"`},
		{"frequency control", base(map[string]any{"frequency": "A\x01B"}), `invalid frequency: 'A\x01B'`},
		// U+0085 and U+200B are not printable: Python escapes them too.
		{"frequency non-printable", base(map[string]any{"frequency": "M\xc2\x85\xe2\x80\x8b"}), `invalid frequency: 'M\x85\` + `u200b'`},
		{"interval zero", base(map[string]any{"interval": 0}), "interval must be a positive int, got 0"},
		{"interval negative", base(map[string]any{"interval": -2}), "interval must be a positive int, got -2"},
		{"interval string", base(map[string]any{"interval": "2"}), "interval must be a positive int, got '2'"},
		{"interval null", base(map[string]any{"interval": nil}), "interval must be a positive int, got None"},
		{"interval false", base(map[string]any{"interval": false}), "interval must be a positive int, got False"},
		{"interval float", base(map[string]any{"interval": 1.5}), "interval must be a positive int, got 1.5"},
		{"interval number text float", base(map[string]any{"interval": json.Number("2.0")}), "interval must be a positive int, got 2.0"},
		{"start missing", map[string]any{"frequency": "MONTHLY"}, "missing or invalid start_date"},
		{"start number", base(map[string]any{"start_date": 20260501}), "missing or invalid start_date"},
		{"start bad", base(map[string]any{"start_date": "05/01/2026"}), "invalid ISO date for start_date: '05/01/2026'"},
		{"start impossible", base(map[string]any{"start_date": "2026-02-30"}), "invalid ISO date for start_date: '2026-02-30'"},
		{"end mode", base(map[string]any{"end_mode": "SOMETIME"}), "invalid end_mode: 'SOMETIME'"},
		{"end mode null", base(map[string]any{"end_mode": nil}), "invalid end_mode: None"},
		{"on date missing", base(map[string]any{"end_mode": "ON_DATE"}), "end_mode=ON_DATE requires end_date"},
		{"on date null", base(map[string]any{"end_mode": "ON_DATE", "end_date": nil}), "end_mode=ON_DATE requires end_date"},
		{"on date bad", base(map[string]any{"end_mode": "ON_DATE", "end_date": "soon"}), "invalid ISO date for end_date: 'soon'"},
		{"on date number", base(map[string]any{"end_mode": "ON_DATE", "end_date": 7}), "missing or invalid end_date"},
		{"after n missing", base(map[string]any{"end_mode": "AFTER_N"}), afterN},
		{"after n zero", base(map[string]any{"end_mode": "AFTER_N", "end_after_cycles": 0}), afterN},
		{"after n string", base(map[string]any{"end_mode": "AFTER_N", "end_after_cycles": "3"}), afterN},
		{"after n fraction", base(map[string]any{"end_mode": "AFTER_N", "end_after_cycles": 2.5}), afterN},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := schedule.Parse(tc.cfg)
			require.Error(t, err)
			assert.Equal(t, tc.want, err.Error())
			assert.ErrorIs(t, err, schedule.ErrSchedule)
		})
	}
}

// TestParseRefusesCompactDates is a port difference (PORT-NOTES 51): Python
// 3.11's date.fromisoformat accepted "20260501"; Go takes YYYY-MM-DD only.
func TestParseRefusesCompactDates(t *testing.T) {
	t.Parallel()
	_, err := schedule.Parse(map[string]any{"frequency": "MONTHLY", "interval": 1, "start_date": "20260501"})
	require.Error(t, err)
	assert.Equal(t, "invalid ISO date for start_date: '20260501'", err.Error())
	assert.ErrorIs(t, err, schedule.ErrSchedule)
}

func TestParseNumberShapes(t *testing.T) {
	t.Parallel()
	ok := func(v any, want int) {
		t.Helper()
		s, err := schedule.Parse(map[string]any{"frequency": "DAILY", "interval": v, "start_date": "2026-05-01"})
		require.NoError(t, err, v)
		assert.Equal(t, want, s.Interval)
	}
	ok(3, 3)
	ok(int64(4), 4)
	ok(float64(5), 5) // what encoding/json gives for 5; indistinguishable from 5.0
	ok(json.Number("6"), 6)
	ok(true, 1) // Python's bool is an int: true is accepted as 1
	_, err := schedule.Parse(map[string]any{"frequency": "DAILY", "interval": json.Number("1e2"), "start_date": "2026-05-01"})
	assert.ErrorIs(t, err, schedule.ErrSchedule)
}

func TestParseIgnoresEndFieldsOfOtherModes(t *testing.T) {
	t.Parallel()
	s, err := schedule.Parse(map[string]any{
		"frequency": "MONTHLY", "start_date": "2026-05-01", "end_mode": "NEVER",
		"end_date": "garbage", "end_after_cycles": "x",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, s.Interval) // interval defaults to 1
	assert.Nil(t, s.EndDate)
	assert.Nil(t, s.EndAfterCycles)
}

// ---- Current: frequencies ----

func TestReturnsNoneBeforeStartDate(t *testing.T) {
	t.Parallel()
	noCycle(t, sch(t, nil), day(2026, 4, 30))
}

func TestStartDateItselfIsCycleZero(t *testing.T) {
	t.Parallel()
	c := cur(t, sch(t, map[string]any{"frequency": "DAILY"}), day(2026, 5, 1))
	assert.Equal(t, day(2026, 5, 1), c.Start)
	assert.Equal(t, 0, c.Index)
}

func TestDailyEveryDayAdvancesIndex(t *testing.T) {
	t.Parallel()
	c := cur(t, sch(t, map[string]any{"frequency": "DAILY", "interval": 1}), day(2026, 5, 3))
	assert.Equal(t, 2, c.Index)
	assert.Equal(t, day(2026, 5, 3), c.Start)
}

func TestDailyIntervalThreeBucketsByThreeDays(t *testing.T) {
	t.Parallel()
	s := sch(t, map[string]any{"frequency": "DAILY", "interval": 3})
	assert.Equal(t, day(2026, 5, 1), cur(t, s, day(2026, 5, 2)).Start)
	assert.Equal(t, day(2026, 5, 1), cur(t, s, day(2026, 5, 3)).Start)
	assert.Equal(t, day(2026, 5, 4), cur(t, s, day(2026, 5, 4)).Start)
	assert.Equal(t, 1, cur(t, s, day(2026, 5, 4)).Index)
}

func TestWeeklyIntervalOneAdvancesBySevenDays(t *testing.T) {
	t.Parallel()
	s := sch(t, map[string]any{"frequency": "WEEKLY", "interval": 1, "start_date": "2026-05-04"})
	assert.Equal(t, day(2026, 5, 4), cur(t, s, day(2026, 5, 4)).Start)
	assert.Equal(t, day(2026, 5, 4), cur(t, s, day(2026, 5, 10)).Start)
	assert.Equal(t, day(2026, 5, 11), cur(t, s, day(2026, 5, 11)).Start)
}

func TestWeeklyIntervalTwoAdvancesEveryFortnight(t *testing.T) {
	t.Parallel()
	s := sch(t, map[string]any{"frequency": "WEEKLY", "interval": 2, "start_date": "2026-05-04"})
	assert.Equal(t, day(2026, 5, 4), cur(t, s, day(2026, 5, 17)).Start)
	assert.Equal(t, day(2026, 5, 18), cur(t, s, day(2026, 5, 18)).Start)
}

func TestMonthlyIntervalOneBasic(t *testing.T) {
	t.Parallel()
	s := sch(t, map[string]any{"frequency": "MONTHLY", "interval": 1})
	assert.Equal(t, day(2026, 5, 1), cur(t, s, day(2026, 5, 15)).Start)
	assert.Equal(t, day(2026, 6, 1), cur(t, s, day(2026, 6, 1)).Start)
	assert.Equal(t, 1, cur(t, s, day(2026, 6, 1)).Index)
	assert.Equal(t, 10, cur(t, s, day(2027, 3, 1)).Index)
}

func TestMonthlyIntervalThreeQuarterly(t *testing.T) {
	t.Parallel()
	s := sch(t, map[string]any{"frequency": "MONTHLY", "interval": 3, "start_date": "2026-01-15"})
	assert.Equal(t, day(2026, 1, 15), cur(t, s, day(2026, 4, 14)).Start)
	assert.Equal(t, day(2026, 4, 15), cur(t, s, day(2026, 4, 15)).Start)
	assert.Equal(t, day(2026, 7, 15), cur(t, s, day(2026, 7, 15)).Start)
}

func TestMonthlyEndOfMonthStartClampsShortMonths(t *testing.T) {
	t.Parallel()
	// Start Jan 31: Feb clamps to 28 (2026 is not a leap year), and March is
	// back on the 31st because every cycle is computed from the start date.
	s := sch(t, map[string]any{"frequency": "MONTHLY", "interval": 1, "start_date": "2026-01-31"})
	assert.Equal(t, day(2026, 2, 28), cur(t, s, day(2026, 2, 28)).Start)
	assert.Equal(t, day(2026, 3, 31), cur(t, s, day(2026, 3, 31)).Start)
}

func TestYearlyIntervalOne(t *testing.T) {
	t.Parallel()
	s := sch(t, map[string]any{"frequency": "YEARLY", "interval": 1})
	assert.Equal(t, day(2026, 5, 1), cur(t, s, day(2027, 4, 30)).Start)
	assert.Equal(t, day(2027, 5, 1), cur(t, s, day(2027, 5, 1)).Start)
	assert.Equal(t, 1, cur(t, s, day(2027, 5, 1)).Index)
}

// ---- end conditions ----

func TestEndOnDateInclusiveOnTheDay(t *testing.T) {
	t.Parallel()
	s := sch(t, map[string]any{"start_date": "2026-01-01", "end_mode": "ON_DATE", "end_date": "2026-06-30"})
	_, ok := s.Current(day(2026, 6, 30))
	assert.True(t, ok)
	noCycle(t, s, day(2026, 7, 1))
}

func TestEndOnDateIsInclusiveWhenItIsACycleStart(t *testing.T) {
	t.Parallel()
	// The ported test above ends mid-cycle, so it cannot tell an inclusive end
	// date from an exclusive one; here the end date is itself a cycle start.
	s := sch(t, map[string]any{"start_date": "2026-01-01", "end_mode": "ON_DATE", "end_date": "2026-03-01"})
	assert.Equal(t, day(2026, 3, 1), cur(t, s, day(2026, 3, 1)).Start)
	noCycle(t, s, day(2026, 4, 1))
	_, ok := s.NextAfter(day(2026, 2, 1))
	assert.True(t, ok)
	_, ok = s.NextAfter(day(2026, 3, 1))
	assert.False(t, ok)
}

func TestEndAfterNCapsByIndex(t *testing.T) {
	t.Parallel()
	s := sch(t, map[string]any{"start_date": "2026-01-01", "end_mode": "AFTER_N", "end_after_cycles": 3})
	assert.Equal(t, 0, cur(t, s, day(2026, 1, 1)).Index)
	assert.Equal(t, 2, cur(t, s, day(2026, 3, 31)).Index)
	noCycle(t, s, day(2026, 4, 1))
}

// ---- Key ----

func TestCycleKeyIsISODateOfCycleStart(t *testing.T) {
	t.Parallel()
	s := sch(t, map[string]any{"start_date": "2026-05-01"})
	assert.Equal(t, "2026-07-01", schedule.Key(cur(t, s, day(2026, 7, 10))))
}

// ---- Describe ----

func TestDescribeMonthlyBasic(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "Monthly on the 1st", schedule.Describe(sch(t, nil)))
}

func TestDescribeMonthlyInterval(t *testing.T) {
	t.Parallel()
	s := sch(t, map[string]any{"interval": 3, "start_date": "2026-05-15"})
	assert.Equal(t, "Every 3 months on the 15th", schedule.Describe(s))
}

func TestDescribeWeekly(t *testing.T) {
	t.Parallel()
	s := sch(t, map[string]any{"frequency": "WEEKLY", "start_date": "2026-05-04"})
	assert.Equal(t, "Weekly on Monday", schedule.Describe(s))
}

func TestDescribeWeeklyInterval(t *testing.T) {
	t.Parallel()
	s := sch(t, map[string]any{"frequency": "WEEKLY", "interval": 2, "start_date": "2026-05-04"})
	assert.Equal(t, "Every 2 weeks on Monday", schedule.Describe(s))
}

func TestDescribeDaily(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "Daily", schedule.Describe(sch(t, map[string]any{"frequency": "DAILY"})))
}

func TestDescribeYearly(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "Yearly on May 1", schedule.Describe(sch(t, map[string]any{"frequency": "YEARLY"})))
}

func TestDescribeWordingBeyondThePythonTests(t *testing.T) {
	t.Parallel()
	cases := []struct {
		over map[string]any
		want string
	}{
		{map[string]any{"frequency": "DAILY", "interval": 5}, "Every 5 days"},
		{map[string]any{"frequency": "YEARLY", "interval": 2, "start_date": "2026-03-09"}, "Every 2 years on March 9"},
		{map[string]any{"frequency": "WEEKLY", "start_date": "2026-05-10"}, "Weekly on Sunday"},
		{map[string]any{"start_date": "2026-05-02"}, "Monthly on the 2nd"},
		{map[string]any{"start_date": "2026-05-03"}, "Monthly on the 3rd"},
		{map[string]any{"start_date": "2026-05-11"}, "Monthly on the 11th"},
		{map[string]any{"start_date": "2026-05-12"}, "Monthly on the 12th"},
		{map[string]any{"start_date": "2026-05-13"}, "Monthly on the 13th"},
		{map[string]any{"start_date": "2026-05-21"}, "Monthly on the 21st"},
		{map[string]any{"start_date": "2026-05-22"}, "Monthly on the 22nd"},
		{map[string]any{"start_date": "2026-05-23"}, "Monthly on the 23rd"},
		{map[string]any{"start_date": "2026-05-30"}, "Monthly on the 30th"},
	}
	for _, tc := range cases {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, schedule.Describe(sch(t, tc.over)))
		})
	}
	custom := schedule.Schedule{Frequency: "HOURLY", Interval: 1, Start: day(2026, 5, 1)}
	assert.Equal(t, "Custom", schedule.Describe(custom))
}

// ---- NextAfter ----

func TestNextCycleAfterBeforeStartReturnsStart(t *testing.T) {
	t.Parallel()
	c, ok := sch(t, nil).NextAfter(day(2026, 4, 15))
	require.True(t, ok)
	assert.Equal(t, day(2026, 5, 1), c.Start)
	assert.Equal(t, 0, c.Index)
}

func TestNextCycleAfterOnCurrentReturnsNext(t *testing.T) {
	t.Parallel()
	c, ok := sch(t, nil).NextAfter(day(2026, 5, 1))
	require.True(t, ok)
	assert.Equal(t, day(2026, 6, 1), c.Start)
	assert.Equal(t, 1, c.Index)
}

func TestNextCycleAfterReturnsNonePastEndDate(t *testing.T) {
	t.Parallel()
	s := sch(t, map[string]any{"start_date": "2026-01-01", "end_mode": "ON_DATE", "end_date": "2026-03-31"})
	_, ok := s.NextAfter(day(2026, 3, 31))
	assert.False(t, ok)
}

func TestNextCycleAfterReturnsNonePastCountCap(t *testing.T) {
	t.Parallel()
	// Cycle 0 is Jan 1 and cycle 1 is Feb 1; after Feb 1 there are no more.
	s := sch(t, map[string]any{"start_date": "2026-01-01", "end_mode": "AFTER_N", "end_after_cycles": 2})
	_, ok := s.NextAfter(day(2026, 2, 1))
	assert.False(t, ok)
}

// ---- IsPaused ----

func TestPausedFlagRoundtrips(t *testing.T) {
	t.Parallel()
	assert.True(t, schedule.IsPaused(map[string]any{"paused": true}))
	assert.False(t, schedule.IsPaused(map[string]any{"paused": false}))
	assert.False(t, schedule.IsPaused(map[string]any{}))
	assert.False(t, schedule.IsPaused(nil))
}

func TestIsPausedFollowsPythonTruthiness(t *testing.T) {
	t.Parallel()
	assert.False(t, schedule.IsPaused(map[string]any{"paused": nil}))
	assert.False(t, schedule.IsPaused(map[string]any{"paused": 0}))
	assert.False(t, schedule.IsPaused(map[string]any{"paused": ""}))
	assert.False(t, schedule.IsPaused(map[string]any{"paused": json.Number("0.0")}))
	assert.True(t, schedule.IsPaused(map[string]any{"paused": "no"}))
	assert.True(t, schedule.IsPaused(map[string]any{"paused": 1}))
	assert.True(t, schedule.IsPaused(map[string]any{"paused": json.Number("2")}))
}

// ---- IsCycleStart ----

func TestIsCycleStart(t *testing.T) {
	t.Parallel()
	s := sch(t, map[string]any{"start_date": "2026-01-31"})
	assert.True(t, s.IsCycleStart(day(2026, 1, 31)))
	assert.True(t, s.IsCycleStart(day(2026, 2, 28)), "clamped February start")
	assert.True(t, s.IsCycleStart(day(2026, 3, 31)))
	assert.False(t, s.IsCycleStart(day(2026, 2, 27)))
	assert.False(t, s.IsCycleStart(day(2026, 1, 30)), "before the start")
}

func TestIsCycleStartIgnoresTheEndCondition(t *testing.T) {
	t.Parallel()
	s := sch(t, map[string]any{"start_date": "2026-01-01", "end_mode": "AFTER_N", "end_after_cycles": 1})
	assert.True(t, s.IsCycleStart(day(2026, 3, 1)))
	noCycle(t, s, day(2026, 3, 1))
}

func TestDatesUseTheirOwnCalendarDay(t *testing.T) {
	t.Parallel()
	loc := time.FixedZone("UTC+8", 8*3600)
	c := cur(t, sch(t, nil), time.Date(2026, 6, 1, 3, 0, 0, 0, loc)) // 1 June local, 31 May UTC
	assert.Equal(t, day(2026, 6, 1), c.Start)
}
