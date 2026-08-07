"""Receipt PDF renderer — pure unit tests, no DB required."""
from __future__ import annotations

from datetime import date
from decimal import Decimal
from uuid import uuid4

from app.models.customer import Customer
from app.models.invoice import Invoice
from app.models.payment import Payment
from app.services.receipt_pdf import balance_note, receipt_reference, render_receipt_pdf


def _make_objects(balance: str) -> tuple[Payment, Invoice, Customer]:
    customer = Customer(name="Acme Pte Ltd", contact_email="acme@example.com")
    invoice = Invoice(
        invoice_type="MILESTONE",
        invoice_number="INV-0100",
        currency="USD",
        amount=Decimal("1000.0000"),
        balance_due=Decimal(balance),
        status="PARTIAL" if Decimal(balance) > 0 else "PAID",
    )
    payment = Payment(
        payment_id=uuid4(),
        amount=Decimal("400.0000"),
        currency="USD",
        payer_name="Acme Pte Ltd",
        payer_reference="TRF-991",
        payment_date=date(2026, 8, 7),
        intake_source="MANUAL",
        external_ref="manual-abc123",
        status="CLEARED",
        adjustment_type="NONE",
    )
    return payment, invoice, customer


def test_receipt_reference_shape():
    payment, _, _ = _make_objects("600.0000")
    ref = receipt_reference(payment)
    assert ref.startswith("RCT-")
    assert len(ref) == 12  # "RCT-" + 8 hex chars
    assert ref == ref.upper()


def test_balance_note_partial_payment_shows_remaining():
    _, invoice, _ = _make_objects("600.0000")
    note = balance_note(invoice)
    assert note.startswith("Remaining balance:")
    assert "600" in note
    assert "USD" in note


def test_balance_note_full_payment_says_paid_in_full():
    _, invoice, _ = _make_objects("0")
    assert balance_note(invoice) == "This invoice is now paid in full."


def test_render_receipt_pdf_returns_pdf_bytes():
    payment, invoice, customer = _make_objects("600.0000")
    pdf = render_receipt_pdf(payment, invoice, customer)
    assert isinstance(pdf, bytes)
    assert pdf[:5] == b"%PDF-"


def test_render_receipt_pdf_without_customer():
    payment, invoice, _ = _make_objects("0")
    pdf = render_receipt_pdf(payment, invoice, None)
    assert pdf[:5] == b"%PDF-"


def test_render_receipt_pdf_escapes_xml_special_chars_in_names():
    """Customer/payer names containing XML-special chars must not crash
    reportlab's Paragraph XML parser (regression for raw 500 on names like
    "Smith & Co <Pte> Ltd")."""
    payment, invoice, customer = _make_objects("600.0000")
    customer.name = "Smith & Co <Pte> Ltd"
    customer.billing_address1 = "1 Raffles & Robinson Ave"
    payment.payer_name = "Smith & Co <Pte> Ltd"

    pdf = render_receipt_pdf(payment, invoice, customer)
    assert pdf[:5] == b"%PDF-"
