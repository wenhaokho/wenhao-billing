# Port notes

Behaviours of the frozen Python backend (`backend-python/`) that look like gaps or bugs. Each was re-read against the
code on 2026-11-09; the file:line is where to see it. The Go port should decide each one on purpose instead of copying
it by accident. "After the switch" is the fix to schedule once Go is live (not before: the port keeps Python's contract).

1. **Usage-lock accrual hook raises `NotImplementedError`.**
   Python: `cutoff_scan` defaults `compute_accrued` to `_stub_accrued`, which raises (`backend-python/app/workers/tasks/usage_lock.py:23-24`, call at `:43`). The scheduler runs it daily at 03:00 (`app/workers/scheduler.py:33`).
   Seen: on any day when an unlocked DRAFT USAGE invoice has `cut_off_day` equal to yesterday's day-of-month, the job fails and nothing locks. Locking by hand through `POST /api/v1/invoices/usage/{id}/lock` (`routers/invoices.py:391`) still works because the caller supplies the amount.
   After the switch: wire a real usage-metering source, or retire the scan and keep manual lock.

2. **Email intake with attachments raises `NotImplementedError` (surfaces as 400).**
   Python: the default OCR function raises (`app/services/intake/email_ocr.py:22-23`); `parse_inbound_email` re-raises it for any attachment that has `content` (`:42-50`); the route is called without an `ocr_fn` and turns it into a 400 (`app/api/v1/routers/payments.py:48-51`, `POST /api/v1/payments/intake/email`).
   Seen: a payment email with an attachment is rejected with 400 and the message "Attach an OCR backend (tesseract/textract/etc) before calling"; only emails whose subject/text/html carry the amount are accepted.
   After the switch: pick an OCR backend, or skip attachments and parse the body only (a decision for the owner).

3. **`GET /api/v1/payments/{id}/log` returns `[]` for an unknown payment.**
   Python: the handler never looks the payment up; it selects log rows by `payment_id` (`app/api/v1/routers/recon.py:105-125`).
   Seen: a typo'd or deleted id looks the same as a payment with no history (200, empty list) instead of 404.
   Go: kept, on purpose (the spec keeps the contract): `200 []` (`payments.TestPaymentLogUnknownIsEmpty`). Manual-review and reverse of an unknown payment stay 400 `payment <id> not found` (`TestManualReviewUnknownPaymentIs400`, `TestReverseOverRest`), not 404.
   After the switch: look the payment up first and return 404 `payment not found`, as the neighbouring routes in `recon.py` do (`:99-102`).

4. **`GET /api/v1/business-profile` creates the row without committing.**
   Python: `_get_or_create` adds and flushes (`app/api/v1/routers/business_profile.py:20-27`); the GET returns it (`:31-36`) with no commit, and `get_db` only closes the session (`app/db/session.py:14-19`), so the insert rolls back.
   Seen: on a fresh database the GET shows defaults, but the row does not exist until the first `PUT` commits (`:47`). Harmless today; it would matter if anything else read the row.
   After the switch: either commit in the GET or make the GET read-only and return defaults without an insert.

5. **`as_of` and "today" defaults use the server-local date.**
   Python: `Query(default_factory=date.today)` on the trial balance, balance sheet, FX revaluation, AR aging and AP aging reports (`app/api/v1/routers/accounting.py:243, 314, 389, 558, 607`); the dashboard and customer balances use `date.today()` too (`routers/stats.py:191, 251, 286`, `routers/customers.py:65, 172`). `date.today()` reads the process time zone.
   Seen: a container running in UTC flips to the next day at 07:00 Jakarta time, so for seven hours each morning the reports and aging buckets show "yesterday" to a user in WIB.
   After the switch: take "today" from one business-time-zone clock (Asia/Jakarta) in Go and use it everywhere.

6. **The matching engine can auto-clear a tie between equal top candidates (PRD safe-stop).**
   Python: `match_payment` sorts candidates by score and takes `scored[0]` with no check on the runner-up (`app/services/matching_engine.py:180-186`); a CLEARED result needs only an exact match on that one candidate with confidence at or above the threshold (`:226-233`). Candidates come from a query with no `ORDER BY` (`app/services/reconciliation.py:47-54`), so which of two equal invoices wins is up to the database.
   Seen: two open invoices of one customer with the same amount, currency and dates both score the same, and the payment is auto-cleared against whichever sorts first. CLAUDE.md says any ambiguity must go to `PENDING_MANUAL_REVIEW`.
   Go (plan 7): this now holds. Ambiguity goes to manual review with the best candidate attached and the reason `ambiguous match: <n> open invoices match exactly`; see note 80 for the rule, its tests and the deterministic candidate order. Python's tie is still what runs until the switch.
   After the switch: none; the fix is in Go. Compare runs will show Python clearing where Go holds, for two identical open invoices.

7. **Payment reversal restores face amount plus withholding, which differs after a cross-currency settlement.**
   Python: `reverse_payment` does `balance_due + payment.amount + payment.wht_amount` (`app/services/reconciliation.py:239`). Clearing a cross-currency payment sets the invoice's `balance_due` to 0 (`:107`) whatever the payment amount was.
   Seen: for a same-currency settlement the reversal puts back exactly what was taken. After a cross-currency settlement it adds the payment-currency amount to an invoice-currency balance, so the restored balance can be wrong (too high or too low), while the reversing journal entry restores the ledger exactly.
   Go: kept, the same arithmetic (`payments.Service.Reverse`: `balance_due + amount + wht_amount`, `wht_booked - wht_amount`, status OPEN). The same-currency case is pinned (`payments.TestReversalCreatesBalancingEntryAndWiresLink`, `TestReverseThenRepay`); no test pins the cross-currency figure.
   After the switch: store the balance relieved on the payment (or on the log row) and restore that figure.

8. **Manual invoice and bill record-payment create no journal entries.**
   Python: `POST /api/v1/invoices/{id}/record-payment` inserts a `CLEARED` Payment and lowers `balance_due` and `wht_booked`, but never calls the ledger (`app/api/v1/routers/invoices.py:510-594`; the only writes are the Payment, the invoice and a ReconciliationLog). `POST /api/v1/bills/{id}/record-payment` only lowers `balance_due` and sets PARTIAL or PAID (`app/api/v1/routers/bills.py:83-111`).
   Seen: cash received or paid by hand never reaches the double-entry ledger, so reports built on journal lines (trial balance, balance sheet) do not move when an invoice is marked paid this way. A manual invoice payment cannot be reversed either: `reverse_payment` fails with "no active journal entry to reverse" (`app/services/reconciliation.py:231`).
   Go (plan 7): kept for the invoice route. record-payment posts no ledger entry (`payments.TestRecordPaymentWritesNoJournal`), so reversing such a payment is a 400 `no active journal entry to reverse for payment <id>` (`payments.TestReverseManualPaymentHasNoEntry`); see note 81. Bill record-payment is note 73.
   After the switch: post the payment to the ledger through the same service as `_clear_against_invoice` (`:82`), then decide how to handle the payments already recorded.

9. **MCP login redirect uses `return_to`, but the login page reads `next`.**
   Python: when `/oauth/authorize` finds no session it redirects to `{app_base_url}/login?return_to=<authorize url>` (`app/mcp/oauth.py:350-358`). The login page only reads `route.query.next` (`frontend/src/views/LoginView.vue:53-54`); the router guard also sets `next` (`frontend/src/router/index.ts:277`).
   Seen: after signing in, an MCP client user lands on `/dashboard`, not back on the authorize screen, so the OAuth flow stalls until the client retries.
   After the switch: make the Go redirect send `next` (and have the login page accept only same-origin paths or the known authorize URL), or teach the login page to read `return_to`.

10. **`null` for a NOT NULL column on PATCH.**
    Python: a create schema declares these fields without `| None` (`name: str`, `active: bool = True`, …), so a `null` on create is already a 422. The PATCH schemas make every field `| None`, so pydantic accepts `null` for fields such as `code`, `name`, `active` and the row update fails at the database. `PATCH /api/v1/accounting/chart-of-accounts/{id}` catches the IntegrityError and answers 409 `account code already exists` (`app/api/v1/routers/accounting.py:100-104`); every other router lets it become a 500. Customers are the exception on create: `CustomerCreate` inherits `CustomerBase`'s `| None` fields (`app/schemas/customer.py:9-53`), so a `null` `active`, `default_currency` or CC list reaches the database on create too.
    Seen: a client that sends `{"code": null}` gets a misleading 409 on accounts and an opaque 500 elsewhere.
    Go: 422 `Request validation failed` with one error per null field, location `<field>` and message `value cannot be null` (`platform.NotNull`). The row is untouched. Not for customers' `default_wht_type` and `default_wht_gross_up`: Python's service treats their `null` as "keep the stored value" (`app/services/customers.py:31-43`), and Go does the same (`customers.TestCustomerNullWhtTypeAndGrossUpKeepStored`).
    After the switch: none: Go's behaviour is the fix.

11. **Duplicate item SKU and unknown item account.**
    Python: `create_item` and `update_item` flush without catching the IntegrityError (`app/services/items.py:51, :71`), so a repeated `sku` or a `revenue_account_id` / `expense_account_id` that does not exist is a 500.
    Seen: the item form shows a generic server error instead of the reason.
    Go: 409 `sku_exists` with detail `sku already exists`; 422 `account_not_found` with detail `account not found`.
    After the switch: none: Go's behaviour is the fix.

12. **`GET /api/v1/business-profile` on a missing row.**
    Python: see note 4, the GET inserts the row and never commits.
    Go: the GET answers the same defaults (all fields null, `updated_at` = now) and writes nothing. This closes note 4 for Go.
    After the switch: none: Go's behaviour is the fix.

13. **Admin password reset signs the target out everywhere.**
    Python: `admin_reset_password` replaces the hash and clears the reset token, but leaves existing sessions alive (`app/api/v1/routers/users.py:79-82`).
    Seen: a user whose password an admin reset keeps working sessions on every device until they expire.
    Go: the reset bumps the user's session epoch, so every session of that user stops working. This is a deliberate spec change, covered by "sign-out everywhere on a password change" under the heading "Best-practice changes" in the spec (`docs/superpowers/specs/2026-11-08-go-backend-design.md`).
    After the switch: none: Go's behaviour is the fix.

14. **Email address rules.**
    Python: `EmailStr` (email-validator) for vendor, customer and user emails and for every CC entry (`app/schemas/vendor.py:9`, `app/schemas/customer.py:17`, `app/schemas/user.py:18`, `app/schemas/auth.py:31`, `app/schemas/email.py:8`).
    Seen: both sides trim, lower-case the domain, and keep the local part's case.
    Go: `platform.NormalizeEmail` keeps the email-validator rules listed in Task 1 of the plan (exactly one `@`; local part 1-64 bytes without whitespace; domain with at least two labels of letters, digits and inner hyphens, each at most 63; no special-use last label). Rarer forms may differ: Go may accept quoted local parts, leading, trailing or double dots in the local part, an all-numeric TLD and domains over 253 characters, which email-validator refuses. An allowed difference for `compare`.
    After the switch: none needed; tighten `NormalizeEmail` only if a refused form is seen in real data.

15. **Boolean query filters accept fewer spellings.**
    Python: `active: bool | None = None` on the list routes (`app/api/v1/routers/accounting.py:47`, `vendors.py:19`, `customers.py:42`, `items.py:19`) is parsed by pydantic, which also accepts `yes/no/on/off/y/n` in any case.
    Seen: `?active=yes` filters in Python.
    Go: `strconv.ParseBool` through `platform.OptionalParam`. `items.TestItemListActiveFilterSpellings` shows `?active=` with `true`, `false`, `1`, `0`, `t`, `f`, `TRUE` or `False` answering 200, and `yes`, `no`, `on`, `off`, `y` or `n` answering 422. The frontend sends `true` and `false`.
    After the switch: none; the frontend sends only `true` and `false`.

16. **Over-long strings are 422 instead of a database 500.**
    Python: some vendor, customer, business-profile and user fields have no `max_length` although their column is a varchar: vendor `contact_name` and `tax_id` (`app/schemas/vendor.py:10, 13`, and on PATCH `name`, `:23`), customer `contact_email` (`customer.py:17`), business-profile `name`, `contact_email`, `contact_phone`, `npwp` (`business_profile.py:9-18`), user `role` (`user.py:20`). A value longer than the column fails in Postgres and surfaces as 500.
    Go: each request field carries the column's `maxLength`, so the same input is a 422 at the field.
    After the switch: none: Go's behaviour is the fix.

17. **`ItemUpdate` limits are enforced on PATCH.**
    Python: `ItemUpdate` has no length limit on `sku` and no `min_length` on `name` (`app/schemas/item.py:82-102`), so an over-long `sku` is a 500 and an empty `name` is stored.
    Go: `sku` at most 50 and `name` at least 1 apply on PATCH as on create.
    After the switch: none: Go's behaviour is the fix.

18. **A whitespace-only `hosting_domain` or target field is accepted and stored as `""`.**
    Python: the hosting check and the target `min_length` test the raw value before it is stripped (`app/schemas/item.py:65-78`), so `"  "` passes and the stripped value is saved.
    Seen: a hosting item can be created with an empty domain.
    Go: the same, kept on purpose to match Python. Odd; decide when hosting is rebuilt.
    After the switch: refuse a value that is empty once trimmed, with the hosting rebuild.

19. **Item price errors list only the first refused price, and a parse refusal hides other errors.**
    Python: pydantic reports every failing field, so a body with both prices refused lists both (`app/schemas/item.py:47-50, 88-91`), and a bad decimal, date or time is listed with the other errors of the body.
    Go: `checkPrices` returns the first refused price (unit price first) as a 422 whose detail is pydantic's sentence. A value that fails to parse (bad decimal, date, time) is reported alone, without the other 422 errors of the same body.
    After the switch: list every refused field in one 422 if a form needs it; the frontend shows `detail` only.

20. **Invalid email `detail` and the CC list error location.**
    Python: a bad single email is a pydantic error list (`value is not a valid email address: ...` text from email-validator) located at the field; a bad CC entry is located at the list, `billing_cc_emails` (`app/schemas/email.py:19-28`).
    Go: `detail` is `value is not a valid email address` with the field as location. A CC entry error carries the element index, `billing_cc_emails.1`, and the message `invalid email address: <addr>`. The frontend reads `detail`, not locations.
    After the switch: none; the frontend reads `detail`.

21. **`POST /users` with `role: ""` or `null`.**
    Python: `role: str = Field(default="admin")` stores `""` as given and refuses `null` (`app/schemas/user.py:20`). `role` is any string in both, with no check against known roles.
    Go: `role` is any string of at most 20 characters; `""` and `null` store `admin`.
    After the switch: check `role` against the roles the app knows.

22. **An admin resetting their own password is signed out too.**
    Python: no session is touched (note 13), so the caller stays signed in.
    Go: the same epoch bump as note 13 applies to the caller's own account when the target is the caller, so the next request returns 401 `session expired` and they sign in again (`auth.TestAdminSelfResetSignsCallerOut`).
    After the switch: none: Go's behaviour is the fix (it matches a password change, which also signs every session out).

23. **A blanked display name cannot be cleared.**
    Python: `PATCH /api/v1/users/me` changes `display_name` only when it is not `null` (`app/api/v1/routers/users.py:42`). The Account page sends `display_name: null` when the field is blanked (`frontend/src/views/AccountView.vue:64`).
    Seen: a user who empties their display name and saves keeps the old name; only `""` (which the page never sends) clears it. Go keeps this contract (`auth.TestUpdateMeNullLeavesFields`).
    After the switch: treat `null` as "clear" in Go (or have the page send `""`).

24. **Business profile `updated_at` moves on a PUT that changes nothing.**
    Python: the PUT sets each sent field on the row (`app/api/v1/routers/business_profile.py:44-47`); SQLAlchemy emits no UPDATE when no value changed, so the model's `onupdate` (`app/models/business_profile.py:32`) does not fire and `updated_at` stays.
    Go: the PUT always writes the row, so `updated_at` moves even when every value sent equals the stored one. An allowed difference for `compare`.
    After the switch: none; nothing reads `updated_at` as a change marker.

25. **A customer `default_wht_rate` with more than four decimal places is 422.**
    Python: `default_wht_rate: Decimal | None` has no `decimal_places` (`app/schemas/customer.py:52`), so the value is rounded into `numeric(7,4)`; a rate such as `99.99999` passes the "below 100" check and is stored as `100.0000`.
    Go: such a rate is refused before the withholding check with a 422, detail `Request validation failed`, and an error at `default_wht_rate` with message `value has too many decimal places (max 4)`, for a JSON string and a JSON number alike; nothing is stored (`customers.TestCustomerWhtRateFivePlacesIs422`).
    After the switch: none: Go's behaviour is the fix.

26. **A payment amount of zero or below is a plain error in the ledger, as Python's crash is.**
    Python: `_build_line` converts only a positive side, so a bank line for an amount of `0` or less has no conversion and `(conv_debit or conv_credit).rate` raises `AttributeError` (`app/services/ledger.py:86`), which no caller catches as a ledger refusal: a 500. API-created payments are validated `gt=0` by `app/schemas/payment.py:41,52`; the database has no CHECK on the amount.
    Go: `ledger.PostPaymentCleared` returns a plain error (not `ErrLedger`), `ledger: payment <id> amount must be > 0 (got <amount>)`, before any line, conversion or insert, so it surfaces as a 500 and is never routed to manual review as a refusal would be (`ledger.TestRefusesNonPositiveAmount`).
    After the switch: none; payments of zero or less never reach the ledger through the API or the matching flow.

27. **Two concurrent reversals of one entry.**
    Python: `post_reversal` reads the original with `db.get` and no lock (`app/services/ledger.py:223`), so two transactions can both see it unreversed and both post a reversal; the later one's `reversed_by_entry_id` wins.
    Go: `ledger.PostReversal` reads the original `FOR UPDATE`; the second transaction waits for the first, then sees `reversed_by_entry_id` set and is refused `entry <id> already reversed` (`ledger.TestReverseConcurrentOnlyOnce`).
    After the switch: none: Go's behaviour is the fix.

28. **FX sync 502 `detail`: the trailing error text is Go's.**
    Python: `fx provider request failed for SRC->TARGET: <httpx error>` for transport and HTTP failures, and `fx provider returned invalid JSON for SRC->TARGET: <error>` for a bad body (`app/services/fx_provider.py:63-70`), surfaced as a 502 (`app/api/v1/routers/accounting.py:185-189`).
    Go: the leading sentence and the 502 are the same. A non-2xx keeps httpx's first line (`Server error '500 Internal Server Error' for url '...'`); httpx's later lines (`For more information check: …`, and for a redirect `Redirect location: …`) are dropped. A transport failure or timeout carries Go's `net/http` text (`Get "...": context deadline exceeded`) and invalid JSON carries `encoding/json`'s text, not httpx's or Python's.
    After the switch: nothing may match on the trailing text; the frontend shows `detail` as is.

29. **A failed sync writes nothing, in Go and in Python.**
    Python: `run_fx_sync` fetches every pair first (`fetch_latest_rates`, `app/workers/tasks/fx_sync.py:23`) and only then adds rows to the session, so a provider failure raises before anything is added; the router rolls back and answers 502 (`app/api/v1/routers/accounting.py:185-189`), and the weekly job never commits. Python could not write a partial set.
    Go: the same order: every pair is fetched, then all pairs are written in one transaction (`fx.TestSyncProviderErrorIs502WritesNothing`).
    After the switch: none; the behaviour is identical.

30. **An FX rate or money value with too many places or digits is 422 in Go.**
    Python: an FX rate is `Field(gt=0, max_digits=19, decimal_places=8)` (`app/schemas/fx.py:10`), so too many places or digits are a 422 there too, with pydantic's own message. Money fields with no such bound behave in two ways: extra decimal places were silently rounded by Postgres into `numeric(19,4)` and the request answered 200; a value with too many integer digits overflowed the column and answered 500.
    Go: `platform.Dec2/Dec4/Dec8` refuse a value with more places than the column holds, or more digits than it can store, as a 422 at the field (`fx.TestCreateRateNinePlacesIs422`).
    After the switch: none: Go's behaviour is the fix; messages differ from pydantic's.

31. **Concurrent "Sync now".**
    Python: `run_fx_sync` selects each rate and then inserts it (`app/workers/tasks/fx_sync.py:29-49`), so two syncs in flight can both insert the same `(from, to, date)` and one hits `uq_fx_rates_triple`, a 500.
    Go: each pair is one `INSERT ... ON CONFLICT ON CONSTRAINT uq_fx_rates_triple DO UPDATE ... WHERE <changed>`, so both succeed and the counts stay right (`fx.TestSyncConcurrentIsIdempotent`).
    After the switch: none: Go's behaviour is the fix.

32. **The sync's 502 also covers responses Python answered 500.**
    Python: `Decimal(str(value))` accepts a numeric string such as `"16000"` (`app/services/fx_provider.py:91`) but raises `InvalidOperation` (500) for `"abc"`; a body that is not a JSON object fails at `body.get` (`:72`) and a `rates` that is not an object at `rates.get` (`:73`), both `AttributeError` (500).
    Python also skipped a falsy `rates` (`[]`, `""`, `0`, `false`) silently with a 200, through `body.get("rates") or {}` (`:72`); and it stored a provider rate of `0` or below as given (the column has no CHECK), while one with more than 11 integer digits overflowed `numeric(19,8)` at commit, a 500.
    Go: a provider rate that is not a JSON number (a string, a bool) is a 502 `fx provider returned invalid rate for SRC->TARGET: <raw>`, so Go refuses `"16000"` where Python stored it; a body or `rates` that is not a JSON object, falsy ones included, is a 502 reported as invalid JSON. A rate that is `0` or below, or does not fit `numeric(19,8)` (11 integer digits after rounding to 8 places), is the same `invalid rate` 502 and nothing is written (`fx.TestSyncInvalidRateIs502`).
    After the switch: none, unless Frankfurter starts sending rates as strings; then the sync fails loudly with 502 instead of storing them.

33. **The FX provider's redirects are not followed.**
    Python: `httpx.Client(...)` (`app/services/fx_provider.py:51`) does not follow redirects by default, so a 3xx from the provider reaches `response.raise_for_status()` (`:61`), raises `Redirect response '302 Found' for url '...'` and is a 502 (`:63-66`).
    Go: `fx.NewClient` sets `CheckRedirect` to `http.ErrUseLastResponse`, so a 3xx reaches the same non-2xx path and is the same 502, `fx provider request failed for SRC->TARGET: Redirect response '302 Found' for url '...'` (`fx.TestSyncRedirectIs502`); Go's default client would have followed it.
    After the switch: if Frankfurter moves its API, update `FX_PROVIDER_BASE_URL` rather than following redirects.

34. **Invoice numbers come from an unlocked scan, and a duplicate number is a 500.**
    Python: `generate_invoice_number` reads every number with the `WH<year>` prefix and takes the largest tail plus one (`backend-python/app/services/invoicing.py:157-171`). The year is `date.today().year`, not the issue date, and `str.isdigit()` accepts characters such as `²` that `int()` then rejects (`:169-170`). A caller-supplied `invoice_number` is stored as given (`:245`), and the unique index `uq_invoices_invoice_number` turns a clash into an unhandled IntegrityError. Milestone invoices and templates never get a number and finalize assigns none (`:192-225`, `:643-663`).
    Seen: two creates at the same moment can pick the same number and one answers 500; typing an existing number does the same.
    Go (a change): `NextInvoiceNumber` takes `pg_advisory_xact_lock` on `invoice-number:<year>` before it scans, inside the creating transaction, and only ASCII-digit tails count; a duplicate supplied number is 409 `invoice number already exists` (`invoices.TestNextInvoiceNumberConcurrent`, `invoices.TestNextInvoiceNumberSkipsNonDigitTails`, `invoices.TestCreateDuplicateNumberIs409`). The year is today's UTC year (note 5); numbers grow past four digits; milestones and templates still get none. A PATCH that writes a number of the client's choosing does not take the year's lock, so in the rare case it writes the very number a concurrent auto-numbered create has just picked, that create answers 409 `invoice number already exists` (a retry gets the next number).
    After the switch: nothing for the race. Decide whether milestone invoices should get a number at finalize.

35. **One invoice per cycle: the trigger takes a lock and accepts one spelling of the cycle key.**
    Python: `trigger_recurring_cycle` checks the key with `date.fromisoformat` (`app/services/invoicing.py:525`), which on Python 3.11 also takes `20260301` and `2026-W09-7`, then stores and compares the raw string (`:497-499`, `:572`). Two spellings of one cycle therefore make two invoices. There is no unique index on (template, cycle key), so a manual trigger and the scanner can both create the cycle, and `find_cycle_invoice` ends in `db.scalar`, which takes an arbitrary row when there are several (`:477-499`).
    Seen: after a manual trigger with `20260301` and a scan with `2026-03-01` the template has two drafts for March.
    Go (a change): the key must be `YYYY-MM-DD` (400 `cycle_key must be an ISO date (YYYY-MM-DD), got '<key>'`, `invoices.TestTriggerRejectsCompactCycleKey`); the trigger takes `pg_advisory_xact_lock` on the template id before it reads the template, so the route and the scanner never both create it (`invoices.TestTriggerConcurrentWithScanOneChild`), and a template `PUT` or `PATCH`, and the generic `PATCH /invoices/{id}` when it edits a template (note 43), which take the same lock first, is seen whole: a child never mixes the new lines with the old discount or withholding (`invoices.TestTriggerWaitsForATemplatePut`, `invoices.TestTriggerWaitsForATemplatePatch`). The lock order is the template lock, then an invoice row `FOR UPDATE`, then the number lock (note 34); the lookup takes the oldest match, so a duplicate already in the data is returned deterministically. A replay returns the first child unchanged (`invoices.TestTriggerReplayReturnsSameInvoice`).
    After the switch: duplicates already in the data stay; find them (two children with one template and the same cycle date) and void the extras.

36. **Inputs that were 500s in Python are client errors in Go.**
    Python: no existence check on `customer_id`, `project_id` or `item_id` when creating (`app/services/invoicing.py:228-280`) or on the milestone route (`app/api/v1/routers/invoices.py:70-86`); a PATCH with `currency: null` or `invoice_type: null` reaches a NOT NULL column (`invoicing.py:293-310`); `invoice_type`, `payment_terms`, `invoice_number` and `po_so_number` have no `max_length` although the columns are 20, 30, 50 and 100 (`app/schemas/invoice.py:60-66`); `GET /invoices?limit=-1` reaches Postgres as a negative LIMIT (`routers/invoices.py:434`); deleting a DRAFT that a payment or a quotation's `converted_invoice_id` references breaks a foreign key with no ON DELETE (`invoicing.py:467-474`), so a DRAFT made by a quotation conversion cannot be deleted.
    Seen: each answers 500 with no hint of the cause.
    Go (changes): an unknown customer, project or item is a 422 at the field (`invoices.TestCreateUnknownCustomerIs422`); an over-long string is a 422 at the field, and `currency` must be exactly three characters as in Python (`invoices.TestCreateOverlongPaymentTermsIs422`); `null` for `currency` or `invoice_type` on PATCH is a 422 `value cannot be null` (`invoices.TestPatchNullCurrencyIs422`); `limit` below 1 is a 422, the default 200 and the 500 maximum stay (`invoices.TestListLimitBounds`); deleting a referenced DRAFT is 409 `invoice is referenced by a payment or quotation` (`invoices.TestDeleteReferencedByPaymentIs409`, `invoices.TestDeleteReferencedByQuotationIs409`). Messages for the length and limit 422s are Huma's, not Pydantic's; only the status matches.
    After the switch: none; the frontend should show `detail` as is.

37. **`line_items: null` on create is 400 in Go, 422 in Python.**
    Python: `line_items` is `list[...] = Field(default_factory=list)` on both create schemas (`app/schemas/invoice.py:75`, `:122`), so an explicit `null` is a 422 `Input should be a valid list`; only an omitted or empty list reaches the service's 400 `invoice must have at least one line item`. On PATCH it is `list[...] | None` (`:96`), so `null` keeps the lines.
    Go: create and template create read `line_items` as a plain list (the platform's optional-field hook does not reach inside `NotNull`/`Optional` list values), so `null`, omitted and `[]` are all the 400 sentence (`invoices.TestCreateRequiresLineItems`, `invoices.TestTemplateRequiresLineItems`). PATCH is Python's: `null` and omitted keep the lines (`invoices.TestPatchLinesNullKeepsLines`).
    After the switch: none; make the platform hooks unwrap `Optional` if exact 422s ever matter.

38. **A due date that overflows is a 400 in Go; Python answered 500.**
    Python: `_parse_payment_terms_days` reads `Net <digits>` with `\d` and `\s` (`app/services/invoicing.py:145-154`), which match Unicode digits; the day count goes into `timedelta` and `date + timedelta` (`:246-250`, `:335-338`), so `Net 99999999` or a result past 9999-12-31 is an OverflowError, a 500. The milestone route takes `due_in_days` unbounded, negatives included (`app/schemas/invoice.py:19`; `invoicing.py:221`), and its customer lookup has no try, so a customer whose stored withholding default no longer normalises is a 500 too (`routers/invoices.py:70-86`).
    Go: `PaymentTermsDays` reads ASCII digits only, so `Net ٣٠` is 14 days here and 30 in Python (`invoices.TestPaymentTermsDays`); a day count that overflows `int`, or a due date outside 0001-01-01..9999-12-31, is a 400 `due date is out of range` on create, PATCH's due-date fill, trigger and milestone (`invoices.TestDueDateOutOfRangeIs400`, `invoices.TestAddDaysBounds`); a stored withholding default that does not normalise is a 400 `invalid_withholding` on the milestone route. Negative and large `due_in_days` inside the range are accepted as in Python.
    After the switch: none.

39. **"Not found" is 400 on every write route that goes through the service.**
    Python: `InvoicingError` becomes a 400 with `detail=str(e)` (`app/api/v1/routers/invoices.py:63`, `:98`, `:314`, `:380`, `:406`, `:462`, `:477`, `:504`, `:630`, `:790`), so PATCH, finalize, void, delete, mark-paid, usage lock, trigger and `PUT` on a template answer 400 `invoice <id> not found`, `template invoice <id> not found` or `recurring template <id> not found` (`invoicing.py:286`, `:420`, `:470`, `:510`, `:618`, `:646`, `:676`, `:694`). The GET routes and `GET`/`PATCH` on a template answer 404 `invoice not found` / `recurring template not found` (`routers/invoices.py:294-295`, `:332-333`, `:490`).
    Seen: the same missing id is 404 on read and 400 on write, and `PUT /recurring-templates/{id}` disagrees with its own `GET`.
    Go: the same statuses and sentences (`invoices.TestPatchUnknownIs400`, `invoices.TestFinalizeUnknownIs400`, `invoices.TestVoidUnknownIs400`, `invoices.TestMarkPaidUnknownIs400`, `invoices.TestLockUnknownIs400`, `invoices.TestTriggerUnknownTemplateIs400`, `invoices.TestPutMissingTemplateIs400`, `invoices.TestGetInvoice404`, `invoices.TestGetNonTemplateIs404`). Codes are stable snake_case (`invoice_not_found`, `recurring_template_not_found`).
    After the switch: make write routes 404, with the frontend reading `status` and not the sentence.

40. **Templates show up in customer views.**
    Python: billing-summary and `GET /customers/{id}/invoices` have no `is_template` filter (`app/api/v1/routers/customers.py:157-164`, `:213-218`). A template is a DRAFT with `balance_due = amount`, so it lands in `recurring` and `unpaid_invoices` and raises `total_unpaid`. Both queries order by `issue_date DESC`, and templates have no issue date, which Postgres sorts first (`:165-170`), so `last_invoice_id` and `last_invoice_issue_date` point at a template (or are null) whenever the customer has one.
    Go: kept, with the tie-break `created_at DESC, invoice_id DESC` added: the latest invoice and the customer's invoice list both order by `issue_date DESC NULLS FIRST`, so a template (no issue date) still comes first; the open-invoice list is `due_date ASC` (NULL last, as Postgres does) then `created_at, invoice_id` (`invoices.TestBillingSummaryIncludesTemplates`, `invoices.TestBillingSummaryLastInvoice`, `invoices.TestBillingSummaryOpenInvoicesTieBreak`, `invoices.TestCustomerInvoicesOrder`).
    After the switch: filter `is_template = false` in both views, or return templates in their own field.

41. **billing-summary totals and the balances list.**
    Python: `total_unpaid` sums DRAFT balances too (`app/api/v1/routers/customers.py:157-161`); `paid_last_12_months` is a flat 365-day window from `date.today()` summing the cash `amount` of CLEARED payments, not the withheld part (`:172-198`); `GET /customers/balances` groups by customer and currency with no ORDER BY (`app/services/stats.py:401-425`).
    Go: kept, with "today" the UTC date (note 5): exactly 365 days is in and 366 is out (`invoices.TestBillingSummaryTwelveMonthWindow`). The balances list is ordered `customer_id, currency` so its output is stable (`invoices.TestBalancesRoute`); every list is `[]`, never `null`.
    After the switch: decide whether drafts belong in "unpaid", and whether the window should be calendar months.

42. **Void is permissive.**
    Python: `void_invoice` refuses only PAID (`app/services/invoicing.py:691-700`). It allows DRAFT, OPEN, SENT, PARTIAL (payments and `wht_booked` stay, the balance is zeroed), an invoice that is already VOID, and templates. The required `reason` is discarded (`app/schemas/invoice.py:33-34`, `routers/invoices.py:467-479`). Voiding a template does not stop generation: the scanner and the rows endpoint do not filter on status (`app/workers/tasks/recurring.py:52-56`, `routers/invoices.py:222-228`).
    Go: kept (`invoices.TestVoidFromEachNonPaidStatus`, `invoices.TestVoidPaidIs400`, `invoices.TestVoidRequiresReason`); `reason` is required (422 when missing) and not stored.
    After the switch: refuse a void while payments exist; store the reason; end a template instead of voiding it.

43. **`PATCH /invoices/{id}` edits templates and never moves an existing due date.**
    Python: `update_invoice` has no `is_template` guard, only the DRAFT check (`app/services/invoicing.py:283-288`), so a template can be edited like an invoice (`PUT /recurring-templates/{id}` is the intended route). The due date is filled only when it is null and an issue date exists, so changing `issue_date` leaves an existing `due_date` where it was (`:335-338`).
    Go: kept (`invoices.TestPatchIssueDateKeepsExistingDueDate`). The generic PATCH takes the template advisory lock first, as the template routes do (note 35), so on a template it waits for a running trigger and a trigger waits for it (`invoices.TestPatchTemplateViaInvoiceRouteWaitsForTrigger`).
    After the switch: refuse templates on this route, or recompute the due date when the issue date or the terms change.

44. **Rounding, discounts and line positions.**
    Python: each line amount is `round4(qty * price)` but the subtotal is `round4(sum of the raw qty * price)`, so the subtotal can differ from the sum of the line amounts (`app/services/invoicing.py:43-44`, `:57-60`, `:186`); totals without withholding are not rounded to the currency's precision, only the withholding path is (`app/services/withholding.py:27-30`); a PERCENT discount has no upper bound and clamps the total at 0 (`app/schemas/invoice.py:71`, `invoicing.py:62-71`); `discount_value` is stored even when `discount_type` is null. `position` defaults to 0, so the `else idx` fallback is dead and lines that omit it tie (`invoicing.py:182`).
    Go: kept, half-up as `_round4` (`invoices.TestSubtotalRoundsRawSum`, `invoices.TestPercentDiscount`, `invoices.TestAmountDiscountClampsAtZero`, `invoices.TestDiscountValueWithoutTypeIgnored`, `invoices.TestNoWHTKeepsFourPlaces`); lines of equal position are ordered by `line_item_id` so the order is stable (`invoices.TestGetRendersLinesAndStoredValues`).
    After the switch: round the subtotal from the rounded lines; cap a percent discount at 100.

45. **Generated children are dated today, and a manual trigger ignores pause and end.**
    Python: a generated invoice gets `issue_date = date.today()` and a due date counted from today (`app/services/invoicing.py:541`, `:574`), not from the cycle start, so a late or backfilled cycle is dated today. `trigger_recurring_cycle` does not look at `paused` or the end condition (`app/services/recurring_schedule.py:185-186` says it is manual on purpose), so a paused or ended template still generates on request.
    Go: kept, with "today" the UTC date (note 5) (`invoices.TestTriggerChildDatesAreToday`, `invoices.TestTriggerIgnoresPauseAndEnd`).
    After the switch: date the child by its cycle (or keep today on purpose), and warn in the UI before triggering an ended template.

46. **Hosting coverage counts `interval` as months and labels every line.**
    Python: `bundle_months = cfg.get("interval") or 1` whatever the frequency, so a WEEKLY interval-1 template gets one month of coverage (`app/services/hosting.py:74`). The `Coverage: <start> to <end>` label is appended to every line of the invoice, not only the hosting lines (`:81-83`). The window ends at the start plus the months, minus one day, clamped to month ends.
    Go: kept (`invoices.TestTriggerRecurringCycleStampsCoverageOnHostingInvoice`, including the month-end case 2026-11-30 to 2027-02-27).
    After the switch: derive the window from the frequency, and label only the hosting lines.

47. **The recurring-template rows endpoint and END_NOW.**
    Python: "previous" is the live child with the latest `issue_date`, not the latest cycle (`app/api/v1/routers/invoices.py:135-172`), so backfilling an old cycle today makes it the previous one and moves Next backwards; `next_run_date` can be in the past and is not flagged; the endpoint runs one query per template (`:230-232`). `END_NOW` writes `end_date = min(today - 1, next_run - 1)` and can write an end date before `start_date`; nothing checks `end_date >= start_date` (`:195-212`, `app/services/recurring_schedule.py:82-85`).
    Go: kept (`invoices.TestRowsPreviousIsCycleDateNotGenerationDate`; `invoices.TestEndNowEndsTemplateThatNeverGenerated` ends a never-run template on the day before its start). Where Python left the order undefined or crashed, Go chooses: children with equal `issue_date` go newest `created_at` first, then `invoice_id`; "previous" reads `cycle_key` as `YYYY-MM-DD` only, so a legacy key such as `20260601`, or a `cycle_key` that is not a string (Python: TypeError, a 500), falls back to the child's `issue_date` (`invoices.TestRowsPreviousFallsBackToIssueDate`); a stored `billing_cycle_ref` that is not a JSON object (Python: 500 on `.get`) shows as a MISCONFIGURED row, and PAUSE or RESUME starts from an empty config (`invoices.TestRowsMisconfigured`). `PUT` and `PATCH` take the template's advisory lock (note 35), read the template `FOR UPDATE` and decide "ended" inside the transaction, where Python checked before it wrote. The rows come from two queries, not one per template.
    After the switch: pick the previous cycle by `cycle_key`; refuse an end date before the start; flag a past `next_run_date`.

48. **The usage lock is effectively dead, and its cut-off scan reads `cut_off_day` like `int()`.**
    Python: no API route writes `cut_off_day`, so the daily scan can never match an API-made invoice, and if one matched, `_stub_accrued` raises and the whole job rolls back (note 1; `app/workers/tasks/usage_lock.py:23-24`, `:40-43`). The lock route never checks `cfg.locked`, so a DRAFT can be locked again, never checks `is_template`, answers `usage invoice already locked (status=X)` for any non-DRAFT status, and ignores the discount, the lines and the subtotal (`app/services/invoicing.py:604-640`). `int(cut_off_day)` accepts `true`, `25.5`, `25.0`, underscores (`"1_0"` is 10) and Unicode digits.
    Go: the lock route and `CutoffScan` are ported as written (`invoices.TestLockSetsCfg`, `invoices.TestLockNonDraftIs400`, `invoices.TestLockNonUsageIs400`, `invoices.TestUsageLockRecomputesFromSnapshot`); the scan stays one transaction for the whole run, so an accrual error changes nothing (`invoices.TestCutoffScanAccrualErrorChangesNothing`); each invoice is read `FOR UPDATE` where Python used a plain `get`; `cut_off_day` is read as an integer or a string of ASCII digits with an optional sign, and `true`, `25.5`, `25.0` (Python's `int(25.0)` is 25, so it locks), underscores such as `"1_0"` or Unicode digits are logged at WARN and skipped where Python's `int()` takes them; whitespace around a string, as in `" 25 "`, is accepted as in Python (`invoices.TestCutoffScanSkipsLockedAndOtherDays`, `invoices.TestCutoffScanLocksMatchingDay`). The scan is not scheduled yet.
    After the switch: as note 1; the scheduler plan wires the real accrual source or retires the scan.

49. **The recurring scanner.**
    Python: `scan_and_generate` loops every template in one session and commits at the end, so one failing template aborts the run and nothing commits (`app/workers/tasks/recurring.py:47-72`); `generated` counts replays of cycles that already exist (`:69`); "today" is the process's `date.today()` (note 5). `draft_summary` carries a formatted amount string, an `/invoices/<id>/edit` link and `(unknown customer)` for a missing customer (`app/services/draft_notifications.py:25-35`).
    Go: `Scan` runs each template in its own transaction, logs a failure and moves on, so one bad template no longer blocks the rest (`invoices.TestScanIsolatesAFailingTemplate`); `generated` still counts replays (`invoices.TestScanGeneratesDueTemplatesAndSkipsPausedEnded`); "today" is the UTC date; templates go in `created_at, invoice_id` order; one notifier call per scan carries only the new drafts (`invoices.TestScanNotifiesOnceWithNewDraftsOnly`). `DraftSummary` carries the invoice id, a decimal amount and a nil customer name instead of the link, the formatted string and `(unknown customer)`; the email plan formats them. A notifier that panics is recovered and logged, so a broken notifier never fails the trigger route or the scan (`invoices.TestTriggerSurvivesAFailingNotifier`), and the notifier gets the caller's context without its cancellation, so a scan or request ending just after a commit still reports the draft; a trigger that committed is counted even when the context ends right after it (`invoices.TestScanCountsACommitBeforeTheContextEnds`, `invoices.TestNotifyDraftsOutlivesTheCallersContext`); Python logged and went on per recipient. The scan logs through `slog.Default()` until the scheduler plan passes a logger.
    After the switch: nothing; keep the isolation.

50. **`expected_cash` prints `"0"` in Python and `"0.0000"` in Go.**
    Python: `max(_ZERO, value)` returns its first argument when the result is exactly zero, so the JSON string is `"0"` instead of the four-place form every other amount has (`app/services/withholding.py:52-56`).
    Go: `expected_cash` always has four places (`invoices.TestInvoiceOutFieldOrder`). An allowed difference for `compare`.
    After the switch: none.

51. **The schedule parser is stricter about dates and number shapes.**
    Python: `parse_schedule` reads `start_date` and `end_date` through `date.fromisoformat`, which on Python 3.11 also accepts `20260301` and week dates (`app/services/recurring_schedule.py:55-62`); `isinstance(interval, int)` accepts `True` as 1 and rejects `2.0` (`:72`, `:88`); integers beyond 64 bits are fine.
    Go: dates are `YYYY-MM-DD` only; `interval: true` is accepted as 1 and `false` is refused, as in Python; a JSON number is accepted only when it is a whole number, and `2.0` or `1e2` as text is refused as in Python; integers beyond `int64` are refused (`schedule.TestParseNumberShapes`, `schedule.TestParseErrorSentences`, `schedule.TestParseRefusesCompactDates`, `schedule.TestDatesUseTheirOwnCalendarDay`). The API writes `interval` as an integer, so only hand-edited rows can differ.
    After the switch: none, unless a stored schedule uses a compact date; the scanner then reports `invalid schedule` for it.

52. **An invoice's currency is any three characters, in any case, and PATCH can change it.**
    Python: `currency` is `Field(min_length=3, max_length=3)` on create and `| None` with the same bounds on PATCH (`app/schemas/invoice.py:61`, `:82`); nothing checks it against a list of currencies or upper-cases it, and `update_invoice` writes it on any DRAFT (`app/services/invoicing.py:293-310`), lines and withholding already priced in the old one.
    Go: kept: exactly three characters, stored as sent (`invoices.TestCurrencyIsAnyThreeCharacters`, `invoices.TestCreateOverlongPaymentTermsIs422`).
    After the switch: validate against the currencies the ledger knows, store them upper-case, and refuse a currency change once lines exist (or reprice them).

53. **The awaiting-finalization list has no limit.**
    Python: `GET /invoices/awaiting-finalization` returns every non-template DRAFT, newest first, with no `limit` or paging (`app/api/v1/routers/invoices.py:411-423`).
    Go: kept, with the tie-break `invoice_id DESC` (`invoices.TestAwaitingFinalizationListsDraftsOnly`).
    After the switch: add `limit` as `GET /invoices` has, or page it.

54. **A PATCH after the usage lock throws the locked total away.**
    Python: `lock_usage_invoice` sets `amount` and `balance_due` from the accrued amount and leaves the lines alone (`app/services/invoicing.py:604-640`); `update_invoice` accepts any DRAFT, the locked one included, and recomputes `subtotal`, `amount` and `balance_due` from the lines (`:283-341`), so editing a note on a locked usage invoice with no lines sets its amount to 0, while `cfg.locked` stays true.
    Go: kept (`invoices.TestPatchAfterUsageLockRecomputesFromLines`).
    After the switch: keep the stored totals while `cfg.locked`, or refuse the edit.

55. **A template's `interval` and `end_after_cycles` are at most 1000.**
    Python: both are `int` with `ge=1` and no upper bound (`app/schemas/invoice.py:101`, `:105`); a huge `interval` is stored, and the cycle arithmetic later leaves the date range (`app/services/recurring_schedule.py:109-130`: `date(...)` raises ValueError past year 9999, `date.fromordinal` OverflowError for an int too large for C), a 500 on the rows view or the trigger and a failed scan run.
    Go (a change): both are 422 above 1000 on template create and PUT (`invoices.TestTemplateScheduleCountsAtMost1000`); 1000 is accepted. A stored config beyond that (hand-edited) is still read.
    After the switch: none.

56. **Quotation numbers are handed out under a lock, and a duplicate is a 409 (a change).**
    Python: `generate_quotation_number` scans every `Q<year>%` number with no lock, takes the highest all-digit tail (`str.isdigit`, which also accepts Unicode digits that `int()` then trips over) and adds one; the year is the server's local `date.today()` (`backend-python/app/services/quoting.py:73-89`, `:87`). Two creates racing read the same maximum, and the second hits the unique constraint at flush with an unhandled IntegrityError, a 500 (`:118`, `:141`). The same gap as note 34 for invoices.
    Go: `quotations.NextQuotationNumber` takes `pg_advisory_xact_lock` keyed `quotation-number:<year>` (UTC year of the clock), reads only ASCII-digit tails, and ignores invoice numbers (`quotations.TestNextQuotationNumberConcurrent`, `quotations.TestCreateConcurrentNumbersAreConsecutive`, `quotations.TestNextQuotationNumberIgnoresInvoiceNumbers`). A number that already exists (client-supplied, or set by PATCH) answers 409 `quotation_number_exists` `quotation number already exists` (`quotations.TestCreateDuplicateNumberIs409`).
    After the switch: none.

57. **Converting a quotation makes one invoice, however many requests race (a change).**
    Python: `convert_to_invoice` reads the quotation with a plain `db.get` and no lock (`app/services/quoting.py:274`); two concurrent converts both see DRAFT, SENT or ACCEPTED, both build an invoice, and the quotation ends pointing at one of them while the other is orphaned.
    Go: the quotation row is read `FOR UPDATE` first, then the invoice number lock is taken (lock order: quotation row, then number), so the second request waits, sees INVOICED and gets Python's 400 `cannot convert a INVOICED quotation to invoice` (`quotations.TestConvertConcurrentMakesOneInvoice`, `quotations.TestConvertTwiceIs400`). The sentence keeps Python's grammar (`a INVOICED`) for each refused status (`quotations.TestConvertRefusedStatuses`).
    After the switch: none.

58. **Bill payments and voids read the bill under a row lock (a change).**
    Python: `record_bill_payment` reads the bill with `db.get`, compares and writes the balance with no lock, so two payments racing both pass the balance check and one is lost (`app/api/v1/routers/bills.py:95-116`); `void` reads it the same way (`:126-136`).
    Go: both routes read the bill `FOR UPDATE` in one transaction. Of two payments of 600 against 1000 one is 200 and the other is 400 `amount 600 exceeds balance due 400.0000` (`bills.TestRecordPaymentConcurrentNeverOverpays`).
    After the switch: none.

59. **Requests that Python answered with a 500 are client errors (a change, quotations and bills).**
    Python: an unknown `customer_id`, `project_id` or `item_id` on a quotation, or an unknown `vendor_id`, `project_id`, `item_id` or `expense_account_id` on a bill, fails at flush with a foreign-key IntegrityError that no route catches (`app/services/quoting.py:122-141`, `app/services/billing_ap.py:46-63`, `:65-80`); strings longer than their column (quotation `quotation_number` 50, `po_so_number` 100, `payment_terms` 30, `accepted_by` 255; bill `bill_number` and `po_number` 100, `payment_terms` 30) are DataErrors; an explicit `null` for a NOT NULL column on PATCH (quotation `customer_id` and `currency`, bill `vendor_id` and `currency`) is an IntegrityError (`quoting.py:173-174`, `billing_ap.py:88-92`); `GET /quotations?limit=-1` is a Postgres error (`app/api/v1/routers/quotations.py:54-58`); a default `valid_until` of `issue_date + 30 days` past 9999-12-31 raises OverflowError (`quoting.py:120`).
    Go: an unknown reference is 422 at the field; a string longer than its column is 422 (`maxLength` from the DDL; accept: `accepted_by` > 255 → 422); a bill currency longer than 3 once upper-cased (`"ßab"` is `"SSAB"`) is 422 at `currency` on create and PATCH; a NOT NULL `null` is 422; `limit` below 1 is 422 (`limit=0` was an empty list in Python); the `valid_until` overflow is 400 `valid_until_out_of_range` `valid until is out of range` (`quotations.TestCreateUnknownCustomerIs422`, `quotations.TestCreateFieldValidationIs422`, `quotations.TestAcceptRefusals`, `bills.TestCreateUppercasesCurrency`, `quotations.TestListFiltersOrderLimit`, `quotations.TestCreateValidUntilOutOfRangeIs400`, `bills.TestCreateUnknownVendorIs422`, `bills.TestCreateUnknownExpenseAccountIs422`, `bills.TestCreateRefusesBadFields`, `bills.TestPatchNullVendorIs422`, `bills.TestPatchUnknownReferencesAre422`). A convert whose due date leaves the calendar (issue date near 9999-12-31, or terms such as `Net 99999999999999999999`) is 400 `due_date_out_of_range` where `quoting.py:316` raised OverflowError (`quotations.TestConvertDueDateOutOfRangeIs400`). A create with `line_items: null` is 400 `line_items_required` on quotations where Python answered 422 (`quotations.TestCreateRequiresLineItems`).
    After the switch: none.

60. **Projects: only a duplicate code is "project code already exists" (a change).**
    Python: `create_project` and `update_project` turn every IntegrityError into the code sentence (`app/services/projects.py:39-43`, `:58-62`). An unknown `customer_id` on create answers 400 `project code 'X' already exists`; on PATCH, an unknown customer or an explicit `null` for `code`, `name`, `currency`, `status`, `active` or `customer_id` (a NotNullViolation) answers 400 `project code already exists`.
    Go: only `projects_code_key` (and its index) maps to the code sentence (`projects.TestCreateProjectDuplicateCodeRaisesAndLeavesOneRow`, `projects.TestPatchDuplicateCodeIs400`); an unknown customer is 422 at `customer_id` and a NOT NULL `null` is 422 (`projects.TestCreateUnknownCustomerIs422`, `projects.TestPatchNullForNotNullColumnIs422`, `projects.TestPatchNullNameIs422`). The create sentence still prints the code as Python's `repr` does, with double quotes when the code holds a `'`.
    After the switch: none.

61. **Project code and currency are normalised after validation; over-long results are 422.**
    Python: `code` and `name` pass `min_length=1` and `max_length` as sent, then `create_project` strips and upper-cases the code and strips the name (`app/services/projects.py:29-31`); `"   "` passes validation and is stored as `""`, and `str.upper` can lengthen (`"ß"` to `"SS"`), so a 40-character code or a 3-character currency can overflow its column after validation, a DataError and a 500. PATCH strips and upper-cases `code`, upper-cases `currency` and does not strip `name` (`:51-57`).
    Go: the normalised value is checked again: a blank code is 422 at `code` and an upper-cased code over 40 or currency over 3 is 422 (`projects.TestBlankCodeIs422`, `projects.TestNormalisedCodeAndCurrencyOverflowIs422`); upper-casing uses full case mapping (`golang.org/x/text/cases`) so `"ß"` becomes `"SS"` as in Python; PATCH leaves `name` as sent (`projects.TestUpdateProjectEditsAndNormalizes`). Go trims with `strings.TrimSpace`, which does not strip U+001C to U+001F as Python's `str.strip` does.
    After the switch: none.

62. **Deleting a project also counts the quotations that reference it (a change).**
    Python: the delete counts invoices (every status, templates included) and bills (VOID included) and ignores quotations; a project referenced only by a quotation passes the check and fails at commit on `quotations_project_id_fkey`, a 500 (`app/api/v1/routers/projects.py:101-112`). Count and delete take no lock.
    Go: the project is read `FOR UPDATE`, quotations are counted with the invoices and bills, and the answer is the same 400 `cannot delete: project is referenced by N invoice(s)/bill(s)`, so N includes quotations while the sentence keeps Python's wording (`projects.TestDeleteProjectReferencedByQuotationIs400`, `projects.TestDeleteProjectReferencedByInvoicesAndBills`).
    After the switch: reword to name quotations, or stop counting VOID documents and templates.

63. **The project summary adds up amounts in any currency and prints `"0"` only in Python.**
    Python: `SUM(amount)` and `SUM(balance_due)` run over every OPEN, SENT, PARTIAL or PAID invoice (templates included) and `SUM(amount)` over every OPEN, PARTIAL or PAID bill, in any currency; `contract_remaining` subtracts that mixed sum from a project-currency value; `paid_total` is invoiced minus outstanding, so withheld tax and mark-paid write-offs count as paid (`app/api/v1/routers/projects.py:126-161`). The sums are `Decimal` values never read back through a column, so an empty project prints `"0"` where every stored amount prints four places.
    Go: kept, with four places everywhere (`projects.TestSummaryMixesCurrencies`, `projects.TestSummaryTotals`, `projects.TestSummaryContractRemainingCanBeNegative`); the `"0"` against `"0.0000"` difference is an allowed one for `compare` (`projects.TestSummaryEmptyProject`).
    After the switch: sum per currency, or convert through the ledger's rates.

64. **A quotation can be converted from DRAFT or SENT, and its payment terms beat `due_in_days`.**
    Python: `convert_to_invoice` refuses only INVOICED, VOID and DECLINED, so DRAFT, SENT, ACCEPTED and EXPIRED convert and `accepted_at` and `accepted_by` stay null (`app/services/quoting.py:277-282`); a non-empty `payment_terms` replaces `due_in_days`, an unparseable one meaning 14 days (`:284-287`); the customer's default withholding is the only source and is computed on the stored quotation amount (`:289-295`); the invoice number's year is today's, not the issue date's (`:301`).
    Go: kept (`quotations.TestConvertFromDraftSentAcceptedExpired`, `quotations.TestConvertTermsOverrideDueInDays`, `quotations.TestConvertFieldMapping`, `quotations.TestQuoteConversionAppliesCustomerDefault`, `quotations.TestConvertInvalidCustomerWithholdingIs400`); "today" is the UTC date of the clock, and the invoice number is allocated under the invoice number lock after the quotation row (note 57).
    After the switch: require ACCEPTED (or record the acceptance on convert), and let an explicit `due_in_days` win.

65. **EXPIRED is never set and `valid_until` is never enforced.**
    Python: the status exists in the schema and the docstring (`app/schemas/quotation.py:12`, `app/models/quotation.py:19`) but nothing sets it, and accept, decline and convert never look at `valid_until`. A row set to EXPIRED by hand can be converted or voided but not edited (`app/services/quoting.py:151-154`, `:277`).
    Go: kept, with EXPIRED behaving as above (`quotations.TestConvertFromDraftSentAcceptedExpired`, `quotations.TestPatchNonEditableIs400`, `quotations.TestVoidFromEveryStatusButInvoiced`).
    After the switch: a job that expires SENT quotations past `valid_until`, and a refusal to accept an expired one.

66. **Voiding a quotation is permissive, and a DRAFT can be accepted without being sent.**
    Python: `void_quotation` refuses only INVOICED, so a VOID quotation can be voided again and an ACCEPTED or DECLINED one can be voided, with no reason (`app/services/quoting.py:243-251`); mark-sent, accept and decline run from DRAFT or SENT (`:209`, `:223`, `:236`).
    Go: kept (`quotations.TestVoidFromEveryStatusButInvoiced`, `quotations.TestVoidInvoicedIs400`, `quotations.TestMarkSentFromDraftAndSent`, `quotations.TestMarkSentFromAcceptedIs400`, `quotations.TestAcceptRefusals`, `quotations.TestDeclineFromSent`).
    After the switch: close the state machine (no void of ACCEPTED, no accept of DRAFT) and take a reason.

67. **Quotation PATCH: `null` clears the number, `line_items: []` leaves an empty quotation, `""` is stored.**
    Python: PATCH copies every sent field with `setattr`, so `quotation_number: null` (what the form sends when the field is blank) sets it to null and `""` is stored as `""`, where create would have generated one; a second `""` collides on the unique constraint, a 500 (`app/services/quoting.py:159-174`). `line_items: []` is accepted and replaces the lines with none (`:176-183`), leaving a 0-amount quotation that convert later refuses with `quotation has no line items to convert`. A PATCH that nulls `valid_until` or `issue_date` does not re-default them. Totals are recomputed on every PATCH, including a discount-only one.
    Go: kept, except that the second `""` is the 409 of note 56 (`quotations.TestPatchNullNumberClears`, `quotations.TestPatchEmptyLinesAccepted`, `quotations.TestPatchRepricesOnDiscountOnly`, `quotations.TestConvertUnknownOrEmptyIs400`).
    After the switch: generate a number when it is cleared, and refuse an empty `line_items`.

68. **A missing quotation is 400 on writes and 404 on read; bills and projects are 404 everywhere.**
    Python: every quotation service function raises `QuotingError("quotation <id> not found")`, which the router turns into 400 (`app/services/quoting.py:150`, `:208`, `:222`, `:235`, `:246`, `:256`, `:276`; `app/api/v1/routers/quotations.py:132-133`), while `GET /quotations/{id}` answers 404 `quotation not found` (`:76`). Bill and project routes look the row up first and answer 404 `bill not found` or `project not found` (`app/api/v1/routers/bills.py:97`, `app/api/v1/routers/projects.py:26-30`). Same split as note 39.
    Go: kept (`quotations.TestGetQuotation404`, `quotations.TestLifecycleUnknownIs400`, `quotations.TestPatchUnknownIs400`, `bills.TestGetBill404`, `bills.TestPatchUnknownBillIs404`, `bills.TestBillPaymentRoutes404`, `projects.TestUpdateProjectNotFound`).
    After the switch: 404 on every write too.

69. **A bill's `status` can be written through PATCH, and that is the only way to open one.**
    Python: `BillUpdate.status` is any of DRAFT, OPEN, PARTIAL, PAID or VOID and `update_bill` copies it (`app/schemas/bill.py:63`, `app/services/billing_ap.py:85-92`): DRAFT or OPEN can jump to PAID with no payment, to VOID without the void note, and OPEN back to DRAFT (which re-baselines `balance_due` on the next line edit). Nothing else opens a bill; the frontend never sends `status` and has no open action, so a bill made in the UI stays DRAFT and the record-payment button never shows. Only the MCP tools open one.
    Go: kept (`bills.TestPatchStatusOpensBill`, `bills.TestPatchPaidIs400`); only a DRAFT or OPEN bill can be edited: PARTIAL, PAID and VOID are refused (`billing_ap.py:85-86`).
    After the switch: an explicit open action, and a status machine that refuses a jump to PAID.

70. **Editing an OPEN bill's lines leaves `balance_due` alone; a discount-only edit does not reprice.**
    Python: `_apply_totals` recomputes `subtotal` and `amount` but re-baselines `balance_due` only while the bill is DRAFT (`app/services/billing_ap.py:27-42`), so on an OPEN bill `balance_due` can end above or below `amount` (and on a PARTIAL one only when `status: "PARTIAL"` and `line_items` arrive in one PATCH of an OPEN bill: a PARTIAL bill itself cannot be edited, `:85-86`, and the status is set before the totals, `:88-92`); `update_bill` calls it only when `line_items` is sent (`:94-112`), so changing `discount_type` or `discount_value` alone stores the new discount and keeps the old totals.
    Go: kept (`bills.TestPatchOpenBillLinesKeepsBalance`, `bills.TestPatchDraftLinesRebaselinesBalance`, `bills.TestPatchLineItemsOmittedNullAndEmpty`).
    After the switch: recompute totals on every edit and reject an edit that would drop `amount` below what is paid.

71. **Bill totals round half-even, unlike quotations and invoices.**
    Python: each line is `quantize(0.0001)` under the context's ROUND_HALF_EVEN, the subtotal is the sum of the rounded lines, a PERCENT total is `subtotal * (1 - pct/100)` unrounded and clamped at zero before the final quantize, and a line's position is `ln.position or idx`, so an explicit position 0 becomes its index (`app/services/billing_ap.py:23-38`, `:71`, `:105`). Quotations and invoices round half-up per line and total.
    Go: kept, with the rounding per call site (`bills.TestBillLinesRoundHalfEven`, `bills.TestBillSubtotalSumsRoundedLines`, `bills.TestBillPercentDiscount`, `bills.TestBillDiscountedAmountRoundsHalfEven`, `bills.TestBillPositionZeroBecomesIndex`).
    After the switch: one rounding rule for every document.

72. **A bill can be created with no lines, no dates and a repeated number.**
    Python: `create_bill` accepts an empty `line_items` and makes a 0-amount DRAFT; `issue_date` and `due_date` are not defaulted, `payment_terms` is stored and never parsed, and `bill_number` is not unique (`app/services/billing_ap.py:45-81`; `bill_number` is a plain `varchar(100)` in `backend/migrations/00001_baseline.sql:30`). A `line_items: null` on create is a 422 in Python and the same as no lines in Go.
    Go: kept (`bills.TestCreateBillWithoutLines`, `bills.TestCreateBillSetsFieldsAndTotals`).
    After the switch: require a line, derive `due_date` from the terms, and make `(vendor_id, bill_number)` unique.

73. **Bill record-payment records nothing but a balance, and echoes the amount as Python printed it.**
    Python: no payment row and no journal entry (note 8); `payment_date`, `payer_name`, `payer_reference` and `notes` are validated and dropped; the status is 200, where an invoice's record-payment is 201 (`app/api/v1/routers/bills.py:84-116`, `app/schemas/payment.py:50-56`). The overpayment sentence prints `Decimal(payload.amount)`; because FastAPI hands pydantic a JSON number as a float, `600.00` prints `600.0`, `6e2` prints `600.0` and `1.5e3` prints `1500.0`, while a JSON string prints as sent (`"600.00"` stays `600.00`, `"6e2"` becomes `6E+2`) and an integer prints its digits.
    Go: the same three printing rules (`bills.TestRecordPaymentOverpaymentIs400`); `payment_date` is required and discarded (`bills.TestRecordPaymentValidation`); nothing is posted (`bills.TestRecordPaymentWritesNoJournal`); an exact payment ends PAID with balance 0 (`bills.TestRecordPaymentExactBalancePays`). Python also compares and subtracts that float-derived amount (`amount = Decimal(payload.amount)`, `bills.py:102-108`) while Go uses the exact decimal sent; the two differ only for a JSON number with more than about 17 significant digits, which Python's float step rounds first.
    After the switch: post the payment to the ledger and keep a payment row, as the invoice side does.

74. **Bill void: any JSON `reason`, a leading newline, PARTIAL bills voidable; there is no REST bill delete.**
    Python: the body is an untyped required `dict`; `reason` is `(payload or {}).get("reason") or "no reason given"` and is formatted with an f-string, so a number, list or object is written as Python prints it (`{'a': 1}`); the note is appended to `notes` with a leading newline even when `notes` is null; a PARTIAL bill is voided with its balance zeroed, PAID and VOID are refused (`app/api/v1/routers/bills.py:119-136`, `app/services/billing_ap.py:118-125`). The frontend never calls it. Only the MCP tools delete a bill (DRAFT only).
    Go: kept, including Python's `repr` for strings inside a container and its truthiness rules (`bills.TestVoidAppendsNote`, `bills.TestVoidReasonNonString`, `bills.TestVoidPaidIs400`). Three differences nobody will meet: a `reason` number outside float range (`1e999`) is Huma's 400 where Python printed `inf`; a lone surrogate escape (`"\ud800"`) becomes U+FFFD where Python kept it and then failed to write (a 500); `unicode.IsPrint` stands in for `str.isprintable`, which can differ across Unicode versions.
    After the switch: type the body, require a reason, and add the delete route when the MCP plan needs it.

75. **A required body that has no required field: accept, convert and bill void.**
    Python: `AcceptQuotationRequest`, `ConvertToInvoiceRequest` and the bill void `dict` are required bodies (`app/api/v1/routers/quotations.py:123-131`, `:247-255`, `app/api/v1/routers/bills.py:119-123`) although every field is optional; a request with no body is 422. The frontend sends `{accepted_by: null}` and `{}`.
    Go: a JSON `null` body is 422, and a request with no body at all is Huma's own 400 `request body is required`, the same as the invoice void precedent (`quotations.TestAcceptRequiresBody`, `quotations.TestConvertRequiresBody`, `bills.TestVoidRequiresBody`); `due_in_days: null` is 422 as in Python.
    After the switch: make the body optional on all three.

76. **Quotation timestamps lose their offset in Python and gain a `Z` in Go.**
    Python: `last_sent_at` and `accepted_at` are `datetime.utcnow()`, a naive instant, while `created_at` is the database's `CURRENT_TIMESTAMP`; both are serialised without an offset (`app/services/quoting.py:212`, `:226`).
    Go: the clock's UTC instant, serialised with `Z` (`quotations.TestLifecycleTimestampsAreUTC`). An allowed difference for `compare`; a client that read the Python string as local time was already off by the server's offset.
    After the switch: none.

77. **Currency case differs by feature.**
    Python: projects and bills upper-case the currency (`app/services/projects.py:31`, `:55`; `app/services/billing_ap.py:51`, `:91`); quotations and invoices store it as sent (`app/services/quoting.py:127`), so a lower-case quotation converts into a lower-case invoice. `"ß"` upper-cases to `"SS"` in Python and overflows a bill's `varchar(3)` (a 500).
    Go: kept per feature (`bills.TestCreateUppercasesCurrency`, `projects.TestCreateProjectNormalizesCodeAndCurrency`, `quotations.TestConvertFieldMapping`); bills and projects both use full case mapping (`platform.PyUpper`), and an upper-cased currency longer than 3 is 422 at `currency` (notes 59 and 61).
    After the switch: one currency rule, upper-case and checked against the ledger's currencies (as note 52).

78. **The project, bill and project-invoice lists have no limit; the quotation list is capped at 500.**
    Python: `GET /projects`, `GET /bills` and `GET /projects/{id}/invoices` return every row, newest first, with no `limit` (`app/api/v1/routers/projects.py:59`, `:176`, `app/api/v1/routers/bills.py:44`); `GET /quotations` has `limit` (default 200, at most 500) (`app/api/v1/routers/quotations.py:54-58`). Rows with equal `created_at` come back in no fixed order, and so do lines with equal `position`.
    Go: kept, with the tie-breaks `<pk> DESC` on the lists and `line_item_id` on lines, and `[]` for an empty list (`projects.TestListFiltersAndOrder`, `bills.TestListFiltersAndOrder`, `bills.TestListEmptyIsArray`, `projects.TestProjectInvoicesExcludeTemplatesAndOrder`, `quotations.TestListFiltersOrderLimit`).
    After the switch: add `limit` or paging to the three open lists.

79. **A quotation, invoice or bill may point at another customer's project, or one in another currency.**
    Python: nothing compares the project's customer or currency with the document's (`app/services/quoting.py:122-138`, `app/services/billing_ap.py:46-61`); only the frontend filters the project picker by customer. The summary of note 63 then adds the mismatch up.
    Go: kept; no test pins the absence of the check.
    After the switch: refuse a project of another customer, and warn on a currency mismatch.

80. **Two open invoices that clear equally go to manual review; a null date scores zero.**
    Python: the engine sorts by score and takes the first row of an unordered query, with no runner-up check (`app/services/matching_engine.py:181-186`), so two invoices of one customer with the same amount auto-cleared against whichever the database returned first; an open invoice with a null `issue_date` or `due_date` raised `TypeError` (500).
    Go: a payment whose best candidate would clear is `PENDING_MANUAL_REVIEW` (best stays attached) when either more than one candidate is an exact match scoring 95 or more, or another exact candidate belongs to the best's own customer (two identical open invoices: the date alone must not pick one). The reasons end with `ambiguous match: <n> open invoices match exactly`, n counting those candidates with the best. An exact invoice of another customer with a weak payer match does not hold. Candidates order by score, then `due_date` (none last), then `invoice_id`. A null date scores date proximity 0 (`matching.TestTwoExactCandidatesHold`, `TestSameCustomerSecondExactInvoiceHolds`, `TestOtherCustomerExactLowScoreDoesNotHold`, `TestTieOrderDeterministic`, `TestNullDatesScoreZeroProximity`).
    Payer names lower-case with full Unicode mapping (`x/text`) and strip Python's whitespace. The date division keeps Python's 28 significant digits. One gap: Python 3.x `lower()` and x/text can differ on a few rare special casings; difflib's rune ratio itself is pinned by goldens.
    After the switch: none; an ambiguous pair is for a human.

81. **Invoice record-payment: locked, `maxLength` as 422, amounts printed as sent, ties broken by id.**
    Python: reads `balance_due` and `wht_booked` without a lock, so two payments at once can both pass the `exceeds balance` check (`app/api/v1/routers/invoices.py:522-577`); `payer_name` and `payer_reference` have no length check, so more than 255 characters is a database DataError (500); the sentences print `Decimal(payload.amount)`, where a JSON number with a fraction or exponent went through float (note 73); the payments list orders by `payment_date DESC, created_at DESC` only (`:606-612`); the payment posts no ledger entry (note 8).
    Go: the invoice is read `FOR NO KEY UPDATE` (`FOR UPDATE` would deadlock with the payment insert's key share) before the checks, so concurrent payments never overdraw (`payments.TestRecordPaymentConcurrentNeverOverdraws`); more than 255 characters is a 422 at the field (`payments.TestRecordPaymentValidation`); the sentences print amounts by note 73's three rules, the sum keeping the larger scale as Python's `Decimal` addition does (`payments.TestRecordPaymentSentencePrintsAmountAsSent`); the list adds `payment_id DESC` as the last key (`payments.TestListPaymentsTieBreakByID`); no ledger entry (`payments.TestRecordPaymentWritesNoJournal`). A PAID invoice falls through to the `exceeds balance` sentence (`payments.TestRecordPaymentPaidInvoiceExceedsSentence`); an explicit `null` `wht_amount` is a 422 as in Python; `notes` is cut at 200 runes (`payments.TestRecordPaymentNotesRuneCut`). The hosting restore is a no-op until the integrations plan.
    After the switch: post the payment to the ledger so it can be reversed (note 8), and refuse a payment on a PAID invoice with its own sentence.

82. **Send-receipt: only for a CLEARED payment; stub PDF; a bad CC is a 422 before any lookup.**
    Python: checks only that the payment's `invoice_id` is the invoice's (`app/api/v1/routers/invoices.py:652-654`), and a PENDING or FLAGGED payment carries its best candidate's invoice, so a receipt was mailed for money not yet received.
    Go: a payment that is missing, on another invoice or not CLEARED is 404 `payment not found on this invoice` (`payments.TestSendReceiptPendingPaymentIs404`, `TestSendReceiptPaymentNotOnInvoiceIs404`). `cc_email` is checked first, as Python's body validation ran before the handler, so a bad address is a 422 at `cc_email` even for an unknown invoice. Any sender error is 502 `failed to send receipt email`. Nothing is written. Subject, text and filename are Python's (`TestSendReceiptBodyExact`); `FormatAmount` is golden-tested against `_fmt_amount`, including its `-0` for a negative amount that rounds to zero (`TestFormatAmountMatchesPython`).
    Until the documents and email plans, the attachment is `StubRenderer`'s blank one-page PDF and the mail is `LogSender`, which logs CC and attachment names, never bodies or files.
    After the switch: wire the real renderer and the Resend sender; the switch-over must not happen before that.

83. **Reversal undoes the newest clearing, not an arbitrary entry (a change).**
    Python: `reverse_payment` finds the entry with `source_id = payment_id`, `source_type = 'PAYMENT'` and `reversed_by_entry_id IS NULL`, through `db.scalar` with no `ORDER BY` (`app/services/reconciliation.py:224-229`). A reversal entry keeps the original's `source_type` and `source_id` and has a NULL `reversed_by_entry_id` (`app/services/ledger.py:229-245`), and a FLAGGED payment can be approved again (`reconciliation.py:185-186` refuses only CLEARED). After approve, reverse, approve, reverse, two rows match, and the second reverse may "reverse" the first reversal, which re-posts the original.
    Go: `ledger.ActiveEntryFor` returns the newest `PAYMENT` entry for the payment that is neither reversed nor itself a reversal (no other entry's `reversed_by_entry_id` points at it), so the second reverse undoes the second clearing (`ledger.TestActiveEntryAfterReversalIsNone`, `TestActiveEntryAfterRepost`; `payments.TestApproveReverseTwiceUndoesLatestClearing`). Entries of one transaction share a posting time, so a tie between two live entries is ordered arbitrarily; the service never makes two.
    After the switch: none; Go's behaviour is the fix.

84. **Approve, reverse and the payment writes take row locks (a change).**
    Python: nothing locks. Manual approval reads the payment and invoice, then writes (`app/services/reconciliation.py:182-203`); reverse does the same (`:218-248`); intake reads candidates, then clears (`:129-161`). Two concurrent approvals of one PENDING payment each see it unfinished and each post a journal entry; two reversals of one payment race the same way (note 27 is the ledger half).
    Go: lock order is payment row, then invoice row, then the journal entry (the ledger locks it). Approve and reverse read the payment `FOR UPDATE` and then the invoice. The invoice lock everywhere is `FOR NO KEY UPDATE`, not `FOR UPDATE`: inserting a payment takes a `KEY SHARE` lock on its invoice through the foreign key, and `FOR UPDATE` conflicts with it, which deadlocked a transaction against itself (`payments.TestIncomingLockCoexistsWithKeyShare`). Two concurrent approvals post one entry and the second is `payment <id> already CLEARED` (`payments.TestApproveTwiceConcurrentPostsOnce`). The order is compatible with plan 5's (template, invoice row, number lock). record-payment and intake lock only the invoice (their payment is new): note 81 and note 89.
    After the switch: none.

85. **Manual approval refuses a DRAFT or VOID invoice; PAID, other currencies and overpayments are still accepted.**
    Python: `approve_manual_match` checks only that the payment is not CLEARED and the invoice exists (`app/services/reconciliation.py:182-190`); a DRAFT or VOID invoice takes a payment, though record-payment refuses both (`app/api/v1/routers/invoices.py:525-529`).
    Go (a change): 400 `cannot approve payment against <STATUS> invoice` for DRAFT and VOID (`payments.TestApproveDraftOrVoidInvoiceIs400`, `TestManualReviewUnknownPaymentIs400`); the transaction rolls back and the payment stays PENDING. Kept as Python: a PAID invoice is accepted (no test pins it); another currency is accepted, books the FX plug and sets the balance to 0 (`TestCrossCurrencyManualApprovalBooksFxLoss`, `...Gain`, `TestForeignCurrencyWithholdingCrossCurrencyApproval`); and an overpayment in the invoice's currency credits AR for the full amount while `balance_due` clamps at 0 (`reconciliation.py:100-103`; `ledger.py:149-157`), leaving a credit balance in AR (no test pins it). `adjustment_type` is stored with no effect on the posting (`reconciliation.py:195`; `payments.TestManualReviewClearsOverRest` stores `BANK_FEE`).
    After the switch: refuse a PAID invoice, and refuse or split an amount above the balance, so AR carries no stray credit.

86. **Intake: bad input is a 400 sentence, never a 500 (a change).**
    Python: `parse_generic_webhook` runs `Decimal(str(body["amount"]))`, `str(body["payer_name"])` and `date.fromisoformat(body["payment_date"])` without catching their errors (`app/services/intake/webhook.py:22-29`). A JSON number, `true` or `null` body fails the `in` test with `TypeError` (`:18`), and a list or string body answers the missing-fields 400; a non-numeric amount is `InvalidOperation`, a non-string date `TypeError`, a currency longer than three characters a database `DataError`, all 500s. A negative or zero amount is accepted, and `str(None)` stores the text `None` as the payer or reference. An open invoice with a null `issue_date` or `due_date` crashes the engine on `pay_date - None` (`app/services/matching_engine.py:129-132`; note 80).
    Go: each is a 400 with a stable `code` (`payments.TestWebhookBadBodiesAre400`, `TestParseWebhookInvalidBodies`):
    - a body that is not an object: `webhook payload must be a JSON object`;
    - an amount that is not a number, has more than four places or more than 15 whole digits: `invalid amount: <repr>`; zero or below: `amount must be greater than 0` (`TestParseWebhookAmountForms`);
    - a currency that is not three letters after upper-casing: `invalid currency: <repr>` (`TestParseWebhookLongCurrencyIs400`);
    - `null` or a non-string `payer_name`, `external_ref` or `payment_date`: `invalid <field>: <repr>` (`TestParseWebhookNullFieldsAre400`); a date that is not `YYYY-MM-DD`: `Invalid isoformat string: '<text>'` (`TestParseWebhookBadDateIs400`);
    - more than 255 characters in `payer_name`, `payer_reference` or `external_ref`: `<field> is too long (max 255)` (`TestParseWebhookTooLongTexts`);
    - beyond Go change 5: a NUL character in a text field is `invalid <field>`; a non-string `payer_reference` is `invalid payer_reference: <repr>` (`null` means none); a non-list `attachments` or a non-object entry is `invalid attachments: <repr>` (Python: 500); arrays and objects in these sentences print as JSON, not Python repr.
    - A null date on an open invoice scores date proximity 0 (`matching.TestNullDatesScoreZeroProximity`).
    The amount is read as text, not as a float (ruling R3): a JSON string goes through `platform.ParseDec4` as sent, a JSON number through its literal text. Python's `Decimal(str(v))` also takes a string with surrounding whitespace, `NaN` and `Infinity` (the last two are a later 500), and turns a long float into its shortest repr first; Go refuses the first three as `invalid amount` and keeps every digit of the others. Exponent forms follow `ParseDec4`. `payment_date` takes only `YYYY-MM-DD`: Python 3.11 `fromisoformat` also takes `YYYYMMDD` and ISO week dates. Go's regular expressions read `\s` and `\d` as ASCII (Python: Unicode, such as a no-break space), which matters only for the email parser's regexes.
    The email parser keeps Python's regexes (`app/services/intake/email_ocr.py:18-19`); an amount of zero or below is `amount must be greater than 0`, and one with more than 15 whole digits is `invalid amount` (`TestEmailBadBodies`, `TestParseEmailAmountRegex`). Only a string (or a non-zero number or `true`) in `from`, `payer_name`, `message_id`, `subject`, `text` or `html` counts; Python would `str()` or crash on other kinds.
    After the switch: none. Compare runs: the sentences for Python's 500 cases are Go-only.

87. **Intake checks run before the body is handled; invalid JSON and wrong content type differ.**
    Python (verified in the frozen container with FastAPI's `TestClient` on `app.main:app`): the webhook route reads the body in a dependency that checks the header first, so a missing `X-Signature` is 400 `missing X-Signature header` before anything is parsed (`app/api/deps.py:37-49`). The email route declares `body: dict` and a token dependency (`app/api/v1/routers/payments.py:42-47`). FastAPI parses the JSON before it runs dependencies, so invalid JSON is 422 (`JSON decode error` at `body`) whether the token is right, wrong or missing; a valid JSON array or an empty body with a wrong or missing token is 401 `invalid email webhook token`; valid JSON sent with a non-JSON `Content-Type` and the right token is 422 `Input should be a valid dictionary`.
    Go: `huma.Operation.Middlewares` run the signature or token check before Huma sees the body (`payments.TestIntakeChecksComeBeforeBodyHandling`): an empty body with a bad signature is 401, with none 400, with an empty `X-Signature` 401 (as Python); the email token is checked before the body is read, so invalid JSON with a wrong or missing token is **401** (Python: 422). The body is read with a 1 MB limit (413 over it). The content type is ignored: a body that parses is accepted (Python: 422 for a non-JSON type). Invalid webhook JSON is `invalid JSON: <Go's text>` (Python's is `invalid JSON: <json.JSONDecodeError text>`; an allowed difference for compare), and the same for an empty body (`invalid JSON: EOF`); trailing data after the object is refused, as in Python. Invalid email JSON or a non-object body is 422 with `JSON decode error` or `Input should be a valid dictionary` at `body`; FastAPI's wording and `loc` differ slightly (an allowed difference for compare).
    After the switch: none; the 401-before-422 order is the safer one for an unauthenticated caller.

88. **A clearing that the ledger or FX refuses holds the payment instead of losing it (a change).**
    Python: `process_incoming_payment` calls `_clear_against_invoice` with no handler (`app/services/reconciliation.py:152-156`), so a missing FX rate or a ledger refusal raises out of the route, nothing is stored, and the sender's webhook is answered with a 500. Manual approval and reverse raise `LedgerError` or `FxRateMissing` (RuntimeError subclasses) past the `except ValueError` of the routes (`app/api/v1/routers/recon.py:139-146, 157-162`): a 500.
    Go: on intake, a CLEARED result whose posting fails with `ledger.ErrLedger` or `fx.ErrRateMissing` is rolled back to a savepoint and stored as `PENDING_MANUAL_REVIEW`, with the error sentence appended to the reasons and a `HELD` log row; the payment, its withholding and the invoice are as before (`payments.TestIncomingClearWithoutRateIsHeld`). On manual-review and reverse the same errors are 400 with their sentence, and the transaction rolls back (`payments.TestApproveMissingRateIs400Sentence`, `TestManualReviewUnknownPaymentIs400`, `TestReverseOverRest`). A negative `wht_booked` on reversal stays a plain error (a 500), as Python's `RuntimeError` (`:241-245`).
    After the switch: none.

89. **Intake re-checks the invoice under its lock (a change).**
    Python: candidates are read without a lock and the chosen invoice is cleared later (`app/services/reconciliation.py:129-161`), so two payments for one invoice that arrive together (different `external_ref`) both match it and both clear: two ledger entries, AR relieved twice, balance forced to 0.
    Go: the clearing invoice is locked `FOR NO KEY UPDATE` and checked again: still OPEN, SENT or PARTIAL, and still an exact match against the current balance and withholding. If not, the payment is `PENDING_MANUAL_REVIEW` with the reason `invoice <id> changed before clearing` (a new sentence, Go only) and a `HELD` row, with the best guess attached (`payments.TestIncomingConcurrentSameInvoiceClearsOnce`, red 5 runs in 5 without the re-check).
    After the switch: none.

90. **The intake secrets have no usable default outside development and test (a change).**
    Python: `webhook_hmac_secret` and `email_webhook_token` default to `dev-webhook-secret-change-me` and `dev-email-token-change-me` (`app/config.py:41-42`), so a deployment that forgets to set them accepts anyone who has read the source.
    Go: `ParseConfig` keeps those defaults but refuses the default or an empty value unless `ENVIRONMENT` is `development` or `test`: an allow-list, so `production`, `staging`, `prod` and any typo are refused (ruling R8; `platform.TestProductionRefusesDefaultIntakeSecrets`, `TestIntakeSecretsDefaultToPythonDevValues`). The default `ENVIRONMENT` is `production`. The check runs for every subcommand that loads the config (`api healthcheck`, `set-password`, `issue-session` too), so each needs both secrets in a deployed environment (the Coolify compose sets both). Staging environments must set their own.
    After the switch: confirm both variables are set in Coolify before the first Go start.

91. **Decimals print at their stored scale on intake responses (a change; an allowed difference for compare).**
    Python: the session keeps its objects after commit (`app/db/session.py:11`, `expire_on_commit=False`) and the intake routes do not refresh (`app/api/v1/routers/payments.py:34-39`), so the response shows `amount` as parsed (`"1500000"`, `"100.5"`), `confidence_score` at the engine's four places (`"97.5000"`, `"0"` with no candidates) and an unscaled `wht_amount`; GET and the lists show four places and two.
    Go: the row is read back from the database after the write, so every route prints `amount` and `wht_amount` at four places, `confidence_score` at two places or `null` (`payments.TestPaymentOutFrom`). Compare runs will show the scale difference on the two intake responses only; it is an allowed difference for compare.
    After the switch: none.

92. **Lists and logs: tie-breaks, `limit`, whole-day dates and `Z` timestamps.**
    Python: all rows of one request share the transaction's `CURRENT_TIMESTAMP`, and the audit log, awaiting-review list and payment log order by it alone (`app/api/v1/routers/recon.py:48, 88, 114`); `limit` has `le=500` and no lower bound, so a negative one is a Postgres error (500) and `0` gives `[]` (`:26`); `payer` goes into `ILIKE` unescaped (`:54`), so `%` and `_` act as wildcards; `to_date` compares with `time.max` (`:56-58`).
    Go (changes): the last key is the primary key, descending where the list is descending: audit log `created_at DESC, log_id DESC`, awaiting-review `created_at DESC, payment_id DESC`, payment log `created_at ASC, log_id ASC`, invoice payments `payment_date DESC, created_at DESC, payment_id DESC` (`payments.TestAuditLogFiltersOrderLimit`, `TestAwaitingReviewOnlyPending`, `TestListPaymentsTieBreakByID`); `limit` is 1 to 500 and 422 outside it, default 200 (`TestAuditLogLimitBounds`); `payer` escapes `%`, `_` and `\` (`TestAuditLogPayerEscapesWildcards`); an empty `action` or `payer` is no filter, as Python's truthiness; `from_date` and `to_date` cover their whole days; the static paths `audit-log` and `awaiting-review` are not read as a payment id (`TestStaticPathsNotSwallowed`).
    Timestamps: Python printed `created_at` as a naive `isoformat` (no offset) on `PaymentOut` and on the audit-log and payment-log rows; Go prints RFC 3339 UTC with `Z` (microseconds only when non-zero). The frontend only displays them. An allowed difference for compare, as note 76.
    After the switch: none.

93. **Reverse `reason` is at most 490 characters (a change); `adjustment_type` rules are Python's.**
    Python: `reason` has no limit (`app/schemas/payment.py:36-37`) and becomes part of the memo `Reversal: <reason>`, a `varchar(500)` column, so a long reason is a database error (500) after the work is done. `adjustment_type` is any string, defaulting to `NONE`; an unknown one is 400 `unknown adjustment_type 'X'` (`app/api/v1/routers/recon.py:136-138`), and `null` is a 422 because the field is not optional.
    Go: `reason` over 490 characters is 422 at `reason` (ruling R7; `payments.TestReverseReasonBound`), leaving 10 for the prefix; `adjustment_type` is as Python's: 422 for `null`, omitted means `NONE`, an unknown value is 400 `unknown adjustment_type <repr>` (`TestManualReviewUnknownAdjustmentIs400`).
    After the switch: none.

94. **Kept: what a reversal does not undo, and what a held payment carries.**
    Python: reversal sets the invoice to OPEN whatever it was and does not undo the hosting restore (`app/services/reconciliation.py:246-248`; `app/services/hosting.py:181-193`; the cross-currency figure is note 7). A held payment keeps the best candidate's `invoice_id` and `customer_id` (`:132-136`). A same-currency auto-clear forces `balance_due` to 0 after `_clear_against_invoice` already computed it (`:157-158`), harmless within the epsilon. A payment's engine reasons are stored only on the log row.
    Go: the same, on purpose: OPEN after reversal (`payments.TestReversalCreatesBalancingEntryAndWiresLink`); held payments carry the best guess (`TestHeldPaymentLeavesInvoiceAndLedger`, `TestAmountMismatchHoldsAndPostsNoJournal`); the forced 0 (`TestClearedPaymentPostsExactlyOneBalancedEntry`). The hosting restore is a no-op (`NoHosting`) until the integrations plan (`TestHostingRestorerCalledOnPaid` pins the call), so the switch-over must not happen before that plan lands.
    After the switch: remember the invoice's earlier status on the payment and restore it; give a held payment no invoice until a person approves it (the receipt rule of note 82 already stops the harm).

95. **Kept: email intake details.**
    Python: `external_ref` is `""` when the email has no `message_id` and no reference match, so a second such email is 409 `duplicate email payment` (`app/services/intake/email_ocr.py:64`; `app/api/v1/routers/payments.py:53-58`); the amount regex is `(?i)`, so the currency letters may be any three letters, such as `abc` (`:18, :72`); the reference regex matches `id` inside words, such as `paid` (`:19`); the default `payment_date` is `date.today()` in the server's time zone (`:62`); an attachment with content raises `NotImplementedError`, a 400 (note 2).
    Go: all kept (`payments.TestEmailEmptyRefSecondIs409`, `TestParseEmailAmountRegex`, `TestParseEmailRefRegex`, `TestParseEmailDefaults`, `TestParseEmailAttachmentIs400`); the default date is the UTC date of `platform.Clock`, not the server's local one (note 5).
    After the switch: reject an email with no reference instead of storing `""`; settle the time zone with note 5.

96. **Python printers: repr now escapes like Python in two error texts.**
    Python: the FX sync error `fx provider returned invalid date for <src>-><target>: <value!r>` (`app/services/fx_provider.py:83`) and the ledger refusal for a payment that is not CLEARED, `with status {payment.status!r}` (`app/services/ledger.py:118`), print Python's `repr`, which escapes a quote or backslash and, for a string, any non-printable non-ASCII character.
    Go: `platform.PyJSONRepr` and `platform.PyRepr` do the same now (before plan 7, the fx text added quotes without escaping and the ledger text did not escape non-printable non-ASCII). No input of a route reaches either difference today; `platform.TestPrintersMatchTheCopiesTheyReplaced` pins the printers.
    After the switch: none.
