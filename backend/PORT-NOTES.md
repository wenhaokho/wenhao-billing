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
