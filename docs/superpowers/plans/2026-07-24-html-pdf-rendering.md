# HTML→PDF Rendering Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Render invoice & quotation PDFs from an HTML/CSS template via headless Chromium, keeping the existing reportlab renderers as an automatic fallback.

**Architecture:** A new `app/services/pdf/` package builds a normalized context dict from the model, renders one shared Jinja template into fully self-contained HTML (fonts + logo embedded as data URIs, no network), and prints it to A4 PDF with Playwright (Chromium launched per request). A thin façade exposes `render_invoice_pdf` / `render_quotation_pdf`, trying HTML first and falling back to reportlab on any error or when `pdf_engine="reportlab"`.

**Tech Stack:** FastAPI, SQLAlchemy 2.0, Alembic, Jinja2, Playwright (Chromium), reportlab (fallback), Vue 3 (one field), Docker.

## Global Constraints

- **Alembic revision ids must be < 32 chars** (version_num column limit).
- Financial amounts are `DECIMAL(19,4)`; format for display with the existing helpers `_fmt_num` / `_discount_amount` in `app/services/invoice_pdf.py`.
- Preserve `business_id`/single-record scoping conventions; `BusinessProfile` is a singleton at `id=1`.
- Do **not** change router signatures or the frontend PDF/send flows beyond what this plan specifies.
- New Python deps pinned in `backend/pyproject.toml`: `jinja2>=3.1`, `playwright>=1.44`.
- Tests requiring Postgres skip without `TEST_DATABASE_URL`; tests requiring Chromium must skip when it is unavailable (never hard-fail the suite).
- reportlab renderers (`app/services/invoice_pdf.py`, `app/services/quotation_pdf.py`) are **retained unchanged** as the fallback — do not delete or rename their public functions.

---

### Task 1: Dependencies & config settings

**Files:**
- Modify: `backend/pyproject.toml:6-25` (add deps)
- Modify: `backend/app/config.py:44-55` (add settings)
- Test: `backend/tests/unit/test_pdf_config.py`

**Interfaces:**
- Produces: `Settings.pdf_engine: str` (default `"html"`), `Settings.pdf_render_timeout_ms: int` (default `15000`).

- [ ] **Step 1: Write the failing test**

```python
# backend/tests/unit/test_pdf_config.py
from app.config import Settings


def test_pdf_settings_defaults():
    s = Settings()
    assert s.pdf_engine == "html"
    assert s.pdf_render_timeout_ms == 15000
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && python -m pytest tests/unit/test_pdf_config.py -v`
Expected: FAIL — `AttributeError: 'Settings' object has no attribute 'pdf_engine'`.

- [ ] **Step 3: Add the settings**

In `backend/app/config.py`, add after the MCP block (before `@lru_cache`):

```python
    # --- PDF rendering ---
    # "html" = Playwright/Chromium HTML→PDF (falls back to reportlab on error);
    # "reportlab" = force the legacy renderer.
    pdf_engine: str = Field(default="html")
    pdf_render_timeout_ms: int = Field(default=15000)
```

- [ ] **Step 4: Add the dependencies**

In `backend/pyproject.toml`, add to the `dependencies` list (after `"reportlab>=4.0",`):

```toml
    "jinja2>=3.1",
    "playwright>=1.44",
```

- [ ] **Step 5: Install deps locally so later tasks can import them**

Run: `cd backend && pip install -e ".[dev]"`
Expected: installs jinja2 and playwright (the Chromium browser binary is installed in Task 11 / Docker; not required for Jinja/context tests).

- [ ] **Step 6: Run test to verify it passes**

Run: `cd backend && python -m pytest tests/unit/test_pdf_config.py -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add backend/pyproject.toml backend/app/config.py backend/tests/unit/test_pdf_config.py
git commit -m "feat(pdf): add jinja2/playwright deps and pdf_engine settings"
```

---

### Task 2: BusinessProfile.payment_instructions (model + migration + schema)

**Files:**
- Modify: `backend/app/models/business_profile.py:20` (add column)
- Create: `backend/alembic/versions/0021_business_pay_instr.py`
- Modify: `backend/app/schemas/business_profile.py:6-28`
- Test: `backend/tests/unit/test_pdf_config.py` (extend — schema field presence, no DB)

**Interfaces:**
- Produces: `BusinessProfile.payment_instructions: str | None`; same field on `BusinessProfileUpdate` / `BusinessProfileOut`.

- [ ] **Step 1: Write the failing test**

Append to `backend/tests/unit/test_pdf_config.py`:

```python
def test_business_profile_has_payment_instructions_field():
    from app.schemas.business_profile import BusinessProfileUpdate
    m = BusinessProfileUpdate(payment_instructions="DBS 012-345678-9")
    assert m.payment_instructions == "DBS 012-345678-9"
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && python -m pytest tests/unit/test_pdf_config.py::test_business_profile_has_payment_instructions_field -v`
Expected: FAIL — pydantic ignores/rejects the unknown field (no attribute).

- [ ] **Step 3: Add the model column**

In `backend/app/models/business_profile.py`, add after `default_notes` (line 20):

```python
    payment_instructions: Mapped[str | None] = mapped_column(Text, nullable=True)
```

- [ ] **Step 4: Add the schema fields**

In `backend/app/schemas/business_profile.py`, add `payment_instructions: str | None = None` to `BusinessProfileUpdate` (after `default_notes`), and `payment_instructions: str | None` to `BusinessProfileOut` (after `default_notes`).

- [ ] **Step 5: Create the migration**

Confirm head first: `cd backend && alembic heads` → expect `mcp_oauth`. Then create `backend/alembic/versions/0021_business_pay_instr.py`:

```python
"""add payment_instructions to business_profile

Revision ID: business_pay_instr
Revises: mcp_oauth
Create Date: 2026-07-24
"""
from alembic import op
import sqlalchemy as sa

revision = "business_pay_instr"
down_revision = "mcp_oauth"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.add_column(
        "business_profile",
        sa.Column("payment_instructions", sa.Text(), nullable=True),
    )


def downgrade() -> None:
    op.drop_column("business_profile", "payment_instructions")
```

- [ ] **Step 6: Apply the migration**

Run: `cd backend && alembic upgrade head`
Expected: `Running upgrade mcp_oauth -> business_pay_instr`. (Requires a running Postgres; if developing via docker compose, run `docker compose exec backend alembic upgrade head`.)

- [ ] **Step 7: Run the test to verify it passes**

Run: `cd backend && python -m pytest tests/unit/test_pdf_config.py -v`
Expected: PASS (both tests).

- [ ] **Step 8: Commit**

```bash
git add backend/app/models/business_profile.py backend/app/schemas/business_profile.py backend/alembic/versions/0021_business_pay_instr.py backend/tests/unit/test_pdf_config.py
git commit -m "feat(pdf): add BusinessProfile.payment_instructions (model, migration, schema)"
```

---

### Task 3: Vendor fonts + default logo, and the assets loader

**Files:**
- Create: `backend/app/services/pdf/__init__.py` (temporary empty; replaced in Task 8)
- Create: `backend/app/services/pdf/assets.py`
- Create (binary): `backend/app/services/pdf/assets/fonts/inter.woff2`, `manrope.woff2`, `jetbrains-mono.woff2`
- Create (binary): `backend/app/services/pdf/assets/default-logo.png`
- Test: `backend/tests/unit/test_pdf_assets.py`

**Interfaces:**
- Produces: `font_face_css() -> str` (concatenated `@font-face` rules, base64 data URIs, cached); `default_logo_data_uri() -> str` (`data:image/png;base64,...`).

- [ ] **Step 1: Create the package + asset dirs and vendor the files**

```bash
mkdir -p backend/app/services/pdf/assets/fonts
: > backend/app/services/pdf/__init__.py
cp wenhao-logo.png backend/app/services/pdf/assets/default-logo.png
curl -fsSL -o backend/app/services/pdf/assets/fonts/inter.woff2 \
  https://cdn.jsdelivr.net/npm/@fontsource-variable/inter/files/inter-latin-wght-normal.woff2
curl -fsSL -o backend/app/services/pdf/assets/fonts/manrope.woff2 \
  https://cdn.jsdelivr.net/npm/@fontsource-variable/manrope/files/manrope-latin-wght-normal.woff2
curl -fsSL -o backend/app/services/pdf/assets/fonts/jetbrains-mono.woff2 \
  https://cdn.jsdelivr.net/npm/@fontsource-variable/jetbrains-mono/files/jetbrains-mono-latin-wght-normal.woff2
ls -la backend/app/services/pdf/assets/fonts/
```
Expected: three non-empty `.woff2` files (~30–120 KB each) and `default-logo.png`.

- [ ] **Step 2: Write the failing test**

```python
# backend/tests/unit/test_pdf_assets.py
from app.services.pdf.assets import font_face_css, default_logo_data_uri


def test_font_face_css_embeds_all_families():
    css = font_face_css()
    assert css.count("@font-face") == 3
    for family in ("Inter", "Manrope", "JetBrains Mono"):
        assert family in css
    assert "base64," in css
    assert "http://" not in css and "https://" not in css


def test_default_logo_is_data_uri():
    uri = default_logo_data_uri()
    assert uri.startswith("data:image/png;base64,")
    assert len(uri) > 100
```

- [ ] **Step 3: Run test to verify it fails**

Run: `cd backend && python -m pytest tests/unit/test_pdf_assets.py -v`
Expected: FAIL — `ModuleNotFoundError: app.services.pdf.assets`.

- [ ] **Step 4: Implement `assets.py`**

```python
# backend/app/services/pdf/assets.py
"""Load and cache bundled PDF assets (fonts, default logo) as data URIs.

Everything is embedded so the rendered HTML is fully self-contained — the
headless browser never needs network access.
"""
from __future__ import annotations

from base64 import b64encode
from functools import lru_cache
from pathlib import Path

_ASSETS = Path(__file__).parent / "assets"
_FONTS = _ASSETS / "fonts"

# family name -> vendored variable woff2 file (weight axis 100..900)
_FONT_FILES = {
    "Inter": "inter.woff2",
    "Manrope": "manrope.woff2",
    "JetBrains Mono": "jetbrains-mono.woff2",
}


@lru_cache(maxsize=1)
def font_face_css() -> str:
    rules = []
    for family, fname in _FONT_FILES.items():
        b64 = b64encode((_FONTS / fname).read_bytes()).decode("ascii")
        rules.append(
            f"@font-face{{font-family:'{family}';font-style:normal;"
            f"font-weight:100 900;font-display:swap;"
            f"src:url(data:font/woff2;base64,{b64}) format('woff2');}}"
        )
    return "\n".join(rules)


@lru_cache(maxsize=1)
def default_logo_data_uri() -> str:
    b64 = b64encode((_ASSETS / "default-logo.png").read_bytes()).decode("ascii")
    return "data:image/png;base64," + b64
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd backend && python -m pytest tests/unit/test_pdf_assets.py -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/app/services/pdf/__init__.py backend/app/services/pdf/assets.py backend/app/services/pdf/assets/ backend/tests/unit/test_pdf_assets.py
git commit -m "feat(pdf): vendor fonts + default logo and add assets loader"
```

---

### Task 4: Logo resolver

**Files:**
- Create: `backend/app/services/pdf/logo.py`
- Test: `backend/tests/unit/test_pdf_logo.py`

**Interfaces:**
- Consumes: `default_logo_data_uri()` (Task 3).
- Produces: `resolve_logo(business) -> str` — returns a `data:` URI. Order: `logo_url` already a `data:` URI → passthrough; `http(s)` → fetch+encode (fall back on error); otherwise the bundled default.

- [ ] **Step 1: Write the failing test**

```python
# backend/tests/unit/test_pdf_logo.py
from types import SimpleNamespace

from app.services.pdf.logo import resolve_logo


def test_passthrough_data_uri():
    biz = SimpleNamespace(logo_url="data:image/png;base64,AAAA")
    assert resolve_logo(biz) == "data:image/png;base64,AAAA"


def test_none_business_uses_default():
    assert resolve_logo(None).startswith("data:image/png;base64,")


def test_blank_logo_uses_default():
    biz = SimpleNamespace(logo_url=None)
    assert resolve_logo(biz).startswith("data:image/png;base64,")
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && python -m pytest tests/unit/test_pdf_logo.py -v`
Expected: FAIL — `ModuleNotFoundError: app.services.pdf.logo`.

- [ ] **Step 3: Implement `logo.py`**

```python
# backend/app/services/pdf/logo.py
"""Resolve the document logo to an embeddable data URI.

The Account page stores uploaded logos directly as base64 data URIs in
BusinessProfile.logo_url, so the common case is a passthrough. An http(s)
URL is fetched and encoded; anything else falls back to the bundled logo.
"""
from __future__ import annotations

import logging
from base64 import b64encode

import httpx

from app.services.pdf.assets import default_logo_data_uri

log = logging.getLogger(__name__)


def resolve_logo(business) -> str:
    url = getattr(business, "logo_url", None) if business is not None else None
    if not url:
        return default_logo_data_uri()
    if url.startswith("data:"):
        return url
    if url.startswith(("http://", "https://")):
        try:
            resp = httpx.get(url, timeout=5.0)
            resp.raise_for_status()
            mime = resp.headers.get("content-type", "image/png").split(";")[0]
            return f"data:{mime};base64," + b64encode(resp.content).decode("ascii")
        except Exception:
            log.warning("failed to fetch logo %s; using default", url, exc_info=True)
    return default_logo_data_uri()
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && python -m pytest tests/unit/test_pdf_logo.py -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/app/services/pdf/logo.py backend/tests/unit/test_pdf_logo.py
git commit -m "feat(pdf): add logo resolver (data-uri passthrough + fetch + default)"
```

---

### Task 5: Context builders

**Files:**
- Create: `backend/app/services/pdf/context.py`
- Test: `backend/tests/unit/test_pdf_context.py`

**Interfaces:**
- Consumes: `_discount_amount`, `_fmt_num`, `_fmt_pct` from `app.services.invoice_pdf`; `font_face_css` (Task 3); `resolve_logo` (Task 4).
- Produces:
  - `status_badge_class(status: str | None) -> str`
  - `build_invoice_context(invoice, customer, business=None) -> dict`
  - `build_quotation_context(quotation, customer, business=None) -> dict`
  - Both dicts share the shape consumed by the template (Task 6): keys `font_face_css, logo, doc_title, ref, status_label, status_class, meta (list[(k,v)]), from_name, from_lines, to_name, to_lines, items (list[{desc,qty,rate,amount}]), currency, summary_rows (list[{label,value,cls}]), hero_label, hero_amount, payment_instructions, notes, terms, foot_name, foot_meta`.

- [ ] **Step 1: Write the failing test**

```python
# backend/tests/unit/test_pdf_context.py
from datetime import date
from decimal import Decimal
from types import SimpleNamespace

from app.services.pdf.context import (
    build_invoice_context,
    build_quotation_context,
    status_badge_class,
)


def _biz():
    return SimpleNamespace(
        name="Wenhao Development Pte Ltd",
        address="1 Marina Boulevard\nSingapore 018989",
        contact_email="accounts@wenhao.dev", contact_phone="+65 6123 4567",
        invoice_title="Invoice", invoice_summary=None, logo_url=None,
        default_notes="Default note.", payment_instructions="DBS 012-345678-9",
    )


def _cust():
    return SimpleNamespace(
        name="Acme Pte Ltd", billing_address1="12 Raffles Place",
        billing_address2=None, billing_city="Singapore", billing_state=None,
        billing_postal_code="048619", billing_country="Singapore",
        billing_address=None, contact_email="ap@acme.example",
    )


def _inv(**over):
    inv = SimpleNamespace(
        invoice_number="WD-2026-0042", po_so_number=None, currency="SGD",
        subtotal=Decimal("11640.00"), discount_type="PERCENT",
        discount_value=Decimal("5"), amount=Decimal("11058.00"),
        balance_due=Decimal("6058.00"), status="PARTIALLY_PAID",
        issue_date=date(2026, 7, 23), due_date=date(2026, 8, 22),
        payment_terms="Net 30", notes=None,
        line_items=[SimpleNamespace(position=1, description="Work",
                    quantity=Decimal("1"), unit_price=Decimal("11640"),
                    amount=Decimal("11640"))],
    )
    for k, v in over.items():
        setattr(inv, k, v)
    return inv


def test_status_badge_class_maps_known_and_unknown():
    assert status_badge_class("PAID") == "paid"
    assert status_badge_class("PARTIALLY_PAID") == "partial"
    assert status_badge_class("OVERDUE") == "overdue"
    assert status_badge_class("DRAFT") == "draft"
    assert status_badge_class("ACCEPTED") == "paid"       # quotation
    assert status_badge_class("weird") == "sent"          # default


def test_invoice_context_discount_and_paid():
    ctx = build_invoice_context(_inv(), _cust(), _biz())
    assert ctx["doc_title"] == "INVOICE"
    assert ctx["ref"] == "WD-2026-0042"
    assert ctx["status_class"] == "partial"
    labels = {r["label"]: r["value"] for r in ctx["summary_rows"]}
    assert labels["Subtotal"] == "11,640.00"
    assert labels["Discount (5%)"] == "−582.00"           # 5% of 11,640
    assert labels["Total"] == "11,058.00"
    assert labels["Paid"] == "−5,000.00"                  # 11,058 − 6,058
    assert ctx["hero_label"] == "Total Due"
    assert ctx["hero_amount"] == "6,058.00"
    assert ctx["payment_instructions"] == "DBS 012-345678-9"
    assert ctx["notes"] == "Default note."                # falls back to business
    assert "@font-face" in ctx["font_face_css"]
    assert ctx["logo"].startswith("data:image/png;base64,")


def test_invoice_context_no_payments_omits_paid_and_total():
    ctx = build_invoice_context(
        _inv(amount=Decimal("11058.00"), balance_due=Decimal("11058.00")),
        _cust(), _biz(),
    )
    labels = [r["label"] for r in ctx["summary_rows"]]
    assert "Paid" not in labels
    assert "Total" not in labels
    assert ctx["hero_amount"] == "11,058.00"


def test_quotation_context_total_hero():
    q = SimpleNamespace(
        quotation_number="Q-2026-0007", po_so_number=None, currency="SGD",
        subtotal=Decimal("5000.00"), discount_type=None, discount_value=None,
        amount=Decimal("5000.00"), status="SENT",
        issue_date=date(2026, 7, 23), valid_until=date(2026, 8, 6),
        payment_terms="Net 14", notes="Quote note",
        line_items=[SimpleNamespace(position=1, description="Scope",
                    quantity=Decimal("1"), unit_price=Decimal("5000"),
                    amount=Decimal("5000"))],
    )
    ctx = build_quotation_context(q, _cust(), _biz())
    assert ctx["doc_title"] == "QUOTATION"
    assert ctx["hero_label"] == "Total"
    assert ctx["hero_amount"] == "5,000.00"
    assert ctx["meta"][2][0] == "Valid Until"
    assert ctx["notes"] == "Quote note"                   # invoice/quote note wins
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && python -m pytest tests/unit/test_pdf_context.py -v`
Expected: FAIL — `ModuleNotFoundError: app.services.pdf.context`.

- [ ] **Step 3: Implement `context.py`**

```python
# backend/app/services/pdf/context.py
"""Normalize an invoice or quotation (plus customer/business) into a single
context dict consumed by templates/document.html.j2."""
from __future__ import annotations

from datetime import date
from decimal import Decimal

from app.services.invoice_pdf import _discount_amount, _fmt_num, _fmt_pct
from app.services.pdf.assets import font_face_css
from app.services.pdf.logo import resolve_logo

_STATUS_BADGE = {
    # invoices
    "DRAFT": "draft", "SENT": "sent", "PAID": "paid",
    "PARTIALLY_PAID": "partial", "PARTIAL": "partial",
    "AWAITING_FINALIZATION": "partial", "OVERDUE": "overdue", "VOID": "draft",
    # quotations
    "ACCEPTED": "paid", "DECLINED": "overdue", "EXPIRED": "overdue",
    "INVOICED": "sent",
}


def status_badge_class(status: str | None) -> str:
    return _STATUS_BADGE.get((status or "").upper(), "sent")


def _status_label(status: str | None) -> str:
    return (status or "—").replace("_", " ").title()


def _fmt_date(d: date | None) -> str:
    return d.strftime("%d %b %Y") if d else "—"


def _addr_lines(customer) -> list[str]:
    if customer is None:
        return []
    lines: list[str] = []
    for attr in ("billing_address1", "billing_address2"):
        v = getattr(customer, attr, None)
        if v:
            lines.append(v)
    city = ", ".join(
        p for p in (
            getattr(customer, "billing_city", None),
            getattr(customer, "billing_state", None),
            getattr(customer, "billing_postal_code", None),
        ) if p
    )
    if city:
        lines.append(city)
    if getattr(customer, "billing_country", None):
        lines.append(customer.billing_country)
    if not lines and getattr(customer, "billing_address", None):
        lines = customer.billing_address.splitlines()
    return lines


def _business_lines(business) -> list[str]:
    if business is None:
        return []
    lines: list[str] = []
    if getattr(business, "address", None):
        lines += [ln for ln in business.address.splitlines() if ln.strip()]
    contact = " · ".join(
        p for p in (
            getattr(business, "contact_email", None),
            getattr(business, "contact_phone", None),
        ) if p
    )
    if contact:
        lines.append(contact)
    return lines


def _item(li) -> dict:
    return {
        "desc": li.description,
        "qty": f"{Decimal(li.quantity):g}",
        "rate": _fmt_num(li.unit_price),
        "amount": _fmt_num(li.amount),
    }


def _discount_row(discount_type, discount_value, subtotal):
    disc = _discount_amount(discount_type, discount_value, subtotal)
    if disc is None:
        return None
    suffix = f" ({_fmt_pct(discount_value)}%)" if discount_type == "PERCENT" else ""
    return {"label": f"Discount{suffix}", "value": f"−{_fmt_num(disc)}", "cls": "discount"}


def _common(doc, customer, business) -> dict:
    to_lines = _addr_lines(customer)
    if getattr(customer, "contact_email", None):
        to_lines = to_lines + [customer.contact_email]
    return {
        "font_face_css": font_face_css(),
        "logo": resolve_logo(business),
        "status_label": _status_label(doc.status),
        "status_class": status_badge_class(doc.status),
        "from_name": getattr(business, "name", None) or "—",
        "from_lines": _business_lines(business),
        "to_name": getattr(customer, "name", None) or "—",
        "to_lines": to_lines,
        "items": [_item(li) for li in doc.line_items],
        "currency": doc.currency,
        "payment_instructions": getattr(business, "payment_instructions", None),
        "notes": doc.notes or getattr(business, "default_notes", None),
        "terms": doc.payment_terms,
        "foot_name": getattr(business, "name", None) or "",
        "foot_meta": [
            p for p in (
                getattr(business, "contact_email", None),
                getattr(business, "contact_phone", None),
            ) if p
        ],
    }


def build_invoice_context(invoice, customer, business=None) -> dict:
    ctx = _common(invoice, customer, business)
    rows = [{"label": "Subtotal", "value": _fmt_num(invoice.subtotal), "cls": "subtotal"}]
    disc = _discount_row(invoice.discount_type, invoice.discount_value, invoice.subtotal)
    if disc:
        rows.append(disc)
    paid = None
    if invoice.amount is not None and invoice.balance_due is not None:
        p = Decimal(invoice.amount) - Decimal(invoice.balance_due)
        if p > 0:
            paid = p.quantize(Decimal("0.01"))
    if paid is not None:
        rows.append({"label": "Total", "value": _fmt_num(invoice.amount), "cls": "total"})
        rows.append({"label": "Paid", "value": f"−{_fmt_num(paid)}", "cls": "paid"})
    ctx.update({
        "doc_title": (getattr(business, "invoice_title", None) or "INVOICE").upper(),
        "ref": invoice.invoice_number or "—",
        "meta": [
            ("Invoice No.", invoice.invoice_number or "—"),
            ("Issue Date", _fmt_date(invoice.issue_date)),
            ("Due Date", _fmt_date(invoice.due_date)),
        ],
        "summary_rows": rows,
        "hero_label": "Total Due",
        "hero_amount": _fmt_num(invoice.balance_due),
    })
    return ctx


def build_quotation_context(quotation, customer, business=None) -> dict:
    ctx = _common(quotation, customer, business)
    rows = [{"label": "Subtotal", "value": _fmt_num(quotation.subtotal), "cls": "subtotal"}]
    disc = _discount_row(quotation.discount_type, quotation.discount_value, quotation.subtotal)
    if disc:
        rows.append(disc)
    ctx.update({
        "doc_title": "QUOTATION",
        "ref": quotation.quotation_number or "—",
        "meta": [
            ("Quote No.", quotation.quotation_number or "—"),
            ("Issue Date", _fmt_date(quotation.issue_date)),
            ("Valid Until", _fmt_date(quotation.valid_until)),
        ],
        "summary_rows": rows,
        "hero_label": "Total",
        "hero_amount": _fmt_num(quotation.amount),
    })
    return ctx
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && python -m pytest tests/unit/test_pdf_context.py -v`
Expected: PASS (5 tests).

- [ ] **Step 5: Commit**

```bash
git add backend/app/services/pdf/context.py backend/tests/unit/test_pdf_context.py
git commit -m "feat(pdf): add invoice/quotation context builders"
```

---

### Task 6: Template + HTML render functions

**Files:**
- Create: `backend/app/services/pdf/templates/document.html.j2`
- Create: `backend/app/services/pdf/render.py`
- Test: `backend/tests/unit/test_pdf_render_html.py`

**Interfaces:**
- Consumes: `build_invoice_context` / `build_quotation_context` (Task 5).
- Produces: `render_invoice_html(invoice, customer, business=None) -> str`, `render_quotation_html(quotation, customer, business=None) -> str` — self-contained HTML strings.

- [ ] **Step 1: Write the failing test**

```python
# backend/tests/unit/test_pdf_render_html.py
from datetime import date
from decimal import Decimal
from types import SimpleNamespace

from app.services.pdf.render import render_invoice_html, render_quotation_html


def _biz():
    return SimpleNamespace(
        name="Wenhao Development Pte Ltd", address="1 Marina Boulevard\nSingapore 018989",
        contact_email="accounts@wenhao.dev", contact_phone="+65 6123 4567",
        invoice_title="Invoice", invoice_summary=None, logo_url=None,
        default_notes=None, payment_instructions="DBS 012-345678-9",
    )


def _cust():
    return SimpleNamespace(
        name="Acme Pte Ltd", billing_address1="12 Raffles Place", billing_address2=None,
        billing_city="Singapore", billing_state=None, billing_postal_code="048619",
        billing_country="Singapore", billing_address=None, contact_email="ap@acme.example",
    )


def _inv():
    return SimpleNamespace(
        invoice_number="WD-2026-0042", po_so_number=None, currency="SGD",
        subtotal=Decimal("11640.00"), discount_type="PERCENT", discount_value=Decimal("5"),
        amount=Decimal("11058.00"), balance_due=Decimal("6058.00"), status="PARTIALLY_PAID",
        issue_date=date(2026, 7, 23), due_date=date(2026, 8, 22),
        payment_terms="Net 30", notes=None,
        line_items=[SimpleNamespace(position=1, description="Frontend engineering",
                    quantity=Decimal("40"), unit_price=Decimal("85"), amount=Decimal("3400"))],
    )


def test_invoice_html_is_self_contained_and_populated():
    html = render_invoice_html(_inv(), _cust(), _biz())
    assert "WD-2026-0042" in html
    assert "Total Due" in html
    assert "Frontend engineering" in html
    assert "DBS 012-345678-9" in html
    assert "@font-face" in html
    assert "data:image/png;base64," in html
    # fully self-contained: no external stylesheet/font/script hosts
    assert "https://" not in html
    assert "http://" not in html


def test_quotation_html_renders_total():
    q = SimpleNamespace(
        quotation_number="Q-2026-0007", po_so_number=None, currency="SGD",
        subtotal=Decimal("5000.00"), discount_type=None, discount_value=None,
        amount=Decimal("5000.00"), status="SENT", issue_date=date(2026, 7, 23),
        valid_until=date(2026, 8, 6), payment_terms="Net 14", notes=None,
        line_items=[SimpleNamespace(position=1, description="Scope",
                    quantity=Decimal("1"), unit_price=Decimal("5000"), amount=Decimal("5000"))],
    )
    html = render_quotation_html(q, _cust(), _biz())
    assert "QUOTATION" in html
    assert "Valid Until" in html
    assert "5,000.00" in html
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && python -m pytest tests/unit/test_pdf_render_html.py -v`
Expected: FAIL — `ModuleNotFoundError: app.services.pdf.render`.

- [ ] **Step 3: Create the template**

Create `backend/app/services/pdf/templates/document.html.j2` with this exact content (the approved design, parameterized; fonts embedded via `{{ font_face_css }}`, logo full-height, no external hosts, no screen-only legend):

```html
<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8" />
<title>{{ doc_title | title }} {{ ref }}</title>
<style>
  {{ font_face_css }}
  :root{
    --navy:#071A3D; --blue:#145BFF; --azure:#008FF5; --cyan:#00D9F5;
    --bg:#F7FAFC; --soft:#EAF1F8; --ink:#14213D; --muted:#64748B;
    --border:#D7E1EC; --paid:#0DBF9B; --overdue:#EF5B5B; --draft:#94A3B8;
    --grad:linear-gradient(135deg,#145BFF 0%,#008FF5 55%,#00D9F5 100%);
    --sans:"Inter", ui-sans-serif, system-ui, sans-serif;
    --display:"Manrope", "Inter", sans-serif;
    --mono:"JetBrains Mono", ui-monospace, monospace;
  }
  *{box-sizing:border-box}
  html,body{margin:0}
  body{font-family:var(--sans); color:var(--ink); background:var(--bg);
    -webkit-font-smoothing:antialiased; padding:40px 20px; line-height:1.5;}
  .sheet{width:100%; max-width:840px; margin:0 auto; background:#fff;
    border:1px solid var(--border); border-radius:14px; overflow:hidden;}
  .head{position:relative; background:var(--navy); color:#fff; padding:20px 40px; overflow:hidden;}
  .head::before{content:""; position:absolute; inset:0 0 auto 0; height:4px; background:var(--grad);}
  .head::after{content:""; position:absolute; top:-120px; right:-120px; width:340px; height:340px;
    background:radial-gradient(circle at center, rgba(0,143,245,.30), rgba(0,217,245,0) 62%); pointer-events:none;}
  .head-row{position:relative; display:flex; justify-content:space-between; align-items:center; gap:24px}
  .brand img{height:76px; width:auto; display:block}
  .doc{text-align:right}
  .doc h1{font-family:var(--display); font-weight:800; font-size:32px; margin:0;
    letter-spacing:.14em; line-height:1; color:#fff;}
  .doc .refno{font-family:var(--mono); font-size:12.5px; color:#AFC6E8; margin-top:10px; display:inline-block;}
  .doc .badge{margin-top:12px}
  .badge{display:inline-flex; align-items:center; gap:7px; font-family:var(--display);
    font-weight:700; font-size:11px; letter-spacing:.09em; text-transform:uppercase;
    padding:6px 12px; border-radius:999px; border:1px solid transparent; white-space:nowrap;}
  .badge .dot{width:7px; height:7px; border-radius:50%; background:currentColor}
  .badge--draft  {color:#CBD5E1; background:rgba(148,163,184,.14); border-color:rgba(148,163,184,.35)}
  .badge--sent   {color:#7FB2FF; background:rgba(20,91,255,.16);  border-color:rgba(20,91,255,.45)}
  .badge--paid   {color:#0DBF9B; background:rgba(13,191,155,.14); border-color:rgba(13,191,155,.45)}
  .badge--partial{color:#00D9F5; background:rgba(0,143,245,.16);  border-color:rgba(0,217,245,.45)}
  .badge--overdue{color:#FF8A8A; background:rgba(239,91,91,.16);  border-color:rgba(239,91,91,.5)}
  .badge.on-light.badge--draft  {color:#5B6b7f; background:#EEF2F7}
  .badge.on-light.badge--sent   {color:#145BFF; background:#E7EFFF}
  .badge.on-light.badge--paid   {color:#0a8f76; background:#E1F7F1}
  .badge.on-light.badge--partial{color:#0378c4; background:#E2F1FB}
  .badge.on-light.badge--overdue{color:#d83b3b; background:#FCE9E9}
  .body{padding:16px 40px 4px}
  .section-label{font-family:var(--display); font-weight:700; font-size:11px; letter-spacing:.12em;
    text-transform:uppercase; color:var(--muted); margin:0 0 6px;}
  .meta{display:grid; grid-template-columns:repeat(4,1fr); border:1px solid var(--border);
    border-radius:12px; overflow:hidden; background:var(--soft); margin-bottom:12px;}
  .meta .cell{padding:10px 16px; background:#fff}
  .meta .cell + .cell{border-left:1px solid var(--border)}
  .meta .k{font-size:10.5px; letter-spacing:.1em; text-transform:uppercase; color:var(--muted); font-weight:600}
  .meta .v{font-family:var(--mono); font-size:14px; color:var(--ink); margin-top:6px; font-weight:500}
  .meta .cell.status{display:flex; flex-direction:column}
  .parties{display:grid; grid-template-columns:1fr 1fr; gap:16px; margin-bottom:12px}
  .party{border:1px solid var(--border); border-radius:12px; padding:12px 16px; background:#fff}
  .party.to{background:linear-gradient(180deg,#FBFDFF,#F4F8FD)}
  .party .name{font-family:var(--display); font-weight:700; font-size:16px; color:var(--navy); margin:1px 0 6px}
  .party .line{font-size:13px; color:var(--muted); margin:1px 0}
  .table-wrap{border:1px solid var(--border); border-radius:12px; overflow:hidden; margin-bottom:12px}
  table{width:100%; border-collapse:collapse}
  thead th{font-family:var(--display); font-weight:700; font-size:10.5px; letter-spacing:.08em;
    text-transform:uppercase; color:#C9D8EC; background:var(--navy); text-align:left; padding:9px 18px;}
  thead th.num{text-align:right}
  tbody td{padding:8px 18px; border-top:1px solid var(--border); font-size:13.5px; vertical-align:top}
  tbody tr:nth-child(even) td{background:#FBFCFE}
  td .desc{font-weight:600; color:var(--ink)}
  td.num{text-align:right; font-family:var(--mono); font-size:13px; color:#334155; white-space:nowrap}
  td.amt{text-align:right; font-family:var(--mono); font-size:13px; font-weight:600; color:var(--navy); white-space:nowrap}
  td.idx{color:var(--muted); font-family:var(--mono); font-size:12px; width:34px}
  .lower{display:grid; grid-template-columns:1.1fr .9fr; gap:22px; align-items:start; margin-bottom:16px}
  .terms{display:grid; grid-template-columns:1fr 1fr; gap:16px}
  .panel{border:1px solid var(--border); border-radius:12px; padding:15px 18px; background:#fff}
  .panel.pay{background:var(--soft); border-color:#CBDAEC}
  .note-text{font-size:12.5px; color:#475569; margin:0; white-space:pre-wrap}
  .summary{border:1px solid var(--border); border-radius:12px; overflow:hidden; background:#fff}
  .summary .row{display:flex; justify-content:space-between; align-items:center; padding:9px 18px; font-size:13.5px}
  .summary .row + .row{border-top:1px solid #EDF2F8}
  .summary .row .lbl{color:var(--muted)}
  .summary .row .val{font-family:var(--mono); font-size:13px; color:var(--ink); font-weight:500}
  .summary .row.discount .val{color:var(--paid)}
  .summary .row.paid .val{color:var(--azure)}
  .summary .row.total .lbl,.summary .row.total .val{color:var(--ink); font-weight:700}
  .total-due{background:var(--grad); color:#fff; padding:16px 20px;
    display:flex; justify-content:space-between; align-items:center;}
  .total-due .lbl{font-family:var(--display); font-weight:700; font-size:12px; letter-spacing:.1em;
    text-transform:uppercase; opacity:.95; white-space:nowrap}
  .total-due .amt{font-family:var(--mono); font-weight:600; font-size:26px}
  .total-due .amt .cur{font-family:var(--display); font-size:13px; font-weight:700; opacity:.9; margin-left:6px}
  .foot{margin-top:14px; background:var(--navy); color:#9FB4D6; padding:13px 40px;
    display:flex; justify-content:space-between; align-items:center; gap:16px; flex-wrap:wrap; font-size:12px;}
  .foot .fbrand{font-family:var(--display); font-weight:700; color:#fff}
  .foot .fmeta{font-family:var(--mono); font-size:11.5px; display:flex; gap:14px; flex-wrap:wrap}
  @page{size:A4; margin:0}
  @media print{
    html,body{background:#fff; padding:0}
    .sheet{max-width:none; width:auto; border:none; border-radius:0}
    .party,.panel,.meta,.table-wrap,.summary,.terms{break-inside:avoid}
    tr{break-inside:avoid}
  }
  *{ -webkit-print-color-adjust:exact; print-color-adjust:exact; }
</style>
</head>
<body>
  <div class="sheet">
    <header class="head">
      <div class="head-row">
        <div class="brand"><img src="{{ logo }}" alt="{{ from_name }}"></div>
        <div class="doc">
          <h1>{{ doc_title }}</h1>
          <span class="refno">{{ ref }}</span>
          <div class="badge badge--{{ status_class }}"><span class="dot"></span>{{ status_label }}</div>
        </div>
      </div>
    </header>
    <div class="body">
      <div class="meta">
        {% for k, v in meta %}
        <div class="cell"><div class="k">{{ k }}</div><div class="v">{{ v }}</div></div>
        {% endfor %}
        <div class="cell status"><div class="k">Status</div>
          <div class="badge on-light badge--{{ status_class }}" style="align-self:flex-start; margin-top:6px"><span class="dot"></span>{{ status_label }}</div>
        </div>
      </div>
      <div class="parties">
        <div class="party from">
          <div class="section-label">Bill From</div>
          <div class="name">{{ from_name }}</div>
          {% for line in from_lines %}<div class="line">{{ line }}</div>{% endfor %}
        </div>
        <div class="party to">
          <div class="section-label">Bill To</div>
          <div class="name">{{ to_name }}</div>
          {% for line in to_lines %}<div class="line">{{ line }}</div>{% endfor %}
        </div>
      </div>
      <div class="table-wrap">
        <table>
          <thead><tr>
            <th class="idx">#</th><th>Description</th>
            <th class="num">Qty</th><th class="num">Rate</th>
            <th class="num">Amount · {{ currency }}</th>
          </tr></thead>
          <tbody>
            {% for it in items %}
            <tr>
              <td class="idx">{{ "%02d"|format(loop.index) }}</td>
              <td><div class="desc">{{ it.desc }}</div></td>
              <td class="num">{{ it.qty }}</td>
              <td class="num">{{ it.rate }}</td>
              <td class="amt">{{ it.amount }}</td>
            </tr>
            {% endfor %}
          </tbody>
        </table>
      </div>
      <div class="lower">
        {% if payment_instructions %}
        <div class="panel pay">
          <div class="section-label">Payment Instructions</div>
          <p class="note-text">{{ payment_instructions }}</p>
        </div>
        {% else %}<div></div>{% endif %}
        <div class="summary">
          {% for r in summary_rows %}
          <div class="row {{ r.cls }}"><span class="lbl">{{ r.label }}</span><span class="val">{{ r.value }}</span></div>
          {% endfor %}
          <div class="total-due">
            <span class="lbl">{{ hero_label }}</span>
            <span class="amt">{{ hero_amount }}<span class="cur">{{ currency }}</span></span>
          </div>
        </div>
      </div>
      {% if notes or terms %}
      <div class="terms">
        {% if notes %}<div class="panel"><div class="section-label">Notes</div><p class="note-text">{{ notes }}</p></div>{% endif %}
        {% if terms %}<div class="panel"><div class="section-label">Terms</div><p class="note-text">{{ terms }}</p></div>{% endif %}
      </div>
      {% endif %}
    </div>
    <footer class="foot">
      <div class="fbrand">{{ foot_name }}</div>
      <div class="fmeta">{% for m in foot_meta %}<span>{{ m }}</span>{% endfor %}</div>
    </footer>
  </div>
</body>
</html>
```

- [ ] **Step 4: Implement `render.py`**

```python
# backend/app/services/pdf/render.py
"""Render invoice/quotation context into self-contained HTML via Jinja2."""
from __future__ import annotations

from pathlib import Path

from jinja2 import Environment, FileSystemLoader, select_autoescape

from app.services.pdf.context import build_invoice_context, build_quotation_context

_env = Environment(
    loader=FileSystemLoader(str(Path(__file__).parent / "templates")),
    autoescape=select_autoescape(["html", "j2"]),
)


def render_invoice_html(invoice, customer, business=None) -> str:
    ctx = build_invoice_context(invoice, customer, business)
    return _env.get_template("document.html.j2").render(**ctx)


def render_quotation_html(quotation, customer, business=None) -> str:
    ctx = build_quotation_context(quotation, customer, business)
    return _env.get_template("document.html.j2").render(**ctx)
```

- [ ] **Step 5: Run test to verify it passes**

Run: `cd backend && python -m pytest tests/unit/test_pdf_render_html.py -v`
Expected: PASS (2 tests). If `http://`/`https://` assertions fail, ensure no asset/font slipped in as a URL.

- [ ] **Step 6: Commit**

```bash
git add backend/app/services/pdf/templates/document.html.j2 backend/app/services/pdf/render.py backend/tests/unit/test_pdf_render_html.py
git commit -m "feat(pdf): add shared Jinja template and HTML render functions"
```

---

### Task 7: Playwright engine

**Files:**
- Create: `backend/app/services/pdf/engine.py`
- Test: `backend/tests/integration/test_pdf_engine.py`

**Interfaces:**
- Consumes: `Settings.pdf_render_timeout_ms`.
- Produces: `html_to_pdf(html: str) -> bytes` — A4 PDF bytes from self-contained HTML.

- [ ] **Step 1: Write the failing (skip-guarded) integration test**

```python
# backend/tests/integration/test_pdf_engine.py
import pytest

pw = pytest.importorskip("playwright.sync_api")


def _chromium_available() -> bool:
    try:
        with pw.sync_playwright() as p:
            b = p.chromium.launch(args=["--no-sandbox"])
            b.close()
        return True
    except Exception:
        return False


pytestmark = pytest.mark.skipif(
    not _chromium_available(), reason="Chromium not installed (run: playwright install chromium)"
)


def test_html_to_pdf_returns_pdf_bytes():
    from app.services.pdf.engine import html_to_pdf
    out = html_to_pdf("<html><body><h1>Hello</h1></body></html>")
    assert out[:5] == b"%PDF-"
    assert len(out) > 500
```

- [ ] **Step 2: Run test to verify it fails (or skips)**

Run: `cd backend && python -m pytest tests/integration/test_pdf_engine.py -v`
Expected: FAIL (`ModuleNotFoundError: app.services.pdf.engine`) if Chromium is present; SKIP if Chromium is absent. Either is acceptable to proceed — implement next.

- [ ] **Step 3: Implement `engine.py`**

```python
# backend/app/services/pdf/engine.py
"""HTML→PDF via headless Chromium (Playwright), launched per request.

Sync Playwright is created and destroyed within one call, so it is safe
inside FastAPI's sync-endpoint threadpool (no cross-thread object sharing).
"""
from __future__ import annotations

from playwright.sync_api import sync_playwright

from app.config import get_settings


def html_to_pdf(html: str) -> bytes:
    timeout = get_settings().pdf_render_timeout_ms
    with sync_playwright() as p:
        browser = p.chromium.launch(args=["--no-sandbox"])
        try:
            page = browser.new_page()
            page.set_default_timeout(timeout)
            page.set_content(html, wait_until="load")
            return page.pdf(
                format="A4",
                print_background=True,
                margin={"top": "0", "right": "0", "bottom": "0", "left": "0"},
                prefer_css_page_size=True,
            )
        finally:
            browser.close()
```

- [ ] **Step 4: Install Chromium locally and run the test**

Run: `cd backend && python -m playwright install chromium && python -m pytest tests/integration/test_pdf_engine.py -v`
Expected: PASS. (If you cannot install Chromium locally, the test SKIPS — that is acceptable; it will run in Docker per Task 11.)

- [ ] **Step 5: Commit**

```bash
git add backend/app/services/pdf/engine.py backend/tests/integration/test_pdf_engine.py
git commit -m "feat(pdf): add Playwright HTML→PDF engine"
```

---

### Task 8: Façade + reportlab fallback

**Files:**
- Modify: `backend/app/services/pdf/__init__.py` (replace the empty placeholder from Task 3)
- Test: `backend/tests/unit/test_pdf_facade.py`

**Interfaces:**
- Consumes: `render_invoice_html`/`render_quotation_html` (Task 6), `html_to_pdf` (Task 7), `Settings.pdf_engine`, and the retained reportlab functions `app.services.invoice_pdf.render_invoice_pdf(invoice, customer, business=None)` and `app.services.quotation_pdf.render_quotation_pdf(quotation, customer)`.
- Produces: `render_invoice_pdf(invoice, customer, business=None) -> bytes`, `render_quotation_pdf(quotation, customer, business=None) -> bytes`.

- [ ] **Step 1: Write the failing test**

```python
# backend/tests/unit/test_pdf_facade.py
import app.services.pdf as pdf


def test_falls_back_to_reportlab_on_engine_error(monkeypatch):
    monkeypatch.setattr(pdf, "html_to_pdf", lambda html: (_ for _ in ()).throw(RuntimeError("boom")))
    monkeypatch.setattr(pdf, "render_invoice_html", lambda *a, **k: "<html></html>")
    called = {}
    monkeypatch.setattr(pdf, "_reportlab_invoice", lambda inv, cust, biz=None: called.setdefault("hit", True) or b"%PDF-RL")
    out = pdf.render_invoice_pdf(object(), object(), None)
    assert called.get("hit") is True
    assert out == b"%PDF-RL"


def test_engine_reportlab_skips_html(monkeypatch):
    from app.config import get_settings
    get_settings.cache_clear()
    monkeypatch.setenv("PDF_ENGINE", "reportlab")
    monkeypatch.setattr(pdf, "render_invoice_html",
                        lambda *a, **k: (_ for _ in ()).throw(AssertionError("HTML path must not run")))
    monkeypatch.setattr(pdf, "_reportlab_invoice", lambda inv, cust, biz=None: b"%PDF-RL")
    out = pdf.render_invoice_pdf(object(), object(), None)
    assert out == b"%PDF-RL"
    get_settings.cache_clear()


def test_html_path_used_on_success(monkeypatch):
    from app.config import get_settings
    get_settings.cache_clear()
    monkeypatch.setattr(pdf, "render_invoice_html", lambda *a, **k: "<html></html>")
    monkeypatch.setattr(pdf, "html_to_pdf", lambda html: b"%PDF-HTML")
    out = pdf.render_invoice_pdf(object(), object(), None)
    assert out == b"%PDF-HTML"
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && python -m pytest tests/unit/test_pdf_facade.py -v`
Expected: FAIL — the façade functions/names don't exist yet in `app.services.pdf`.

- [ ] **Step 3: Implement the façade** (overwrite `backend/app/services/pdf/__init__.py`)

```python
# backend/app/services/pdf/__init__.py
"""PDF façade: HTML→PDF first, reportlab as automatic fallback.

Set settings.pdf_engine="reportlab" to force the legacy renderer.
"""
from __future__ import annotations

import logging

from app.config import get_settings
from app.services.invoice_pdf import render_invoice_pdf as _reportlab_invoice
from app.services.quotation_pdf import render_quotation_pdf as _reportlab_quotation
from app.services.pdf.engine import html_to_pdf
from app.services.pdf.render import render_invoice_html, render_quotation_html

log = logging.getLogger(__name__)

__all__ = ["render_invoice_pdf", "render_quotation_pdf"]


def render_invoice_pdf(invoice, customer, business=None) -> bytes:
    if get_settings().pdf_engine == "reportlab":
        return _reportlab_invoice(invoice, customer, business)
    try:
        return html_to_pdf(render_invoice_html(invoice, customer, business))
    except Exception:
        log.exception("HTML invoice render failed; falling back to reportlab")
        return _reportlab_invoice(invoice, customer, business)


def render_quotation_pdf(quotation, customer, business=None) -> bytes:
    if get_settings().pdf_engine == "reportlab":
        return _reportlab_quotation(quotation, customer)
    try:
        return html_to_pdf(render_quotation_html(quotation, customer, business))
    except Exception:
        log.exception("HTML quotation render failed; falling back to reportlab")
        return _reportlab_quotation(quotation, customer)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && python -m pytest tests/unit/test_pdf_facade.py -v`
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add backend/app/services/pdf/__init__.py backend/tests/unit/test_pdf_facade.py
git commit -m "feat(pdf): add façade with automatic reportlab fallback"
```

---

### Task 9: Wire the routers

**Files:**
- Modify: `backend/app/api/v1/routers/invoices.py:41` (import path)
- Modify: `backend/app/api/v1/routers/quotations.py:25,174-175,198,206` (import + pass business)
- Test: `backend/tests/unit/test_pdf_wiring.py`

**Interfaces:**
- Consumes: `app.services.pdf.render_invoice_pdf` / `render_quotation_pdf` (Task 8).

- [ ] **Step 1: Write the failing test**

```python
# backend/tests/unit/test_pdf_wiring.py
import app.api.v1.routers.invoices as inv
import app.api.v1.routers.quotations as quo
import app.services.pdf as pdf


def test_routers_use_facade():
    assert inv.render_invoice_pdf is pdf.render_invoice_pdf
    assert quo.render_quotation_pdf is pdf.render_quotation_pdf
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && python -m pytest tests/unit/test_pdf_wiring.py -v`
Expected: FAIL — routers still import the reportlab modules, so identities differ.

- [ ] **Step 3: Repoint the invoice router import**

In `backend/app/api/v1/routers/invoices.py`, change line 41:

```python
from app.services.pdf import render_invoice_pdf
```

(The endpoints already load `business = db.get(BusinessProfile, 1)` and call `render_invoice_pdf(invoice, customer, business)` — no other change.)

- [ ] **Step 4: Repoint the quotation router import and pass business**

In `backend/app/api/v1/routers/quotations.py`:

Change line 25:
```python
from app.services.pdf import render_quotation_pdf
```

Add the `BusinessProfile` import near the other model imports (match existing import style at the top of the file):
```python
from app.models.business_profile import BusinessProfile
```

In `quotation_pdf` (GET) replace lines 174-175:
```python
    customer = db.get(Customer, q.customer_id) if q.customer_id else None
    business = db.get(BusinessProfile, 1)
    pdf_bytes = render_quotation_pdf(q, customer, business)
```

In `send` (POST) replace line 198 area + 206:
```python
    customer = db.get(Customer, q.customer_id) if q.customer_id else None
    business = db.get(BusinessProfile, 1)
```
```python
    pdf_bytes = render_quotation_pdf(q, customer, business)
```

- [ ] **Step 5: Run test + import-check**

Run: `cd backend && python -m pytest tests/unit/test_pdf_wiring.py -v && python -c "import app.api.v1.routers.invoices, app.api.v1.routers.quotations; print('ok')"`
Expected: PASS and `ok`.

- [ ] **Step 6: Commit**

```bash
git add backend/app/api/v1/routers/invoices.py backend/app/api/v1/routers/quotations.py backend/tests/unit/test_pdf_wiring.py
git commit -m "feat(pdf): route invoice/quotation PDFs through the HTML façade"
```

---

### Task 10: Account page — payment instructions field

**Files:**
- Modify: `frontend/src/views/AccountView.vue` (interface ~75-84, ref ~85-94, loadCompany ~104-113, template company form ~356)

**Interfaces:** none (frontend). The existing `saveCompany` serializes every key of `company.value`, so the new field is persisted automatically via `PUT /business-profile`.

- [ ] **Step 1: Add the field to the TS interface**

In `frontend/src/views/AccountView.vue`, in the `BusinessProfile` interface (around line 83, after `default_notes: string | null;`), add:
```ts
  payment_instructions: string | null;
```

- [ ] **Step 2: Add to the reactive default**

In the `company` ref default object (around line 93, after `default_notes: "",`), add:
```ts
  payment_instructions: "",
```

- [ ] **Step 3: Map it in loadCompany**

In `loadCompany`, in the `company.value = { ... }` mapping (around line 112, after `default_notes: data.default_notes ?? "",`), add:
```ts
      payment_instructions: data.payment_instructions ?? "",
```

- [ ] **Step 4: Add the textarea to the company form**

In the company `<form>`, after the `default_notes` field block (around line 356), add a label+Textarea consistent with the surrounding markup:
```html
            <label class="field">
              Payment instructions
              <small class="hint">Bank / PayNow details shown on invoices &amp; quotations.</small>
              <Textarea v-model="company.payment_instructions" rows="4" />
            </label>
```
(If the surrounding fields don't use a `.hint` element, drop the `<small>` line to match the existing pattern.)

- [ ] **Step 5: Typecheck / build**

Run: `cd frontend && npm run build`
Expected: `vue-tsc` + `vite build` succeed with no type errors.

- [ ] **Step 6: Commit**

```bash
git add frontend/src/views/AccountView.vue
git commit -m "feat(pdf): add payment instructions field to Account company form"
```

---

### Task 11: Dockerfile — install Chromium

**Files:**
- Modify: `backend/Dockerfile:18` (after pip install)

- [ ] **Step 1: Add the Chromium install layer**

In `backend/Dockerfile`, immediately after `RUN pip install -e ".[dev]"` (line 18), add:

```dockerfile
# Headless Chromium for HTML→PDF invoice/quotation rendering (Playwright).
RUN python -m playwright install --with-deps chromium
```

- [ ] **Step 2: Rebuild the backend image and verify Chromium is present**

Run: `docker compose build backend && docker compose run --rm backend python -m playwright install --dry-run chromium`
Expected: build succeeds; Chromium reported as already installed.

- [ ] **Step 3: End-to-end render check inside the container**

Run:
```bash
docker compose run --rm backend python -c "from app.services.pdf.engine import html_to_pdf; print(html_to_pdf('<h1>ok</h1>')[:5])"
```
Expected: `b'%PDF-'`.

- [ ] **Step 4: Commit**

```bash
git add backend/Dockerfile
git commit -m "build(pdf): install headless Chromium for HTML→PDF rendering"
```

---

### Task 12: Full-suite verification & one-page visual check

**Files:** none (verification only).

- [ ] **Step 1: Run the whole backend test suite**

Run: `cd backend && TEST_DATABASE_URL=postgresql+psycopg://billing:billing@localhost:5432/billing python -m pytest -q`
Expected: all pass; browser-dependent tests skip only if Chromium is absent. (Use your actual `TEST_DATABASE_URL`.)

- [ ] **Step 2: Lint**

Run: `cd backend && python -m ruff check app tests`
Expected: `All checks passed!`

- [ ] **Step 3: Visual one-page check**

Start the stack (`docker compose up`), open an invoice, and hit `GET /api/v1/invoices/{id}/pdf` (the app's PDF button). Confirm: renders on **one A4 page**, logo fills the masthead height, Total Due gradient hero present, no browser header/footer. Repeat for a quotation. If content overflows to a second page for a realistic invoice, tighten spacing in `document.html.j2` (reduce `.body`/panel paddings) — the approved prototype fits four line items on one page.

- [ ] **Step 4: Fallback smoke check**

Temporarily set `PDF_ENGINE=reportlab` in the backend environment, reload, and confirm the PDF endpoints still return a (legacy) PDF. Restore `PDF_ENGINE=html` (or unset).

- [ ] **Step 5: Final commit (if any tweaks were made)**

```bash
git add -A
git commit -m "test(pdf): verify full suite, one-page render, and reportlab fallback"
```

---

## Self-Review

**Spec coverage:**
- HTML→PDF pipeline (Playwright, per-request) → Tasks 7, 8. ✓
- reportlab automatic fallback + `pdf_engine` toggle → Tasks 1, 8. ✓
- Both invoices and quotations → Tasks 5, 6, 8, 9. ✓
- Logo from `logo_url` (data-URI passthrough) + bundled fallback, full masthead height → Tasks 3, 4, 6. ✓
- `BusinessProfile.payment_instructions` (model/migration/schema/frontend) → Tasks 2, 10. ✓
- Self-hosted fonts, self-contained HTML → Tasks 3, 6 (test asserts no `http(s)`). ✓
- No Tax row; Paid = amount − balance_due; data-driven summary → Task 5. ✓
- Docker Chromium → Task 11. ✓
- Testing (unit no-browser + skip-guarded render) → Tasks 1–8, 12. ✓
- Frontend/routers otherwise unchanged → Task 9 keeps signatures. ✓

**Placeholder scan:** No TBD/TODO; every code step contains complete code. ✓

**Type consistency:** Context keys produced in Task 5 (`summary_rows[].cls`, `hero_amount`, `meta`, `from_lines`, etc.) match the template consumption in Task 6. Façade names (`html_to_pdf`, `render_invoice_html`, `_reportlab_invoice`) match Tasks 6–8 and the monkeypatch targets in the Task 8 test. Reportlab fallback signatures match the retained functions (`render_invoice_pdf(inv, cust, business=None)`, `render_quotation_pdf(quotation, customer)`). ✓
