-- +goose Up
-- The slot a scheduled job ran in (Go change 8): the primary key makes a
-- second runner a no-op, so a job runs once per slot.
CREATE TABLE scheduled_job_runs (
  job_id text NOT NULL,
  slot timestamptz NOT NULL,
  started_at timestamptz NOT NULL DEFAULT now(),
  finished_at timestamptz,
  error text,
  PRIMARY KEY (job_id, slot)
);

-- +goose Down
DROP TABLE scheduled_job_runs;
