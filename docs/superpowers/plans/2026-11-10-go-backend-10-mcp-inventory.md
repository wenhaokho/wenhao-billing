# Plan 10 contract inventory: MCP connector and OAuth server (frozen Python -> Go)

Read-only inventory. All paths are relative to `backend-python/` unless prefixed. Abbreviations:

| Abbr | File |
|---|---|
| MAIN | app/main.py (lifespan, router + mount, no-store middleware) |
| CFG | app/config.py |
| DEPS | app/api/deps.py |
| OA | app/mcp/oauth.py (OAuth 2.1 server) |
| TK | app/mcp/tokens.py (access/refresh JWTs) |
| VF | app/mcp/verifier.py (bearer check on `/mcp/`) |
| SRV | app/mcp/server.py (FastMCP instance, `ping`, transport) |
| CTX | app/mcp/context.py |
| MDB | app/mcp/db.py (`tool_session`) |
| CF | app/mcp/confirm.py (gated confirm tokens) |
| SER | app/mcp/serialize.py |
| VAL | app/mcp/validate.py |
| MOC | app/models/oauth_client.py |
| RB / RC / RCU / RI / RM / RP / RQ / RR | app/mcp/tools/read_bills.py / read_catalog.py / read_customers.py / read_invoices.py / read_misc.py / read_projects.py / read_quotations.py / read_recon.py |
| WB / WC / WCU / WI / WP / WQ / WR | app/mcp/tools/write_bills.py / write_catalog.py / write_customers.py / write_invoices.py / write_projects.py / write_quotations.py / write_recon.py |
| GD / GE | app/mcp/tools/gated_delete.py / gated_email.py |
| TI | app/mcp/tools/__init__.py |
| SINV / SAP / SIT / SVE / SCU / SPR / SQU / SDN / SST / SEM / SREC / SW | app/services/invoicing.py / billing_ap.py / items.py / vendors.py / customers.py / projects.py / quoting.py / draft_notifications.py / stats.py / email.py / reconciliation.py / withholding.py |
| DDL | backend/migrations/00001_baseline.sql |
| PN | backend/PORT-NOTES.md |
| RT | backend/ROUTES.md |
| SPEC | docs/superpowers/specs/2026-11-08-go-backend-design.md |
| FE | frontend/src/ |
| NGX | frontend/nginx.conf.template |

Global facts:
- Everything here exists only when `mcp_enabled` (env `MCP_ENABLED`, default `True`, CFG:70). The OAuth router is included at the root, **not** under `/api/v1` (MAIN:103-106), "so discovery URLs match the public base origin" (MAIN:104).
- MCP settings (CFG:69-78): `mcp_confirm_secret` default `"dev-mcp-confirm-secret-change-me"`, `min_length=16` (CFG:72); `mcp_token_secret` default `"dev-mcp-token-secret-change-me"`, `min_length=16` (CFG:74); `mcp_access_token_ttl_seconds` 3600 (CFG:75); `mcp_refresh_token_ttl_seconds` `60*60*24*30` = 2592000 (CFG:76); `mcp_public_base_url` `"http://localhost:8000"` (CFG:78). `app_base_url` `"http://localhost:5173"` (CFG:63) is the frontend origin used for the login bounce. Coolify sets `MCP_PUBLIC_BASE_URL` and `APP_BASE_URL` to the same public origin and the secrets from `SERVICE_BASE64_64_*` (docker-compose.coolify.yml:37-43). Go `platform.Config` has **no** MCP variables yet (backend/internal/platform/config.go has no `MCP_`).
- `_NoStoreMiddleware` (MAIN:144-175) stamps `Cache-Control: no-store` on every path starting `/api/`, `/oauth/`, `/mcp`, `/.well-known/` (MAIN:154), replacing any existing value; pure ASGI so streaming passes through (MAIN:150-151). Go already has `noStore` for `/api` (TESTS.md: `platform.TestNoStoreOnApiPaths`); its prefix list must gain these four.
- Session cookie: Starlette `SessionMiddleware`, cookie `billing_session` (CFG:31), SameSite=Lax, `https_only=False` (MAIN:64-76). Login writes `session["user_id"]` and `session["exp"]` (app/api/v1/routers/auth.py:49, :59). The OAuth authorize step reads `session["user_id"]` only (OA:129, :350) — **not** `exp` (§5.2). Go replaces the cookie with the HS256 `billing_session` token (SPEC:275-280).
- nginx forwards `/oauth/` and `/.well-known/` to the API (NGX:34-36) and `/mcp` + `/mcp/...` with `proxy_buffering off`, `proxy_cache off`, `proxy_read_timeout 1h` (NGX:43-48); it keeps the original `Host` (NGX:22-25) because the DNS-rebinding guard and the login redirect depend on it.
- uvicorn runs with no `--proxy-headers`/`--forwarded-allow-ips` (backend-python/Dockerfile.prod:64), so `request.url` is built from `Host` and the scheme the API sees (plain `http` behind nginx). This matters for `return_to` (§1.4, §5.3).
- DB: no `business_id` on either OAuth table (DDL:326-342). Tool bodies run in `tool_session()` = new `SessionLocal`, `commit` on success, `rollback` + re-raise on any exception, always `close` (MDB:32-42). Session uses `expire_on_commit=False` (app/db/session.py:11), so values serialised after `flush` are the in-memory Python values (§0.4).
- FastMCP version pinned `fastmcp==3.4.3`, `pyjwt>=2.8,<3` (pyproject.toml:23, :25). Facts below marked **(FastMCP)** describe library behaviour, not repo code; confirm them against a recorded transcript of the live Python server before freezing Go shapes.

---

## 0. Storage and shapes

### 0.1 oauth_clients (DDL:326-331, PK DDL:638-639; model MOC:20-31)
client_id varchar NOT NULL (PK; Python default `uuid4().hex`, 32 lowercase hex, MOC:23-25); client_name varchar NULL; redirect_uris varchar NOT NULL — **space-delimited** list (MOC:27-28, joined with `" "` at OA:311, split with `.split()` at OA:113); created_at timestamptz DEFAULT now() NOT NULL. No other index, no FK, no CHECK, never deleted or pruned.

### 0.2 oauth_refresh_tokens (DDL:337-342, PK DDL:645-646; model MOC:34-42)
jti varchar NOT NULL (PK; `uuid4().hex`, TK:60); user_id varchar NOT NULL (the user UUID as a **string**, no FK, OA:259); revoked boolean NOT NULL (no DB default; Python default False, MOC:39); created_at timestamptz DEFAULT now() NOT NULL. **No index on user_id** (family revoke scans the table, OA:246-249). No client_id column: a refresh token is not bound to the client that obtained it. Rows are never deleted.

### 0.3 Token formats (TK, OA)
All three are PyJWT HS256 JWTs signed with `mcp_token_secret` (TK:18, :25-26; OA:55, :224). Secret read at call time (TK:6-7). Header is PyJWT's default `{"alg":"HS256","typ":"JWT"}`. No `iss`, `aud`, `nbf`, `kid` or `scope` claim in any of them.
| token | claims, in insertion order | lifetime | where |
|---|---|---|---|
| access | `sub` (user UUID str), `typ` `"access"`, `iat`, `exp` | `mcp_access_token_ttl_seconds` (3600) | TK:36-45 |
| refresh | `sub`, `typ` `"refresh"`, `iat`, `exp`, `jti` (32 hex) | `mcp_refresh_token_ttl_seconds` (2592000) | TK:48-62 |
| authorization code | `typ` `"code"`, `sub`, `client_id`, `redirect_uri`, `code_challenge`, `iat`, `exp` | `_CODE_TTL_SECONDS = 120` (OA:58) | OA:206-224 |
Decoding: `jwt.decode(token, secret, algorithms=["HS256"])` (TK:29-33, OA:227-236); any `PyJWTError` becomes `TokenError(str(e))` with PyJWT's sentence (e.g. `"Signature has expired"`, `"Signature verification failed"`, `"Not enough segments"`). PyJWT also rejects a future `iat` (`"The token is not yet valid (iat)"`). Then `typ` is checked: `"not an access token"` (TK:68-69), `"not a refresh token"` (TK:80-81), `"refresh token missing jti"` (TK:82-84), `"not an authorization code"` (OA:234-235). `sub` is parsed with `UUID(...)` (TK:70, :85); a non-UUID `sub` raises ValueError, which is **not** a TokenError (500 on `/oauth/token`, §5).
Codes are stateless: replayable for 120 s (SPEC:333-334 makes them single use in Go).
SPEC:321-323: Go keeps "the same HS256 claims and `MCP_TOKEN_SECRET`", so pre-switch access/refresh tokens should still verify.

### 0.4 Tool result serialisation (SER, FastMCP)
- `to_dict(obj, fields)` = `{f: coerce_value(getattr(obj, f))}` in the field-list order (SER:23-24). `coerce_value`: Decimal -> `str(v)` ("preserve exact money"), date/datetime -> `isoformat()`, UUID -> `str`, everything else unchanged (bool, int, None, list, dict from jsonb) (SER:10-20).
- Decimal scale: read tools load from the DB, so money prints with the column scale (`"1000.0000"`, numeric(5,2) rates `"2.50"`). Write tools serialise after `db.flush()` without refresh (e.g. WI:93-94), so values the service computed in Python print with Python's scale (whatever the arithmetic produced), while untouched loaded columns print with DB scale. Go's fixed-scale `platform.Dec4/Dec2` will differ for write results (allowed difference, record a port note as plan 7 did for intake).
- **(FastMCP)** A tool returning `dict` is sent as `structuredContent` = that object plus a `TextContent` holding its JSON; a tool returning `list[dict]` or `str` is wrapped as `{"result": ...}` in `structuredContent` (FastMCP's wrap-result output schema); the e2e test reads them through `Client.call_tool(...).data`, which unwraps (`ping.data == "pong"`, `invoices.data` is the list; tests/integration/test_mcp_e2e.py:145-150). The Go SDK's typed `AddTool` needs an object output; wrap lists and `ping` as `{"result": ...}` to keep the shape.
- **(FastMCP)** A Python exception escaping a tool becomes a `CallToolResult` with `isError: true` and text `"Error calling tool '<name>': <message>"` (`mask_error_details` is not set, SRV:16). Argument-schema failures are also `isError` results. Neither is a JSON-RPC error. Which tools return `{"error": ...}` (a normal, non-error result) and which raise is per tool (§2.6).

### 0.5 Field sets reused by many tools
- `_BILL_FIELDS` (RB:29-41): bill_id, vendor_id, project_id, bill_number, po_number, currency, amount, balance_due, status, issue_date, due_date. `_BILL_DETAIL_FIELDS` (RB:43-50) adds subtotal, discount_type, discount_value, payment_terms, notes.
- `_ITEM_FIELDS` (RC:47-57): item_id, sku, name, item_type, default_currency, default_unit_price, default_purchase_price, active, is_hosting. `_ITEM_DETAIL_FIELDS` (RC:59-78): item_id, sku, name, item_type, description, default_currency, default_unit_price, default_purchase_price, revenue_account_id, expense_account_id, is_sold, is_purchased, active, is_hosting, hosting_domain, hosting_grace_days, hosting_suspension_enabled, hosting_status.
- `_VENDOR_FIELDS` (RC:80-88): vendor_id, name, contact_email, contact_name, default_currency, payment_terms_days, active. `_VENDOR_DETAIL_FIELDS` (RC:90-100) adds tax_id, notes (before active).
- `_CUSTOMER_FIELDS` (RCU:25): customer_id, name, contact_email. `_CUSTOMER_DETAIL_FIELDS` (RCU:27-64): customer_id, name, active, matching_aliases, contact_first_name, contact_last_name, contact_email, contact_phone, contact_phone_2, account_number, website, notes, default_currency, default_wht_type, default_wht_rate, default_wht_gross_up, billing_address (legacy, read only), billing_address1, billing_address2, billing_city, billing_state, billing_postal_code, billing_country, ship_to_name, shipping_address1, shipping_address2, shipping_city, shipping_state, shipping_postal_code, shipping_country, shipping_phone, shipping_delivery_instructions.
- `_INVOICE_FIELDS` (RI:33-48): invoice_id, invoice_number, customer_id, invoice_type, currency, amount, balance_due, status, issue_date, due_date, wht_type, wht_rate, wht_gross_up, wht_amount. `_INVOICE_DETAIL_FIELDS` (RI:50-61) adds project_id, po_so_number, subtotal, discount_type, discount_value, payment_terms, notes, footer, is_template. **Every** invoice result goes through `invoice_to_dict`, which appends `expected_cash = coerce_value(withholding.expected_cash(inv))` as the last key (RI:64-69).
- `_PROJECT_FIELDS` (RP:27-38): project_id, customer_id, code, name, currency, contract_value, status, start_date, end_date, active. `_PROJECT_DETAIL_FIELDS` adds notes (RP:40).
- `_QUOTATION_FIELDS` (RQ:30-40): quotation_id, quotation_number, customer_id, project_id, currency, amount, status, issue_date, valid_until. `_QUOTATION_DETAIL_FIELDS` (RQ:42-51) adds po_so_number, subtotal, discount_type, discount_value, payment_terms, notes, footer.
- `_PAYMENT_FIELDS` (RR:25-37): payment_id, invoice_id, customer_id, amount, currency, payer_name, payer_reference, payment_date, intake_source, status, confidence_score (no wht_amount, external_ref, adjustment_type, created_at).
- `_PROFILE_FIELDS` (RM:17-30): id, name, address, contact_email, contact_phone, invoice_title, invoice_summary, logo_url, default_notes, npwp, npwp_name, updated_at.

---

## 1. Routes (9 rows in RT:119-136: 7 OAuth/discovery, the `/mcp` redirect, the `/mcp/` mount)

All OAuth error bodies use `_oauth_error(error, description, status_code=400)` -> `JSONResponse({"error": error, "error_description": description})` (OA:70-74), the RFC 6749 shape, **not** problem+json and not FastAPI's `{"detail"}`. SPEC:319-321 keeps it. **But** FastAPI validation still runs first on the declared parameters: a missing required `Form(...)` field or a bad JSON body is FastAPI's default **422 `{"detail": [...]}`** (OA:274-276, :386-393, :443, :502).

### 1.1 GET /.well-known/oauth-authorization-server -> 200 (OA:282-295)
`base = mcp_public_base_url.rstrip("/")` (OA:66-67). Body, keys in order:
`issuer` base; `authorization_endpoint` `{base}/oauth/authorize`; `token_endpoint` `{base}/oauth/token`; `registration_endpoint` `{base}/oauth/register`; `revocation_endpoint` `{base}/oauth/revoke`; `response_types_supported` `["code"]`; `grant_types_supported` `["authorization_code", "refresh_token"]`; `code_challenge_methods_supported` `["S256"]`; `token_endpoint_auth_methods_supported` `["none"]`. No `scopes_supported`, no `jwks_uri`, no `revocation_endpoint_auth_methods_supported`.

### 1.2 GET /.well-known/oauth-protected-resource -> 200 (OA:297-303)
`{"resource": base, "authorization_servers": [base]}`. `resource` is the origin, not `{base}/mcp` (§5.12). No `bearer_methods_supported`/`scopes_supported`.

### 1.3 POST /oauth/register -> 201 (OA:305-323) — RFC 7591, no auth, JSON body
Request `RegisterRequest` (OA:274-276): `redirect_uris: list[str]` **required**, `client_name: str|null = None`. Pydantic default `extra="ignore"`: `grant_types`, `token_endpoint_auth_method`, `scope`, etc. in the request are dropped silently. Missing `redirect_uris` or a non-list -> FastAPI 422.
1. Empty list -> 400 `{"error":"invalid_redirect_uri","error_description":"redirect_uris is required"}` (OA:307-308).
2. INSERT oauth_clients (client_name, `" ".join(redirect_uris)`), commit, refresh (OA:309-315). **No URI validation**: any string (non-https, `javascript:`, relative, containing a space, containing a fragment) is accepted; a URI with a space splits into two on read (§5.6).
3. 201 body, keys in order: `client_id` (32 hex), `client_name` (null if absent), `redirect_uris` (the request list, not re-read), `token_endpoint_auth_method` `"none"`, `grant_types` `["authorization_code", "refresh_token"]`, `response_types` `["code"]` (OA:316-323). No `client_id_issued_at`, no `client_secret`.

### 1.4 GET /oauth/authorize -> 200 HTML | 302 | 4xx JSON (OA:325-380)
Query params read raw from `request.query_params` (OA:331-337): `response_type`, `client_id`, `redirect_uri`, `code_challenge`, `code_challenge_method` (**default `"S256"` when absent**, OA:336), `state`. `scope` and `resource` are ignored.
Checks in order, shared with the consent POST (`_validate_authorize_request`, OA:83-120); every failure is a JSON body, **never a redirect** (OA:94-95):
1. `response_type != "code"` -> 400 `unsupported_response_type` / `"response_type must be 'code'"` (OA:97-98).
2. no `client_id` or no `redirect_uri` -> 400 `invalid_request` / `"client_id and redirect_uri are required"` (OA:99-100).
3. no `code_challenge` -> 400 `invalid_request` / `"code_challenge is required (PKCE)"` (OA:102-103).
4. method != `"S256"` -> 400 `invalid_request` / `"code_challenge_method must be 'S256'"` (OA:104-105).
5. unknown client -> **401** `invalid_client` / `"unknown client_id"` (OA:110-112).
6. `redirect_uri` not exactly one of the registered URIs (string equality after `.split()`) -> 400 `invalid_request` / `"redirect_uri is not registered for this client"` (OA:113-119).
Then:
7. No `session["user_id"]` -> **302** to `{app_base_url.rstrip('/')}/login?` + `urlencode({"return_to": str(request.url)})` (OA:350-359). `str(request.url)` is the **absolute** URL as the API saw it (scheme `http` behind nginx, §Global). Test only asserts 302/307 and no `code=` (tests/integration/test_mcp_oauth.py:112-127).
8. Session user missing or role != `"admin"` (`_admin_session_user`, OA:123-135) -> **403** `access_denied` / `"admin role required"` (OA:363-364). `session["exp"]` is not checked (§5.2).
9. `csrf_token = secrets.token_urlsafe(32)` (43 chars), stored in `session["oauth_authorize_csrf"]` (OA:53, :368-369), overwriting any previous one (two tabs: only the last page's form works).
10. 200 `text/html` consent page (OA:370-380, template OA:144-203). Title `Authorize MCP access`; heading `Authorize access`; text `"{client_label} is requesting access to your wenhao-billing account via the AI assistant (MCP)."` where `client_label = html.escape(client_name or client_id, quote=True)` (OA:158, :188); scope box `"Scope: read and write access to your billing data (customers, invoices, bills, reconciliation) on your behalf."` (OA:189-192); `"Approve only if you initiated this connection."` (OA:193). Form `method="post" action="/oauth/authorize/consent"` (relative path, OA:194) with hidden inputs, in order, `response_type` (always `"code"`, OA:373), `client_id`, `redirect_uri`, `code_challenge`, `code_challenge_method`, `state` (omitted when None), `csrf_token`; values `html.escape(..., quote=True)` (OA:138-141, :159-170). Buttons `name="decision"` `value="approve"` ("Approve") and `value="deny"` ("Deny") (OA:196-199). Tests find the token with `name="csrf_token" value="([^"]+)"` (test_mcp_oauth.py:143-146, test_mcp_e2e.py:73) — keep attribute order `name` then `value`.
No code is minted on GET (OA:327-330, :366-367; test_authorize_get_renders_consent_and_mints_no_code).

### 1.5 POST /oauth/authorize/consent -> 302 | 4xx JSON (OA:382-439)
Form fields (`application/x-www-form-urlencoded`): `response_type`, `client_id`, `redirect_uri`, `code_challenge`, `decision` required (missing -> 422 `{"detail"}`); `code_challenge_method` default `"S256"`; `state`, `csrf_token` optional (OA:386-393).
Order:
1. `_admin_session_user` None -> 403 `access_denied` / `"admin session required"` (OA:397-399).
2. `session.pop("oauth_authorize_csrf")` — **consumed before comparing**, so any POST (even a wrong one) burns it (OA:404). Missing session token, missing posted token, or `hmac.compare_digest` mismatch -> 400 `invalid_request` / `"invalid or missing CSRF token"` (OA:405-410).
3. `decision != "approve"` -> **400 JSON** `access_denied` / `"authorization denied by operator"` (OA:413-414). Not a redirect to the client with `error=access_denied` (§5.7).
4. Re-run `_validate_authorize_request` on the posted values (OA:418-427).
5. Mint code (§0.3) for `session user`, `client_id`, `redirect_uri`, `code_challenge` (OA:429-434); the posted `code_challenge_method` is not stored (only S256 is possible).
6. 302 to `redirect_uri + ("&" if "?" in redirect_uri else "?") + urlencode({"code": code, "state": state})`; `state` only if truthy (empty string dropped) (OA:435-439). A fragment in `redirect_uri` is not handled (the query lands after `#`).
Tests: deny/no-CSRF/wrong-CSRF mint nothing; full flow gives `code` and echoes `state` (test_mcp_oauth.py:177-219, :290-304).

### 1.6 POST /oauth/token -> 200 | 4xx JSON (OA:441-498)
Form fields: `grant_type` required (missing -> 422 `{"detail"}`), optional `code`, `redirect_uri`, `client_id`, `code_verifier`, `refresh_token` (OA:442-450). No client authentication (public clients). No `scope`, no `resource`.
**authorization_code** (OA:451-472):
1. no `code` or no `code_verifier` -> 400 `invalid_request` / `"code and code_verifier are required"`.
2. no `client_id` -> 400 `invalid_request` / `"client_id is required"` (test_token_requires_client_id).
3. decode failure -> 400 `invalid_grant` / `f"invalid authorization code: {e}"` (PyJWT text, e.g. `"invalid authorization code: Signature has expired"`).
4. `redirect_uri != payload["redirect_uri"]` (an omitted redirect_uri is None, so it fails) -> 400 `invalid_grant` / `"redirect_uri mismatch"`.
5. `client_id != payload["client_id"]` -> 400 `invalid_grant` / `"client_id mismatch"`.
6. PKCE: `base64url_nopad(sha256(code_verifier.encode("ascii")))` compared with `hmac.compare_digest` to the stored challenge (OA:77-80); mismatch -> 400 `invalid_grant` / `"PKCE verification failed"` (test_token_rejects_wrong_pkce_verifier). No length (43-128) or charset check on the verifier; a non-ASCII verifier raises UnicodeEncodeError -> **500** (§5.8).
7. The client row is **not** re-read (a client is only checked at authorize). The user is **not** re-checked (a demoted admin with a live code still gets tokens; the bearer check at `/mcp/` then rejects them).
8. `_token_response` (OA:253-266): mint access + refresh, INSERT oauth_refresh_tokens(jti, user_id str, revoked False), commit; 200 `{"access_token", "token_type": "Bearer", "expires_in": mcp_access_token_ttl_seconds, "refresh_token"}` (key order as listed). No `scope`.
**refresh_token** (OA:474-496):
1. no `refresh_token` -> 400 `invalid_request` / `"refresh_token is required"`.
2. `verify_refresh_token` failure -> 400 `invalid_grant` / `f"invalid refresh token: {e}"` (e.g. `"invalid refresh token: not a refresh token"`).
3. no row for `jti` -> 400 `invalid_grant` / `"unknown refresh token"`.
4. row revoked -> **reuse detection**: `UPDATE oauth_refresh_tokens SET revoked=true WHERE user_id = :uid AND revoked = false` + commit (`_revoke_user_family`, OA:239-250), then 400 `invalid_grant` / `"refresh token reuse detected"`. The "family" is **every live refresh token of that user** across all clients (OA:242-244; test_refresh_reuse_revokes_successor_family).
5. else `row.revoked = True`, flush, then `_token_response` (new pair, one commit) (OA:494-496). `client_id` in the form is ignored. No row lock: two concurrent refreshes of the same token can both succeed (§5.9).
**other** grant_type -> 400 `unsupported_grant_type` / `f"unsupported grant_type: {grant_type}"` (OA:498).

### 1.7 POST /oauth/revoke -> 200 `{}` (OA:500-514) — RFC 7009
Form `token` required (missing -> 422). `token_type_hint` and `client_id` ignored. Only refresh tokens are revocable: any token that fails `verify_refresh_token` (including a valid **access** token) -> 200 `{}` with no effect (OA:506-509). Known, unrevoked row -> `revoked = True`, commit (OA:510-513). Always 200 `{}` (JSON object, not empty body).

### 1.8 GET|POST|DELETE /mcp -> 307 Location `/mcp/` (MAIN:113-117)
`include_in_schema=False`. Relative Location `/mcp/`; query string dropped. Test parametrises all three methods (tests/integration/test_mcp_server.py:137-149). The frontend shows the connector URL **without** the slash (`https://billing.wenhao.id/mcp`, FE/views/McpIntegrationView.vue:8-9), so every client hits this redirect first. SPEC:306 keeps it.

### 1.9 MOUNT /mcp/ (MAIN:119; SRV:28-64; VF)
- **(FastMCP)** Streamable HTTP, `mcp.http_app(path="/", transport="streamable-http", allowed_hosts, allowed_origins)` (SRV:59-64), mounted at `/mcp`; its lifespan is chained into the FastAPI lifespan or "/mcp requests hang" (MAIN:32-40, :53-55). Default FastMCP mode is **stateful** (in-memory session id in the `Mcp-Session-Id` header; POST JSON-RPC, GET for the server stream, DELETE to end), responses as SSE unless the client accepts JSON. No setting in the repo changes `stateless_http` or `json_response`.
- DNS-rebinding guard (SRV:28-51): allowed hosts = `{hostname, netloc}` of `mcp_public_base_url` (both `localhost` and `localhost:8000` when a port is present), allowed origins = `[f"{scheme}://{netloc}"]`; computed once at import from cached settings. A Host outside the list (plus FastMCP's built-in 127.0.0.1/localhost/::1 and the ASGI server host) -> **421 "Misdirected Request"** (SRV:32-37; SPEC:308-309). Unit tests: tests/unit/test_mcp_config.py:16-29. **(FastMCP)** the Origin check applies only when an Origin header is present (non-browser clients send none); the status for a bad Origin is not asserted anywhere.
- Auth on **every HTTP request** (VF:54-78): FastMCP extracts `Authorization: Bearer <t>`; `verify_access_token` (signature, expiry, `typ == "access"`) -> on TokenError `None` -> 401; then a fresh `SessionLocal` loads the user and requires `role == "admin"` (`current_tool_user`, CTX:33-37: `ToolAuthError("authenticated principal is not an admin user")`) -> otherwise `None` -> 401 (VF:60-70). So a deleted or demoted user loses access on the next request, without waiting for the 1 h TTL. Success -> `AccessToken(token, client_id="billing-mcp", scopes=[], subject=str(user_id), claims={"user_id": ...})` (VF:24-26, :72-78). `resource_base_url = mcp_public_base_url` (VF:51-52). Tests: no token -> 401 (test_mcp_server.py:126-134), bad token -> 401, non-admin token -> 401 (test_mcp_e2e.py:155-168, :194+); verifier unit tests test_mcp_oauth.py:367-426. The `session["exp"]`/`users.session_epoch` of the browser session play no part.
- **(FastMCP)** The 401 carries a `WWW-Authenticate: Bearer ...` header pointing at protected-resource metadata (derived from `resource_base_url`); its exact text and any extra `.well-known` route FastMCP adds under the mount are not asserted by tests — capture from the live server.
- Tool bodies get the principal via `fastmcp.server.dependencies.get_access_token()`; only `resolve_reconciliation_match` uses it (WR:40-45, actor for the recon log; `None` when called outside a request). `authed_user_id()` (CTX:23-30) is **unused**. No per-tool role check beyond the transport check.
- Server name `"wenhao-billing"` (SRV:16). No instructions, no prompts, no resources. Tool registration order (= `tools/list` order, **(FastMCP)** insertion order): `ping` (SRV:19-22), then modules in TI:2-20 order: gated_delete, gated_email, read_bills, read_catalog, read_customers, read_invoices, read_misc, read_projects, read_quotations, read_recon, write_bills, write_catalog, write_customers, write_invoices, write_projects, write_quotations, write_recon; within a module, source order.
- Go (SPEC:303-316): `github.com/modelcontextprotocol/go-sdk`, streamable HTTP at `/mcp/`, Host/Origin guard -> 421, all 42 tools keep names, argument schemas and result shapes.

---

## 2. Rules

### 2.1 Frontend side of the login bounce (the one frontend edit)
- Authorize sends the browser to `{APP_BASE_URL}/login?return_to=<absolute authorize URL>` (OA:353-359).
- `LoginView.vue` reads only `route.query.next`, default `"/dashboard"`, and calls `router.push(next)` (FE/views/LoginView.vue:53-54) — a client-side route push, so even `next` could not reach a backend path. The router guard redirects anonymous users with `{ name: "login", query: { next: to.fullPath } }` (FE/router/index.ts:277) and sends a signed-in user away from guest-only `/login` to `dashboard` (FE/router/index.ts:275).
- Result today: after login the browser lands on `/dashboard` and the connector never resumes (PN 9, PN:47-49; SPEC:324-329).
- Go fix (SPEC:329-331): after a successful login, a `return_to` that is a **same-origin path under `/oauth/`** is followed with `window.location.assign`; anything else is ignored. Because Python's `return_to` is an absolute URL built from the API's own view of the request (`http://...`, §Global), Go should emit `return_to` as a path (`/oauth/authorize?...`) or build it from `MCP_PUBLIC_BASE_URL`, otherwise the same-origin check rejects it behind the proxy. If the user is already signed in, `/login` is guest-only and bounces to the dashboard before reading `return_to` (FE/router/index.ts:275) — only reachable if the API saw no session while the SPA has one (cookie mismatch), but the frontend edit should handle it or the authorize step must not bounce a signed-in user.
- The consent page itself is server-rendered (OA:144-203); there is no frontend view for it.

### 2.2 OAuth invariants worth testing in Go
- PKCE S256 only; `plain` refused; method defaults to S256 when absent (OA:336, :390).
- Redirect URI exact string match against the registered list; validated at authorize, at consent and again (as part of the code) at token.
- Codes: 120 s, bound to user, client_id, redirect_uri, challenge; Go adds single use (SPEC:333-334: "a new table of spent code ids, pruned after expiry"). The table does not exist in DDL; plan 10 adds a migration.
- Refresh: rotation on every use; replay of a revoked token revokes every live refresh token of the user; unknown jti 400; signature/expiry checked before the DB.
- Revoke: 200 `{}` always.
- Consent CSRF: single use, session-bound. Go's session is a stateless HS256 cookie (SPEC:275-280) with nowhere to store a server-side value; options: a second short-lived HttpOnly cookie holding the CSRF value (double submit) or an HMAC of (session token, client_id, redirect_uri, challenge, expiry) as the form token. The single-use property needs either the cookie being cleared on POST or a stored nonce.
- Admin only at authorize, consent and every `/mcp/` request; the token endpoint does not re-check.

### 2.3 Confirm tokens for gated tools (CF)
- `make_confirm_token(action, payload)`: `expiry = int(time.time()) + 300` (CF:15, :33-34); `sig = hex(HMAC-SHA256(mcp_confirm_secret UTF-8, canonical))`; token `f"{expiry}.{sig}"` (CF:35-36).
- canonical = `json.dumps({"action", "payload", "expiry"}, sort_keys=True, separators=(",", ":")).encode()` (CF:22-24). Python's default `ensure_ascii=True` escapes non-ASCII as `\uXXXX`; Go's `encoding/json` does not and escapes `<>&` instead. Byte equality with Python only matters for a token minted by Python and verified by Go inside 300 s at switch-over, so it can be ignored; Go must just be self-consistent.
- `verify_confirm_token` (CF:39-50): `token.split(".", 1)` + `int(expiry)` failure -> `ConfirmError("malformed confirm_token")`; `now > expiry` -> `"confirm_token expired — re-request the action"` (em dash U+2014); signature mismatch (`hmac.compare_digest`) -> `"confirm_token does not match this action/payload"`. Expired is checked **before** the signature, so a forged token with a past expiry reports "expired".
- Stateless: a token can be used any number of times within 300 s (§5.4). Not bound to the user.
- Payloads: delete tools `{"target_id": <id str>}` (GD:55-56); `send_invoice_email` `{"target_id", "amount": str(invoice.amount), "currency", "recipient"}` (GE:27-33); `send_payment_reminder` the same with `amount = str(expected_cash(invoice))` (GE:36-42). The action string is part of the MAC (`delete_invoice`, `delete_bill`, `delete_customer`, `send_invoice_email`, `send_payment_reminder`, GD:45-47, GE:23-24).
- SPEC:313-316 keeps the format, 300 s and `MCP_CONFIRM_SECRET`.

### 2.4 Unknown-key check for `update_*` (VAL:12-40)
`unknown = sorted(set(changes) - set(model.model_fields))`; if any: `{"error": f"unknown field(s): {', '.join(unknown)}. Valid fields: {', '.join(sorted(model.model_fields))}"}`. Runs **before** the session opens, so it wins over not-found (test_update_bill_unknown_field_check_precedes_not_found). Applies to the seven `update_*` tools with the Pydantic models BillUpdate, ItemUpdate, VendorUpdate, CustomerUpdate, InvoiceUpdate, ProjectUpdate, QuotationUpdate (WB:97, WC:99, :158, WCU:144, WI:110, WP:68, WQ:92). Go needs each model's sorted field-name list (from the plan 3/5/6 `*Update` structs' JSON names) to reproduce the "Valid fields" sentence. SPEC:311-313 keeps it.

### 2.5 Common argument handling
- Ids arrive as strings and are parsed with `UUID(x)` (accepts braces, `urn:uuid:`, no hyphens, upper case); a bad id raises ValueError, which escapes as a FastMCP error result in every tool **except** `create_customer`/`update_customer` (catch ValueError) and `resolve_reconciliation_match` (catches ValueError) (§2.6).
- Money args are strings (`discount_value`, `wht_rate`, `default_*_price`, `contract_value`, `default_wht_rate`); converted with `Decimal(x) if x else None`, so `""` means None and `"abc"` raises `decimal.InvalidOperation` (not a ValueError subclass; escapes as an error result; only `create_customer` catches it, WCU:85-88). Line items: `Decimal(str(ln["quantity"]))`, `Decimal(str(ln["unit_price"]))` (numbers or strings accepted), `item_id` UUID if truthy, `position` default 0; a missing `description`/`quantity`/`unit_price` key raises KeyError (WI:41-54, WB:31-45 also `expense_account_id`, WQ:27-40).
- Dates arrive as strings and are left to Pydantic (`issue_date`, `due_date`, `valid_until`, `start_date`, `end_date`).
- `limit` on list tools: `.limit(min(limit, 500))`; default 200; no lower bound (0 -> `[]`; negative -> Postgres `LIMIT must not be negative` -> error result) (RB:61, RC:107, :117, RCU:71, RI:85, RP:50, RQ:65, RR:48).
- `query` filters: `name ILIKE '%{query}%'`, `%`/`_` not escaped (RC:109, :119, RCU:73). `resolve_*` take the first 10 matches with **no ORDER BY** (RC:134-138, RCU:97-101).
- **(FastMCP)** argument validation is Pydantic in lax mode (e.g. `"5"` accepted for `limit: int`); `list[dict]` and `dict` are open objects.

### 2.6 The 42 tools

Description = the docstring's first sentence, verbatim (FastMCP sends the **whole** docstring as the tool description; Go should copy the full docstrings, which carry usage rules such as the GATED two-step and the safe-stop warning). Inputs: `name: type = default`; no default means required. "R" read, "W" write, "G" gated write. `{"error"}` = returns a normal result `{"error": "<sentence>"}` (FastMCP `isError` false); "raises" = the exception escapes and becomes an `isError` result (§0.4). Every tool also raises on a bad UUID unless stated.

| # | tool (file:lines) | description (first sentence) | inputs | Python calls | output | errors | kind / email |
|---|---|---|---|---|---|---|---|
| 1 | ping (SRV:19-22) | "Health check — returns 'pong'." | none | — | `"pong"` (str; wrapped `{"result":"pong"}`) | none | R |
| 2 | list_bills (RB:53-66) | "List bills with optional status/vendor filters." | `status: list[str]\|None=None`, `vendor_id: str\|None=None`, `limit: int=200` | `select(Bill).order_by(created_at desc).limit(min(limit,500))`, `status IN`, `vendor_id =` (empty list = no filter) | `list[_BILL_FIELDS]` | raises (bad UUID, negative limit) | R |
| 3 | get_bill (RB:69-75) | "Fetch one bill by id, including terms/notes/discount." | `bill_id: str` | `db.get(Bill)` | `_BILL_DETAIL_FIELDS` or `{}` | raises | R |
| 4 | list_items (RC:103-110) | "List items, optionally filtered by a name substring." | `query: str\|None=None`, `limit: int=200` | `select(Item).order_by(name asc)`, `name ILIKE` | `list[_ITEM_FIELDS]` (active and inactive, hosting included) | raises | R |
| 5 | list_vendors (RC:113-120) | "List vendors, optionally filtered by a name substring." | `query: str\|None=None`, `limit: int=200` | `select(Vendor).order_by(name asc)`, `name ILIKE` | `list[_VENDOR_FIELDS]` (inactive included) | raises | R |
| 6 | resolve_vendor (RC:123-145) | "Resolve a vendor name substring to a single vendor id." | `query: str` | `name ILIKE '%q%' LIMIT 10`, no ORDER BY | exactly one -> `{"vendor_id": "<uuid>"}`; else `{"candidates": [{"vendor_id", "name"}, ...]}` (0-10 entries) | none (`""` matches everything) | R |
| 7 | list_customers (RCU:67-74) | "List customers, optionally filtered by a name substring." | `query: str\|None=None`, `limit: int=200` | `select(Customer).order_by(name)` | `list[_CUSTOMER_FIELDS]` (inactive included) | raises | R |
| 8 | get_customer (RCU:77-83) | "Fetch one customer by id, with its full contact/billing/shipping profile." | `customer_id: str` | `db.get(Customer)` | `_CUSTOMER_DETAIL_FIELDS` or `{}` | raises | R |
| 9 | resolve_customer (RCU:86-108) | "Resolve a customer name substring to a single customer id." | `query: str` | as resolve_vendor | `{"customer_id"}` or `{"candidates": [{"customer_id", "name"}]}` | none | R |
| 10 | list_invoices (RI:72-93) | "List non-template invoices with optional status/customer/type filters." | `status: list[str]\|None=None`, `customer_id: str\|None=None`, `invoice_type: str\|None=None`, `limit: int=200` | `is_template = false`, order created_at desc, filters | `list[_INVOICE_FIELDS + expected_cash]` | raises | R |
| 11 | get_invoice (RI:96-102) | "Fetch one invoice by id, including terms/notes/footer/discount." | `invoice_id: str` | `db.get(Invoice)` (templates included) | `_INVOICE_DETAIL_FIELDS + expected_cash` or `{}` | raises | R |
| 12 | get_business_profile (RM:33-50) | "Return the agency's business profile (name, contact, address, etc.)." | none | `db.scalar(select(BusinessProfile))` (first row, **no** `id = 1`) | `_PROFILE_FIELDS` + `has_logo: bool(logo)`; a `data:` logo becomes `f"<inline {mime} image, {len(logo) // 1024} KB omitted>"` with `mime = logo[5:].split(";",1)[0].split(",",1)[0] or "unknown"` (RM:44-49); no row -> `{}` | none | R |
| 13 | get_stats (RM:60-89) | "Return dashboard/summary figures (counts + open AR/quotation totals per currency), mirroring GET /api/v1/stats/dashboard (minus `recent_activity`, which is a reconciliation-log feed, not a summary figure)." | none | `stats.compute_dashboard_stats(db)` (SST:451) | keys in order: awaiting_review_count, draft_count, sent_count, customer_count, auto_cleared_last_30d, open_ar_by_currency `[{currency, amount}]`, open_quotation_count, open_quotation_pipeline `[{currency, amount}]`, mrr_by_currency `[{currency, amount}]`, base_currency, open_ar_base_by_currency `[{currency, base_amount}]`, open_ar_unconverted (RM:73-89); amounts `str(Decimal)` | raises | R |
| 14 | list_projects (RP:43-54) | "List projects, optionally filtered by customer_id." | `customer_id: str\|None=None`, `limit: int=200` | order created_at desc | `list[_PROJECT_FIELDS]` | raises | R |
| 15 | get_project (RP:57-62) | "Fetch one project by id, including notes." | `project_id: str` | `db.get(Project)` | `_PROJECT_DETAIL_FIELDS` or `{}` | raises | R |
| 16 | list_quotations (RQ:54-71) | "List quotations with optional status/customer filters." | `status: list[str]\|None=None`, `customer_id: str\|None=None`, `limit: int=200` | order created_at desc | `list[_QUOTATION_FIELDS]` | raises | R |
| 17 | get_quotation (RQ:74-80) | "Fetch one quotation by id, including terms/notes/footer/discount." | `quotation_id: str` | `db.get(Quotation)` | `_QUOTATION_DETAIL_FIELDS` or `{}` | raises | R |
| 18 | get_reconciliation_queue (RR:40-50) | "List payments awaiting manual review (status == PENDING_MANUAL_REVIEW)." | `limit: int=200` | `status='PENDING_MANUAL_REVIEW' ORDER BY created_at DESC LIMIT min(limit,500)` (plan 7 awaiting-review + limit) | `list[_PAYMENT_FIELDS]`; confidence_score DB scale `"97.50"` | raises (negative limit) | R |
| 19 | create_bill (WB:48-84) | "Create a DRAFT bill." | `vendor_id: str`, `currency: str`, `line_items: list[dict]` (`description`, `quantity`, `unit_price`, `item_id?`, `expense_account_id?`, `position=0`), `project_id`, `bill_number`, `po_number`, `issue_date`, `due_date`, `payment_terms`, `notes`, `discount_type`, `discount_value` all `str\|None=None` | `BillCreate(...)` then `billing_ap.create_bill(db, payload)` (SAP:45), flush | `_BILL_DETAIL_FIELDS` | `{"error"}` only for `BillError` (create never raises it); Pydantic ValidationError, bad UUID, bad Decimal, FK violation (unknown vendor) raise | W |
| 20 | update_bill (WB:87-108) | "Edit a DRAFT/OPEN bill." | `bill_id: str`, `changes: dict` (BillUpdate keys) | VAL check; `db.get(Bill)`; `billing_ap.update_bill(db, bill, BillUpdate(**changes))` (SAP:84) | `_BILL_DETAIL_FIELDS` | `{"error"}`: unknown keys (VAL), `f"bill {bill_id} not found"` (WB:103), `f"bill is {status} and cannot be edited"` (SAP:86); ValidationError raises. `BillUpdate.status` lets the tool open/pay/void a bill (PN:359) | W |
| 21 | create_item (WC:37-87) | "Create a catalog item (non-hosting: item_type SERVICE/USAGE/FIXED_FEE)." | `name: str`, `item_type: str`, `sku`, `description: str\|None=None`, `default_currency: str="IDR"`, `default_unit_price`, `default_purchase_price: str\|None=None`, `revenue_account_id`, `expense_account_id: int\|None=None`, `is_sold: bool=True`, `is_purchased: bool=False`, `active: bool=True` | `ItemCreate(...)` (no hosting fields), `items.create_item` (SIT:44) | `_ITEM_DETAIL_FIELDS` | `{"error": str(e.orig)}` on IntegrityError (psycopg text, e.g. duplicate SKU) (WC:86-87); ValidationError raises | W |
| 22 | update_item (WC:90-107) | "Edit an item." | `item_id: str`, `changes: dict` (ItemUpdate) | VAL; `items.update_item(db, id, ItemUpdate(**changes))` (SIT:58) | `_ITEM_DETAIL_FIELDS` | `{"error"}`: unknown keys, `f"item {item_id} not found"` (SIT:61); IntegrityError (duplicate SKU) **raises** here | W |
| 23 | create_vendor (WC:110-145) | "Create a vendor." | `name: str`, `contact_email`, `contact_name: str\|None=None`, `default_currency: str="IDR"`, `payment_terms_days: int=30`, `tax_id`, `notes: str\|None=None`, `active: bool=True` | `VendorCreate`, `vendors.create_vendor` (SVE:19) | `_VENDOR_DETAIL_FIELDS` | `{"error": str(e.orig)}` on IntegrityError; ValidationError (built **outside** the try, WC:129-138) raises | W |
| 24 | update_vendor (WC:148-166) | "Edit a vendor." | `vendor_id: str`, `changes: dict` (VendorUpdate) | VAL; `vendors.update_vendor` (SVE:26) | `_VENDOR_DETAIL_FIELDS` | `{"error"}`: unknown keys, `f"vendor {vendor_id} not found"` (SVE:29) | W |
| 25 | create_customer (WCU:30-127) | "Create a customer." | `name: str`, `matching_aliases: list[str]\|None=None` (None -> `[]`), and 27 more `str\|None=None` args: contact_first_name, contact_last_name, contact_email, contact_phone, contact_phone_2, account_number, website, notes, default_currency, default_wht_type, default_wht_rate (string), billing_address1, billing_address2, billing_city, billing_state, billing_postal_code, billing_country, ship_to_name, shipping_address1, shipping_address2, shipping_city, shipping_state, shipping_postal_code, shipping_country, shipping_phone, shipping_delivery_instructions; `default_wht_gross_up: bool\|None=None` | `CustomerCreate`, `customers.create_customer` (SCU:49) | `_CUSTOMER_DETAIL_FIELDS` | `{"error": f"invalid default_wht_rate: {default_wht_rate!r}"}` on InvalidOperation (WCU:85-88); `{"error": str(e)}` for **any ValueError**, i.e. Pydantic ValidationError text and withholding normalisation (WCU:125-127). No `contact_name` arg (WCU:79-83). `billing_address` not writable | W |
| 26 | update_customer (WCU:130-154) | "Edit a customer." | `customer_id: str`, `changes: dict` (CustomerUpdate) | VAL; `customers.update_customer` (SCU:58) | `_CUSTOMER_DETAIL_FIELDS` | `{"error"}`: unknown keys, `f"customer {customer_id} not found"` (SCU:61), any ValueError (bad UUID included) | W |
| 27 | create_invoice (WI:57-96) | "Create a DRAFT invoice (lands in Awaiting Finalization)." | `customer_id: str`, `currency: str`, `line_items: list[dict]`, `invoice_type: str="MILESTONE"`, `discount_type`, `discount_value`, `payment_terms`, `notes`, `wht_type`, `wht_rate: str\|None=None`, `wht_gross_up: bool\|None=None` | `InvoiceCreate`, `invoicing.create_invoice` (SINV:228) | `_INVOICE_DETAIL_FIELDS + expected_cash` | `{"error"}` for InvoicingError: `"invoice must have at least one line item"` (SINV:230), wrapped withholding errors (SINV:82); ValidationError raises | W |
| 28 | update_invoice (WI:99-118) | "Edit a DRAFT invoice." | `invoice_id: str`, `changes: dict` (InvoiceUpdate) | VAL; `invoicing.update_invoice` (SINV:283) | same | `{"error"}`: unknown keys, `f"invoice {id} not found"`, `f"only DRAFT invoices can be edited (got {status})"` (SINV:286-288) | W |
| 29 | finalize_invoice (WI:121-134) | "Issue a DRAFT invoice (-> OPEN; becomes SENT only when emailed)." | `invoice_id: str` | `invoicing.finalize_invoice` (SINV:643-663): DRAFT only, not template; balance <= 0 -> PAID with balance 0, else OPEN | same | `{"error"}`: not found, `f"only DRAFT invoices can be finalized (got {status})"`, `"cannot finalize a recurring template"` | W |
| 30 | void_invoice (WI:137-146) | "Void an invoice (reversible state, not a delete)." | `invoice_id: str` | `invoicing.void_invoice` (SINV:691-700): refuses PAID only; status VOID, balance 0 | same | `{"error"}`: not found, `"cannot void a PAID invoice — reverse payments first"` | W (docstring says "reversible", but nothing un-voids) |
| 31 | create_recurring_template (WI:149-194) | "Create a recurring invoice template (is_template=True, DRAFT)." | `customer_id: str`, `currency: str`, `line_items: list[dict]`, `schedule: dict` (frequency, interval, start_date, end_mode, end_date?, end_after_cycles?), `payment_terms: str` (**required**), `project_id`, `po_so_number`, `notes`, `footer`, `discount_type`, `discount_value`, `wht_type`, `wht_rate: str\|None=None`, `wht_gross_up: bool\|None=None` | `RecurringTemplateCreate`, `invoicing.create_recurring_template` (SINV:364) | same | `{"error"}`: `f"invalid schedule: {e}"` (SINV:360), `"recurring template must have at least one line item"` (SINV:366) | W |
| 32 | trigger_recurring (WI:197-222) | "Manually trigger one recurring cycle for a template — idempotent per (template_id, cycle_key)." | `template_id: str`, `cycle_key: str` | `invoicing.find_cycle_invoice` (SINV:477) then `trigger_recurring_cycle` (SINV:501); when no prior cycle invoice: `draft_notifications.draft_summary` + `user_emails` inside the session, then **after commit** `send_draft_notifications(emails, new_drafts)` (WI:211-221, SDN:23-34) | `_INVOICE_DETAIL_FIELDS + expected_cash` of the cycle invoice (existing or new) | `{"error"}`: `f"template invoice {id} not found"`, `f"invoice {id} is not RECURRING"`, the cycle-key sentences (SINV:510-531) | W; **emails**: the recurring-drafts digest to **every** `users.email` (no role/active filter, SDN:38-39), failures logged and swallowed (SDN:27-34) |
| 33 | create_project (WP:25-55) | "Create a project." | `customer_id`, `code`, `name`, `currency: str`, `contract_value: str\|None=None`, `status: str="ACTIVE"`, `start_date`, `end_date`, `notes: str\|None=None` | `ProjectCreate`, `projects.create_project` (SPR:26) | `_PROJECT_DETAIL_FIELDS` | `{"error"}` ProjectError: `f"project code {code!r} already exists"` (SPR:43); ValidationError raises | W |
| 34 | update_project (WP:58-78) | "Edit a project." | `project_id: str`, `changes: dict` (ProjectUpdate) | VAL; `projects.update_project` (SPR:47) | same | `{"error"}`: unknown keys, `f"project {project_id} not found"` (SPR:50), `"project code already exists"` (SPR:62) | W |
| 35 | create_quotation (WQ:43-79) | "Create a DRAFT quotation." | `customer_id`, `currency: str`, `line_items: list[dict]`, `project_id`, `po_so_number`, `issue_date`, `valid_until`, `payment_terms`, `notes`, `footer`, `discount_type`, `discount_value: str\|None=None` | `QuotationCreate`, `quoting.create_quotation` (SQU:110) | `_QUOTATION_DETAIL_FIELDS` | `{"error"}`: `"quotation must have at least one line item"` (SQU:112) | W |
| 36 | update_quotation (WQ:82-100) | "Edit a DRAFT/SENT quotation." | `quotation_id: str`, `changes: dict` (QuotationUpdate) | VAL; `quoting.update_quotation` (SQU:145) | same | `{"error"}`: unknown keys, `f"quotation {id} not found"` (SQU:150), status refusal (SQU:152) | W |
| 37 | resolve_reconciliation_match (WR:48-95) | "Force-resolve a PENDING_MANUAL_REVIEW payment against a chosen invoice." | `payment_id: str`, `invoice_id: str` | load payment, invoice; `matching_engine.is_exact_match(payment, invoice)` guard; then `reconciliation.approve_manual_match(db, pid, iid, actor_user_id=<token subject or None>)` (SREC:167; adjustment_type default) | `_PAYMENT_FIELDS` of the cleared payment | `{"error"}`: `f"payment {payment_id} not found"`, `f"invoice {invoice_id} not found"` (WR:59-63); currency mismatch `f"currency mismatch: payment {pcur} vs invoice {icur} — refusing autonomous resolution, route to manual review"` (WR:70-77); amount mismatch `f"amount mismatch: payment {payment.amount} vs invoice expected cash {withholding.expected_cash(invoice)} — refusing autonomous resolution, route to manual review"` (WR:78-84, Python Decimal str); any ValueError from the service (`already CLEARED`, bad UUID) (WR:94-95). LedgerError / FxRateMissing (RuntimeError) **raise**. Payment status is not checked here: a FLAGGED payment is approvable (service refuses only CLEARED) | W |
| 38 | delete_invoice (GD:66-98) | "Permanently delete a DRAFT invoice." | `invoice_id: str`, `confirm_token: str\|None=None` | preview; then `invoicing.delete_invoice` (SINV:467-472) | preview `{"requires_confirmation": true, "action": "delete_invoice", "invoice_id", "invoice_number", "status", "confirm_token"}`; done `{"deleted": true, "invoice_id"}` | `{"error"}`: `"invoice not found"`, ConfirmError text, `f"only DRAFT invoices can be deleted (got {status})"` (checked only at execute) | G |
| 39 | delete_bill (GD:101-133) | "Permanently delete a DRAFT bill." | `bill_id: str`, `confirm_token: str\|None=None` | preview; then `db.delete(bill)` + flush after a local DRAFT check (GD:59-63) — no service exists | preview `{"requires_confirmation", "action": "delete_bill", "bill_id", "bill_number", "status", "confirm_token"}`; done `{"deleted": true, "bill_id"}` | `{"error"}`: `"bill not found"`, ConfirmError, `f"only DRAFT bills can be deleted (got {status})"` | G |
| 40 | delete_customer (GD:136-165) | "Deactivate a customer (soft delete — sets active=False)." | `customer_id: str`, `confirm_token: str\|None=None` | preview; then `customer.active = False` (never refuses; already inactive is fine) | preview `{"requires_confirmation", "action": "delete_customer", "customer_id", "name", "confirm_token"}`; done `{"deleted": true, "customer_id"}` | `{"error"}`: `"customer not found"`, ConfirmError | G |
| 41 | send_invoice_email (GE:45-100) | "Email an invoice PDF to a customer." | `invoice_id: str`, `to_email`, `subject`, `message`, `confirm_token: str\|None=None` | recipient `to_email or customer.contact_email`; preview; then `BusinessProfile id 1`, `render_invoice_pdf(inv, cust, business)` (app/services/pdf/__init__.py:25), `send_email(to_email=recipient, cc_email=None, subject=subject or f"Invoice {number}" (or "Invoice"), body_text=message or f"Please find attached invoice {number or ''}.", attachments=[(f"invoice-{number or str(id)[:8]}.pdf", pdf, "application/pdf")])`; DRAFT/OPEN -> SENT (GE:86-99) | preview `{"requires_confirmation", "action": "send_invoice_email", "invoice_id", "invoice_number", "recipient", "amount": str(inv.amount), "currency", "confirm_token"}`; done `{"sent": true, "invoice_id", "recipient"}` | `{"error"}`: `"invoice not found"`, `"cannot send a voided invoice"`, `"no recipient: pass to_email or set customer contact_email"`, ConfirmError. send_email failure **raises** (status unchanged by rollback). No CC, no `to_email` validation. `subject`/`message` are **not** in the MAC (§5.4) | G; **email** with PDF |
| 42 | send_payment_reminder (GE:103-155) | "Email a payment reminder for an outstanding invoice." | `invoice_id: str`, `to_email: str\|None=None`, `confirm_token: str\|None=None` | preview; then `send_email(to_email=recipient, cc_email=None, subject=f"Payment reminder: invoice {number}" (or "Payment reminder"), body_text=f"This is a reminder that invoice {number or invoice_id} has an outstanding balance of {expected_cash(inv)} {currency}.")` (GE:144-154) | preview `{..., "action": "send_payment_reminder", ..., "amount": str(expected_cash(inv)), ...}`; done `{"sent": true, "invoice_id", "recipient"}` | `{"error"}`: `"invoice not found"`, `f"cannot remind on a {status} invoice"` (VOID, PAID), no recipient, ConfirmError. DRAFT invoices can be reminded | G; **email** (no PDF; the amount prints as Python Decimal str, e.g. `10000000.0000`, test asserts the substring `outstanding balance of 10000000`) |

Counts: 18 read (incl. `ping`), 19 write, 5 gated = **42** (SPEC:310 "41 in `mcp/tools/` plus `ping`").

Gated flow (GD:4-9, GE:3-7): the first call (no token) does the lookups and refusals, returns the preview and writes nothing; the second call recomputes the payload from the **current** row and verifies the token, so a changed amount/recipient/expected cash fails verification. Domain refusals for deletes happen only at execute time (GD:14-18).

---

## 3. Tests in scope

[IN] = this plan. [OUT] = Python test-harness tests with no Go equivalent (record as "not ported" with reason). All rows below are `todo` in backend/TESTS.md (lines 100-258, 310, 411-417).

tests/integration/test_mcp_oauth.py (TESTS.md:124-147), all [IN]:
- tokens: test_access_token_roundtrip; test_expired_access_token_rejected; test_tampered_token_rejected; test_refresh_token_not_accepted_as_access_token.
- metadata: test_authorization_server_metadata; test_protected_resource_metadata.
- authorize: test_authorize_redirects_to_login_when_anonymous; test_authorize_get_renders_consent_and_mints_no_code; test_consent_post_without_csrf_mints_nothing; test_consent_post_with_wrong_csrf_mints_nothing; test_consent_post_deny_mints_nothing; test_full_consent_flow_mints_code; test_authorize_refuses_non_admin_session; test_authorize_rejects_unknown_client; test_authorize_rejects_unregistered_redirect_uri.
- token: test_register_authorize_token_refresh_revoke; test_token_rejects_wrong_pkce_verifier; test_token_requires_client_id; test_refresh_reuse_revokes_successor_family.
- verifier: test_token_verifier_accepts_valid_access_token_for_admin; test_token_verifier_rejects_non_admin_subject; test_token_verifier_rejects_deleted_subject; test_token_verifier_rejects_bad_token; test_token_verifier_rejects_refresh_token.

tests/integration/test_mcp_server.py (TESTS.md:193-199):
- [OUT] test_tool_session_uses_injected_factory — Python fixture plumbing (`set_tool_session_factory`).
- [OUT] test_mcp_app_importable — import smoke test; Go compiles.
- [OUT] test_tool_session_without_mcp_tool_db_fails_loud — Python fixture guard.
- [IN] test_tool_session_rolls_back_on_error — a tool that fails mid-write leaves nothing.
- [OUT] test_mcp_tool_db_composes_with_client_http_commits — Python SAVEPOINT fixture composition.
- [IN] test_mcp_requires_auth — `/mcp/` without a token is 401.
- [IN] test_mcp_bare_path_redirects_to_trailing_slash — 307 `/mcp/` for GET/POST/DELETE (parametrised, one row).

tests/integration/test_mcp_e2e.py (TESTS.md:100-103), all [IN]: test_e2e_oauth_then_authenticated_tool_calls; test_e2e_bad_token_rejected; test_e2e_send_invoice_email_requires_confirmation_over_transport; test_e2e_non_admin_token_rejected. Go drives them with the SDK's client over `httptest`.

tests/unit/test_mcp_config.py (TESTS.md:411-413), all [IN]: test_mcp_settings_have_secure_defaults_shape (enabled default true, access TTL 3600, secret lengths); test_transport_allowlist_public_https_host; test_transport_allowlist_includes_host_with_port.

tests/unit/test_mcp_confirm.py (TESTS.md:414-417), all [IN]: test_roundtrip_ok; test_rejects_wrong_action; test_rejects_mutated_payload; test_rejects_expired.

tests/integration/test_mcp_gated_delete.py (TESTS.md:104-114), all [IN]: test_delete_invoice_preview_does_not_delete; test_delete_invoice_previews_then_deletes; test_delete_non_draft_invoice_errors; test_delete_invoice_tampered_token_rejected; test_delete_bill_preview_does_not_delete; test_delete_bill_previews_then_deletes; test_delete_non_draft_bill_errors; test_delete_bill_tampered_token_rejected; test_delete_customer_preview_does_not_deactivate; test_delete_customer_previews_then_deactivates; test_delete_customer_tampered_token_rejected.

tests/integration/test_mcp_gated_email.py (TESTS.md:115-123), all [IN] (stub `email.Sender` and PDF renderer from plan 9): test_first_call_returns_preview_and_sends_nothing; test_second_call_with_token_sends; test_tampered_token_refuses; test_draft_invoice_promoted_to_sent_after_send; test_void_invoice_refuses; test_no_recipient_refuses; test_reminder_first_call_returns_preview_and_sends_nothing; test_reminder_second_call_with_token_sends; test_reminder_tampered_token_refuses.

tests/integration/test_record_payment_withholding.py (TESTS.md:310): [IN] test_payment_reminder_quotes_expected_cash (left [OUT] by plan 7 for this plan).

tests/integration/test_mcp_read_customers.py (TESTS.md:148-154), all [IN]: test_resolve_unique_match; test_resolve_ambiguous_returns_candidates; test_resolve_no_match_returns_empty_candidates; test_list_customers_filters_by_name_substring; test_list_customers_no_query_returns_all; test_get_customer_found; test_get_customer_not_found_returns_empty_dict.

tests/integration/test_mcp_read_invoices.py (TESTS.md:155-163), all [IN]: test_list_invoices_excludes_templates; test_list_invoices_filters_by_status; test_list_invoices_filters_by_customer_id; test_list_invoices_filters_by_invoice_type; test_list_invoices_limit_clamped_to_500; test_list_invoices_returns_money_as_decimal_strings; test_get_invoice_found; test_get_invoice_not_found_returns_empty_dict; test_get_and_list_invoices_include_withholding_fields.

tests/integration/test_mcp_read_misc.py (TESTS.md:164-168), all [IN]: test_get_business_profile_returns_dict; test_get_business_profile_omits_inline_logo; test_get_business_profile_keeps_regular_logo_url; test_get_stats_returns_dict_with_expected_keys; test_get_stats_renders_money_as_exact_decimal_strings.

tests/integration/test_mcp_reads_all.py (TESTS.md:169-192), all [IN]: test_list_bills_returns_list; test_list_bills_filters_by_status; test_list_bills_filters_by_vendor_id; test_get_bill_found; test_get_bill_not_found_returns_empty_dict; test_list_quotations_returns_list; test_list_quotations_filters_by_status; test_list_quotations_filters_by_customer_id; test_get_quotation_found; test_get_quotation_not_found_returns_empty_dict; test_list_projects_returns_list; test_list_projects_filters_by_customer_id; test_get_project_found; test_get_project_not_found_returns_empty_dict; test_list_items_returns_list; test_list_items_filters_by_query; test_list_vendors_returns_list; test_list_vendors_filters_by_query; test_resolve_vendor_unique_match; test_resolve_vendor_ambiguous_returns_candidates; test_resolve_vendor_no_match_returns_empty_candidates; test_get_reconciliation_queue_returns_list; test_get_reconciliation_queue_excludes_other_statuses; test_get_reconciliation_queue_respects_limit.

tests/integration/test_mcp_write_customers.py (TESTS.md:200-204), all [IN]: test_customer_default_withholding_roundtrip; test_create_customer_invalid_wht_type_returns_error; test_create_customer_out_of_range_wht_rate_returns_error; test_create_customer_unparseable_wht_rate_returns_error; test_update_customer_unparseable_wht_rate_returns_error.

tests/integration/test_mcp_write_invoices.py (TESTS.md:205-217), all [IN]: test_create_invoice_makes_draft; test_create_invoice_applies_percent_discount; test_finalize_moves_draft_to_open; test_void_invoice_sets_void_and_zeroes_balance; test_update_invoice_edits_draft; test_create_invoice_without_line_items_returns_error; test_finalize_non_draft_returns_error_and_leaves_state_unchanged; test_update_invoice_on_non_draft_returns_error_and_leaves_notes_unchanged; test_create_recurring_template_and_trigger_recurring; test_trigger_recurring_on_non_recurring_template_returns_error; test_mcp_create_invoice_with_pph23_gross_up; test_mcp_create_invoice_invalid_wht_rate_returns_error; test_mcp_update_invoice_sets_withholding.

tests/integration/test_mcp_writes_all.py (TESTS.md:218-258), all [IN]:
- customers: test_create_customer_sets_fields; test_update_customer_edits_fields; test_update_customer_not_found_returns_error; test_create_customer_stores_phone_and_address_in_their_own_columns; test_create_customer_echoes_back_contact_and_address; test_update_customer_rejects_unknown_field_names; test_update_customer_writes_address_fields.
- projects: test_create_project_normalizes_code_and_currency; test_create_project_duplicate_code_returns_error_and_persists_only_one; test_update_project_edits_fields; test_update_project_rejects_unknown_field_names; test_create_project_echoes_notes.
- items/vendors: test_create_item_sets_fields; test_update_item_edits_fields; test_update_item_not_found_returns_error; test_update_item_rejects_unknown_field_names; test_create_item_sets_account_ids; test_create_item_echoes_description_and_flags; test_create_vendor_sets_fields; test_create_vendor_can_set_active; test_create_vendor_echoes_tax_id_and_notes; test_update_vendor_edits_fields; test_update_vendor_rejects_unknown_field_names; test_create_item_duplicate_sku_returns_error; test_create_vendor_integrity_error_returns_error.
- bills: test_create_bill_makes_draft; test_update_bill_edits_open_bill; test_update_bill_not_found_returns_error; test_update_bill_rejects_unknown_field_names; test_update_bill_unknown_field_check_precedes_not_found; test_update_bill_echoes_notes.
- quotations/invoices: test_create_quotation_makes_draft; test_update_quotation_edits_draft; test_create_quotation_without_line_items_returns_error; test_update_quotation_rejects_unknown_field_names; test_update_quotation_echoes_notes; test_update_invoice_rejects_unknown_field_names; test_update_invoice_echoes_notes.
- recon: test_resolve_reconciliation_match_exact_match_clears; test_resolve_reconciliation_match_currency_mismatch_refuses; test_resolve_reconciliation_match_amount_mismatch_refuses.

Counts: 167 rows, **163 [IN]**, 4 [OUT] (all four in test_mcp_server.py). Cross-check: 166 rows match `test_mcp` in TESTS.md plus test_payment_reminder_quotes_expected_cash (line 310); every one is `todo`, none `ported`. Note `test_create_item_duplicate_sku_returns_error` asserts only that `"error"` is present and one row persists (tests/integration/test_mcp_writes_all.py:322-335). `test_create_vendor_integrity_error_returns_error` monkeypatches `vendor_service.create_vendor` to raise an IntegrityError whose `orig` is `"duplicate vendor"` and asserts that text is passed through (:337-351) — vendors have no unique constraint, so Go cannot reach this path without a fake; port it as "a DB error from the create becomes `{"error"}`" with a stubbed store, or mark it not portable (§5.15).
Not covered by any Python test (Go should add): authorize `return_to` value and the frontend `return_to` follow; `unsupported_grant_type`; `response_type`/missing-challenge/`plain` errors; refresh of an unknown jti; revoke of an access token; 421 on a wrong Host; `code` replay (new behaviour); the 307 dropping the query; the reminder on VOID/PAID; `send_payment_reminder` with a stale token after a partial payment; `trigger_recurring` notification emails; `resolve_reconciliation_match` on a FLAGGED payment; LedgerError from `resolve_reconciliation_match`.

---

## 4. Existing Go code each tool can reuse

All write logic in the ported packages is **unexported** (written for the HTTP routes). Plan 10 needs either exported service functions in each package (preferred: one place for the rules, as Python's MCP tools call the same services as the routers) or an MCP adapter file inside each package. Exported functions today are listed with file:line; unexported ones are in parentheses.

| tool | Go to reuse | gap |
|---|---|---|
| ping | — | — |
| list_bills / get_bill | `billsdb.ListBills` (bills/queries.sql:22-29: statuses + vendor_id, no limit), (`bills.get` api.go:165, `bills.list` api.go:178) | export get/list; add a limit; MCP field subset differs from `BillOut` |
| list_items | `itemsdb.ListItems` (items/queries.sql:1-8: active/item_type/is_hosting), `items.Get` service.go:20, (`items.list` service.go:45) | no name ILIKE, no limit |
| list_vendors / resolve_vendor | `vendorsdb.ListVendors` (vendors/queries.sql:1-6) | no name ILIKE/limit; no service layer (handlers in vendors/routes.go:66-149) |
| list_customers / get_customer / resolve_customer | `customers.Get` service.go:19, `customersdb.ListCustomers` (customers/queries.sql:1-6) | no name ILIKE/limit |
| list_invoices / get_invoice | `invoices.Get` store.go:17, `invoices.ListBy` store.go:38 (`ListFilter{ProjectID, ExcludeTemplates}`), `withholding.ExpectedCash` withholding.go:59 | `ListFilter` lacks statuses, customer_id, invoice_type, limit; Python list has no id tie-break |
| get_business_profile | `business.Profile` api.go:44 | Go returns an all-null profile when row 1 is missing; the tool must return `{}`; Python takes the first row of any id |
| get_stats | none | `compute_dashboard_stats` belongs to plan 8 (reports/stats); plan 10 depends on it |
| list_projects / get_project | `projectsdb.ListProjects` (projects/queries.sql:1-9), `GetProject` | no service layer (projects/routes.go:136-176); no limit |
| list_quotations / get_quotation | `quotationsdb.ListQuotations` (quotations/queries.sql:56-66, has `row_limit`), (`quotations.get` api.go:167, `list` api.go:180) | export |
| get_reconciliation_queue | plan 7 awaiting-review query + `payments.PaymentOutFrom` | add a limit variant; field subset |
| create_bill / update_bill | (`bills.createBill` store.go:91, `bills.updateBill` store.go:131) | export; Go `BillUpdate.status` behaviour per PN:359 |
| create_item / update_item | (`items.create` service.go:153, `items.update` service.go:196, `checkHosting` service.go:94) | export; MCP create has no hosting fields |
| create_vendor / update_vendor | `vendorsdb.CreateVendor`/`UpdateVendor` | extract service functions from vendors/routes.go:82, :121 |
| create_customer / update_customer | (`customers.create` service.go:144, `customers.update` service.go:161) | export |
| create_invoice / update_invoice / finalize_invoice / void_invoice | (`invoices.createInvoice` core.go:174, `updateInvoice` core.go:245, `finalizeInvoice` lifecycle.go:31, `voidInvoice` lifecycle.go:73), `invoices.MapWriteError` core.go:127, `invoices.RefusalCode` api.go:39, `invoices.ResolveWHT` wht.go:32 | export; turn `ErrInvoicing` refusals into `{"error"}` sentences |
| create_recurring_template | (`invoices.createTemplate` templates.go:122, `checkTemplate` templates.go:425), `schedule.Parse` schedule.go:62 | export |
| trigger_recurring | `invoices.Trigger` trigger.go:52 (returns created flag), (`triggerAndSummarise` trigger.go:231, `draftSummary` trigger.go:212), `invoices.DraftNotifier` notify.go:24 / `LogNotifier` notify.go:30-53 | export `triggerAndSummarise` or reuse `Trigger` + notifier; notify after commit |
| create_project / update_project | `projectsdb.CreateProject`/`UpdateProject` | extract from projects/routes.go:96, :178 (`mapWrite` :74 for duplicate code) |
| create_quotation / update_quotation | (`quotations.createQuotation` store.go:92, `updateQuotation` store.go:151) | export |
| resolve_reconciliation_match | plan 7: `payments.Service.ApproveManual(ctx, tx, paymentID, invoiceID uuid.UUID, actor *uuid.UUID, adj string)`, `matching.IsExactMatch(p Incoming, c Candidate) bool`, `withholding.ExpectedCash`, `platform.PyDecimalStr` pyformat.go:85 | plan 7 makes ApproveManual refuse DRAFT/VOID invoices (TestApproveDraftOrVoidInvoiceIs400); the tool inherits that (better than Python, note it) |
| delete_invoice | (`invoices.deleteInvoice` lifecycle.go:101) | export |
| delete_bill | none | no delete function or `DELETE FROM bills` query (bills/queries.sql has only `DeleteBillLines`:54); PN:386 says add it when MCP needs it |
| delete_customer | `customersdb.DeactivateCustomer` (customers/queries.sql:58), handler customers/routes.go:132 | extract |
| send_invoice_email | `email.Sender` email.go:18 | PDF renderer and invoice-email wiring belong to plan 9 (documents); plan 10 depends on it |
| send_payment_reminder | `email.Sender`, `withholding.ExpectedCash`, `platform.PyDecimalStr` | — |
| (all) | `platform.PyRepr` pyformat.go:24 (for `{!r}` sentences), `platform.PrincipalFrom` principal.go:46, `platform.Sessions` principal.go:92 (read the cookie in `/oauth/authorize`), `platform.NewServer` server.go:42 (mount `/mcp/`, `/oauth/*`, `/.well-known/*` outside huma) | MCP config vars (`MCP_ENABLED`, `MCP_CONFIRM_SECRET`, `MCP_TOKEN_SECRET`, `MCP_ACCESS_TOKEN_TTL_SECONDS`, `MCP_REFRESH_TOKEN_TTL_SECONDS`, `MCP_PUBLIC_BASE_URL`); spent-codes table + migration; `noStore` prefixes |

Order dependency: plan 8 (stats) and plan 9 (PDF + Resend) must land before `get_stats` and `send_invoice_email` can be ported faithfully; otherwise stub them behind interfaces as plan 7 did for receipts.

Go SDK notes (verify against the pinned version): the SDK's streamable handler takes a per-request server factory; the `auth` subpackage provides bearer middleware that calls a verifier with the raw token and puts the token info on the request context (tool handlers read it from the request's extra data). Python is stateful; nothing here needs server-to-client messages, so stateless mode is the simpler choice and survives restarts (§5.13).

---

## 5. Oddities

1. **Authorization codes replayable for 120 s** (stateless JWT, OA:206-236, :460-472). SPEC:333-334 changes it: spent-code table. Change (decided).
2. **Authorize ignores session expiry.** `_admin_session_user` reads only `session["user_id"]` (OA:129-135); `current_admin` also requires `session["exp"]` (DEPS:25-30). The cookie lives 30 days (MAIN:68-73), so an 8-hour login that has expired can still approve a connector. Change: Go reads the session token, which carries its own expiry and epoch (SPEC:275-283).
3. **Login bounce is broken and `return_to` is absolute.** LoginView reads `next` and `router.push`es it (FE/views/LoginView.vue:53-54); `return_to` is `str(request.url)` as uvicorn sees it behind nginx (OA:354, Dockerfile.prod:64). Change (decided, SPEC:324-331): frontend follows a same-origin `/oauth/` path; Go should emit a path or a URL built from `MCP_PUBLIC_BASE_URL`.
4. **Confirm tokens are reusable for 300 s** (CF:39-50, no store). A second `send_invoice_email` with the same token sends the same email again; `subject` and `message` are not in the MAC (GE:27-33), so they can change between preview and send. Change: record spent confirm tokens (as for codes) and put subject/message in the payload; or keep and note.
5. **Refresh tokens are not bound to a client** (no client_id column, DDL:337-342; `client_id` ignored on refresh, OA:474-496), and reuse detection revokes **all** of the user's refresh tokens across every connector (OA:239-250). Keep (single user); note.
6. **Dynamic registration accepts anything** (OA:305-323): no URI scheme or format check, no auth, no rate limit, rows never pruned; a URI containing a space is split into two registered URIs (OA:311, :113). Change: reject URIs that are not `https` or loopback `http`, or contain whitespace/fragments; keep open registration (MCP clients need it).
7. **Deny is a JSON 400, not a redirect** (OA:413-414). RFC 6749 §4.1.2.1 sends `error=access_denied` (+state) to the client's redirect URI; the connector hangs instead. Change: redirect with `error=access_denied` and `state`.
8. **PKCE verifier not validated** (OA:77-80): any length/charset; non-ASCII raises UnicodeEncodeError -> 500; a non-UUID `sub` in a forged-but-signed token -> ValueError 500 (TK:70, :85). Change: enforce RFC 7636 (43-128 chars of `[A-Za-z0-9-._~]`) -> `invalid_grant`/`invalid_request`.
9. **Refresh race**: no `SELECT ... FOR UPDATE` on the token row (OA:482-496); two concurrent refreshes of one token both succeed. Change: lock the row (or `UPDATE ... SET revoked = true WHERE jti = $1 AND NOT revoked RETURNING`).
10. **FastAPI 422 `{"detail"}` leaks into the OAuth surface** for missing form fields / bad register JSON (OA:274-276, :386-393, :443, :502). Change: `invalid_request` in the OAuth shape (SPEC:319-321 says errors keep the OAuth shape).
11. **Revoke only knows refresh tokens** (OA:506-509): revoking an access token is a silent no-op (stateless, 1 h TTL). Keep; note.
12. **Protected-resource `resource` is the origin**, not the MCP URL (OA:300-303); tokens carry no `aud`, and `resource` parameters are ignored (OA:331-337, :442-450). Keep for compatibility with connected clients; note (RFC 8707/9728 would use `{base}/mcp/`).
13. **Stateful in-memory MCP sessions** (FastMCP default, SRV:59-64): a restart or a second worker drops sessions and clients must re-initialise. Change: Go SDK stateless mode (no server-initiated messages are used).
14. **Inconsistent tool error behaviour.** Some failures are `{"error"}` results, others escape as `isError` (§2.6): bad UUIDs everywhere except the customer and recon tools; Pydantic errors except in `create_customer`/`update_customer`; IntegrityError caught on `create_item`/`create_vendor` but not `update_item`; `resolve_reconciliation_match` raises on LedgerError/FxRateMissing. Keep the split where tests pin it; otherwise make every argument/domain failure `{"error": sentence}`; record as a port note.
15. **Raw psycopg text in tool results** (`str(e.orig)`, WC:86-87, :144-145) leaks constraint names and SQL detail. Change: `{"error": "item sku already exists"}`-style sentences (the HTTP routes already map constraints, `platform.MapConstraint`).
16. **`resolve_*` are unordered and include inactive rows** (RC:134-138, RCU:97-101): 11+ matches show an arbitrary 10; a deactivated customer can be the unique match; `query=""` matches all. Change: `ORDER BY name, id`; keep inactive (list tools include them too), note.
17. **List ordering has no tie-break** (RB:61, RC:107, :117, RCU:71, RI:84, RP:49, RQ:64, RR:47); `ILIKE` wildcards unescaped; negative `limit` is a DB error. Change: add the id tie-break (as Go list queries already do), escape `%`/`_`, clamp limit to `0..500`.
18. **`get_business_profile` reads any first row** (RM:41) while everything else uses id 1 (GE:86). Change: id 1.
19. **`trigger_recurring` emails every user**, not only admins or active users (SDN:38-39), and only when the cycle invoice is new (WI:213-218). Keep (single tenant), note.
20. **Reminder email prints raw Decimal** (`10000000.0000 IDR`, GE:145-148) and can be sent for DRAFT invoices (GE:119 refuses only VOID/PAID). Keep text for parity (test asserts the substring); note.
21. **`send_invoice_email` sends DRAFT invoices and marks them SENT without finalising** (GE:97-98, same as the router app/api/v1/routers/invoices.py:774-775): a zero-value DRAFT becomes SENT instead of PAID; templates can be sent. Keep for parity with the router; note.
22. **`void_invoice` docstring says "reversible"** (WI:139) but nothing un-voids. Keep; fix the wording only if the owner agrees (descriptions are part of the contract).
23. **`resolve_reconciliation_match` accepts FLAGGED payments** (no status check, WR:56-93; service refuses only CLEARED, SREC:185-186). Change: refuse anything not PENDING_MANUAL_REVIEW, matching the docstring.
24. **Write results print computed decimals at Python's scale** (§0.4). Port note: Go prints the column scale.
25. **CSRF token overwritten per authorize GET** (OA:368-369) and consumed on any POST (OA:404); two concurrent authorize tabs: only the last works. Keep; note.
26. **Default secrets accepted in production** (`dev-mcp-*-change-me`, CFG:72, :74; docker-compose.yml:29-30). Change: refuse to start with defaults outside development (as plan 7 suggests for webhook secrets).
27. **No index on `oauth_refresh_tokens.user_id`, no pruning of expired rows or clients** (DDL:337-342). Change: index + prune expired rows with the spent-code pruning job.
28. **`authed_user_id` is dead code** (CTX:23-30). Drop.
29. **`code_challenge_method` defaults to S256 when absent** (OA:336, :390); RFC 7636 says absent means `plain`. Keep (S256 is the only method; a client omitting it still sends an S256 challenge in practice); note.
