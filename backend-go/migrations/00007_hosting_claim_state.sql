-- +goose Up
ALTER TABLE hosting_operation_leases ADD COLUMN item_state_fingerprint text;
-- Existing uncertain calls have no trustworthy pre-call state to compare.
UPDATE hosting_operation_leases SET manual_review_required=true;
-- +goose Down
ALTER TABLE hosting_operation_leases DROP COLUMN item_state_fingerprint;
