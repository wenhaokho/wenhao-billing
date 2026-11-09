# Go backend, plan 7: payments, intake, matching, reconciliation

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Go answers the 11 payment routes with Python's contract: manual record-payment, the invoice's payment list, send-receipt, webhook and email intake, and the six reconciliation routes. It also carries the matching engine, auto-clear, manual approval and reversal, with ledger postings exactly where Python makes them.

**Architecture:** There are two new packages.
- `internal/matching` is pure, with no database: Python's `difflib` ratio, the score, and `Match`.
- `internal/payments` holds the shared service functions (`ProcessIncoming`, `ApproveManual`, `Reverse`) and all 11 routes. Each service function runs in its caller's transaction, so the MCP plan can call it later.

The payments package uses these from earlier plans:
- `ledger.PostPaymentCleared`, `ledger.PostReversal` and a new `ledger.ActiveEntryFor`;
- `fx` (through the ledger);
- `withholding.ExpectedCash`.

It writes invoices' `balance_due`, `wht_booked` and `status` itself, under a row lock. The receipt PDF and the Resend call belong to the documents plan. Here they sit behind two small interfaces: a stub renderer and the existing logging `email.Sender`.

**Tech Stack:** Go 1.27, Huma v2.40 on chi v5, pgx v5, sqlc 1.31.1, shopspring/decimal, gokit v0.2.0, Postgres 16, Docker Compose (through `backend/dev`).

**Spec:** `docs/superpowers/specs/2026-11-08-go-backend-design.md`. The relevant sections are "Behaviour that must be carried exactly" (the money rules), "Auth" (webhooks), "Email" and "Tests" (the mutation rule names the webhook signature, currency mismatch, the 95% threshold and PENDING_MANUAL_REVIEW).

**Contract inventory:** `docs/superpowers/plans/2026-11-09-go-backend-07-payments-recon-inventory.md`. It gives every route's fields, sentences and statuses with Python file:line, the engine and ledger rules (§2), the 50 in-scope Python tests (§3), the Go code already there (§4) and 21 oddities (§5). It is the reference for exact values. This plan says which values to keep and which to change.

## Global Constraints

**Python and port notes**
- `backend-python/` is frozen and never edited. Odd behaviour that Go keeps goes into `backend/PORT-NOTES.md`. Each "Go changes these" item below also gets a note.

**Statuses and sentences (inventory §1)**
- Statuses and sentences are Python's, verbatim, except where a "Go changes these" item says otherwise.
  - record-payment: 404 `invoice not found`, then the 400s in Python's order.
  - Intake: 400 `missing X-Signature header`, 401 `invalid webhook signature`, 401 `invalid email webhook token`, 409 `duplicate (intake_source, external_ref)` / `duplicate email payment`.
  - manual-review and reverse: an unknown payment is **400** `payment <id> not found` (kept).
  - `GET /payments/{id}/log` of an unknown payment is `200 []` (kept, spec).
- `code` is stable snake_case per sentence.

**Matching constants**
- Matching constants are `AutoClearThreshold = 95.0`, `AmountEpsilon = 0.0001` and `DateWindowDays = 14`.
- Weights are 50 (amount), 20 (currency), 20 × payer ratio and 10 × date proximity, quantized to 4 places **half-even**.
- The payer ratio is a faithful port of Python's `difflib.SequenceMatcher(None, a, b).ratio()`:
  - on runes;
  - with `autojunk` (on for strings of 200 runes or more);
  - its float converted through `platform.PyFloatRepr`.
- Date proximity is `(14 − anchor) / 14` to 28 decimal places, or 0 when the anchor is 14 or more.

**Money rules (spec)**
- The currency is the invoice's.
- A payment in another currency is never converted: candidates are filtered by `currency =` the payment's.
- Only a CLEARED result posts to the ledger. Any mismatch or ambiguity is `PENDING_MANUAL_REVIEW` and changes neither the invoice nor the ledger.
- Rounding:
  - the engine score uses `RoundHalfEven4`;
  - ledger base amounts use what `fx` already does;
  - there is no withholding rounding on payment paths.

**Lock order**
- The order is: **payment row → invoice row → journal entry** (the ledger locks the entry).
- record-payment and intake lock only the invoice row (their payment is new).
- This stays compatible with plan 5's order: template lock → invoice row → number lock.

**Lists and responses**
- Lists are bare arrays, never `null`.
- `ORDER BY` is Python's, plus the primary key as the last key, descending wherever the list is descending:
  - payments `payment_date DESC, created_at DESC, payment_id DESC`;
  - awaiting-review `created_at DESC, payment_id DESC`;
  - audit-log `created_at DESC, log_id DESC`;
  - payment log `created_at ASC, log_id ASC`.
- audit-log `limit`: default 200, max 500, < 1 → 422.
- `PaymentOut` uses the inventory §0 field order.
  - `amount` and `wht_amount` are `platform.Dec4` (always 4 places).
  - `confidence_score` is `platform.Dec2` or `null`.
  - `created_at` is RFC 3339 UTC.
  - Intake responses print the stored scale too (Go change 9).

**Today and timestamps**
- **Today** is the UTC calendar date of `platform.Clock`. Timestamps come from the database (`CURRENT_TIMESTAMP`), as Python's do.

**Tests**
- Tests run on pgtestdb through `internal/apitest`.
- **Every test and subtest calls `t.Parallel()` first** (lint enforces it).
- FX seeds are USD→IDR 16000 and SGD→IDR 12000 on 2026-01-01.
- Every guard in a "Mutations" line is removed once and its test watched going red, then restored.

**Commits and branch**
- Commits name their paths. Never use `git add -A`: `AGENTS.md`, `docs/redesign/` and the three Nov 5 docs are not ours.
- Messages end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- The branch is `feat/go-payments-recon` from `main`, created with this plan's commit.
- No push or PR until the final review. Merging waits for the owner.

## Go changes these (each a port note)

1. **Ambiguity holds.** If more than one candidate meets the clear condition (exact match and score ≥ 95), the payment is `PENDING_MANUAL_REVIEW`. The best candidate stays attached, and the reasons gain `ambiguous match: <n> open invoices clear equally`.
   - Candidates are ordered by score DESC, then `due_date` ASC NULLS LAST, then `invoice_id`, so the attached one is deterministic.
   - Python took the first row of an unordered query (inventory §5.1, PN 6). The spec says any ambiguity goes to manual review.
2. **Reversal finds the right entry.** `ledger.ActiveEntryFor` returns the newest `PAYMENT` entry for the payment that is neither reversed nor itself a reversal: no other entry's `reversed_by_entry_id` points at it.
   - So approve → reverse → approve → reverse undoes the second clearing, never the first reversal (§5.2).
3. **Row locks.**
   - record-payment and intake clearing read the invoice `FOR UPDATE`.
   - manual-review and reverse read the payment, then the invoice, `FOR UPDATE`.
   - Two concurrent record-payments cannot overdraw a balance, and two approvals of one payment post one entry (§5.5).
4. **Manual approval refuses a DRAFT or VOID invoice** with 400 `cannot approve payment against <STATUS> invoice`. This is record-payment's rule.
   - PAID, another currency and amounts above the balance stay accepted, as in Python (§5.3, noted).
5. **Intake answers bad input with a 400 sentence, never a 500** (§5.7, §5.8, §5.11). The cases:
   - a non-object body: `webhook payload must be a JSON object`;
   - a non-numeric amount, or one with more than 4 places or 15 integer digits: `invalid amount: <repr>`;
   - an amount ≤ 0: `amount must be greater than 0`;
   - a currency that is not 3 letters: `invalid currency: <repr>`;
   - `null` or non-string `payer_name`, `external_ref` or `payment_date`: `invalid <field>: <repr>`;
   - an over-long `payer_name` or `payer_reference` (more than 255 characters): `<field> is too long (max 255)`.

   An open invoice with a null `issue_date` or `due_date` scores date proximity 0 instead of crashing.
6. **Ledger and FX failures do not lose payments.**
   - On intake, a CLEARED result whose posting fails with `ledger.ErrLedger` or `fx.ErrRateMissing` is rolled back to a savepoint. It is then stored as `PENDING_MANUAL_REVIEW`, with the error sentence as an extra reason and a `HELD` log row.
   - On manual-review and reverse, the same errors are 400 with their sentence (Python: 500, §5.19).
7. **Receipts only for CLEARED payments.** send-receipt for a PENDING or FLAGGED payment is 404 `payment not found on this invoice` (§5.9).
8. **No default secrets in production.** `WEBHOOK_HMAC_SECRET` and `EMAIL_WEBHOOK_TOKEN` default to Python's dev values (`dev-webhook-secret-change-me`, `dev-email-token-change-me`). `ParseConfig` refuses either default, or an empty value, when `ENVIRONMENT=production` (§5.12).
9. **Decimals always print at the stored scale**: amounts at 4 places, confidence at 2 (§5.13). This is an allowed comparison difference on intake responses.
10. **Tie-breaks and limits** as in Global Constraints (§5.14, §5.16).

**Kept (port notes):**
- record-payment posts no ledger entry, so such a payment cannot be reversed (PN 8).
- Reversal re-opens the invoice to OPEN and does not undo the hosting restore. The cross-currency restore is PN 7.
- `adjustment_type` is stored with no effect.
- Held payments carry their best guess's invoice and customer.
- Email intake details:
  - `external_ref` is `""` without `message_id` or a ref, so a second such email is 409;
  - the `(?i)` currency letters;
  - `id` matches inside words;
  - the default `payment_date` is the clock's UTC date (Python: server-local).
- A same-currency auto-clear forces `balance_due` to 0.
- Manual approval of an overpayment leaves an AR credit.
- record-payment on a PAID invoice falls through to the exceeds-balance sentence.
- `notes[:200]` is cut in runes.
- The hosting restore is a no-op until the integrations plan (Task 5). The switch-over must not happen before that plan lands.

## Review Focus

1. Two invoices from one customer for the same amount, both open and both on the payment's date. A webhook payment must go to manual review, never clear one of them at random: Task 3 `TestTwoExactCandidatesHold`, Task 8 `TestWebhookAmbiguousIsHeld`.
2. A payment approved, reversed, approved again and reversed again. It must leave the ledger balanced with the second clearing undone, and the invoice back to its pre-payment balance: Task 5 `TestApproveReverseTwiceUndoesLatestClearing`.
3. Two record-payments of 600 sent at once against a balance of 1000. Exactly one must succeed; the other gets the exceeds-balance 400: Task 6 `TestRecordPaymentConcurrentNeverOverdraws`.
4. A USD webhook payment that would clear when no USD→IDR rate exists on its date. It must be stored for manual review with the rate sentence, not lost with a 500: Task 5 `TestIncomingClearWithoutRateIsHeld`.
5. A USD invoice with withholding, paid net in USD. DR 1300 must be in USD at the USD rate, and the entry must balance in IDR: Task 5 `TestForeignCurrencyWithholdingBalancesInBase` (the carried plan 4 test).

---

### Task 1: Platform consolidation and a non-panicking decimal parse

**Files:**
- Modify:
  - `backend/internal/platform/pyformat.go` and `pyformat_test.go`;
  - `backend/internal/platform/types.go` and `types_test.go`;
  - a new `backend/internal/platform/pgconv.go`;
  - the callers in `ledger/ledger.go`, `fx/provider.go`, `invoices/schedule/schedule.go`, `bills`, `invoices`, `projects` and `quotations`.

**Interfaces:**
- Produces:
  - `platform.PyRepr` / `PyDecimalStr` now used everywhere: the private `pyRepr`, `pyStr` and `repr` copies are deleted, after a test proves the outputs identical for each caller's inputs;
  - `func UUIDPtr(u pgtype.UUID) *uuid.UUID`;
  - `func MergeUUID(f Optional[uuid.UUID], cur pgtype.UUID) pgtype.UUID`;
  - `func MergeDate(f Optional[Date], cur pgtype.Date) pgtype.Date`;
  - `func ParseDec4(s string) (decimal.Decimal, error)`: the same bounds and refusal sentence as `Dec4`'s JSON decode, returned as an error rather than stored for a later panic. It is for decoders outside Huma (Task 8).

- [ ] **Step 1: Failing tests.**
  - `TestParseDec4` cases:
    - `"100.5"` → ok;
    - `"1.00001"` → error `value has too many decimal places (max 4)`;
    - a 16-integer-digit value → error;
    - `"abc"` and `"1e999999999"` → error, without hanging.
  - `TestMergeHelpers`: omitted keeps, null clears, value sets.
  - A golden test pinning each deleted copy's outputs against `platform`'s. Use the inputs from the existing ledger, fx and schedule tests.
- [ ] **Step 2: Implement.** Swap the callers and delete the copies. `./dev test ./...` stays green with the test count unchanged or higher.
- [ ] **Step 3: Mutate and commit.** Mutation: make `ParseDec4` accept 5 places (its test goes red). Commit `refactor(go): one home for Python printers, pg helpers and decimal parsing`.

---

### Task 2: Python's difflib ratio

**Files:**
- Create: `backend/internal/matching/difflib.go`, `difflib_test.go`, `testdata/ratio_golden.json`

**Interfaces:**
- Produces: `func Ratio(a, b string) float64`. It equals `difflib.SequenceMatcher(None, a, b).ratio()` and works on runes.

- [ ] **Step 1: Generate goldens.** Run against the frozen Python with `docker compose exec -T backend python -c '<script>'`, printing `[a, b, repr(ratio)]` as JSON. Commit the script's text as a comment at the top of the JSON file. Include at least these pairs:
  - identical strings;
  - `""`/`""` (1.0) and `""`/`"a"`;
  - `"pt maju jaya"` / `"pt maju jaya tbk"`;
  - `"acme"` / `"amce"`;
  - `"abcabcabc"` / `"cbacbacba"`;
  - repeated characters;
  - `"café"` / `"cafe"`;
  - a 4-byte emoji;
  - a pair over 200 runes where autojunk matters (a popular character over 1% of `b`);
  - 20 realistic payer/customer pairs.
- [ ] **Step 2: Failing test.** `TestRatioMatchesPython` asserts `platform.PyFloatRepr(Ratio(a, b)) == want` for every golden.
- [ ] **Step 3: Implement.** Port `find_longest_match`, `get_matching_blocks` and the `b2j`/autojunk set-up from CPython's `difflib.py`.
- [ ] **Step 4: Mutate and commit.** Mutation: turn autojunk off (the >200 golden goes red). Commit `feat(go): port difflib SequenceMatcher ratio`.

---

### Task 3: Matching engine

**Files:**
- Create: `backend/internal/matching/engine.go`, `engine_test.go`

**Interfaces:**
- Produces:
  - `type Incoming struct { Amount decimal.Decimal; Currency, PayerName string; PayerReference *string; PaymentDate time.Time }`.
  - `type Candidate struct { InvoiceID, CustomerID uuid.UUID; Currency string; BalanceDue, WhtOutstanding decimal.Decimal; IssueDate, DueDate *time.Time; CustomerName string; Aliases []string }`.
  - `type Result struct { Status, AdjustmentType string; Invoice *Candidate; Confidence decimal.Decimal; CurrencyMismatch, AmountMismatch bool; Reasons []string }`.
  - `func Match(p Incoming, cands []Candidate) Result`.
  - `func IsExactMatch(p Incoming, c Candidate) bool`.
  - `func Score(p Incoming, c Candidate) decimal.Decimal`.
  - Constants `StatusCleared`, `StatusPending`, `StatusFlagged`, and `AdjNone`, `AdjPartialPay`, `AdjCreditOnAccount`, `AdjBankFee`.
- The caller pre-filters by status and currency, as Python's query does. `Match` still checks status-free fields only (inventory §2.1).

- [ ] **Step 1: Failing tests.**
  - Port the 12 tests in `tests/unit/test_matching_engine.py` (inventory §3), with Python's values.
  - `TestReasonsSentences`: exact strings for currency, amount (`delta=-100.0000`) and confidence (`confidence 72.8571 below threshold 95.0`).
  - `TestNoCandidates`: PENDING, `CREDIT_ON_ACCOUNT`, confidence `0`, reason `no open invoice matches payer/currency`.
  - `TestTwoExactCandidatesHold` (Go change 1): the earlier `due_date` is attached; the reason count is 2.
  - `TestTieOrderDeterministic`.
  - `TestNullDatesScoreZeroProximity`.
  - `TestScoreHalfEven`: a crafted payer ratio whose 20× value ends in 5 at the fifth place.
  - `TestWithinEpsilonLowConfidenceAdjustment` (kept, §2.1 step 4).
- [ ] **Step 2: Implement, run and mutate.**
  - Mutations, each turning its test red:
    - threshold `>= 94` (the date-decay test);
    - drop the currency check in `IsExactMatch` (the currency test);
    - drop the ambiguity rule (`TestTwoExactCandidatesHold`);
    - epsilon 0 (the epsilon test).
  - Commit `feat(go): matching engine`.

---

### Task 4: The ledger's active entry for a payment

**Files:**
- Modify: `backend/internal/ledger/queries.sql`, regenerate `ledgerdb/`, `ledger.go`
- Test: `backend/internal/ledger/active_test.go`

**Interfaces:**
- Produces: `func ActiveEntryFor(ctx context.Context, tx pgx.Tx, sourceType string, sourceID uuid.UUID) (uuid.UUID, bool, error)`. It returns the newest entry (`posted_at DESC, entry_id DESC`) with `reversed_by_entry_id IS NULL` and `NOT EXISTS` (an entry whose `reversed_by_entry_id` is this one). `false` means there is none.

- [ ] **Step 1: Failing tests.**
  - `TestActiveEntryNone`.
  - `TestActiveEntryAfterPost`.
  - `TestActiveEntryAfterReversalIsNone`: the reversal entry is not returned.
  - `TestActiveEntryAfterRepost`: post → reverse → post returns the second post.
- [ ] **Step 2: Implement, mutate and commit.** Mutation: drop the `NOT EXISTS` (the after-reversal test goes red). Commit `feat(go): ledger active entry lookup excludes reversals`.

---

### Task 5: Payments service: clear, incoming, approve, reverse

**Files:**
- Create:
  - `backend/internal/payments/queries.sql`, generated `paymentsdb/`;
  - `store.go`: payment insert, get, `FOR UPDATE`; recon log insert; the invoice row `FOR UPDATE` and its update; the candidate load;
  - `clear.go`;
  - `service.go`;
  - `hosting.go`.
- Modify: `backend/sqlc.yaml`
- Test: `backend/internal/payments/service_test.go`, `withholding_test.go`

**Interfaces:**
- Consumes: Task 3's `matching.*`, Task 4's `ledger.ActiveEntryFor`, `ledger.PostPaymentCleared`, `ledger.PostReversal`, and `withholding.ExpectedCash`.
- Produces:
  - `type IntakePayload struct { Amount decimal.Decimal; Currency, PayerName string; PayerReference *string; PaymentDate time.Time; IntakeSource, ExternalRef string }`.
  - `type Service struct { BaseCurrency string; Hosting HostingRestorer }`.
  - `func (s Service) ProcessIncoming(ctx, tx pgx.Tx, in IntakePayload) (paymentsdb.Payment, error)`.
  - `func (s Service) ApproveManual(ctx, tx, paymentID, invoiceID uuid.UUID, actor *uuid.UUID, adj string) (paymentsdb.Payment, error)`.
  - `func (s Service) Reverse(ctx, tx, paymentID uuid.UUID, actor *uuid.UUID, reason string) (paymentsdb.Payment, error)`.
  - `var ErrPayment`: refusals whose text is Python's sentence, matched by `errors.Is`.
  - `type HostingRestorer interface { RestoreIfPaid(ctx context.Context, tx pgx.Tx, invoiceID uuid.UUID) error }`, with `NoHosting{}`.
  - `func PaymentOutFrom(paymentsdb.Payment) PaymentOut`.
  - A duplicate `(intake_source, external_ref)` returns `ErrDuplicate`.

Rules: inventory §2.3–§2.6, in Python's order, with Go changes 1–4 and 6.
- Clearing uses `settles` and `withheld` as §2.6 defines them. It sets `payment.wht_amount = withheld`, posts the ledger before changing the balance, then sets `wht_booked += withheld` and the balance (same currency: clamped; cross currency: 0).
- PAID calls `Hosting.RestoreIfPaid`.
- Log rows:
  - `CLEARED` or `HELD` (actor nil);
  - `OVERRIDDEN` with `manual approval against invoice <id>`;
  - `REVERSED` with `[reason]`.
- The reversal memo is `Reversal: <reason>`, and the payment becomes FLAGGED.
- Reversal restores the invoice:
  - `balance_due += amount + wht_amount`;
  - `wht_booked -= wht_amount`, where a negative result is a plain error;
  - status OPEN.

- [ ] **Step 1: Failing tests.**
  - Port the 9 tests of `test_reconciliation_pipeline.py` and the 5 of `test_reconciliation_withholding.py` (inventory §3).
  - New:
    - `TestForeignCurrencyWithholdingBalancesInBase`: the plan 4 carry, a USD invoice with PPh;
    - `TestIncomingClearWithoutRateIsHeld` (Go change 6);
    - `TestIncomingDuplicateRef`;
    - `TestHeldPaymentLeavesInvoiceAndLedger`;
    - `TestApproveDraftOrVoidInvoiceIs400`;
    - `TestApproveFlaggedPayment` (kept);
    - `TestApproveTwiceConcurrentPostsOnce`;
    - `TestApproveReverseTwiceUndoesLatestClearing`;
    - `TestReverseManualPaymentHasNoEntry` (400 sentence, kept);
    - `TestReverseNonClearedIs400`;
    - `TestApproveMissingRateIs400Sentence`;
    - `TestHostingRestorerCalledOnPaid` (a recording fake).
- [ ] **Step 2: Implement, run and mutate.** Mutations, each turning its test red:
  - Book withholding on a partial payment (`…non_matching_amount_books_no_withholding`).
  - Skip the payment `FOR UPDATE` (the concurrent approve test, at `-count=5`).
  - Use the oldest entry instead of `ActiveEntryFor` (the approve/reverse-twice test).
  - Post a ledger entry on HELD (the held test).
  - Accept DRAFT on approve (the draft test).
  - Drop the savepoint (the missing-rate test is red with an error).

  Commit `feat(go): payments service: clear, incoming, approve, reverse`.

---

### Task 6: Record-payment and the invoice's payments

**Files:**
- Create: `backend/internal/payments/invoice_routes.go`, `api.go` (`PaymentOut`, `Register`)
- Modify: `backend/cmd/api/main.go` (register with `Service{BaseCurrency: cfg.BaseCurrency, Hosting: NoHosting{}}`)
- Test: `backend/internal/payments/record_test.go`, `list_test.go`

Routes:
- `POST /api/v1/invoices/{invoice_id}/record-payment` → **201** (inventory §1).
  - Body: `amount` and `wht_amount` as `platform.Dec4`, `ge=0`; `payment_date`; `payer_name`, `payer_reference` and `notes` are optional. `payer_name` and `payer_reference` have `maxLength` 255, as 422s.
  - Errors are Python's 5, in order.
  - Inserts a MANUAL CLEARED payment: `external_ref` is `manual-` + 12 lowercase hex characters; `payer_name` is the value or `(manual entry)`; the currency is the invoice's.
  - Updates the balance and status under the invoice's `FOR UPDATE`. PAID calls `Hosting`.
  - Writes a `CLEARED` log row: `["manual-entry"]` plus `notes` cut to 200 runes when not empty; the actor is the user.
  - No ledger posting.
- `GET /api/v1/invoices/{invoice_id}/payments`: CLEARED only, 404 `invoice not found`.

- [ ] **Step 1: Failing tests.**
  - Port the 9 tests of `test_record_payment_withholding.py` [IN] and the 4 of `test_invoice_payments_list.py`.
  - New:
    - `TestRecordPaymentOnDraftAndVoidIs400`;
    - `TestRecordPaymentPaidInvoiceExceedsSentence` (kept);
    - `TestRecordPaymentConcurrentNeverOverdraws`;
    - `TestRecordPaymentWritesNoJournal`;
    - `TestRecordPaymentExternalRefShape`;
    - `TestRecordPaymentNotesRuneCut` (a 250-rune string with multi-byte runes);
    - `TestRecordPaymentFiveDecimalsIs422`;
    - `TestPaymentRoutesRequireAdmin` (401/403).
- [ ] **Step 2: Implement, run and mutate.** Mutations, each turning its test red:
  - Drop the invoice lock (the concurrent test).
  - Drop the withheld-above-outstanding check (its test).
  - Allow DRAFT (its test).

  Commit `feat(go): record-payment and invoice payments list`.

---

### Task 7: Reconciliation routes

**Files:**
- Create: `backend/internal/payments/recon_routes.go`
- Test: `backend/internal/payments/recon_test.go`

Routes (inventory §1). The literal paths are registered before `{payment_id}`.
- `GET /api/v1/payments/audit-log`:
  - filters `action`, `payer` (ILIKE `%payer%`, with `%`, `_` and `\` escaped: a Go change folded into note 10), `from_date`, `to_date` (the whole day) and `limit`;
  - row keys in order, `amount` a 4-place string or null, `actor_email` from users.
- `GET /api/v1/payments/awaiting-review`.
- `GET /api/v1/payments/{payment_id}`: 404 `payment not found`.
- `GET /api/v1/payments/{payment_id}/log`: `[]` when unknown.
- `POST /api/v1/payments/{payment_id}/manual-review`:
  - body `{invoice_id, adjustment_type="NONE"}`;
  - `adjustment_type` outside the four is 400 `unknown adjustment_type 'X'` (through `platform.PyRepr`);
  - service refusals are 400 with their sentence.
- `POST /api/v1/payments/{payment_id}/reverse`: body `{reason}`, required, may be empty.

- [ ] **Step 1: Failing tests.**
  - `TestAuditLogFiltersOrderLimit`.
  - `TestAuditLogPayerEscapesWildcards`.
  - `TestAuditLogLimitBounds` (0 → 422, 501 → 422).
  - `TestAwaitingReviewOnlyPending`.
  - `TestGetPayment404`.
  - `TestPaymentLogUnknownIsEmpty`.
  - `TestStaticPathsNotSwallowed`.
  - `TestManualReviewClearsOverRest`.
  - `TestManualReviewUnknownAdjustmentIs400`.
  - `TestManualReviewUnknownPaymentIs400`.
  - `TestReverseOverRest`.
  - `TestReconRoutesRequireAdmin`.
- [ ] **Step 2: Implement, run and mutate.** Mutation: register `{payment_id}` first (the static-path test goes red). Commit `feat(go): reconciliation routes`.

---

### Task 8: Intake: webhook and email

**Files:**
- Create: `backend/internal/payments/intake.go` (the parsers), `intake_routes.go`
- Modify: `backend/internal/platform/config.go` (`WebhookHMACSecret`, `EmailWebhookToken`, Go change 8), `config_test.go`
- Test: `backend/internal/payments/intake_test.go`, `intake_routes_test.go`

**Interfaces:**
- Produces:
  - `func ParseWebhook(body []byte) (IntakePayload, error)`;
  - `func ParseEmail(body map[string]any, today time.Time) (IntakePayload, error)`;
  - errors are `ErrIntake`, whose text is the 400 sentence.

Routes:
- `POST /api/v1/payments/intake/webhook`:
  - reads the raw body (`huma.RawBody`) with no session;
  - `X-Signature` missing → 400; otherwise the trimmed value is compared to lowercase hex HMAC-SHA256 with `hmac.Equal`, and a mismatch → 401;
  - invalid JSON → 400 `invalid JSON: <Go's text>` (an allowed difference, noted);
  - missing keys → 400 `webhook payload missing fields: ['amount', 'external_ref']`, in Python's key order;
  - Go change 5 sentences;
  - `amount` through `platform.ParseDec4`, from a JSON number or a string;
  - the currency upper-cased;
  - then `ProcessIncoming` in one transaction; `ErrDuplicate` → 409.
- `POST /api/v1/payments/intake/email`:
  - `X-Email-Token`, constant time, → 401;
  - a non-object body → 422;
  - an attachment with content → 400 `Attach an OCR backend (tesseract/textract/etc) before calling`;
  - `AMOUNT_RE` and `REF_RE` ported verbatim (Go `regexp`, `(?i)`);
  - the amount not found → 400 `could not locate amount in email body or attachments`;
  - `payer_name` from `from` / `payer_name` / `UNKNOWN`;
  - `payment_date` from the body, or the clock's UTC date;
  - `external_ref` from `message_id`, the ref, or `""`;
  - a duplicate → 409 `duplicate email payment`.

- [ ] **Step 1: Failing tests.**
  - Parsers:
    - `TestParseWebhookMissingFieldsSentence`;
    - `TestParseWebhookAmountForms` (number, string, 5 places → 400, `"abc"` → 400, ≤ 0 → 400);
    - `TestParseWebhookNullFieldsAre400`;
    - `TestParseWebhookBadDateIs400`;
    - `TestParseWebhookLongCurrencyIs400`;
    - `TestParseEmailAmountRegex` (`Total paid: USD 1,234.56`, `amount=100.00`, the kept `THE` case);
    - `TestParseEmailRefRegex` (the kept `said thanks` case);
    - `TestParseEmailDefaults`;
    - `TestParseEmailAttachmentIs400`.
  - Routes:
    - `TestWebhookMissingSignature400`;
    - `TestWebhookBadSignature401`;
    - `TestWebhookUppercaseHexIs401` (kept);
    - `TestWebhookClearsAndPosts`;
    - `TestWebhookAmbiguousIsHeld`;
    - `TestWebhookDuplicate409`;
    - `TestEmailTokenRequired401`;
    - `TestEmailHeldWithoutCandidates`;
    - `TestEmailEmptyRefSecondIs409` (kept).
  - Config: `TestProductionRefusesDefaultIntakeSecrets`.
- [ ] **Step 2: Implement, run and mutate.** Mutations, each turning its test red:
  - Skip the HMAC comparison (the bad-signature test).
  - Accept any token (the token test).
  - Allow the default secret in production (the config test).

  Commit `feat(go): payment intake webhook and email`.

---

### Task 9: Send receipt

**Files:**
- Create: `backend/internal/payments/receipt.go`, `receipt_routes.go`
- Modify:
  - `backend/internal/email/email.go`: `Message` gains `Cc []string` and `Attachments []Attachment{Filename, ContentType string; Content []byte}`; `LogSender` logs CC and attachment names;
  - `backend/internal/platform/email.go`: add `ParseCcEmails`.
- Test: `backend/internal/payments/receipt_test.go`, `receipt_routes_test.go`, `platform/email_test.go`

**Interfaces:**
- Produces:
  - `func ReceiptReference(paymentID uuid.UUID) string`, returning `RCT-` + the first 8 hex characters, upper-cased;
  - `func BalanceNote(balance decimal.Decimal, currency string) string`;
  - `func FormatAmount(amount decimal.Decimal, currency string) string`, a port of `_fmt_amount` in `app/services/invoice_pdf.py` (0 places for IDR, 2 otherwise, Python's grouping), golden-tested against Python and kept for the documents plan;
  - `type ReceiptRenderer interface { RenderReceipt(ctx context.Context, d ReceiptData) ([]byte, error) }`, with `StubRenderer{}` returning a minimal valid one-page PDF (starting `%PDF-`) until the documents plan;
  - `func ParseCcEmails(s string) (*string, error)`, which splits on `[,;\s]+`, validates each address, de-duplicates case-insensitively keeping the first spelling, joins with `", "`, returns blank as nil, and gives `invalid email address: <addr>` as a 422 at `cc_email`.

Route: `POST /api/v1/invoices/{invoice_id}/payments/{payment_id}/send-receipt` → 200 `{"sent_to"}`.
- Errors, in order: 404 `invoice not found`; 404 `payment not found on this invoice` (Go change 7); 400 `no recipient: …`; 502 `failed to send receipt email`.
- Subject and body are exactly inventory §1. The filename is `receipt-<ref>.pdf`.
- No database writes.

- [ ] **Step 1: Failing tests.**
  - Port:
    - the 6 tests of `test_send_receipt.py`, using a recording `email.Sender` and a failing one;
    - the 2 of `test_customer_cc.py`;
    - the 3 [IN] of `test_receipt_pdf.py`.
  - New:
    - `TestSendReceiptPendingPaymentIs404`;
    - `TestFormatAmountMatchesPython` (goldens from compose Python);
    - `TestSendReceiptBodyExact`.
- [ ] **Step 2: Implement, run and mutate.** Mutations, each turning its test red:
  - Drop the payment's invoice check (the not-on-invoice test).
  - Drop the CLEARED check (the pending test).

  Commit `feat(go): send payment receipt`.

---

### Task 10: Inventories, port notes, checks

**Files:** `backend/ROUTES.md`, `backend/TESTS.md`, `backend/PORT-NOTES.md`

- [ ] **Step 1: Routes.** Run `python backend/port/inventory.py`. Mark the 11 routes `ported` with `go` = `payments`.
- [ ] **Step 2: Tests.** Mark the 50 ported Python tests with their Go names and recorded mutations (`—` where there are none). The 14 [OUT] tests keep `todo`, with their plan named. The expected ported count is 207.
- [ ] **Step 3: Port notes.** Write one entry per "Go changes these" item and per kept behaviour. Each entry has the Python file:line (verified), the Go test that pins it, and "After the switch". Update PN 6 (ambiguity now holds) and PN 8 (record-payment and reversal).
- [ ] **Step 4: Checks.** Run `bash scripts/check.sh` → `all checks passed`, with the Go test count above 730 and none skipped.
- [ ] **Step 5: Commit.** Commit `docs(go): payments and reconciliation ported; port notes`, locally. The controller opens the PR after the final review.
