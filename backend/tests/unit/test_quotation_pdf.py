"""Quotation PDF renderer — pure unit tests, no DB required."""
from __future__ import annotations

from datetime import date
from decimal import Decimal

from app.models.customer import Customer
from app.models.quotation import Quotation
from app.models.quotation_line_item import QuotationLineItem
from app.services.quotation_pdf import render_quotation_pdf


def _make_objects() -> tuple[Quotation, Customer]:
    customer = Customer(name="Acme Pte Ltd", contact_email="acme@example.com")
    quotation = Quotation(
        quotation_number="QUO-0042",
        currency="SGD",
        subtotal=Decimal("1000.0000"),
        amount=Decimal("1000.0000"),
        status="DRAFT",
        issue_date=date(2026, 8, 7),
        valid_until=date(2026, 9, 6),
    )
    quotation.line_items = [
        QuotationLineItem(
            position=1,
            description="Website redesign",
            quantity=Decimal("1"),
            unit_price=Decimal("1000.0000"),
            amount=Decimal("1000.0000"),
        ),
    ]
    return quotation, customer


def test_render_quotation_pdf_returns_pdf_bytes():
    quotation, customer = _make_objects()
    pdf = render_quotation_pdf(quotation, customer)
    assert isinstance(pdf, bytes)
    assert pdf[:5] == b"%PDF-"


def test_render_quotation_pdf_without_customer():
    quotation, _ = _make_objects()
    pdf = render_quotation_pdf(quotation, None)
    assert pdf[:5] == b"%PDF-"


def test_render_quotation_pdf_escapes_xml_special_chars_in_names():
    """Customer fields containing XML-special chars must not crash reportlab's
    Paragraph XML parser (regression for raw 500 on names like
    "Smith & Co <bros Ltd" — an unclosed tag start is a paraparser
    ValueError when interpolated unescaped)."""
    quotation, customer = _make_objects()
    customer.name = "Smith & Co <bros Ltd"
    customer.billing_address1 = "R&D <lab, 1 Raffles Ave"
    customer.contact_email = "ap<br>illing@smith.example"

    pdf = render_quotation_pdf(quotation, customer)
    assert pdf[:5] == b"%PDF-"


def test_render_quotation_pdf_escapes_xml_special_chars_in_free_text():
    """Payment terms, notes, and footer are user-supplied free text and are
    interpolated into Paragraph markup — escape must keep <br/> conversion
    for newlines working while neutralising XML-special chars."""
    quotation, customer = _make_objects()
    quotation.payment_terms = "50% upfront <br> balance"
    quotation.notes = "Scope: design & build\n<subject to change"
    quotation.footer = "Thanks & regards\n<see terms"

    pdf = render_quotation_pdf(quotation, customer)
    assert pdf[:5] == b"%PDF-"
