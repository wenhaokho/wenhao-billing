-- +goose Up
-- Bumped to end every session of a user at once (change-password, reset-password, set-password).
ALTER TABLE users ADD COLUMN session_epoch integer NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE users DROP COLUMN session_epoch;
