# Go backend, plan 10: MCP connector and OAuth server

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> **Refreshed 2026-11-10 against `main` at 6895e8b (plans 7 and 8 merged; plan 9 still building).** Checked present:
> - `payments.Service.ApproveManual(ctx, tx pgx.Tx, paymentID, invoiceID uuid.UUID, actor *uuid.UUID, adj string)` (payments/service.go:219);
> - `matching.IsExactMatch(p Incoming, c Candidate) bool` (matching/engine.go:94);
> - `stats.Dashboard(ctx, db statsdb.DBTX, base string, today, now time.Time) (DashboardCore, error)` (stats/core.go:46). It takes a `DBTX` (pool or tx) rather than `*statsdb.Queries`, per plan 8's ruling R1; Task 8 passes the tool's transaction.
>
> Still missing: plan 9's invoice PDF renderer and Resend sender. Task 10's two email tools wait for plan 9 to merge; Tasks 1–9 do not.
>
> **Running in parallel:** this runs alongside plan 9 in the worktree `D:/Development/wenhao-billing-plan10` (run `./dev` with `COMPOSE_PROJECT_NAME=wenhao-billing`). Plan 9 adds migration `00004_scheduled_job_runs`, so this plan's migrations are numbered from `00005`. Both plans edit `platform/config.go`, `cmd/api/main.go` and the docs, so this branch rebases on `main` after plan 9 merges.

**Goal:** Go serves the OAuth 2.1 server (7 routes), the `/mcp` redirect and the `/mcp/` streamable-HTTP server with all 42 tools. Names, argument schemas, descriptions and result shapes are Python's, so the owner's Claude connector keeps working.

**Architecture:** There are three packages:
- `internal/oauth`: tokens, discovery, registration, authorize/consent and token/revoke, mounted on chi outside Huma at the root.
- `internal/mcp`: the official `github.com/modelcontextprotocol/go-sdk` server in **stateless** streamable-HTTP mode at `/mcp/`, with a bearer verifier, a Host/Origin guard and the confirm tokens. Each tool calls an exported service function of its feature package, in one transaction per call.
- Each feature package exports the service functions its routes already use. Rules stay in one place.

**Tech Stack:** Go 1.27, `github.com/modelcontextprotocol/go-sdk` (pinned, vendored), the gokit `auth` HS256 helpers or `golang-jwt/jwt/v5` (whichever is vendored; HS256 only), chi v5, Huma v2.40, pgx v5, sqlc 1.31.1, Postgres 16.

**Spec:** `docs/superpowers/specs/2026-11-08-go-backend-design.md`: "MCP connector", "Auth", and "Best-practice changes" (single-use codes, the login redirect fix, PKCE, refresh reuse).

**Contract inventory:** `docs/superpowers/plans/2026-11-10-go-backend-10-mcp-inventory.md`:
- §0 storage, token formats and result serialisation;
- §1 the 9 routes;
- §2 the OAuth rules, confirm tokens, the unknown-key check and the **42-tool table** (§2.6);
- §3 163 [IN] and 4 [OUT] tests;
- §4 the Go code to reuse per tool;
- §5 29 oddities.

## Global Constraints

**Python and port notes**
- `backend-python/` is frozen. Every "Go changes these" item gets a port note.

**OAuth errors**
- OAuth errors are `{"error", "error_description"}` with Python's statuses and sentences (inventory §1). Missing or bad form fields are `400 invalid_request` in the same shape, never problem+json (Go change 9).

**Tokens**
- Tokens are HS256 with `MCP_TOKEN_SECRET`, and their claims are exactly inventory §0.3:
  - access: `sub, typ, iat, exp`, lasting 3600 s;
  - refresh: `+jti`, lasting 2592000 s;
  - code: `typ, sub, client_id, redirect_uri, code_challenge, iat, exp`, lasting 120 s.
- Pre-switch Python tokens must verify in Go.

**Confirm tokens**
- The format is `"<expiry>.<hex HMAC-SHA256>"` with `MCP_CONFIRM_SECRET` and a 300 s lifetime.
- The sentences are `malformed confirm_token`, `confirm_token expired — re-request the action` (em dash) and `confirm_token does not match this action/payload`.
- Expiry is checked before the signature.

**Tools**
- Tool names, the **full** Python docstrings as descriptions, argument names, types and defaults, and the `tools/list` order (inventory §1.9) are Python's.
- Results are JSON objects. A list or string result is wrapped as `{"result": …}`.
- Decimals in tool results are strings at the column scale (an allowed difference for write results, noted).
- Ids are UUID strings.
- Unknown `update_*` keys give `unknown field(s): a, b. Valid fields: …`, with the sorted JSON names of the package's update struct. It runs before any lookup.

**The /mcp/ server**
- Every `/mcp/` request needs a valid access token whose user exists and is admin; otherwise 401.
- A Host outside `{hostname, netloc}` of `MCP_PUBLIC_BASE_URL` (plus loopback) is 421.
- `/mcp` with any method → 307 `Location: /mcp/`.

**Today and tests**
- Today and now come from `platform.Clock`.
- Tests run on pgtestdb. **Every test and subtest calls `t.Parallel()` first.**
- Transport tests drive the SDK's own client against `httptest`.
- Every guard is mutated once and watched going red: PKCE, refresh reuse, single-use code, confirm token, admin check, Host guard, CSRF and redirect URI match.

**Commits and branch**
- Commits name their paths and end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- The branch is `feat/go-mcp` from `main`. No push or PR until the final review.

## Go changes these (each a port note)

1. **Single-use authorization codes.** A new `oauth_spent_codes(code_hash, expires_at)` table records each used code. A second use is `invalid_grant` / `authorization code already used`. Rows are pruned after expiry (spec).
2. **The authorize step honours the session's expiry and epoch.** It reads the Go session token (§5.2).
3. **The login bounce works** (spec, the one frontend edit). `return_to` is a path, `/oauth/authorize?…`. `LoginView.vue` follows a `return_to` that starts with `/oauth/` using `window.location.assign`, and ignores anything else. A signed-in user who reaches `/login?return_to=…` is sent there too (§5.3, §2.1).
4. **Confirm tokens are single use and cover the whole message.** A `mcp_spent_confirm_tokens(token_hash, expires_at)` table records them, and `send_invoice_email`'s payload includes `subject` and `message` (§5.4).
5. **Registration validates redirect URIs.** Only `https://…`, or `http://localhost` / `127.0.0.1` / `[::1]` with any port, with no whitespace and no fragment. Otherwise 400 `invalid_redirect_uri` / `redirect_uri must be https or loopback http` (§5.6).
6. **Deny redirects to the client** with `error=access_denied` (and `state`) (§5.7).
7. **PKCE verifier per RFC 7636**: 43–128 characters of `[A-Za-z0-9-._~]`. Otherwise 400 `invalid_grant` / `invalid code_verifier`. A forged non-UUID `sub` is `invalid_grant`, never 500 (§5.8).
8. **Refresh under a row lock**: `UPDATE … SET revoked = true WHERE jti = $1 AND NOT revoked RETURNING`. Two concurrent refreshes give one new pair; the other is reuse (§5.9).
9. **OAuth-shaped 400s** instead of FastAPI 422s (§5.10).
10. **Stateless MCP sessions**, so a restart does not drop clients (§5.13).
11. **Tool failures are `{"error": sentence}` for every argument or domain refusal**: bad id, bad decimal, validation, a known constraint and a ledger/FX refusal. Only an unexpected failure is an `isError` result. Raw driver text never reaches a result (§5.14, §5.15).
12. **Order and limits.** List tools end with the id tie-break, `resolve_*` order by `name, id`, ILIKE escapes `%`/`_`/`\`, and `limit` is clamped to 0..500 (§5.16, §5.17).
13. **`get_business_profile` reads row id 1**, and returns `{}` when it is missing (§5.18).
14. **`resolve_reconciliation_match` refuses a payment that is not PENDING_MANUAL_REVIEW**: `{"error": "payment <id> is <STATUS>, not awaiting review"}`. It inherits plan 7's DRAFT/VOID refusal (§5.23).
15. **No default MCP secrets in production**, and an index on `oauth_refresh_tokens(user_id)` (§5.26, §5.27).

**Kept (port notes):**
- Refresh tokens are not bound to a client, and reuse revokes every token the user has.
- Revoke only knows refresh tokens.
- `resource` is the origin.
- The CSRF token is replaced on each authorize GET.
- `code_challenge_method` defaults to S256.
- `trigger_recurring` emails every user.
- The reminder shows a raw decimal, and DRAFT invoices can be reminded.
- `send_invoice_email` marks a DRAFT as SENT.
- The "reversible" wording in the void docstring.
- Write-result scale (allowed difference).

## Review Focus

1. A connector that registered against Python must keep working after the switch: a Python-minted refresh token is accepted by Go. Task 2 `TestPythonMintedTokensVerify`, using goldens minted by the frozen Python.
2. Signing in from the connector's login bounce must land back on the consent page, not the dashboard: Task 4 `TestAuthorizeReturnToIsPath`, and the frontend build plus the browser check in Task 4.
3. A second `send_invoice_email` call with the same confirm token must not send twice: Task 10 `TestConfirmTokenSingleUse`.
4. A demoted or deleted user's live access token must stop working on the next `/mcp/` request: Task 6 `TestVerifierRechecksAdminEachRequest`.
5. `tools/list` must list the 42 tools in Python's order with Python's descriptions and schemas: Task 7 `TestToolsListMatchesPython`, against a golden recorded from the frozen Python server.

---

### Task 1: Config, migrations, no-store paths

**Files:**
- Modify: `backend/internal/platform/config.go`, `config_test.go`, `server.go` (the `noStore` prefixes `/oauth/`, `/mcp`, `/.well-known/`)
- Create: `backend/migrations/000NN_oauth_spent_codes.sql`, `000NN_mcp_spent_confirm_tokens.sql`, `000NN_oauth_refresh_user_idx.sql` (additive, so Python keeps working)

The config gains these variables with Python's names and defaults (inventory §Global facts):
- `MCP_ENABLED`;
- `MCP_CONFIRM_SECRET` and `MCP_TOKEN_SECRET` (at least 16 bytes; dev defaults refused in production);
- `MCP_ACCESS_TOKEN_TTL_SECONDS` and `MCP_REFRESH_TOKEN_TTL_SECONDS`;
- `MCP_PUBLIC_BASE_URL`.

- [ ] **Step 1: Failing tests.** Port the 3 tests of `tests/unit/test_mcp_config.py`. Add:
  - `TestProductionRefusesDefaultMcpSecrets`;
  - `TestNoStoreOnOauthMcpWellKnown`;
  - `TestMigrationsAdditive` (Python's insert paths still work).
- [ ] **Step 2: Implement, mutate, commit.**
  - Mutation: allow the default secret in production. The config test goes red.
  - Commit `feat(go): MCP config, OAuth tables, no-store paths`.

---

### Task 2: Tokens and confirm tokens

**Files:**
- Create: `backend/internal/oauth/tokens.go`, `backend/internal/mcp/confirm.go`, plus tests and `testdata/python_tokens.json`

**Interfaces:**
- Produces:
  - `MintAccess(sub uuid.UUID, now time.Time) string`, `MintRefresh(...) (token, jti string)`, `MintCode(...) string`;
  - `VerifyAccess(tok string, now time.Time) (uuid.UUID, error)`, `VerifyRefresh`, `VerifyCode`, whose error texts follow inventory §0.3 (PyJWT's sentences where tests assert them: `Signature has expired`, `Signature verification failed`);
  - `MakeConfirm(action string, payload map[string]string, now time.Time) string`, `VerifyConfirm(token, action string, payload map[string]string, now time.Time) error`.

- [ ] **Step 1: Goldens.** Mint access, refresh and code tokens and a confirm token with the frozen Python (`docker compose run --rm --no-deps backend python -c …`), using a fixed secret and `iat`, and save them.
- [ ] **Step 2: Failing tests.** Port the 4 token tests of `test_mcp_oauth.py` and the 4 of `test_mcp_confirm.py`. Add:
  - `TestPythonMintedTokensVerify`;
  - `TestConfirmExpiredBeforeSignature`;
  - `TestNonUUIDSubIsTokenError`.
- [ ] **Step 3: Implement, mutate, commit.**
  - Mutation: skip the `typ` check. The refresh-as-access test goes red.
  - Commit `feat(go): OAuth and confirm tokens`.

---

### Task 3: Discovery and registration

**Files:**
- Create: `backend/internal/oauth/routes.go`, `register.go`, `queries.sql`, generated `oauthdb/`
- Modify: `backend/cmd/api/main.go` (mount at the root, only when `MCP_ENABLED`)

The routes are inventory §1.1–§1.3, with Go change 5.

- [ ] **Step 1: Failing tests.** Port the 2 metadata tests. Add:
  - `TestRegisterReturnsPythonShape`;
  - `TestRegisterEmptyRedirectUris400`;
  - `TestRegisterRejectsNonHttpsAndFragments`;
  - `TestRegisterAcceptsLoopbackHttp`;
  - `TestRegisterBadJsonIsOauth400`;
  - `TestMcpDisabledHidesRoutes`.
- [ ] **Step 2: Implement, mutate, commit.**
  - Mutation: drop the URI check. The rejects test goes red.
  - Commit `feat(go): OAuth discovery and client registration`.

---

### Task 4: Authorize, consent, and the login bounce

**Files:**
- Create: `backend/internal/oauth/authorize.go`, `consent.html` (embedded)
- Modify: `frontend/src/views/LoginView.vue`, `frontend/src/router/index.ts`
- Test: `authorize_test.go`

The rules are inventory §1.4–§1.5, with Go changes 2, 3 and 6.
- **The consent page:** it is text-identical to Python's, keeping hidden-input order and attribute order `name` then `value`.
- **CSRF:** a short-lived (10 minute) HttpOnly SameSite=Lax cookie `oauth_csrf` holds a random value. That value is in the form and is compared in constant time. The cookie is cleared on every POST, so it is single use.
- **The session:** it is read through `platform.Sessions`.

- [ ] **Step 1: Failing tests.** Port the 9 authorize/consent tests of `test_mcp_oauth.py`. Add:
  - `TestAuthorizeReturnToIsPath`;
  - `TestAuthorizeExpiredSessionBounces`;
  - `TestDenyRedirectsWithAccessDenied`;
  - `TestAuthorizeValidationErrorsInOrder` (response_type, missing ids, challenge, `plain`);
  - `TestCsrfCookieSingleUse`.
- [ ] **Step 2: Frontend.** In `LoginView.vue`, after login, a `return_to` starting with `/oauth/` goes through `window.location.assign`; otherwise `next` as today. The router's guest guard keeps `return_to` for a signed-in user. Run `docker compose exec -T frontend npm run build`.
- [ ] **Step 3: Implement, mutate, commit.** Mutations, each turning its test red:
  - Skip the CSRF compare (the wrong-CSRF test).
  - Accept any registered-prefix redirect (the unregistered test).

  Commit `feat(go): OAuth authorize and consent; login follows return_to`.

---

### Task 5: Token and revoke

**Files:**
- Create: `backend/internal/oauth/token.go`, `revoke.go`
- Test: `token_test.go`

The rules are inventory §1.6–§1.7, with Go changes 1, 7 and 8.

- [ ] **Step 1: Failing tests.** Port the 4 token-flow tests of `test_mcp_oauth.py`. Add:
  - `TestCodeSingleUse`;
  - `TestCodeReplayConcurrentOnce`;
  - `TestVerifierCharsetAndLength`;
  - `TestRefreshConcurrentOnePair`;
  - `TestUnknownJti400`;
  - `TestUnsupportedGrantType`;
  - `TestRevokeAccessTokenNoop`;
  - `TestRevokeAlways200`;
  - `TestSpentCodesPruned`.
- [ ] **Step 2: Implement, mutate, commit.** Mutations, each turning its test red:
  - Skip PKCE (the wrong-verifier test).
  - Skip the family revoke (the reuse test).
  - Skip the spent-code insert (the single-use test).

  Commit `feat(go): OAuth token and revoke`.

---

### Task 6: The /mcp transport

**Files:**
- Create: `backend/internal/mcp/server.go`, `verifier.go`, `guard.go`, `testdata/python_session.json`
- Modify: `backend/cmd/api/main.go`
- Test: `transport_test.go`

**Interfaces:**
- Produces:
  - `func NewHandler(deps Deps) http.Handler`;
  - `type Deps struct { Pool *pgxpool.Pool; Clock platform.Clock; Cfg platform.Config; Sender email.Sender; Payments payments.Service; … }`;
  - `func principal(ctx) (uuid.UUID, bool)` for tools.

Server name `wenhao-billing`, stateless mode, and the `ping` tool returning `{"result":"pong"}`.

- [ ] **Step 1: Record Python.** Run the frozen Python server with a test token, and save the raw HTTP exchange for these:
  - `initialize`;
  - `tools/list`;
  - `ping`;
  - a list tool;
  - an error result;
  - an argument-validation failure;
  - the 401 with its `WWW-Authenticate` header.

  These pin the shapes inventory §0.4 marks "(FastMCP)".
- [ ] **Step 2: Failing tests.** Port:
  - `test_mcp_requires_auth`;
  - `test_mcp_bare_path_redirects_to_trailing_slash`;
  - the 4 e2e tests (send-invoice deferred to Task 10);
  - the 5 verifier tests of `test_mcp_oauth.py`;
  - `test_tool_session_rolls_back_on_error`.

  Add:
  - `TestVerifierRechecksAdminEachRequest`;
  - `TestWrongHost421`;
  - `TestWwwAuthenticateMatchesPython`;
  - `TestPingShapeMatchesPython`.
- [ ] **Step 3: Implement, mutate, commit.** Mutations, each turning its test red:
  - Skip the admin re-check (its test).
  - Skip the Host guard (the 421 test).

  Commit `feat(go): MCP streamable HTTP server`.

---

### Task 7: Export the service functions the tools need

**Files:**
- Modify: the packages in inventory §4's "gap" column:
  - bills, items, vendors, customers and projects (extract the service functions from their routes);
  - quotations and invoices (export `create*`, `update*`, `finalize`, `void`, `delete`, `createTemplate`, `triggerAndSummarise`);
  - a new `DeleteBill` query (DRAFT only).

  Each list loader gains `NameLike`, the statuses, `customer_id` / `vendor_id` / `invoice_type` and a `Limit`.
- Test: the existing route tests must stay green, unchanged. Add a small direct test per exported function.

- [ ] **Step 1: Export and keep green.** Export without behaviour change. The full suite is green with an unchanged count, plus the new tests.
- [ ] **Step 2: Golden tools list.** Build `TestToolsListMatchesPython` scaffolding: load `python_session.json` `tools/list` and compare names, order, descriptions and input schemas once tools exist. It skips with a clear message until Task 8, and lint allows a skip only with a reason that names the task.
- [ ] **Step 3: Commit.** Commit `refactor(go): export service functions for MCP tools`.

---

### Task 8: The 17 read tools

**Files:**
- Create: `backend/internal/mcp/tools_read.go`, `serialize.go`, `validate.go`
- Test: `tools_read_test.go`

The tools are inventory §2.6 rows 2–18, using the field sets of §0.5:
- `get_*` returns `{}` when the row is not found;
- `invoice_to_dict` appends `expected_cash`;
- `get_stats` calls `stats.Dashboard`;
- `get_reconciliation_queue` uses plan 7's awaiting-review query with a limit.

- [ ] **Step 1: Failing tests.** Port the tests of:
  - `test_mcp_read_customers.py` (7);
  - `test_mcp_read_invoices.py` (9);
  - `test_mcp_read_misc.py` (5);
  - `test_mcp_reads_all.py` (24).

  Add `TestListToolsTieBreakAndEscape` and `TestToolsListMatchesPython`, which now fully runs for the read tools.
- [ ] **Step 2: Implement, mutate, commit.**
  - Mutation: drop the template filter in `list_invoices`. Its test goes red.
  - Commit `feat(go): MCP read tools`.

---

### Task 9: The 19 write tools

**Files:**
- Create: `backend/internal/mcp/tools_write.go`
- Test: `tools_write_test.go`

The tools are inventory §2.6 rows 19–37, with Go changes 11 and 14:
- the unknown-key check runs first;
- `trigger_recurring` notifies after commit through the plan 9 digest sender;
- `resolve_reconciliation_match` checks `matching.IsExactMatch` with Python's two refusal sentences (Python Decimal str through `platform.PyDecimalStr`), then calls `ApproveManual` with the token's subject as actor.

- [ ] **Step 1: Failing tests.** Port the tests of:
  - `test_mcp_write_customers.py` (5);
  - `test_mcp_write_invoices.py` (13);
  - `test_mcp_writes_all.py` (41). Port `test_create_vendor_integrity_error_returns_error` as "a refused write becomes `{"error"}`" using a real constraint, and note it.

  Add:
  - `TestResolveFlaggedPaymentRefused`;
  - `TestResolveLedgerRefusalIsError`;
  - `TestTriggerRecurringNotifiesOnceAfterCommit`;
  - `TestBadUuidIsErrorResult`.
- [ ] **Step 2: Implement, mutate, commit.** Mutations, each turning its test red:
  - Run the unknown-key check after the lookup (the precedence test).
  - Skip `IsExactMatch` (the mismatch tests).

  Commit `feat(go): MCP write tools`.

---

### Task 10: The 5 gated tools

**Files:**
- Create: `backend/internal/mcp/tools_gated.go`
- Test: `tools_gated_test.go`

The tools are inventory §2.6 rows 38–42 and the gated flow, with Go change 4:
- the first call previews and writes nothing;
- the second recomputes the payload from the current row, verifies the token, records it as spent, and acts;
- `send_invoice_email` uses plan 9's invoice PDF renderer and `email.Sender`; a send failure is `{"error": "failed to send invoice email"}` and leaves the status alone;
- `delete_bill` uses the new DRAFT-only delete.

- [ ] **Step 1: Failing tests.** Port:
  - the tests of `test_mcp_gated_delete.py` (11) and `test_mcp_gated_email.py` (9);
  - `test_payment_reminder_quotes_expected_cash`;
  - the e2e send-invoice test.

  Add:
  - `TestConfirmTokenSingleUse`;
  - `TestSubjectChangeInvalidatesToken`;
  - `TestReminderStaleAfterPartialPayment`.
- [ ] **Step 2: Implement, mutate, commit.** Mutations, each turning its test red:
  - Skip the spent-token insert (the single-use test).
  - Drop the subject from the payload (its test).

  Commit `feat(go): MCP gated tools`.

---

### Task 11: Inventories, port notes, checks

- [ ] **Step 1: Routes.** Mark the 9 routes `ported`.
- [ ] **Step 2: Tests.** Mark the 163 tests with their Go names and mutations. The 4 [OUT] tests are `not ported`, with their reason (Python fixture plumbing).
- [ ] **Step 3: Port notes.** Write one entry per "Go changes these" item and per kept behaviour. Update PN 9 (the login bounce is fixed).
- [ ] **Step 4: Checks.** Run `bash scripts/check.sh` → `all checks passed`, plus the frontend build.
- [ ] **Step 5: Browser check.** Run the local connector flow in a browser against Go: register, authorize, sign in, return to consent, approve, tool call. Record the result.
- [ ] **Step 6: Commit.** Commit `docs(go): MCP ported; port notes`.
