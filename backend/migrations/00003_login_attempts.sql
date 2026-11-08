-- +goose Up
-- Failed logins, for throttling by account and by client address.
CREATE TABLE login_attempts (id bigserial PRIMARY KEY, email text NOT NULL, client_addr text NOT NULL, failed_at timestamptz NOT NULL DEFAULT now());
CREATE INDEX login_attempts_email_client_addr_failed_at_idx ON login_attempts (email, client_addr, failed_at);
CREATE INDEX login_attempts_client_addr_failed_at_idx ON login_attempts (client_addr, failed_at);

-- +goose Down
DROP TABLE login_attempts;
