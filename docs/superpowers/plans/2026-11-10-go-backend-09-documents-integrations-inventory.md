# Plan 9 contract inventory: documents and integrations (frozen Python -> Go)

Read-only inventory. All paths are relative to `backend-python/` unless prefixed. Abbreviations:

| Abbr | File |
|---|---|
| RI | app/api/v1/routers/invoices.py (pdf, send; also trigger, record-payment, send-receipt call sites) |
| RQ | app/api/v1/routers/quotations.py (pdf, send) |
| RA | app/api/v1/routers/accounting.py (fx sync route, already ported) |
| DEPS | app/api/deps.py |
| CFG | app/config.py |
| MAIN | app/main.py |
| SI / SQ | app/schemas/invoice.py / app/schemas/quotation.py |
| CEM | app/schemas/email.py |
| PF | app/services/pdf/__init__.py (facade) |
| PE / PR / PA / PL | app/services/pdf/engine.py / render.py / assets.py / logo.py |
| CTX | app/services/pdf/context.py |
| TPL | app/services/pdf/templates/document.html.j2 |
| IP | app/services/invoice_pdf.py (reportlab invoice + shared formatters) |
| QP | app/services/quotation_pdf.py (reportlab quotation) |
| RCP | app/services/receipt_pdf.py (reportlab receipt + `receipt_reference`, `balance_note`) |
| EM | app/services/email.py |
| DN | app/services/draft_notifications.py |
| CF | app/services/cloudflare.py |
| H | app/services/hosting.py |
| W | app/services/withholding.py (Go: backend/internal/withholding) |
| QS | app/services/quoting.py |
| FXP | app/services/fx_provider.py (Go: backend/internal/fx/provider.go) |
| SCH | app/workers/scheduler.py |
| REC / UL / HE / FXS | app/workers/tasks/recurring.py / usage_lock.py / hosting_enforcement.py / fx_sync.py |
| MCT / MIT / MBP / MPA | app/models/cloudflare_target.py / item.py / business_profile.py / payment_account.py |
| GE | backend/internal/email/email.go |
| GN | backend/internal/invoices/notify.go |
| GC | backend/internal/platform/config.go |
| GM | backend/cmd/api/main.go |
| KS | gokit `sched/sched.go` at tag v0.2.0 (identical at HEAD; read from D:/Development/gokit) |
| PN | backend/PORT-NOTES.md |
| DDL | backend/migrations/00001_baseline.sql |
| SPEC | docs/superpowers/specs/2026-11-08-go-backend-design.md |
| P7 | docs/superpowers/plans/2026-11-09-go-backend-07-payments-recon.md |

Global facts:
- Auth: all four routes are admin-only via `Depends(current_admin)` (DEPS:18-34: 401 `"not authenticated"`, 401 `"session expired"`, 403 `"admin required"`). The PDF routes are opened by the browser with `window.open` (frontend/src/views/InvoiceFormView.vue:467, QuotationDetailView.vue:134, QuotationFormView.vue:267), so they authenticate by the session **cookie** alone and an auth failure shows the raw error body in a tab.
- Validation errors are FastAPI's default 422. The send bodies are required (`payload: SendInvoiceRequest`, RI:723; `payload: SendQuotationRequest`, RQ:189): no body -> 422.
- Business profile is always `db.get(BusinessProfile, 1)` (RI:710, :744; RQ:176, :201); it may be `None` (row missing) and every renderer copes (`getattr(business, ..., None)`).
- One PDF renderer is chosen at runtime: `settings.pdf_engine` (`PDF_ENGINE`, default `"html"`, CFG:83). `"html"` = Jinja2 template (TPL) through WeasyPrint (PE:18-19); **any exception** falls back to the reportlab renderer, logged as `"HTML invoice render failed; falling back to reportlab"` (PF:25-32; same for quotation PF:35-42 and receipt PF:45-52). `"reportlab"` forces the old renderer. SPEC "Documents": Maroto v2 replaces both, "The reportlab fallback goes; there is one renderer", layout is "A clean design in the spirit of today's; not a pixel copy". **The HTML/WeasyPrint design (CTX + TPL) is the one to reproduce**; reportlab (IP/QP/RCP render functions) is only described where tests touch it or where it shows fields HTML drops (§5).
- Decimal display rounding: every `quantize` in the formatters runs in Python's default context, **ROUND_HALF_EVEN** (IP:82, :96, :131-132; CTX:188). Go must use banker's rounding (`RoundBank`) for display, not `Round`.
- Timestamps written here are naive UTC: `datetime.utcnow()` (RQ:235-236) and `datetime.now(UTC).replace(tzinfo=None, microsecond=0)` (H:30-31, **seconds precision**).
- Scheduled jobs use **server-local** `date.today()` (REC:48, UL:28, HE:18); the container runs UTC, the Go port already takes the UTC date (backend/internal/invoices/scan.go:27, usage.go:97-98).
- No route in scope takes a row lock. Sending re-reads the document after the email (RI:774-778, RQ:233-239).

---

## 0. Storage and response shapes

### business_profile (DDL:51-65; MBP:13-40)
id integer (CHECK id = 1, DDL:64); name varchar(255); address text; contact_email varchar(255); contact_phone varchar(50); invoice_title varchar(255); invoice_summary text; logo_url text; updated_at; default_notes text; npwp varchar(32); npwp_name varchar(255). All nullable. `payment_accounts` relationship ordered by `PaymentAccount.currency` (MBP:35-40).

### payment_account (DDL:348-355; MPA)
id integer; business_profile_id integer; currency varchar(3); instructions text NOT NULL; is_default boolean DEFAULT false; updated_at. Go already reads them: `business.PaymentAccounts` (backend/internal/business/api.go:93), query `ListPaymentAccounts` (backend/internal/business/queries.sql:24).

### cloudflare_targets (DDL:119-134; MCT:15-54)
target_id uuid; item_id uuid UNIQUE (FK items ON DELETE CASCADE, MCT:21-26); zone_id varchar(100); record_id varchar(100); record_name varchar(255); record_type varchar(20); live_content text; maintenance_content text; proxied boolean DEFAULT true; provider_status varchar(20) DEFAULT 'ACTIVE' (values written: `ACTIVE`, `SUSPENDED`, `ERROR`); last_action_at timestamp NULL; last_error text NULL; created_at; updated_at (ORM `onupdate=current_timestamp`, MCT:44-48: **Go must set updated_at itself on every write**).

### items hosting columns (DDL:281-288; MIT:40-53)
is_hosting boolean DEFAULT false NOT NULL; hosting_domain varchar(255); hosting_grace_days integer; hosting_suspension_enabled boolean; hosting_status varchar(20) (`ACTIVE`, `SUSPEND_PENDING`, `SUSPENDED`, `CANCELLED`, H:22; schema Literal app/schemas/item.py:9); hosting_last_paid_at timestamp; hosting_last_action_at timestamp; hosting_last_error text. **hosting_last_action_at and hosting_last_error are never written by any Python service** (only listed in schemas, app/schemas/item.py:112-113); the action/error stamps go on the cloudflare target.

### quotations.last_sent_at
timestamp without time zone NULL (app/models/quotation.py:48), set by send (RQ:235-236) and mark-sent (QS:212). Invoices have no last_sent_at.

### SendInvoiceRequest (SI:126-134) / SendQuotationRequest (SQ:72-78), identical fields
| field | type | default | notes |
|---|---|---|---|
| to_email | str\|null | None | **no email validation**; empty string falls back to the customer |
| cc_email | `CcEmails` | None | CEM:31-38: split on `[,;\s]+` (CEM:15), each validated as EmailStr, case-insensitive de-dup keeping first spelling, joined `", "`, blank -> null; invalid -> 422 `"invalid email address: {addr}"` (CEM:25). P7 Task 9 adds `platform.ParseCcEmails` for exactly this. |
| subject | str\|null | None | falsy (null or `""`) -> default subject |
| message | str\|null | None | falsy -> default body |

Responses: `POST .../send` returns `InvoiceOut` / `QuotationOut` (status 200, no status_code), the same shapes the Go invoices and quotations packages already emit.

### Content-Disposition
`inline; filename="{filename}"`, filename `invoice-{invoice_number or str(invoice_id)[:8]}.pdf` (RI:712, :716) and `quotation-{quotation_number or str(quotation_id)[:8]}.pdf` (RQ:178, :182). `str(uuid)[:8]` is the first 8 lowercase hex characters (before the first dash). The name is not escaped or quoted further (§5). The same filename is the email attachment name (RI:746, RQ:210). SPEC:239-243 carries this exactly.

---

## 1. Routes (4)

### GET /api/v1/invoices/{invoice_id}/pdf -> 200 application/pdf (RI:699-717)
1. 404 `"invoice not found"` (RI:706-708). No status check: DRAFT, VOID and **recurring templates** render.
2. customer = `db.get(Customer, invoice.customer_id)` if customer_id else None (RI:709); business id 1 (RI:710).
3. `render_invoice_pdf(invoice, customer, business)` (PF:25-32) -> bytes; `Response(media_type="application/pdf", headers={"Content-Disposition": ...})` (RI:713-717). No writes.
4. A render failure in both engines is an unhandled 500.

### POST /api/v1/invoices/{invoice_id}/send -> 200 InvoiceOut (RI:720-778)
Errors in order:
1. 404 `"invoice not found"` (RI:731-733).
2. 400 `"cannot send a voided invoice"` when status == `"VOID"` (RI:734-735). Every other status is sendable: DRAFT, OPEN, SENT, PARTIAL, PAID, AWAITING_FINALIZATION, and templates.
3. 400 `"no recipient: provide to_email or set a contact_email on the customer"` when `payload.to_email or customer.contact_email` is falsy (RI:737-742).
4. Email failure: `send_email` raises and nothing catches it -> **500** (RI:766-772), nothing committed. SPEC "Email": becomes a 502 problem body in Go (a stated change; the receipt route already uses `"failed to send receipt email"`; pick `"failed to send invoice email"` and record it in PN).

Message:
- subject: `payload.subject or (f"Invoice {invoice.invoice_number}" if invoice.invoice_number else "Invoice")` (RI:747-749).
- default body (RI:750-763), built line by line:
  - `f"Hi {greeting_name},\n\n"` with greeting_name = `customer.name if customer else "there"`;
  - `f"Please find attached invoice {invoice.invoice_number or ''} for {_fmt_amount(invoice.amount, invoice.currency)}.\n"` (no number -> two spaces: `"invoice  for"`);
  - if `invoice.wht_type != "NONE"`: `"Amount to transfer (after withholding tax): " + _fmt_amount(expected_cash(invoice), invoice.currency) + ".\n"` (`expected_cash` = `max(0, balance_due - (wht_amount - wht_booked))`, W:53-56);
  - if `invoice.due_date`: `f"Due date: {invoice.due_date}.\n"` (ISO `YYYY-MM-DD`);
  - `"\nThank you."` (no trailing newline).
  - body_text = `payload.message or default_body` (RI:764). Text only, no HTML.
- `_fmt_amount(v, cur)` (IP:78-84): `None` -> `"—"`; else quantize to 0 places for zero-decimal currencies (`IDR, JPY, KRW, VND, CLP, ISK, HUF, PYG, RWF, UGX, XAF, XOF, XPF`, IP:68-70, compared upper-cased IP:75) or 2 places otherwise, half-even, grouped with `,`, then `" {currency}"` (currency as stored, not upper-cased). Tested: `"for 10,204,082 IDR."` and `"Amount to transfer (after withholding tax): 10,000,000 IDR."` (tests/integration/test_record_payment_withholding.py:152-153). P7 Task 9 ports this as `FormatAmount`; reuse it.
- `send_email(to_email=..., cc_email=payload.cc_email, subject, body_text, attachments=[(filename, pdf_bytes, "application/pdf")])` (RI:766-772).

Side effects after a successful send (RI:774-778): if status in (`"DRAFT"`, `"OPEN"`) -> `"SENT"`; commit; refresh; return. No last_sent_at, no log row, no number allocation, no finalize checks (§5). The PDF is rendered **before** the status change, so a DRAFT's attachment shows the Draft badge.

### GET /api/v1/quotations/{quotation_id}/pdf -> 200 application/pdf (RQ:166-183)
404 `"quotation not found"` (RQ:172-174). Otherwise as the invoice PDF: customer if customer_id, business id 1, `render_quotation_pdf(q, customer, business)` (PF:35-42), `inline` disposition. No status check (VOID, INVOICED render).

### POST /api/v1/quotations/{quotation_id}/send -> 200 QuotationOut (RQ:186-239)
Errors in order:
1. 404 `"quotation not found"` (RQ:193-195).
2. 400 `f"cannot send a {q.status} quotation"` when status in (`"VOID"`, `"INVOICED"`) (RQ:196-199): `"cannot send a VOID quotation"`, `"cannot send a INVOICED quotation"`. ACCEPTED, DECLINED, EXPIRED, SENT, DRAFT are sendable.
3. 400 `"no recipient: provide to_email or set a contact_email on the customer"` (RQ:202-207). (Business is loaded before this check, RQ:201; no effect.)
4. Email failure -> unhandled 500, nothing committed (RQ:225-231). Go: 502 (SPEC), e.g. `"failed to send quotation email"`.

Message:
- subject: `payload.subject or (f"Quotation {q.quotation_number}" if q.quotation_number else "Quotation")` (RQ:211-213).
- default body (RQ:214-222): `f"Hi {greeting},\n\n"` (greeting = `customer.name if customer else "there"`); `f"Please find attached our quotation {q.quotation_number or ''} for {q.amount} {q.currency}.\n"` — **`q.amount` is `str(Decimal)` at DB scale**, e.g. `"for 5000.0000 SGD."` (not `_fmt_amount`; §5); if `q.valid_until`: `f"Valid until: {q.valid_until}.\n"` (ISO); then `"\nThank you."`. body_text = `payload.message or default_body` (RQ:223). Go can print it with `platform.PyDecimalStr` (backend/internal/platform/pyformat.go:85) if kept.
- `send_email(to_email, cc_email=payload.cc_email, subject, body_text, attachments=[(filename, pdf, "application/pdf")])` (RQ:225-231).

Side effects (RQ:233-239): DRAFT -> SENT (other statuses unchanged); `last_sent_at = datetime.utcnow()` **always**, also when ACCEPTED/DECLINED/SENT; commit; refresh. Go quotations already has `MarkQuotationSent(QuotationID, LastSentAt)` (backend/internal/quotations/lifecycle.go:66-72) but that query sets status SENT unconditionally (mark-sent only allows DRAFT/SENT); send needs "SENT only if DRAFT, last_sent_at always".

### Out of this plan but sharing the renderer/mailer
- `POST /api/v1/invoices/{id}/payments/{pid}/send-receipt` (RI:636-696): plan 7 Task 9 (P7:456-494) ports it with `ReceiptRenderer` + `StubRenderer` and `email.Message{Cc, Attachments}`. Plan 9 supplies the real `ReceiptRenderer` (receipt layout §2.4) and the Resend `email.Sender`.
- `POST /api/v1/invoices/recurring/{template_id}/trigger` (RI:365-388) and the scheduled scan call the draft digest (§2.6); Go route and `Scan` already call `DraftNotifier` (GN:24-26, :39-50).
- `POST /api/v1/accounting/fx-rates/sync` (RA:176-189): ported (backend/ROUTES.md:19).
- `POST /api/v1/auth/forgot-password` sends `send_password_reset_email` (app/api/v1/routers/auth.py:127; EM:201-218): ported in Go auth through `email.Sender` (GM:70, :118-120); it gets Resend for free when plan 9 swaps the sender.
- MCP `gated_email` (app/mcp/tools/gated_email.py:20, :87-89, :149) renders the invoice PDF and sends invoice/reminder mail: MCP plan; must reuse the same renderer and sender.

---

## 2. Service rules

### 2.1 Shared formatters (IP, CTX) — port once, golden-test against Python
| Python | Rule | Go |
|---|---|---|
| `_currency_decimals(cur)` IP:73-75 | 0 for the 13 zero-decimal codes (upper-cased compare), else 2 | new |
| `_fmt_amount(v, cur)` IP:78-84 | `"—"` for None; quantize half-even; `f"{q:,.{dp}f} {cur}"` | P7 `FormatAmount` |
| `_fmt_num(v, cur=None)` IP:87-97 | same without the currency suffix; **`cur=None` -> 2 places** | new |
| `_fmt_qty(v)` IP:100-106 | `"—"` for None; `normalize()` then `:f` -> `6.0000`->`"6"`, `6.5000`->`"6.5"`, `600.0000`->`"600"` | new |
| `_fmt_pct(v)` IP:109-114 | `f"{v:f}"`, strip trailing zeros then a trailing `.`: `5.0000`->`"5"`, `7.5000`->`"7.5"`, `10`->`"10"` | new |
| `_discount_amount(type, value, subtotal)` IP:117-132 | None when type or value falsy (value 0 -> None); PERCENT -> `(subtotal or 0) * value / 100` quantize 0.01 half-even; else `value` quantize 0.01 | new |
| `_npwp_line(invoice, business)` IP:218-230 | None unless `(wht_type or "NONE") != "NONE"` and `business.npwp.strip()` non-empty; `f"NPWP {npwp} a.n. {name}"` with stripped npwp_name, or `f"NPWP {npwp}"` | new |
| `_fmt_date(d)` CTX:39-40 | `d.strftime("%d %b %Y")` (`"23 Jul 2026"`, English month abbreviations, zero-padded day) or `"—"` | new |
| `status_badge_class(s)` CTX:20-32 | upper-cased lookup: DRAFT draft, OPEN sent, SENT sent, PAID paid, PARTIALLY_PAID partial, PARTIAL partial, AWAITING_FINALIZATION partial, OVERDUE overdue, VOID draft, ACCEPTED paid, DECLINED overdue, EXPIRED overdue, INVOICED sent; unknown -> `"sent"` | new |
| `_status_label(s)` CTX:35-36 | `(s or "—").replace("_", " ").title()`: `"Partially Paid"`, `"Awaiting Finalization"`, `"Draft"` | new |
| `WHT_LABELS` W:14 | `{"PPH21": "PPh 21", "PPH23": "PPh 23"}` | backend/internal/withholding |
| `expected_cash(inv)` W:53-56 | `max(0, balance_due - (wht_amount - wht_booked))` | `withholding.ExpectedCash` (withholding.go:59) |

Minus signs: the HTML context prefixes **U+2212 `−`** (CTX:118, :201, :205, :208); the reportlab renderer and email bodies use ASCII `-` (IP:455, :465). A negative value would print `−-1,234.00` (no guard).

### 2.2 Common document data (`_common`, CTX:141-167) — invoice and quotation
| key | value |
|---|---|
| logo | `resolve_logo(business)` (§2.5) |
| status_label / status_class | §2.1 from `doc.status` |
| from_name | `business.name or "—"` |
| from_lines | `_business_lines` (CTX:85-99): non-blank lines of `business.address` (split on newlines, blank lines dropped), then one line `"{contact_email} · {contact_phone}"` joining whichever is set with `" · "` (U+00B7 with spaces). **NPWP is not here.** |
| to_name | `customer.name or "—"` (no customer -> `"—"`) |
| to_lines | `_customer_lines` (CTX:72-82): `_addr_lines` (CTX:43-69): billing_address1, billing_address2, `"{state}, {postal_code}"` (present parts joined `", "`), `"{city}, {country}"`; if none of those and `billing_address` set, its `splitlines()` (blank lines **kept**); then the `"email · phone"` contact line |
| items | per line item, ordered by `position` (app/models/invoice.py:75-78, app/models/quotation.py:58-61): desc = description (raw, may contain `\n`, rendered `white-space:pre-line`), qty `_fmt_qty`, rate `_fmt_num(unit_price, cur)`, amount `_fmt_num(amount, cur)`. The row index printed is `"%02d"` of the loop index (TPL:188), **not** `position`. |
| currency | doc.currency |
| payment_instructions | `resolve_payment_instructions(business.payment_accounts, currency)` (CTX:123-138): first account whose `currency == doc.currency` (exact, case-sensitive), else first `is_default`, else None; accounts in currency order |
| npwp | None (invoice overrides) |
| notes | `doc.notes or business.default_notes` |
| terms | `doc.payment_terms` (invoice overrides to None) |
| foot_name | `business.name or ""` |
| foot_meta | `[contact_email, contact_phone]` present ones |

### 2.3 Invoice (`build_invoice_context`, CTX:170-224)
- npwp = `_npwp_line(invoice, business)` (CTX:172).
- doc_title = `(business.invoice_title or "INVOICE").upper()` (CTX:210); ref = `invoice_number or "—"`; meta = `("Invoice No.", number or "—")`, `("Issue Date", _fmt_date(issue_date))`, `("Due Date", _fmt_date(due_date))` (CTX:215-219). **terms forced None** (CTX:211-213: payment_terms is "Net N", not customer-facing). po_so_number, invoice_summary and footer are **not shown** (§5).
- paid (CTX:179-188): if amount and balance_due not None: `p = amount - balance_due - (wht_booked or 0)`; shown only if `p > 0`, quantized to 0.01 half-even.
- summary rows (label, value, cls), in order:
  1. `("Subtotal", _fmt_num(subtotal, cur), "subtotal")` — always (subtotal None -> `"—"`).
  2. discount, if `_discount_amount` not None: label `"Discount"` + `" ({_fmt_pct(value)}%)"` when PERCENT; value `"−" + _fmt_num(disc, cur)`; cls `discount` (CTX:111-120).
  3. If `wht_type != "NONE"` (CTX:178, no None guard):
     - if `wht_gross_up`: `(f"{WHT_LABELS[t]} ({rate}%)", _fmt_num(amount - wht_base, cur), "grossup")`;
     - `("Total", _fmt_num(amount), "total")`;
     - `(f"{label} withholding ({rate}%)", "−" + _fmt_num(wht_amount), "wht")`;
     - `("Paid", "−" + _fmt_num(paid), "paid")` if paid.
  4. Else if paid: `("Total", ...)`, `("Paid", ...)`. **With no withholding and nothing paid there is no Total row** (test_invoice_context_no_payments_omits_paid_and_total).
- hero: label `"Amount to Transfer"` with withholding else `"Total Due"`; amount `_fmt_num(expected_cash(inv) if wht else balance_due, cur)`; the template appends the currency code (TPL:214).
- Goldens from tests/unit/test_pdf_context.py: SGD 11,640 subtotal, 5% -> `Subtotal "11,640.00"`, `Discount (5%) "−582.00"`, `Total "11,058.00"`, `Paid "−5,000.00"`, hero `"6,058.00"` (:65-80); IDR gross-up PPh 23 2% -> `[("Subtotal","10,000,000"), ("PPh 23 (2%)","204,082"), ("Total","10,204,082"), ("PPh 23 withholding (2%)","−204,082")]`, hero `"Amount to Transfer"` `"10,000,000"` (:94-111); no gross-up hero `"9,800,000"` (:114-129); partial `Paid "−4,000,000"`, hero `"5,800,000"` (:132-146); paid gross-up with booked PPh `Paid "−10,000,000"`, hero `"0"` (:160-171).

### 2.4 Quotation and receipt
Quotation (`build_quotation_context`, CTX:299-320): `_common` plus doc_title `"QUOTATION"` (business.invoice_title ignored); ref `quotation_number or "—"`; meta `Quote No.`, `Issue Date`, `Valid Until` (`_fmt_date`); summary rows Subtotal and the discount row only (no withholding, no paid); hero `"Total"` = `_fmt_num(amount, cur)`; terms = payment_terms (shown, unlike the invoice); payment_instructions per currency; npwp None.

Receipt (`build_receipt_context`, CTX:227-296), the data `ReceiptRenderer` needs:
- doc_title `"RECEIPT"`; ref `receipt_reference(payment)` = `f"RCT-{payment.payment_id.hex[:8].upper()}"` (RCP:31-33); status label `"Received"`, class `paid`.
- from_label `"Received By"`, to_label `"Received From"` (invoice/quotation default `"Bill From"` / `"Bill To"`, TPL:168, :173).
- to_name `customer.name or payment.payer_name or "—"`; to_lines as §2.2 (with phone, test_receipt_received_from_includes_customer_phone).
- simple table (no Qty/Rate columns, TPL:182, :190-191), one row: desc `f"Payment for invoice {invoice_number}"` or `"Payment received"`; sub = `_method_label(payment)` (RCP:44-47: `"Manual entry"` for MANUAL, else `intake_source.replace("_"," ").title()`: `"Webhook"`, `"Email Parser"`) plus `" · Ref: {payer_reference}"` when set; amount `_fmt_num(payment.amount, payment.currency)`.
- currency (column header and hero suffix) = **payment.currency**.
- meta `Receipt No.`, `Payment Date` (`_fmt_date`), `Invoice No.` (`invoice_number or "—"`).
- summary rows `("Invoice total", _fmt_num(invoice.amount, invoice.currency), "subtotal")`, `("Remaining balance", _fmt_num(balance_due or 0, invoice.currency), "total")` — formatted with **invoice.currency** places though labelled in the payment currency (§5).
- hero `"Amount Received"` = `_fmt_num(payment.amount, payment.currency)`; payment_instructions None; npwp None; notes `f"{balance_note(invoice)}\nThank you for your payment."` (`balance_note`, RCP:36-41: `"This invoice is now paid in full."` when balance <= 0 else `f"Remaining balance: {_fmt_amount(balance, invoice.currency)}."`); terms None; footer as §2.2.

### 2.5 Layout spec for Maroto (from TPL; colours, sizes in CSS px at 96 dpi, A4, `@page margin:0` TPL:104)
Fonts: Inter (body), Manrope (display: title, labels, badge, table header, footer brand), JetBrains Mono (figures, ref no., meta values, footer meta) — vendored variable woff2 in app/services/pdf/assets/fonts/ (PA:16-20). SPEC: embed TTF instead.
Palette (TPL:8-16): navy `#071A3D`, blue `#145BFF`, azure `#008FF5`, cyan `#00D9F5`, bg `#F7FAFC`, soft `#EAF1F8`, ink `#14213D`, muted `#64748B`, border `#D7E1EC`, paid `#0DBF9B`, overdue `#EF5B5B`, draft `#94A3B8`; gradient 135deg `#145BFF` -> `#008FF5` -> `#00D9F5`.

Sections, top to bottom:
1. **Header band, repeated on every page** (TPL:144-155, :23-34): navy fill, 4px gradient strip at its top edge, padding 20px 40px. Left: logo, height 76px, width auto (TPL:28, :147). Right-aligned: doc_title (Manrope 800, 32px, letter-spacing .14em, white, TPL:30-31); below it the ref (mono 12.5px `#AFC6E8`) and the status pill (TPL:35-44, :151: dot + label, Manrope 700 11px upper-case, colours per class: draft `#CBD5E1`, sent `#7FB2FF`, paid `#0DBF9B`, partial `#00D9F5`, overdue `#FF8A8A`, each on a 14-16% tint of its hue). 16px gap under the header on every page (TPL:121).
2. **Meta strip** (TPL:50-55, :161-165): 3 equal cells in one rounded box (12px radius, border `#D7E1EC`); key upper-case 10.5px muted weight 600; value mono 14px ink.
3. **Parties** (TPL:56-60, :166-177): two equal boxes, 16px gap. Label (Manrope 700 11px upper-case muted: `Bill From`/`Bill To` or receipt labels), name (Manrope 700 16px navy), lines 13px muted. The "to" box has a faint vertical gradient `#FBFDFF` -> `#F4F8FD`.
4. **Items table** (TPL:61-72, :178-197): header row navy fill, Manrope 700 10.5px upper-case `#C9D8EC`; columns `#` (mono 12px muted, 34px), `Description` (600 ink, optional sub-line 12px muted), `Qty`, `Rate` (mono 13px `#334155`, right), `Amount · {CUR}` (mono 13px 600 navy, right). Body 13.5px, rows separated by 1px border, even rows `#FBFCFE`. Rows never split across pages; the header row repeats on each page (TPL:128-132).
5. **Lower row** (TPL:73-91, :198-217): left, only if payment_instructions or npwp: "pay" panel (fill soft `#EAF1F8`, border `#CBDAEC`) with label `Payment Instructions` + the text (pre-wrap, 12.5px `#475569`) and the NPWP line under a 1px rule; right, fixed 340px summary box: rows label muted / value mono 13px; value colours: discount and grossup paid-green `#0DBF9B`, wht red `#EF5B5B`, paid azure `#008FF5`, total bold ink; then the **hero band** (gradient fill, white): label Manrope 700 11px upper-case, amount mono 600 22px right-aligned with the currency code (Manrope 700 12px) after it.
6. **Notes / Terms** (TPL:218-223): side-by-side panels when both, each `section-label` + pre-wrap text. Invoices never have Terms (§2.3).
7. **Footer band, pinned to the bottom of every page** (TPL:99-103, :127, :227-230): navy, 50px tall, padding 13px 40px; left foot_name (Manrope 700 white), right foot_meta items (mono 11.5px `#9FB4D6`, 14px apart). Reserved space so content never runs under it (TPL:113, :156-158).
8. PDF title metadata `"{doc_title|title} {ref}"` (TPL:5), e.g. `"Invoice WH20260042"`, `"Receipt RCT-1A2B3C4D"`.
No page numbers in the HTML design (reportlab printed `Invoice {number}` / `Page N`, IP:272-277).

### 2.6 Logo (PL:19-35)
`business.logo_url` falsy -> bundled default PNG (PA:37-40, app/services/pdf/assets/default-logo.png). `data:` -> used as is. `http(s)://` -> `httpx.get(url, timeout=5.0)`, `raise_for_status`, mime = content-type before `;`, **non-`image/` mime is relabelled `image/png`** (PL:29-31), any exception -> logged warning `"failed to fetch logo %s; using default"` and default. Anything else -> default. Fetched on **every** render, no cache, no size cap. SPEC "Documents": decode data URIs; fetch with 5 s timeout and 2 MB cap; sniff the type from the bytes; failure -> default logo.

### 2.7 Email (EM)
`send_email(*, to_email, subject, body_text, body_html=None, cc_email=None, attachments=None)` (EM:82-149):
1. `cc_emails = split_emails(cc_email)` (CEM:11-16; `"a, b"` -> `["a","b"]`).
2. If `RESEND_API_KEY` set -> `_send_via_resend` (EM:23-79): POST `{RESEND_BASE_URL}/emails` (default `https://api.resend.com`), header `Authorization: Bearer {key}`, timeout `RESEND_TIMEOUT_SECONDS` (15). JSON: `from` = `f"{RESEND_FROM_NAME} <{RESEND_FROM_EMAIL}>"` (default `"Wenhao Dev Billing <noreply@wenhao.id>"`), `to` = `[to_email]`, `subject`, `text`; `html` only if given; `cc` list only if non-empty; `reply_to` = `SMTP_REPLY_TO` if set (default `"wenhao.kho@gmail.com"`); `attachments` = `[{"filename", "content": base64}]` only if given (mime not sent, EM:48-49). Status >= 400 -> log error `"Resend rejected email to %s: %s %s"` (to, status, body) then raise; any exception logged `"failed to send email via Resend to %s"` and re-raised (EM:65-77). Success -> info `"sent email via Resend to=%s subject=%r"`. No idempotency key.
3. Else if `SMTP_HOST` unset -> info `"[email disabled] would send to=%s cc=%s subject=%r attachments=%s body=%s"` with attachment filenames and **the full body** (EM:111-120); returns. Go's `LogSender` already logs this sentence without body outside local (GE:32-44); P7 adds CC and attachment names.
4. Else SMTP (EM:122-149): dropped in Go (SPEC "Email").
`build_recurring_drafts_email(drafts)` (EM:152-193): `n = len(drafts)`, plural `"s"` unless 1. Subject `f"{n} draft invoice{s} awaiting your approval"`. Text = lines `"Hi,"`, `""`, intro `f"The recurring billing scan generated {n} draft invoice{s} that need your review and approval:"`, `""`, then per draft `f"- {invoice_number} · {customer_name} · {currency} {amount} · cycle {cycle_date}\n  {link}"`, then `""`, outro `"Each invoice stays in the Awaiting Finalization queue until you approve and issue it."`, joined `"\n"` plus a trailing `"\n"`. HTML = `f"<p>Hi,</p><p>{intro}</p><ul>{rows}</ul><p>{outro}</p>"`, row `f"<li><a href=\"{link}\">{invoice_number}</a> — {customer_name} — {currency} {amount} — cycle {cycle_date}</li>"` (em dashes; **no HTML escaping** of names).
Digest data (DN:25-35): invoice_number; customer_name = customer.name or `"(unknown customer)"`; **amount `f"{invoice.amount:,.2f}"` (2 places always, IDR included)**; currency; cycle_date = cycle key; link `f"{APP_BASE_URL.rstrip('/')}/invoices/{invoice_id}/edit"`. Recipients: **every row of users** (`select(User.email)`, DN:38-39; no role or active filter). One `send_recurring_drafts_email` per recipient; each failure logged `"failed to notify %s about %d new drafts"` and the loop continues (DN:42-52); nothing when there are no drafts (DN:46-47, :56-57). Sent after commit (REC:70-71, RI:386-387). Go: `DraftSummary` already has InvoiceID, InvoiceNumber, CustomerName *string, Currency, Amount, CycleDate (GN:13-20); plan 9 adds an `EmailNotifier` implementing `DraftNotifier` (list users, build subject/text/html, link from `cfg.AppBaseURL`) and swaps it for `LogNotifier` at GM:79.

### 2.8 Cloudflare (CF)
- `_headers()` (CF:15-22): `CLOUDFLARE_API_TOKEN` unset -> `CloudflareError("cloudflare_api_token is not configured")`; else `Authorization: Bearer {token}`, `Content-Type: application/json`.
- `_update_target(target, content)` (CF:37-54): `PATCH {CLOUDFLARE_API_BASE_URL}/zones/{zone_id}/dns_records/{record_id}` (default base `https://api.cloudflare.com/client/v4`), timeout `CLOUDFLARE_TIMEOUT_SECONDS` (15), JSON `{"type": record_type, "name": record_name, "content": content, "proxied": proxied}`. `raise_for_status()` first (httpx's text, e.g. `"Server error '500 ...' for url '...'"`), then `body.success` false -> `CloudflareError(_parse_error(body))` (CF:25-34: no errors -> `"unknown Cloudflare error"`; first error dict -> `f"{code}: {message}"` or message (default `"Cloudflare request failed"`); non-dict -> `str(first)`).
- `suspend_target` -> content = maintenance_content; `restore_target` -> live_content (CF:57-62).

### 2.9 Hosting (H)
- `resolve_overdue_invoice(item, today)` (H:97-113): invoices joined through `invoice_line_items.item_id = item`, `is_template` false, status in (`OPEN`, `SENT`, `PARTIAL`) (H:23), `balance_due > 0` (H:86-94); ordered `due_date ASC NULLS LAST, created_at ASC`; first with `due_date + hosting_grace_days (None->0) < today` (strict). An invoice with several lines for the item appears once per line (join, no DISTINCT; harmless).
- `suspend_hosting(item, invoice)` (H:124-151): returns False if `not hosting_suspension_enabled` or status CANCELLED; HostingError `"hosting item is missing a Cloudflare target"` if no target; False if item SUSPENDED **and** target SUSPENDED. Else item `SUSPEND_PENDING`, target.last_error None, flush; call suspend; on exception: target provider_status `ERROR`, last_error `str(exc)`, last_action_at now, flush, **re-raise** (item stays SUSPEND_PENDING). On success: item SUSPENDED, target SUSPENDED, last_action_at now, last_error None; True. The `invoice` argument is unused.
- `restore_hosting_if_eligible(item)` (H:154-178): False if no target, if any open invoice with balance (`_has_open_balance`, H:116-121, **any** open invoice, not only overdue), or if item ACTIVE and target ACTIVE. Else call restore; on exception mark target ERROR/last_error/last_action_at and re-raise; on success item ACTIVE, target ACTIVE, stamps. **No check of hosting_suspension_enabled or CANCELLED**.
- `ensure_hosting_restored(invoice)` (H:181-193): returns False if `balance_due > 0`; for each hosting item on the invoice's lines (H:54-58, `ln.item.is_hosting`): `hosting_last_paid_at = utcnow` then `restore_hosting_if_eligible`; flush; returns whether any restored. Exceptions propagate to the caller's transaction. Callers: record-payment (RI:575), auto-clear (app/services/reconciliation.py:161), manual approval (app/services/reconciliation.py:201). P7 Task 5 defines `HostingRestorer{RestoreIfPaid(ctx, tx, invoiceID) error}` with `NoHosting{}` (P7:269-274); plan 9 supplies the real one.
- `apply_generated_invoice_hosting_fields` (H:61-83): already ported (`invoices` stampCoverage, backend/internal/invoices/trigger.go:190; TESTS.md:52).

### 2.10 Scheduler and jobs (SCH)
Python: APScheduler `BackgroundScheduler(timezone="UTC", coalesce=True, max_instances=1, misfire_grace_time=3600)` (SCH:58-61), started by the FastAPI lifespan when `SCHEDULER_ENABLED` (default **true**, CFG:28; MAIN:43-60), in-memory job store (SCH:56-57: missed runs while down are skipped). Each run: `key = zlib.crc32(job_id.encode())` (unsigned 32-bit, passed as bigint), `pg_try_advisory_lock(key)` on its own connection; not acquired -> info `"job %s skipped: already running in another process"`; result logged `"job %s finished: %r"`; exception logged `"job %s failed"` (Sentry gets it via ERROR logs, CFG:103-104) and swallowed; always `pg_advisory_unlock` (SCH:40-52).

| job id | cron (UTC) | function | lock key | what | Go today |
|---|---|---|---|---|---|
| recurring-scan-and-generate | daily 02:00 (SCH:32) | `scan_and_generate` (REC:47-72) | 3587987709 | every RECURRING template (`is_template`), not paused, valid schedule, cycle containing today -> `trigger_recurring_cycle` (idempotent per template+cycle key); one commit for the whole run; digest of drafts that did not exist before (REC:61-68); returns count incl. replays | `invoices.Scan(ctx, pool, today, notify)` (scan.go:28): per-trigger transactions (port note), done |
| usage-lock-cutoff-scan | daily 03:00 (SCH:33) | `cutoff_scan` (UL:27-52) | 2413487788 | DRAFT USAGE invoices, not locked, `int(cut_off_day) == yesterday.day` -> `lock_usage_invoice(accrued, cutoff=yesterday)`; accrual hook is `_stub_accrued`, which **raises NotImplementedError** (UL:23-24), so any matching invoice fails the whole run (one transaction) | `invoices.CutoffScan(ctx, pool, today, accrued AccrualSource)` (usage.go:89, :99): done; plan 9 passes a source that fails like Python (SPEC "Best-practice changes": kept, port note) |
| hosting-overdue-enforcement | daily 04:00 (SCH:34) | `daily_hosting_enforcement` (HE:43-47) | 1601569364 | items `is_hosting` and `hosting_suspension_enabled` and status in (ACTIVE, SUSPEND_PENDING, SUSPENDED), oldest first (HE:20-28); per item: overdue invoice -> `suspend_hosting` (count suspended if True) and `continue`; else `restore_hosting_if_eligible` (count restored); any exception -> `errors += 1` and continue (HE:29-39), **no rollback**: ERROR/SUSPEND_PENDING stamps flushed before the raise are committed (HE:46); returns `{"suspended","restored","errors"}` | missing |
| fx-rates-weekly-sync | Monday 05:00 (SCH:35-36) | `sync_fx_rates` (FXS:60-66) | 1988665425 | `run_fx_sync` (FXS:20-57) then commit | `fx.Sync(ctx, pool, cfg, client)` (backend/internal/fx/sync.go:26): done, fetch-all-then-write in one tx, upsert (PN on fx); only the job wiring is missing |

Go side: nothing schedules anything yet; GM `serve` registers routes only (GM:69-82). `gokit/sched` v0.2.0 (KS) offers `Job{Name, Every time.Duration, LockKey int64, Run func(ctx) error}` (KS:16-21) and `Start(ctx, pool, report, jobs...)` (KS:26): a fixed-interval ticker per job (first run one interval after start), `pg_try_advisory_lock($1)` on an acquired pool connection, skipped silently when not acquired, unlock with `WithoutCancel`, panics turned into errors, errors to `report` (KS:47-71). It has **no cron/slot notion and no catch-up table**: plan 9 must build the SPEC "Background work" design on top: tick every minute (say), and in `Run` compute the latest due slot (daily HH:00 UTC, weekly Monday 05:00), skip if recorded in the new `scheduled_job_runs` table, skip-and-log a slot older than 24 h, else run and record. Lock keys must be the **unsigned** crc32 values above as int64 (`int64(crc32.ChecksumIEEE([]byte(id)))`), never an int32 cast, or Python and Go will not exclude each other. `scheduled_job_runs` is a new additive goose migration (SPEC "Database"). `gokit/sched` is not yet vendored (backend/vendor/github.com/LOQATO/gokit has apierr, auth, db, i18n, money, observe, testkit).

### 2.11 Config / env (CFG; Go: GC)
| Python setting (env) | default | Go today | plan 9 |
|---|---|---|---|
| scheduler_enabled (`SCHEDULER_ENABLED`) | True (CFG:28) | absent | add; SPEC: off in Go until switch-over (compose sets false); decide default |
| app_base_url (`APP_BASE_URL`) | `http://localhost:5173` (CFG:63) | `AppBaseURL` (GC:22, :42) | reuse for digest links |
| base_currency, fx_provider_base_url, fx_provider_timeout_seconds, fx_sync_currencies | IDR, `https://api.frankfurter.dev/v1`, 15, `["USD","SGD"]` (CFG:48, :86-91) | present (GC:28-31, :47-50) | none |
| resend_api_key (`RESEND_API_KEY`) | None (CFG:96) | absent | add; empty -> LogSender |
| resend_from_email | `noreply@wenhao.id` (CFG:97) | absent | add |
| resend_from_name | `Wenhao Dev Billing` (CFG:98) | absent | add |
| resend_base_url | `https://api.resend.com` (CFG:99) | absent | add (tests point it at httptest) |
| resend_timeout_seconds | 15.0 (CFG:100) | absent | add |
| smtp_reply_to (`SMTP_REPLY_TO`) | `wenhao.kho@gmail.com` (CFG:59) | absent | add (SPEC keeps the name) |
| smtp_host/port/username/password/use_tls/from_email/from_name | None/587/None/None/True/`noreply@wenhao.id`/`wenhao-billing` (CFG:52-58) | absent | dropped (SPEC) |
| cloudflare_api_token (`CLOUDFLARE_API_TOKEN`) | None (CFG:65) | absent | add |
| cloudflare_api_base_url | `https://api.cloudflare.com/client/v4` (CFG:66) | absent | add |
| cloudflare_timeout_seconds | 15.0 (CFG:67) | absent | add |
| pdf_engine (`PDF_ENGINE`) | `"html"` (CFG:83) | absent | dropped (one renderer) |
Production compose sets `APP_BASE_URL`, `RESEND_API_KEY`, `RESEND_FROM_EMAIL`, `RESEND_FROM_NAME`, `CLOUDFLARE_API_TOKEN` (docker-compose.coolify.yml:37, :47-51); no SMTP host, so production mail is Resend or logged.

---

## 3. Tests in scope

[IN] = port in plan 9. [IN~] = port with the assertion moved from Python internals (reportlab spies, WeasyPrint, HTML) to the Go equivalent (layout model / extracted PDF text / render data). [DROP] = plan 9 marks it "not ported" with the reason given. [OUT] = another plan.

tests/unit/test_email_resend.py (Resend via a fake httpx client; Go: httptest server):
- [IN~] test_resend_settings_defaults — Go config defaults (from, name, base URL, 15 s; key empty).
- [IN~] test_resend_api_key_from_env — `RESEND_API_KEY` read by config.
- [IN~] test_resend_used_when_key_set_and_smtp_untouched — URL `/emails`, `Bearer`, from `"Wenhao Dev Billing <noreply@wenhao.id>"`, `to` list, subject, text, `reply_to` default, no html/cc/attachments keys (SMTP half moot).
- [IN] test_resend_payload_includes_html_cc_and_attachments — html, `cc: ["cc@example.com"]`, attachments `[{"filename","content": base64}]`.
- [IN] test_resend_payload_splits_multiple_cc — `"a@example.com, b@example.com;c@example.com"` -> 3.
- [IN] test_resend_raises_on_error_response — 422 -> error.
- [IN] test_logging_fallback_when_nothing_configured — no key -> logged, no network.
- [IN] test_resend_error_body_is_logged — error log carries Resend's body.

tests/unit/test_recurring_drafts_email.py:
- [IN] test_subject_counts_drafts; [IN] test_text_body_lists_draft_details; [IN] test_html_body_links_invoice_number.

tests/unit/test_invoice_pdf.py (reportlab):
- [IN] test_discount_percent_computes_monetary_amount; [IN] test_discount_percent_handles_fractional_rate; [IN] test_discount_amount_type_is_the_value_itself; [IN] test_discount_absent_returns_none; [IN] test_fmt_pct_trims_trailing_zeros; [IN] test_fmt_num_groups_thousands (`_fmt_num(None) == "—"`, no currency -> 2 places).
- [IN~] test_render_full_invoice_produces_pdf (`%PDF-`, > 1000 bytes); [IN~] test_render_without_customer_or_business; [IN~] test_render_without_discount; [IN~] test_render_invoice_pdf_escapes_xml_special_chars_in_names (Go: `<&>"` in names render, text intact).
- [IN~] test_notes_fall_back_to_business_default_when_invoice_has_none — spied Paragraph texts; Go: on render data / extracted text.
- [IN~] test_render_wht_invoice_hero_is_amount_to_transfer — reportlab strings `"AMOUNT TO TRANSFER"`, `"10,000,000<"`, row `["PPh 23 withholding (2%)", "-204,082 IDR"]`; Go asserts the HTML design's `"Amount to Transfer"`, `"10,000,000"`, `"−204,082"`.
- [IN~] test_render_no_wht_invoice_hero_is_balance_due — reportlab `"BALANCE&nbsp;DUE"`; Go: hero `"Total Due"`.

tests/unit/test_pdf_context.py (pure context; Go: the render-data builder):
- [IN] test_status_badge_class_maps_known_and_unknown; [IN] test_invoice_context_discount_and_paid; [IN] test_invoice_context_no_payments_omits_paid_and_total; [IN] test_invoice_context_gross_up_rows; [IN] test_invoice_context_wht_without_gross_up; [IN] test_invoice_context_wht_with_partial_payment; [IN] test_invoice_context_paid_gross_up_excludes_booked_wht; [IN] test_invoice_context_partial_gross_up_excludes_booked_wht; [IN] test_invoice_context_no_wht_unchanged; [IN] test_quotation_context_total_hero; [IN~] test_both_builders_share_key_set (Go: one struct type for both); [IN] test_quotation_notes_fall_back_to_business_default; [IN] test_resolve_payment_instructions_matches_currency; [IN] test_resolve_payment_instructions_falls_back_to_default; [IN] test_resolve_payment_instructions_none_when_empty; [IN] test_invoice_context_selects_default_when_currency_unmatched; [IN] test_bill_to_joins_customer_email_and_phone; [IN] test_bill_to_phone_only_when_no_email; [IN] test_bill_to_email_only_when_no_phone; [IN] test_receipt_received_from_includes_customer_phone; [IN] test_invoice_context_omits_block_when_no_accounts; [IN] test_invoice_from_block_shows_npwp_when_withholding; [IN] test_invoice_from_block_omits_npwp_without_withholding; [IN] test_invoice_from_block_omits_npwp_when_unset.

tests/unit/test_pdf_render_html.py:
- [IN~] test_invoice_html_is_self_contained_and_populated — number, `"Total Due"`, item text, payment instructions in the output; fonts/logo embedded; no `http(s)://` fetch (Go: assert on extracted PDF text; fonts are embedded TTF).
- [IN~] test_quotation_html_renders_total — `"QUOTATION"`, `"Valid Until"`, `"5,000.00"`.
- [IN~] test_invoice_html_puts_npwp_in_payment_panel_when_withholding — instructions and `"NPWP 2102060605910001 a.n. Kardono Wijaya"` in the pay panel.

tests/unit/test_pdf_logo.py:
- [IN] test_passthrough_data_uri; [IN] test_none_business_uses_default; [IN] test_blank_logo_uses_default; [IN] test_http_fetch_success; [IN] test_http_fetch_failure_falls_back_to_default.
- [IN~] test_http_fetch_non_image_mime_falls_back_to_png — Python embeds HTML bytes labelled PNG; Go (SPEC: sniff bytes) falls back to the default logo instead. Record in PN.

tests/unit/test_pdf_assets.py:
- [IN~] test_font_face_css_embeds_all_families (Go: the three TTF families register); [IN] test_default_logo_is_data_uri (Go: default logo bytes embedded, PNG).

tests/unit/test_quotation_pdf.py (reportlab quotation; Go: the Maroto quotation):
- [IN~] test_render_quotation_pdf_returns_pdf_bytes; [IN~] test_render_quotation_pdf_without_customer; [IN~] test_render_quotation_pdf_escapes_xml_special_chars_in_names; [IN~] test_render_quotation_pdf_escapes_xml_special_chars_in_free_text.

tests/unit/test_receipt_pdf.py:
- [OUT] test_receipt_reference_shape; [OUT] test_balance_note_partial_payment_shows_remaining; [OUT] test_balance_note_full_payment_says_paid_in_full (plan 7 Task 9).
- [IN~] test_render_receipt_pdf_returns_pdf_bytes; [IN~] test_render_receipt_pdf_without_customer; [IN~] test_render_receipt_pdf_escapes_xml_special_chars_in_names.

tests/integration/test_invoice_internal_note.py:
- [IN~] test_internal_note_never_reaches_the_pdf — rendered output has `"Customer-facing note"` and not the internal note.

tests/integration/test_record_payment_withholding.py (send-invoice body; send_email and render patched):
- [IN] test_send_invoice_body_quotes_amount_to_transfer — `"for 10,204,082 IDR."`, `"Amount to transfer (after withholding tax): 10,000,000 IDR."`.
- [IN] test_send_invoice_body_without_wht_unchanged.

tests/integration/test_hosting.py (Cloudflare calls patched; Go: httptest Cloudflare):
- [IN] test_hosting_enforcement_suspends_and_reconciliation_restore_reactivates (needs plan 7's `ProcessIncoming`; suspend on 2026-06-12 with due 06-08 + grace 3, then a USD 240 webhook clears and restores; calls `[suspend, restore]` on `record_123`).
- [IN] test_enforcement_suspend_failure_marks_provider_error — counts `{0,0,1}`; item SUSPEND_PENDING; target ERROR, last_error `"cloudflare 500"`, last_action_at set.
- [IN] test_enforcement_is_idempotent_for_already_suspended_hosting_item — second run `{0,0,0}`, no second call, last_action_at unchanged.
- [IN] test_enforcement_ignores_non_overdue_invoice — due 06-15, run 06-12: nothing.

Plan 9 drops (no HTML engine, no fallback, no engine setting):
- [DROP] tests/unit/test_pdf_config.py::test_pdf_settings_defaults — `PDF_ENGINE` is gone.
- [DROP] tests/unit/test_pdf_facade.py::test_falls_back_to_reportlab_on_engine_error, ::test_engine_reportlab_skips_html, ::test_html_path_used_on_success — one renderer.
- [DROP] tests/unit/test_pdf_wiring.py::test_routers_use_facade — Python import wiring; Go route tests cover it.
- [DROP] tests/integration/test_pdf_engine.py::test_html_to_pdf_returns_pdf_bytes, ::test_fetcher_refuses_non_embedded_urls (3 parametrized cases), ::test_fetcher_allows_data_uris, ::test_external_reference_is_skipped_not_fatal — WeasyPrint URL fetcher; Maroto never fetches from content. The intent (no file/network reads from user content) is kept by a new Go test that a logo URL is the only fetch.

Out of scope but touching these services:
- [OUT] tests/integration/test_send_receipt.py (6) and tests/integration/test_customer_cc.py::test_send_receipt_multiple_cc_normalised, ::test_send_receipt_invalid_cc_422 — plan 7 Task 9 (stub renderer; the real receipt renderer arrives here without changing them).
- [OUT] tests/integration/test_mcp_gated_email.py (9: test_first_call_returns_preview_and_sends_nothing, test_second_call_with_token_sends, test_tampered_token_refuses, test_draft_invoice_promoted_to_sent_after_send, test_void_invoice_refuses, test_no_recipient_refuses, test_reminder_first_call_returns_preview_and_sends_nothing, test_reminder_second_call_with_token_sends, test_reminder_tampered_token_refuses) and tests/integration/test_record_payment_withholding.py::test_payment_reminder_quotes_expected_cash — MCP plan.
- Already ported: test_hosting.py::test_hosting_item_creates_with_cloudflare_target, ::test_trigger_recurring_cycle_stamps_coverage_on_hosting_invoice (TESTS.md:51-52); test_items_service.py::test_create_hosting_item_sets_active_status_and_normalizes (TESTS.md:396); test_recurring_scheduling.py::test_manual_trigger_notifies_users_once (TESTS.md:326, through `DraftNotifier`).

Counts: **73 [IN]/[IN~]**, **9 [DROP]**, 3 + 8 + 10 = 21 [OUT] listed. All 82 [IN]/[IN~]/[DROP] are `todo` rows in backend/TESTS.md: lines 53-56, 59, 281-284, 308-309, 374-394, 418-457, 462-465, 469-474 (4+1+4+2+21+40+4+6 = 82). Collected pytest cases are 84 (test_fetcher_refuses_non_embedded_urls has 3 parameters). No Python test covers: the four routes' 404/400/recipient paths or Content-Disposition (only the send-invoice body is tested), the quotation send body or last_sent_at, the scheduler (`SCHEDULER_ENABLED=false` in tests/conftest.py:30-31), the advisory lock, `run_fx_sync` (Go already has its own fx tests), `cloudflare.py` HTTP handling, `restore_hosting_if_eligible` from the enforcement job, or `ensure_hosting_restored` from record-payment. Go should add route tests for each of these, plus scheduler slot/catch-up tests against `scheduled_job_runs`.

---

## 4. Existing Go code

- **email** (GE): `Message{To, Subject, Text, HTML string}` (GE:13-15), `Sender` interface (GE:18-20), `LogSender{Logger, ShowBodies}` logging `"[email disabled] would send to=%s subject=%q"` (GE:26-44). P7 Task 9 adds `Cc []string`, `Attachments []Attachment{Filename, ContentType, Content}` and CC/attachment logging. **Gap**: a Resend `Sender` (SPEC: `resend-go`; idempotency key = document id + hash of recipients and body), chosen in GM `mailSender` (GM:118-120) when `RESEND_API_KEY` is set.
- **invoices** (GN): `DraftSummary`, `DraftNotifier`, `notifyDrafts` (best effort, `WithoutCancel`, panic-safe, GN:39-50), `LogNotifier` wired at GM:79. `Scan` (backend/internal/invoices/scan.go:28), `CutoffScan` + `AccrualSource` (backend/internal/invoices/usage.go:89, :99), `Get` (backend/internal/invoices/store.go:17), `InvoiceOut`. **Gaps**: `EmailNotifier`; the pdf and send routes; a status update "DRAFT/OPEN -> SENT"; render data loader (invoice + lines + customer + business + payment accounts).
- **quotations**: `LastSentAt` in the response (backend/internal/quotations/api.go:158), `MarkQuotationSent` query (backend/internal/quotations/lifecycle.go:66-72, sets SENT unconditionally). **Gaps**: pdf and send routes; a "send" update (SENT only from DRAFT, last_sent_at always).
- **business**: `Profile` (backend/internal/business/api.go:44), `PaymentAccounts` (:93), queries `GetProfile`, `ListPaymentAccounts` (backend/internal/business/queries.sql:1, :24). Reuse for render data; `Profile` takes `now` because the GET creates the row — the renderer must read without creating (Python `db.get` returns None).
- **withholding**: `ExpectedCash` (withholding.go:59), `Labels` = `{"PPH21": "PPh 21", "PPH23": "PPh 23"}` (withholding.go:29), same as W:14.
- **platform**: `PyDecimalStr` (pyformat.go:85) for the quotation body's raw Decimal; `NormalizeEmail` (backend/internal/platform/email.go:28) and P7's `ParseCcEmails`; `Clock` for last_sent_at/utcnow; `AppBaseURL` in config.
- **fx**: `Sync` (backend/internal/fx/sync.go:26), `NewClient` (backend/internal/fx/provider.go:37), provider with Python's error sentences (provider.go:22-33). Remaining: only the weekly job.
- **items**: `CloudflareTargetIn/Out`, `ItemOut` with hosting fields (backend/internal/items/api.go:28, :39, :58), queries `GetTarget`, `ListTargets`, `UpsertTarget` (backend/internal/items/queries.sql:46-52). **Gaps**: hosting package (overdue lookup, suspend, restore, stamps on target and item, `updated_at`), Cloudflare client with Python's error sentences, `HostingRestorer` implementation for P7's interface, enforcement job.
- **payments** (P7, not yet merged): `FormatAmount`, `ReceiptReference`, `BalanceNote`, `ReceiptRenderer` + `StubRenderer`, `HostingRestorer` + `NoHosting`. Plan 9 swaps both stand-ins in GM.
- **scheduler**: nothing in GM; `gokit/sched` v0.2.0 (KS) as described in §2.10, not vendored yet. **Gaps**: `SCHEDULER_ENABLED`, `scheduled_job_runs` migration, slot/catch-up runner, four job wirings, Sentry/log reporting.
- **PDF**: nothing. **Gaps**: Maroto v2 dependency, embedded TTF fonts (Inter, Manrope, JetBrains Mono) and default logo, logo resolver, one render-data struct for invoice/quotation/receipt, formatters of §2.1 (half-even), three renderers.
- **Config gaps**: §2.11.

---

## 5. Oddities

1. **A Cloudflare failure fails the payment.** `ensure_hosting_restored` calls Cloudflare inside the record-payment / auto-clear / approval transaction and re-raises (H:163-170, :190; RI:575; app/services/reconciliation.py:161, :201): a Cloudflare outage or a missing `CLOUDFLARE_API_TOKEN` (CF:17-18) makes recording a payment for a suspended hosting invoice a 500 and rolls the payment back. Change: stamp `hosting_last_paid_at` in the transaction, restore after commit, best effort (log + target ERROR), and let the daily job retry.
2. **Sending promotes DRAFT straight to SENT, bypassing finalize.** RI:774-775 moves DRAFT (and recurring templates, which are never checked) to SENT without finalize's checks (template refusal, zero-balance -> PAID, app/services/invoicing.py:647-662); a DRAFT with no number is mailed as `"invoice  for"` with subject `"Invoice"`. Change: refuse DRAFT and templates (400) or finalize first; owner decision. Keep for parity otherwise, port note.
3. **Email first, commit after, 500 on failure.** Invoice and quotation sends call the mailer before the status change and do not catch its error (RI:766-772, RQ:225-231) -> 500 plain text, while the receipt gives 502. Change (SPEC): 502 problem bodies; idempotency key so a retry after a timeout does not double-send.
4. **Enforcement commits half-done suspensions and restores manual suspensions.** A Cloudflare error leaves the item SUSPEND_PENDING and the target ERROR, committed (H:133-143, HE:38-46); next day it retries (SUSPEND_PENDING is in scope). `restore_hosting_if_eligible` restores any SUSPENDED item with no open invoice, even one an admin suspended by hand through `hosting_status`, and ignores `hosting_suspension_enabled`/CANCELLED on restore (H:154-178, HE:36-37). Keep the retry; change: only auto-restore what enforcement suspended (or respect CANCELLED/disabled), port note.
5. **Amount formatting differs by channel.** Quotation email prints the raw DB Decimal `"5000.0000 SGD"` (RQ:218); the draft digest always prints 2 places, `"1,500,000.00"` for IDR (DN:31); PDFs and the invoice email use 0 places for IDR (IP:68-84). Change: `FormatAmount` everywhere; port note (bodies are not compared by the frontend).
6. **The HTML invoice drops fields the reportlab fallback showed.** payment_terms (forced None, CTX:211-213), footer (IP:572-582), po_so_number (IP:325-329), invoice_summary tagline (IP:314-315) never appear in the HTML design; quotation footer and po_so too (QP:86-87, :204-206). Since any WeasyPrint error silently switches design (PF:28-32), the same invoice can render two ways. Go: one design; decide with the owner whether footer and PO/SO return (SPEC lists "notes and footer").
7. **Receipt figures mix currencies.** The receipt's column header and hero use payment.currency but "Invoice total"/"Remaining balance" use invoice.currency places with no code (CTX:233, :271-282); a cross-currency manual approval shows USD amounts under an IDR heading. Change: label each summary value with its own currency.
8. **Logo fetch trusts the server's content type and has no limits.** Non-image bodies are embedded as `image/png` (PL:29-31), which then breaks WeasyPrint and silently falls back to reportlab; no size cap; fetched on every render (PL:27). SPEC change: sniff bytes, 2 MB cap, fall back to default; consider caching per logo_url.
9. **Display rounding is half-even.** `quantize` without a rounding mode (IP:82, :96, :131-132; CTX:188): `0.125 SGD` shows `0.12`, IDR `2.5` shows `2`. Keep (Go `RoundBank`) so figures match Python; note it.
10. **Missed scheduled runs are skipped** (SCH:56-57); jobs use server-local `date.today()` (REC:48, UL:28, HE:18). Change (SPEC): catch up one slot via `scheduled_job_runs`, UTC dates.
11. **Usage lock job fails whenever it has work**: `_stub_accrued` raises NotImplementedError (UL:23-24), rolling back the whole run every night a USAGE draft hits its cut-off. Keep (SPEC lists it), port note.
12. **Advisory lock keys exceed int32.** crc32 is unsigned (SCH:41): 3587987709 and 2413487788 are above 2^31. Go must pass them as int64 unchanged, or Python and Go lock different keys and could run the same job twice during side-by-side.
13. **Digest goes to every user row** with no role/active filter (DN:38-39) and its HTML is not escaped (EM:180-186): a customer name with `<` breaks the mail. Keep recipients; escape HTML in Go.
14. **`to_email` is not validated** (SI:127-129, SQ:73) while CC is; a bad address reaches Resend and becomes a 500 (502 in Go). Change: validate with `platform.NormalizeEmail` -> 422, or keep and rely on 502; owner decision.
15. **Content-Disposition is not escaped**: a number containing `"` or non-ASCII would break the header (RI:716, RQ:182). Numbers are generated `WH{YYYY}{NNNN}`/`Q{YYYY}{NNNN}` but PATCH can set them. Change: RFC 6266 `filename*` (Go `mime.FormatMediaType`).
16. **Quotation send stamps last_sent_at for any status** and allows ACCEPTED/DECLINED/EXPIRED (RQ:196-199, :233-236); the error says `"cannot send a INVOICED quotation"` (grammar). Keep, port note.
17. **`hosting_last_paid_at` is stamped on every hosting item of a paid invoice** even when no restore happens, with seconds precision (H:30-31, :188-190); `hosting_last_action_at`/`hosting_last_error` on items are never written (§0). Keep, note.
18. **No HTML body on document emails**, and the SMTP path, `PDF_ENGINE` and the reportlab renderers go away in Go (SPEC). Record in PN as intended removals.
19. **`wht_type != "NONE"` without a None guard** in the HTML context (CTX:178) vs `(wht_type or "NONE")` elsewhere (IP:224, :458). The column is NOT NULL DEFAULT 'NONE' (DDL:254), so harmless; Go treats empty as NONE.
20. **The resolve-overdue join repeats an invoice per line** and orders by `due_date NULLS LAST, created_at` with no id tie-break (H:86-94, :103-104); only the first match matters. Keep.
