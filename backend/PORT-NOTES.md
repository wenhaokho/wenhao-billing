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

10. **`null` for a NOT NULL column on create or PATCH.**
   Python: pydantic accepts `null` for fields such as `code`, `name`, `active` on PATCH, and the row update fails at the database. `PATCH /api/v1/accounting/chart-of-accounts/{id}` catches the IntegrityError and answers 409 `account code already exists` (`app/api/v1/routers/accounting.py:100-104`); every other router lets it become a 500.
   Seen: a client that sends `{"code": null}` gets a misleading 409 on accounts and an opaque 500 elsewhere.
   Go: 422 `Request validation failed` with one error per null field, location `<field>` and message `value cannot be null` (`platform.NotNull`). The row is untouched.

11. **Duplicate item SKU and unknown item account.**
   Python: `create_item` and `update_item` flush without catching the IntegrityError (`app/services/items.py:50`), so a repeated `sku` or a `revenue_account_id` / `expense_account_id` that does not exist is a 500.
   Seen: the item form shows a generic server error instead of the reason.
   Go: 409 `sku_exists` with detail `sku already exists`; 422 `account_not_found` with detail `account not found`.

12. **`GET /api/v1/business-profile` on a missing row.**
   Python: see note 4, the GET inserts the row and never commits.
   Go: the GET answers the same defaults (all fields null, `updated_at` = now) and writes nothing. This closes note 4 for Go.

13. **Admin password reset signs the target out everywhere.**
   Python: `admin_reset_password` replaces the hash and clears the reset token, but leaves existing sessions alive (`app/api/v1/routers/users.py:79-82`).
   Seen: a user whose password an admin reset keeps working sessions on every device until they expire.
   Go: the reset bumps the user's session epoch, so every session of that user stops working. This is a deliberate spec change, listed under "Best-practice changes".

14. **Email address rules.**
   Python: `EmailStr` (email-validator) for vendor, customer and user emails and for every CC entry (`app/schemas/vendor.py:9`, `app/schemas/customer.py:17`, `app/schemas/user.py:16`, `app/schemas/email.py:8`).
   Seen: both sides trim, lower-case the domain, and keep the local part's case.
   Go: `platform.NormalizeEmail` keeps the email-validator rules listed in Task 1 of the plan (exactly one `@`; local part 1-64 bytes without whitespace; domain with at least two labels of letters, digits and inner hyphens, each at most 63; no special-use last label). Rarer forms may differ: Go may accept quoted local parts, leading, trailing or double dots in the local part, an all-numeric TLD and domains over 253 characters, which email-validator refuses. An allowed difference for `compare`.

15. **Boolean query filters accept only `true` / `false`.**
   Python: `active: bool | None = None` on the list routes (`app/api/v1/routers/accounting.py:47`, `vendors.py:19`, `customers.py:42`, `items.py:19`) is parsed by pydantic, which also accepts `yes/no/on/off/y/n/t/f/1/0`.
   Seen: `?active=yes` filters in Python.
   Go: `strconv.ParseBool` semantics through `platform.OptionalParam`: `1`, `0`, `t`, `f`, `true`, `false` in any case of true/false; `yes`, `no`, `on`, `off` answer 422. The frontend sends `true` and `false`.

16. **Over-long strings are 422 instead of a database 500.**
   Python: the vendor, customer, business-profile and user schemas have no `max_length` on columns such as `contact_email`, `contact_name`, `tax_id`, `name`, `contact_phone`, `npwp` (`app/schemas/vendor.py:9-13`, `customer.py:17`, `business_profile.py:9-18`, `user.py:19-21`). A value longer than the varchar column fails in Postgres and surfaces as 500.
   Go: each request field carries the column's `maxLength`, so the same input is a 422 at the field.

17. **`ItemUpdate` limits are enforced on PATCH.**
   Python: `ItemUpdate` has no length limit on `sku` and no `min_length` on `name` (`app/schemas/item.py:82-102`), so an over-long `sku` is a 500 and an empty `name` is stored.
   Go: `sku` at most 50 and `name` at least 1 apply on PATCH as on create.

18. **A whitespace-only `hosting_domain` or target field is accepted and stored as `""`.**
   Python: the hosting check and the target `min_length` test the raw value before it is stripped (`app/schemas/item.py:65-78`), so `"  "` passes and the stripped value is saved.
   Seen: a hosting item can be created with an empty domain.
   Go: the same, kept on purpose to match Python. Odd; decide when hosting is rebuilt.

19. **Item price errors list only the first refused price, and a parse refusal hides other errors.**
   Python: pydantic reports every failing field, so a body with both prices refused lists both (`app/schemas/item.py:47-50, 88-91`), and a bad decimal, date or time is listed with the other errors of the body.
   Go: `checkPrices` returns the first refused price (unit price first) as a 422 whose detail is pydantic's sentence. A value that fails to parse (bad decimal, date, time) is reported alone, without the other 422 errors of the same body.

20. **Invalid email `detail` and the CC list error location.**
   Python: a bad single email is a pydantic error list (`value is not a valid email address: ...` text from email-validator) located at the field; a bad CC entry is located at the list, `billing_cc_emails` (`app/schemas/email.py:19-28`).
   Go: `detail` is `value is not a valid email address` with the field as location. A CC entry error carries the element index, `billing_cc_emails.1`, and the message `invalid email address: <addr>`. The frontend reads `detail`, not locations.

21. **`POST /users` with `role: ""` or `null`.**
   Python: `role: str = Field(default="admin")` stores `""` as given and refuses `null` (`app/schemas/user.py:20`). `role` is any string in both, with no check against known roles.
   Go: `role` is any string of at most 20 characters; `""` and `null` store `admin`.

22. **An admin resetting their own password is signed out too.**
   Python: no session is touched (note 13), so the caller stays signed in.
   Go: the same epoch bump as note 13 applies to the caller's own account when the target is the caller, so the next request returns 401 and they sign in again.
