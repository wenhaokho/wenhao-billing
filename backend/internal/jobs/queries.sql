-- name: GetSlot :one
-- The recorded run of a job in a slot, if there is one.
SELECT * FROM scheduled_job_runs
 WHERE job_id = sqlc.arg(job_id) AND slot = sqlc.arg(slot);

-- name: InsertSlot :exec
-- The slot row of a run. The primary key makes a second runner of the same
-- slot fail: that is how a slot runs only once.
INSERT INTO scheduled_job_runs (job_id, slot, started_at)
VALUES (sqlc.arg(job_id), sqlc.arg(slot), sqlc.arg(started_at));

-- name: FinishSlot :exec
-- The end of a run: the time it finished, or the error it failed with.
UPDATE scheduled_job_runs
   SET finished_at = sqlc.arg(finished_at), error = sqlc.narg(error)
 WHERE job_id = sqlc.arg(job_id) AND slot = sqlc.arg(slot);

-- name: RestartSlot :exec
-- An abandoned slot: the row exists but neither finished_at nor error is
-- set, so the run that wrote it died (a killed process, a panic, a
-- cancelled context). The advisory lock is held, so no live runner owns
-- it: the slot runs again and the row records the new attempt.
UPDATE scheduled_job_runs
   SET started_at = sqlc.arg(started_at), finished_at = NULL, error = NULL
 WHERE job_id = sqlc.arg(job_id) AND slot = sqlc.arg(slot);

-- name: SkipSlot :exec
-- A slot that is too old to run: recorded so it is never considered again,
-- with the reason it was skipped.
INSERT INTO scheduled_job_runs (job_id, slot, started_at, finished_at, error)
VALUES (sqlc.arg(job_id), sqlc.arg(slot), sqlc.arg(started_at), sqlc.arg(started_at), sqlc.arg(error))
ON CONFLICT (job_id, slot) DO NOTHING;
