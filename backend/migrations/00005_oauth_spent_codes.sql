-- +goose Up
-- Each authorization code that has been exchanged, by hash, so a second use is refused. Rows are pruned after expires_at.
CREATE TABLE oauth_spent_codes (code_hash text PRIMARY KEY, expires_at timestamptz NOT NULL);
CREATE INDEX oauth_spent_codes_expires_at_idx ON oauth_spent_codes (expires_at);

-- +goose Down
DROP TABLE oauth_spent_codes;
