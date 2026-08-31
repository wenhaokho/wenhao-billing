# Manual "Send receipt" on Invoice Detail — Design

**Date:** 2026-08-07
**Status:** Approved
**Builds on:** `2026-08-07-payment-receipt-email-design.md` (PR #23)

## Problem

Receipts are only sent at record-payment time (opt-out checkbox). If the
email was skipped, failed and dismissed, or the customer asks for a copy
later, staff have no way to (re)send a receipt for an already-recorded
payment.

## Design

A **"Send receipt"** button on the invoice detail view (`InvoiceFormView`)
for invoices that have payments (status `PAID` or `PARTIAL`), opening a
small dialog:

- **Payment** dropdown — the invoice's payments, newest first, defaulting
  to the most recent. Option label: `date · amount · reference` (reference
  omitted when absent). Shown even when there is only one payment.
- **To** — defaults to the customer's `contact_email`.
- **Cc** — defaults to the logged-in user's email (same pattern as
  `SendInvoiceDialog` / `RecordPaymentDialog`).
- **Send** → calls the existing
  `POST /invoices/{invoice_id}/payments/{payment_id}/send-receipt`.
  Success closes the dialog; failure shows the error inside the dialog
  (the open dialog itself is the retry mechanism — no confirm prompt).

## Backend

One new endpoint: `GET /invoices/{invoice_id}/payments` →
`list[PaymentOut]`, ordered by `payment_date` desc then `created_at` desc.
404 when the invoice doesn't exist; empty list when it has no payments.
The send-receipt endpoint is reused unchanged. No model/schema changes,
no migration.

## Frontend

- New `SendReceiptDialog.vue` component (Dialog + Dropdown + To/Cc
  InputTexts), mirroring `SendInvoiceDialog.vue` conventions.
- `InvoiceFormView`: button in the read-only actions row
  (`v-if` status `PAID`/`PARTIAL`), a vue-query fetch of the payments list
  (enabled only when the button is visible), and a mutation calling
  send-receipt. NOTE: the view already has a `sendReceipt` helper (the
  post-record-payment auto-send with confirm-retry) — the new pieces use
  distinct names (`resendReceipt`, `showReceiptDialog`, `receiptError`).

## Out of scope

- Receipt sending from the invoices list rows
- Receipt history / audit of sent receipts
- Bills (AP side)

## Testing

- Integration: payments list ordering (two payments), empty list, unknown
  invoice 404.
- Frontend: `vue-tsc` + build; browser smoke (button appears on the PAID
  smoke invoice, dialog defaults, send succeeds).
