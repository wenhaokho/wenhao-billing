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

When the beat scan generates one or more **new** DRAFT invoices, email every
app user (single-tenant: the admins) a digest listing the drafts with a link
to review each one.

## Non-goals

- No notification for manually triggered cycles (UI "Trigger now" button, MCP
  `trigger_recurring` tool) — the user is already looking at the app there.
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

### 3. Scanner changes (`workers/tasks/recurring.py`)

Per template: check `find_cycle_invoice` first; if `None`, the subsequent
`trigger_recurring_cycle` call created a new draft — collect its summary
(customer name joined from `customers`). After `db.commit()`, send one digest
per user in the `users` table. Email sending is wrapped in try/except per
recipient: a mail failure is logged but never fails the beat task or rolls
back committed drafts.

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
