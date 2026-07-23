# HTML→PDF rendering for invoices & quotations

**Date:** 2026-07-24
**Status:** Approved (design)
**Scope:** Backend PDF rendering pipeline + one BusinessProfile field + Docker + one frontend field.

## Problem

Invoice and quotation PDFs are generated with **reportlab**
([`app/services/invoice_pdf.py`](../../../backend/app/services/invoice_pdf.py),
[`app/services/quotation_pdf.py`](../../../backend/app/services/quotation_pdf.py)),
which cannot render the approved premium "SaaS billing" design — it relies on web
fonts (Manrope / Inter / JetBrains Mono), a CSS brand gradient, rounded corners and
subtle shadows. We want that HTML/CSS design to become the produced PDF, for both
documents, without regressing the current UX.

## Decisions (locked)

1. **Scope:** migrate **both** invoices and quotations to HTML→PDF.
2. **reportlab:** **keep as an automatic fallback** if HTML rendering fails at runtime.
3. **Logo:** render `BusinessProfile.logo_url` when set, else a **bundled default logo**;
   the logo fills the **full masthead height**.
4. **Data model:** add a `payment_instructions` text field to `BusinessProfile`.
   **No Tax/GST** (the ledger has no tax concept). Template renders only real data.
5. **Engine:** Playwright + Chromium, **launched per request** (sync API, in FastAPI's
   threadpool). Not a persistent singleton (thread-affinity/lifecycle complexity,
   unjustified at this volume) and not WeasyPrint (cannot render the gradient/webfont
   design).

## Architecture

New package `backend/app/services/pdf/`:

```
pdf/
  __init__.py      # façade: render_invoice_pdf(), render_quotation_pdf()
  engine.py        # html_to_pdf(html: str) -> bytes  (Playwright, launch-per-request)
  context.py       # build_invoice_context(), build_quotation_context()  (model -> dict)
  logo.py          # resolve_logo(business) -> data URI  (logo_url file/URL | bundled default)
  assets.py        # load + cache bundled fonts (woff2 -> data URI) and default logo
  templates/
    base.html.j2       # shared shell: <head>, embedded @font-face, masthead, footer
    invoice.html.j2    # extends base
    quotation.html.j2  # extends base
  assets/
    fonts/             # Inter, Manrope, JetBrains Mono (self-hosted woff2)
    default-logo.png
```

### Render pipeline

```
endpoint
  → build_invoice_context(invoice, customer, business)      # pure: model -> dict
  → Jinja2 render invoice.html.j2 with context              # self-contained HTML:
                                                             #   fonts + logo embedded
                                                             #   as data URIs, NO network
  → engine.html_to_pdf(html)                                # Chromium page.pdf(...)
  → PDF bytes
```

`engine.html_to_pdf`:

```python
from playwright.sync_api import sync_playwright

def html_to_pdf(html: str) -> bytes:
    with sync_playwright() as p:
        browser = p.chromium.launch(args=["--no-sandbox"])
        try:
            page = browser.new_page()
            page.set_content(html, wait_until="load")   # self-contained => no networkidle
            return page.pdf(
                format="A4", print_background=True,
                margin={"top": "0", "right": "0", "bottom": "0", "left": "0"},
                prefer_css_page_size=True,
            )
        finally:
            browser.close()
```

Sync Playwright is created and destroyed within a single request thread (FastAPI runs
`def` endpoints in a threadpool), so there is no cross-thread object sharing.

### Façade + fallback

`app/services/pdf/__init__.py` exposes the stable entry points the routers already call:

```python
def render_invoice_pdf(invoice, customer, business=None) -> bytes:
    if get_settings().pdf_engine == "reportlab":
        return _reportlab_invoice(invoice, customer, business)
    try:
        html = render_invoice_html(invoice, customer, business)
        return html_to_pdf(html)
    except Exception:
        log.exception("HTML PDF render failed; falling back to reportlab")
        return _reportlab_invoice(invoice, customer, business)
```

- The existing reportlab renderers are **retained** as `_reportlab_invoice` /
  `_reportlab_quotation` (moved/kept in the current modules; imported by the façade).
- **Routers are unchanged** apart from their import path resolving to the façade;
  they still receive PDF bytes. `render_invoice_pdf(invoice, customer, business)` keeps
  its signature. Quotations get the analogous `render_quotation_pdf`.
- **Frontend is untouched**: `GET /invoices/{id}/pdf` and `/quotations/{id}/pdf` still
  stream bytes (`window.open`); `POST .../send` unchanged.

### Config

Add to settings:

- `pdf_engine: Literal["html", "reportlab"] = "html"` — force the legacy path if needed.
- `pdf_render_timeout_ms: int = 15000` — Chromium page/pdf timeout; on timeout the
  façade falls back to reportlab.

## Templates

- `base.html.j2` holds the shared shell: palette CSS variables, embedded `@font-face`
  (data-URI woff2 from `assets.py`), the navy masthead (logo left at full header height,
  document title + reference id + status badge right), and the footer.
- `invoice.html.j2` — meta grid (number / issue / due / status), Bill From / Bill To,
  itemised table (description, qty, rate, amount), Payment Instructions panel
  (`business.payment_instructions`), Notes (`invoice.notes` or
  `business.default_notes`), Terms (`invoice.payment_terms`), and the summary:
  **Subtotal, Discount, Paid, Total Due (hero)**. No Tax row.
- `quotation.html.j2` — same shell; summary shows **Total** as the hero (a quotation has
  no paid/balance concept). Fields mapped from the quotation model.
- Status → badge class mapping (`draft / sent / paid / partial / overdue`, default to
  the accent) shared via a Jinja filter or the context.
- Print CSS: `@page { size:A4; margin:0 }`, `print-color-adjust:exact`, one-page layout,
  `break-inside:avoid` on cards/rows. (Carried over from the approved prototype.)

### Data mapping (invoice)

| Template element        | Source |
|-------------------------|--------|
| Masthead title          | `business.invoice_title` or "INVOICE" |
| Reference id            | `invoice.invoice_number` |
| Status badge            | `invoice.status` → badge class |
| Meta: issue / due       | `invoice.issue_date` / `invoice.due_date` |
| Bill From               | `business.name / address / contact_email / contact_phone` |
| Payment Instructions    | `business.payment_instructions` (new field) |
| Bill To                 | `customer.name` + billing address lines + `contact_email` |
| Line items              | `invoice.line_items[*]`: position, description, quantity, unit_price, amount |
| Subtotal                | `invoice.subtotal` |
| Discount                | `_discount_amount(discount_type, discount_value, subtotal)` (existing helper) |
| Paid                    | `invoice.amount − invoice.balance_due` (rendered only if > 0) |
| Total Due (hero)        | `invoice.balance_due` |
| Notes                   | `invoice.notes` or `business.default_notes` |
| Terms                   | `invoice.payment_terms` |
| Currency                | `invoice.currency` (stated once, on the amount column + totals) |

Quotation mapping mirrors this against the quotation model; Total (hero) = quotation
total, no Paid/Balance rows.

### Logo resolution

`resolve_logo(business) -> data URI`:

1. If `business.logo_url` points to a locally stored uploaded file → read bytes.
2. Else if it is an `http(s)` URL → fetch bytes (short timeout) — on failure, fall back.
3. Else → bundled `assets/default-logo.png`.
4. Encode as `data:image/...;base64,...` and inject into the template (self-contained;
   Chromium never needs the network).

Ties into the in-progress logo-upload feature; the exact `logo_url` storage form is
read here, with the bundled default guaranteeing a logo always renders.

## Schema change: `BusinessProfile.payment_instructions`

- **Model:** add `payment_instructions: Mapped[str | None] = mapped_column(Text, nullable=True)`.
- **Migration:** new Alembic revision (id < 32 chars), `add_column` nullable — safe, no backfill.
- **Schemas:** add the field to `BusinessProfileUpdate` and `BusinessProfileOut`
  (the existing generic `GET/PUT /business-profile` handles it).
- **Frontend:** one `<textarea>` on the company form in
  [`AccountView.vue`](../../../frontend/src/views/AccountView.vue) (label "Payment
  instructions", helper text "Bank / PayNow details shown on invoices").

## Docker

[`backend/Dockerfile`](../../../backend/Dockerfile):

- Add `playwright` and `jinja2` to `pyproject.toml` dependencies.
- After `pip install`, run `playwright install --with-deps chromium` (installs Chromium
  + OS libs; ~+300 MB). Shared by the `backend` and `worker` images (same Dockerfile);
  only the web process renders at runtime, but availability in both is harmless.

## Testing

- **Unit (no browser, no DB)** — build transient model instances:
  - `build_invoice_context` / `build_quotation_context`: correct field mapping, discount
    amount, Paid = amount − balance_due (and omitted when 0), currency.
  - `resolve_logo`: uploaded → bundled fallback ordering.
  - status → badge class mapping.
  - façade fallback: when `html_to_pdf` raises, reportlab output is returned; when
    `pdf_engine="reportlab"`, HTML path is skipped.
- **Integration (render)** — one test per document renders HTML→PDF and asserts the
  result starts with `%PDF-`; **skipped when Chromium/Playwright is unavailable**
  (mirrors the `TEST_DATABASE_URL` skip pattern), so the suite still runs on machines
  without the browser.
- Existing reportlab tests (`tests/unit/test_invoice_pdf.py`) remain valid against the
  retained fallback renderer.

## Backward compatibility & rollout

- Routers and frontend PDF/send flows are unchanged (endpoints still return bytes; one
  new AccountView field).
- Default `pdf_engine="html"` with automatic reportlab fallback; set `"reportlab"` to
  fully revert without a deploy code change.

## Risks

- **Image size** +~300 MB (Chromium). Accepted.
- **Latency** ~1 s per render (launch-per-request). Acceptable for on-demand PDFs.
- **Font embedding** enlarges the in-memory HTML (~200–400 KB). Generated in memory; fine.

## Out of scope

- Tax/GST support (no ledger concept today).
- Structured (multi-field) bank details — a single free-text `payment_instructions` block
  is used instead.
- Persistent-browser performance optimisation.
- Any change to the invoice/quotation data models beyond `payment_instructions`.
