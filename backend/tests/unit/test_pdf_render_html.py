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
