"""Pure unit tests for the invoice PDF renderer.

These need no database — the renderer operates on plain model instances, so
we build transient (unpersisted) objects and assert on the discount math,
number/percent formatting, and that a valid PDF is produced across the
main branches (with/without customer, business, and discount).

Module under test: `app.services.invoice_pdf`.

Contract pinned here:
  * `_discount_amount(type, value, subtotal)` computes the monetary discount
    the same way `services.invoicing` computes invoice totals:
      - PERCENT -> subtotal * value / 100  (this is the bug that showed "-5"
        instead of "-317.50" on a 5% discount of 6,350)
      - AMOUNT  -> value
      - None when no discount applies.
  * `_fmt_pct` trims trailing zeros; `_fmt_num` groups thousands, 2dp.
  * `render_invoice_pdf(invoice, customer, business=None)` returns PDF bytes.
"""

from __future__ import annotations

from datetime import date
from decimal import Decimal
from types import SimpleNamespace

from app.models.invoice import Invoice
from app.models.invoice_line_item import InvoiceLineItem
from app.services.invoice_pdf import (
    _discount_amount,
    _fmt_num,
    _fmt_pct,
    render_invoice_pdf,
)


# --- discount math (the fixed bug) -----------------------------------------

def test_discount_percent_computes_monetary_amount():
    # 5% of 6,350.00 -> 317.50 (previously rendered as the raw "5")
    assert _discount_amount("PERCENT", Decimal("5"), Decimal("6350.00")) == Decimal("317.50")


def test_discount_percent_handles_fractional_rate():
    assert _discount_amount("PERCENT", Decimal("7.5"), Decimal("1000")) == Decimal("75.00")


def test_discount_amount_type_is_the_value_itself():
    assert _discount_amount("AMOUNT", Decimal("100"), Decimal("6350.00")) == Decimal("100.00")


def test_discount_absent_returns_none():
    assert _discount_amount(None, None, Decimal("6350.00")) is None
    assert _discount_amount("PERCENT", None, Decimal("6350.00")) is None
    assert _discount_amount("PERCENT", Decimal("0"), Decimal("6350.00")) is None


# --- formatting helpers -----------------------------------------------------

def test_fmt_pct_trims_trailing_zeros():
    assert _fmt_pct(Decimal("5.0000")) == "5"
    assert _fmt_pct(Decimal("7.5000")) == "7.5"
    assert _fmt_pct(Decimal("10")) == "10"


def test_fmt_num_groups_thousands():
    assert _fmt_num(Decimal("6350")) == "6,350.00"
    assert _fmt_num(Decimal("1234.5")) == "1,234.50"
    assert _fmt_num(None) == "—"


# --- render smoke tests -----------------------------------------------------

def _invoice(**overrides) -> Invoice:
    inv = Invoice(
        invoice_type="MILESTONE",
        invoice_number="INV-2026-0042",
        po_so_number="PO-88913",
        currency="SGD",
        subtotal=Decimal("6350.00"),
        discount_type="PERCENT",
        discount_value=Decimal("5"),
        amount=Decimal("6032.50"),
        balance_due=Decimal("6032.50"),
        status="SENT",
        issue_date=date(2026, 7, 23),
        due_date=date(2026, 8, 22),
        payment_terms="Net 30",
        notes="Thanks!",
    )
    for k, v in overrides.items():
        setattr(inv, k, v)
    inv.line_items = [
        InvoiceLineItem(
            position=1, description="Website redesign",
            quantity=Decimal("1"), unit_price=Decimal("2500.00"), amount=Decimal("2500.00"),
        ),
        InvoiceLineItem(
            position=2, description="Frontend implementation",
            quantity=Decimal("40"), unit_price=Decimal("85.00"), amount=Decimal("3400.00"),
        ),
    ]
    return inv


def test_render_full_invoice_produces_pdf():
    business = SimpleNamespace(
        name="Wenhao Studio Pte Ltd",
        address="1 Marina Boulevard\nSingapore 018989",
        contact_email="accounts@wenhaostudio.sg",
        contact_phone="+65 6123 4567",
        invoice_title="Invoice",
        invoice_summary="Design & engineering, done right.",
    )
    customer = SimpleNamespace(
        name="Acme Pte Ltd",
        billing_address1="12 Raffles Place",
        billing_address2=None,
        billing_city="Singapore",
        billing_state=None,
        billing_postal_code="048619",
        billing_country="Singapore",
        billing_address=None,
        contact_email="ap@acme.example",
    )
    out = render_invoice_pdf(_invoice(), customer, business)
    assert out[:5] == b"%PDF-"
    assert len(out) > 1000


def test_render_without_customer_or_business():
    out = render_invoice_pdf(_invoice(), None)
    assert out[:5] == b"%PDF-"


def test_render_without_discount():
    inv = _invoice(discount_type=None, discount_value=None)
    out = render_invoice_pdf(inv, None)
    assert out[:5] == b"%PDF-"


def test_render_invoice_pdf_escapes_xml_special_chars_in_names():
    """Customer/business names, addresses, descriptions, and free text
    containing XML-special chars must not crash reportlab's Paragraph XML
    parser (regression for raw 500 on names like "Smith & Co <bros Ltd" —
    an unclosed tag start is a paraparser ValueError when interpolated
    unescaped)."""
    business = SimpleNamespace(
        name="Smith & Co <bros Ltd",
        address="R&D <lab, 1 Raffles Ave\nSingapore 048619",
        contact_email="ap<br>illing@smith.example",
        contact_phone="+65 6123 4567",
        invoice_title="Tax <invoice",
        invoice_summary="Design & <engineering",
        default_notes=None,
    )
    customer = SimpleNamespace(
        name="Jones & Sons <holdings",
        billing_address1="2 Cecil <st",
        billing_address2=None,
        billing_city="Singapore",
        billing_state=None,
        billing_postal_code="048619",
        billing_country="Singapore",
        billing_address=None,
        contact_email="ap<br>finance@jones.example",
    )
    inv = _invoice(
        invoice_number="INV <draft 2026",
        po_so_number="PO <2026 & co",
        payment_terms="50% upfront <br> balance",
        notes="Scope: design & build\n<subject to change",
        footer="Thanks & regards\n<see terms",
    )
    inv.line_items[0].description = "Design & build <landing page"

    out = render_invoice_pdf(inv, customer, business)
    assert out[:5] == b"%PDF-"


def test_notes_fall_back_to_business_default_when_invoice_has_none():
    # Renderer prefers invoice.notes; when absent it uses business.default_notes.
    from app.services import invoice_pdf as ip

    captured: list[str | None] = []
    original = ip.Paragraph

    def spy(text, style, *a, **k):
        captured.append(text)
        return original(text, style, *a, **k)

    ip.Paragraph = spy
    try:
        # invoice has no notes -> falls back to the profile default
        inv = _invoice(notes=None)
        business = SimpleNamespace(default_notes="Payable within 30 days.")
        ip.render_invoice_pdf(inv, None, business)
        assert any("Payable within 30 days." in (t or "") for t in captured)

        # invoice notes win over the profile default
        captured.clear()
        inv2 = _invoice(notes="Invoice-specific note.")
        ip.render_invoice_pdf(inv2, None, business)
        assert any("Invoice-specific note." in (t or "") for t in captured)
        assert not any("Payable within 30 days." in (t or "") for t in captured)
    finally:
        ip.Paragraph = original
