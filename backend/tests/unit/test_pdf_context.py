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


def test_both_builders_share_key_set():
    inv_ctx = build_invoice_context(_inv(), _cust(), _biz())
    q = SimpleNamespace(
        quotation_number="Q-1", po_so_number=None, currency="SGD",
        subtotal=Decimal("100"), discount_type=None, discount_value=None,
        amount=Decimal("100"), status="SENT", issue_date=date(2026, 7, 23),
        valid_until=date(2026, 8, 6), payment_terms=None, notes=None,
        line_items=[SimpleNamespace(position=1, description="x",
                    quantity=Decimal("1"), unit_price=Decimal("100"), amount=Decimal("100"))],
    )
    q_ctx = build_quotation_context(q, _cust(), _biz())
    assert set(inv_ctx) == set(q_ctx)


def test_quotation_notes_fall_back_to_business_default():
    q = SimpleNamespace(
        quotation_number="Q-2", po_so_number=None, currency="SGD",
        subtotal=Decimal("100"), discount_type=None, discount_value=None,
        amount=Decimal("100"), status="SENT", issue_date=date(2026, 7, 23),
        valid_until=date(2026, 8, 6), payment_terms=None, notes=None,
        line_items=[SimpleNamespace(position=1, description="x",
                    quantity=Decimal("1"), unit_price=Decimal("100"), amount=Decimal("100"))],
    )
    ctx = build_quotation_context(q, _cust(), _biz())
    assert ctx["notes"] == "Default note."
