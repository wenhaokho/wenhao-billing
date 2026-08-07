# Recurring draft invoice → approval email notification

**Date:** 2026-08-07
**Status:** Approved (autonomous session — user request: "when recurring invoice
triggered draft invoice need my attention for approve please send me email")

## Problem

The Celery beat task `app.workers.tasks.recurring.scan_and_generate` creates
DRAFT invoices from recurring templates. These land in the "Awaiting
Finalization" queue silently — nobody is told a draft needs approval, so drafts
can sit unnoticed until the user happens to open the app.

## Goal

When a recurring cycle generates a **new** DRAFT invoice — via the beat scan,
the UI "Trigger now" button, or the MCP `trigger_recurring` tool — email every
app user (single-tenant: the admins) a digest listing the drafts with a link
to review each one. (Manual/MCP triggers were added by user follow-up: the
email is wanted as a notification regardless of how the draft was created.)

## Non-goals

- No notification for usage-lock drafts (different pipeline; can be added later
  with the same email helper).
- No per-user notification preferences (single-tenant, YAGNI).

## Design

### 1. `invoicing.find_cycle_invoice()` (new helper)

Extract the existing-invoice lookup from `trigger_recurring_cycle` into
`find_cycle_invoice(db, *, template_invoice_id, cycle_key) -> Invoice | None`.
Both `trigger_recurring_cycle` and the scanner use it, so "does this cycle
already have an invoice?" has exactly one definition. This is how the scanner
distinguishes *newly created* drafts from idempotent re-returns — the trigger's
public signature stays unchanged (13 call sites untouched).

### 2. `email.send_recurring_drafts_email()` (new)

Digest email built on the existing `send_email` transport (Resend → SMTP →
logging fallback). Input: recipient + list of draft summaries
(`invoice_number`, `customer_name`, `amount`, `currency`, `cycle_date`,
`link`). Body-building lives in a pure function
`build_recurring_drafts_email(drafts) -> (subject, text, html)` so formatting
is unit-testable without a transport.

Links point at `{app_base_url}/invoices/{invoice_id}/edit` (the draft review /
finalize screen).

### 3. `services/draft_notifications.py` (shared notifier)

`draft_summary(db, invoice, cycle_key)`, `user_emails(db)`,
`send_draft_notifications(emails, drafts)` (log-and-continue per recipient),
and the convenience `notify_users_of_new_drafts(db, drafts)`. All three
trigger paths use it:

- **Beat scanner** (`workers/tasks/recurring.py`): per template, check
  `find_cycle_invoice` first; if `None` the trigger created a new draft —
  collect its summary. Notify after `db.commit()`.
- **UI trigger** (`routers/invoices.py` `POST /recurring/{id}/trigger`):
  same check; notify after commit.
- **MCP tool** (`mcp/tools/write_invoices.py` `trigger_recurring`): summaries
  and recipient emails are collected inside `tool_session` (which commits on
  exit); the send happens after the session block, so no email fires for a
  rolled-back draft.

In every path a mail failure is logged but never fails the operation or rolls
back the committed draft.

## Error handling

- Malformed templates: unchanged (skipped via `_cycle_key_for`).
- Email transport down: drafts still committed; error logged; next scan does
  **not** re-notify (the cycle invoice now exists). Accepted trade-off — the
  app queue remains the source of truth.

## Testing

- Integration: `find_cycle_invoice` returns `None` before trigger, the created
  invoice after (extends `test_recurring_scheduling.py`).
- Unit: `build_recurring_drafts_email` renders number, customer, amount,
  currency, cycle date, and link in text + html.
