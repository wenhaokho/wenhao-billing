# Go backend, plan 3: master data

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Go answers the 28 master-data routes (chart of accounts, vendors, items, customers, business profile and payment accounts, users) with Python's contract, on the foundation merged in plan 2.

**Architecture:** One package per feature under `backend/internal/` (`accounts`, `vendors`, `items`, `customers`, `business`), each with `api.go` (request and response types), `routes.go` (`Register`), `queries.sql` and generated `<name>db/`. The user-admin routes join `internal/auth`, which already owns `UserOut`, hashing and the password queries. Task 1 adds what every feature shares: a Python-compatible email type, a constraint-error mapper, an API test harness, and the plan-2 carry-overs. A PATCH loads the row `FOR UPDATE`, applies the `Optional` fields in Go and writes the whole row back in the same transaction.

**Tech Stack:** Go 1.27, Huma v2.40 on chi v5, pgx v5, sqlc 1.31.1, shopspring/decimal, gokit v0.2.0 (`apierr`, `db`, `testkit`), Postgres 16, Docker Compose (through `backend/dev`).

**Spec:** `docs/superpowers/specs/2026-11-08-go-backend-design.md` (Goals 1–2, "Behaviour that must be carried exactly", "Database", "Tests"). Plan 2 (`docs/superpowers/plans/2026-11-09-go-backend-02-foundation.md`) defines the platform this builds on.

## Global Constraints

- `backend-python/` is frozen: never edited. Odd behaviour goes to `backend/PORT-NOTES.md`; a deliberate difference is a port note that says "Go does X instead".
- Errors are `platform.Err(status, code, detail)` problem+json. `detail` is **Python's exact sentence**: `account not found`, `account code already exists`, `vendor not found`, `item not found`, `customer not found`, `user not found`, `email already in use`, `email already exists`, `Duplicate currency in payment accounts`, `Unsupported currency: <sorted, comma+space joined>`, `At most one account can be the default`, `Unknown withholding tax type: <type>`, `Withholding tax rate must be greater than 0 and less than 100`. `code` is snake_case and stable (for example `account_not_found`).
- Lists are bare JSON arrays, never `null`; `ORDER BY` is Python's and ends with the primary key. None of these routes takes a `limit`.
- Every response field is present: nullable fields are pointers without `omitempty`; slices are made non-nil before encoding.
- Decimals: `platform.Dec4` for `numeric(19,4)` and for withholding rates `numeric(7,4)` (`"2.0000"`); request decimals accept a JSON number or string. `float64` is refused by lint outside tests.
- Timestamps are `platform.Time` (UTC, `Z`); ids are UUID strings except `account_id` and payment-account `id`, which are JSON integers.
- PATCH: `platform.Optional[T]` fields; omitted leaves the column alone, `null` clears a nullable column. Unknown request fields are ignored.
- Every operation is `platform.Admin(...)`. The fail-closed test in `cmd/api` (`TestEveryOperationIsAdminOrPublic`) must keep passing as features register.
- No new migration in this plan: every table exists in `00001_baseline.sql`. Inserts write every column Python's ORM defaults fill client-side (`active`, `default_currency`, `is_sold`, …); `created_at` and `updated_at` come from the database's `CURRENT_TIMESTAMP`, as in Python.
- Tests use pgtestdb through `internal/apitest` (Task 1), never a mocked database. Every guard listed in a task's "Mutations" line is removed once, the named test is watched going red for the stated reason, and the guard is restored.
- Go runs only through `backend/dev` in the root Compose project; never create a Compose project or network.
- Commits name their paths (never `git add -A`; `AGENTS.md` and three Nov 5 docs belong to Codex and stay uncommitted). Messages end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Branch `feat/go-master-data` from `main`, created with this plan's own commit. Merging waits for the owner.

## Review Focus

- A customer created from the frontend sends `"default_wht_rate": 3` (a number) and sees `"3.0000"` back; a string `"3"` must give the same: Task 6 `TestCustomerWhtRateAcceptsNumberAndString`.
- A PATCH that sends `"billing_cc_emails": []` must clear the list, while one that omits it must keep it: Task 6 `TestCustomerCCClearAndKeep`.
- An explicit `null` for a NOT NULL column (`"name": null` on a vendor PATCH) must be a 422 naming the field, never a 500 from the database: Task 4 `TestVendorPatchNullNameIs422`.
- Two concurrent `PUT /business-profile/payment-accounts` must leave exactly one request's accounts, never a mix or a unique-violation 500: Task 7 `TestReplacePaymentAccountsConcurrent`.
- A hosting item PATCH that sends only `cloudflare_target` must update the existing target row, not insert a second one (the column is unique): Task 5 `TestItemPatchUpdatesExistingTarget`.

---

### Task 1: Shared pieces for features, and plan 2's carry-overs

**Files:**
- Create: `backend/internal/platform/email.go`, `backend/internal/platform/pgerr.go`, `backend/internal/apitest/apitest.go`
- Modify: `backend/dev` (`generated`), `backend/internal/platform/types.go` (per-scale patterns, parse errors), `backend/internal/platform/optional.go` (embedded structs, map values)
- Test: `backend/internal/platform/email_test.go`, `pgerr_test.go`, `types_test.go`, `optional_test.go`; `backend/internal/apitest/apitest_test.go`; `backend/cmd/api/routes_test.go`

**Interfaces:**
- Produces:
```go
// platform/email.go: Python's EmailStr (pydantic + email-validator, deliverability off).
type Email string // UnmarshalJSON: trims, validates, lowercases the domain; a bad address is a 422 whose detail is "value is not a valid email address"
func NormalizeEmail(s string) (string, bool)
// platform/pgerr.go
type Constraint struct{ Status int; Code, Detail string }
func MapConstraint(err error, known map[string]Constraint) error // a *pgconn.PgError whose ConstraintName is in known → platform.Err of that entry; any other error unchanged
// apitest
type Deps struct { Pool *pgxpool.Pool; Sessions platform.Sessions; Mail *Mail /* records sent messages */; Config platform.Config; Clock *testkit.Clock }
type App struct { Deps; Handler http.Handler; Cookie string /* a signed-in admin's billing_session request cookie */; AdminID uuid.UUID }
func New(t *testing.T, register func(api huma.API, d Deps)) *App // fresh pgtestdb database, clock at 2026-01-15T09:00:00Z, config with Environment "test" and a 32-byte secret, one admin user whose password_hash is a placeholder (it never logs in), session issued with Sessions.Issue, not through /auth/login
func (a *App) Do(t *testing.T, method, path string, body any) *httptest.ResponseRecorder // body nil, a string (sent verbatim) or a value (JSON-encoded); sends a.Cookie
func (a *App) Anon(t *testing.T, method, path string, body any) *httptest.ResponseRecorder // the same without the cookie
func Decode[T any](t *testing.T, rec *httptest.ResponseRecorder, status int) T // fails the test on another status, showing the body
```

`NormalizeEmail` keeps email-validator's rules that the owner can hit: trim surrounding space; exactly one `@` at the last position, local part 1–64 bytes with no whitespace; domain lower-cased, at least one `.`, labels 1–63 characters of letters (Unicode allowed), digits and inner hyphens; the last label is not one of `arpa`, `invalid`, `local`, `localhost`, `onion`, `test`. The local part keeps its case.

- [ ] **Step 1: Failing tests**

- `TestNormalizeEmail`, table-driven, with values observed from the frozen Python on 2026-11-09: `"AP@Beta.COM"`→`"AP@beta.com"`; `" a@b.co "`→`"a@b.co"`; `"x@exämple.com"`→ itself; `"Ä@example.com"`→ itself; refused: `"a@localhost"`, `"a@b"`, `"a@example.test"`, `"not-an-email"`, `"a b@x.com"`, `""`.
- `TestEmailFieldIs422`: a throwaway operation with body `{email platform.Email}` → `"nope"` gives 422 with `detail` `value is not a valid email address`; `"A@X.COM"` reaches the handler as `"A@x.com"`.
- `TestMapConstraint`: insert a duplicate `chart_of_accounts.code` on a real database → `MapConstraint(err, {"chart_of_accounts_code_key": {409, "account_code_exists", "account code already exists"}})` is that problem; an unlisted constraint and a non-pg error come back unchanged.
- `TestDecimalSchemaPatternPerScale`: the OpenAPI string branch of `Dec4` has pattern `^-?[0-9]+(\.[0-9]{1,4})?$`, `Dec8` `{1,8}`, `Dec2` `{1,2}`.
- `TestBadDecimalDateTimeIs422WithSentence`: bodies with `"amount": "abc"`, `"day": "2026-13-01"`, `"at": "yesterday"` → 422 whose `detail` is a sentence and whose `errors[0].location` is `body.amount` (`body.day`, `body.at`), never a Go type name.
- `TestRelaxReachesEmbeddedAndMapFields`: an omitted `Optional` field inside an embedded struct and inside a `map[string]struct{…}` value is not required.
- `TestResponsesNeverOmitFields` (`cmd/api`): walk every response schema of the real server's OpenAPI; every object property is listed in `required`. (Huma marks a field required unless it has `omitempty`; this pins "never omitted".)
- `TestApitestAdminReachesAdminRoute`: `apitest.New` with the auth routes → `Do GET /api/v1/auth/me` is 200 and `Anon` is 401.

Run: `cd backend && ./dev test ./internal/platform/ ./internal/apitest/ ./cmd/api/`
Expected: FAIL (undefined `NormalizeEmail`, `MapConstraint`, `apitest.New`).

- [ ] **Step 2: Implement**

The parse errors of `Dec*`, `Date` and `Time` must reach the client at the field's location with these sentences: `value is not a valid decimal`, `value is not a valid date (YYYY-MM-DD)`, `value is not a valid timestamp (RFC 3339)`. First see what Huma already reports (its schema validation runs before unmarshalling and may catch `"abc"` by pattern but not `2026-13-01`); make up the rest in the types (an error implementing `huma.ErrorDetailer`) or, if Huma drops the location there, with a `huma.Resolver` on the body types. The test decides.

- [ ] **Step 3: Scope `./dev generated` to generated files**

Today it fails on any uncommitted file under `internal/`, so `scripts/check.sh` cannot run mid-change. Read every `out:` path from `sqlc.yaml` (a `grep`/`sed` over the file; no YAML tool), and run the `git diff` and `git status --porcelain` checks only on those directories plus `openapi.json`.

Mutation: edit one generated line in `internal/auth/authdb/queries.sql.go` → `./dev generated` exits 1 naming it; restore. Then create an untracked `internal/auth/scratch.go` → `./dev generated` exits 0; delete it.

- [ ] **Step 4: Run, mutate**

Run Step 1's command — Expected: PASS. Mutations, each reverted: skip the reserved-TLD check (`a@example.test` row red); drop `strings.ToLower` on the domain (`AP@Beta.COM` row red); add `omitempty` to `auth.UserOut.DisplayName` (`TestResponsesNeverOmitFields` red naming `display_name`).

- [ ] **Step 5: Commit** `feat(go): Python email type, constraint mapping, API test harness; generated check scoped to generated files`

---

### Task 2: Withholding tax helpers

**Files:**
- Create: `backend/internal/withholding/withholding.go`
- Test: `backend/internal/withholding/withholding_test.go`

**Interfaces:**
- Consumes: `platform.RoundWithholding(d, currency)` (HALF_UP, 0 places for IDR, 2 otherwise).
- Produces:
```go
var Types = []string{"NONE", "PPH21", "PPH23"}
var DefaultRates = map[string]decimal.Decimal{"PPH21": 2.5, "PPH23": 2} // as decimals
var Labels = map[string]string{"PPH21": "PPh 21", "PPH23": "PPh 23"}
type Result struct{ Total, Amount, Base decimal.Decimal }
func Compute(base decimal.Decimal, currency, whtType string, rate *decimal.Decimal, grossUp bool) Result
func ExpectedCash(balanceDue, whtAmount, whtBooked decimal.Decimal) decimal.Decimal // max(0, balance − (amount − booked))
func Normalize(whtType string, rate *decimal.Decimal, grossUp bool) (string, *decimal.Decimal, bool, error) // error text is Python's sentence
```
Semantics are `backend-python/app/services/withholding.py:33-70`: `NONE`, a nil rate or a zero base gives `{base, 0, base}`; gross-up total is `RoundWithholding(base / (1 − rate/100))` and amount `total − base`; otherwise amount is `RoundWithholding(base × rate / 100)`. Divide with `base.DivRound(x, 28)` (28 places, at least Python's 28 significant digits for these magnitudes) before rounding; never change the global `decimal.DivisionPrecision`.

- [ ] **Step 1: Failing tests** (one Go test per Python test, same name in Go case)

`TestPph23NoGrossUp` (10000000 IDR 2% → total 10000000, amount 200000), `TestPph23GrossUp` (→ 10204082, 204082, total − amount = base), `TestPph21UsdGrossUpTwoDecimals` (1000 USD 2.5% → 1025.64, 25.64), `TestNoneType`, `TestZeroBaseGrossUp`, `TestRoundHalfUp` (IDR 0.5 → 1; USD 1.005 → 1.01), `TestNormalizeDefaultsRate` (PPH21, nil, true → PPH21, 2.5, true), `TestNormalizeNoneClears` (NONE, 2, true → NONE, nil, false), `TestNormalizeRejectsInvalid` (PPH23 100; PPH23 0; PPN → errors `Withholding tax rate must be greater than 0 and less than 100` ×2 and `Unknown withholding tax type: PPN`), `TestExpectedCash` (1000, 20, 5 → 985), `TestExpectedCashNeverNegative` (10, 50, 0 → 0).

Run: `./dev test ./internal/withholding/` — Expected: FAIL.

- [ ] **Step 2: Implement, run, mutate**

Expected: PASS. Mutations: make the rate check `>=` 0 (the `PPH23 0` case red); round half-even (`TestPph21UsdGrossUpTwoDecimals` or `TestRoundHalfUp` red).

- [ ] **Step 3: Commit** `feat(go): withholding tax helpers (PPh 21/23)`

---

### Task 3: Chart of accounts

**Files:**
- Create: `backend/internal/accounts/api.go`, `routes.go`, `queries.sql`, generated `accountsdb/`
- Modify: `backend/sqlc.yaml` (an entry for `internal/accounts/queries.sql` → `accountsdb`, same overrides as auth), `backend/cmd/api/main.go` (`register`)
- Test: `backend/internal/accounts/accounts_test.go`

**Interfaces:**
- Consumes: `apitest.New`, `platform.MapConstraint`, `platform.Optional`.
- Produces: `func Register(api huma.API, pool *pgxpool.Pool)`; `type AccountOut struct { AccountID int32 "account_id"; Code, Name, Type string; Active bool }`; `var AccountTypes = []string{"ASSET","LIABILITY","EQUITY","INCOME","EXPENSE","COGS"}` (later plans validate account references against this table).

| Route | Python | Kept |
|---|---|---|
| `GET /api/v1/accounting/chart-of-accounts?active=` | `accounting.py:45-54` | `ORDER BY code, account_id` |
| `POST …` `{code 1–10, name 1–100, type enum, active=true}` | `:57-73` | 201; unique `chart_of_accounts_code_key` → 409 `account code already exists` |
| `GET …/{account_id}` | `:76-85` | 404 `account not found` |
| `PATCH …/{account_id}` | `:88-106` | partial; same 409 and 404 |
| `DELETE …/{account_id}` | `:109-121` | 200, sets `active=false`, returns the account |

`account_id` is an integer path parameter (a non-integer is 422).

- [ ] **Step 1: Failing tests**

`TestAccountsListOrderedByCodeAndFiltered` (the baseline's seeded accounts come back ordered by code; `?active=false` after a delete returns only that one), `TestAccountsEmptyFilterIsArray` (`?active=false` on the seed → `[]`), `TestAccountCreate201`, `TestAccountCreateDuplicateCodeIs409`, `TestAccountPatchDuplicateCodeIs409`, `TestAccountPatchPartial` (only `name` sent; `code`, `type`, `active` unchanged), `TestAccountTypeOutsideEnumIs422`, `TestAccountPatchNullCodeIs422` (`{"code": null}` → 422, `errors[0].location` `body.code`), `TestAccountNotFound404` (GET, PATCH, DELETE of `999999`), `TestAccountDeleteDeactivates`, `TestAccountsRequireAdmin` (`Anon` GET → 401 `not authenticated`).

Run: `./dev test ./internal/accounts/` — Expected: FAIL.

- [ ] **Step 2: Queries, generate, implement**

Queries: `ListAccounts` (`sqlc.narg(active)::boolean IS NULL OR active = …`), `GetAccount`, `GetAccountForUpdate`, `CreateAccount`, `UpdateAccount` (all columns), `DeactivateAccount`. Run `./dev sqlc`. A null for `code`, `name`, `type` or `active` is refused before any query with 422 `<field> cannot be null` (see PORT-NOTES entry added in Task 9: Python answers 409 here).

- [ ] **Step 3: Run, mutate, commit**

Expected: PASS. Mutations: drop the `chart_of_accounts_code_key` entry from the map (409 tests red with 500); drop the not-found check on PATCH (404 PATCH test red with 500).
Commit `feat(go): chart of accounts routes`.

---

### Task 4: Vendors

**Files:**
- Create: `backend/internal/vendors/api.go`, `routes.go`, `queries.sql`, generated `vendorsdb/`
- Modify: `backend/sqlc.yaml`, `backend/cmd/api/main.go`
- Test: `backend/internal/vendors/vendors_test.go`

**Interfaces:**
- Produces: `func Register(api huma.API, pool *pgxpool.Pool)`; `type VendorOut struct` with, in this order, `name`, `contact_email` (`*string`), `contact_name`, `default_currency`, `payment_terms_days` (int), `tax_id`, `notes`, `active`, `vendor_id`, `created_at` (`platform.Time`).

Create body (`schemas/vendor.py:7-19`): `name` 1–255 required; `contact_email` `*platform.Email`; `contact_name`, `tax_id` (≤100), `notes` nullable strings; `default_currency` 3 characters, default `IDR`; `payment_terms_days` 0–365, default 30; `active` default true. PATCH: every field `Optional`, same limits. List `?active=` ordered by `name, vendor_id`. 404 `vendor not found`. DELETE sets `active=false` and returns the vendor (200).

- [ ] **Step 1: Failing tests**

`TestCreateVendorSetsFields` (ports `test_create_vendor_sets_fields`), `TestUpdateVendorEditsFields` (ports `test_update_vendor_edits_fields`), `TestUpdateVendorNotFound` (ports `test_update_vendor_not_found_raises`: 404 `vendor not found`), `TestVendorDefaults` (IDR, 30, active), `TestVendorListOrderAndFilter`, `TestVendorEmailNormalisedAndValidated` (`AP@Beta.COM` stored `AP@beta.com`; `nope` → 422), `TestVendorTermsOutOfRangeIs422` (366), `TestVendorPatchNullNameIs422`, `TestVendorPatchNullContactEmailClears`, `TestVendorDeleteDeactivates`.

- [ ] **Step 2: Implement, run, mutate**

Expected: PASS. Mutations: let `name: null` through to the UPDATE (null-name test red with 500); drop the `Optional.Set` check so omitted fields write zero values (`TestUpdateVendorEditsFields` red: `name` blanked).

- [ ] **Step 3: Commit** `feat(go): vendor routes`

---

### Task 5: Items and their Cloudflare target

**Files:**
- Create: `backend/internal/items/api.go`, `routes.go`, `service.go`, `queries.sql`, generated `itemsdb/`
- Modify: `backend/sqlc.yaml`, `backend/cmd/api/main.go`
- Test: `backend/internal/items/items_test.go`

**Interfaces:**
- Produces: `func Register(api huma.API, pool *pgxpool.Pool)`; `type ItemOut struct` (fields of `schemas/item.py:41-59` then `item_id`, `created_at`, `hosting_status`, `hosting_last_paid_at`, `hosting_last_action_at`, `hosting_last_error`, `cloudflare_target *CloudflareTargetOut`); `type CloudflareTargetOut` (`schemas/item.py:22-38`); `func Get(ctx context.Context, q *itemsdb.Queries, id uuid.UUID) (ItemOut, error)` for later plans (invoices, hosting enforcement).

Rules from `backend-python/app/services/items.py` and `schemas/item.py`:
- `item_type` ∈ `SERVICE`, `USAGE`, `FIXED_FEE`; `hosting_status` (PATCH only) ∈ `ACTIVE`, `SUSPEND_PENDING`, `SUSPENDED`, `CANCELLED`; prices `Dec4`, ≥ 0, at most 4 decimal places (more is 422); `hosting_grace_days` 0–365.
- Create with `is_hosting: true` needs `hosting_domain` (non-empty), `hosting_grace_days`, `hosting_suspension_enabled` and `cloudflare_target`; otherwise 422 `hosting item requires: <missing, comma+space joined, in that order>`. Create sets `hosting_status = ACTIVE` when hosting.
- `hosting_domain` is trimmed and lower-cased; in the target, every string is trimmed, `record_name` lower-cased, `record_type` upper-cased. A new target gets `provider_status = ACTIVE` (the column default).
- PATCH: after applying fields, if `is_hosting` and `hosting_status` is null, set `ACTIVE`. A `cloudflare_target` in the PATCH replaces the fields of the existing target (one row per item, `cloudflare_targets_item_id_key`) or inserts one; `updated_at` of an updated target is `CURRENT_TIMESTAMP`.
- `items_sku_key` → 409 `sku already exists`; an unknown `revenue_account_id`/`expense_account_id` (FK) → 422 `account not found`. (Python answers 500 for both; Task 9 records the difference.)
- List `?active=&item_type=&is_hosting=` ordered by `name, item_id`; an empty `item_type` means no filter, as Python's `if item_type:`.
- 404 `item not found`; DELETE deactivates and returns the item.

- [ ] **Step 1: Failing tests**

Ports: `TestCreateItemBasic`, `TestCreateHostingItemSetsActiveStatusAndNormalizes` (`Example.COM`→`example.com`, `WWW`→`www`, `a`→`A`), `TestUpdateItemNotFound` (404), `TestUpdateItemEditsFields`, `TestItemDefaultsSoldTruePurchasedFalse`, `TestItemCanBePurchasedOnly` (`120.00` → `"120.0000"`), `TestItemCanBeBothSoldAndPurchased`, `TestItemCreateAcceptsPurchaseFields` (an `expense_account_id` that exists in the seed), `TestItemOutSerializesNewFields`, `TestItemUpdateCanTogglePurchased`, `TestHostingItemCreatesWithCloudflareTarget` (`provider_status` `ACTIVE`).
New: `TestHostingItemMissingFieldsIs422` (detail `hosting item requires: hosting_domain, hosting_grace_days, hosting_suspension_enabled, cloudflare_target`), `TestItemPatchUpdatesExistingTarget` (one `cloudflare_targets` row after two PATCHes), `TestItemPatchHostingSetsActive`, `TestItemPriceFivePlacesIs422`, `TestItemDuplicateSkuIs409`, `TestItemUnknownAccountIs422`, `TestItemListFilters`, `TestItemResponseHasNullTarget` (`"cloudflare_target": null` present for a non-hosting item).

- [ ] **Step 2: Implement, run, mutate**

Create and PATCH run in one transaction (item row, then target). Expected: PASS. Mutations: remove the hosting completeness check (422 test red with 201); insert instead of upsert the target (`TestItemPatchUpdatesExistingTarget` red with 500 from the unique key); skip the `record_type` upper-casing (normalise test red).

- [ ] **Step 3: Commit** `feat(go): item routes with hosting fields and Cloudflare target`

---

### Task 6: Customers

**Files:**
- Create: `backend/internal/customers/api.go`, `routes.go`, `service.go`, `queries.sql`, generated `customersdb/`
- Modify: `backend/sqlc.yaml` (JSONB `matching_aliases`, `quotation_cc_emails`, `billing_cc_emails` as `[]string`), `backend/cmd/api/main.go`
- Test: `backend/internal/customers/customers_test.go`

**Interfaces:**
- Consumes: `withholding.Normalize`, `platform.Email`, `platform.NormalizeEmail`.
- Produces: `func Register(api huma.API, pool *pgxpool.Pool)`; `type CustomerOut struct` (every field of `schemas/customer.py:65-106`, same names; `default_wht_rate *platform.Dec4`); `func Get(ctx context.Context, q *customersdb.Queries, id uuid.UUID) (CustomerOut, error)` for later plans.

Five routes: `GET /api/v1/customers?active=` (by `name, customer_id`), `POST` (201), `GET /{customer_id}`, `PATCH /{customer_id}`, `DELETE /{customer_id}` (deactivates, 200). **Not in this plan:** `GET /customers/balances`, `/{id}/billing-summary`, `/{id}/invoices`: they return `InvoiceOut` and read invoices and payments, so they move to the invoices plan with that type (`ROUTES.md` note says so).

Rules (`services/customers.py`, `schemas/customer.py`, `schemas/email.py`):
- Field lengths as in `CustomerBase` (`name` 1–255 required on create; contacts ≤120; phones ≤64; postal codes ≤32; countries ≤100; others ≤255; `default_wht_type` ≤16; `default_currency` exactly 3).
- `contact_email` is `platform.Email`. `quotation_cc_emails` and `billing_cc_emails` are JSON lists of strings: blanks dropped, each address validated (`invalid email address: <addr>` 422), duplicates dropped case-insensitively keeping the first. `matching_aliases` is a list of strings, default `[]`.
- Withholding: if the body has any of `default_wht_type`, `default_wht_rate`, `default_wht_gross_up`, run `withholding.Normalize` over the stored values overlaid with the supplied ones; a type change without a rate uses the new type's default rate; `NONE` clears rate and gross-up. On create the stored values are `NONE`, nil, false. `Normalize`'s error is a 422 with its sentence.
- An omitted `active` or `default_currency` on create takes `true` / `IDR`; `null` for any NOT NULL column (`name`, `active`, `default_currency`, the CC lists, `matching_aliases`, `default_wht_type`, `default_wht_gross_up`) is 422 `<field> cannot be null`.
- 404 `customer not found`.

- [ ] **Step 1: Failing tests**

Ports: `TestCreateCustomerSetsFields`, `TestUpdateCustomerEditsFields`, `TestUpdateCustomerNotFound`, `TestCustomerCCListsRoundtrip`, `TestCustomerCCRejectsInvalidAddress`, `TestCreateCustomerWithPph23GrossUp` (`"2.0000"`), `TestCustomerDefaultsToNone`, `TestCustomerWhtInvalidRate422` (150), `TestCustomerWhtUnknownType422` (`PPH99`; detail `Unknown withholding tax type: PPH99`), `TestCustomerNoneClearsRate`, `TestPatchWithoutWhtFieldsKeepsStored`, `TestPatchPartialWhtMergesWithStored`, `TestPatchTypeSwitchUsesNewDefaultRate` (`"2.5000"`).
New: `TestCustomerWhtRateAcceptsNumberAndString`, `TestCustomerCCClearAndKeep`, `TestCustomerCCDedupesCaseInsensitively` (`["a@x.com","A@x.com"]` → `["a@x.com"]`), `TestCustomerListOrderAndFilter`, `TestCustomerDeleteDeactivates`, `TestCustomerPatchNullNameIs422`, `TestCustomerResponseHasEveryField` (a minimal customer's JSON has all 34 keys, nullable ones `null`, lists `[]`).

- [ ] **Step 2: Implement, run, mutate**

Expected: PASS. Mutations: skip `Normalize` on PATCH (merge and switch tests red); keep the stored rate on a type switch (`TestPatchTypeSwitchUsesNewDefaultRate` red with `"2.0000"`); skip CC validation (invalid-address test red with 201).

- [ ] **Step 3: Commit** `feat(go): customer routes with withholding defaults and CC lists`

---

### Task 7: Business profile and payment accounts

**Files:**
- Create: `backend/internal/business/api.go`, `routes.go`, `queries.sql`, generated `businessdb/`
- Modify: `backend/sqlc.yaml`, `backend/cmd/api/main.go`
- Test: `backend/internal/business/business_test.go`

**Interfaces:**
- Produces: `func Register(api huma.API, pool *pgxpool.Pool, clock platform.Clock)`; `type ProfileOut struct` (`name`, `address`, `contact_email`, `contact_phone`, `invoice_title`, `invoice_summary`, `logo_url`, `default_notes`, `npwp`, `npwp_name` as `*string`; `updated_at platform.Time`); `type PaymentAccountOut struct { ID int32 "id"; Currency, Instructions string; IsDefault bool "is_default"; UpdatedAt platform.Time }`; `func Profile(ctx context.Context, q *businessdb.Queries) (ProfileOut, error)` and `func PaymentAccounts(ctx context.Context, q *businessdb.Queries) ([]PaymentAccountOut, error)` for the PDF and email plans.

| Route | Python | Go |
|---|---|---|
| `GET /api/v1/business-profile` | `business_profile.py:30-35` | reads row 1; if it is missing, answers all-null fields with `updated_at` = clock now and writes nothing (what Python shows, PORT-NOTES 4) |
| `PUT …` (every field optional, `null` clears) | `:38-49` | upserts row 1; `updated_at = CURRENT_TIMESTAMP` only when at least one field was sent (SQLAlchemy's `onupdate` fires only on a real UPDATE) |
| `GET …/payment-accounts` | `:62-68` | `ORDER BY currency, id` |
| `PUT …/payment-accounts` (bare array of `{currency, instructions, is_default=false}`) | `:71-99` | checks in Python's order, on upper-cased currencies: duplicates → 422 `Duplicate currency in payment accounts`; outside `IDR`, `SGD`, `USD` → 422 `Unsupported currency: GBP` (sorted, `, `-joined); more than one default → 422 `At most one account can be the default`. Then, in one transaction holding `SELECT … FROM business_profile WHERE id = 1 FOR UPDATE` (the row is created first if missing), deletes every account and inserts the new ones. Returns the list. |

- [ ] **Step 1: Failing tests**

Ports: `TestReplaceAndListPaymentAccounts`, `TestReplaceIsFullOverwrite`, `TestRejectDuplicateCurrency`, `TestRejectTwoDefaults`, `TestRejectUnsupportedCurrency` (detail `Unsupported currency: GBP`), `TestRelationshipOrdersAndReadsAccounts` (IDR before USD), `TestDuplicateCurrencyRejected` and `TestSecondDefaultRejected` (the two database constraints, `uq_payment_account_currency` and `uq_payment_account_one_default`, asserted with direct inserts through the pool: they stay in force for Python).
New: `TestProfileGetReturnsSeedRow`, `TestProfileGetMissingRowWritesNothing` (delete row 1, GET → 200 all null, row count still 0), `TestProfilePutPartialAndNullClears`, `TestProfilePutEmptyKeepsUpdatedAt`, `TestPaymentAccountsLowercaseCurrencyUppercased` (`idr` stored `IDR`), `TestDuplicateCurrencyCaseInsensitive` (`sgd` + `SGD` → 422), `TestReplacePaymentAccountsConcurrent` (two goroutines PUT different lists 20 times; afterwards the rows equal one of the two lists, and no response is 500), `TestPaymentAccountsEmptyIsArray` (`PUT []` → `[]`).

- [ ] **Step 2: Implement, run, mutate**

Expected: PASS. Mutations: drop the `FOR UPDATE` (concurrency test red: a 500 from `uq_payment_account_currency` or a mixed list; run it with `-count=5` to see it); drop the upper-casing before the duplicate check (case-insensitive test red).

- [ ] **Step 3: Commit** `feat(go): business profile and payment account routes`

---

### Task 8: User administration

**Files:**
- Create: `backend/internal/auth/users.go`
- Modify: `backend/internal/auth/queries.sql` (`ListUsers`, `CreateUser`, `UpdateProfile`, `UserByEmail` reused), `backend/internal/auth/auth.go` (register the users routes from `Register`)
- Test: `backend/internal/auth/users_test.go`

**Interfaces:**
- Consumes: `hashPassword`, `authdb.SetPassword` (bumps `session_epoch`, voids the reset token), `UserOut`, `platform.Email`.
- Produces: no new exported names; the four routes register from `auth.Register`.

| Route | Python (`routers/users.py`) | Kept |
|---|---|---|
| `GET /api/v1/users` | `:18-23` | `ORDER BY email, user_id` |
| `PATCH /api/v1/users/me` `{display_name ≤255, email}` | `:26-46` | `null` and omitted both leave a field alone (Python tests `is not None`); `display_name: ""` stores `NULL`; a changed email held by another user → 409 `email already in use` (also when the unique key fires under a race) |
| `POST /api/v1/users` `{email, password 6–128, role="admin" ≤20, display_name ≤255}` | `:49-66` | 201; existing email → 409 `email already exists`; argon2id |
| `POST /api/v1/users/{user_id}/reset-password` `{password 6–128}` | `:69-84` | 404 `user not found`; new hash, reset token cleared, **and `session_epoch` bumped** (spec, "Signing out everywhere") |

- [ ] **Step 1: Failing tests**

`TestListUsersOrderedByEmail`, `TestUpdateMeDisplayNameAndEmail`, `TestUpdateMeEmptyDisplayNameIsNull`, `TestUpdateMeNullLeavesFields`, `TestUpdateMeEmailInUseIs409`, `TestUpdateMeSameEmailIsNoConflict`, `TestCreateUser201ThenLogin` (the new user signs in through `/auth/login`), `TestCreateUserDuplicateIs409`, `TestCreateUserPasswordLengthIs422`, `TestAdminResetPasswordSignsUserOut` (a session of the target user issued before → 401 `session expired`; the new password signs in), `TestAdminResetPasswordUnknownIs404`.

- [ ] **Step 2: Implement, run, mutate**

Expected: PASS. Mutations: reset with an UPDATE that does not bump the epoch (sign-out test red); drop the clash check and rely on nothing (409 test red with 500).

- [ ] **Step 3: Commit** `feat(go): user administration routes`

---

### Task 9: Inventories, port notes, checks, PR

**Files:**
- Modify: `backend/ROUTES.md`, `backend/TESTS.md`, `backend/PORT-NOTES.md`, `backend/openapi.json` (regenerated)

- [ ] **Step 1: ROUTES.md**

Run `python backend/port/inventory.py` (it keeps the hand-edited columns). Mark the 28 routes of Tasks 3–8 `ported` with their Go package. On the three deferred customer routes write the note `moves to the invoices plan (returns InvoiceOut)`.

- [ ] **Step 2: TESTS.md**

Mark ported, with the Go test name and the mutation that turned it red: `test_customers_service.py` (3), `test_vendors_service.py` (3), `test_items_service.py` (4), `test_items_sell_buy.py` (6), `test_withholding.py` (11), `test_customer_wht_defaults.py` (8), `test_customer_cc.py` (the first 2; the two send-receipt tests stay `todo` for the receipts plan), `test_payment_account_model.py` (3), `test_payment_accounts_api.py` (5), `test_hosting.py::test_hosting_item_creates_with_cloudflare_target` (1). That is 46 rows.

Run: `grep -c '| ported |' backend/TESTS.md` — Expected: 52 (6 from plan 2 plus 46).

- [ ] **Step 3: PORT-NOTES.md**

New entries, each with the Python file:line and what the comparison will see:
10. `null` for a NOT NULL column in a PATCH or create: Python answers 409 `account code already exists` on accounts (`accounting.py:100-104` catches the IntegrityError) and 500 elsewhere; Go answers 422 `<field> cannot be null`.
11. Duplicate item SKU and unknown item account: Python 500 (`services/items.py:50`, flush without a catch); Go 409 `sku already exists` and 422 `account not found`.
12. `GET /business-profile` on a missing row: Go answers the same defaults without writing (closes note 4 for Go).
13. Admin password reset now signs the user out everywhere (`users.py:79-82` leaves sessions alive); a spec change, listed under "Best-practice changes".
14. Email addresses: Go's `NormalizeEmail` keeps the email-validator rules listed in Task 1; rarer rules (quoted local parts, IDNA length limits) may differ. An allowed difference for `compare`.

- [ ] **Step 4: Full checks**

Run: `bash scripts/check.sh`
Expected: `go tests: N passed, none skipped` with N > 112, and `all checks passed`.

- [ ] **Step 5: Commit, PR**

```bash
git add backend/ROUTES.md backend/TESTS.md backend/PORT-NOTES.md backend/openapi.json
git commit -m "docs(go): master data ported in ROUTES.md and TESTS.md; port notes 10-14"
git push -u origin feat/go-master-data
gh pr create --base main --title "feat(go): master data — accounts, vendors, items, customers, business profile, users" --body-file -
```
The body lists the 28 routes, the 46 ported tests, every mutation run, the three customer routes deferred to the invoices plan, the new port notes, and that merging changes no production container. Merge waits for the owner.
