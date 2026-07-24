# Resend Email Integration — Design

**Date:** 2026-07-24
**Status:** Approved (pending spec review)

## Goal

Add [Resend](https://resend.com) as a native-HTTP-API email backend, so outgoing
mail (password resets, invoice emails, quotation emails, MCP-triggered sends) can
be delivered through Resend instead of raw SMTP. The native API is preferred over
Resend's SMTP relay because outbound SMTP ports are frequently blocked on managed
hosts (e.g. Coolify), and the API gives delivery tracking.

## Non-goals

- No changes to the four call sites (`auth.py`, `invoices.py`, `quotations.py`,
  `mcp/tools/gated_email.py`). They keep calling `send_email()` unchanged.
- No changes to the MCP gated-email preview/confirm flow.
- No delivery-status webhooks, open/click tracking, or template management. Out of scope.
- SMTP support is **not** removed — Resend is additive.

## Architecture

Keep the single `send_email()` seam in `backend/app/services/email.py`. Add a
provider-selection with explicit precedence inside it:

1. `resend_api_key` set → send via Resend HTTP API (`_send_via_resend()`)
2. else `smtp_host` set → existing SMTP path (unchanged)
3. else → existing logging fallback (unchanged)

Because selection is gated on a new, default-unset key, existing deployments see
**no behavior change** until they set `RESEND_API_KEY`.

## Config additions (`backend/app/config.py`)

| Setting | Type | Default |
|---|---|---|
| `resend_api_key` | `str \| None` | `None` (the on/off switch) |
| `resend_from_email` | `str` | `"noreply@wenhao.id"` |
| `resend_from_name` | `str` | `"Wenhao Dev Billing"` |
| `resend_base_url` | `str` | `"https://api.resend.com"` |
| `resend_timeout_seconds` | `float` | `15.0` |

Reply-To reuses the existing `smtp_reply_to` setting (it is not sender-domain-bound,
so no separate Resend field is warranted).

## New backend: `_send_via_resend()`

Signature mirrors the data `send_email()` already assembles:

```python
def _send_via_resend(
    *,
    to_email: str,
    subject: str,
    body_text: str,
    body_html: str | None,
    cc_email: str | None,
    attachments: list[tuple[str, bytes, str]] | None,
) -> None:
```

Behavior:

- `POST {resend_base_url}/emails` with header `Authorization: Bearer {resend_api_key}`,
  using `httpx.Client(timeout=settings.resend_timeout_seconds)` — matching the
  established sync-client pattern in `services/cloudflare.py` and `services/fx_provider.py`.
- JSON body:
  - `from`: `f"{resend_from_name} <{resend_from_email}>"`
  - `to`: `[to_email]`
  - `subject`, `text` (always), `html` (only if `body_html`)
  - `cc`: `[cc_email]` (only if provided)
  - `reply_to`: `smtp_reply_to` (only if set)
  - `attachments`: `[{"filename": name, "content": base64(bytes)}]` for each tuple.
    Resend infers content type from the filename, so the tuple's mime element is
    unused in the payload.
- On non-2xx: `resp.raise_for_status()`, preserving the raise-on-failure contract of
  the SMTP path. On success: log at info level (message id if present in response).

`send_email()` gains a branch at the top: if `settings.resend_api_key`, delegate to
`_send_via_resend(...)` and return; otherwise fall through to the existing SMTP /
logging logic verbatim.

## Error handling

Unchanged contract: a failed send raises. Callers already propagate this, and the
MCP gated-email flow only depends on `send_email` being invoked, so its tests and
behavior are unaffected.

## Testing

New `backend/tests/unit/test_email_resend.py`, mocking `httpx.Client`:

1. **Provider precedence** — with `resend_api_key` set, `_send_via_resend` is used and
   the SMTP path (`smtplib.SMTP`) is never touched.
2. **Payload shape** — asserts `from`, `to`, `subject`, `text`, `html`, `cc`,
   `reply_to`, and base64-encoded `attachments` are built correctly, including the
   omit-when-absent cases (no html, no cc, no reply_to, no attachments).
3. **Raise on failure** — a non-2xx response raises.
4. **Fallback intact** — with `resend_api_key` unset and `smtp_host` unset, the call
   still logs and returns without error.

Existing `tests/integration/test_mcp_gated_email.py` continues to pass untouched
because it mocks `send_email` at the call site, above this change.

## Docs

Add to `backend/.env.example` and `.env.coolify.example`:

```
# Resend (native API). When RESEND_API_KEY is set it takes precedence over SMTP.
RESEND_API_KEY=
RESEND_FROM_EMAIL=noreply@wenhao.id
RESEND_FROM_NAME=Wenhao Dev Billing
```

With a one-line note that the sender domain must be verified in Resend and that
Resend takes precedence over the `SMTP_*` settings.
