-- +goose Up
-- Each MCP confirm token that has been used, by hash, so it works once. Rows are pruned after expires_at.
CREATE TABLE mcp_spent_confirm_tokens (token_hash text PRIMARY KEY, expires_at timestamptz NOT NULL);
CREATE INDEX mcp_spent_confirm_tokens_expires_at_idx ON mcp_spent_confirm_tokens (expires_at);

-- +goose Down
DROP TABLE mcp_spent_confirm_tokens;
