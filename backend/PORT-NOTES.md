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
