-- +goose Up
-- Go's own (UI revamp part 2, "FX Rates" last sync): one row per successful
-- provider sync, written by fx.Sync in the transaction that stores the rates.
-- trigger is SCHEDULED for the weekly job and MANUAL for "sync now", whose
-- caller is actor_user_id (null for the job, or once the user is deleted).
CREATE TABLE fx_sync_runs (
  run_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  ran_at timestamptz NOT NULL DEFAULT now(),
  source text NOT NULL,
  trigger text NOT NULL CHECK (trigger IN ('SCHEDULED', 'MANUAL')),
  created integer NOT NULL,
  updated integer NOT NULL,
  actor_user_id uuid REFERENCES users (user_id) ON DELETE SET NULL
);

CREATE INDEX ix_fx_sync_runs_ran_at ON fx_sync_runs (ran_at DESC);

-- +goose Down
DROP TABLE fx_sync_runs;
