-- +goose Up
ALTER TABLE recurring_draft_events ADD COLUMN scan_batch_id uuid;
CREATE INDEX recurring_draft_events_scan_batch ON recurring_draft_events(scan_batch_id) WHERE scan_batch_id IS NOT NULL;
-- +goose Down
DROP INDEX recurring_draft_events_scan_batch;
ALTER TABLE recurring_draft_events DROP COLUMN scan_batch_id;
