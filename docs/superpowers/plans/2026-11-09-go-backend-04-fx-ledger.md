# Go backend, plan 4: FX rates and the ledger

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Go answers the six FX routes with Python's contract, syncs rates from Frankfurter on demand, and has the double-entry ledger service (payment-cleared posting with withholding and realised FX gain/loss, and reversal) that the payments plan will call. Before any money code, the decimal types get their bounds.

**Architecture:** Task 1 finishes the platform's decimal story (bounds in the types, string-only response schemas, a guard for components shared by request and response). `internal/fx` holds rate lookup and conversion (`GetRate`, `Convert`), the Frankfurter client and the sync core; its routes live beside them. `internal/ledger` is a service with no routes: it takes a `pgx.Tx` and small input structs, so the payments plan can call it inside its own transaction. The weekly sync job is wired in the scheduler plan; this plan builds the core it will call.

**Tech Stack:** Go 1.27, Huma v2.40 on chi v5, pgx v5, sqlc 1.31.1, shopspring/decimal, gokit v0.2.0, `net/http` + `httptest` for Frankfurter, Postgres 16, Docker Compose (through `backend/dev`).

**Spec:** `docs/superpowers/specs/2026-11-08-go-backend-design.md` (Goals 1–3, "Behaviour that must be carried exactly" — money, rounding, the money rules; "Tests"). Plans 2 and 3 define the platform this builds on; `backend/PORT-NOTES.md` lists the differences so far.

## Global Constraints

- `backend-python/` is frozen: never edited. Odd behaviour goes to `backend/PORT-NOTES.md`.
- Errors: `platform.Err(status, code, detail)` problem+json; `detail` is **Python's exact sentence**: `fx rate for this currency pair and date already exists` (409), `no rate found` (404 on `/fx-rates/latest`), `rate not found` (404 on DELETE). Validation failures are Huma 422s with the field's location in `errors`.
- Lists are bare JSON arrays, never `null`; `ORDER BY` is Python's and ends with the primary key.
- Every response field is present; nullable fields are pointers without `omitempty`.
- Decimals: `platform.Dec8` for `numeric(19,8)` (FX rates, `"16000.00000000"`), `platform.Dec4` for `numeric(19,4)`. `decimal.Decimal` end to end; `float64` is refused by lint outside tests.
- `rate_id` is a JSON integer (bigint); dates are `platform.Date`; timestamps `platform.Time`.
- **Rounding:** `fx.Convert` rounds `amount × rate` with `platform.RoundHalfEven4` (Python's `quantize(Decimal("0.0001"))` under the default context, `services/fx.py:72`). No other rounding happens in the ledger.
- **Money rules (prd.md, CLAUDE.md):** only a `CLEARED` payment posts; every entry balances in the base currency; a single-currency entry also balances in its currency; a cross-currency settlement books the base-amount difference to FX Gain `4900` or FX Loss `5900`; a reversal is a new entry with sides swapped, and the original's `reversed_by_entry_id` points at it. Postings happen only where Python makes them (the payments plan calls this service; nothing in this plan posts on its own).
- Tests run on pgtestdb through `internal/apitest`, with Python's seed rates: USD→IDR 16000 and SGD→IDR 12000 on 2026-01-01, source `TEST` (`backend-python/tests/conftest.py:524-540`). Frankfurter is an `httptest.Server`; the database is never faked. Every guard named in a task's "Mutations" line is removed once and the named test watched going red, then restored.
- Commits name their paths (never `git add -A`; `AGENTS.md` and the three Nov 5 docs belong to Codex). Messages end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Branch `feat/go-fx-ledger` from `main`, created with this plan's commit. No push or PR until the final review; merging waits for the owner.

## Review Focus

- A manual FX rate typed with more than 8 decimals, or with more than 11 integer digits, must be a 422 at `rate`, not a database overflow 500 (the Vue form sends numbers): Task 1 `TestDec8RefusesNinePlacesAndTwelveDigits` and Task 3 `TestCreateRateNinePlacesIs422`.
- Two "Sync now" clicks at once must leave one row per pair and date, never a unique-violation 500: Task 3 `TestSyncConcurrentIsIdempotent`.
- Frankfurter returning a rate as a long float literal (`16234.56789`) must be stored exactly as written, not through a float: Task 3 `TestSyncKeepsProviderLiteral`.
- A same-currency payment with withholding must relieve AR by exactly the sum of the debit base amounts, even when each debit rounds separately: Task 4 `TestSameCurrencyWithholdingRelievesExactDebitBase` (USD with a rate whose products round differently).
- Reversing an entry twice, even from two transactions at once, must fail the second time, never create two reversals: Task 4 `TestReverseTwiceFails` and `TestReverseConcurrentOnlyOnce`.

---

### Task 1: Decimal bounds, string-only response decimals, shared-component guard

These close the plan-3 final review's "before the invoices plan" items.

**Files:**
- Modify: `backend/internal/platform/types.go` (Dec2/Dec4/Dec8 unmarshal), `backend/internal/platform/nullable.go` (or the response-walk hook beside it), `backend/internal/items/service.go` and `backend/internal/customers/service.go` (drop checks the types now make)
- Test: `backend/internal/platform/types_test.go`, `backend/cmd/api/routes_test.go`

**Interfaces:**
- Produces: `Dec2`, `Dec4`, `Dec8` refuse, when decoding a JSON number **or** string, more decimal places than their scale (trailing zeros beyond the scale are allowed: `1.50000` is fine for Dec4) or more integer digits than `19 − scale` for Dec4/Dec8 and `5 − 2` for Dec2 (its column is `numeric(5,2)`). The refusal is a 422 at the field with `detail` `Request validation failed` and message `value has too many decimal places (max N)` or `value has too many digits (max M before the point)`.
- Produces: in every **response** schema, a decimal is `{"type":"string","pattern":…}` (nullable ones also accept null); request schemas keep `oneOf [string, number]`.
- Produces: a test that fails when a component schema is reachable from both a request body and a response body (today none is), naming the component.

- [ ] **Step 1: Failing tests**

`TestDec4RefusesFivePlaces` (number `1.23456` and string `"1.23456"` → 422 at the field, message names max 4), `TestDec4AllowsTrailingZeros` (`1.50000` → `"1.5000"`), `TestDec4RefusesSixteenDigits` (`1234567890123456` → 422, message names max 15), `TestDec8RefusesNinePlacesAndTwelveDigits`, `TestDec2Bounds` (`999.99` ok, `1000` and `1.234` refused), `TestResponseDecimalsAreStringOnly` (cmd/api: every decimal property reachable from a 2xx response has `type` `string` or `["string","null"]`, never `oneOf` with `number`), `TestNoComponentSharedByRequestAndResponse` (cmd/api).

Run: `cd backend && ./dev test ./internal/platform/ ./cmd/api/` — Expected: FAIL.

- [ ] **Step 2: Implement, simplify callers**

The string pattern on request schemas keeps the scale; the integer-digit bound goes in it too (`^-?[0-9]{1,15}(\.[0-9]{1,4})?$` for Dec4, etc.). Items' `checkPrices` keeps only "≥ 0"; customers keep their `default_wht_rate` places check only if the type does not already give the same 422 (it now does: remove it, keep the test).

- [ ] **Step 3: Run, mutate**

Run the whole module: `./dev test` — Expected: PASS, including items' and customers' existing price and rate tests. Mutations: drop the places check in `Dec4.UnmarshalJSON` (`TestDec4RefusesFivePlaces` number case red); drop the response rewrite (`TestResponseDecimalsAreStringOnly` red naming `ItemOut.default_unit_price`).

- [ ] **Step 4: Commit** `feat(go): decimal bounds in the types; string-only response decimals; shared-component guard`

---

### Task 2: FX settings, rate lookup and conversion

**Files:**
- Modify: `backend/internal/platform/config.go`, `backend/internal/apitest/apitest.go` (seed rates)
- Create: `backend/internal/fx/fx.go`, `backend/internal/fx/queries.sql`, generated `backend/internal/fx/fxdb/`
- Modify: `backend/sqlc.yaml`
- Test: `backend/internal/platform/config_test.go`, `backend/internal/fx/fx_test.go`

**Interfaces:**
- Produces in `platform.Config` (Python's names and defaults, `backend-python/app/config.py:48, 86-91`):
```go
BaseCurrency         string        // BASE_CURRENCY, default "IDR"; 3 letters, upper-cased
FXProviderBaseURL    string        // FX_PROVIDER_BASE_URL, default "https://api.frankfurter.dev/v1"
FXProviderTimeout    time.Duration // FX_PROVIDER_TIMEOUT_SECONDS, default 15 (seconds, may be fractional)
FXSyncCurrencies     []string      // FX_SYNC_CURRENCIES: a JSON list (pydantic's format) or comma-separated; default ["USD","SGD"]; upper-cased
```
- Produces in `fx`:
```go
var ErrRateMissing = errors.New("fx rate missing") // wrapped with Python's text: "no fx rate for USD->IDR on or before 2026-01-01"
type Conversion struct{ BaseAmount, Rate decimal.Decimal }
func GetRate(ctx context.Context, q *fxdb.Queries, from string, on time.Time, to string) (decimal.Decimal, error) // upper-cases; from == to → 1 without a query; newest rate with as_of_date <= on
func Convert(ctx context.Context, q *fxdb.Queries, amount decimal.Decimal, from string, on time.Time, to string) (Conversion, error) // BaseAmount = RoundHalfEven4(amount × rate)
```
`fxdb.Queries` works on a pool or a `pgx.Tx` (`fxdb.New(tx)`), so the ledger can convert inside its transaction.
- Produces: `apitest.New` seeds Python's two test rates (Global Constraints) in every test database.

- [ ] **Step 1: Failing tests**

`TestConfigFXDefaults`, `TestConfigReadsJSONFXSyncCurrencies` (`'["usd","eur"]'` → `["USD","EUR"]`), `TestConfigRefusesBadBaseCurrency` (`"RUPIAH"` → error naming `BASE_CURRENCY`), `TestGetRateBaseIsOne` (no query: works with a nil-pool-safe path — assert by passing a `Queries` on a closed pool), `TestGetRatePicksNewestOnOrBefore` (rates on 01-01 and 01-10; on 01-09 → the 01-01 rate; on 01-10 → the 01-10 one), `TestGetRateMissing` (`errors.Is(err, ErrRateMissing)`, text `no fx rate for EUR->IDR on or before 2026-01-01`), `TestGetRateUpperCases`, `TestConvertRoundsHalfEven` (amount and rate chosen so the product ends in exactly `…5` at the 5th place, once with an even and once with an odd 4th digit), `TestApitestSeedsPythonRates`.

Run: `./dev test ./internal/platform/ ./internal/fx/ ./internal/apitest/` — Expected: FAIL.

- [ ] **Step 2: Implement, run, mutate**

Expected: PASS. Mutations: compare `as_of_date < on` instead of `<=` (newest-on-or-before test red); round with `Round` (half-up) in `Convert` (half-even test red).

- [ ] **Step 3: Commit** `feat(go): FX settings, rate lookup and conversion`

---

### Task 3: FX routes and the Frankfurter sync

**Files:**
- Create: `backend/internal/fx/api.go`, `routes.go`, `provider.go`, `sync.go`
- Modify: `backend/internal/fx/queries.sql`, `backend/cmd/api/main.go` (`register`)
- Test: `backend/internal/fx/routes_test.go`, `provider_test.go`

**Interfaces:**
- Produces: `func Register(api huma.API, pool *pgxpool.Pool, cfg platform.Config, client *http.Client)`; `type RateOut struct { RateID int64 "rate_id"; FromCurrency, ToCurrency string; Rate platform.Dec8; AsOfDate platform.Date "as_of_date"; Source string; CreatedAt platform.Time "created_at" }`; `type SyncResult struct { Created, Updated, Fetched int; Pairs []string; Source string }`; `func Sync(ctx context.Context, pool *pgxpool.Pool, cfg platform.Config, client *http.Client) (SyncResult, error)` (the scheduler plan's weekly job calls this); `var ErrProvider` (wrapped with Python's text).

| Route | Python (`routers/accounting.py`) | Kept |
|---|---|---|
| `GET /api/v1/accounting/base-currency` | `:129-131` | `{"base_currency": cfg.BaseCurrency}` |
| `GET /api/v1/accounting/fx-rates?from_currency=&to_currency=` | `:134-146` | each filter 3 characters (else 422), upper-cased; `ORDER BY as_of_date DESC, rate_id DESC` |
| `POST /api/v1/accounting/fx-rates` `{from_currency 3, to_currency 3, rate Dec8 > 0, as_of_date, source="MANUAL" 1–50}` | `:149-172` | 201; currencies upper-cased; `uq_fx_rates_triple` → 409 |
| `POST /api/v1/accounting/fx-rates/sync` | `:175-191` | 200 `SyncResult`; provider failure → 502 with the provider error's text as `detail`, nothing written |
| `GET /api/v1/accounting/fx-rates/latest?from_currency=&to_currency=&on_date=` | `:194-215` | `from_currency` required (3); `to_currency` default base; newest by `as_of_date DESC, rate_id DESC`, `<= on_date` when given; 404 `no rate found` |
| `DELETE /api/v1/accounting/fx-rates/{rate_id}` | `:218-228` | 204; 404 `rate not found` |

`/fx-rates/sync` and `/fx-rates/latest` are literal paths: they must not be taken by `{rate_id}` (only DELETE has the parameter; test it anyway).

Provider (`services/fx_provider.py`): for each sync currency, upper-cased, skipping the base, `GET {base_url}/latest?base=SRC&symbols=TARGET` with `cfg.FXProviderTimeout`; a transport error or non-2xx → `fx provider request failed for SRC->TARGET: <error>`; a body that is not JSON → `fx provider returned invalid JSON for SRC->TARGET: <error>`; a missing `rates[TARGET]` → that pair is skipped; a bad `date` → `fx provider returned invalid date for SRC->TARGET: <value>`. The rate is decoded as the JSON literal (`json.Number` → `decimal`), never through `float64`. Fetch every pair before writing anything.

Sync (`workers/tasks/fx_sync.py:13-49`), in one transaction: per fetched pair, `INSERT … ON CONFLICT ON CONSTRAINT uq_fx_rates_triple DO UPDATE SET rate = EXCLUDED.rate, source = 'FRANKFURTER' WHERE fx_rates.rate <> EXCLUDED.rate OR fx_rates.source <> 'FRANKFURTER' RETURNING (xmax = 0) AS inserted`; an inserted row counts `created`, an updated one `updated`, no row returned counts neither. `pairs` lists `SRC->TARGET` for every fetched pair; `source` is `FRANKFURTER`.

- [ ] **Step 1: Failing tests**

Routes: `TestBaseCurrency`, `TestListRatesOrderAndFilters` (seed plus two more; lower-case filter works), `TestListRatesFilterLengthIs422`, `TestCreateRate201` (`rate` `"16250.5"` → `"16250.50000000"`, currencies upper-cased), `TestCreateRateDuplicateIs409`, `TestCreateRateNinePlacesIs422`, `TestCreateRateZeroIs422`, `TestLatestRate` (with and without `on_date`, default `to_currency`), `TestLatestRateNotFound404`, `TestDeleteRate204ThenGone`, `TestDeleteUnknown404`, `TestLiteralPathsNotSwallowed` (`GET /fx-rates/latest?...` is the latest route; `DELETE /fx-rates/latest` is 405 or 422, not a delete), `TestFXRoutesRequireAdmin`.
Sync (Frankfurter as `httptest.Server`, URL in `cfg.FXProviderBaseURL`): `TestSyncCreatesThenIdempotent` (first run created 2; second run with the same answers created 0, updated 0; a changed rate → updated 1), `TestSyncOverwritesManualSameDate` (a MANUAL row on the provider date becomes FRANKFURTER, counted updated), `TestSyncLeavesOtherDates`, `TestSyncSkipsMissingPair`, `TestSyncSkipsBaseCurrency`, `TestSyncProviderErrorIs502WritesNothing` (second pair answers 500 → 502, `detail` starts `fx provider request failed for SGD->IDR:`, no row for the first pair either), `TestSyncInvalidJSONIs502`, `TestSyncInvalidDateIs502`, `TestSyncKeepsProviderLiteral`, `TestSyncConcurrentIsIdempotent` (two syncs at once, `-count=5` when mutating: one row per pair and date, no 500), `TestSyncTimeout` (a server that sleeps past a 100 ms timeout → 502).

Run: `./dev test ./internal/fx/` — Expected: FAIL.

- [ ] **Step 2: Implement, run, mutate**

Expected: PASS. Mutations: write each pair as it is fetched instead of after all fetches (`TestSyncProviderErrorIs502WritesNothing` red); decode the rate through `float64` (`TestSyncKeepsProviderLiteral` red); drop the `WHERE` on the upsert (`TestSyncCreatesThenIdempotent` second-run `updated` red); select-then-insert instead of the upsert (`TestSyncConcurrentIsIdempotent` red with a 500).

- [ ] **Step 3: Commit** `feat(go): FX rate routes and Frankfurter sync`

---

### Task 4: The ledger service

**Files:**
- Create: `backend/internal/ledger/ledger.go`, `backend/internal/ledger/queries.sql`, generated `backend/internal/ledger/ledgerdb/`
- Modify: `backend/sqlc.yaml`
- Test: `backend/internal/ledger/ledger_test.go`

**Interfaces:**
- Consumes: `fx.Convert`, `platform.Config.BaseCurrency`.
- Produces:
```go
const (BankCode = "1000"; ReceivableCode = "1100"; PrepaidTaxCode = "1300"; FXGainCode = "4900"; FXLossCode = "5900")
var ErrLedger = errors.New("ledger") // every refusal wraps it, with Python's text
type Payment struct { ID uuid.UUID; Status string; Amount decimal.Decimal; Currency string; Date time.Time }
type Invoice struct { ID uuid.UUID; Currency string; BalanceDue decimal.Decimal }
type Line struct { AccountID int32; Debit, Credit decimal.Decimal; Currency string; BaseDebit, BaseCredit, FXRate decimal.Decimal }
type Entry struct { ID uuid.UUID; SourceType string; SourceID uuid.UUID; Memo *string; Lines []Line }
func PostPaymentCleared(ctx context.Context, tx pgx.Tx, baseCurrency string, p Payment, inv Invoice, withheld decimal.Decimal) (Entry, error)
func PostReversal(ctx context.Context, tx pgx.Tx, baseCurrency string, originalID uuid.UUID, memo *string) (Entry, error)
```
Rules, line for line from `backend-python/app/services/ledger.py`:
- Refusals (each an `ErrLedger` with Python's text): status not `CLEARED` (`refuse to post ledger for payment <id> with status '<status>' — only CLEARED may post`); `withheld < 0` (`withheld tax cannot be negative (got <n>)`); a missing account code (`chart_of_accounts missing code <code>`); cross-currency with `BalanceDue <= 0` (`cross-currency settlement requires invoice balance_due > 0 (invoice <id> balance=<n>)`); a missing original or one already reversed (`entry <id> not found` / `entry <id> already reversed`); an unbalanced entry (`journal entry unbalanced in base currency: net=<n>` / `journal entry unbalanced in <ccy>: net=<n>`). `fx.ErrRateMissing` passes through unchanged.
- A line converts its one non-zero side with `fx.Convert` on the payment date; `FXRate` is that conversion's rate; the other base side is 0.
- Bank: DR `p.Amount` in `p.Currency`. Withholding (only if `withheld > 0`): DR `withheld` in `inv.Currency` to 1300.
- Same currency: AR CR `p.Amount + withheld` in `p.Currency`, and its base credit is set to the **exact sum of the debit lines' base debits** (not its own conversion).
- Cross currency: AR CR `inv.BalanceDue` in `inv.Currency`; `diff = bank base + withholding base − AR base`; `diff < 0` → DR `−diff` to 5900, `diff > 0` → CR `diff` to 4900, both in `baseCurrency` (rate 1 by `GetRate`).
- Entry: `source_type` `PAYMENT`, `source_id` the payment id, memo `Payment <pid> cleared against invoice <iid>`; balance checked before any insert (base always; per currency only when every line has one currency); `posted_at` from the database default.
- Reversal: lock the original (`SELECT … FOR UPDATE`), refuse as above, insert an entry with the original's `source_type`/`source_id`, memo `memo` or `Reversal of <original id>`, every line with debit↔credit and base debit↔base credit swapped and the same rate; then set the original's `reversed_by_entry_id`.

- [ ] **Step 1: Failing tests** (Go-only: Python covers the ledger through the reconciliation tests, which the payments plan ports; journal rows need no payment or invoice row, `source_id` has no foreign key)

`TestSameCurrencyIDR` (IDR 1,000,000 → bank DR / AR CR, base = amount, rate 1, balanced), `TestSameCurrencyUSD` (USD 100 at the seed rate → base 1,600,000.0000 on both), `TestSameCurrencyWithholdingRelievesExactDebitBase` (insert a USD rate whose products round separately so `base(amount) + base(withheld) ≠ base(amount + withheld)`; AR base credit equals the sum), `TestCrossCurrencyGain` (USD payment on an IDR invoice where bank base > balance → CR 4900 for the difference, entry balanced in base, not checked per currency), `TestCrossCurrencyLoss` (→ DR 5900), `TestCrossCurrencyExact` (no plug line), `TestCrossCurrencyWithholdingCountsInDiff`, `TestRefusesNonCleared` (`PENDING_MANUAL_REVIEW` → `ErrLedger`, no rows written), `TestRefusesNegativeWithheld`, `TestRefusesCrossCurrencyZeroBalance`, `TestRateMissingPassesThrough` (EUR payment, no rate → `errors.Is(err, fx.ErrRateMissing)`, no rows), `TestMissingAccountCode` (rename 1300 inside the test transaction → error naming `1300`), `TestReversalSwapsSidesAndLinks`, `TestReversalDefaultMemo`, `TestReverseTwiceFails`, `TestReverseUnknownFails`, `TestReverseConcurrentOnlyOnce` (two transactions reverse the same entry; exactly one succeeds, one reversal row exists), `TestUnbalancedEntryRefused`, `TestLinesAreOneSided` (every inserted line has exactly one non-zero side, matching `ck_journal_lines_one_sided`).

Run: `./dev test ./internal/ledger/` — Expected: FAIL.

- [ ] **Step 2: Implement, run, mutate**

Expected: PASS. Mutations, each reverted: drop the CLEARED check (`TestRefusesNonCleared` red); give AR its own conversion in the same-currency case (`TestSameCurrencyWithholdingRelievesExactDebitBase` red with an unbalanced-entry error); swap the gain/loss accounts (`TestCrossCurrencyGain` red); drop the base-balance check (`TestUnbalancedEntryRefused` red: an in-package test that hands the balance check lines off by 0.0001 in base, and lines balanced in base but not in their single currency, and expects both refusals); drop the `FOR UPDATE` on the original (`TestReverseConcurrentOnlyOnce` red at `-count=5`).

- [ ] **Step 3: Commit** `feat(go): ledger service — payment cleared with withholding and FX gain/loss, reversal`

---

### Task 5: Inventories, port notes, checks

**Files:**
- Modify: `backend/ROUTES.md`, `backend/TESTS.md`, `backend/PORT-NOTES.md`, `backend/README.md` (the FX settings)

- [ ] **Step 1: ROUTES.md** — run `python backend/port/inventory.py`; mark the six FX routes `ported` (`go` = `fx`). The five report routes under `/accounting/reports/` stay `todo` with note `reports plan`.

- [ ] **Step 2: TESTS.md** — no Python test ports in this plan (the ledger's Python coverage is the reconciliation tests, ported in the payments plan). Add one line under the counts: `conftest._seed_test_fx_rates` is carried by `apitest.New` (Task 2, `TestApitestSeedsPythonRates`).

- [ ] **Step 3: PORT-NOTES.md** (same format as entries 10–25, each with "After the switch"):
  - FX sync 502 `detail`: same leading sentence, but the trailing error text is Go's (`net/http`), not httpx's.
  - Sync writes nothing when any pair fails; Python also wrote nothing (it raised before commit) — confirm against `routers/accounting.py:184-189` and say so.
  - Decimal bounds: an FX rate or money value with too many places or digits is a 422 in Go where Python's pydantic gave its own 422 (rates, `decimal_places=8`) or the database a 500 (unbounded fields).
  - Concurrent "Sync now": Python's select-then-insert could hit `uq_fx_rates_triple` (500); Go upserts.
  - Ledger reversal under concurrency: Python had no row lock (`services/ledger.py:222-251`); Go locks the original.

- [ ] **Step 4: Full checks**

Run: `bash scripts/check.sh`
Expected: `go tests: N passed, none skipped` with N > 250, `all checks passed`.

- [ ] **Step 5: Commit** `docs(go): FX routes ported; port notes for FX and the ledger` (local; the controller pushes and opens the PR after the final review).
