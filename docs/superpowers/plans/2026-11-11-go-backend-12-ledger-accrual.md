# Go backend, plan 12: accrual postings for invoices and bills

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> **Written 2026-11-11 against `fix/go-manual-payment-ledger` at f1e6131 (PORT-NOTES ends at 161).** Checked present:
> - `ledger.PostPaymentCleared(ctx, tx, baseCurrency string, p Payment, inv Invoice, withheld decimal.Decimal) (Entry, error)` (ledger/ledger.go:101). It relieves the receivable at the **payment date's** rate.
> - `ledger.PostReversal(ctx, tx, _ string, originalID uuid.UUID, memo *string) (Entry, error)` (:200) and `ledger.ActiveEntryFor(ctx, tx, sourceType string, sourceID uuid.UUID) (uuid.UUID, bool, error)` (:347).
> - `payments.Service.postCleared` (payments/clear.go:63). It is the only caller of `PostPaymentCleared`: intake, approval and record-payment all go through it.
> - `invoices.FinalizeInvoice` / `VoidInvoice(ctx, tx, id)` (invoices/lifecycle.go:30, :79). These are also the MCP `finalize_invoice` / `void_invoice` tools (mcp/tools_write.go:829, :842).
> - `bills.UpdateBill(ctx, tx, id, b)` is the only way a bill leaves DRAFT: a PATCH may set any of the five statuses (bills/api.go:105, store.go:177). MCP `update_bill` uses the same path. `recordPayment` and `voidBill` are in bills/payments.go:117, :156.
> - Chart of accounts (migrations/00001_baseline.sql:1094-1105): `1000` Operating Bank, `1100` AR, `1300` Prepaid Tax, `2000` AP, `2100` Sales Tax Payable, `2200` Unbilled AR, `4000` Fixed-Fee, `4100` Support/Maintenance, `4200` Usage-Based (INCOME), `4900` FX Gain (INCOME), `5000`/`5100`/`5200` (COGS), `5900` FX Loss (EXPENSE). There is no general expense account, and `payment_account` holds bank instructions, not ledger accounts.
> - Journal tables are read only by `reports` (trial balance, balance sheet, revaluation, all filtered on `posted_at`) and `compare`. Aging and every stats query read `invoices`/`bills`.
>
> **Where it runs:** between plan 11 Task 5 and Task 6. Branch `feat/go-ledger-accrual`, cut from `fix/go-manual-payment-ledger` (rebase on `main` once that merges). Migrations are numbered from `00008`.

**Goal:** Go posts accrual entries: invoice finalize (Dr AR / Cr revenue), bill open (Dr expense / Cr AP), void reversals, and stored, reversible bill payments. Payments relieve AR/AP at the document's issue basis with realised FX gain/loss. A one-off command back-posts the documents already issued.

**Architecture:**
- `internal/ledger` gains three postings and one query. The postings are document issue, bill payment, and dated reversal. The query is the open basis: the face and base still owed on a document, summed from its own entries and its payments' entries.
- `invoices`, `bills` and `payments` call these postings inside their existing transactions, under the document lock they already take. Routes, MCP tools and jobs keep calling the same service functions, so every path posts.
- A new `internal/backpost` package backs the `api backpost-ledger` subcommand. It runs in one transaction, which a dry run rolls back.

**Tech Stack:** Go 1.27, pgx v5, sqlc 1.31.1, goose, Huma v2.40, shopspring/decimal, pgtestdb on Postgres 16.

**Spec:** `docs/superpowers/specs/2026-11-08-go-backend-design.md`, together with:
- the owner's decisions of 2026-11-11, copied into Global Constraints below;
- `prd.md` §4 (accrual chart of accounts);
- CLAUDE.md "Multi-currency ledger".

## Global Constraints

**Python, port notes, branch**
- `backend-python/` stays frozen. Go diverges deliberately, and each divergence is a PORT-NOTES entry:
  - 162 invoice issue and void;
  - 163 payment relief at issue basis;
  - 164 bill open, edit, status and void;
  - 165 bill payments;
  - 166 back-post.
- Notes 8, 73 and 161 get a closing line pointing at the new notes.
- Commits name their paths and end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. No push or PR until the final review.

**Owner decisions (2026-11-11), binding**
- Invoices post at finalize: Dr AR / Cr revenue for `invoices.amount` (net of discount, grossed up when withholding is grossed up). The rate is the issue date's, and the base amount is recorded on every line.
- Revenue account per line: the item's `revenue_account_id`. Without one, the account follows `invoice_type`: `RECURRING` → `4100`, `USAGE` → `4200`, anything else (`MILESTONE` and free-text types) → `4000`.
- Withholding stays booked at payment (Dr `1300`), never at issue.
- A payment relieves AR at the invoice's issue basis. The difference against the cash's base at the payment date is realised FX: `4900` gain, `5900` loss.
- Void reverses the issue entry.
- Bills post at open: Dr expense / Cr `2000`. Expense account per line: the line's `expense_account_id`, else the item's `expense_account_id`, else `6000` (new, Task 4).
- Bill payments get a row and post Dr `2000` / Cr `1000` with realised FX. They are reversible.
- Templates, DRAFTs and quotations never post.
- Existing issued documents are back-posted at their own issue date and rate by one command. It has a dry run and refuses on missing rates. The production run is an OWNER YES step.

**Posting rules**
- Postings happen only through `internal/ledger`, inside the caller's transaction.
- One active issue entry per document. `ledger.PostIssued` refuses a second one under the document's row lock.
- `posted_at`:
  - an issue entry is posted at `LEAST(issue date 00:00, CURRENT_TIMESTAMP)`;
  - every other entry is posted at `CURRENT_TIMESTAMP` (as now);
  - back-post restatements keep the original entry's `posted_at`.
- Rate date: the document's `issue_date`, else its `created_at` date (both can be null-dated: an invoice PATCH can null `issue_date`, and bills never default it). Payments use `payment_date`.
- Base amounts are `fx.Convert` (half-even, 4 places). `fx_rate` keeps 8 places.

**Errors and safe-stop**
- A ledger refusal is `ledger.ErrLedger` → 400 `ledger_refused`. A missing rate is `fx.ErrRateMissing` → 400 `fx_rate_missing`. These are PN 161's answers.
- On intake, either one holds the payment as `PENDING_MANUAL_REVIEW`. That is the existing `clearIncoming` savepoint path.
- MCP already maps both errors (mcp/tools_write.go:498).

**Locks**
- payment → invoice (`FOR NO KEY UPDATE`) → entry, as in plan 7.
- bill payment → bill (`FOR UPDATE`) → entry.
- Document-only paths (finalize, void, PATCH, back-post): document `FOR UPDATE` → entry.
- No path locks a payment row after its document.

**Out of scope**
- `2100` (documents carry no sales tax) and `2200` (usage posts at finalize, not on accrual of usage).
- Frontend changes: a bill-payment list and reverse button wait for the UI revamp.
- Stats: they read `invoices` only and are unchanged.

**Tests and commands**
- Tests run on pgtestdb (real Postgres). **Every test and subtest calls `t.Parallel()` first.**
- Each guard is mutated once and watched going red. The mutation goes in its PN entry.
- Commands, from `backend/`:
  - `./dev sqlc` after query edits;
  - `./dev test ./internal/<pkg>/...`;
  - `./dev lint`.
- The gate is `bash scripts/check.sh` from the repo root.

## Review Focus

1. A USD invoice paid in two parts at two different rates must end with its AR at exactly 0 in both USD and IDR, with the FX lines summing to the true difference: Task 3 `TestTwoPartialsClearARBaseExactly`.
2. Voiding a partly paid invoice or bill must be refused, with nothing reversed and nothing changed. Without this, AR/AP would be left in credit with no document: Task 2 `TestVoidWithClearedPaymentRefused` and Task 4 `TestVoidBillWithPaymentRefused`.
3. Running the back-post twice must post nothing the second time: Task 7 `TestBackpostIdempotent`.
4. An item whose revenue account is not INCOME (say someone picked `1100`) must stop the finalize with a clear sentence, and the invoice must stay DRAFT: Task 2 `TestFinalizeRefusesNonIncomeAccount`.
5. Finalizing a USD invoice with no USD rate on or before its issue date must answer 400 `fx_rate_missing`, and the invoice must stay DRAFT: Task 2 `TestFinalizeMissingRateLeavesDraft`.

---

### Task 1: Ledger primitives (issue posting, dated entries, account lookup)

**Files:**
- Modify: `backend/internal/ledger/ledger.go`, `backend/internal/ledger/queries.sql` (regenerate `ledgerdb`)
- Create: `backend/internal/ledger/issue.go`, `backend/internal/ledger/issue_test.go`

**Interfaces:**
- Produces:
  - Constants:
    - `SourceInvoice = "INVOICE"`, `SourceBill = "BILL"`, `SourceBillPayment = "BILL_PAYMENT"`;
    - `PayableCode = "2000"`, `FixedFeeCode = "4000"`, `SupportCode = "4100"`, `UsageCode = "4200"`.
  - `type Share struct { AccountID int32; Amount decimal.Decimal }`
  - `type Document struct { SourceType string; ID uuid.UUID; Currency string; Amount decimal.Decimal; RateDate, IssueDate time.Time; Memo string; Shares []Share }`
  - `func PostIssued(ctx context.Context, tx pgx.Tx, baseCurrency string, d Document) (Entry, error)`:
    - for `SourceInvoice`: Dr `1100` Amount, Cr each share;
    - for `SourceBill`: Dr each share, Cr `2000` Amount.
  - `func ReverseIssued(ctx context.Context, tx pgx.Tx, sourceType string, id uuid.UUID, memo string) (reversed bool, err error)`. It is ActiveEntryFor + PostReversal, and returns false when there is no active entry.
  - `func PostReversalAt(ctx context.Context, tx pgx.Tx, originalID uuid.UUID, memo *string, postedAt time.Time) (Entry, error)`. `PostReversal` keeps its signature and posts at now.
  - `type Account struct { ID int32; Code, Type string; Active bool }`
  - `func AccountsByID(ctx context.Context, tx pgx.Tx, ids []int32) (map[int32]Account, error)`
  - `func AccountsByCode(ctx context.Context, tx pgx.Tx, codes []string) (map[string]Account, error)`

- [ ] **Step 1: Failing tests** in `issue_test.go`:
  - `TestPostIssuedInvoiceIDR`: amount 1000.0000 with shares 4000:600 and 4100:400. Lines: Dr 1100 1000 / Cr 4000 600 / Cr 4100 400, base equal, `fx_rate` 1.
  - `TestPostIssuedInvoiceUSDBaseRounding`:
    - USD 100.0001 at rate 15123.45678901, three shares 33.3334/33.3334/33.3333.
    - The control base is `Convert(100.0001)`. The largest share (first by code on a tie) absorbs control − Σ others.
    - The entry balances to 0.0000 in base.
  - `TestPostIssuedBillDebitsSharesCreditsAP`: the mirror image for a bill.
  - `TestPostIssuedPostedAtIsIssueDateOrNow`:
    - issue date 2026-01-05 → `posted_at` 2026-01-05 00:00;
    - issue date in the future → `posted_at` within a second of `now()`.
  - `TestPostIssuedTwiceRefused`: a second call for the same source is `ErrLedger` `"<source> <id> already has an active issue entry"`.
  - `TestPostIssuedSharesMustSumToAmount`: shares off by 0.0001 → `ErrLedger`, nothing written.
  - `TestPostIssuedRateMissing`: `errors.Is(err, fx.ErrRateMissing)`, nothing written.
  - `TestReverseIssuedNoneIsFalse`, and `TestReverseIssuedThenPostAgain`: after the reversal, a new `PostIssued` is allowed. This is the bill OPEN→DRAFT→OPEN path.
  - `TestPostReversalAtKeepsDate`.
- [ ] **Step 2: Run** `./dev test ./internal/ledger/...`. The new tests fail to compile.
- [ ] **Step 3: Implement.**
  - `InsertEntry` becomes `INSERT … posted_at = LEAST(COALESCE(sqlc.narg(posted_at)::timestamp, CURRENT_TIMESTAMP), CURRENT_TIMESTAMP)`. `insert` takes a `*time.Time` (nil = now). Existing callers pass nil.
  - Add the queries `AccountsByID` / `AccountsByCode` (`code, account_id, type, active`). `newBuilder` uses `AccountsByCode` with the codes it needs.
  - `PostIssued`:
    - checks `ActiveEntryFor` first;
    - checks Σ shares == Amount and every share > 0;
    - builds lines with `builder` on `RateDate`, applying the rounding rule from the test;
    - takes `posted_at` from `IssueDate`;
    - runs `checkBalanced` before the insert.
  - Account-type checks belong to callers (Tasks 2 and 4), not to `PostIssued`.
- [ ] **Step 4: Run** the same command. PASS. **Mutation:** drop the `ActiveEntryFor` check. `TestPostIssuedTwiceRefused` goes red.
- [ ] **Step 5: Commit** `feat(go): ledger issue postings and dated entries`.

---

### Task 2: Invoice finalize posts; void reverses and refuses paid ones

**Files:**
- Create: `backend/internal/invoices/posting.go`, `backend/internal/invoices/posting_test.go`
- Modify: `backend/internal/invoices/lifecycle.go`, `invoices/routes.go` (`Register` gets `base string`; `clientError` maps `ledger.ErrLedger` / `fx.ErrRateMissing`), `invoices/queries.sql` (`ListInvoiceLineAccounts`, `HasClearedPayments`), `mcp/tools_write.go` (pass `d.Cfg.BaseCurrency`), `cmd/api/main.go`, `backend/PORT-NOTES.md` (162)

**Interfaces:**
- Consumes: `ledger.PostIssued`, `ledger.ReverseIssued`, `ledger.AccountsByID`, `ledger.AccountsByCode`, `ledger.Share` (Task 1).
- Produces:
  - `func FinalizeInvoice(ctx context.Context, tx pgx.Tx, base string, id uuid.UUID) (InvoiceOut, error)` (signature change);
  - `func VoidInvoice(ctx, tx, id)` (signature unchanged);
  - `func RevenueShares(ctx context.Context, tx pgx.Tx, inv invoicesdb.Invoice) ([]ledger.Share, error)` (exported for Task 7);
  - `func ModeRevenueCode(invoiceType string) string`.

- [ ] **Step 1: Failing tests** in `posting_test.go`. They drive the HTTP routes through `apitest` unless noted.
  - `TestFinalizePostsIssueEntry`: two lines, one with an item whose revenue account is `4100`, one without, on a `MILESTONE` invoice. Expect Cr 4100 and 4000 in line proportion, Dr 1100 = amount.
  - `TestRevenueSharesByBillingMode`, a table test: RECURRING → 4100, USAGE → 4200, MILESTONE → 4000, `"RETAINER"` → 4000.
  - `TestRevenueSharesDiscountAndGrossUp`: lines 600/400, 10% discount and PPh23 gross-up. The shares sum to `amount`, and the remainder goes to the largest group.
  - `TestRevenueSharesNoLines`: a milestone invoice with no lines puts its whole amount on 4000.
  - `TestFinalizeUSDUsesIssueDateRate`: rates on 01-01 and 01-10, issue date 01-05. The 01-01 rate is used, and `posted_at` is 01-05.
  - `TestFinalizeZeroInvoicePostsNothing`: PAID, no entry.
  - `TestFinalizeTemplateRefusedPostsNothing`.
  - `TestFinalizeRefusesNonIncomeAccount`: item revenue account `1100` → 400 `ledger_refused` `"account 1100 (ASSET) cannot take revenue; set the item's revenue account to an INCOME account"`, the invoice still DRAFT.
  - `TestFinalizeMissingRateLeavesDraft`: 400 `fx_rate_missing`, still DRAFT, no entry.
  - `TestVoidReversesIssueEntry`: OPEN → VOID. The issue entry has `reversed_by_entry_id`, and the trial balance nets AR and revenue to 0.
  - `TestVoidWithClearedPaymentRefused`:
    - a PARTIAL invoice → 400 `invoice_has_payments` `"cannot void an invoice with cleared payments — reverse payments first"`;
    - status, balance and journal are unchanged;
    - a withholding-only payment counts too.
  - `TestVoidDraftAndTemplatePostNothing`.
  - `TestMCPFinalizeAndVoidPost`: the tools write the same entries (mcp package).
  - `TestRecurringAndUsageDraftsPostNothing`: run the recurring trigger and the usage lock → no journal rows. A quotation convert → no journal rows.
- [ ] **Step 2: Run** `./dev test ./internal/invoices/... ./internal/mcp/...`. Red.
- [ ] **Step 3: Implement.**
  - `RevenueShares` groups line amounts by resolved account, using `ListInvoiceLineAccounts` (`l.amount, i.revenue_account_id`, in position order). It allocates `amount` as `RoundHalfEven4(amount × group / Σ lines)`, with the remainder on the largest group. It refuses any account whose type is not `INCOME`.
  - `finalizeInvoice` posts after the status check when the result is OPEN. The memo is `"Invoice <number|id> issued"`. RateDate and IssueDate follow Global Constraints.
  - `voidInvoice`:
    1. the PAID check (as now);
    2. `HasClearedPayments` → refusal;
    3. `ReverseIssued` with memo `"Void of invoice <id>"`;
    4. `false` on an invoice that is not DRAFT, not a template and has `amount > 0` → `ErrLedger` `"invoice <id> has no issue entry — run api backpost-ledger"`.
  - Add `invoice_has_payments` to the refusal codes.
- [ ] **Step 4: Run.** PASS.
  - **Mutation 1:** skip `HasClearedPayments`. `TestVoidWithClearedPaymentRefused` goes red.
  - **Mutation 2:** skip the type check. `TestFinalizeRefusesNonIncomeAccount` goes red.
- [ ] **Step 5: PORT-NOTES 162.**
  - Python posts nothing at finalize or void.
  - Go posts at finalize and reverses at void.
  - Finalize can now answer 400 `ledger_refused` / `fx_rate_missing`.
  - Voiding with cleared payments is refused (Python voided a PARTIAL invoice and left its payments).
  - Name the tests and the two mutations.
- [ ] **Step 6: Commit** `feat(go): invoices post to the ledger at finalize and void`.

---

### Task 3: Payments relieve AR at the issue basis, with realised FX

**Files:**
- Create: `backend/internal/ledger/basis.go`, `backend/internal/ledger/basis_test.go`, `backend/internal/ledger/ledgertest/ledgertest.go` (test-only helper package)
- Modify: `ledger/ledger.go` (`PostPaymentCleared`), `ledger/queries.sql` (`InvoiceARBasis`), `ledger/ledger_test.go`, `payments/*_test.go`, `mcp/*_test.go`, `hosting/hosting_test.go`, `compare/writes_test.go` (fixtures), `backend/PORT-NOTES.md` (163; 161's closing line), `backend/TESTS.md`

**Interfaces:**
- Consumes: Task 1 `PostIssued`, `SourceInvoice`.
- Produces:
  - `type Basis struct { Issued bool; Face, Base decimal.Decimal }`
  - `func InvoiceBasis(ctx context.Context, tx pgx.Tx, invoiceID uuid.UUID) (Basis, error)`:
    - sums AR (`1100`) lines over entries with (`INVOICE`, id) or (`PAYMENT`, any payment whose `invoice_id` is id), reversals included;
    - Face = Σ(debit − credit), Base = Σ(base_debit − base_credit);
    - Issued = an active `INVOICE` entry exists.
  - `func reliefBase(b Basis, relief decimal.Decimal, convertExcess func(decimal.Decimal) (decimal.Decimal, error)) (decimal.Decimal, error)`, shared with Task 5.
  - `func PostPaymentClearedAt(ctx, tx, base string, p Payment, inv Invoice, withheld decimal.Decimal, postedAt time.Time) (Entry, error)` for Task 7. `PostPaymentCleared` keeps its signature and posts at now.
  - `ledgertest.IssueFixture(t *testing.T, tx pgx.Tx, invoiceID uuid.UUID)` posts the issue entry for a fixture invoice inserted by SQL. It reads amount, currency and dates from the row and puts everything on 4000.

- [ ] **Step 1: Failing tests** in `basis_test.go`. Rates: USD→IDR 15000 on 01-01, 16000 on 02-01, 14000 on 03-01.
  - `TestUSDPaidSameCurrencyBooksFXGain`:
    - USD 100 issued 01-01, paid USD 100 on 02-01;
    - Dr 1000 100 (base 1,600,000) / Cr 1100 100 (base 1,500,000) / Cr 4900 100,000;
    - AR face and base both 0 afterwards.
  - `TestUSDPaidSameCurrencyBooksFXLoss`: the same, paid 03-01 → Dr 5900 100,000.
  - `TestTwoPartialsClearARBaseExactly`:
    - USD 100.0001 issued 01-01, paid 33.3333 on 02-01 (proportional base, half-even) and 66.6668 on 03-01 (the remaining base);
    - AR face 0 and base 0.0000;
    - Σ(4900 − 5900) = cash base − issue base.
  - `TestWithholdingAtIssueBasis`: IDR invoice, cash + PPh. AR is relieved at amount+withheld, no FX line. Same in USD: Dr 1300 at the payment rate, and the FX plug covers it.
  - `TestCrossCurrencyRelievesRemainingBasis`:
    - IDR payment against a USD invoice: AR relief = balance at the remaining base;
    - USD payment against an IDR invoice: the base equals the face. The FX amount matches today's for this case.
  - `TestOverReliefExcessAtPaymentRate`: approving an over-payment in the same currency relieves the face at basis and the excess at the payment rate.
  - `TestUnissuedInvoiceRefused`: no INVOICE entry → `ErrLedger` `"invoice <id> has no issue entry — run api backpost-ledger"`.
  - `payments.TestIntakeAgainstUnissuedInvoiceHeld`: a webhook for an exact match on an un-posted invoice → `PENDING_MANUAL_REVIEW` with that sentence among its reasons, and no entry.
  - `payments.TestReverseRestoresBasis`: pay, then reverse, then `InvoiceBasis` equals the issue values again. Paying again books the same FX.
  - `payments.TestARLedgerMatchesBalanceDue`:
    - IDR and USD invoices through finalize → partial pay → reverse → pay → withholding-only;
    - after each step, `InvoiceBasis.Face == balance_due`.
- [ ] **Step 2: Run** `./dev test ./internal/ledger/... ./internal/payments/...`. Red.
- [ ] **Step 3: Implement.**
  - **Relief R:** amount + withheld in the same currency; `inv.BalanceDue` across currencies (as now).
  - **reliefBase:**
    - when Face ≤ 0: Convert(R) at the payment date;
    - when R ≥ Face: Base, plus Convert(R − Face) when R > Face;
    - otherwise: `RoundHalfEven4(R × Base / Face)`.
  - The AR line is credit R at that base, with `fx_rate = round8(base / R)`.
  - **FX plug:** Σ debit bases − reliefBase. Above 0 is Cr 4900, below 0 is Dr 5900, in the base currency, for every currency combination. The old same-currency "AR base = Σ debit bases" branch goes away.
  - Not `Issued` → the refusal.
  - **Fixtures:** every test that inserts an OPEN/SENT/PARTIAL invoice by SQL and then pays it calls `ledgertest.IssueFixture`. Find them with `./dev test ./...`, which is red on the refusal.
- [ ] **Step 4: Run** `./dev test ./...`. PASS.
  - **Mutation:** use the payment date's rate for the AR line. `TestUSDPaidSameCurrencyBooksFXGain` and `TestTwoPartialsClearARBaseExactly` go red.
- [ ] **Step 5: Docs.**
  - **PORT-NOTES 163:**
    - Python relieves AR at the payment date's rate and books FX only across currencies.
    - Go relieves at the issue basis and books realised FX in every combination.
    - A payment against an invoice with no issue entry is refused (intake holds it for review).
  - **PN 161:** append "Superseded in part by 163: same-currency foreign payments now book realised FX."
  - **TESTS.md:** rows whose Go test changed expectation, such as `payments.TestCrossCurrencyManualApprovalBooksFx*` if their numbers move, get `changed (owner decision 2026-11-11, PN 163): …`.
- [ ] **Step 6: Commit** `feat(go): payments relieve receivable at issue basis with realised FX`.

---

### Task 4: Bills post at open; edits re-post; void reverses

**Files:**
- Create: `backend/migrations/00008_default_expense_account.sql`, `backend/internal/bills/posting.go`, `backend/internal/bills/posting_test.go`
- Modify: `bills/store.go` (`updateBill`), `bills/payments.go` (`voidBill`), `bills/routes.go` (`Register` gets `base`; error mapping), `bills/queries.sql` (`ListBillLineAccounts`), `mcp/tools_write.go`, `cmd/api/main.go`, `backend/PORT-NOTES.md` (164)

**Interfaces:**
- Consumes: Task 1.
- Produces:
  - `func UpdateBill(ctx context.Context, tx pgx.Tx, base string, id uuid.UUID, b BillUpdate) (BillOut, error)` (signature change);
  - `func ExpenseShares(ctx context.Context, tx pgx.Tx, bill billsdb.Bill) ([]ledger.Share, error)` (exported for Task 7);
  - `func openBill(ctx, tx, base string, bill billsdb.Bill) error`;
  - `func unpostBill(ctx, tx, id uuid.UUID, memo string) error`;
  - `ledger.DefaultExpenseCode = "6000"`.

- [ ] **Step 1: Migration** `00008_default_expense_account.sql`:
  - Up: `INSERT INTO chart_of_accounts (code, name, type) VALUES ('6000', 'Uncategorised Expense', 'EXPENSE') ON CONFLICT (code) DO NOTHING;` and the same `ON CONFLICT` insert for `2000`, `4000`, `4100`, `4200` (already present in every Alembic-built database; the insert is a guard).
  - Down deletes `6000` only when no journal line references it.
- [ ] **Step 2: Failing tests** in `posting_test.go`, through the routes:
  - `TestPatchOpenPostsBill`: three lines resolve as line account `5100`, item expense `5000`, and none (`6000`). Expect Dr in line proportion after discount, Cr 2000 = amount.
  - `TestBillExpenseTypeRefused`: a line account of type INCOME → 400 `ledger_refused`, the bill stays DRAFT. Types allowed: EXPENSE, COGS, ASSET.
  - `TestOpenBillEditReposts`:
    - an OPEN bill's PATCH with new lines reverses the active entry, sets balance = amount (Python left the balance stale) and posts again;
    - the PATCH without lines/discount/currency/issue_date posts nothing.
  - `TestOpenToDraftReverses`.
  - `TestPatchToPartialRefused`: 400 `bill_status_not_settable` `"PARTIAL comes from recording a payment"`.
  - `TestPatchToVoidUsesVoid`: same as `POST /void` with reason `"status set to VOID via update"`; the balance is 0 and the issue entry is reversed.
  - `TestVoidBillReversesIssue`. The refusal while payments are active is Task 5's, since payments are stored only from there.
  - `TestDraftBillNeverPosts`: create, edit, delete → no journal rows.
  - `TestMCPUpdateBillOpenPosts`.
- [ ] **Step 3: Run** `./dev test ./internal/bills/... ./internal/mcp/...`. Red.
- [ ] **Step 4: Implement.**
  - `updateBill`:
    1. overlay the fields;
    2. reprice;
    3. apply the transition: DRAFT→OPEN `openBill`; OPEN→OPEN with posting fields sent → `unpostBill` + balance := amount + `openBill`; OPEN→DRAFT `unpostBill`; →VOID `voidBill`; →PARTIAL refused; →PAID is Task 5 (refused here with `bill_status_not_settable` until then).
  - `openBill` posts nothing when amount is 0. The memo is `"Bill <bill_number|id> opened"`.
  - `voidBill` reverses with memo `"Void of bill <id>"`. `false` with amount > 0 on an OPEN/PARTIAL bill → the backpost refusal sentence.
- [ ] **Step 5: Run.** PASS.
  - **Mutation:** skip `unpostBill` on an OPEN edit. `TestOpenBillEditReposts` goes red (two active entries refused, or AP doubled).
- [ ] **Step 6: PORT-NOTES 164.**
  - Bill open, edit and void posting.
  - PATCH status limits: PARTIAL is refused, VOID goes through void (notes appended, balance 0, where Python's overlay kept the balance).
  - An OPEN edit re-baselines the balance.
  - The new account `6000`.
- [ ] **Step 7: Commit** `feat(go): bills post to the ledger at open, edit and void`.

---

### Task 5: Bill payments are stored, posted and reversible

**Files:**
- Create: `backend/migrations/00009_bill_payments.sql`, `backend/internal/bills/billpayments.go`, `backend/internal/bills/billpayments_test.go`, `backend/internal/ledger/billpay.go`, `backend/internal/ledger/billpay_test.go`
- Modify: `bills/payments.go` (`recordPayment` stores and posts; the doc text no longer says "nothing is posted"), `bills/store.go` (PATCH →PAID), `bills/queries.sql`, `ledger/queries.sql` (`BillAPBasis`), `reports/aging_test.go`, `backend/PORT-NOTES.md` (165; notes 8 and 73 closing lines)

**Interfaces:**
- Consumes: Task 1, Task 3's `Basis` and `reliefBase`, Task 4's `openBill`.
- Produces:
  - `func BillBasis(ctx, tx, billID uuid.UUID) (Basis, error)`. It sums `2000` lines over (`BILL`, id) and (`BILL_PAYMENT`, the bill's payments), with Face = Σ(credit − debit).
  - `type BillPayment struct { ID uuid.UUID; Amount decimal.Decimal; Currency string; Date time.Time }`
  - `func PostBillPayment(ctx, tx, base string, bp BillPayment, billID uuid.UUID) (Entry, error)` and `PostBillPaymentAt(…, postedAt time.Time)`:
    - Dr `2000` relief R = amount at reliefBase;
    - Cr `1000` amount at the payment date's rate;
    - plug reliefBase − bankBase: above 0 is Cr 4900, below 0 is Dr 5900;
    - a bill with no issue entry → the backpost refusal.
  - Routes (admin):
    - `GET /api/v1/bills/{bill_id}/payments` → `[]BillPaymentOut`;
    - `POST /api/v1/bills/{bill_id}/payments/{bill_payment_id}/reverse`, body `{reason: string, required, non-empty}` → `BillPaymentOut`.
  - `BillPaymentOut` fields: `bill_payment_id, bill_id, amount, currency, payment_date, payer_name, reference, notes, status, source, created_at, reversed_at, reversal_reason`. `BillOut` is unchanged.

- [ ] **Step 1: Migration** `00009_bill_payments.sql`. The table `bill_payments` has:
  - `bill_payment_id uuid PK`;
  - `bill_id uuid NOT NULL REFERENCES bills`;
  - `amount numeric(19,4) NOT NULL CHECK (amount > 0)`;
  - `currency varchar(3) NOT NULL`;
  - `payment_date date NOT NULL`;
  - `payer_name varchar(255)`, `reference varchar(255)`, `notes text`;
  - `status varchar(20) NOT NULL DEFAULT 'CLEARED' CHECK (status IN ('CLEARED','REVERSED'))`;
  - `source varchar(20) NOT NULL DEFAULT 'MANUAL' CHECK (source IN ('MANUAL','STATUS_UPDATE','LEGACY_BACKPOST'))`;
  - `created_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP`, `reversed_at timestamp`, `reversal_reason text`;
  - and the index `ix_bill_payments_bill_id`.
- [ ] **Step 2: Failing tests:**
  - `TestRecordBillPaymentStoresAndPosts`: an IDR bill; the row keeps the request's `payment_date`, `payer_name`, `payer_reference` → `reference`, and `notes`. The entry is Dr 2000 / Cr 1000.
  - `TestUSDBillPaymentBooksFX`: opened at 15000, paid at 16000 → Dr 5900 (loss). Paid at 14000 → Cr 4900.
  - `TestBillPartialsClearAPExactly`: AP face and base are both 0 after two payments.
  - `TestReverseBillPayment`:
    - reversal entry, status REVERSED, `reversed_at`;
    - the balance is restored, status OPEN when balance == amount and PARTIAL otherwise;
    - reversing twice → 400 `bill_payment_not_reversible`.
  - `TestVoidBillWithPaymentRefused`: 400 `bill_has_payments` `"cannot void a bill with payments — reverse them first"`, nothing changed. After the reversal, void succeeds.
  - `TestPatchToPaidRecordsPayment`:
    - OPEN or PARTIAL → PAID records a `STATUS_UPDATE` payment of the balance dated `CURRENT_DATE`, notes `"status set to PAID via update"`;
    - DRAFT → PAID opens it first;
    - MCP `update_bill(status="PAID")` keeps working.
  - `TestBillPaymentRoutesAdminOnly`.
  - `reports.TestApAgingAfterBillPaymentAndReversal`.
- [ ] **Step 3: Run** `./dev test ./internal/bills/... ./internal/ledger/... ./internal/reports/...`. Red.
- [ ] **Step 4: Implement.**
  - Lock order: bill payment `FOR UPDATE` → bill `FOR UPDATE` → entry, on reverse.
  - `recordPayment` keeps its checks and sentences. It then inserts the row (`currency` = the bill's), posts, and sets the balance.
  - The reverse memo is `"Reversal: <reason>"`.
- [ ] **Step 5: Run.** PASS.
  - **Mutation 1:** skip the reversal posting. `TestReverseBillPayment` goes red on the trial balance.
  - **Mutation 2:** drop the active-payment check in void. `TestVoidBillWithPaymentRefused` goes red.
- [ ] **Step 6: PORT-NOTES 165.**
  - Python keeps no row and posts nothing.
  - Go stores, posts, lists and reverses bill payments.
  - PATCH →PAID records a payment.
  - Void is refused while payments are active.
  - A bill-payment list and reverse button in the UI wait for the revamp.
  - Close notes 8 and 73 with "Superseded by 165".
- [ ] **Step 7: Commit** `feat(go): bill payments stored, posted and reversible`.

---

### Task 6: Reports stay right with accrual postings

**Files:**
- Create: `backend/internal/reports/accrual_test.go`
- Modify: `reports/*.go` only if a test finds a defect

**Interfaces:** Consumes the routes of Tasks 2–5 through `apitest`. Produces tests only.

- [ ] **Step 1: Write the tests.** Each one builds a book through the HTTP routes:
  - IDR invoice finalized and half paid;
  - USD invoice finalized 01-01 and fully paid 02-01;
  - USD invoice finalized and open;
  - IDR bill opened and paid;
  - USD bill opened and open;
  - one voided invoice.

  The tests:
  - `TestTrialBalanceBalancesAfterAccrualFlow`: Σ base debit = Σ base credit. 1100 IDR face = the IDR open balance. 4000/4100 credit = issued revenue minus the voided invoice.
  - `TestBalanceSheetARAPAtIssueBasis`:
    - AR base = IDR open balance + open USD × 15000;
    - AP likewise;
    - income includes 4900/5900;
    - assets = liabilities + equity + income − expense (the report's own identity).
  - `TestRevaluationPaidForeignInvoiceLeavesNoResidue`: no 1100-USD bucket from the paid invoice.
  - `TestRevaluationRevaluesOpenForeignARAndAP`: the open USD AR and AP are revalued at the as-of rate.
  - `TestTrialBalanceAsOfBeforeIssueExcludesInvoice`: as-of the day before the issue date → no 1100 line. This checks `posted_at` = issue date.
  - `TestAgingUnchangedByPostings`: AR/AP aging equals the invoice and bill balances.
- [ ] **Step 2: Run** `./dev test ./internal/reports/...`. Any red is a defect: fix it in `reports` (or the owning task's code) with the test as proof. The expected result is green with no code change.
  - **Mutation:** set the issue `posted_at` to now. `TestTrialBalanceAsOfBeforeIssueExcludesInvoice` goes red. Record it in PN 162.
- [ ] **Step 3: Commit** `test(go): reports under accrual postings`.

---

### Task 7: Back-post command

**Files:**
- Create: `backend/internal/backpost/backpost.go`, `backend/internal/backpost/queries.sql` (+ `backpostdb`), `backend/internal/backpost/backpost_test.go`
- Modify: `backend/cmd/api/main.go` (subcommand and usage line), `backend/PORT-NOTES.md` (166), `docs/superpowers/plans/2026-11-10-go-backend-11-compare-switch.md` (Task 6 and Task 9 steps)

**Interfaces:**
- Consumes:
  - Task 1 `PostIssued`, `PostReversalAt`;
  - Task 2 `invoices.RevenueShares`;
  - Task 3 `PostPaymentClearedAt`, `InvoiceBasis`;
  - Task 4 `bills.ExpenseShares`;
  - Task 5 `PostBillPaymentAt`.
- Produces:
  - `func Run(ctx context.Context, pool *pgxpool.Pool, base string, apply bool, w io.Writer) (Report, error)`;
  - `type Report struct { Invoices, Bills, PaymentsRestated, PaymentsPosted, LegacyBillPayments int; Totals []AccountTotal; MissingRates, BadAccounts, Warnings []string }`;
  - `var ErrRefused = errors.New("backpost refused")`;
  - CLI `api backpost-ledger [--apply]`: exit 0 done or dry-run, 1 refused, 2 error.

**Rules (the decisions this task encodes):**
- **Candidates:**
  - invoices: not templates, status OPEN/SENT/PARTIAL/PAID, amount > 0, no active INVOICE entry;
  - bills: status OPEN/PARTIAL/PAID, amount > 0, no active BILL entry.
  - Order: issue date, created_at, id.
- **Preflight (writes nothing).** Collect every missing rate and every share account of the wrong type:
  - the document's rate date;
  - for invoice candidates, each of their CLEARED payments' `payment_date` that needs posting.

  If there are any, print them (`INVOICE <number> USD 2026-01-05: no USD→IDR rate on or before`), return `ErrRefused` and write nothing. This holds in `--apply` too.
- **Per invoice**, locked `FOR UPDATE`:
  1. `PostIssued`.
  2. When the invoice currency differs from the base: reverse every active PAYMENT entry of its CLEARED payments with `PostReversalAt(original posted_at)`.
  3. Re-post each payment, ordered by `payment_date, created_at`, with `PostPaymentClearedAt(original posted_at)`. `BalanceDue` is the running basis Face. Memo: the original's + `" (restated at issue basis)"`.
  4. Python's manual payments (CLEARED with no PAYMENT entry, PN 161) are posted the same way at the payment's `created_at`, in every currency.
  5. Base-currency invoices' existing payment entries are already right and are kept.
- **Justification.** Without the restatement, every paid foreign invoice keeps a base residue in AR. That residue is a gain or loss that is never realised, and the revaluation report would revalue it forever. Restating at the original `posted_at` keeps past reports' dates.
- **Per bill**, locked:
  1. `PostIssued`.
  2. When `amount − balance_due > 0`: insert one `LEGACY_BACKPOST` bill payment for it. It is dated the bill's rate date (Python stored no payment date), so no FX is guessed. Notes: `"paid before the switch; Python kept no payment date"`. Post it with `PostBillPaymentAt(LEAST(rate date, now))`.
- **Warnings** (printed, never fatal):
  - VOID invoices with CLEARED payments (their AR credit stays);
  - VOID bills with `amount ≠ balance` (not posted);
  - documents with a null issue date (created_at used);
  - after posting, every candidate invoice whose `InvoiceBasis.Face ≠ balance_due` (id, number, currency, face, balance).
- **End.** Assert Σ base debit = Σ base credit over all journal lines, else return an error. Print counts and per-account base Dr/Cr totals of the entries written. Commit only with `--apply`; otherwise roll back and print `dry run: nothing written`.
- **Concurrency.** It runs with the API stopped (runbook). If it runs live, the same document locks apply. A concurrent payment reversal of a restated entry is refused (`entry … already reversed`) and can be retried.

- [ ] **Step 1: Failing tests:**
  - `TestBackpostDryRunWritesNothing`;
  - `TestBackpostPostsIssuedInvoicesAndBills`: Python-shaped data inserted by SQL (no entries, Python-style payment entries at payment rate).
  - `TestBackpostRestatesForeignPayments`: after the run, USD AR face 0 and base 0 for the paid USD invoice, and 4900 booked.
  - `TestBackpostKeepsBaseCurrencyPaymentEntries`.
  - `TestBackpostPostsPythonManualPayments`: the payment becomes reversible through `POST /payments/{id}/reverse`.
  - `TestBackpostLegacyBillPayment`.
  - `TestBackpostRefusesOnMissingRates`: lists all of them, writes nothing, exit 1.
  - `TestBackpostIdempotent`: the second run reports 0 of everything.
  - `TestBackpostSkipsDraftsTemplatesVoid`.
  - `TestBackpostWarnsOnFaceMismatch`.
  - `TestBackpostCLIUsage`: `cmd/api`.
- [ ] **Step 2: Run** `./dev test ./internal/backpost/... ./cmd/api/...`. Red.
- [ ] **Step 3: Implement** to the rules above.
- [ ] **Step 4: Run.** PASS.
  - **Mutation 1:** skip the active-entry filter in candidates. `TestBackpostIdempotent` goes red.
  - **Mutation 2:** skip the preflight. `TestBackpostRefusesOnMissingRates` goes red (a partial write).
- [ ] **Step 5: Docs.**
  - **PORT-NOTES 166.** The command, its rules and the restatement justification, plus the way back: Python ignores `bill_payments` and the INVOICE/BILL entries. Payments that Python clears after a way back relieve AR at the payment rate, so a second switch must re-run the dry run and review its face-mismatch warnings.
  - **Plan 11, Task 6:** add Step 4b. On `billing_cmp_go`, run `api backpost-ledger` (dry run). Report its counts, totals, missing rates and warnings to the owner. Fix missing rates (owner enters them) before the switch.
  - **Plan 11, Task 9:** after migrations and before traffic, add `api backpost-ledger --apply` (**OWNER YES REQUIRED**), then the trial-balance check.
- [ ] **Step 6: Commit** `feat(go): api backpost-ledger posts documents issued before accrual`.

---

### Task 8: The comparison classifies accrual differences

**Files:**
- Modify: `backend/internal/compare/rows.go`, `compare/writes.go`, `compare/writes_test.go`, `backend/port/compare-writes.yaml`, `backend/cmd/compare/main.go` (summary line)

**Interfaces:**
- Produces:
  - `RowDiff.Classify string` (`json:"classify,omitempty"`);
  - `WritesReport.Unclassified int`;
  - `func classifyRowDiff(d RowDiff, goRow, pyRow Row, goEntrySource func(entryID string) string) string`.

**Rules (named, never an allowance; every difference is still counted and the exit code is unchanged):**
- `bill_payments` joins `goOnlyTables`, with a comment: Go's own table (PN 165), never in Python.
- A Go-only `journal_entries` row with source_type `INVOICE` → `PN 162`, `BILL` → `PN 164`, `BILL_PAYMENT` → `PN 165`. A Go-only `journal_lines` group whose entry has one of those sources takes the same note.
- A `journal_lines` group of a `PAYMENT` entry → `PN 163` when the two sides are equal once `base_amount_debit`, `base_amount_credit`, `fx_rate` and the 4900/5900 lines are dropped.
- A `bills` row where Python is VOID with `balance_due ≠ 0` and Go is VOID with 0 and a `[voided]` notes line → `PN 164`.
- Anything else stays unclassified.

- [ ] **Step 1: Failing tests:**
  - `TestClassifyAccrualEntries`: a table test over each rule, including an unclassified PAYMENT line diff where the bank line amount differs.
  - `TestWritesReportCountsUnclassified`.
  - `TestBillPaymentsIsGoOnly`.
- [ ] **Step 2: Run** `./dev test ./internal/compare/... ./cmd/compare/...`. Red.
- [ ] **Step 3: Implement.**
  - The report prints `N differences, M unclassified`.
  - In `compare-writes.yaml`:
    - add `finalize-invoice` to `invoice-lifecycle` before the webhook, if it is missing;
    - add a `bill-lifecycle` scenario (create, PATCH `status: OPEN`, record-payment), tables `bills`, `bill_line_items`, `journal_entries`, `journal_lines`;
    - fix the comment above `invoice-lifecycle` (PN 161–163).
- [ ] **Step 4: Run** `./dev test ./internal/compare/...`, then `bash scripts/check.sh`. PASS.
  - **Mutation:** classify every Go-only journal row. The unclassified case in `TestClassifyAccrualEntries` goes red.
- [ ] **Step 5: Commit** `feat(go): compare classifies accrual postings by port note`.

---

### Task 9: Production (each step OWNER YES REQUIRED)

This task runs inside plan 11. Nothing here runs before plan 11 Task 6.

- [ ] **Step 1: Dry run on the production copy (plan 11 Task 6, Step 4b). OWNER YES REQUIRED.**
  - Run `api backpost-ledger` against `billing_cmp_go`.
  - Give the owner the counts, per-account totals, missing rates, and the warning lists: legacy bill payments dated at issue, face mismatches, VOID-with-payments.
  - The owner enters any missing rates in production through the UI and re-runs until the preflight passes.
- [ ] **Step 2: Apply on production at the switch (plan 11 Task 9). OWNER YES REQUIRED.**
  - With Python stopped and Go migrated but not yet serving, run `api backpost-ledger --apply`.
  - Check the trial balance (`GET /api/v1/reports/trial-balance`) balances, and that AR in the balance sheet equals the dashboard's open AR at issue basis.
  - Record the printed totals in the switch log.

---

## Self-review notes

**Coverage of the owner decisions and the brief:**

| Brief item | Task |
| --- | --- |
| Issue and finalize posting | 2 |
| Billing-mode accounts (all exist; no income migration needed) | 2 |
| Withholding at payment | 3 |
| Basis relief and FX | 3 |
| Void, including partly paid | 2, 4, 5 |
| Bill open | 4 |
| Bill payment row and reversal | 5 |
| Default expense `6000` | 4 |
| Templates and drafts never post | 2 (recurring, usage, convert), 4 |
| Back-post: dry run, idempotent, refusal list, owner step, interplay with old payments | 7, 9 |
| Every status path: invoice HTTP and MCP finalize and void; bill PATCH, record, void, MCP `update_bill`; mark-paid needs no posting (balance 0); quotation convert and recurring/usage jobs make DRAFTs | 2, 4, 5 |
| Lock order | Global Constraints |
| Idempotency | 1, 7 |
| Reports | 6 |
| Compare | 8 |
| PORT-NOTES and TESTS.md | 2–7 |

**Types:**
- `Basis` and `reliefBase` are shared by Tasks 3 and 5.
- `FinalizeInvoice` and `UpdateBill` gain `base`; MCP and `main.go` callers change in Tasks 2 and 4.
- `RevenueShares` and `ExpenseShares` are exported where they are written (Tasks 2 and 4), since Task 7 calls them.
