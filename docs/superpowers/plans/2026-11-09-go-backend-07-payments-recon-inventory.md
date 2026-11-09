# Plan 7 contract inventory: payments, intake, matching, reconciliation (frozen Python -> Go)

Read-only inventory. All paths are relative to `backend-python/` unless prefixed. Abbreviations:

| Abbr | File |
|---|---|
| RI | app/api/v1/routers/invoices.py (record-payment, payments list, send-receipt) |
| RPAY | app/api/v1/routers/payments.py (intake) |
| RR | app/api/v1/routers/recon.py |
| DEPS | app/api/deps.py |
| CFG | app/config.py |
| SM | app/services/matching_engine.py |
| SR | app/services/reconciliation.py |
| SW | app/services/intake/webhook.py |
| SE | app/services/intake/email_ocr.py |
| SL | app/services/ledger.py (Go: backend/internal/ledger) |
| SFX | app/services/fx.py (Go: backend/internal/fx) |
| W | app/services/withholding.py (Go: backend/internal/withholding) |
| H | app/services/hosting.py (not ported) |
| RCP | app/services/receipt_pdf.py |
| MPAY / MLOG / MJ | app/models/payment.py / recon_log.py / journal.py |
| CPAY | app/schemas/payment.py |
| CEM | app/schemas/email.py |
| DB | app/db/session.py |
| MCPW / MCPR | app/mcp/tools/write_recon.py / read_recon.py |
| PN | backend/PORT-NOTES.md |
| DDL | backend/migrations/00001_baseline.sql |
| FE | frontend/src/views/ |

Global facts:
- Routers: `payments.router` (RPAY:15, prefix `/payments`) is mounted before `recon.router` (RR:17, also prefix `/payments`) under `/api/v1` (app/main.py:92-93). Static paths `audit-log` and `awaiting-review` are declared before `/{payment_id}` (RR:20, :79, :93); Go must register them so `{payment_id}` does not swallow them.
- Auth: RI and RR routes are admin-only via `Depends(current_admin)` (DEPS:18-34: 401 `"not authenticated"`, 401 `"session expired"`, 403 `"admin required"`). The two intake routes take **no session**: webhook uses HMAC (DEPS:37-49), email uses a shared token (DEPS:52-59).
- Validation errors are FastAPI's default 422.
- Session uses `expire_on_commit=False` (DB:11). After `db.commit()` the ORM object keeps its in-memory Python values; only server-default columns not yet loaded (`created_at`) are fetched on access. This decides the scale of decimals in intake responses (see §0 PaymentOut notes).
- Postgres `CURRENT_TIMESTAMP` is the **transaction start time**: every row written in one request (payment, log row, journal entry) gets the same `created_at`/`posted_at`. All `ORDER BY created_at` in scope have no tie-break.
- Decimal arithmetic: Python default context (28 significant digits, ROUND_HALF_EVEN). `quantize` calls in scope use that default: matching score (SM:153-155) and FX base amounts (SFX:72) are **half-even** to 4 places. `withholding.round_ccy` (HALF_UP, W:27-30) is not called on any payment path.
- No `business_id` column on `payments` or `reconciliation_log` (DDL:379-394, :464-470).
- No route in scope takes a row lock. Every read-then-write on `invoices.balance_due`/`wht_booked`/`status` and `payments.status` is racy (§5).

---

## 0. Storage and response shapes

### payments (DDL:379-394)
payment_id uuid NOT NULL (PK `payments_pkey` DDL:659-660; Python uuid4 MPAY:18-20); invoice_id uuid NULL (FK invoices, no ON DELETE, DDL:1033-1034); customer_id uuid NULL (FK customers DDL:1026-1027); amount numeric(19,4) NOT NULL; currency varchar(3) NOT NULL; payer_name varchar(255) NOT NULL; payer_reference varchar(255); payment_date date NOT NULL; intake_source varchar(50) NOT NULL; external_ref varchar(255) NULL; status varchar(30) NOT NULL (no default); adjustment_type varchar(30) DEFAULT 'NONE' NOT NULL; confidence_score **numeric(5,2)** NULL; created_at timestamp (no tz) DEFAULT CURRENT_TIMESTAMP, **nullable**; wht_amount numeric(19,4) DEFAULT 0 NOT NULL (column is last).
- UNIQUE `uq_payments_source_ref (intake_source, external_ref)` (DDL:729-730; MPAY:15). This is the only idempotency mechanism.
- Index `ix_payments_status` (DDL:841). No index on invoice_id. No CHECK.
- Values in use: status `CLEARED`, `PENDING_MANUAL_REVIEW`, `FLAGGED` (SM:27-30; FLAGGED only by reversal SR:234). adjustment_type `NONE`, `PARTIAL_PAY`, `CREDIT_ON_ACCOUNT`, `BANK_FEE` (SM:33-37). intake_source `WEBHOOK` (SW:28), `EMAIL_PARSER` (SE:63), `MANUAL` (RI:563).

### reconciliation_log (DDL:464-470)
log_id uuid NOT NULL (PK DDL:701-702, Python uuid4 MLOG:12-14); payment_id uuid NOT NULL (FK payments DDL:1089-1090); action varchar(30) NOT NULL; reasons **text[]** DEFAULT ARRAY[]::text[] NOT NULL; actor_user_id uuid NULL (FK users DDL:1082-1083); created_at timestamp DEFAULT CURRENT_TIMESTAMP, nullable. Index `ix_reconciliation_log_payment_id` (DDL:883).
- Actions written: `CLEARED` (SR:113 auto, RI:587 manual), `HELD` (SR:113), `OVERRIDDEN` (SR:208), `REVERSED` (SR:253).

### journal_entries / journal_lines (DDL:296-320)
As in the ledger plan. `source_type` varchar(20) = `"PAYMENT"`, `source_id` = payment_id (SL:210-213); index `ix_journal_entries_source (source_type, source_id)` (DDL:829). `reversed_by_entry_id` FK self (DDL:998-999). Lines CHECK one-sided (DDL:319). Memo varchar(500).

### PaymentOut (CPAY:11-28), fields in order
1. payment_id UUID
2. invoice_id UUID|null
3. customer_id UUID|null
4. amount Decimal
5. wht_amount Decimal
6. currency str
7. payer_name str
8. payer_reference str|null
9. payment_date date
10. intake_source str
11. external_ref str|null
12. status str
13. adjustment_type str
14. confidence_score Decimal|null
15. created_at datetime (naive, DB `CURRENT_TIMESTAMP`)

Decimal scale per route (follows from DB:11):
- record-payment: `db.refresh(payment)` (RI:593) -> DB scale: amount/wht_amount `"400.0000"`, confidence_score `null`.
- GET payment, awaiting-review, invoice payments list: loaded from DB -> amount `"x.0000"`, confidence `"97.50"` (2 places, Postgres rounds half away from zero on insert).
- intake webhook/email: **no refresh**. amount is the parsed Decimal as given (`"1500000"`, `"100.5"`, email `"1500000.00"`); confidence_score is the engine's 4-place value (`"97.5000"`, `"0"` when no candidates, SM:173 `Decimal("0")`); wht_amount is the ORM default `Decimal(0)` -> `"0"` when not cleared, or `invoice.wht_amount - invoice.wht_booked` (`"0.0000"`/`"20000.0000"`) when cleared (SR:94-96). Go `platform.Dec4` always prints 4 places -> allowed difference (like PN 50); confidence should print with 2 places to match the stored value (choose and note).
- manual-review / reverse: `db.get` then mutate, no refresh: amount DB scale; wht_amount as computed (manual-review) or DB (reverse); confidence DB 2 places.

### Audit-log row (RR:60-76), dict, keys in order
log_id str, payment_id str, action str, reasons list[str] (`r.reasons or []`), actor_user_id str|null, actor_email str|null, created_at datetime, payer_name str|null, amount **str** of DB numeric (`"1000.0000"`) or null, currency str|null, invoice_id str|null, intake_source str|null. Null payment fields only possible if the outer join finds no payment (FK makes that impossible).

### Payment-log row (RR:116-125), keys in order
log_id UUID, action str, reasons list[str], actor_user_id UUID|null, created_at datetime.

### Send-receipt response (RI:696)
`{"sent_to": "<to_email>"}`, status 200 (no status_code set).

---

## 1. Routes (11)

### POST /api/v1/invoices/{invoice_id}/record-payment -> 201 PaymentOut (RI:510-594)
Auth: admin session. Request `InvoiceRecordPaymentRequest` (CPAY:59-66, base CPAY:50-56):
| field | type | default | constraints |
|---|---|---|---|
| amount | Decimal | **required** | `ge=0, max_digits=19, decimal_places=4` (CPAY:65 overrides base `gt=0`) |
| wht_amount | Decimal | `0` | `ge=0, max_digits=19, decimal_places=4` (CPAY:66) |
| payment_date | date | **required** | ISO date |
| payer_name | str\|null | None | no length check (varchar 255 -> DataError 500 if longer) |
| payer_reference | str\|null | None | same (255) |
| notes | str\|null | None | only stored as a log reason, truncated to 200 chars |
Pydantic messages: `"Input should be greater than or equal to 0"`, `"Decimal input should have no more than 4 decimal places"`, `"Decimal input should have no more than 19 digits in total"`.
FE sends amount and wht_amount as JSON **numbers**, payment_date string, payer_name/payer_reference/notes string|null (FE/InvoiceFormView.vue:411-424, FE/InvoicesView.vue:166-179; the `receipt` key is stripped before posting).

Errors in order:
1. 404 `"invoice not found"` (RI:524).
2. 400 `"cannot record payment on {status} invoice"` for DRAFT or VOID (RI:525-529). PAID is not refused here (falls to 4).
3. 400 `"amount and withheld cannot both be zero"` when amount + wht_amount == 0 (RI:533-537).
4. 400 when `settled > balance_due` (RI:538-546): `"amount + withheld {settled} exceeds balance due {balance_due}"` if wht_amount > 0, else `"amount {settled} exceeds balance due {balance_due}"`. Values are Python `str(Decimal)`: settled keeps the request's scale (`150`, `150.5`), balance_due DB scale (`100.0000`). Test: test_exceeding_error_mentions_withheld.
5. 400 `"withheld {wht_amount} exceeds outstanding withholding {wht_outstanding}"` when wht_amount > wht_amount_col - wht_booked (RI:547-552). wht_outstanding prints with DB scale.

Side effects (one transaction, commit at RI:592):
1. INSERT payments: invoice_id, customer_id from invoice, amount, wht_amount, currency = **invoice.currency** (no currency in request), payer_name = `payload.payer_name or "(manual entry)"` (empty string also becomes the default), payer_reference, payment_date, intake_source `"MANUAL"`, external_ref `f"manual-{uuid4().hex[:12]}"` (12 lowercase hex), status `"CLEARED"`, adjustment_type `"NONE"`, confidence_score NULL (RI:554-568).
2. invoice.balance_due -= settled; invoice.wht_booked += wht_amount (RI:570-571).
3. If balance_due <= 0: balance_due = 0, status `"PAID"`, `ensure_subscription_restored(db, invoice)` (H:181-193: stamps `hosting_last_paid_at = utcnow` on hosting items of the invoice and restores suspended ones; no-op when balance > 0). Else status `"PARTIAL"` (RI:572-577).
4. flush (RI:579), INSERT reconciliation_log action `"CLEARED"`, reasons `["manual-entry"]` + `notes[:200]` if notes truthy, actor = current user (RI:581-591).
5. **No ledger posting** (PN 8). No receipt email (frontend calls send-receipt separately).

### GET /api/v1/invoices/{invoice_id}/payments -> 200 list[PaymentOut] (RI:597-613)
- 404 `"invoice not found"` (RI:604-605).
- `WHERE invoice_id = :id AND status = 'CLEARED' ORDER BY payment_date DESC, created_at DESC` (RI:606-612). No tie-break by id (two manual payments in one day from different requests differ by created_at; same request impossible).
- FE reads it as `ReceiptPayment[]` (FE/InvoiceFormView.vue:517).

### POST /api/v1/invoices/{invoice_id}/payments/{payment_id}/send-receipt -> 200 (RI:636-696)
Request `SendReceiptRequest` (CPAY:69-76): `to_email` str|null (no email validation), `cc_email` `CcEmails` (CEM:31-38: split on `[,;\s]+`, each validated as EmailStr, case-insensitive de-dup keeping first spelling, joined `", "`, blank -> null; invalid -> 422 with `"invalid email address: {addr}"`).
Errors in order:
1. 404 `"invoice not found"` (RI:650-651).
2. 404 `"payment not found on this invoice"` when payment missing or `payment.invoice_id != invoice_id` (RI:652-654). **Payment status not checked**: a PENDING_MANUAL_REVIEW or FLAGGED payment carries the invoice_id of its best candidate (SR:135-136) and gets a receipt.
3. 400 `"no recipient: provide to_email or set a contact_email on the customer"` (RI:656-662). `to_email` empty string falls back to the customer.
4. 502 `"failed to send receipt email"` on any non-HTTPException from send_email (RI:684-695). An HTTPException from send_email passes through unchanged.

What it does (RI:664-696):
- loads BusinessProfile id 1; `render_receipt_pdf(payment, invoice, customer, business)` -> bytes (documents plan).
- filename `receipt-RCT-{first 8 hex of payment_id, upper}.pdf` (RCP:31-33 `receipt_reference`).
- subject `"Payment receipt for invoice {invoice_number}"` or `"Payment receipt"` (RI:667-671).
- body_text (RI:672-683): `"Hi {customer.name or 'there'},\n\nWe have received your payment of {_fmt_amount(amount, currency)} against {'invoice ' + number or 'your invoice'} on {payment_date ISO}.\n{balance_note(invoice)}\n\nA PDF receipt is attached for your records.\n\nThank you."`; `balance_note` (RCP:36-41): `"This invoice is now paid in full."` when balance_due <= 0 else `"Remaining balance: {_fmt_amount(balance, currency)}."`. `_fmt_amount` is the invoice-PDF formatter (0 places IDR, 2 otherwise; app/services/invoice_pdf.py).
- `send_email(to_email, cc_email, subject, body_text, attachments=[(filename, pdf, "application/pdf")])`. No DB writes, no commit, nothing logged in reconciliation_log.
Split for Go: routing, the three lookups/errors, recipient fallback, CC normalisation, subject, body text, filename and `RCT-` reference all belong here; the PDF bytes and the Resend call belong to the documents plan. This plan can define `ReceiptMailer` / `ReceiptRenderer` interfaces, ship a logging mailer (the spec already logs mail when `RESEND_API_KEY` is empty, spec "Email") and a stub renderer, and let the documents plan swap them. The happy-path test asserts the attachment starts with `%PDF-` (tests/integration/test_send_receipt.py:31-53), so the stub must emit a minimal PDF or that one assertion is deferred.

### POST /api/v1/payments/intake/webhook -> 201 PaymentOut (RPAY:18-39)
Auth: `X-Signature` header (DEPS:37-49). Missing -> 400 `"missing X-Signature header"` (DEPS:42-43, checked before the body is read). Expected = lowercase hex `HMAC-SHA256(secret=webhook_hmac_secret UTF-8, raw body)`; compared with `hmac.compare_digest(expected, x_signature.strip())`; mismatch -> 401 `"invalid webhook signature"` (DEPS:47-48). Upper-case hex fails. Secret env `WEBHOOK_HMAC_SECRET`, default `"dev-webhook-secret-change-me"` (CFG:41).
Body: raw bytes, `json.loads` -> 400 `f"invalid JSON: {e}"` (Python's JSONDecodeError text, e.g. `"invalid JSON: Expecting value: line 1 column 1 (char 0)"`) (RPAY:23-26). The Pydantic `WebhookPayload` (CPAY:40-47) is **not used**.
`parse_generic_webhook` (SW:16-30):
- required keys `amount, currency, payer_name, payment_date, external_ref`; missing -> 400 `f"webhook payload missing fields: {missing}"`, a Python list repr in that order, e.g. `"webhook payload missing fields: ['amount', 'external_ref']"` (SW:17-20, RPAY:30-31).
- amount `Decimal(str(v))` (no sign or scale check; `"abc"` raises InvalidOperation -> **500**); currency `str(v).upper()` (no length check; >3 chars -> DataError **500**); payer_name `str(v)` (`null` -> `"None"`); payer_reference `body.get` raw (non-string -> DB error); payment_date `date.fromisoformat` (bad string -> ValueError -> 400 with Python's text `"Invalid isoformat string: 'x'"`; non-string -> TypeError **500**); external_ref `str(v)` (`null` -> `"None"`); intake_source `"WEBHOOK"`.
- A JSON array body passes the key check only if it contains those strings, then `.get` -> AttributeError 500; a scalar body -> TypeError 500.
Then `process_incoming_payment` (§2.3) and commit (RPAY:33-35). IntegrityError (duplicate `(WEBHOOK, external_ref)`) -> rollback, 409 `"duplicate (intake_source, external_ref)"` (RPAY:36-38). LedgerError / FxRateMissing on the CLEARED path -> 500.

### POST /api/v1/payments/intake/email -> 201 PaymentOut (RPAY:42-59)
Auth: `X-Email-Token` header compared constant-time to `email_webhook_token` (env `EMAIL_WEBHOOK_TOKEN`, default `"dev-email-token-change-me"`, CFG:42); missing or wrong -> 401 `"invalid email webhook token"` (DEPS:52-59). Dependency errors come before body validation.
Body: `dict` (any JSON object; non-object -> 422). `EmailWebhookPayload` (CPAY:79-89) is **not used**.
`parse_inbound_email` (SE:26-65):
- corpus = `" ".join` of truthy `subject`, `text`, `html` (SE:39-41).
- any attachment with non-null `content` calls the OCR stub, which raises NotImplementedError `"Attach an OCR backend (tesseract/textract/etc) before calling"` -> 400 with that text (SE:22-23, :42-50, RPAY:50-51; PN 2).
- amount: `AMOUNT_RE = (?i)(?:amount|total|paid)\s*[:=]?\s*([A-Z]{3})?\s*([\d,]+\.\d{2})` (SE:18), first match; none -> 400 `"could not locate amount in email body or attachments"` (SE:69-71). Currency group (case-insensitive because of `(?i)`) upper-cased, default `"IDR"` (SE:72); amount = group 2 with commas removed (SE:73). Note `(?i)` makes `[A-Z]{3}` match any three letters, so `"Total paid 100.00"` is fine but `"Amount: the 100.00"` gives currency `"THE"`.
- ref: `REF_RE = (?i)(?:ref|reference|txn|id)\s*[:#]?\s*([A-Za-z0-9\-]{4,})` (SE:19), first match -> payer_reference (SE:59). No word boundaries: `id` matches inside words, e.g. `"said thanks"` gives ref `"thanks"`.
- payer_name = `str(body["from"] or body["payer_name"] or "UNKNOWN")` (SE:58).
- payment_date = `date.fromisoformat(body["payment_date"])` if truthy else **`date.today()`** (server local date) (SE:60-62); bad -> ValueError 400.
- external_ref = `str(message_id or ref or "")` (SE:64): empty string when neither, so the second such email is a 409.
- intake_source `"EMAIL_PARSER"`.
Then process + commit; IntegrityError -> 409 `"duplicate email payment"` (RPAY:56-58).

### GET /api/v1/payments/audit-log -> 200 list[row] (RR:20-76)
Query: `action` str|null (exact match), `payer` str|null (`payer_name ILIKE '%{payer}%'`, `%`/`_` not escaped), `from_date`/`to_date` date|null (422 on bad date), `limit` int default 200, `le=500` (422 `"Input should be less than or equal to 500"`; no lower bound: 0 -> `[]`, negative -> Postgres error **500**).
SQL: reconciliation_log LEFT JOIN payments LEFT JOIN users; `created_at >= from_date 00:00:00`, `created_at <= to_date 23:59:59.999999` (RR:55-58); `ORDER BY created_at DESC LIMIT n` (RR:48-49), no tie-break. FE: FE/AuditLogView.vue:15-28, :56-58 (amount read as string).

### GET /api/v1/payments/awaiting-review -> 200 list[PaymentOut] (RR:79-90)
`WHERE status='PENDING_MANUAL_REVIEW' ORDER BY created_at DESC`, no limit, no tie-break. FE: FE/ManualReviewView.vue:44-45 (amount, confidence_score read as strings, type at :2-15).

### GET /api/v1/payments/{payment_id} -> 200 PaymentOut (RR:93-102)
404 `"payment not found"` (RR:101). Bad UUID -> 422. Not used by the frontend.

### GET /api/v1/payments/{payment_id}/log -> 200 list[row] (RR:105-125)
`WHERE payment_id = :id ORDER BY created_at ASC`. Unknown id -> `[]` (PN 3). FE: FE/ManualReviewView.vue:51.

### POST /api/v1/payments/{payment_id}/manual-review -> 200 PaymentOut (RR:128-147)
Request `ManualApproveRequest` (CPAY:31-33): `invoice_id` UUID required; `adjustment_type` str default `"NONE"`. FE sends `{invoice_id, adjustment_type: "NONE"}` (FE/ManualReviewView.vue:57-60) and shows `response.data.detail` on error (:170).
Errors: unknown adjustment_type -> 400 `f"unknown adjustment_type {value!r}"`, e.g. `"unknown adjustment_type 'FOO'"` (RR:135-138); service ValueErrors -> rollback, 400 with the text (RR:144-146): `"payment {id} not found"` (SR:184, **400 not 404**), `"payment {id} already CLEARED"` (SR:186), `"invoice {id} not found"` (SR:190). LedgerError/FxRateMissing (RuntimeError) -> 500.
Side effects: §2.4. Commit RR:143.

### POST /api/v1/payments/{payment_id}/reverse -> 200 PaymentOut (RR:150-163)
Request `ReverseRequest` (CPAY:36-37): `reason` str required (empty allowed; FE disables the button when empty, FE/ManualReviewView.vue:199).
Errors -> 400: `"payment {id} not found"` (SR:220), `"only CLEARED payments can be reversed (got {status})"` (SR:222), `"no active journal entry to reverse for payment {id}"` (SR:231; always the case for MANUAL payments, PN 8). RuntimeError wht_booked negative (SR:241-245) and LedgerError -> 500; memo over 500 chars -> DataError 500.
Side effects: §2.5. Commit RR:159.

---

## 2. Service rules

### 2.1 Matching engine (SM)
Constants: `AUTO_CLEAR_THRESHOLD = Decimal("95.0")` (SM:22), `AMOUNT_EPSILON = Decimal("0.0001")` (SM:23), `DATE_WINDOW_DAYS = 14` (SM:24).
Expected cash: `withholding.expected_cash` = `max(0, balance_due - (wht_amount - wht_booked))` (W:53-56); candidate carries `wht_outstanding = invoice.wht_amount - invoice.wht_booked` (SR:65) and presents it with wht_booked 0 (SM:65-80). Go: `withholding.ExpectedCash(balanceDue, whtAmount, whtBooked)`.
`is_exact_match` (SM:89-103): `payment.currency.upper() == invoice.currency.upper()` AND `|payment.amount - expected_cash| <= 0.0001`. Shared with MCP `resolve_reconciliation_match` (MCPW:69).
Score (SM:138-155), 0-100, quantized to 0.0001 half-even:
- amount 50 if `|amount - expected_cash| <= 0.0001` else 0;
- currency 20 if upper-case equal else 0;
- payer 20 x best `difflib.SequenceMatcher(None, payer.lower().strip(), name.lower().strip()).ratio()` over customer name + `matching_aliases` (SM:117-124). The float ratio is converted with `Decimal(str(ratio))` (Python shortest repr). SequenceMatcher default `autojunk=True` (kicks in at 200+ chars; payer_name is up to 255). Go needs a faithful port of `ratio()` (2*M/T with Python's longest-match block algorithm), not Levenshtein;
- date 10 x proximity: anchor = min(|pay - issue_date|, |pay - due_date|) days; `0` if anchor >= 14 else `Decimal(14 - anchor) / Decimal(14)` (28-digit context) (SM:127-135). NULL issue_date or due_date -> TypeError 500 (§5).
- `payer_reference` is carried but **not used** in scoring.
`match_payment` (SM:158-234):
1. keep candidates with status in (`OPEN`,`SENT`,`PARTIAL`) (SM:168). None -> PENDING_MANUAL_REVIEW, invoice None, confidence `Decimal("0")`, amount_mismatch True, adjustment `CREDIT_ON_ACCOUNT`, reasons `("no open invoice matches payer/currency",)` (SM:170-179).
2. sort by score desc (stable `sorted`, so equal scores keep query order; the query has no ORDER BY), take the first; **no runner-up/ambiguity check** (SM:181-186; PN 6).
3. reasons, in order (SM:193-202): `"currency mismatch: payment {pcur} vs invoice {icur}"` (raw case, not upper-cased); `"amount mismatch: delta={amount - expected}"` (Python Decimal str, e.g. `delta=-100.0000`); `"confidence {confidence} below threshold 95.0"` (confidence 4 places, e.g. `confidence 72.8571 below threshold 95.0`).
4. not exact OR confidence < 95 -> PENDING_MANUAL_REVIEW with the best invoice attached; adjustment: currency mismatch -> `NONE`; amount < expected -> `PARTIAL_PAY`; amount > expected -> `CREDIT_ON_ACCOUNT`; else `NONE` (SM:206-224). Note: within epsilon but not equal still compares with `<`/`>`, so an exact-within-epsilon low-confidence payment can be tagged PARTIAL_PAY/CREDIT_ON_ACCOUNT.
5. else CLEARED, adjustment NONE, reasons `("exact amount+currency, high payer+date confidence",)` (SM:226-234).
Consequence: exact amount + currency gives 70; CLEARED needs payer + date >= 25 out of 30 (e.g. payer ratio 1.0 and date within 7 days, or ratio >= 0.75 and same day).
Currency mismatch is unreachable from intake: candidates are pre-filtered by `Invoice.currency == payload.currency` (SR:52, exact case; webhook upper-cases, email upper-cases). A payment in a currency with no open invoices lands as "no open invoice" CREDIT_ON_ACCOUNT. No auto-conversion anywhere.

### 2.2 Candidate load (SR:47-68)
`SELECT invoice, customer FROM invoices JOIN customers ON customer_id WHERE status IN ('OPEN','SENT','PARTIAL') AND currency = :cur` — no ORDER BY, no limit, no lock. `customer_aliases = tuple(customer.matching_aliases or ())` (jsonb array, DDL:143).

### 2.3 process_incoming_payment (SR:124-164) — "caller commits"
1. load candidates, score.
2. INSERT payments: invoice_id/customer_id of best candidate **even when held** (SR:132-136), amount, currency, payer_name, payer_reference, payment_date, intake_source, external_ref, status, adjustment_type, confidence_score (4-place value; DB stores 2 places). wht_amount ORM default 0. flush (SR:150) — duplicate key raises here.
3. if CLEARED: `db.get(Invoice)` (None -> RuntimeError `"CLEARED result without invoice — engine invariant violated"`, SR:155); `_clear_against_invoice` (§2.6); then `balance_due = 0` unconditionally ("sub-epsilon residue", SR:157-158); status `PAID` if not already (SR:159-160); `ensure_subscription_restored` (SR:161).
4. log row: action `CLEARED` or `HELD`, reasons = result reasons, actor None for intake (SR:110-121, :163).
Ledger posting only on CLEARED. HELD payments never touch the invoice.

### 2.4 approve_manual_match (SR:167-213)
Checks: payment exists; status != `CLEARED` (so `PENDING_MANUAL_REVIEW` **and `FLAGGED`** are approvable); invoice exists. **No check of invoice status** (DRAFT, VOID, PAID all accepted), of currency, or of amount vs balance.
1. payment.invoice_id/customer_id = invoice's; status `CLEARED`; adjustment_type = request value (stored only; no logic uses it) (SR:192-196); flush.
2. `_clear_against_invoice` (§2.6).
3. `balance_due == 0` -> `PAID` + `ensure_subscription_restored`; else `PARTIAL` (SR:199-203).
4. log `OVERRIDDEN`, reasons `[f"manual approval against invoice {invoice_id}"]`, actor = user (SR:205-212). MCP passes actor None (MCPW:40-45, :86-91) — the Go function must take `*uuid.UUID`.

### 2.5 reverse_payment (SR:216-258)
1. payment exists and status == `CLEARED`.
2. find entry `source_id = payment_id AND source_type = 'PAYMENT' AND reversed_by_entry_id IS NULL` via `db.scalar` (first row, no ORDER BY, no error if several) (SR:224-229).
3. `ledger.post_reversal(entry_id, memo=f"Reversal: {reason}")` (SR:233): new entry with same source_type/source_id, lines with debit<->credit and base debit<->base credit swapped, same fx_rate; `original.reversed_by_entry_id = reversal.entry_id` (SL:222-251). The reversal entry itself has `reversed_by_entry_id` NULL. Errors `"entry {id} not found"`, `"entry {id} already reversed"` (SL:224-227).
4. payment.status `FLAGGED` (SR:234). wht_amount on the payment is kept.
5. if invoice_id set and invoice exists: `balance_due += payment.amount + payment.wht_amount`; `wht_booked -= payment.wht_amount`; negative -> RuntimeError (SR:241-245); status `OPEN` unconditionally (SR:248). Hosting restore is **not** undone; PN 7 (cross-currency restore is wrong).
6. log `REVERSED`, reasons `[reason]`, actor = user (SR:250-257).

### 2.6 _clear_against_invoice and the ledger (SR:82-107, SL:98-219)
- `same_currency = payment.currency == invoice.currency` (exact case).
- `settles = not same_currency or |payment.amount - expected_cash(invoice)| <= 0.0001`; `withheld = invoice.wht_amount - invoice.wht_booked if settles else 0` (SR:90-94). Partial payments book no withholding (test_manual_approve_non_matching_amount_books_no_withholding).
- `payment.wht_amount = withheld` (SR:96); post ledger **before** changing balance_due (cross-currency reads it) (SR:97-98); `wht_booked += withheld` (SR:99).
- balance: same currency -> `max(0, balance_due - (amount + withheld))`; cross currency -> 0 (SR:100-107).
Ledger `post_payment_cleared` (SL:98-219):
- refuses status != CLEARED: `"refuse to post ledger for payment {id} with status {status!r} — only CLEARED may post"` (SL:115-119); withheld < 0: `"withheld tax cannot be negative (got {w})"` (SL:121-122).
- DR 1000 Operating Bank `payment.amount` in payment.currency; DR 1300 Prepaid Income Tax `withheld` in **invoice.currency** if > 0 (SL:127-147).
- same currency: CR 1100 AR `amount + withheld`, with `base_amount_credit` overwritten to the exact sum of the debit lines' base debits (SL:149-164).
- cross currency: CR 1100 AR `invoice.balance_due` in invoice currency (balance <= 0 -> LedgerError `"cross-currency settlement requires invoice balance_due > 0 (invoice {id} balance={b})"`, SL:166-171); `diff = bank_base + wht_base - ar_base`; diff < 0 -> DR 5900 FX Loss `-diff`; diff > 0 -> CR 4900 FX Gain `diff`; both in `base_currency` (rate 1) (SL:181-208).
- each line: `fx.convert(amount, currency, payment_date)` = newest rate with `as_of_date <= date`, base currency -> 1 without DB; missing -> FxRateMissing `"no fx rate for {src}->{base} on or before {date}"` (SFX:36-60); `base_amount = (amount * rate).quantize(0.0001)` half-even (SFX:72). Account lookup by code; missing -> `"chart_of_accounts missing code {code}"` (SL:39-43).
- entry source_type `PAYMENT`, source_id payment_id, memo `"Payment {payment_id} cleared against invoice {invoice_id}"` (SL:210-215); balance asserted in base (and per currency when single-currency) (SL:46-70).
- Go equivalents exist already (§4).

### 2.7 Receipt numbering
`RCT-` + first 8 hex of payment_id upper-cased (RCP:31-33). Not stored; derived on each call. Spec confirms ("Document numbers").

### 2.8 Timestamps
payments.created_at and reconciliation_log.created_at come from DB `CURRENT_TIMESTAMP` (MPAY:40-42, MLOG:25-27); journal posted_at likewise (MJ:18-20). The only Python clock writes are `hosting_last_paid_at = _utcnow()` (H:189) and email `date.today()` (SE:62).

### 2.9 Call sites outside the routes (later plans; Go functions must suit them)
- `approve_manual_match`: MCP `resolve_reconciliation_match` (MCPW:48-95), after its own `is_exact_match` guard; errors returned as `{"error": ...}`, actor may be None, uses `tool_session()` and `db.flush()` (commit by the session helper).
- `is_exact_match`: MCPW:37, :69.
- MCP read `get_reconciliation_queue(limit=200)` (MCPR:41-50) = awaiting-review with a limit.
- `ensure_hosting_restored` is also called by the hosting worker flow (H:181; test_hosting.py:110).
- No worker in app/workers/tasks/ calls the reconciliation, matching or ledger functions (`fx_sync`, `hosting_enforcement`, `recurring`, `usage_lock` only). `PaymentIntakeWidget.vue` reads `/stats/dashboard` (reports plan), not these routes.
Suggested Go shape: a `payments` package with `ProcessIncoming(ctx, tx, baseCcy, IntakePayload) (Payment, error)`, `ApproveManual(ctx, tx, baseCcy, paymentID, invoiceID uuid.UUID, actor *uuid.UUID, adj string) (Payment, error)`, `Reverse(ctx, tx, paymentID uuid.UUID, actor uuid.UUID, reason string) (Payment, error)`, `IsExactMatch(...)`, and a pure `Match(IncomingPayment, []Candidate) MatchResult`.

---

## 3. Tests in scope

[IN] = this plan. [IN*] = this plan with a stub mailer/renderer. [OUT] = later plan.

tests/unit/test_matching_engine.py (pure engine):
- [IN] test_exact_match_clears — exact amount/currency/payer/date -> CLEARED, NONE.
- [IN] test_amount_mismatch_holds_and_no_ledger_hint — short payment -> PENDING, PARTIAL_PAY, amount_mismatch.
- [IN] test_overpayment_flagged_as_credit_on_account — over -> PENDING, CREDIT_ON_ACCOUNT.
- [IN] test_currency_mismatch_always_holds_even_with_exact_amount — PENDING, adjustment NONE, currency_mismatch.
- [IN] test_amount_epsilon_boundary_accepts_within_tolerance — delta 0.0001 still CLEARED.
- [IN] test_payer_alias_hit_raises_confidence — alias match beats name miss.
- [IN] test_payer_alias_miss_does_not_fail_if_amount_currency_exact — weak payer still scored, outcome per threshold.
- [IN] test_empty_candidates_holds_as_credit_on_account — no candidates -> PENDING, CREDIT_ON_ACCOUNT, confidence 0.
- [IN] test_date_decay_still_clears_within_window — few days off still >= 95.
- [IN] test_date_outside_window_still_holds_if_payer_weak — date 0 + weak payer -> PENDING.
- [IN] test_withholding_net_payment_is_exact_match — net-of-PPh amount is exact.
- [IN] test_gross_payment_on_withholding_invoice_goes_to_review — gross amount -> review.

tests/integration/test_reconciliation_pipeline.py (service level):
- [IN] test_cleared_payment_posts_exactly_one_balanced_entry
- [IN] test_amount_mismatch_holds_and_posts_no_journal
- [IN] test_currency_mismatch_holds_regardless_of_amount
- [IN] test_reconciliation_log_written_for_every_payment
- [IN] test_reversal_creates_balancing_entry_and_wires_link
- [IN] test_invariant_all_active_entries_balance_to_zero
- [IN] test_cross_currency_manual_approval_books_fx_loss
- [IN] test_cross_currency_manual_approval_books_fx_gain
- [IN] test_manual_approval_clears_and_posts

tests/integration/test_reconciliation_withholding.py:
- [IN] test_auto_clear_books_prepaid_tax — DR 1300 for outstanding PPh, wht_booked set.
- [IN] test_reverse_then_repay — reversal restores balance and wht_booked; repay clears again.
- [IN] test_manual_approve_non_matching_amount_books_no_withholding
- [IN] test_foreign_currency_withholding_entry_balances_in_base
- [IN] test_gross_payment_is_held_without_side_effects

tests/integration/test_record_payment_withholding.py:
- [IN] test_record_net_plus_withheld_marks_paid
- [IN] test_record_exceeding_balance_400
- [IN] test_record_without_wht_unchanged
- [IN] test_record_partial_with_wht_reduces_balance_by_both
- [IN] test_exceeding_error_mentions_withheld — sentence with "amount + withheld".
- [IN] test_withheld_above_outstanding_400
- [IN] test_withheld_above_remaining_after_partial_400
- [IN] test_pph_only_payment_settles_stuck_partial — amount 0 + wht settles.
- [IN] test_zero_amount_and_zero_withheld_400
- [OUT] test_send_invoice_body_quotes_amount_to_transfer — invoice send email (documents/email).
- [OUT] test_send_invoice_body_without_wht_unchanged — same.
- [OUT] test_payment_reminder_quotes_expected_cash — MCP reminder.
- [OUT] test_bill_payment_schema_still_requires_positive_amount — bills schema (plan 6 area).

tests/integration/test_invoice_payments_list.py:
- [IN] test_list_payments_newest_first; [IN] test_list_payments_empty; [IN] test_list_payments_unknown_invoice_404; [IN] test_list_payments_excludes_non_cleared.

tests/integration/test_send_receipt.py (monkeypatches `send_email`):
- [IN*] test_send_receipt_happy_path — 200 `{"sent_to"}`, to/cc passed, filename `receipt-RCT-*.pdf`, content starts `%PDF-`, body contains `400` and `600`.
- [IN*] test_send_receipt_defaults_to_customer_contact_email
- [IN] test_send_receipt_no_recipient_400
- [IN] test_send_receipt_payment_not_on_invoice_404
- [IN] test_send_receipt_unknown_payment_404
- [IN*] test_send_receipt_email_failure_502

tests/integration/test_customer_cc.py:
- [IN*] test_send_receipt_multiple_cc_normalised — `" a@example.com;b@example.com,, A@example.com "` -> `"a@example.com, b@example.com"`.
- [IN] test_send_receipt_invalid_cc_422.

tests/unit/test_receipt_pdf.py:
- [IN] test_receipt_reference_shape; [IN] test_balance_note_partial_payment_shows_remaining; [IN] test_balance_note_full_payment_says_paid_in_full.
- [OUT] test_render_receipt_pdf_returns_pdf_bytes; [OUT] test_render_receipt_pdf_without_customer; [OUT] test_render_receipt_pdf_escapes_xml_special_chars_in_names (documents).

Out of scope but touching these services:
- [OUT] tests/integration/test_hosting.py::test_hosting_enforcement_suspends_and_reconciliation_restore_reactivates (hosting plan).
- [OUT] tests/integration/test_mcp_reads_all.py::test_get_reconciliation_queue_returns_list, ::test_get_reconciliation_queue_excludes_other_statuses, ::test_get_reconciliation_queue_respects_limit (MCP).
- [OUT] tests/integration/test_mcp_writes_all.py::test_resolve_reconciliation_match_exact_match_clears, ::_currency_mismatch_refuses, ::_amount_mismatch_refuses (MCP).

Counts: **50 [IN]/[IN*]**, 14 [OUT]. All 50 are `todo` rows in backend/TESTS.md (lines 29-30, 61-64, 285-307, 348-353, 399-410, 466-468). No Python test covers the intake routes' HMAC/token checks, `parse_generic_webhook`, `parse_inbound_email`, audit-log, awaiting-review, GET payment, the payment log, or the manual-review/reverse **routes** (only the service functions); Go should add route tests for these.

---

## 4. Existing Go code

- **ledger** (backend/internal/ledger/ledger.go): constants `BankCode "1000"`, `ReceivableCode "1100"`, `PrepaidTaxCode "1300"`, `FXGainCode "4900"`, `FXLossCode "5900"` (:31-35); `ErrLedger` with Python sentences (:38-45); types `Payment{ID, Status, Amount, Currency, Date}` (:51-57), `Invoice{ID, Currency, BalanceDue}` (:60-64), `Line`, `Entry` (:68-82).
  - `func PostPaymentCleared(ctx context.Context, tx pgx.Tx, baseCurrency string, p Payment, inv Invoice, withheld decimal.Decimal) (Entry, error)` (:97).
  - `func PostReversal(ctx context.Context, tx pgx.Tx, _ string, originalID uuid.UUID, memo *string) (Entry, error)` (:189): locks the original (`LockEntry`, queries.sql:16-23, `FOR UPDATE`), so a concurrent second reversal waits and is refused (ledger_test.go:450-520).
  - queries.sql has `AccountIDsByCode`, `InsertEntry`, `InsertLine`, `LockEntry`, `EntryLines`, `SetReversedBy` only. **Gap**: no "active entry for source" lookup (SR:224-229). The plan must add one and decide how to exclude reversal entries (they share source_type/source_id and have `reversed_by_entry_id` NULL; §5.2).
- **fx** (backend/internal/fx/fx.go): `func GetRate(ctx, q *fxdb.Queries, from string, on time.Time, to string) (decimal.Decimal, error)` (:39), `func Convert(ctx, q *fxdb.Queries, amount decimal.Decimal, from string, on time.Time, to string) (Conversion, error)` (:56), `ErrRateMissing` (:29-30). Ledger already calls these.
- **withholding** (backend/internal/withholding/withholding.go): `func ExpectedCash(balanceDue, whtAmount, whtBooked decimal.Decimal) decimal.Decimal` (:59) — use for candidates with `whtBooked = 0` and `whtAmount = outstanding`; `Compute` (:44), `Normalize` (:69).
- **invoices** (backend/internal/invoices): no payment code. `settleInvoice` (lifecycle.go:53, unexported) is mark-paid. No exported hook for balance_due/wht_booked/status updates; the payments package will write invoices directly (needs `UPDATE invoices SET balance_due, wht_booked, status` queries, ideally with `SELECT ... FOR UPDATE`). `InvoiceOut` not needed (all routes return PaymentOut).
- **hosting**: not ported. `ensure_hosting_restored` (H:181-193) is called on every PAID transition (RI:575, SR:161, SR:201). **Gap**: define a `HostingRestorer` interface with a no-op (or a minimal port stamping `hosting_last_paid_at`) and record it as a port note; test_hosting is OUT.
- **email/PDF**: `invoices.LogNotifier` (notify.go) is the pattern for a logging stand-in; send-receipt needs a mailer and a renderer interface (§1).
- **platform**: Python printers (`pyRepr`/`pyStr`, used by ledger refusals) cover the `{!r}`, Decimal-str and list-repr sentences above (`unknown adjustment_type 'X'`, `webhook payload missing fields: ['a', 'b']`).
- **Config gap**: Go config must add `WEBHOOK_HMAC_SECRET` and `EMAIL_WEBHOOK_TOKEN` (spec "Webhooks" keeps Python's headers, statuses and sentences).
- **Difflib gap**: SequenceMatcher.ratio needs a Go port with golden tests against Python values.

---

## 5. Oddities

1. **Ambiguous matches auto-clear.** Ties take the first candidate of an unordered query (SM:181-186, SR:47-54); PN 6. Suggest change: hold when the runner-up's score is within some margin or equal.
2. **Reversal can reverse the wrong entry after a re-approval.** The reversal entry keeps `source_id = payment_id` with `reversed_by_entry_id` NULL (SL:229-245), FLAGGED payments can be re-approved (SR:185-186 only refuses CLEARED), and the lookup uses `db.scalar` with no ORDER BY (SR:224-229). After approve -> reverse -> approve -> reverse, two rows match and the old reversal entry may be "reversed" (re-posting the original). Change: exclude entries that are themselves reversals (e.g. `NOT EXISTS (SELECT 1 FROM journal_entries o WHERE o.reversed_by_entry_id = e.entry_id)`), order by posted_at desc.
3. **Manual approval ignores invoice status, currency and amount.** DRAFT/VOID/PAID invoices are accepted (SR:188-196); a same-currency overpayment credits AR for the full amount + withheld while balance_due clamps at 0 (SR:100-103, SL:149-157), leaving a credit in AR. Change: refuse DRAFT/VOID (as record-payment does, RI:525-529) and refuse or split amounts above the balance.
4. **Manual record-payment posts no ledger and cannot be reversed.** RI:510-594; reverse fails with "no active journal entry" (SR:231); PN 8. Keep for parity in this plan, or post through `_clear_against_invoice`; decide with the owner.
5. **Race conditions.** record-payment, approve and reverse all read balance_due/wht_booked/status then write without `FOR UPDATE` (RI:522-577, SR:183-203, SR:219-248). Two concurrent record-payments can both pass the `exceeds balance` check and drive balance negative before the clamp; two concurrent approvals of one PENDING payment both post a journal entry. Change: lock the payment and invoice rows in Go (`SELECT ... FOR UPDATE`), invoice first.
6. **Reversal re-opens to OPEN and does not undo hosting restore** (SR:248, H:181-193); cross-currency restore wrong (PN 7). Keep OPEN; note hosting.
7. **Intake 500s on bad input.** Non-numeric amount (InvalidOperation, SW:23), non-string payment_date (TypeError, SW:27), currency > 3 chars (DataError, SW:24), non-object JSON (SW:18, :26), negative amounts accepted (no check). Change: 400 with a sentence; reject amount <= 0.
8. **NULL issue_date/due_date on an open invoice crashes intake** (`pay_date - None`, SM:129-132; columns nullable DDL:237-238). Change: treat a missing date as proximity 0.
9. **Held payments carry invoice_id/customer_id of the best guess** (SR:132-136), so send-receipt will mail a receipt for a PENDING or FLAGGED payment (RI:652-654 checks only invoice_id). Change: require status CLEARED in send-receipt (404 or 400).
10. **Email intake defaults**: `external_ref` becomes `""` without message_id or ref, so a second such email is a 409 (SE:64); `payment_date` defaults to server-local `date.today()` (SE:62); `(?i)` lets any three letters be a currency (SE:18, :72). Keep, note.
11. **`str(None)` stored as `"None"`** for webhook payer_name/external_ref (SW:25, :29). Change: 400 on null.
12. **Default secrets** `dev-webhook-secret-change-me` / `dev-email-token-change-me` (CFG:41-42) are accepted in production. Change: refuse to start with defaults outside dev.
13. **Response decimal scale differs by route** (intake returns unscaled amounts and 4-place confidence, GET returns 4-place amount and 2-place confidence) because of `expire_on_commit=False` (DB:11, RPAY:34-39). Change: always the stored scale; port note.
14. **audit-log `limit` has no lower bound** (RR:26): negative -> Postgres error 500. Change: `ge=0`/`ge=1` -> 422.
15. **`GET /payments/{id}/log` is 200 `[]` for unknown ids** (RR:105-125); manual-review/reverse give **400** (not 404) for an unknown payment (SR:184, :220, RR:144-146). PN 3; keep 400 for parity or move to 404 together.
16. **Ordering ties**: all rows of one request share `CURRENT_TIMESTAMP`; audit-log, awaiting-review, payment log and the invoice payments list order by created_at without a tie-break (RR:48, :88, :114; RI:611). Change: add `log_id`/`payment_id` as the last sort key.
17. **`adjustment_type` from manual-review is stored but has no effect** (SR:196), and the engine's PARTIAL_PAY/CREDIT_ON_ACCOUNT on held payments is advisory only. Keep.
18. **Unused schemas** `WebhookPayload` and `EmailWebhookPayload` (CPAY:40-47, :79-89) look like validation but are never applied. Drop in Go; validate by hand as Python does.
19. **Ledger/FX errors on approval are 500** (RuntimeError subclasses not caught, RR:139-146; SFX:26-27, SL:35-36): a missing FX rate on a USD approval gives a 500 instead of a sentence. Change: 400 with the LedgerError/FxRateMissing text.
20. **`notes[:200]` slices code points** (RI:583); Go must slice runes, not bytes.
21. **A same-currency auto-clear forces balance_due 0** even though `_clear_against_invoice` already computed it (SR:157-158); harmless within epsilon. Keep.
