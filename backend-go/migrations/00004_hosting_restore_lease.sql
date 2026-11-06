-- +goose Up
ALTER TABLE hosting_restore_intents ADD COLUMN lease_token uuid, ADD COLUMN lease_until timestamptz, ADD COLUMN next_attempt_at timestamptz NOT NULL DEFAULT now(), ADD COLUMN attempts integer NOT NULL DEFAULT 0;
CREATE UNIQUE INDEX hosting_restore_one_lease_per_item ON hosting_restore_intents(item_id) WHERE lease_token IS NOT NULL;
-- +goose Down
DROP INDEX hosting_restore_one_lease_per_item;
ALTER TABLE hosting_restore_intents DROP COLUMN lease_token, DROP COLUMN lease_until, DROP COLUMN next_attempt_at, DROP COLUMN attempts;
