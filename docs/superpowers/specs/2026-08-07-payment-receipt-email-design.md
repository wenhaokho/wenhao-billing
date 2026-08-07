# Payment Receipt Email on Manual Record-Payment — Design

**Date:** 2026-08-07
**Status:** Approved

## Problem

When staff manually records a payment against an invoice, nothing is sent to the
customer. Customers expect a payment receipt, and its absence generates
"did you get my transfer?" follow-up emails.

## Decision summary

- **Trigger:** Only the manual record-payment flow, via a pre-checked opt-out
  checkbox in the Record Payment dialog. Auto-reconciled webhook/email-intake
  payments send nothing — no customer email without a human in the loop.
- **Format:** Email with a formal PDF receipt attached, plus a short summary in
  the body.

## Flow

1. Record Payment dialog (invoice detail view) gains a pre-checked checkbox
   **"Email receipt (PDF) to customer"**. When checked, it reveals editable
   **To** (defaults to the customer's billing email) and **Cc** (defaults to the
   logged-in user's email, matching the invoice send dialog).
2. Frontend calls the existing `POST /invoices/{invoice_id}/record-payment`
   endpoint unchanged. On success, if the checkbox is ticked, it calls a new
   endpoint:
   `POST /invoices/{invoice_id}/payments/{payment_id}/send-receipt`
   with `{to, cc}`.
3. Two separate calls keep payment recording atomic. If the email fails, the
   payment is already recorded; the UI shows a warning toast with a retry that
   re-calls `send-receipt` only.

## Backend

- **`services/receipt_pdf.py`** — new PDF builder mirroring
  `quotation_pdf.py`, using the shared `services/pdf` engine. Contents:
  - Business and customer details
  - Receipt reference: `RCT-{payment_id}`
  - Invoice number, payment date, amount + currency, intake source/method
  - Remaining balance, or "Paid in full" when the invoice balance reaches zero
- **`send-receipt` endpoint** in the invoices router:
  - Validates the payment belongs to the invoice and the invoice to the
    caller's business (business-id scoping preserved)
  - Renders the receipt PDF and sends via the existing `send_email` service
    with the PDF attached — same shape as `send_invoice`
  - Email body: invoice number, amount received, remaining balance
- No schema/model changes; no migration needed.

## Frontend

- Record Payment dialog: checkbox + To/Cc fields (shown only when checked).
  No new views or routes.

## Error handling

- Payment recording never fails or rolls back due to email problems.
- `send-receipt` failures surface as a warning toast with a retry action.
- Invalid recipient, payment/invoice mismatch, or cross-business access
  return 4xx from `send-receipt` without side effects.

## Testing

- Unit: receipt PDF context — partial payment shows remaining balance;
  full payment shows "Paid in full"; currency formatting.
- Endpoint: happy path with email service mocked; payment not on this
  invoice rejected; cross-business access rejected.

## Out of scope (deliberate)

- Receipts for auto-reconciled (webhook/email-intake) payments
- Sequential receipt numbering
- Resend history / audit UI for receipts
