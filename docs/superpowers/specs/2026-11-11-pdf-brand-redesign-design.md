# UI revamp, part 3: invoice, receipt and quotation PDFs in the brand style — design

Date: 2026-11-11 · Status: draft for owner approval

## Why

The PDFs are what customers see. They are drawn today in navy/blue; the owner's mockup
(`docs/redesign/invoice-receipt/Invoice - Receipt.html`, two A4 pages: invoice WH20260028 and receipt RC20260028)
puts them on the wenhao.id brand — burgundy `#a51c30`, gold, cream, Manrope + DM Sans — matching the app restyle
(part 1, running in parallel in another session) and the rest of the software house's identity.

## Goals

- Invoice, receipt and quotation PDFs follow the mockup's layout and visual system.
- Everything customers rely on today stays: uploaded logo, PO/SO cell, footer text, configurable document title,
  default notes, payment instructions, NPWP, discount rows, PPh rows, foreign currencies, cross-currency receipts.
- The receipt gains the withholding rows and "Applied to" table the mockup shows, from data already stored.

## Non-goals

- No new data: no signer name, no "Service"/"Period", no "Cleared by" (owner, 2026-11-11: "no need new fields").
  Mockup elements that need them are dropped: the signature block, the invoice "Reference" block's Service and
  Period rows, the receipt's "Cleared by" row and bank-reference row unless the payment already stores one.
- No database, API or frontend changes. Endpoints, filenames, email attachments and the `Content-Disposition`
  behaviour stay exactly as they are.
- No change of PDF engine.

## Anti-goals

- A document that shows a wrong or invented amount, a missing PPh row, or a total that differs from today's for the
  same invoice. The redesign moves boxes; it never changes a number.
- Losing the PO/SO cell, the footer, the uploaded logo or foreign-currency support (owner decision, PORT-NOTES
  note 126).
- Breaking byte-for-byte reproducibility (`TestRenderIsReproducible`).
- Item rows split across pages, or a long invoice losing its repeated header/footer.

## Constraints

- Go backend, package `backend/internal/pdf/`: Maroto v2 on gofpdf, embedded fonts in `fonts/`, own pager/stack
  layout helpers in `layout.go` (714 lines), neutral `Document` struct built in `data.go`.
- Tests run via `backend/dev test` on real Postgres where needed; every Go test and subtest calls `t.Parallel()`
  (lint-enforced); every guard gets a test shown red when removed.
- Logos are PNG/JPEG (SVG falls back to the default logo) — unchanged.
- Every merge to main deploys via Coolify and needs the owner's yes.

## Approaches considered

- **A. Rebuild the layout in Maroto (chosen by the owner, 2026-11-11).** Redraw `layout.go` in the mockup's style;
  no new dependencies, no infrastructure change, output stays reproducible, existing tests keep guarding behaviour.
  Cost: every element is placed in code.
- **B. HTML templates printed by headless Chromium/Gotenberg.** Mockup CSS reusable, easy later tweaks; but a ~500 MB
  browser container in production, a Coolify change, non-reproducible output. Rejected.

## Design

### Visual system (from the mockup)

Header band burgundy `#a51c30`; gold `#d8bf84` for the location line; ink `#501921` for body text; line `#e1d7c8`
for rules and box borders; muted `#75665f`/`#9d8a6e` for labels; surface-2 `#f6f2ea` for tinted panels; PPh/negative
rows in red `#a51c30`; "Paid in full" pill green `#2f6b4f` on `#e4efe7`. Fonts: Manrope for headings, document
number and amounts; DM Sans for body; the existing monospace for references. Manrope and a monospace are already
embedded; DM Sans is added to `fonts/` (Inter is removed if nothing else uses it). No gradients (the current
`gradient()` helper goes).

### Invoice (top to bottom)

1. **Header band**, full bleed: left — white square holding the uploaded logo (default logo when none), business
   name, "Batam · Indonesia" style location line in gold from the business city/country; right — document title
   (configurable, default "Invoice"), number at 22pt, issue and due dates, and an outlined status chip.
2. **Second band, three columns:** From (business), Bill to (customer), Reference — the reference column holds
   what exists today: currency, payment terms/due, PO/SO (when set), and any other meta cells the current
   `Document.Meta` carries.
3. **Items table:** numbered rows (01, 02, …, one number per line item), description with the existing `Sub` line
   under it when present, qty, rate, amount; tabular figures.
4. **Lower block:** left — "Payment to" panel (payment instructions as today, NPWP when the business has one);
   right — boxed summary: Subtotal, discount (if any), PPh gross-up (if any), Total, PPh withholding as a red
   negative (if any), Paid (if any), ending in a burgundy bar. Bar label and amount follow today's rules exactly:
   "Amount to Transfer" when there is withholding, otherwise "Total Due" (same values as
   `TestRenderWHTInvoiceHeroIsAmountToTransfer` / `TestRenderNoWHTInvoiceHeroIsTotalDue`).
5. **Notes and Terms** in two columns (default notes fallback as today).
6. **Footer:** the business footer text, a bottom rule, and "Page X of Y"; repeated on every page.

### Receipt

Same header band (title "Payment Receipt", receipt number from `ReceiptReference`), a second band with Issued by /
Received from / Payment (method from `MethodLabel`, payment date, and the bank reference only if the payment record
already stores one); a large "Amount received" figure in the payment currency, the amount in words (English), and a
green "Paid in full" pill when the invoice balance is zero (otherwise the existing `BalanceNote` text); an
"Applied to" table with the invoice number, issue date, invoice total and the amount applied; when the invoice has
PPh withholding, the withholding row and the mockup's note asking for the bukti potong; a summary box ending in
"Balance due". Cross-currency payments (payment currency ≠ invoice currency) show both currencies as the current
receipt does. Footer as the invoice.

Receipt amounts (Claude's call, builder check): every figure is a stored value, never derived across currencies.
"Amount received" = `payment.Amount` (payment currency). The "Applied to" row shows the invoice number, issue date,
invoice total (invoice currency) and "This payment" = `payment.Amount` plus `payment.WhtAmount` when the payment
carries withholding, in the payment currency. The PPh row uses `payment.WhtAmount` (this payment's withholding, not
the invoice's). For a cross-currency payment no invoice-currency equivalent of the payment is computed; the receipt
shows the payment in its own currency and the invoice figures in theirs, as today.

Receipt balance (owner decision, 2026-11-11: "as of payment"): the receipt's "Balance due", the "Paid in full" pill
and `BalanceNote` reflect the invoice balance immediately after this payment, not the invoice's live balance. The
loader computes it from the invoice and the payments recorded on it up to and including this one (ordered by payment
date then creation time; reversed/void payments excluded), using the same amounts the ledger relieves the invoice by
(cash plus withholding in invoice terms, at the rules `InvoiceBasis`/`reliefBase` already apply for cross-currency
payments). A test pins it: two partial payments, the first payment's receipt shows the remaining balance after it,
not zero, after the second payment clears the invoice.

### Quotation

Same as the invoice layout with the quotation's labels and rows; it keeps the payment panel it has today
(`common()` resolves payment instructions for quotations), its status chip as today, and the validity date in place
of the due date.

### Reference column contents (Claude's call)

Exactly today's `Document.Meta` beyond number and dates: currency and PO/SO when set. No payment-terms row
(`InvoiceDocument` deliberately blanks terms — Net N drives the due date).

### Pages (Claude's call)

The full header band and the From/Bill to/Reference band appear on page 1 only. Continuation pages get a compact
burgundy strip (business name left; document title and number right) registered as the repeating header, so the
pager budget stays predictable; the footer (footer text, rule, "Page X of Y") repeats on every page. Item rows still
never split.

### Titles (Claude's call)

Titles print in the mockup's case: the configured invoice title as typed (default "Invoice"), "Quotation",
"Payment Receipt" — no forced upper-casing. Tests pinning "INVOICE"/"RECEIPT" are updated.

### Status chips (Claude's call)

Keep today's `statusBadge` status→class mapping (draft, sent, paid, partial, overdue; VOID→draft,
AWAITING_FINALIZATION→partial; quotation statuses as today) and recolour the classes with the app restyle's pairs:
paid green, sent blue, partial amber, overdue red, draft neutral. The receipt's chip stays "Received" (paid class);
the separate green "Paid in full" pill is the receipt body element described above.

### Fonts (Claude's call)

DM Sans added as static TTF instances (regular, medium) — gofpdf cannot use variable fonts; Manrope gains the
weights the design uses if the embedded set lacks them. gofpdf has no tabular-figures feature; amounts are
right-aligned instead. The gold location line is the last non-empty line of the business's free-text address
(there are no city/country fields); omitted when the address is empty.

### Verification

- All existing `render_test.go`, `data_test.go`, `logo_test.go`, `format_test.go` tests still pass; tests that pin
  the old navy colours or layout positions are updated to the new design, never deleted.
- New tests: numbered rows; receipt shows PPh row and bukti-potong note only when the invoice has withholding;
  receipt "Paid in full" pill only at zero balance; amount-in-words for IDR, SGD, USD and an amount with cents;
  status chip label for every status; PO/SO, footer and uploaded logo still present; reproducibility unchanged.
- `TestWriteSamples` regenerates sample PDFs (invoice with and without PPh, multi-page invoice, SGD invoice,
  quotation, IDR receipt, cross-currency receipt); render each to PNG and compare by eye with the mockup.

### Left to the builder

- Exact sizes, paddings and column splits (match the mockup visually).
- How the location line is composed from the business address fields.
- English amount-in-words wording for currency names and cents.
