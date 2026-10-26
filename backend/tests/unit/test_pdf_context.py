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
        default_notes="Default note.",
        payment_accounts=[
            SimpleNamespace(currency="SGD", instructions="DBS 012-345678-9",
                            is_default=False),
            SimpleNamespace(currency="IDR", instructions="OCBC NISP IDR",
                            is_default=True),
        ],
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
        wht_type="NONE", wht_rate=None, wht_gross_up=False, wht_base=None,
        wht_amount=Decimal("0"), wht_booked=Decimal("0"),
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


def test_invoice_context_gross_up_rows():
    inv = _inv(
        currency="IDR", subtotal=Decimal("10000000"), discount_type=None,
        discount_value=None, amount=Decimal("10204082"),
        balance_due=Decimal("10204082"), status="OPEN",
        wht_type="PPH23", wht_rate=Decimal("2.0000"), wht_gross_up=True,
        wht_base=Decimal("10000000"), wht_amount=Decimal("204082"),
    )
    ctx = build_invoice_context(inv, _cust(), _biz())
    rows = [(r["label"], r["value"], r["cls"]) for r in ctx["summary_rows"]]
    assert rows == [
        ("Subtotal", "10,000,000", "subtotal"),
        ("PPh 23 (2%)", "204,082", "grossup"),
        ("Total", "10,204,082", "total"),
        ("PPh 23 withholding (2%)", "−204,082", "wht"),
    ]
    assert ctx["hero_label"] == "Amount to Transfer"
    assert ctx["hero_amount"] == "10,000,000"


def test_invoice_context_wht_without_gross_up():
    inv = _inv(
        currency="IDR", subtotal=Decimal("10000000"), discount_type=None,
        discount_value=None, amount=Decimal("10000000"),
        balance_due=Decimal("10000000"), status="OPEN",
        wht_type="PPH23", wht_rate=Decimal("2.0000"), wht_gross_up=False,
        wht_base=Decimal("10000000"), wht_amount=Decimal("200000"),
    )
    ctx = build_invoice_context(inv, _cust(), _biz())
    rows = [(r["label"], r["value"]) for r in ctx["summary_rows"]]
    assert rows == [
        ("Subtotal", "10,000,000"),
        ("Total", "10,000,000"),
        ("PPh 23 withholding (2%)", "−200,000"),
    ]
    assert ctx["hero_amount"] == "9,800,000"


def test_invoice_context_wht_with_partial_payment():
    inv = _inv(
        currency="IDR", subtotal=Decimal("10000000"), discount_type=None,
        discount_value=None, amount=Decimal("10000000"),
        balance_due=Decimal("6000000"), status="PARTIALLY_PAID",
        wht_type="PPH23", wht_rate=Decimal("2.0000"), wht_gross_up=False,
        wht_base=Decimal("10000000"), wht_amount=Decimal("200000"),
    )
    ctx = build_invoice_context(inv, _cust(), _biz())
    labels = [r["label"] for r in ctx["summary_rows"]]
    assert labels == [
        "Subtotal", "Total", "PPh 23 withholding (2%)", "Paid",
    ]
    assert ctx["summary_rows"][-1]["value"] == "−4,000,000"
    assert ctx["hero_amount"] == "5,800,000"      # expected_cash


def _gross_up_inv(**over):
    base = {
        "currency": "IDR", "subtotal": Decimal(10000000), "discount_type": None,
        "discount_value": None, "amount": Decimal(10204082),
        "wht_type": "PPH23", "wht_rate": Decimal("2.0000"), "wht_gross_up": True,
        "wht_base": Decimal(10000000), "wht_amount": Decimal(204082),
    }
    base.update(over)
    return _inv(**base)


def test_invoice_context_paid_gross_up_excludes_booked_wht():
    inv = _gross_up_inv(
        balance_due=Decimal(0), status="PAID", wht_booked=Decimal(204082),
    )
    ctx = build_invoice_context(inv, _cust(), _biz())
    rows = [(r["label"], r["value"]) for r in ctx["summary_rows"]]
    assert rows[-3:] == [
        ("Total", "10,204,082"),
        ("PPh 23 withholding (2%)", "−204,082"),
        ("Paid", "−10,000,000"),
    ]
    assert ctx["hero_amount"] == "0"


def test_invoice_context_partial_gross_up_excludes_booked_wht():
    # 5,000,000 cash + 204,082 PPh booked against 10,204,082.
    inv = _gross_up_inv(
        balance_due=Decimal(5000000), status="PARTIALLY_PAID",
        wht_booked=Decimal(204082),
    )
    ctx = build_invoice_context(inv, _cust(), _biz())
    assert ctx["summary_rows"][-1]["label"] == "Paid"
    assert ctx["summary_rows"][-1]["value"] == "−5,000,000"
    assert ctx["hero_amount"] == "5,000,000"


def test_invoice_context_no_wht_unchanged():
    ctx = build_invoice_context(_inv(), _cust(), _biz())
    assert [r["label"] for r in ctx["summary_rows"]] == [
        "Subtotal", "Discount (5%)", "Total", "Paid",
    ]
    assert ctx["hero_label"] == "Total Due"


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


from app.services.pdf.context import resolve_payment_instructions


def test_resolve_payment_instructions_matches_currency():
    accounts = _biz().payment_accounts
    assert resolve_payment_instructions(accounts, "SGD") == "DBS 012-345678-9"


def test_resolve_payment_instructions_falls_back_to_default():
    accounts = _biz().payment_accounts
    # No USD account -> the is_default (IDR) account wins.
    assert resolve_payment_instructions(accounts, "USD") == "OCBC NISP IDR"


def test_resolve_payment_instructions_none_when_empty():
    assert resolve_payment_instructions([], "SGD") is None
    assert resolve_payment_instructions(None, "SGD") is None


def test_invoice_context_selects_default_when_currency_unmatched():
    ctx = build_invoice_context(_inv(currency="USD"), _cust(), _biz())
    assert ctx["payment_instructions"] == "OCBC NISP IDR"


def test_bill_to_joins_customer_email_and_phone():
    cust = _cust()
    cust.contact_phone = "+65 9123 4567"
    ctx = build_invoice_context(_inv(), cust, _biz())
    assert ctx["to_lines"][-1] == "ap@acme.example · +65 9123 4567"


def test_bill_to_phone_only_when_no_email():
    cust = _cust()
    cust.contact_email = None
    cust.contact_phone = "+65 9123 4567"
    ctx = build_invoice_context(_inv(), cust, _biz())
    assert ctx["to_lines"][-1] == "+65 9123 4567"


def test_bill_to_email_only_when_no_phone():
    ctx = build_invoice_context(_inv(), _cust(), _biz())
    assert ctx["to_lines"][-1] == "ap@acme.example"


def test_receipt_received_from_includes_customer_phone():
    from uuid import uuid4

    from app.services.pdf.context import build_receipt_context

    cust = _cust()
    cust.contact_phone = "+65 9123 4567"
    payment = SimpleNamespace(
        payment_id=uuid4(), amount=Decimal("400"), currency="SGD",
        payer_name="Acme Pte Ltd", payer_reference=None,
        payment_date=date(2026, 8, 7), intake_source="MANUAL",
    )
    ctx = build_receipt_context(payment, _inv(), cust, _biz())
    assert ctx["to_lines"][-1] == "ap@acme.example · +65 9123 4567"


def test_invoice_context_omits_block_when_no_accounts():
    biz = _biz()
    biz.payment_accounts = []
    ctx = build_invoice_context(_inv(), _cust(), biz)
    assert ctx["payment_instructions"] is None


def _npwp_biz():
    biz = _biz()
    biz.npwp = "2102060605910001"
    biz.npwp_name = "Kardono Wijaya"
    return biz


def test_invoice_from_block_shows_npwp_when_withholding():
    ctx = build_invoice_context(_gross_up_inv(balance_due=Decimal(10204082)),
                                _cust(), _npwp_biz())
    assert ctx["from_lines"][-1] == "NPWP 2102060605910001 a.n. Kardono Wijaya"


def test_invoice_from_block_omits_npwp_without_withholding():
    ctx = build_invoice_context(_inv(), _cust(), _npwp_biz())
    assert not any(ln.startswith("NPWP") for ln in ctx["from_lines"])


def test_invoice_from_block_omits_npwp_when_unset():
    ctx = build_invoice_context(_gross_up_inv(balance_due=Decimal(10204082)),
                                _cust(), _biz())
    assert not any(ln.startswith("NPWP") for ln in ctx["from_lines"])

