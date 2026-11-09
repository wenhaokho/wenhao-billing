package jobs

import (
	"hash/crc32"
	"time"
)

// The job ids and the UTC slots of the Python crons
// (workers/scheduler.py:31-37). Python runs the jobs with APScheduler's
// CronTrigger in the UTC time zone; Go computes the slot itself, so the two
// agree on which run is which.
const (
	// RecurringScanID scans the recurring templates and generates the
	// drafts of the cycles due today.
	RecurringScanID = "recurring-scan-and-generate"
	// UsageLockScanID locks the usage drafts past their cut-off day.
	UsageLockScanID = "usage-lock-cutoff-scan"
	// HostingEnforcementID suspends and restores the overdue hosting.
	HostingEnforcementID = "hosting-overdue-enforcement"
	// FXRatesSyncID refreshes the provider's FX rates.
	FXRatesSyncID = "fx-rates-weekly-sync"
)

// LockKey is the Postgres advisory lock key of a job: the unsigned crc32 of
// the job id, as Python's zlib.crc32 gives it (scheduler.py:41). Python
// passes the key to pg_try_advisory_lock as a bigint, so Go must pass the
// same value: an int32 cast would give a different key for the ids whose
// crc32 is above 2^31 and the two backends would run the same job at once.
func LockKey(jobID string) int64 {
	return int64(crc32.ChecksumIEEE([]byte(jobID)))
}

// DailySlot is the slot function of a job that runs every day at the given
// UTC hour: the latest time of that hour that has passed. Before the hour of
// the day has come, the slot is the previous day's.
func DailySlot(hour int) func(now time.Time) time.Time {
	return func(now time.Time) time.Time {
		now = now.UTC()
		slot := time.Date(now.Year(), now.Month(), now.Day(), hour, 0, 0, 0, time.UTC)
		if slot.After(now) {
			slot = slot.AddDate(0, 0, -1)
		}
		return slot
	}
}

// WeeklySlot is the slot function of a job that runs once a week, on the
// given weekday at the given UTC hour: the latest such time that has
// passed.
func WeeklySlot(weekday time.Weekday, hour int) func(now time.Time) time.Time {
	return func(now time.Time) time.Time {
		now = now.UTC()
		// The Monday of now's week, then the wanted weekday of that week.
		monday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		monday = monday.AddDate(0, 0, -int(monday.Weekday())+int(time.Monday))
		slot := monday.AddDate(0, 0, int(weekday)-int(time.Monday)).Add(time.Duration(hour) * time.Hour)
		if slot.After(now) {
			slot = slot.AddDate(0, 0, -7)
		}
		return slot
	}
}
