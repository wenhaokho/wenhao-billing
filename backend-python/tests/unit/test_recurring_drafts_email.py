"""Formatting of the recurring-draft approval digest email."""

from app.services.email import build_recurring_drafts_email

DRAFT = {
    "invoice_number": "INV-2026-0042",
    "customer_name": "Acme Pte Ltd",
    "amount": "1,500.00",
    "currency": "SGD",
    "cycle_date": "2026-08-01",
    "link": "http://localhost:5173/invoices/abc-123/edit",
}


def test_subject_counts_drafts():
    subject, _, _ = build_recurring_drafts_email([DRAFT])
    assert subject == "1 draft invoice awaiting your approval"

    subject, _, _ = build_recurring_drafts_email([DRAFT, DRAFT])
    assert subject == "2 draft invoices awaiting your approval"


def test_text_body_lists_draft_details():
    _, text, _ = build_recurring_drafts_email([DRAFT])
    assert "INV-2026-0042" in text
    assert "Acme Pte Ltd" in text
    assert "SGD 1,500.00" in text
    assert "cycle 2026-08-01" in text
    assert "http://localhost:5173/invoices/abc-123/edit" in text


def test_html_body_links_invoice_number():
    _, _, html = build_recurring_drafts_email([DRAFT])
    assert (
        '<a href="http://localhost:5173/invoices/abc-123/edit">INV-2026-0042</a>'
        in html
    )
    assert "Awaiting Finalization" in html
