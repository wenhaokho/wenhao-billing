# Go backend, plan 5: invoices

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Go answers the 21 invoice routes (core CRUD and lists, lifecycle, recurring templates and trigger, usage lock, and the three customer invoice views) with Python's contract, and holds the cores of the two invoice jobs (recurring scan, usage cut-off) for the scheduler plan to wire.

**Architecture:** `internal/invoices/schedule` is the pure recurring-schedule math. `internal/invoices` holds the store (sqlc), totals, numbering, the shared `InvoiceOut`, and the routes, split by area into files. Quotations (next plan) reuse the exported helpers (`PaymentTermsDays`, `ResolveWHT`, `NextInvoiceNumber`, `ComputeTotals`). Email digests, PDFs, sending, and payments are later plans: the trigger calls a `DraftNotifier` interface whose real sender comes with the email plan.

**Tech Stack:** Go 1.27, Huma v2.40 on chi v5, pgx v5, sqlc 1.31.1, shopspring/decimal, gokit v0.2.0, Postgres 16, Docker Compose (through `backend/dev`).

**Spec:** `docs/superpowers/specs/2026-11-08-go-backend-design.md` ("Behaviour that must be carried exactly": errors, lists, decimals, rounding, PATCH, repeated query params, route order, document numbers; "Tests").

**Contract inventory (read the section a task names):** `docs/superpowers/plans/2026-11-09-go-backend-05-invoices-inventory.md` — every route's request and response fields, every error sentence with its status, the service rules with Python file:line, the JSONB shapes, the 100 in-scope Python tests, and 24 oddities. It is the reference for exact values; this plan says which to keep and which to change.

## Global Constraints

- `backend-python/` is frozen: never edited. Odd behaviour is kept and goes to `backend/PORT-NOTES.md`, except where this plan says "Go changes this" (each such change also gets a note).
- **Statuses and sentences are Python's.** Every `InvoicingError` is a **400** with Python's exact `detail`, including "not found" on write routes (`invoice <id> not found`); the GET routes and the template GET/PATCH answer **404** (`invoice not found`, `recurring template not found`, `customer not found`). Withholding normalisation errors are **400** on invoice routes (not 422 as on customers). `code` is a stable snake_case per sentence. Em dashes (U+2014) in sentences are kept.
- Lists are bare JSON arrays, never `null`. `ORDER BY` is Python's and ends with the primary key (`created_at DESC, invoice_id DESC`, etc.). `GET /invoices` keeps `limit` (default 200, max 500; Go also refuses < 1 with a 422 — note). Repeated `?status=` values are a list.
- **Route order:** the literal paths (`/invoices/milestone`, `/recurring-template`, `/recurring-templates`, `/recurring-templates/rows`, `/awaiting-finalization`, `/customers/balances`) must never be taken by `{invoice_id}`/`{customer_id}`; a test per literal path.
- Decimals: `platform.Dec4` for every numeric(19,4) and for `wht_rate` (numeric(7,4)); read with `.Decimal()`, build with `platform.NewDec4`. `expected_cash` is rendered at 4 places (Python prints `"0"` when exactly zero: an allowed comparison difference, note).
- **Rounding:** line amount `RoundHalfUp4(qty × price)`; subtotal `RoundHalfUp4(Σ raw qty × price)` (not the sum of rounded lines); discount results `RoundHalfUp4`; withholding through `withholding.Compute` (currency precision, half-up). This is `invoicing.py:40-73`, `quoting.py` uses the same; never `RoundHalfEven4` here.
- PATCH is Python's `exclude_unset`: omitted keeps, explicit `null` writes null for nullable columns; `null` for a NOT NULL column (`currency`, `invoice_type`) is a 422 at the field (`platform.NotNull`, as plan 3). `line_items: null` and omitted both keep the lines.
- **Today:** every "today" (`issue_date` default, number year, trigger dates, END_NOW, 12-month window, overdue cut-off, scanner) is the UTC calendar date of `platform.Clock` — Python's `date.today()` in a UTC container (port note 5 stands).
- `billing_cycle_ref` is returned as the stored JSON object (`json.RawMessage`, so Postgres's key order is kept); requests send `schedule` on templates.
- No ledger posting anywhere in this plan.
- Tests: pgtestdb through `internal/apitest`; **every test and subtest calls `t.Parallel()` first** (lint enforces `paralleltest`/`tparallel`; an exception needs `//nolint:paralleltest // <reason>`); time through `apitest`'s `*testkit.Clock`, never the wall clock. Each package stays well under `./dev test`'s 60 s timeout. Every guard named in a "Mutations" line is removed once and its test watched going red, then restored.
- Commits name their paths (never `git add -A`; `AGENTS.md` and the three Nov 5 docs are Codex's). Messages end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Branch `feat/go-invoices` from `main`, created with this plan's commit. No push or PR until the final review; merging waits for the owner.

## Go changes these (best-practice changes, each a port note)

1. **Invoice numbers under a lock.** `NextInvoiceNumber` takes `pg_advisory_xact_lock` keyed on `invoice-number:<year>` before scanning (spec, "Document numbers"); a user-supplied duplicate number is a **409** `invoice number already exists` instead of a 500. The scan only counts ASCII-digit tails.
2. **One invoice per cycle.** The trigger accepts only `YYYY-MM-DD` cycle keys (Python also took `20260301` and stored the raw string, so two spellings made two invoices), and takes `pg_advisory_xact_lock` keyed on the template id before `find_cycle_invoice`, so a manual trigger and the scanner cannot both create the cycle.
3. **500s become client errors:** an unknown `customer_id`/`project_id`/`item_id` (FK) is a 422 naming the field; strings longer than their column are 422 (`maxLength`: `invoice_type` 20, `payment_terms` 30, `invoice_number` 50, `po_so_number` 100, `currency` exactly 3); deleting a DRAFT that a payment or quotation references is a **409** `invoice is referenced by a payment or quotation`.

Everything else in the inventory's "Oddities" is **kept** (templates in customer views, permissive void, PATCH editing templates, issue date = today on generated children, interval-as-months coverage, 400-for-not-found, etc.) and listed in PORT-NOTES by Task 9.

## Review Focus

- Three lines of quantity `0.5` at `0.0001` must give line amounts `0.0001` each but a subtotal of `0.0002` (Python rounds the raw sum `0.00015` once, half-up), not `0.0003`: Task 2 `TestSubtotalRoundsRawSum`.
- Two invoices created at the same moment without numbers must get consecutive numbers, never a 500: Task 2 `TestNextInvoiceNumberConcurrent`.
- A PATCH that sends only `notes` must not change `invoice_type`, lines, or totals: Task 3 `TestPatchWithoutInvoiceTypeKeepsIt` (ported) and `TestPatchNotesOnlyKeepsTotals`.
- A recurring template triggered by hand while the 02:00 scan runs must produce exactly one child: Task 6 `TestTriggerConcurrentWithScanOneChild`.
- A monthly template starting on 31 January must generate on 28 February and 31 March, and END_NOW on a template whose next cycle is overdue must end on the day before that cycle: Task 1 `TestMonthlyEndOfMonthStartClampsShortMonths` (ported) and Task 5 `TestEndNowEndsTemplateWithOverdueCycle` (ported).

---

### Task 1: Recurring schedule math (pure)

**Files:**
- Create: `backend/internal/invoices/schedule/schedule.go`
- Test: `backend/internal/invoices/schedule/schedule_test.go`, `scan_test.go`

**Interfaces:**
- Produces:
```go
var ErrSchedule = errors.New("schedule") // every parse error wraps it; Error() is Python's sentence
type Schedule struct { Frequency string; Interval int; Start time.Time; EndMode string; EndDate *time.Time; EndAfterCycles *int }
type Cycle struct{ Index int; Start time.Time }
func Parse(cfg map[string]any) (Schedule, error)            // recurring_schedule.py:65-101, messages verbatim
func (s Schedule) Current(today time.Time) (Cycle, bool)     // :169-177
func (s Schedule) IsCycleStart(day time.Time) bool           // :180-191 (ignores the end condition)
func (s Schedule) NextAfter(after time.Time) (Cycle, bool)    // :194-204
func Key(c Cycle) string                                      // "YYYY-MM-DD"
func IsPaused(cfg map[string]any) bool                        // :211-215
func Describe(s Schedule) string                              // :242-262, wording verbatim
func CycleKeyFor(cfg map[string]any, today time.Time) (string, bool) // workers/tasks/recurring.py:29-44: paused, invalid or not due → false
```
Dates are calendar dates (`time.Time` at UTC midnight). Month arithmetic clamps the day to the target month and is always computed from `Start` (31 Jan → 28 Feb → 31 Mar). `interval` and `end_after_cycles` from JSON arrive as `float64` or `json.Number`; accept whole numbers ≥ 1 only (Python's `bool` is an int there: `true` as interval is accepted as 1 by Python — keep that, note it).

- [ ] **Step 1: Failing tests** — port all 33 `tests/unit/test_recurring_schedule.py` tests and the 8 `tests/unit/test_recurring_scanner.py` tests (inventory §4) under the same names in Go case (`TestParseMinimalMonthlySchedule` …). Add `TestParseErrorSentences` (every sentence in inventory §2 "Recurring schedule", exact text).

Run: `cd backend && ./dev test ./internal/invoices/schedule/` — Expected: FAIL.

- [ ] **Step 2: Implement, run, mutate** — Expected: PASS. Mutations: compute monthly cycles from the previous cycle instead of from `Start` (`TestMonthlyEndOfMonthStartClampsShortMonths` red); make `end_date` exclusive (`TestEndOnDateInclusiveOnTheDay` red).

- [ ] **Step 3: Commit** `feat(go): recurring schedule math`

---

### Task 2: Invoice store, totals, numbering, `InvoiceOut`

**Files:**
- Create: `backend/internal/invoices/api.go` (types), `totals.go`, `numbering.go`, `wht.go`, `store.go`, `queries.sql`, generated `invoicesdb/`
- Modify: `backend/sqlc.yaml` (entry `internal/invoices/queries.sql` → `invoicesdb`; `invoices.billing_cycle_ref` as `json.RawMessage`/nullable)
- Test: `backend/internal/invoices/totals_test.go`, `numbering_test.go`, `wht_test.go`

**Interfaces:**
- Produces (exported for this plan's routes and the quotations plan):
```go
type LineIn struct { ItemID *uuid.UUID; Description string; Quantity, UnitPrice decimal.Decimal; Position int }
type Totals struct { Subtotal, Amount decimal.Decimal; WHT withholding.Result }
func ComputeTotals(lines []LineIn, discountType *string, discountValue *decimal.Decimal, currency, whtType string, whtRate *decimal.Decimal, grossUp bool) Totals // invoicing.py:47-73
func LineAmount(qty, price decimal.Decimal) decimal.Decimal                          // RoundHalfUp4(qty×price)
func PaymentTermsDays(terms *string) int                                             // invoicing.py:145-154
func ResolveWHT(ctx context.Context, q *invoicesdb.Queries, customerID *uuid.UUID, whtType *string, rate *decimal.Decimal, grossUp *bool) (string, *decimal.Decimal, bool, error) // :85-103
func MergeWHT(storedType string, storedRate *decimal.Decimal, storedGrossUp bool, whtType platform.Optional[string], rate platform.Optional[platform.Dec4], grossUp platform.Optional[bool]) (string, *decimal.Decimal, bool, error) // :106-119; no field Set → stored values unchanged, not normalized
func NextInvoiceNumber(ctx context.Context, tx pgx.Tx, today time.Time) (string, error) // WH{YYYY}{NNNN}, advisory lock (Go change 1)
type InvoiceOut struct { … }   // inventory §0 "InvoiceOut", 33 fields in that order; LineItems []LineItemOut (never null, ORDER BY position, line_item_id); ExpectedCash platform.Dec4 last
func Get(ctx context.Context, q *invoicesdb.Queries, id uuid.UUID) (InvoiceOut, error) // pgx.ErrNoRows when missing
var ErrInvoicing = errors.New("invoicing") // every 400 refusal wraps it; Error() is Python's sentence
```
`ExpectedCash` is `withholding.ExpectedCash(balance_due, wht_amount, wht_booked)`.

- [ ] **Step 1: Failing tests**

Totals: `TestLineAmountHalfUp`, `TestSubtotalRoundsRawSum`, `TestPercentDiscount` (10% of 1,000,000 → 900,000), `TestAmountDiscountClampsAtZero`, `TestDiscountValueWithoutTypeIgnored`, `TestNoWHTKeepsFourPlaces` (USD 10.3333), `TestWHTGrossUpIDR` (10,000,000 PPH23 2% → 10,204,082 / 204,082). Terms: `TestPaymentTermsDays` (nil/"" → 14; "On Receipt", "on-receipt", "due on receipt" → 0; "Net 30", " net  45 " → 30/45; "Monthly" → 14). WHT: `TestResolveWHTPayloadWins` (explicit NONE beats the customer default), `TestResolveWHTCustomerDefault`, `TestResolveWHTNoCustomer`, `TestMergeWHTUntouchedKeepsStoredWithoutNormalizing`, `TestMergeWHTTypeSwitchTakesNewDefault`, `TestMergeWHTGrossUpOnlyKeepsTypeAndRate`. Numbering: `TestNextInvoiceNumberFirstOfYear` (`WH20260001`), `TestNextInvoiceNumberSkipsNonDigitTails`, `TestNextInvoiceNumberGrowsPastFourDigits` (`WH202610000`), `TestNextInvoiceNumberUsesTodaysYear` (issue dates don't matter), `TestNextInvoiceNumberConcurrent` (20 goroutines, each in its own tx inserting an invoice with the next number: 20 distinct consecutive numbers, no error). `TestInvoiceOutFieldOrder` (JSON keys of a seeded invoice equal inventory §0's list, in order; `line_items` `[]`, `expected_cash` last).

Run: `./dev test ./internal/invoices/` — Expected: FAIL.

- [ ] **Step 2: Implement, run, mutate** — Expected: PASS. Mutations: round each line before summing the subtotal (`TestSubtotalRoundsRawSum` red); drop the advisory lock (`TestNextInvoiceNumberConcurrent` red at `-count=5`); re-normalize in `MergeWHT` when no WHT field was sent (`TestMergeWHTUntouchedKeepsStoredWithoutNormalizing` red).

- [ ] **Step 3: Commit** `feat(go): invoice store, totals, numbering`

---

### Task 3: Core routes

**Files:**
- Create: `backend/internal/invoices/routes.go` (`Register`), `core.go`
- Modify: `backend/cmd/api/main.go` (`register`)
- Test: `backend/internal/invoices/core_test.go`

**Interfaces:**
- Produces: `func Register(api huma.API, pool *pgxpool.Pool, clock platform.Clock, notify DraftNotifier)` (Task 6 defines `DraftNotifier`; until then this task registers with a nil-safe default).

Routes (inventory §1(a)): `POST /api/v1/invoices` (201), `POST /api/v1/invoices/milestone` (201), `GET /api/v1/invoices` (`status` repeatable, `customer_id`, `invoice_type`, `limit`), `GET /api/v1/invoices/{invoice_id}` (404; templates included), `PATCH /api/v1/invoices/{invoice_id}`, `GET /api/v1/invoices/awaiting-finalization` (no limit). Create and PATCH follow `invoicing.py:228-341` step by step (inventory §2 "create_invoice", "update_invoice"), in one transaction; PATCH replaces lines only when `line_items` is a list. Milestone follows `:192-225` (no number, no lines, `billing_cycle_ref` `{"milestone_ref": …}`).

- [ ] **Step 1: Failing tests**

Ports (inventory §4): from `test_invoice_withholding.py` — `TestCreateInvoiceGrossUpPph23`, `TestCreateInvoiceInheritsCustomerDefault`, `TestExplicitNoneOverridesCustomerDefault`, `TestDiscountThenWithholding`, `TestUpdateDraftRecomputesWithholding`, `TestUpdateTypeSwitchResetsRate`, `TestCreateInvoiceRate100Returns400`, `TestMilestoneInvoiceInheritsCustomerDefault`; from `test_invoice_internal_note.py` — `TestInternalNoteRoundTripsThroughApi`, `TestInternalNoteCanBeCleared`; `TestPatchWithoutInvoiceTypeKeepsIt` (RECURRING and USAGE subtests).
New: `TestCreateRequiresLineItems` (400 sentence), `TestCreateDefaultsIssueDueAndNumber` (clock date, terms days, `WH<year>0001`), `TestCreateDuplicateNumberIs409`, `TestCreateUnknownCustomerIs422`, `TestCreateOverlongPaymentTermsIs422`, `TestListFiltersAndOrder` (two statuses repeated, customer, type, templates excluded, `created_at DESC`), `TestListLimitBounds` (0 and 501 → 422), `TestGetInvoice404`, `TestPatchNonDraftIs400`, `TestPatchUnknownIs400` (`invoice <id> not found`), `TestPatchNullCurrencyIs422`, `TestPatchNotesOnlyKeepsTotals`, `TestPatchLinesNullKeepsLines`, `TestPatchIssueDateKeepsExistingDueDate`, `TestAwaitingFinalizationListsDraftsOnly`, `TestMilestoneCreate`, `TestLiteralPathsNotSwallowed` (each literal path in Global Constraints reaches its own handler), `TestInvoiceRoutesRequireAdmin`.

- [ ] **Step 2: Implement, run, mutate** — Expected: PASS. Mutations: let `invoice_type` default overwrite on PATCH (`TestPatchWithoutInvoiceTypeKeepsIt` red); drop the `is_template = false` filter on the list (`TestListFiltersAndOrder` red).

- [ ] **Step 3: Commit** `feat(go): invoice create, read, list, patch`

---

### Task 4: Lifecycle

**Files:**
- Create: `backend/internal/invoices/lifecycle.go`
- Test: `backend/internal/invoices/lifecycle_test.go`

Routes (inventory §1(b)): `POST …/{invoice_id}/finalize`, `POST …/{invoice_id}/void` (body `{reason}` required, unused), `DELETE …/{invoice_id}` (204), `POST …/{invoice_id}/mark-paid`. Refusals in Python's order with Python's sentences (inventory §2 "Status machine"); each runs on the row read `FOR UPDATE`.

- [ ] **Step 1: Failing tests**

Ports from `test_zero_value_invoice.py` (8): `TestZeroValueDraftHasZeroTotals`, `TestFinalizeZeroValueInvoiceBecomesPaid`, `TestFinalizeNonzeroInvoiceBecomesOpen`, `TestSettleZeroValueInvoiceService`, `TestSettleRejectsNonzeroBalance` (sentence with `100.0000` and the em dash), `TestSettleRejectsDraft`, `TestMarkPaidEndpointSettlesZeroValue`, `TestMarkPaidEndpointRejectsNonzeroBalance`.
New: `TestFinalizeNonDraftIs400`, `TestFinalizeTemplateIs400`, `TestFinalizeUnknownIs400`, `TestVoidFromEachNonPaidStatus` (DRAFT, OPEN, SENT, PARTIAL, VOID, template → VOID, balance 0), `TestVoidPaidIs400` (em dash), `TestVoidRequiresReason` (422), `TestDeleteDraft204CascadesLines`, `TestDeleteNonDraftIs400`, `TestDeleteReferencedByQuotationIs409` (insert a quotation row with `converted_invoice_id`), `TestDeleteReferencedByPaymentIs409`.

- [ ] **Step 2: Implement, run, mutate** — Expected: PASS. Mutations: allow void from PAID (`TestVoidPaidIs400` red); drop the template check on finalize (`TestFinalizeTemplateIs400` red).

- [ ] **Step 3: Commit** `feat(go): invoice finalize, void, delete, mark-paid`

---

### Task 5: Recurring templates

**Files:**
- Create: `backend/internal/invoices/templates.go`
- Test: `backend/internal/invoices/templates_test.go`

Routes (inventory §1(c), all but trigger): `POST /api/v1/invoices/recurring-template` (201), `GET …/recurring-templates`, `GET …/recurring-templates/rows`, `GET …/recurring-templates/{template_id}` (404), `PUT …/recurring-templates/{template_id}` (409 `recurring template has ended and is view-only` first, then service 400s), `PATCH …/recurring-templates/{template_id}` (`{action}` PAUSE/RESUME/END_NOW, upper-cased; 404; 409 on PAUSE/RESUME when ended; 400 `unknown action: <as sent>`). Schedule stored as `_schedule_to_json` (inventory §3), `paused` preserved on PUT. Rows: inventory §1(c) "rows" algorithm (one query for all templates' children is fine; same results: previous = latest-`issue_date` non-VOID child's `cycle_key`, falling back to its `issue_date`; next = `NextAfter(previous or start − 1 day)`; status ENDED / PAUSED / ACTIVE / MISCONFIGURED with `Schedule not configured`). END_NOW date = `min(today − 1, next_run − 1)`, yesterday when invalid.

- [ ] **Step 1: Failing tests**

Ports from `test_recurring_scheduling.py`: `TestCreateTemplateStoresScheduleInBillingCycleRef`, `TestCreateTemplatePersistsEndCondition`, `TestCreateTemplateRejectsInconsistentEndCondition`, `TestCycleKeyNoneBeforeStart`, `TestCycleKeyIsIsoCycleStartMonthly`, `TestCycleKeyWeeklyIntervalTwo`, `TestCycleKeyNonePastEndDate`, `TestCycleKeyNonePastAfterN`, `TestCycleKeyReturnsNoneIfConfigMissing`, `TestRowsPreviousIsCycleDateNotGenerationDate`, `TestRowsNextIsPreviousCyclePlusOneInterval`, `TestRowsExcludesVoidChildren`, `TestEndNowEndsTemplateWithOverdueCycle`, `TestEndNowEndsTemplateThatNeverGenerated`, `TestEndNowWithFutureNextCycleEndsYesterday`, `TestEndNowOnPausedTemplateShowsEndedNotPaused`, `TestRowsEndedScheduleOutranksPausedFlag`, `TestEndedTemplateRejectsEdit`, `TestEndedTemplateRejectsPauseAndResume`, `TestActiveTemplateStillAcceptsEdit`; from `test_invoice_withholding.py`: `TestUpdateTemplateGrossUpOnlyKeepsTypeAndRate`. (Rows tests that need children insert them directly or through Task 6's service once it exists; this task inserts rows directly.)
New: `TestTemplateRequiresLineItems`, `TestPutMissingTemplateIs400`, `TestGetNonTemplateIs404`, `TestPatchUnknownActionIs400`, `TestRowsMisconfigured`, `TestRowsDescribe` (`Every 3 months on the 15th`), `TestPutPreservesPaused`.

- [ ] **Step 2: Implement, run, mutate** — Expected: PASS. Mutations: drop the ended check on PUT (`TestEndedTemplateRejectsEdit` red); take "previous" from the newest `created_at` instead of `issue_date` (`TestRowsPreviousIsCycleDateNotGenerationDate` red).

- [ ] **Step 3: Commit** `feat(go): recurring templates`

---

### Task 6: Trigger, draft notifier, recurring scan core

**Files:**
- Create: `backend/internal/invoices/trigger.go`, `scan.go`, `notify.go`
- Test: `backend/internal/invoices/trigger_test.go`, `scan_test.go`

**Interfaces:**
- Produces:
```go
type DraftSummary struct { InvoiceID uuid.UUID; InvoiceNumber string; CustomerName *string; Currency string; Amount decimal.Decimal; CycleDate string }
type DraftNotifier interface { NotifyNewDrafts(ctx context.Context, drafts []DraftSummary) } // best effort; never fails the caller
type LogNotifier struct{ Logger *slog.Logger }                                        // default until the email plan
func Trigger(ctx context.Context, tx pgx.Tx, templateID uuid.UUID, cycleKey string, today time.Time) (InvoiceOut, bool /*created*/, error)
func Scan(ctx context.Context, pool *pgxpool.Pool, today time.Time, notify DraftNotifier) (generated int, err error) // workers/tasks/recurring.py:47-72; the scheduler plan wires it
```
Route `POST /api/v1/invoices/recurring/{template_id}/trigger` `{cycle_key}` → 200 `InvoiceOut` (new or existing). Rules (inventory §2 "trigger_recurring_cycle", in order): template exists (400 `template invoice <id> not found`), type RECURRING (400 `invoice <id> is not RECURRING`), schedule parses (400 `template <id> has an invalid schedule: <err>`), cycle key is `YYYY-MM-DD` (400 `cycle_key must be an ISO date (YYYY-MM-DD), got '<key>'`, Python's repr quoting), is a cycle start (400 `cycle_key '<key>' is not a valid cycle start for this schedule`); then the template advisory lock (Go change 2); then `find_cycle_invoice` → return it unchanged; else create the child (copies, `WH` number, `issue_date` today, `due_date` today + terms, lines verbatim), then hosting coverage when any line's item `is_hosting` (`coverage_start` cycle start, `coverage_end` = start + `interval` months − 1 day, `\nCoverage: <start> to <end>` appended to every line lacking it). The route notifies only when a child was created, after commit. `Scan` uses `schedule.CycleKeyFor` per template (no order needed; process by `created_at, invoice_id`), triggers each in its own transaction (Python rolled back the whole run on one failure — Go isolates them and logs; note), counts every returned invoice (as Python), notifies once with the new drafts.

- [ ] **Step 1: Failing tests**

Ports: `TestTriggerRejectsYearMonthCycleKey`, `TestTriggerRejectsOffCycleDate`, `TestTriggerAcceptsValidCycleStart`, `TestManualTriggerNotifiesUsersOnce` (fake notifier: first call one summary, replay none), `TestFindCycleInvoiceNoneBeforeTriggerThenFound`, `TestRecurringCycleUsesTemplateSnapshot`, `TestTemplateInternalNoteCopiesToGeneratedChild`, `TestTriggerRecurringCycleStampsCoverageOnHostingInvoice` (monthly interval 3 from 2026-06-01 → 2026-06-01..2026-08-31; needs an items row with `is_hosting` and a target).
New: `TestTriggerRejectsCompactCycleKey` (`20260301` → 400; Go change 2), `TestTriggerReplayReturnsSameInvoice`, `TestTriggerChildDatesAreToday`, `TestTriggerConcurrentWithScanOneChild` (trigger and `Scan` at once, `-count=5` when mutating: one child), `TestScanGeneratesDueTemplatesAndSkipsPausedEnded`, `TestScanIsolatesAFailingTemplate`, `TestScanNotifiesOnceWithNewDraftsOnly`, `TestTriggerUnknownTemplateIs400`, `TestTriggerOnChildInvoiceIs400` (`… has an invalid schedule: invalid frequency: None`).

- [ ] **Step 2: Implement, run, mutate** — Expected: PASS. Mutations: drop the template advisory lock (`TestTriggerConcurrentWithScanOneChild` red); notify on replays (`TestManualTriggerNotifiesUsersOnce` red); accept compact keys through a lenient date parser (`TestTriggerRejectsCompactCycleKey` red).

- [ ] **Step 3: Commit** `feat(go): recurring trigger, draft notifier seam, scan core`

---

### Task 7: Usage lock and cut-off core

**Files:**
- Create: `backend/internal/invoices/usage.go`
- Test: `backend/internal/invoices/usage_test.go`

**Interfaces:**
- Produces: route `POST /api/v1/invoices/usage/{invoice_id}/lock` `{accrued_amount Dec4 ≥ 0, cutoff_date}` → 200 `InvoiceOut`; `type AccrualSource func(ctx context.Context, invoiceID uuid.UUID) (decimal.Decimal, error)`; `func CutoffScan(ctx context.Context, pool *pgxpool.Pool, today time.Time, accrued AccrualSource) (int, error)` (`workers/tasks/usage_lock.py:27-52`; the scheduler plan wires it with a source that returns an error, as Python's stub raises).

Rules (inventory §2 "lock_usage_invoice"): 400 `invoice <id> not found` / `invoice <id> is not USAGE` / `usage invoice already locked (status=<status>)`; withholding recomputed from the stored snapshot; `amount = balance_due = total`; `cfg` gains `cutoff_date` and `locked: true`; status stays DRAFT; a re-lock while DRAFT is allowed (as Python). Cut-off: yesterday = today − 1 day; USAGE DRAFT rows whose `cfg.locked` is unset and `int(cfg.cut_off_day)` equals yesterday's day of month; an accrual error stops the run without changes (Python's whole-run rollback — keep).

- [ ] **Step 1: Failing tests** — port `TestUsageLockRecomputesFromSnapshot` (10,204,082 / 204,082 / 10,000,000); new `TestLockNonUsageIs400`, `TestLockNonDraftIs400`, `TestLockSetsCfg`, `TestCutoffScanLocksMatchingDay`, `TestCutoffScanSkipsLockedAndOtherDays`, `TestCutoffScanAccrualErrorChangesNothing`.

- [ ] **Step 2: Implement, run, mutate, commit** — Mutation: lock non-DRAFT (`TestLockNonDraftIs400` red). Commit `feat(go): usage lock and cut-off core`.

---

### Task 8: Customer invoice views

**Files:**
- Create: `backend/internal/invoices/customers.go`
- Test: `backend/internal/invoices/customers_test.go`

Routes (inventory §1(a) customer routes), registered by `invoices.Register` (the paths stay under `/api/v1/customers`): `GET /api/v1/customers/balances` (rows `customer_id, currency, balance, overdue`; Python has no ORDER BY — Go orders `customer_id, currency`), `GET /api/v1/customers/{customer_id}/billing-summary` (404 `customer not found`; templates **kept** in the lists as Python; `total_unpaid` and `paid_last_12_months` sorted by currency; the 12-month window is `today − 365 days` over `CLEARED` payments' `amount`; `last_invoice_*` by `issue_date DESC NULLS FIRST, created_at DESC` as Postgres orders Python's query), `GET /api/v1/customers/{customer_id}/invoices` (404; `issue_date DESC NULLS FIRST, created_at DESC, invoice_id DESC`). Sums are `Dec4`.

- [ ] **Step 1: Failing tests** — port the 10 `test_customer_balances.py` tests (inventory §4) against the balances query; new `TestBalancesRoute`, `TestBalancesNotSwallowedByCustomerID`, `TestBillingSummary` (lists by type, totals per currency, 12-month window edge at exactly 365 days, last invoice), `TestBillingSummaryIncludesTemplates` (pins the kept Python behaviour), `TestCustomerInvoicesOrder`, `TestCustomerViews404`.

- [ ] **Step 2: Implement, run, mutate, commit** — Mutation: count DRAFT in balances (`TestDraftVoidPaidTemplateAllExcluded` red). Commit `feat(go): customer invoice views`.

---

### Task 9: Inventories, port notes, checks

**Files:**
- Modify: `backend/ROUTES.md`, `backend/TESTS.md`, `backend/PORT-NOTES.md`

- [ ] **Step 1: ROUTES.md** — `python backend/port/inventory.py`; mark the 21 routes `ported` (`go` = `invoices`); the five group-(e) invoice routes get notes `payments plan` (record-payment, payments list, send-receipt) or `documents plan` (pdf, send).
- [ ] **Step 2: TESTS.md** — mark the ported tests with their Go names and recorded mutations (`—` where none); the `[OUT]` tests in inventory §4 stay `todo` with a note naming their plan. Expected ported count: 52 + the number of in-scope tests ported here (state it).
- [ ] **Step 3: PORT-NOTES.md** — one entry per "Go changes this" item (1–3), plus every kept oddity from inventory §5 that a person could notice (1, 4, 6, 7, 8, 10, 11, 12, 13, 14, 15, 16, 17 with Go's isolation change, 18, 21, 22, 23, 24) and the `interval: true` acceptance from Task 1; same format as entries 10–33, each with "After the switch".
- [ ] **Step 4: Full checks** — `bash scripts/check.sh` → `all checks passed`, Go test count above 328, none skipped.
- [ ] **Step 5: Commit** `docs(go): invoices ported; port notes` (local; the controller opens the PR after the final review).
