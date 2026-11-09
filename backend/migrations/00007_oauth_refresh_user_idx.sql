-- +goose Up
-- Reuse of a refresh token revokes every token of the user; this index serves that update.
CREATE INDEX oauth_refresh_tokens_user_id_idx ON oauth_refresh_tokens (user_id);

-- +goose Down
DROP INDEX oauth_refresh_tokens_user_id_idx;
