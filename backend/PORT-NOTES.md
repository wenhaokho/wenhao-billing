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
   After the switch: if the second-best score equals the best (or both are exact matches), return PENDING_MANUAL_REVIEW with a reason such as "ambiguous: N invoices tie". This changes Python's behaviour, so ship it as a deliberate fix with its own test.

7. **Payment reversal restores face amount plus withholding, which differs after a cross-currency settlement.**
   Python: `reverse_payment` does `balance_due + payment.amount + payment.wht_amount` (`app/services/reconciliation.py:239`). Clearing a cross-currency payment sets the invoice's `balance_due` to 0 (`:107`) whatever the payment amount was.
   Seen: for a same-currency settlement the reversal puts back exactly what was taken. After a cross-currency settlement it adds the payment-currency amount to an invoice-currency balance, so the restored balance can be wrong (too high or too low), while the reversing journal entry restores the ledger exactly.
   After the switch: store the balance relieved on the payment (or on the log row) and restore that figure.

8. **Manual invoice and bill record-payment create no journal entries.**
   Python: `POST /api/v1/invoices/{id}/record-payment` inserts a `CLEARED` Payment and lowers `balance_due` and `wht_booked`, but never calls the ledger (`app/api/v1/routers/invoices.py:510-594`; the only writes are the Payment, the invoice and a ReconciliationLog). `POST /api/v1/bills/{id}/record-payment` only lowers `balance_due` and sets PARTIAL or PAID (`app/api/v1/routers/bills.py:83-111`).
   Seen: cash received or paid by hand never reaches the double-entry ledger, so reports built on journal lines (trial balance, balance sheet) do not move when an invoice is marked paid this way. A manual invoice payment cannot be reversed either: `reverse_payment` fails with "no active journal entry to reverse" (`app/services/reconciliation.py:231`).
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
    Go (a change): the key must be `YYYY-MM-DD` (400 `cycle_key must be an ISO date (YYYY-MM-DD), got '<key>'`, `invoices.TestTriggerRejectsCompactCycleKey`); the trigger takes `pg_advisory_xact_lock` on the template id before it reads the template, so the route and the scanner never both create it (`invoices.TestTriggerConcurrentWithScanOneChild`), and a template `PUT` or `PATCH`, which take the same lock first, is seen whole: a child never mixes the new lines with the old discount or withholding (`invoices.TestTriggerWaitsForATemplatePut`, `invoices.TestTriggerWaitsForATemplatePatch`). The lock order is the template lock, then an invoice row `FOR UPDATE`, then the number lock (note 34); the lookup takes the oldest match, so a duplicate already in the data is returned deterministically. A replay returns the first child unchanged (`invoices.TestTriggerReplayReturnsSameInvoice`).
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
    Go: kept (`invoices.TestPatchIssueDateKeepsExistingDueDate`).
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
