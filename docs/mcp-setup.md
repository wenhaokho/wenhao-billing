# Connecting an MCP client to the billing backend

The backend exposes a remote MCP server in-process — it rides on the same
uvicorn process as the REST API, no separate container or port. In
production it is reachable at:

```
https://billing.wenhao.id/mcp
```

Locally (via `docker compose up`) it is at `http://localhost:8000/mcp`
(or whatever `BACKEND_HOST_PORT` you've set).

## Connecting Claude Desktop / Claude Code

Add a custom connector pointing at the `/mcp` URL:

1. In Claude Desktop: **Settings → Connectors → Add custom connector**.
2. Enter the server URL: `https://billing.wenhao.id/mcp`.
3. Claude Desktop performs OAuth dynamic client registration
   (`POST /oauth/register`) automatically, then opens a browser to the
   authorization endpoint.

Claude Code (CLI) can be pointed at the same URL via its MCP server
configuration — supply `https://billing.wenhao.id/mcp` as a remote/HTTP MCP
server and let it drive the OAuth flow the same way.

## The OAuth login flow you'll see

The MCP server implements its own OAuth 2.1 authorization server
(authorization-code + PKCE, S256 only) backed by the app's existing admin
login — there is no separate MCP account or password.

1. The MCP client opens `GET /oauth/authorize` in a browser.
2. If you don't already have a valid app session cookie, you're redirected
   to the normal frontend login page (`/login`) with a `return_to` pointing
   back at the authorize request.
3. Log in with your existing admin credentials (same login as the web app).
4. You're redirected back to `/oauth/authorize`, which — now that
   `request.session["user_id"]` is set — issues an authorization code and
   redirects to the MCP client's registered redirect URI.
5. The client exchanges the code at `POST /oauth/token` for an access token
   (short-lived, default 1 hour) and a refresh token (default 30 days).

There is no separate consent screen: being logged in to the app *is* the
consent, since the MCP server only ever acts as the logged-in admin.

## Discovery endpoints

Clients find the above automatically via the standard well-known documents:

- `GET /.well-known/oauth-authorization-server`
- `GET /.well-known/oauth-protected-resource`

Both are served from the same origin as `/mcp`, using `MCP_PUBLIC_BASE_URL`
to build absolute URLs — this **must** be set to
`https://billing.wenhao.id` in production or the metadata will advertise
the wrong endpoints.

## Revoking access

Refresh tokens are persisted (as their `jti`) in the `oauth_refresh_tokens`
table so they can be revoked independently of the access token's lifetime:

- **Client-initiated:** `POST /oauth/revoke` with the token (per RFC 7009).
  Most MCP clients call this automatically when you remove/disconnect the
  connector.
- **Operator-initiated (e.g. lost device, suspected compromise):** mark the
  corresponding row(s) in `oauth_refresh_tokens` as `revoked = true`
  (filtered by `user_id`), or revoke every live refresh token for that user
  at once. Rotation also self-heals: replaying an already-rotated-out
  refresh token revokes the entire family for that user (reuse detection),
  which is a fast way to kill all sessions for an account if needed.
- Access tokens are stateless JWTs, so a revoked refresh token stops future
  renewals but any already-issued access token remains valid until it
  naturally expires (`MCP_ACCESS_TOKEN_TTL_SECONDS`, default 1 hour). For
  faster containment, disable the account/session or restart the backend
  with a rotated `MCP_TOKEN_SECRET` (invalidates all outstanding tokens,
  including everyone else's — use only as a last resort).

## Reverse proxy requirements

The MCP transport (`/mcp`) uses streamable-HTTP, which keeps the response
stream open — the reverse proxy in front of `billing.wenhao.id` must:

- Forward `/mcp`, `/oauth/*`, and `/.well-known/oauth-*` to the backend
  service (same origin as the rest of the API, no path rewriting needed).
- **Disable proxy buffering for `/mcp`** so streamed responses aren't
  buffered and delayed/truncated. For nginx, that's
  `proxy_buffering off;` (plus `proxy_read_timeout` raised enough to cover
  long-lived MCP sessions) scoped to the `/mcp` location block.
- Preserve the `Authorization` header on proxied requests (default nginx
  behavior; don't strip it).

## Configuration reference

Set in `.env` (see `.env.example`) and picked up by the `backend` service
in `docker-compose.yml`. These vars are also mirrored onto the `worker`
service for config consistency (`docker-compose.yml`); MCP token/OAuth
validation only happens in the backend process — the worker does not
currently read them.

| Variable | Purpose |
| --- | --- |
| `MCP_ENABLED` | Mount `/mcp` + OAuth routes at all. `false` disables both. |
| `MCP_CONFIRM_SECRET` | HMAC secret for stateless confirm-tokens (destructive-action gating). |
| `MCP_TOKEN_SECRET` | Signing secret for OAuth access/refresh JWTs. |
| `MCP_PUBLIC_BASE_URL` | Public origin used in OAuth discovery/URLs — `https://billing.wenhao.id` in production. |
| `MCP_ACCESS_TOKEN_TTL_SECONDS` | Access token lifetime (default 3600). |
| `MCP_REFRESH_TOKEN_TTL_SECONDS` | Refresh token lifetime (default 2592000 / 30 days). |

Generate strong random values for the two secrets before deploying, e.g.:

```
openssl rand -hex 32
```

Never reuse the `dev-mcp-*-secret-change-me` defaults outside local
development.
