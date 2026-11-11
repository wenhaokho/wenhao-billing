# PDF Brand Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Redraw the invoice, quotation and receipt PDFs in the wenhao.id brand style from the owner's mockup, keeping every number and every field customers rely on, and make each receipt show the balance as of its own payment.

**Architecture:** All work is in `backend/internal/pdf/` (Maroto v2 on gofpdf): new palette/fonts, a rewritten `layout.go` sharing one page frame across the three documents, receipt data extended in `data.go`, and the receipt loader in `load.go` replaying the invoice's cleared payments with the same relief rule `payments.clear` applies. Output stays byte-for-byte reproducible.

**Tech Stack:** Go, Maroto v2, gofpdf, `ledongthuc/pdf` (`lpdf`, test text extraction), shopspring/decimal, sqlc, real Postgres via `backend/dev test`.

**Spec:** `docs/superpowers/specs/2026-11-11-pdf-brand-redesign-design.md`

## Global Constraints

- Work only in `backend/internal/pdf/` (plus a sqlc query in `backend/internal/pdf/` or `internal/payments/` queries for Task 3, and `backend/PORT-NOTES.md`). No API, endpoint, filename, email or frontend change; no migrations.
- Never change a number: every amount printed equals today's for the same document; totals' labels follow today's rules ("Amount to Transfer" with withholding, else "Total Due").
- Keep: uploaded logo (PNG/JPEG; SVG → default logo), PO/SO cell, footer text, configurable invoice title (printed as typed, default "Invoice"), default-notes fallback, payment instructions, NPWP, discount and PPh rows, foreign currencies, cross-currency receipts, internal note never printed, item rows never split, "Page X of Y" on every page.
- Colours: band/total/PPh red `#a51c30`, gold `#d8bf84`, ink `#501921`, line `#e1d7c8`, muted `#75665f`, muted-soft `#9d8a6e`, surface-2 `#f6f2ea`, paid pill `#2f6b4f` on `#e4efe7`; chip classes paid green `#2f6b4f`/`#e4efe7`, sent blue `#3b5a7a`/`#e3e9ef`, partial amber `#8a5a12`/`#f6ecd6`, overdue red `#a51c30`/`#f3dfe1`, draft neutral `#75665f`/`#ede7de`. No gradients.
- Fonts: Manrope (headings, numbers, amounts), DM Sans static TTFs regular + medium (body), JetBrains Mono (references). Inter removed. All embedded; OFL texts kept beside them.
- Titles: configured invoice title as typed (default "Invoice"), "Quotation", "Payment Receipt".
- No new data fields; dropped mockup elements: signature block, Service/Period, Cleared by, payment-terms row.
- Every Go test and subtest calls `t.Parallel()`; every guard gets a test shown red when the guard is removed. Tests via `cd backend && ./dev test ./internal/pdf/...`; lint via `./dev lint`.
- `TestRenderIsReproducible` must keep passing.
- Every merge to main deploys; merging needs the owner's yes.

## Review Focus

1. A 40-line invoice: header band only on page 1, compact strip on later pages, footer and "Page X of Y" on all, no item row split — pinned in Task 2 (extend `TestLongInvoiceRepeatsHeaderAndFooter`, `TestItemRowsNeverSplit`).
2. A business with no logo, no address, no NPWP and a customer with no address: the bands still render without empty labels or "—" noise — pinned in Task 2 (`TestRenderWithoutCustomerOrBusiness` extended).
3. Very long customer name / address lines wrap inside their column and never overlap the next column — pinned in Task 2.
4. A receipt for the first of two partial payments, printed after the invoice is fully paid, shows the remaining balance after payment 1 and no "Paid in full" — pinned in Task 3.
5. A reversed payment between two cleared ones is ignored by the balance replay — pinned in Task 3.

---

### Task 1: Palette, fonts and drawing primitives

**Files:**
- Modify: `backend/internal/pdf/layout.go` (colour constants, `statusColour`, `summaryColour`, `font()`, remove `gradient()`), `backend/internal/pdf/render.go` (font registration)
- Create: `backend/internal/pdf/fonts/DMSans-Regular.ttf`, `DMSans-Medium.ttf`, `DMSans-OFL.txt`, `Manrope-Regular.ttf` (and `Manrope-Medium.ttf` if used); delete `Inter-*.ttf`, `Inter-OFL.txt`
- Test: `backend/internal/pdf/render_test.go`

**Interfaces:**
- Produces: font family names `"manrope"`, `"dmsans"`, `"mono"` registered in `render.go`; colour vars `cRed, cGold, cInk, cLine, cMuted, cMutedSoft, cSurface2, cPaidFg, cPaidBg` (`*props.Color`); `chipColours(class string) (fg, bg *props.Color)` for classes `paid, sent, partial, overdue, draft`.

- [ ] **Step 1: Update `TestFontsEmbedAllFamilies`** to require Manrope, DM Sans and JetBrains Mono in the PDF's font resources and forbid Inter; add `TestChipColoursCoverEveryClass` asserting `chipColours` returns the Global Constraints pair for each of the five classes and a non-nil pair for an unknown class (draft's).
- [ ] **Step 2: Run** `cd backend && ./dev test ./internal/pdf/...` → the two tests FAIL.
- [ ] **Step 3: Implement.** Fetch DM Sans and Manrope static TTFs from the Google Fonts GitHub repo (`google/fonts`, `ofl/dmsans/static` and `ofl/manrope/static` or the projects' release assets), verify they are static (not variable) instances, keep their OFL text. Swap colours; drop `gradient()` and its callers by filling those bands with `cRed` (the full layout redesign is Task 2).
- [ ] **Step 4: Run** the package tests → all PASS, including `TestRenderIsReproducible`.
- [ ] **Step 5: Commit** — `feat(pdf): brand palette and fonts (Manrope, DM Sans)`.

### Task 2: Invoice and quotation layout

**Files:** Modify `backend/internal/pdf/layout.go`, `backend/internal/pdf/data.go` (only `Document` fields the layout needs: `Location string`, `ChipClass` reuse of `StatusClass`), `backend/internal/pdf/format.go` (row number format) · Test: `render_test.go`, `data_test.go`

**Interfaces:**
- Consumes: Task 1 colours, fonts, `chipColours`.
- Produces: `Document.Location string` (last non-empty line of the business address; empty when none), `rowNumber(i int) string` returning `"01"`, `"02"`, … `"10"`, `"100"` (zero-padded to 2). Layout pieces reused by Task 4: `headerBand(d Document, logo []byte) core.Row`, `continuationStrip(d Document) core.Row`, `partiesBand(d Document, third column) core.Row`, `footerBand(d Document) core.Row`, `summaryBox(d Document) core.Row`.

- [ ] **Step 1: Tests (new or updated):**
  - `TestInvoiceRowsAreNumbered`: `fullInvoice()` text contains `"01"` before "Website redesign" and `"02"` before "Frontend implementation".
  - `TestTitlesPrintAsTyped`: default invoice title text "Invoice" (not "INVOICE"); a business `InvoiceTitle` of "Tax Invoice" prints "Tax Invoice"; quotation prints "Quotation". Update every existing test that pinned upper-case titles.
  - `TestLocationLineIsLastAddressLine`: business address `"Jl. Raya 1\nBatam · Indonesia\n"` → `Document.Location == "Batam · Indonesia"`; empty address → `""` and the PDF has no location line.
  - Extend `TestLongInvoiceRepeatsHeaderAndFooter`: `longInvoice(40)` → page 1 text contains "Bill to"; pages 2..n do not contain "Bill to" but contain the business name and invoice number; every page contains `"Page i of n"`.
  - Keep green: `TestItemRowsNeverSplit`, `TestInvoiceAndQuotationShowFooterAndPOSO`, `TestRenderWHTInvoiceHeroIsAmountToTransfer`, `TestRenderNoWHTInvoiceHeroIsTotalDue`, `TestInvoicePutsNPWPInPaymentPanelWhenWithholding`, `TestNotesFallBackToBusinessDefaultWhenInvoiceHasNone`, `TestOnlyLogoUrlIsFetched`, `TestInternalNoteNeverReachesThePDF`, `TestRenderIsReproducible`.
  - `TestLongNamesWrapInsideColumns`: customer name of 120 characters and a 200-character address line render without error and every word of them appears in the text (no truncation).
  - Extend `TestRenderWithoutCustomerOrBusiness`: text has no `"Bill to"` label followed by nothing and no `"—"` in the parties band.
- [ ] **Step 2: Run** → new/updated tests FAIL.
- [ ] **Step 3: Implement** the spec's invoice layout (header band page 1 with logo square, business name, gold location, title, number, dates, outlined chip; three-column From / Bill to / Reference band with today's meta cells; numbered items with `Sub` lines; Payment-to panel + summary box ending in the red bar with today's label/amount; Notes/Terms two columns; footer with footer text, rule, "Page X of Y"; compact continuation strip registered as the repeating header for pages ≥2). Quotation uses the same frame with its labels, its payment panel and validity date.
- [ ] **Step 4: Run** package tests → PASS; `./dev lint` → clean.
- [ ] **Step 5: Commit** — `feat(pdf): brand invoice and quotation layout`.

### Task 3: Receipt balance as of its payment

**Files:** Modify `backend/internal/pdf/load.go`, `backend/internal/pdf/data.go` (`ReceiptData` gains `BalanceAfter decimal.Decimal`); add a sqlc query listing an invoice's payments (in the package that owns payment queries, `internal/payments/queries.sql` → `paymentsdb`, regenerate with `./dev sqlc`); create `backend/internal/pdf/balance.go` · Test: `backend/internal/pdf/balance_test.go` (pure), `load_test.go` (DB)

**Interfaces:**
- Produces: `func BalanceAfter(invoiceAmount decimal.Decimal, invoiceCurrency string, payments []paymentsdb.Payment, upTo uuid.UUID) decimal.Decimal` — replays, in order (payment_date, then created_at, then payment_id), only payments with status `CLEARED`, stopping after `upTo`: start at `invoiceAmount`; same currency → `max(0, balance − (amount + wht_amount))`; different currency → `0`. This is exactly the rule in `internal/payments/clear.go` (`clear`) and the manual path in `invoice_routes.go`; a comment there points to `BalanceAfter` and vice versa. `ReceiptDocument` uses `BalanceAfter` (not `Invoice.BalanceDue`) for "Balance due", the paid pill and `BalanceNote`.
- Ruling for the implementer: `invoiceAmount` is the invoice's gross `Amount` (what `balance_due` starts at when issued); confirm against where `balance_due` is first set on issue (`internal/invoices/queries.sql` / service) and stop with NEEDS_CONTEXT if it is set differently.

- [ ] **Step 1: Pure tests in `balance_test.go`:** single full payment → 0; two partials 400 + 600 on 1000 → after first = 600, after second = 0; payment with `wht_amount` 25 and amount 475 on 500 → 0; a `REVERSED` payment between them is skipped; cross-currency payment → 0; overpayment clamps to 0; `upTo` not in the list → balance after all cleared payments (documented choice).
- [ ] **Step 2: DB test in `load_test.go`:** `TestReceiptShowsBalanceAsOfItsPayment` — create an invoice of 1000 IDR, record two manual payments (400 then 600) through the payments service, then `LoadReceipt` for the first payment → `BalanceAfter == 600`, and its rendered receipt text contains the 600 balance and not "Paid in full"; for the second → 0 and "Paid in full". `TestBalanceReplayMatchesLiveBalance` — after the two payments, `BalanceAfter(..., last)` equals the invoice's stored `balance_due` (guards drift from `clear`).
- [ ] **Step 3: Run** → FAIL.
- [ ] **Step 4: Implement** query + `BalanceAfter` + loader wiring.
- [ ] **Step 5: Run** `./dev test ./internal/pdf/... ./internal/payments/...` and `./dev generated` → PASS / no drift.
- [ ] **Step 6: Commit** — `feat(pdf): receipt balance as of its own payment`.

### Task 4: Receipt layout and amount in words

**Files:** Modify `layout.go`, `data.go` (`ReceiptDocument`), `format.go` (`AmountInWords`) · Test: `format_test.go`, `render_test.go`

**Interfaces:**
- Consumes: Task 2 layout pieces, Task 3 `ReceiptData.BalanceAfter`.
- Produces: `func AmountInWords(amount decimal.Decimal, currency string) string` — English, capitalised first word, currency name after the whole part, cents as "and NN/100": IDR → "rupiah" (no cents shown when zero); SGD → "Singapore dollars"; USD → "US dollars"; MYR → "Malaysian ringgit"; other codes → the code.

- [ ] **Step 1: Tests:**
  - `format_test.go` `TestAmountInWords`: `3600000 IDR` → `"Three million six hundred thousand rupiah"`; `2000 SGD` → `"Two thousand Singapore dollars"`; `1234.56 USD` → `"One thousand two hundred thirty-four US dollars and 56/100"`; `0.5 USD` → `"Zero US dollars and 50/100"`; `1 IDR` → `"One rupiah"`.
  - `TestReceiptLayout`: `receipt(...)` text contains "Payment Receipt", the receipt number, "Amount received", the payment amount, its amount in words, "Applied to", the invoice number, "Balance due"; `"Received"` chip present.
  - `TestReceiptShowsWithholdingOnlyWhenPaymentHasIt`: a payment with `WhtAmount` 92308 shows a PPh row with `−92,308` and the bukti potong note; one with zero shows neither.
  - `TestReceiptPaidPillOnlyAtZeroBalance`: `BalanceAfter` 0 → "Paid in full"; 600 → no "Paid in full", today's "Remaining balance: …" note.
  - `TestCrossCurrencyReceiptShowsBothCurrencies`: SGD payment on an IDR invoice shows the SGD amount received and the IDR invoice total, no converted figure.
  - Keep green: the three existing receipt tests and `TestRenderIsReproducible`.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement** the spec's receipt layout using the shared header band, parties band (Issued by / Received from / Payment: method, date, bank reference only when `PayerReference` is set), the big amount, words, pill or note, Applied-to table, PPh row + note, summary box ending "Balance due", footer.
- [ ] **Step 4: Run** package tests → PASS; lint clean.
- [ ] **Step 5: Commit** — `feat(pdf): brand receipt with amount in words`.

### Task 5: Samples, notes and PR

- [ ] **Step 1:** Extend `TestWriteSamples` with: invoice without PPh, SGD invoice, IDR receipt with PPh, cross-currency receipt (keep the existing four). Run `./dev test ./internal/pdf/ -run TestWriteSamples -args -samples <dir>` into the session scratchpad, convert pages to PNG (`pdftoppm` in a throwaway Docker image), compare with `docs/redesign/invoice-receipt/Invoice - Receipt.html` rendered in the browser pane; fix visual gaps in a follow-up commit.
- [ ] **Step 2:** Add a `backend/PORT-NOTES.md` note: brand redesign; receipts now show balance as of the payment (behaviour change, owner decision 2026-11-11); titles printed as typed; dropped mockup elements and why.
- [ ] **Step 3:** `bash scripts/check.sh` → passes.
- [ ] **Step 4:** Push `feat/pdf-brand`, open PR "PDF brand redesign (revamp part 3)" with sample PNGs, the behaviour change called out, and the dropped elements listed. Ask the owner for the merge yes; after merge, confirm the Coolify deploy and download one live invoice PDF.
