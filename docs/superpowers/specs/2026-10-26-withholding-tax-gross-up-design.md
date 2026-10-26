# Withholding Tax (PPh 21 / PPh 23) with Gross-Up — Design

Date: 2026-10-26
Status: Draft, awaiting approval

## Intent

Indonesian clients often withhold income tax (PPh 23 for services billed by a company, PPh 21 for services
billed by an individual) from what they pay on our invoices. Today the system has no tax at all. When a
client withholds tax, the bank transfer is smaller than the invoice. The matching engine sees an amount
mismatch and sends every payment to `PENDING_MANUAL_REVIEW`. There is also no way to bill so that we
still receive the full agreed price.

The goal is to record the withholding on the invoice. Then:

1. the system knows how much cash to expect, and matching works again;
2. we can choose to **gross up**, raising the invoice so that after the client's withholding we still
   receive the full agreed price.

### Worked example (service price IDR 10,000,000, PPh 23 at 2%)

| Mode | Invoice total | Withheld by client | Cash we receive |
|------|---------------|--------------------|-----------------|
| No tax | 10,000,000 | 0 | 10,000,000 |
| Withholding, no gross-up | 10,000,000 | 200,000 | 9,800,000 |
| Withholding, gross-up | 10,204,082 | 204,082 | 10,000,000 |

Gross-up formula: `total = base / (1 - rate)`, where `withheld = total * rate` and `net = total - withheld = base`.

## Goals

- Each invoice carries a withholding tax setting: tax type (`NONE`, `PPH21`, `PPH23`), rate, and gross-up
  on/off.
- Each customer carries a **default** tax setting. New invoices and recurring templates for that customer
  start with it, and the user can override it on each invoice or recurring template.
- Invoices store a snapshot of the tax setting and the computed amounts. Changing a customer default later
  never changes existing invoices.
- The matching engine compares incoming payments against the **expected net cash**, not the invoice total.
- When a payment settles an invoice that has withholding, the ledger books the withheld part to a new
  prepaid-tax asset account, and the invoice ends fully paid.
- The invoice PDF shows the tax lines and the amount the client should transfer.
- Recurring cycles copy the template's tax setting onto each generated invoice.

## Non-goals

- PPN / VAT, and any tax *added* on top that the client pays to us. (Possible later; that is when option B
  below becomes worth it.)
- Tax on vendor bills (AP side).
- Tracking bukti potong (withholding slips): document numbers, uploads, and reconciliation with
  e-Bupot/DJP.
- Progressive PPh 21 calculation. The user types the effective rate when it differs from the default.
- Tax on quotations. A quote converted to an invoice picks up the customer default on the invoice.
- Per-line-item tax.

## Anti-goals (failure even if it "works")

- Silently adjusting a payment amount to make it match. Matching must still be exact against the expected
  net. Anything else still goes to `PENDING_MANUAL_REVIEW`, as the PRD requires.
- Changing the totals of existing invoices. All existing rows migrate to `NONE`, so their amounts are
  unchanged.
- An unbalanced journal, or a withholding posting that cannot be reversed by the existing
  `reverse_payment` flow.

## Constraints (from the codebase)

- Amounts are `DECIMAL(19,4)`. Invoice currency is fixed at creation.
- The ledger currently posts **only at payment time**: `ledger.post_payment_cleared` debits Bank and
  credits AR. Nothing posts at finalize, and revenue is not booked by the ledger. This design keeps that
  model.
- Matching (`services/matching_engine.py`) compares `payment.amount` against `InvoiceCandidate.balance_due`.
  Candidates are built in `services/reconciliation.py::_load_candidates`.
- Totals are computed in `services/invoicing.py::_compute_totals` (subtotal less discount). That function is
  called by create/update for invoices and recurring templates. `trigger_recurring_cycle` copies totals
  from the template.
- Migrations are mandatory with model changes, and revision IDs must be under 32 characters. The next
  revision is `0027_...`.
- `business_id` scoping must be preserved on any new query.
- Frontend list views use TabView. Forms live in `InvoiceFormView.vue`, `CustomerNewView.vue` /
  `CustomerDetailView.vue`, and `RecurringInvoicesView.vue`. Data fetching goes through `frontend/src/api/`
  plus vue-query.
- MCP tools (`app/mcp/tools/`) expose invoice/customer/recurring create and update. They should accept the
  new optional fields so the assistant can set them.

## Approaches considered

**A. Tax types defined in code (CHOSEN).** An enum `NONE | PPH21 | PPH23` with default rates
(PPH21 2.5%, PPH23 2%) in a small module, e.g. `app/services/withholding.py`. Customer and invoice columns
hold the type, rate, and gross-up flag. Adding a type later is a one-line code change. This is the simplest
option and needs no settings screen.

**B. Tax-type master table with an admin settings screen.** More flexible (admin-editable rates, per-type
ledger accounts, room for PPN). It costs a new CRUD module and UI for what is two types today. Rejected for
now. A can be upgraded to B later by turning the enum into rows, without reshaping invoice data.

**Per-line-item tax.** Rejected: PPh is withheld on the total service amount.

The user picked **A**.

## Decided design

### Data model (migration `0027_withholding_tax`)

`customers`:
- `default_wht_type` VARCHAR(10) NOT NULL DEFAULT 'NONE'
- `default_wht_rate` NUMERIC(7,4) NULL. This is a percent, e.g. 2.0000. NULL means use the type's default.
- `default_wht_gross_up` BOOLEAN NOT NULL DEFAULT false

`invoices` (also used for recurring templates, since `is_template` rows live in the same table):
- `wht_type` VARCHAR(10) NOT NULL DEFAULT 'NONE'
- `wht_rate` NUMERIC(7,4) NULL. The percent snapshot. Required when type ≠ NONE.
- `wht_gross_up` BOOLEAN NOT NULL DEFAULT false
- `wht_base` NUMERIC(19,4) NULL. The amount after discount, before gross-up.
- `wht_amount` NUMERIC(19,4) NOT NULL DEFAULT 0. The amount the client is expected to withhold.

Meaning of the existing columns stays as it is:
- `amount` = the invoice total the client is billed. With gross-up, this is the grossed-up total.
- `balance_due` = what is still owed **including** the part the client will withhold. This keeps AR and the
  existing balance logic coherent.

Expected cash for a fully open invoice = `balance_due - wht_amount`.

### Calculation (`_compute_totals` extended)

1. `subtotal` = sum of the lines (unchanged).
2. `base` = subtotal after discount. This is the current `amount`.
3. If type is NONE: `total = base`, `wht_amount = 0`.
4. Without gross-up: `total = base` and `wht_amount = round(base * rate)`.
5. With gross-up: `total = round(base / (1 - rate))` and `wht_amount = total - base`.
   So the net is always exactly `base`, with no rounding residue.
6. Rounding uses the currency's minor unit. IDR rounds to 0 decimals; USD/SGD round to 2. Withholding on
   non-IDR invoices is allowed.

The rate must be in the range 0 < rate < 100 when type ≠ NONE. Validate this in the Pydantic schemas.

### Defaults flow

- On the invoice and recurring-template forms, choosing a customer pre-fills type, rate, and gross-up from
  the customer's defaults. The user can override them. The backend also applies customer defaults when a
  create payload omits the tax fields. This covers MCP, quote→invoice, and recurring.
- `trigger_recurring_cycle` and `lock_usage_invoice` copy the template's/invoice's tax fields and recompute
  through the same function.

### Matching

- `InvoiceCandidate` gains `expected_cash`. For an invoice with no payments yet, this is
  `balance_due - wht_amount`.
- `_score`, `is_exact_match`, and the mismatch logic compare `payment.amount` against `expected_cash`
  instead of `balance_due`. When `wht_amount = 0`, behaviour is unchanged.
- A payment equal to the gross total (the client did not withhold) is an amount mismatch, so it goes to
  manual review. That is correct.

### Ledger (`post_payment_cleared`)

- New CoA account seeded in the migration: `1300 Prepaid Income Tax (PPh dibayar di muka)`, type ASSET.
  It sits under assets because it is a tax credit we claim against our own annual PPh.
- When a payment settles an invoice with `wht_amount > 0`, the entry is:
  - DR Bank `payment.amount`
  - DR Prepaid Income Tax `wht_amount`
  - CR AR `payment.amount + wht_amount`

  This applies in the same-currency case. In the cross-currency case, the withholding line is in the
  invoice currency, and AR is relieved by the full `balance_due` as today.
- `invoice.balance_due` goes to 0 and the status becomes paid, exactly as for a normal full payment.
- `reverse_payment` already reverses the whole journal entry, so the withholding line reverses with it.
  `balance_due` must be restored by `payment.amount + payment.wht_amount`, not just `payment.amount`.

### Withholding that differs from the snapshot (user decision)

If the client withholds a different amount, or doesn't withhold at all, the user coordinates with the client
outside the system. For example, the client tops up the shortfall. The system adds **no** special tooling
for this:

- Manual-review approval (`approve_manual_match`) books the withholding only when
  `payment.amount == expected_cash` for that invoice. Any other amount keeps today's partial-pay behaviour.
- The actual withheld amount is stored on the payment (`payments.wht_amount` NUMERIC(19,4) NOT NULL
  DEFAULT 0). This lets reversal restore `balance_due` by `payment.amount + payment.wht_amount`. The
  invoice's `wht_amount` is the expectation; the payment's `wht_amount` is what was booked. (My call.)

### Manual "Record payment" (user decision: option A)

The manual record-payment endpoint (`routers/invoices.py::record_payment`) creates a CLEARED payment and
lowers `balance_due`, but it posts **no ledger entry**. This stays as it is: it still does not post to
the ledger.

- The dialog gets a "PPh withheld" field. It is pre-filled with the invoice's `wht_amount` when nothing has
  been withheld on that invoice yet, and 0 otherwise. The user can edit it.
- `balance_due` decreases by `amount + wht_withheld`. Validation: the sum must be ≤ `balance_due`. The
  value is stored in `payments.wht_amount`.
- Known gap, accepted: withholding recorded this way does not reach the Prepaid Income Tax account,
  because this path has no ledger posting at all today. Moving this path onto the ledger is a separate
  follow-up.

### PDF

The totals block in `services/pdf/context.py` gets these rows:
- Subtotal
- Discount (if any)
- `Gross-up PPh 23 (2%)` — only shown in gross-up mode, as `total - base`
- **Total**
- `Less: PPh 23 withheld (2%)` — shown as a negative of `wht_amount`
- **Amount to transfer** = `total - wht_amount`

When type is NONE, the PDF is unchanged.

### Frontend

- Customer form: a "Withholding tax" block with a type select (None / PPh 21 / PPh 23), a rate input
  (pre-filled with the type default and editable), and a "Gross up" checkbox.
- Invoice form and recurring template form: the same three controls near the discount field. They
  pre-fill from the customer, and the live totals preview shows the PDF rows above.
- The invoice detail/list may show the expected cash. That is the builder's call; it is not required.

### MCP

The invoice/recurring/customer create and update tools accept optional `wht_type`, `wht_rate`, and
`wht_gross_up`. Read tools return them, plus `wht_amount`.

## Left to the builder

- Exact module and function names, schema field naming in API payloads, and form layout details.
- Test coverage. At minimum:
  - unit tests for the calculation (all three modes, with IDR and USD rounding);
  - matching with withholding;
  - ledger balance and reversal;
  - recurring copy;
  - migration up and down.
