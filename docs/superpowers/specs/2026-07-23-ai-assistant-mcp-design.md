# AI Assistant (MCP Server) — Design Spec

**Date:** 2026-07-23
**Status:** Approved (design) — pending implementation plan
**Author:** brainstormed with Claude Code

## Summary

Add a natural-language AI assistant to wenhao-billing so the operator can drive
all modules (invoices, quotations, bills, customers, vendors, items, projects,
reconciliation, accounting/stats) by talking to Claude in plain English.

The assistant is delivered as a **remote MCP server** hosted in the same
deployment as the backend (`billing.wenhao.id`). It runs **in-process** with the
FastAPI app — importing `app.services.*` and sharing the DB session/engine — so
it reuses every existing invariant (`business_id` scoping, double-entry ledger,
currency locking, awaiting-finalization queue, audit log) with no business-logic
duplication. It is reachable at `billing.wenhao.id/mcp` over MCP streamable-HTTP
transport, authenticated via OAuth that delegates to the existing password login.

## Goals

- One assistant covers **all modules** — read and write.
- Reuse existing service-layer functions; add **no** new business logic in the
  MCP layer.
- Autonomous operation for everything internal and reversible.
- Hard confirmation gate only for the two irreversible/outward-facing actions:
  **sending customer emails** and **hard deletes**.
- Internet-exposed endpoint secured by OAuth tied to the existing admin login.

## Non-Goals

- No in-app (Vue) chat panel in this iteration. (Shared-core option deferred;
  the tool layer is written as a plain module so an in-app endpoint could mount
  it later without rework.)
- No second identity/user system — reuse `User` + password auth from `auth.py`.
- No auto currency conversion, no amount guessing, no bypass of safe-stop
  reconciliation (per `prd.md`).

## Architecture

```
Claude Desktop ──OAuth──▶ billing.wenhao.id/mcp ──in-process──▶ app.services.* ──▶ ledger / DB
                                    │
                              (same FastAPI app, new mounted sub-app)
```

- **In-process, not HTTP client:** tools call `app.services.*` directly against a
  shared `Session`. The AI physically cannot bypass `business_id` scoping or post
  an unbalanced ledger entry — safety lives in trusted existing code, not tool
  prompts.
- **Transport:** MCP streamable-HTTP, mounted as a sub-app on the existing
  FastAPI instance (or a sibling `mcp` container sharing the DB — final call in
  planning). Reverse proxy routes `/mcp` and `/oauth/*`. Existing `/api/v1`
  routers are untouched.

## Tool Catalog

One tool per operation, grouped by risk. Writes map to existing service
functions in `app/services/` and existing router logic.

### Read (unrestricted, no confirmation)
- `list_invoices`, `get_invoice`
- `list_customers`, `get_customer`
- `list_bills`, `get_bill`
- `list_quotations`, `get_quotation`
- `list_projects`, `get_project`
- `list_items`
- `list_vendors`
- `get_reconciliation_queue`
- `get_stats` (dashboard/report figures)
- `query_ledger`
- `get_business_profile`

### Write — autonomous (no confirmation)
- `create_invoice`, `update_invoice`, `issue_invoice`
- `create_quotation`, `update_quotation`
- `create_bill`, `update_bill`
- `create_customer`, `update_customer`
- `create_item`, `update_item`
- `create_vendor`, `update_vendor`
- `create_project`, `update_project`
- `generate_recurring_run`
- `resolve_reconciliation_match` (manual-review resolution within safe-stop rules)

### Write — gated (confirmation token required)
- `send_invoice_email`, `send_payment_reminder` (outward-facing)
- `delete_invoice`, `delete_bill`, `delete_customer`, `delete_*` (hard delete)

> Exact final list of write tools is finalized in the implementation plan against
> the real service signatures; the catalog above is the intended coverage.

## Safety / Confirmation Model

- **Autonomous by default.** Any internal, reversible action executes immediately.
- **Two gated actions** use a stateless confirm-token protocol:
  1. Tool call #1 returns
     `{action, resolved_target, amount, currency, requires_confirmation: true, confirm_token}`
     and performs **no** side effect.
  2. The AI shows the operator that exact summary.
  3. Tool call #2 echoes the `confirm_token`; only then does the action execute.
- **confirm_token** is a signed, short-TTL hash of the *exact* payload (target id,
  amount, recipient). It cannot be replayed, cannot be applied to a different
  invoice than the one previewed, and expires quickly.
- **Ambiguity stops.** If a reference (e.g. "Acme") matches multiple records, the
  tool returns the candidate list and takes no action — never guesses. Mirrors the
  PRD safe-stop / `PENDING_MANUAL_REVIEW` rule.
- **Every write is audited** through the existing ledger + audit log, so a
  reversible trail exists regardless of autonomy level.
- **Currency is never auto-converted.** Mismatches are surfaced, not resolved.

## Authentication (OAuth via existing login)

MCP clients expect OAuth 2.1. Add a thin OAuth layer in front of the `/mcp` mount
that **delegates to the existing password login**:

- Client connects to `/mcp` → redirected to `billing.wenhao.id/oauth/authorize`
  → existing login page → operator signs in with the same admin credentials.
- On success, issue a short-lived **scoped bearer token** to the client, plus a
  refresh token for continuity. Tokens are listable and revocable.
- The MCP session resolves to the existing `admin` `User`; every tool runs as that
  user with existing permissions.
- Reuses `_pwd` (bcrypt) and `User` from `auth.py` — no second identity system.

Security notes:
- The `/mcp` endpoint can create and issue real financial records; OAuth is the
  security boundary and must be enforced before any tool dispatch.
- Store token secrets hashed; support revocation; short access-token TTL.

## Data Flow (create invoice, happy path)

1. Operator: "invoice Acme for 40 hours design at $100/hr, USD."
2. AI calls `list_customers(query="Acme")` → single match → resolves customer id.
3. AI calls `create_invoice(...)` → service creates a **draft / awaiting
   finalization** invoice via existing `invoicing.py` logic, `business_id`-scoped.
4. AI reports the created draft id + totals. No email sent (that would be gated).

## Deployment

- New `mcp` sub-app mounted on the FastAPI instance, or a sibling `mcp` container
  in `docker-compose.yml` sharing the DB (final decision in planning).
- Reverse proxy routes `/mcp` and `/oauth/*`; existing routers unchanged.
- Env/config: OAuth signing secret, token TTLs — added to `app/config.py`
  settings.

## Testing

- **Per-tool unit tests** against a real Postgres (existing `tests/conftest.py`
  transactional-rollback pattern):
  - reads are `business_id`-scoped,
  - writes call the correct service and produce the expected ledger/DB state,
  - gated actions refuse to execute without a valid, matching `confirm_token`,
  - ambiguous inputs return candidates and cause **no** side effect.
- **OAuth flow test:** login → issue token → authorized tool call → revoke → 401.
- **Safe-stop test:** currency mismatch / ambiguous match never auto-resolves.

## Open Questions (resolve in planning)

- Mounted sub-app vs. sibling container for the MCP server.
- Exact final write-tool list against real service signatures.
- OAuth library/implementation choice (dynamic client registration support).
- Token storage table + migration.

## References

- `prd.md` — authoritative product rules (safe-stop, currency, reversibility).
- `backend/app/services/` — invoicing, quoting, billing_ap, reconciliation, ledger.
- `backend/app/api/deps.py`, `backend/app/api/v1/routers/auth.py` — existing auth.
