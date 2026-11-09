# Go backend, plan 6: quotations, projects, bills

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Go answers the 23 quotation, project and bill routes with Python's contract, including quotation-to-invoice conversion through the invoices package, and closes the one gap plan 5 left open.

**Architecture:** Three feature packages: `internal/projects`, `internal/quotations`, `internal/bills`, each with `api.go`, `routes.go`, `queries.sql`, generated `<name>db/`, and tests through `internal/apitest`. Quotations reuse `invoices.ComputeTotals`/`LineAmount` (with withholding `NONE`) and, for conversion, `invoices.ResolveWHT`, `withholding.Compute`, `invoices.NextInvoiceNumber`, `invoices.PaymentTermsDays`/`AddDays`, `invoices.MapWriteError` and `invoices.Get`. Bills keep their own totals (half-even, sum of rounded lines). The quotation PDF and send routes belong to the documents plan.

**Tech Stack:** Go 1.27, Huma v2.40 on chi v5, pgx v5, sqlc 1.31.1, shopspring/decimal, gokit v0.2.0, Postgres 16, Docker Compose (through `backend/dev`).

**Spec:** `docs/superpowers/specs/2026-11-08-go-backend-design.md` ("Behaviour that must be carried exactly", "Tests").

**Contract inventory:** `docs/superpowers/plans/2026-11-09-go-backend-06-quotes-projects-bills-inventory.md` — every route's fields, sentences and statuses with Python file:line, the service rules, the 5 in-scope Python tests, and 24 oddities. It is the reference for exact values; this plan says which to keep and which to change.

## Global Constraints

- `backend-python/` is frozen: never edited. Kept odd behaviour goes to `backend/PORT-NOTES.md`; each "Go changes this" item below also gets a note.
- **Statuses and sentences are Python's** (inventory §1, §2): quotation writes answer **400** for not found (`quotation <id> not found` per the inventory) and GET **404** `quotation not found`; projects and bills answer **404** `project not found` / `bill not found` on every route. Every refusal sentence is verbatim (e.g. `cannot convert a INVOICED quotation to invoice`, `bill is PAID and cannot be voided`, `amount 600 exceeds balance due 500.0000`, `cannot delete: project is referenced by 3 invoice(s)/bill(s)`). `code` is stable snake_case per sentence.
- Lists are bare arrays, never `null`. `ORDER BY` is Python's (`created_at DESC`) plus `<pk> DESC`; line items `position, line_item_id`. Repeated query parameters (`status`) are lists. `GET /quotations` keeps `limit` (default 200, max 500, < 1 → 422); projects and bills lists have none (as Python).
- Decimals `platform.Dec4` (read `.Decimal()`, build `platform.NewDec4`). **Rounding per call site** (inventory §2 "Rounding per call site"): quotations `RoundHalfUp4` through `invoices.ComputeTotals`/`LineAmount`; bills `RoundHalfEven4` on each line, subtotal = sum of rounded lines, total from the unrounded discount then `RoundHalfEven4`; conversion withholding through `withholding.Compute`. Project summary sums are unrounded sums of stored values, rendered at 4 places (Python prints `"0"` when empty: allowed comparison difference, note).
- PATCH is Python's `exclude_unset`: omitted keeps, explicit `null` clears a nullable column; `null` for a NOT NULL column is a 422 at the field (`platform.NotNull`). `line_items` omitted or `null` keeps the lines; a list replaces them.
- **Today** is the UTC calendar date of `platform.Clock`; `last_sent_at`/`accepted_at` are the clock's UTC instant.
- No ledger posting anywhere (bill record-payment writes no journal, port note 8).
- Tests: pgtestdb through `internal/apitest`; **every test and subtest calls `t.Parallel()` first** (lint enforces it); the clock is apitest's `*testkit.Clock`. Every guard in a "Mutations" line is removed once and its test watched going red, then restored. Each package stays well under `./dev test`'s 60 s timeout.
- Commits name their paths (never `git add -A`; `AGENTS.md` and the three Nov 5 docs are Codex's). Messages end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Branch `feat/go-quotes-projects-bills` from `main`, created with this plan's commit. No push or PR until the final review; merging waits for the owner.

## Go changes these (each a port note)

1. **Quotation numbers under a lock**: `NextQuotationNumber` mirrors `invoices.NextInvoiceNumber` (`pg_advisory_xact_lock` keyed `quotation-number:<year>`, ASCII-digit tails, UTC year); a duplicate number is **409** `quotation number already exists`.
2. **One invoice per quotation**: convert reads the quotation `FOR UPDATE` first (lock order: quotation row → invoice number lock), so two concurrent converts make one invoice and the second gets Python's 400 `cannot convert a INVOICED quotation to invoice`.
3. **Bill payments under a row lock**: record-payment and void read the bill `FOR UPDATE` (Python lost an update when two payments raced).
4. **500s become client errors**: unknown `customer_id`/`project_id`/`vendor_id`/`item_id`/`expense_account_id` → 422 at the field; strings longer than their column → 422 (`maxLength` per the DDL); projects map only `projects_code_key` (and its index) to the code sentence — an unknown customer is 422 and a NOT NULL `null` is 422 instead of `project code already exists`; deleting a project referenced only by a quotation is the same 400 with quotations counted (`… referenced by N invoice(s)/bill(s)` keeps Python's wording; the count includes quotations — note); over-long upper-cased codes/currencies are 422.

Kept (port notes): bill `status` freely writable through PATCH (it is the only way to open a bill), EXPIRED never set and `valid_until` never enforced, permissive quotation void, convert from DRAFT/SENT, payment terms overriding `due_in_days` on convert, mixed-currency project sums, currency case per feature, project/customer mismatch allowed, no limits on projects/bills lists, required-but-empty bodies on accept/convert/bill void.

## Review Focus

- Two "Convert to invoice" clicks at once must create one invoice, never two, and the quotation must point at it: Task 6 `TestConvertConcurrentMakesOneInvoice`.
- A quotation with payment terms `Net 30` converted with `due_in_days: 7` must take 30 days (terms win, as Python); with no terms it takes 7: Task 6 `TestConvertTermsOverrideDueInDays`.
- A bill line of `0.5 × 0.0001` must be `0.0000` (half-even on `0.00005`), and the subtotal the sum of rounded lines — not the invoice rule: Task 7 `TestBillLinesRoundHalfEven`.
- Paying a bill exactly its balance must make it PAID with balance `0.0000`, and a second payment must then be refused, even when the two are sent at once: Task 8 `TestRecordPaymentExactBalancePays` and `TestRecordPaymentConcurrentNeverOverpays`.
- Deleting a project used only by a quotation must be a clean refusal, never a 500: Task 2 `TestDeleteProjectReferencedByQuotationIs400`.

---

### Task 1: Plan 5 carry-overs and an invoice list loader

**Files:**
- Modify: `backend/internal/invoices/core.go` (PATCH takes the template lock), `core.go` `MapWriteError` doc, a new exported loader in `store.go`, `backend/PORT-NOTES.md` (notes 35 and 43)
- Test: `backend/internal/invoices/core_test.go`, `store_test.go`

**Interfaces:**
- Produces: `func ListBy(ctx context.Context, q *invoicesdb.Queries, filter ListFilter) ([]InvoiceOut, error)` with `type ListFilter struct { ProjectID *uuid.UUID; ExcludeTemplates bool }` (or an equivalent small exported loader: lines batch-loaded, `expected_cash` and order identical to the list route) — used by Task 3.

- [ ] **Step 1: Failing tests** — `TestPatchTemplateViaInvoiceRouteWaitsForTrigger` (a `PATCH /invoices/{template_id}` holding its transaction makes a concurrent trigger wait, as the template PUT test does — lock order template → row → number); `TestListByProject` (templates excluded, order `created_at DESC, invoice_id DESC`, lines present).
- [ ] **Step 2: Implement** — `updateInvoice` takes `LockRecurringTemplate(id)` before `GetInvoiceForUpdate` (harmless for ordinary invoices); correct the `MapWriteError` doc; qualify PORT-NOTES 35 and 43 (the generic PATCH now waits too).
- [ ] **Step 3: Run, mutate, commit** — Mutation: drop the new lock (the waits-for test red). Commit `fix(go): invoice PATCH takes the template lock; invoice list loader`.

---

### Task 2: Projects CRUD

**Files:**
- Create: `backend/internal/projects/api.go`, `routes.go`, `queries.sql`, generated `projectsdb/`
- Modify: `backend/sqlc.yaml`, `backend/cmd/api/main.go`
- Test: `backend/internal/projects/projects_test.go`

Routes (inventory §1(b)): `POST /api/v1/projects` (201), `GET /api/v1/projects` (`status` repeatable, `customer_id`, `active` filters per inventory), `GET /api/v1/projects/{project_id}`, `PATCH …`, `DELETE …` (204). Normalisation: create `code.strip().upper()`, `name.strip()`, `currency.upper()`; PATCH `code` and `currency` only (name not stripped, as Python). Validation runs on the normalised value (Go change 4). Delete counts invoices (any status, templates included), bills and quotations referencing the project, under a row lock on the project, and refuses with Python's sentence.

- [ ] **Step 1: Failing tests** — ports `TestCreateProjectNormalizesCodeAndCurrency`, `TestCreateProjectDuplicateCodeRaisesAndLeavesOneRow` (400 `project code 'DUP-1' already exists`, one row), `TestUpdateProjectEditsAndNormalizes`, `TestUpdateProjectNotFound` (404 over REST); new `TestPatchDuplicateCodeIs400` (`project code already exists`), `TestCreateUnknownCustomerIs422`, `TestPatchNullNameIs422`, `TestBlankCodeIs422` (`"   "`), `TestListFiltersAndOrder`, `TestDeleteUnreferencedProject204`, `TestDeleteProjectReferencedByInvoicesAndBills` (exact count sentence), `TestDeleteProjectReferencedByQuotationIs400`, `TestProjectRoutesRequireAdmin` (401 and 403).
- [ ] **Step 2: Implement, run, mutate, commit** — Mutations: map every constraint to the code sentence (unknown-customer test red); stop counting quotations (quotation-reference test red with 500). Commit `feat(go): project routes`.

---

### Task 3: Project summary and project invoices

**Files:**
- Create: `backend/internal/projects/summary.go`
- Test: `backend/internal/projects/summary_test.go`

Routes: `GET /api/v1/projects/{project_id}/summary` (inventory §1(b) and §2 "Projects": `invoiced_total` = Σ amount over OPEN/SENT/PARTIAL/PAID invoices, `outstanding_total` = Σ balance_due, `paid_total` = invoiced − outstanding, `bill_total` = Σ bills.amount over OPEN/PARTIAL/PAID, `contract_remaining` = contract_value − invoiced or null; all currencies mixed, as Python), `GET /api/v1/projects/{project_id}/invoices` (Task 1 loader; 404 `project not found`).

- [ ] **Step 1: Failing tests** — `TestSummaryTotals` (seed invoices in every status and bills; exact sums), `TestSummaryEmptyProject` (`"0.0000"` everywhere, `contract_remaining` null without a contract value), `TestSummaryMixesCurrencies` (pins the kept behaviour), `TestProjectInvoicesExcludeTemplatesAndOrder`, `TestSummaryAndInvoices404`.
- [ ] **Step 2: Implement, run, mutate, commit** — Mutation: include DRAFT in the invoiced sum (`TestSummaryTotals` red). Commit `feat(go): project summary and invoices`.

---

### Task 4: Quotations store, numbering and CRUD

**Files:**
- Create: `backend/internal/quotations/api.go`, `routes.go`, `numbering.go`, `queries.sql`, generated `quotationsdb/`
- Modify: `backend/sqlc.yaml`, `backend/cmd/api/main.go`
- Test: `backend/internal/quotations/quotations_test.go`, `numbering_test.go`

**Interfaces:**
- Produces: `QuotationOut` (inventory §0 field order), `func Register(api huma.API, pool *pgxpool.Pool, clock platform.Clock)`, `func NextQuotationNumber(ctx context.Context, tx pgx.Tx, today time.Time) (string, error)`.

Routes (inventory §1(a)): `POST /api/v1/quotations` (201; empty lines → 400 `quotation must have at least one line item`; number generated when absent or `""`; `issue_date` default today; `valid_until` default issue + 30 days), `GET /api/v1/quotations` (filters per inventory, `limit`), `GET …/{quotation_id}` (404), `PATCH …` (DRAFT or SENT only; reprices from the current lines and discount even when only the discount changed), `DELETE …` (DRAFT only, 204). Totals: `invoices.ComputeTotals(lines, dt, dv, currency, "NONE", nil, false)`; line amounts `invoices.LineAmount`; positions as sent (default 0).

- [ ] **Step 1: Failing tests** — `TestCreateQuotationTotals` (discount PERCENT and AMOUNT, clamp at 0, half-up), `TestCreateDefaultsNumberIssueValidUntil` (`Q<year>0001`, today, +30 days), `TestCreateRequiresLineItems`, `TestCreateDuplicateNumberIs409`, `TestCreateUnknownCustomerIs422`, `TestNextQuotationNumberConcurrent` (20 transactions, consecutive), `TestNextQuotationNumberIgnoresInvoiceNumbers`, `TestListFiltersOrderLimit`, `TestGetQuotation404`, `TestPatchRepricesOnDiscountOnly`, `TestPatchNullNumberClears` (kept, oddity 17), `TestPatchEmptyLinesAccepted` (kept, oddity 18), `TestPatchNonEditableIs400`, `TestPatchUnknownIs400`, `TestDeleteDraft204`, `TestDeleteNonDraftIs400`, `TestQuotationRoutesRequireAdmin`.
- [ ] **Step 2: Implement, run, mutate, commit** — Mutations: drop the number lock (concurrent test red at `-count=5`); recompute only when lines change (discount-only test red). Commit `feat(go): quotation routes and numbering`.

---

### Task 5: Quotation lifecycle

**Files:**
- Create: `backend/internal/quotations/lifecycle.go`
- Test: `backend/internal/quotations/lifecycle_test.go`

Routes: `POST …/{quotation_id}/mark-sent` (DRAFT/SENT → SENT, `last_sent_at` = clock), `/accept` (body `{accepted_by?}` required; DRAFT/SENT → ACCEPTED, `accepted_at` = clock), `/decline` (DRAFT/SENT → DECLINED), `/void` (any but INVOICED → VOID; re-void allowed). Each reads the row `FOR UPDATE`; refusals per inventory §2 "Quote status machine", verbatim.

- [ ] **Step 1: Failing tests** — one per transition and refusal (`TestMarkSentFromDraftAndSent`, `TestMarkSentFromAcceptedIs400`, `TestAcceptSetsAcceptedAtAndBy`, `TestAcceptRequiresBody`, `TestDeclineFromSent`, `TestVoidFromEveryStatusButInvoiced`, `TestVoidInvoicedIs400`, `TestLifecycleUnknownIs400`), `TestLifecycleTimestampsAreUTC`.
- [ ] **Step 2: Implement, run, mutate, commit** — Mutation: allow void from INVOICED (`TestVoidInvoicedIs400` red). Commit `feat(go): quotation lifecycle`.

---

### Task 6: Convert to invoice

**Files:**
- Create: `backend/internal/quotations/convert.go`
- Test: `backend/internal/quotations/convert_test.go`

Route: `POST /api/v1/quotations/{quotation_id}/convert-to-invoice` (body `{issue_date?, due_in_days=14}` required) → **201** `invoices.InvoiceOut`. One transaction (inventory §2 "convert_to_invoice", in order): quotation `FOR UPDATE` → 400 `quotation <id> not found` / `cannot convert a <STATUS> quotation to invoice` (INVOICED, VOID, DECLINED) / `quotation has no line items to convert` → today = `issue_date` or clock date → days = `invoices.PaymentTermsDays(terms)` when terms are non-empty (overflow → 400 `due date is out of range`), else `due_in_days` → `invoices.AddDays` (out of range → same 400) → `invoices.ResolveWHT` with the customer default only → `withholding.Compute` on the stored quote amount → `invoices.NextInvoiceNumber` → insert the invoice (field mapping table in the inventory; `invoice_type` MILESTONE, DRAFT, `source_quote_id`) and copy lines verbatim (stored amounts) through `invoices.MapWriteError` → quotation INVOICED with `converted_invoice_id` → respond with `invoices.Get`.

- [ ] **Step 1: Failing tests** — port `TestQuoteConversionAppliesCustomerDefault` (10,204,082 / 204,082 / 10,000,000); new `TestConvertFieldMapping` (every mapped field), `TestConvertCopiesLinesVerbatim`, `TestConvertTermsOverrideDueInDays`, `TestConvertFromDraftSentAcceptedExpired`, `TestConvertRefusedStatuses`, `TestConvertTwiceIs400`, `TestConvertConcurrentMakesOneInvoice` (two at once, `-count=5` when mutating), `TestConvertedInvoiceCannotBeDeleted` (409 from plan 5), `TestConvertDueDateOutOfRangeIs400`.
- [ ] **Step 2: Implement, run, mutate, commit** — Mutations: drop the `FOR UPDATE` (concurrent test red); ignore payment terms (terms test red). Commit `feat(go): quotation conversion to invoice`.

---

### Task 7: Bills CRUD

**Files:**
- Create: `backend/internal/bills/api.go`, `routes.go`, `totals.go`, `queries.sql`, generated `billsdb/`
- Modify: `backend/sqlc.yaml`, `backend/cmd/api/main.go`
- Test: `backend/internal/bills/bills_test.go`, `totals_test.go`

Routes (inventory §1(c)): `POST /api/v1/bills` (201; lines optional; currency upper-cased; DRAFT; `amount = balance_due`), `GET /api/v1/bills` (filters per inventory), `GET …/{bill_id}` (404), `PATCH …` (only while DRAFT or OPEN, else 400 `bill is <STATUS> and cannot be edited`; `status` writable to any of the five — kept; totals recomputed only when lines change, balance re-baselined only while DRAFT — kept, oddity 6). Totals per inventory §2 "Bills": line `RoundHalfEven4(qty × price)`, subtotal Σ rounded lines, PERCENT/AMOUNT on the unrounded subtotal, clamp at 0, `RoundHalfEven4`; position `ln.position or idx`.

- [ ] **Step 1: Failing tests** — `TestBillLinesRoundHalfEven`, `TestBillSubtotalSumsRoundedLines`, `TestBillPercentDiscount`, `TestBillPositionZeroBecomesIndex`, `TestCreateBillWithoutLines`, `TestCreateUppercasesCurrency`, `TestCreateUnknownVendorIs422`, `TestCreateUnknownExpenseAccountIs422`, `TestListFiltersAndOrder`, `TestGetBill404`, `TestPatchStatusOpensBill` (kept), `TestPatchPaidIs400`, `TestPatchOpenBillLinesKeepsBalance` (kept), `TestPatchNullVendorIs422`, `TestBillRoutesRequireAdmin`.
- [ ] **Step 2: Implement, run, mutate, commit** — Mutations: round half-up (`TestBillLinesRoundHalfEven` red); round the raw sum (subtotal test red). Commit `feat(go): bill routes`.

---

### Task 8: Bill record-payment and void

**Files:**
- Create: `backend/internal/bills/payments.go`
- Test: `backend/internal/bills/payments_test.go`

Routes: `POST …/{bill_id}/record-payment` (**200**; body per inventory; refusals `cannot record payment on <STATUS> bill` for DRAFT/VOID/PAID, `amount <as sent> exceeds balance due <balance at 4 places>`; exact payment → PAID, balance 0; otherwise PARTIAL; payment fields other than amount discarded; no journal), `POST …/{bill_id}/void` (body a required JSON object; `reason` any JSON value, missing/falsy → `no reason given`; refused for PAID/VOID; VOID, balance 0, notes += `\n[voided] <reason>`). Both read the bill `FOR UPDATE` (Go change 3). "As sent" means the decimal's text as received (`600`, `600.5`, `600.00`): keep the literal.

- [ ] **Step 1: Failing tests** — `TestRecordPaymentPartial`, `TestRecordPaymentExactBalancePays`, `TestRecordPaymentOverpaymentIs400` (sentence keeps `600.00` as sent), `TestRecordPaymentOnDraftIs400`, `TestRecordPaymentConcurrentNeverOverpays`, `TestRecordPaymentWritesNoJournal`, `TestVoidAppendsNote` (null notes → leading newline), `TestVoidReasonNonString` (number reason formatted as Python would), `TestVoidPaidIs400`, `TestVoidRequiresBody`, `TestBillPaymentRoutes404`.
- [ ] **Step 2: Implement, run, mutate, commit** — Mutations: drop the row lock (concurrent test red at `-count=5`); allow `amount == balance` to stay PARTIAL (exact test red). Commit `feat(go): bill payments and void`.

---

### Task 9: Inventories, port notes, checks

**Files:** `backend/ROUTES.md`, `backend/TESTS.md`, `backend/PORT-NOTES.md`

- [ ] **Step 1** — `python backend/port/inventory.py`; mark the 23 routes `ported` (`go` = `projects`, `quotations`, `bills`); the quotation PDF and send routes get `documents plan`.
- [ ] **Step 2** — mark the 5 ported Python tests with their Go names and recorded mutations (`—` where none); the [OUT] tests keep `todo` with their plan named. Expected ported count 157.
- [ ] **Step 3** — PORT-NOTES: one entry per "Go changes these" item and per kept oddity in the inventory §5 that a person could notice (1–24, merged where they overlap), each with Python file:line (verified), the Go test that pins it, and "After the switch".
- [ ] **Step 4** — `bash scripts/check.sh` → `all checks passed`, Go test count above 602, none skipped.
- [ ] **Step 5** — Commit `docs(go): quotations, projects, bills ported; port notes` (local; the controller opens the PR after the final review).
