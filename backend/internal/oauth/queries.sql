-- name: InsertOAuthClient :exec
-- redirect_uris is the registered URIs joined by single spaces (Python's
-- format); registration rejects a URI containing whitespace, so the join
-- splits back losslessly.
INSERT INTO oauth_clients (client_id, client_name, redirect_uris)
VALUES (@client_id, @client_name, @redirect_uris);
