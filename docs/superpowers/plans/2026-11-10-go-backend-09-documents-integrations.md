# Go backend, plan 9: documents and integrations

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> **Refreshed 2026-11-10 against `main` at 6bf89c5 (plan 7 merged).** Checked present: `payments.FormatAmount` (receipt.go:75), `ReceiptRenderer`/`StubRenderer` (receipt.go:28-34), `HostingRestorer{RestoreIfPaid}`/`NoHosting` (hosting.go:13-22, called from `Service.restore`, service.go:68), `email.Message{Cc, Attachments}` and `email.Attachment` (email.go:14-22), `platform.ParseCcEmails` (email.go:97). Runs in parallel with plan 8 in the worktree `D:/Development/wenhao-billing-plan9` (run `./dev` with `COMPOSE_PROJECT_NAME=wenhao-billing`); plan 8 touches no file this plan creates, but both append to `PORT-NOTES.md`, `TESTS.md` and `ROUTES.md`, so this branch rebases on `main` after plan 8 merges. The owner has not answered the three questions below; their defaults stand.

**Goal:** Go renders invoice, quotation and receipt PDFs with Maroto, and sends mail through Resend. It suspends and restores hosting through Cloudflare, and runs the four scheduled jobs with Python's lock keys and a catch-up of one missed slot. It answers the 4 PDF and send routes with Python's contract.

**Architecture:** The new and changed packages:
- `internal/pdf`: formatters, one render-data struct, and Maroto renderers for the three documents. Fonts and the default logo are embedded, and the logo resolver is here.
- `internal/email`: gains `ResendSender`.
- `internal/hosting`: the Cloudflare client, suspend, restore and enforcement.
- `internal/jobs`: the slot runner on `gokit/sched`, plus `scheduled_job_runs`.
- The invoices and quotations packages gain their pdf and send routes.

`cmd/api` swaps in the real implementations:
- `ResendSender` replaces `LogSender` when `RESEND_API_KEY` is set;
- an `EmailNotifier` replaces `LogNotifier`;
- the Maroto receipt renderer replaces `StubRenderer`;
- `hosting.Restorer` replaces `NoHosting`.

**Tech Stack:** Go 1.27, `github.com/johnfercher/maroto/v2` (pinned, vendored), `github.com/resend/resend-go/v2` (or a thin `net/http` client if the SDK cannot set the idempotency key; decide in Task 1 and note it), gokit v0.2.0 `sched` (vendored now), pgx v5, sqlc 1.31.1, Postgres 16. A text-extraction library for PDF tests only (for example `github.com/ledongthuc/pdf`), vendored as a test dependency.

**Spec:** `docs/superpowers/specs/2026-11-08-go-backend-design.md` ("Documents", "Email", "Background work", "Database").

**Contract inventory:** `docs/superpowers/plans/2026-11-10-go-backend-09-documents-integrations-inventory.md`:
- §0 shapes and requests;
- §1 the 4 routes;
- §2 formatters (§2.1), document data (§2.2–§2.4), **the layout spec (§2.5)**, logo (§2.6), email (§2.7), Cloudflare (§2.8), hosting (§2.9), scheduler (§2.10), config (§2.11);
- §3 73 tests to port and 9 to drop;
- §5 20 oddities.

## Global Constraints

**Python and port notes**
- `backend-python/` is frozen. Every "Go changes these" item gets a port note.

**PDFs**
- The design is the HTML/WeasyPrint one (inventory §2.5): palette, fonts (Inter, Manrope, JetBrains Mono, embedded TTF), sections, a header and footer on every page, and table rows that never split. It is a faithful reading, not a pixel copy (spec).
- Text values come from the §2.1 formatters, with display rounding **half-even** (`RoundBank`). Minus signs in the PDF are U+2212; in email bodies they are ASCII.
- `Content-Disposition: inline; filename="invoice-<number or first 8 hex of id>.pdf"`. When the name is not plain ASCII, it uses an RFC 6266 `filename*` (Go change 6).

**Email**
- Sends the owner starts are synchronous. A failure is **502** problem+json with `failed to send invoice email` / `failed to send quotation email`.
- The Resend idempotency key is the document id plus SHA-256 of the recipients, subject and body (spec).
- With no `RESEND_API_KEY`, mail is logged as today.

**Scheduler**
- Lock keys are `int64(crc32.ChecksumIEEE(id))`, **unsigned**: recurring 3587987709, usage 2413487788, hosting 1601569364, fx 1988665425.
- Slots are UTC: daily 02:00, 03:00, 04:00, and Monday 05:00.
- One missed slot is caught up after a restart. A slot older than 24 h is skipped and logged.
- `SCHEDULER_ENABLED` defaults to **false** in Go (Go change 8).

**Config**
- New config uses Python's names and defaults (inventory §2.11): `RESEND_*`, `SMTP_REPLY_TO`, `CLOUDFLARE_*`, `SCHEDULER_ENABLED`. `PDF_ENGINE` and `SMTP_*` (except `SMTP_REPLY_TO`) are dropped.

**External services in tests**
- Resend, Cloudflare and the logo host are reached only through `httptest` in tests.

**Tests**
- Tests run on pgtestdb. **Every test and subtest calls `t.Parallel()` first.**
- Every guard is mutated once and watched going red: the 502, the idempotency key, the lock key, the slot record, the after-commit restore, and the VOID / INVOICED / no-recipient refusals.

**Commits and branch**
- Commits name their paths and end with `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- The branch is `feat/go-documents-integrations` from `main`. No push or PR until the final review.

## Go changes these (each a port note)

1. **Cloudflare never fails a payment.**
   - In the payment's transaction, Go stamps `hosting_last_paid_at` on the invoice's hosting items.
   - After commit, it restores them best effort: a failure marks the target `ERROR` and is logged and reported, and the daily job retries.
   - The plan 7 interface becomes `HostingRestorer{ StampPaid(ctx, tx, invoiceID) error; RestoreAfterCommit(ctx, invoiceID) }`, and the three payment paths call it that way (§5.1).
2. **Restore respects CANCELLED and `hosting_suspension_enabled = false`**, so a hand-set suspension is not undone by the job (§5.4).
3. **One amount format** in every email body: the quotation send and the draft digest use `FormatAmount`, so IDR has 0 places and other currencies 2 (§5.5).
4. **Receipt summary values carry their own currency code** (§5.7).
5. **The logo**:
   - a data URI is decoded;
   - http(s) is fetched with a 5 s timeout and a 2 MB cap;
   - the type is sniffed from the bytes;
   - a failure or a non-image falls back to the default logo;
   - fetched logos are cached per URL for 10 minutes (§5.8, spec).
6. **`Content-Disposition` is escaped** (§5.15).
7. **The draft digest's HTML is escaped** (§5.13).
8. **Missed runs catch up one slot, and `SCHEDULER_ENABLED` defaults to false** (§5.10, spec).
9. **`to_email` is validated** with `platform.NormalizeEmail`. A bad address is 422 at `to_email`: `invalid email address: <addr>` (§5.14).
10. **The invoice and quotation PDFs show the footer and PO/SO number** when set: the PO/SO number in the meta strip as a fourth cell, the footer under Notes/Terms (§5.6, spec "notes and footer").

**Kept (port notes):**
- Send moves DRAFT/OPEN to SENT without finalize (§5.2).
- Quotation send stamps `last_sent_at` for any sendable status, and its grammar.
- The usage-lock stub fails.
- `hosting_last_paid_at` is stamped on every hosting item.
- The digest goes to every user.
- Display rounding is half-even.
- The enforcement retry keeps SUSPEND_PENDING.

**Questions for the owner** (rulings above, reversible):
- Should sending a DRAFT invoice finalize it first, or be refused (§5.2)?
- Should the footer and PO/SO number come back on PDFs (change 10)?
- Is `to_email` validation (change 9) wanted?

## Review Focus

1. Recording a payment on a suspended hosting invoice while Cloudflare is down must succeed. The payment is kept, the target is marked ERROR, and the next enforcement run restores it: Task 5 `TestPaymentSucceedsWhenCloudflareDown`.
2. Python and Go running side by side must never run the same job at once: Task 6 `TestLockKeysMatchPython` (keys above 2^31) and `TestPythonHoldingLockSkipsGo`.
3. A deploy at 02:30 UTC must run the 02:00 recurring scan once on start-up, and a second restart must not run it again: Task 6 `TestCatchUpOneSlotOnce`.
4. An invoice whose customer name contains `<b>&"` must render with that text intact in the PDF: Task 3 `TestRenderEscapesSpecialCharacters`.
5. A send retried after a timeout must reach Resend with the same idempotency key, and a changed body must get a new key: Task 1 `TestIdempotencyKeyStableAndBodyBound`.

---

### Task 1: Resend sender and the draft digest

**Files:**
- Create: `backend/internal/email/resend.go`, `resend_test.go`, `backend/internal/invoices/notify_email.go`, `notify_email_test.go`
- Modify: `backend/internal/platform/config.go` (`RESEND_*`, `SMTP_REPLY_TO`), `backend/internal/email/email.go` (an `IdempotencyKey` field on `Message`), `backend/cmd/api/main.go`

**Interfaces:**
- Produces:
  - `func NewResendSender(cfg ResendConfig, client *http.Client) Sender`;
  - `type EmailNotifier struct { Q *invoicesdb.Queries; Sender email.Sender; AppBaseURL string; Logger *slog.Logger }`, implementing `DraftNotifier`.

The Resend payload is exactly inventory §2.7:
- `from "<name> <email>"`;
- `to` as a list;
- `subject` and `text`;
- `html` only when set;
- `cc` only when non-empty;
- `reply_to`;
- attachments as base64.

Status ≥ 400 logs `Resend rejected email to <to>: <status> <body>` and returns an error.

The digest subject, text and HTML follow §2.7, with HTML escaped and amounts through `payments.FormatAmount`. It is sent once per user, and each failure is logged and skipped.

- [ ] **Step 1: Failing tests.** Port the 8 tests of `test_email_resend.py` (the settings ones against Go config) and the 3 of `test_recurring_drafts_email.py`. Add:
  - `TestIdempotencyKeyStableAndBodyBound`;
  - `TestResendTimeout`;
  - `TestDigestEscapesHtml`.
- [ ] **Step 2: Implement, mutate, commit.**
  - Mutation: drop the key header. The idempotency test goes red.
  - Commit `feat(go): Resend sender and draft digest email`.

---

### Task 2: Formatters and document data

**Files:**
- Create: `backend/internal/pdf/format.go`, `data.go`, `load.go`, and tests

**Interfaces:**
- Produces:
  - the formatters `CurrencyDecimals`, `FmtAmount` (moved from `payments.FormatAmount`, which then calls it), `FmtNum`, `FmtQty`, `FmtPct`, `DiscountAmount`, `NPWPLine`, `FmtDate`, `StatusBadgeClass` and `StatusLabel`;
  - `type Document struct { … }`: one struct for all three documents, with the §2.2 keys plus `DocTitle`, `Ref`, `Meta []MetaCell`, `Summary []SummaryRow`, `Hero`, `FromLabel`, `ToLabel`, `Simple bool`;
  - `InvoiceDocument(...)`, `QuotationDocument(...)` and `ReceiptDocument(...)`;
  - `func LoadInvoice(ctx, q, id) (InvoiceData, error)`, which reads the invoice, its lines by `position`, the customer, business row 1 (**without creating it**) and the payment accounts by currency. The quotation and receipt loaders are the same shape.

- [ ] **Step 1: Failing tests.** Port:
  - the 24 [IN] of `test_pdf_context.py`;
  - the 6 formatter tests of `test_invoice_pdf.py`.

  Add:
  - `TestFormattersMatchPython`: goldens from the frozen Python for `_fmt_amount`, `_fmt_num`, `_fmt_qty` and `_fmt_pct` on ties, negatives, large values and the 13 zero-decimal currencies;
  - `TestReceiptSummaryLabelsOwnCurrency`.
- [ ] **Step 2: Implement, mutate, commit.** Mutations, each turning its test red:
  - Round half-up (the golden tie cases).
  - Show the Total row with no withholding and nothing paid (`…omits_paid_and_total`).

  Commit `feat(go): document formatters and render data`.

---

### Task 3: Maroto renderers, fonts, logo

**Files:**
- Create: `backend/internal/pdf/render.go`, `layout.go`, `fonts/` (TTF, licences beside them), `default-logo.png`, `logo.go`, and tests
- Modify: `backend/internal/payments` (`main.go` wires `pdf.ReceiptRenderer` in place of `StubRenderer`)

**Interfaces:**
- Produces:
  - `func RenderInvoice(ctx, d Document) ([]byte, error)`, `RenderQuotation` and `RenderReceipt`;
  - `type LogoResolver struct { Client *http.Client; … }`, with `Resolve(ctx, logoURL *string) []byte`;
  - `pdf.ReceiptRenderer` satisfies `payments.ReceiptRenderer`.

The layout follows inventory §2.5 sections 1–8, plus Go change 10. Page size is A4. The PDF title metadata is `"<Doc title> <ref>"`.

- [ ] **Step 1: Failing tests.** These assert on extracted PDF text and on the `Document` used:
  - Port the 4 render tests of `test_invoice_pdf.py` [IN~], the 3 of `test_pdf_render_html.py` [IN~], the 4 of `test_quotation_pdf.py` [IN~], the 3 receipt renders, `test_internal_note_never_reaches_the_pdf`, the 6 of `test_pdf_logo.py` and the 2 of `test_pdf_assets.py`.
  - Add:
    - `TestRenderEscapesSpecialCharacters`;
    - `TestLongInvoiceRepeatsHeaderAndFooter` (60 lines → at least 2 pages, each with the doc title and footer name);
    - `TestLogoSizeCapFallsBack`;
    - `TestOnlyLogoUrlIsFetched`;
    - `TestPdfTitleMetadata`.
- [ ] **Step 2: Owner review.** Render one invoice (IDR with PPh 23 gross-up, partial payment), one quotation and one receipt to the scratchpad. Send them to the owner for a look before the routes go in. Note their feedback; layout changes are allowed within the spec.
- [ ] **Step 3: Implement, mutate, commit.**
  - Mutation: skip the size cap. The cap test goes red.
  - Commit `feat(go): Maroto invoice, quotation and receipt PDFs`.

---

### Task 4: PDF and send routes

**Files:**
- Create: `backend/internal/invoices/documents.go`, `backend/internal/quotations/documents.go`, and route tests
- Modify: `invoices/queries.sql` (`MarkInvoiceSent`: DRAFT/OPEN → SENT), `quotations/queries.sql` (`MarkQuotationSentOnSend`: DRAFT → SENT, `last_sent_at` always)

The routes are inventory §1:
- `GET …/pdf` gives 404 `invoice not found` / `quotation not found`, and renders any status.
- `POST …/send` refuses in this order:
  1. 404;
  2. VOID, for invoices; `cannot send a VOID quotation` / `cannot send a INVOICED quotation`;
  3. no recipient: `no recipient: provide to_email or set a contact_email on the customer`;
  4. a send failure: 502.
- Subjects and default bodies are exactly §1.
- The status change and `last_sent_at` are written only after a successful send, then the document is returned.

- [ ] **Step 1: Failing tests.** Port `test_send_invoice_body_quotes_amount_to_transfer` and `…_without_wht_unchanged`. Add:
  - `TestInvoicePdfHeadersAndBytes`;
  - `TestPdf404`;
  - `TestSendVoidInvoice400`;
  - `TestSendNoRecipient400`;
  - `TestSendFailure502LeavesStatus`;
  - `TestSendDraftBecomesSent` (kept);
  - `TestQuotationSendBodyAndLastSentAt`;
  - `TestQuotationSendRefusals`;
  - `TestBadToEmail422`;
  - `TestDocumentRoutesRequireAdmin`.
- [ ] **Step 2: Implement, mutate, commit.** Mutations, each turning its test red:
  - Change status before sending (the 502 test).
  - Allow VOID (its test).

  Commit `feat(go): invoice and quotation pdf and send routes`.

---

### Task 5: Cloudflare and hosting

**Files:**
- Create: `backend/internal/hosting/cloudflare.go`, `hosting.go`, `enforce.go`, `queries.sql`, generated `hostingdb/`, and tests
- Modify:
  - `backend/internal/payments` (the three paid paths call `StampPaid` in the transaction and `RestoreAfterCommit` after it);
  - `backend/internal/platform/config.go` (`CLOUDFLARE_*`);
  - `backend/cmd/api/main.go`.

**Interfaces:**
- Produces:
  - `type Client struct{…}`, with `Suspend(ctx, target)` and `Restore(ctx, target)`, whose errors follow §2.8;
  - `func Enforce(ctx, pool, cl *Client, today time.Time) (Result{Suspended, Restored, Errors int}, error)`;
  - `type Restorer struct{…}`, implementing plan 7's revised `HostingRestorer`.

The rules are inventory §2.9, with Go changes 1 and 2:
- every write sets `updated_at` and `last_action_at` itself, at seconds precision;
- enforcement processes the oldest items first, and one item's failure does not stop the run, as in Python.

- [ ] **Step 1: Failing tests.** Port the 4 of `test_hosting.py` (the first through plan 7's `ProcessIncoming` with a USD 240 webhook). Add:
  - `TestPaymentSucceedsWhenCloudflareDown`;
  - `TestRestoreSkipsCancelledAndDisabled`;
  - `TestMissingTokenIsCloudflareError`;
  - `TestCloudflareErrorSentences`;
  - `TestRestoreWaitsForAllOpenInvoices`.
- [ ] **Step 2: Implement, mutate, commit.** Mutations, each turning its test red:
  - Call Cloudflare inside the payment transaction (the Cloudflare-down test).
  - Make the grace day comparison inclusive (the not-overdue test).

  Commit `feat(go): Cloudflare hosting suspend, restore and enforcement`.

---

### Task 6: The scheduler

**Files:**
- Create:
  - `backend/internal/jobs/slots.go`, `runner.go`, `jobs.go`, `queries.sql`, generated `jobsdb/`, and tests;
  - `backend/migrations/000NN_scheduled_job_runs.sql`: `(job_id text, slot timestamptz, started_at, finished_at, error text, PRIMARY KEY (job_id, slot))`, additive.
- Modify: `backend/vendor` (add `gokit/sched` with `./dev vendor`; GitHub access through the owner's `gh`), `backend/internal/platform/config.go` (`SCHEDULER_ENABLED`, default false), `backend/cmd/api/main.go`

**Interfaces:**
- Produces:
  - `type Job struct { ID string; Due func(now time.Time) time.Time; Run func(ctx context.Context, slot time.Time) error }`;
  - `func Start(ctx, pool, clock, report func(error), jobs ...Job)`, which ticks every minute through `gokit/sched` under the job's Python lock key.
- On each tick:
  - compute the latest due slot;
  - skip it if it is recorded;
  - if it is older than 24 h, record it as skipped and log;
  - otherwise insert the slot row first (the primary key makes a second runner a no-op), run the job, then record the finish or the error.
- The jobs:
  - `invoices.Scan` with `EmailNotifier`;
  - `invoices.CutoffScan` with an accrual source that fails like Python's stub (kept, note);
  - `hosting.Enforce`;
  - `fx.Sync`.

- [ ] **Step 1: Failing tests.**
  - `TestLockKeysMatchPython`.
  - `TestPythonHoldingLockSkipsGo`: hold `pg_advisory_lock(3587987709)` on another connection, and the tick does not run.
  - `TestDueSlotsDailyAndWeekly`.
  - `TestCatchUpOneSlotOnce`.
  - `TestSlotOlderThan24hSkippedAndLogged`.
  - `TestJobErrorRecordedAndReported`.
  - `TestSchedulerOffByDefault`.
- [ ] **Step 2: Implement, mutate, commit.** Mutations, each turning its test red:
  - Cast the key through int32 (the keys test).
  - Skip the slot insert (the catch-up-once test).

  Commit `feat(go): scheduled jobs with slots and Python lock keys`.

---

### Task 7: Inventories, port notes, checks

- [ ] **Step 1: Routes.** Mark the 4 routes `ported`.
- [ ] **Step 2: Tests.** Mark the 73 tests with their Go names and mutations. The 9 [DROP] tests are `not ported`, with their reasons.
- [ ] **Step 3: Port notes.** Write one entry per "Go changes these" item, per kept behaviour and per intended removal (SMTP, `PDF_ENGINE`, reportlab).
- [ ] **Step 4: Checks.** Run `bash scripts/check.sh` → `all checks passed`.
- [ ] **Step 5: Commit.** Commit `docs(go): documents and integrations ported; port notes`.
