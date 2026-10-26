# Withholding Tax (PPh 21/23) with Gross-Up — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let invoices carry PPh 21/23 withholding, with optional gross-up. Customer defaults can be
overridden per invoice/template. Matching and the ledger use the expected net cash.

**Architecture:** One pure calculation module (`app/services/withholding.py`) owns the tax types, the
default rates, the rounding, and the gross-up math. Invoicing, matching, ledger, PDF, MCP and the frontend
all call into it, or mirror it in TypeScript. Invoices keep `amount`/`balance_due` as the gross figures.
The expected cash is always `balance_due - (wht_amount - wht_booked)`.

**Tech Stack:** FastAPI, SQLAlchemy 2.0, Alembic, Postgres 16, pytest (real DB), Vue 3 + TS + PrimeVue.

**Spec:** `docs/superpowers/specs/2026-10-26-withholding-tax-gross-up-design.md`

## Global Constraints

- All money is `Numeric(19,4)`. The rate is stored as a percent in `Numeric(7,4)` (2% → `2.0000`).
- Tax types are `NONE`, `PPH21`, `PPH23`. Default rates are PPH21 `2.5` and PPH23 `2.0`. PDF/UI labels
  are `PPh 21` and `PPh 23`.
- Rate validation: when type ≠ NONE, `0 < rate < 100`. When type = NONE, rate is stored as NULL and
  gross-up as false.
- Rounding: IDR rounds to 0 decimals; every other currency rounds to 2. Use `ROUND_HALF_UP`.
- Gross-up: `total = round(base / (1 - rate/100))` and `wht = total - base`. Without gross-up:
  `total = base` and `wht = round(base * rate/100)`. Here `base` is the subtotal after discount.
- Migration revision ID: `0027_withholding_tax`, with `down_revision = "0026_customer_cc_emails"`.
- Payment matching stays exact (`AMOUNT_EPSILON`). Never adjust amounts to force a match.
- The manual record-payment endpoint still posts **no** ledger entry (user chose option A).
- Run commands from the repo root through Docker:
  - Tests: `docker compose exec -T -e TEST_DATABASE_URL="postgresql+psycopg://billing:billing@db:5432/billing_test" backend pytest <args>`.
  - Lint only the touched files: `docker compose exec -T backend ruff check <files>`. The repo has a baseline
    of about 470 errors, so lint only the delta.
  - Frontend: `docker compose exec -T frontend npm run build`.

**Plan decision (refines the spec):** add the denormalised column `invoices.wht_booked` (the withholding
already recognised by payments) next to `payments.wht_amount`. This works like `balance_due`: matching and
the PDF can compute the expected cash without querying payments.

## Review Focus

1. **Discount + gross-up together.** The tax base is the post-discount amount. A 100% discount gives
   base 0, so wht is 0 and the invoice still settles as zero-value. → Task 1 and Task 3 tests.
2. **A client pays the gross total, without withholding.** The payment must go to `PENDING_MANUAL_REVIEW`
   and must not clear. → Task 4 test.
3. **Reversal, then re-payment.** Reversing an auto-cleared withholding payment restores `balance_due`
   and `wht_booked`. A fresh payment of the net amount then auto-clears again. → Task 4 test.
4. **Changing the customer on a draft overwrites tax fields the user typed.** Tax fields should prefill
   only when the customer changes, the same way currency already does. They must not be re-applied when
   an existing invoice loads. → Task 9 manual check.
5. **A recurring cycle after the customer default changes.** The generated invoice uses the
   *template's* snapshot, not the customer's current default. → Task 3 test.

---

### Task 1: Calculation module, migration, model columns

**Files:**
- Create: `backend/app/services/withholding.py`
- Create: `backend/alembic/versions/0027_withholding_tax.py`
- Modify: `backend/app/models/customer.py`, `backend/app/models/invoice.py`, `backend/app/models/payment.py`
- Test: `backend/tests/unit/test_withholding.py`

**Interfaces:**
- Produces:
  - `WHT_TYPES: tuple[str, ...] = ("NONE", "PPH21", "PPH23")`
  - `DEFAULT_RATES: dict[str, Decimal] = {"PPH21": Decimal("2.5"), "PPH23": Decimal("2")}`
  - `WHT_LABELS = {"PPH21": "PPh 21", "PPH23": "PPh 23"}`
  - `@dataclass(frozen=True) class WhtResult: total: Decimal; wht_amount: Decimal; base: Decimal`
  - `round_ccy(value: Decimal, currency: str) -> Decimal`
  - `compute_withholding(base: Decimal, currency: str, wht_type: str, rate: Decimal | None, gross_up: bool) -> WhtResult`
  - `expected_cash(invoice) -> Decimal`, which returns `balance_due - (wht_amount - wht_booked)` and is never below 0
  - `normalize(wht_type: str, rate: Decimal | None, gross_up: bool) -> tuple[str, Decimal | None, bool]`.
    For NONE it returns `("NONE", None, False)`. For other types, a missing rate becomes `DEFAULT_RATES[type]`.
    It raises `ValueError` on an unknown type or a rate outside `(0, 100)`.
- Columns:
  - customers: `default_wht_type` VARCHAR(10) NOT NULL DEFAULT 'NONE', `default_wht_rate` NUMERIC(7,4) NULL,
    `default_wht_gross_up` BOOL NOT NULL DEFAULT false.
  - invoices: `wht_type`, `wht_rate`, `wht_gross_up` (same shapes as the customer columns), plus
    `wht_base` NUMERIC(19,4) NULL, `wht_amount` NUMERIC(19,4) NOT NULL DEFAULT 0, and
    `wht_booked` NUMERIC(19,4) NOT NULL DEFAULT 0.
  - payments: `wht_amount` NUMERIC(19,4) NOT NULL DEFAULT 0.
  - The migration also inserts CoA `("1300", "Prepaid Income Tax (PPh dibayar di muka)", "ASSET")`.
    Downgrade deletes that row and drops the columns.

- [ ] **Step 1: Write failing unit tests** in `test_withholding.py`:
  - `compute_withholding(Decimal("10000000"), "IDR", "PPH23", Decimal("2"), False)` gives `total == 10000000`
    and `wht_amount == 200000`.
  - The same call with gross-up gives `total == 10204082` and `wht_amount == 204082`, and
    `total - wht_amount == 10000000`.
  - `compute_withholding(Decimal("1000"), "USD", "PPH21", Decimal("2.5"), True)` gives `total == Decimal("1025.64")`
    and `wht_amount == Decimal("25.64")`.
  - With NONE: `total == base` and `wht_amount == 0`.
  - With `base == 0` and gross-up: `total == 0` and `wht == 0`.
  - `normalize("PPH21", None, True) == ("PPH21", Decimal("2.5"), True)`.
  - `normalize("NONE", Decimal("2"), True) == ("NONE", None, False)`.
  - `normalize("PPH23", Decimal("100"), False)` raises `ValueError`, and so does `normalize("PPN", ...)`.
- [ ] **Step 2: Run the tests and confirm they fail.**
  Run `... backend pytest tests/unit/test_withholding.py -v`. Expected: ImportError.
- [ ] **Step 3: Implement `withholding.py`.** Follow `0026_customer_cc_emails.py` style for the migration and add the
  model columns. Use `server_default` for the NOT NULL columns so existing rows become NONE/0.
- [ ] **Step 4: Run the tests and confirm they pass.** Run the unit tests, then
  `... backend pytest tests/integration/test_zero_value_invoice.py` to confirm the migration applies and
  nothing regressed.
- [ ] **Step 5: Commit** with the message `feat(tax): withholding calc module and schema`.

### Task 2: Customer default tax settings (API + service)

**Files:**
- Modify: `backend/app/schemas/customer.py` (CustomerBase / CustomerOut), `backend/app/services/customers.py`
- Test: `backend/tests/integration/test_customer_wht_defaults.py`

**Interfaces:**
- Consumes: `withholding.normalize`.
- Produces: `CustomerOut` gains `default_wht_type: str`, `default_wht_rate: Decimal | None` and
  `default_wht_gross_up: bool`. Create/update accept the same fields, all optional; create defaults to
  NONE. Values pass through `normalize`; a `ValueError` becomes a 422.

- [ ] **Step 1: Write failing tests.**
  - `test_create_customer_with_pph23_gross_up`: POST `/api/v1/customers` with `default_wht_type="PPH23"`
    and `default_wht_gross_up=true` and no rate. The response has `default_wht_rate == "2.0000"`.
  - `test_customer_wht_invalid_rate_422`: a rate of 150 returns 422.
  - `test_customer_none_clears_rate`: PATCH to NONE stores rate NULL and gross-up false.
- [ ] **Step 2: Run the tests and confirm they fail.**
- [ ] **Step 3: Implement** the schema fields plus normalisation in the customer service's create/update.
- [ ] **Step 4: Run the tests and confirm they pass**, along with the existing `tests/unit/test_customers_service.py`.
- [ ] **Step 5: Commit** with the message `feat(tax): customer default withholding settings`.

### Task 3: Invoice, recurring template, quote conversion, recurring/usage generation

**Files:**
- Modify: `backend/app/schemas/invoice.py` (InvoiceCreate, InvoiceUpdate, RecurringTemplateCreate, InvoiceOut;
  also the recurring-template update schema if it is separate)
- Modify: `backend/app/services/invoicing.py` (`_compute_totals`, `create_invoice`, `update_invoice`,
  `create_recurring_template`, `update_recurring_template`, `trigger_recurring_cycle`, `lock_usage_invoice`)
- Modify: `backend/app/services/quoting.py` (quote→invoice conversion, around line 286)
- Test: `backend/tests/integration/test_invoice_withholding.py`

**Interfaces:**
- Consumes: `compute_withholding`, `normalize`, and the customer default columns.
- Produces:
  - `_compute_totals(lines, discount_type, discount_value, currency: str, wht_type: str = "NONE", wht_rate: Decimal | None = None, wht_gross_up: bool = False) -> tuple[Decimal, Decimal, WhtResult]`.
    It returns `(subtotal, total, wht)`, and `invoice.amount = balance_due = total`.
  - `_resolve_wht(db, customer_id, payload_type, payload_rate, payload_gross_up) -> tuple[str, Decimal | None, bool]`.
    It uses the payload fields when `wht_type` is set, otherwise the customer defaults, otherwise NONE.
  - Invoice schemas: optional `wht_type: Literal["NONE","PPH21","PPH23"] | None`, `wht_rate: Decimal | None`
    and `wht_gross_up: bool | None` on create/update. `InvoiceOut` adds `wht_type`, `wht_rate`,
    `wht_gross_up`, `wht_base`, `wht_amount`, `wht_booked` and `expected_cash: Decimal`, computed with
    `withholding.expected_cash`.
  - Every place that builds an `Invoice` sets `wht_type`, `wht_rate`, `wht_gross_up`, `wht_base` and
    `wht_amount`. Cycle and usage generation copy the source template/invoice's snapshot and recompute
    through `_compute_totals`; they do not copy `amount` verbatim. Quote conversion applies the customer
    default through `_resolve_wht` and recomputes `amount` and `balance_due`.

- [ ] **Step 1: Write failing tests.**
  - `test_create_invoice_gross_up_pph23`: an IDR line of 10,000,000 with PPH23 2% and gross-up gives
    `amount == balance_due == 10204082` and `wht_amount == 204082`. The API's `expected_cash` is
    `10000000`.
  - `test_create_invoice_inherits_customer_default`: the customer has PPH21 with gross-up and the payload
    has no wht fields, so the invoice gets `wht_type == "PPH21"` and `wht_rate == 2.5`.
  - `test_explicit_none_overrides_customer_default`: the payload sets `wht_type="NONE"`, so `wht_amount == 0`.
  - `test_discount_then_withholding`: a 10% discount on 1,000,000 with PPH23 and no gross-up gives
    `amount == 900000` and `wht_amount == 18000`.
  - `test_update_draft_recomputes_withholding`: turning on gross-up for a draft updates `amount`.
  - `test_recurring_cycle_uses_template_snapshot`: the template is PPH23 with gross-up. After that, the
    customer default changes to NONE. The triggered cycle invoice is still PPH23 with gross-up and has
    the same `amount` as the template.
  - `test_quote_conversion_applies_customer_default`.
- [ ] **Step 2: Run the tests and confirm they fail.**
- [ ] **Step 3: Implement** the changes listed in Interfaces. Update every existing caller of `_compute_totals`.
- [ ] **Step 4: Run the tests and confirm they pass**, plus the regression suites
  `tests/integration/test_recurring_scheduling.py`, `test_zero_value_invoice.py` and `test_invoices_summary.py`.
- [ ] **Step 5: Commit** with the message `feat(tax): withholding on invoices, templates, conversions`.

### Task 4: Matching, auto-clear, manual approval, ledger, reversal

**Files:**
- Modify: `backend/app/services/matching_engine.py`, `backend/app/services/reconciliation.py`, `backend/app/services/ledger.py`
- Test: `backend/tests/unit/test_matching_engine.py` (add cases),
  `backend/tests/integration/test_reconciliation_withholding.py`

**Interfaces:**
- Consumes: `withholding.expected_cash` and the `wht_*` columns.
- Produces:
  - `InvoiceCandidate` gains the trailing field `wht_outstanding: Decimal = Decimal("0")` and the property
    `expected_cash -> Decimal` (`balance_due - wht_outstanding`). `_score`, `is_exact_match` and the
    mismatch/adjustment logic in `match_payment` compare against `expected_cash`.
    `_load_candidates` fills `wht_outstanding = wht_amount - wht_booked`.
  - `ledger.post_payment_cleared(db, payment, invoice, withheld: Decimal = Decimal("0")) -> JournalEntry`
    with `PREPAID_TAX_CODE = "1300"`.
    - Same currency: DR Bank `payment.amount`, DR 1300 `withheld`, CR AR `payment.amount + withheld`.
    - Cross currency: AR is credited `balance_due` as today, and DR 1300 `withheld` is booked in the
      invoice currency. The FX plug is computed from `bank_base + wht_base - ar_base`.
    - The 1300 line is omitted when `withheld == 0`.
  - Clearing logic, in `process_incoming_payment` CLEARED and in `approve_manual_match`:
    - Set `withheld = wht_amount - wht_booked` when the currency matches and
      `abs(payment.amount - expected_cash) <= AMOUNT_EPSILON`, or on a cross-currency full settle.
      Otherwise set `withheld = 0`.
    - Then set `payment.wht_amount = withheld`, add `withheld` to `invoice.wht_booked`, and reduce
      `balance_due` by `payment.amount + withheld`.
  - `reverse_payment`: `balance_due += payment.amount + payment.wht_amount` and
    `invoice.wht_booked -= payment.wht_amount`.

- [ ] **Step 1: Write failing tests.**
  - Unit test `test_withholding_net_payment_is_exact_match`: candidate `balance_due=10204082`,
    `wht_outstanding=204082`; payment 10,000,000 → CLEARED.
  - Unit test `test_gross_payment_on_withholding_invoice_goes_to_review`: payment 10,204,082 on the same
    candidate → `PENDING_MANUAL_REVIEW` with `amount_mismatch`.
  - Integration test `test_auto_clear_books_prepaid_tax`: after clearing, the invoice is PAID with
    `balance_due == 0` and `wht_booked == 204082`. The journal is balanced and has a DR line on account
    1300 for 204,082 and a CR AR line for 10,204,082.
  - Integration test `test_reverse_then_repay`: reversal restores `balance_due == 10204082` and
    `wht_booked == 0`, with status OPEN. A second net payment of 10,000,000 auto-clears again.
  - Integration test `test_manual_approve_non_matching_amount_books_no_withholding`: approving 9,600,000
    gives `payment.wht_amount == 0`, `balance_due == 604082` and status PARTIAL.
- [ ] **Step 2: Run the tests and confirm they fail.**
- [ ] **Step 3: Implement** the changes listed in Interfaces.
- [ ] **Step 4: Run the tests and confirm they pass**, plus `tests/unit/test_matching_engine.py`,
  `tests/integration/test_reconciliation_pipeline.py` and `test_open_ar_base.py`.
- [ ] **Step 5: Commit** with the message `feat(tax): match on expected net cash, book PPh to prepaid tax`.

### Task 5: Manual record-payment with withheld amount (no ledger)

**Files:**
- Modify: `backend/app/schemas/payment.py` (`RecordPaymentRequest`, `PaymentOut`), `backend/app/api/v1/routers/invoices.py::record_payment`
- Test: `backend/tests/integration/test_record_payment_withholding.py`

**Interfaces:**
- Produces:
  - `RecordPaymentRequest.wht_amount: Decimal = Field(default=Decimal("0"), ge=0, max_digits=19, decimal_places=4)`.
  - `PaymentOut.wht_amount: Decimal`.
  - The endpoint rejects `amount + wht_amount > balance_due` with a 400. It sets `payment.wht_amount`,
    adds the amount to `invoice.wht_booked`, and reduces `balance_due` by `amount + wht_amount`. It
    still makes **no** ledger call.

- [ ] **Step 1: Write failing tests.**
  - `test_record_net_plus_withheld_marks_paid`: a 10,000,000 payment with `wht_amount` 204,082 on the
    gross-up invoice → PAID, with `wht_booked == 204082`.
  - `test_record_exceeding_balance_400`.
  - `test_record_without_wht_unchanged`: the old behaviour is preserved when `wht_amount` is omitted.
- [ ] **Step 2: Run the tests and confirm they fail.**
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run the tests and confirm they pass**, plus `test_invoice_payments_list.py` and `test_send_receipt.py`.
- [ ] **Step 5: Commit** with the message `feat(tax): withheld amount on manual record-payment`.

### Task 6: Invoice PDF rows

**Files:**
- Modify: `backend/app/services/pdf/context.py::build_invoice_context`
- Test: `backend/tests/unit/test_pdf_context.py` (add cases)

**Interfaces:**
- Consumes: `WHT_LABELS`, `withholding.expected_cash`, and `_fmt_num`/`_fmt_pct`.
- Produces: when `wht_type != "NONE"`, the summary rows in order are:
  - Subtotal
  - [Discount]
  - `Gross-up {label} ({rate}%)` with value `amount - wht_base` (only when gross-up is on; cls `"grossup"`)
  - `Total` with value `amount` (cls `"total"`)
  - `Less: {label} withheld ({rate}%)` with value `−wht_amount` (cls `"wht"`)
  - [Paid]

  The hero label becomes `"Amount to Transfer"` and the hero amount becomes `expected_cash(invoice)`.
  For NONE, the output is identical to today.

- [ ] **Step 1: Write failing tests.**
  - `test_invoice_context_gross_up_rows`: check the labels/values above for the 10,204,082 example, and
    that `hero_amount` formats 10,000,000.
  - `test_invoice_context_no_wht_unchanged`: the row labels equal today's.
- [ ] **Step 2: Run the tests and confirm they fail.**
- [ ] **Step 3: Implement.** Add CSS for `.grossup`/`.wht` only if the template needs it to match the discount row style.
- [ ] **Step 4: Run the tests and confirm they pass**, plus `tests/unit/test_invoice_pdf.py` and `test_pdf_render_html.py`.
- [ ] **Step 5: Commit** with the message `feat(tax): show withholding on invoice PDF`.

### Task 7: MCP tools

**Files:**
- Modify: `backend/app/mcp/tools/write_invoices.py` (`create_invoice`, `update_invoice`, `create_recurring_template`),
  `backend/app/mcp/tools/write_customers.py`, and the field lists in `read_invoices.py` / the customer read tool
- Test: extend `backend/tests/integration/test_mcp_write_invoices.py` and `test_mcp_read_invoices.py`

**Interfaces:**
- Produces: optional `wht_type`, `wht_rate` and `wht_gross_up` parameters, passed through to the
  schemas. Customer tools take `default_wht_*`. Invoice reads include `wht_type`, `wht_rate`,
  `wht_gross_up`, `wht_amount` and `expected_cash`. Tool docstrings state the gross-up meaning in one line.

- [ ] **Step 1: Write failing tests.** `test_mcp_create_invoice_with_pph23_gross_up` asserts the returned
  `wht_amount`. A read test asserts the `expected_cash` key.
- [ ] **Step 2: Run the tests and confirm they fail.**
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run the tests and confirm they pass**, plus `test_mcp_writes_all.py` and `test_mcp_reads_all.py`.
- [ ] **Step 5: Commit** with the message `feat(tax): expose withholding via MCP tools`.

### Task 8: Frontend — shared calc + customer form

**Files:**
- Create: `frontend/src/composables/withholding.ts`
- Modify: `frontend/src/components/customerForm.ts`, `frontend/src/components/CustomerForm.vue`

**Interfaces:**
- Produces in `withholding.ts`:
  - `type WhtType = "NONE" | "PPH21" | "PPH23"`
  - `WHT_OPTIONS: { label: string; value: WhtType }[]` with labels "None", "PPh 21" and "PPh 23"
  - `WHT_DEFAULT_RATES: Record<"PPH21"|"PPH23", number>` = `{ PPH21: 2.5, PPH23: 2 }`
  - `computeWithholding(base: number, currency: string, type: WhtType, rate: number | null, grossUp: boolean): { total: number; whtAmount: number }`,
    which mirrors the backend math and rounding.
- `CustomerFormData` gains `default_wht_type`, `default_wht_rate` and `default_wht_gross_up`.
  `emptyCustomerForm` sets them to NONE/null/false. `buildCustomerPayload` sends them.
- The UI adds a "Withholding tax" block: a type Select, a rate InputNumber (prefilled from the default
  when the type changes, and disabled when NONE), and a "Gross up" Checkbox (disabled when NONE).

- [ ] **Step 1:** Implement the above.
- [ ] **Step 2:** Run `docker compose exec -T frontend npm run build`. Expected: exit 0.
- [ ] **Step 3: Browser check** (preview at http://localhost:5173). Create a customer with PPh 23 and gross-up,
  reload it, and confirm the values persisted.
- [ ] **Step 4: Commit** with the message `feat(tax): customer withholding defaults in UI`.

### Task 9: Frontend — invoice/template form and record-payment dialog

**Files:**
- Modify: `frontend/src/views/InvoiceFormView.vue`, `frontend/src/composables/useInvoiceForm.ts`, `frontend/src/components/RecordPaymentDialog.vue`

**Interfaces:**
- Consumes: `computeWithholding`, `WHT_OPTIONS`, `WHT_DEFAULT_RATES`, and the API fields from Tasks 3 and 5.
- `useInvoiceForm` accepts `whtType`, `whtRate` and `whtGrossUp` refs plus `currency`, and returns the
  added computeds `whtAmount`, `grossUpAmount` and `amountToTransfer`. The existing `total` becomes the
  grossed-up total.
- InvoiceFormView:
  - Tax controls sit next to the discount controls and are used in both invoice and template mode.
  - The `watch(customerId, …)` at about line 510, which already prefills currency, also prefills the
    three tax fields from the customer. The `watch(existing, …)` loader (about line 537) and the
    template loader (about line 261) set them from the saved record.
  - Both save payloads (about lines 602 and 627) send `wht_type`, `wht_rate` and `wht_gross_up`.
  - The totals panel (about line 1029) shows the same rows as the PDF in Task 6.
- RecordPaymentDialog:
  - New prop `whtOutstanding: string | number`, taken from `wht_amount - wht_booked`.
  - A "PPh withheld" InputNumber, prefilled with `whtOutstanding`.
  - The "Full balance" action fills `amount = balanceDue - whtOutstanding`.
  - `canSubmit` requires `amount + wht ≤ balanceDue`, and `wht_amount` is included in the POST.
  - The parent passes the prop from the invoice.

- [ ] **Step 1:** Implement the above.
- [ ] **Step 2:** Run `docker compose exec -T frontend npm run build`. Expected: exit 0.
- [ ] **Step 3: Browser check.**
  - Create an invoice for the PPh 23 gross-up customer with one 10,000,000 IDR line. The totals show
    Gross-up 204,082, Total 10,204,082, Less PPh 23 −204,082, and Amount to Transfer 10,000,000.
  - Save, reload, and confirm the values persisted. Change the tax to NONE on the draft and confirm
    the totals revert.
  - Finalize it, open Record payment, and confirm it prefills 10,000,000 + 204,082. Submit, and confirm
    the invoice is PAID.
  - Create a recurring template and confirm the tax fields persist.
  - (Review Focus 4) Open an existing invoice whose tax differs from the customer default, and confirm
    the saved values are shown, not the customer default.
- [ ] **Step 4: Commit** with the message `feat(tax): withholding in invoice form and record-payment dialog`.

### Task 10: Final verification

- [ ] Full backend suite: `docker compose exec -T -e TEST_DATABASE_URL=... backend pytest -q`. Expected: all pass.
- [ ] Ruff on the touched backend files shows no new errors compared with main.
- [ ] Migration round trip on the test DB: `alembic downgrade 0026_customer_cc_emails && alembic upgrade head`,
  run with `-x` / `DATABASE_URL` pointed at `billing_test`.
- [ ] Generate a PDF for the gross-up invoice in the browser and eyeball the totals block.
