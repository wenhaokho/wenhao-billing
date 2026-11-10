-- name: InsertOAuthClient :exec
-- redirect_uris is the registered URIs joined by single spaces (Python's
-- format); registration rejects a URI containing whitespace, so the join
-- splits back losslessly.
INSERT INTO oauth_clients (client_id, client_name, redirect_uris)
VALUES (@client_id, @client_name, @redirect_uris);

-- name: GetOAuthClient :one
-- The client an authorize request names: its label and registered URIs.
SELECT client_name, redirect_uris FROM oauth_clients WHERE client_id = @client_id;

-- name: PruneSpentCodes :exec
-- Spent codes past their expiry can no longer verify, so their rows go;
-- cutoff is now less a grace period for clock skew.
DELETE FROM oauth_spent_codes WHERE expires_at < @cutoff;

-- name: SpendCode :one
-- Records an exchanged code. No row back means the code was already spent:
-- a concurrent exchange of the same code waits on the primary key and then
-- conflicts, so exactly one wins.
INSERT INTO oauth_spent_codes (code_hash, expires_at) VALUES (@code_hash, @expires_at)
ON CONFLICT (code_hash) DO NOTHING
RETURNING code_hash;

-- name: InsertRefreshToken :exec
-- user_id is the user UUID as text (Python's column).
INSERT INTO oauth_refresh_tokens (jti, user_id, revoked) VALUES (@jti, @user_id, false);

-- name: RotateRefreshToken :one
-- Revokes a live refresh token under its row lock. No row back means it is
-- unknown or already revoked: of two concurrent refreshes, the second waits
-- for the first to commit and then finds it revoked.
UPDATE oauth_refresh_tokens SET revoked = true WHERE jti = @jti AND NOT revoked
RETURNING user_id;

-- name: RefreshTokenExists :one
SELECT EXISTS (SELECT 1 FROM oauth_refresh_tokens WHERE jti = @jti);

-- name: RevokeUserRefreshTokens :exec
-- Reuse detection: every live refresh token of the user, across all clients.
UPDATE oauth_refresh_tokens SET revoked = true WHERE user_id = @user_id AND NOT revoked;

-- name: RevokeRefreshToken :exec
UPDATE oauth_refresh_tokens SET revoked = true WHERE jti = @jti AND NOT revoked;

-- name: PruneSpentConfirmTokens :exec
-- MCP confirm tokens past their expiry can no longer verify, so their spent
-- rows go; cutoff is now less a grace period for clock skew.
DELETE FROM mcp_spent_confirm_tokens WHERE expires_at < @cutoff;

-- name: SpendConfirmToken :one
-- Records a confirmed MCP action's token (token_hash is canonical: the hash
-- of the decoded MAC). No row back means it was already spent: a concurrent
-- confirm of the same token waits on the primary key until the first one's
-- transaction ends, then conflicts if it committed.
INSERT INTO mcp_spent_confirm_tokens (token_hash, expires_at) VALUES (@token_hash, @expires_at)
ON CONFLICT (token_hash) DO NOTHING
RETURNING token_hash;
