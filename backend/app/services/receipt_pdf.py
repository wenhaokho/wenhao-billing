"""Render a payment receipt to a PDF bytes buffer.

Reportlab-only sibling of quotation_pdf.py. Receipts are simple one-page
documents, so there is no HTML variant behind the services/pdf façade —
callers import render_receipt_pdf from here directly.
"""

from __future__ import annotations

from decimal import Decimal
from io import BytesIO

from reportlab.lib import colors
from reportlab.lib.pagesizes import A4
from reportlab.lib.styles import ParagraphStyle, getSampleStyleSheet
from reportlab.lib.units import mm
from reportlab.platypus import Paragraph, SimpleDocTemplate, Spacer, Table, TableStyle

from app.models.customer import Customer
from app.models.invoice import Invoice
from app.models.payment import Payment
from app.services.invoice_pdf import _fmt_amount
from app.services.quotation_pdf import _addr_lines

# Shared, currency-aware figure formatting (0dp for IDR and other zero-decimal
# currencies, 2dp otherwise) — keeps receipt figures consistent with invoices.


def receipt_reference(payment: Payment) -> str:
    """Human-readable receipt reference derived from the payment id."""
    return f"RCT-{payment.payment_id.hex[:8].upper()}"


def balance_note(invoice: Invoice) -> str:
    """One-line balance summary shared by the receipt PDF and the email body."""
    balance = Decimal(invoice.balance_due or 0)
    if balance <= 0:
        return "This invoice is now paid in full."
    return f"Remaining balance: {_fmt_amount(balance, invoice.currency)}."


def _method_label(payment: Payment) -> str:
    if payment.intake_source == "MANUAL":
        return "Manual entry"
    return payment.intake_source.replace("_", " ").title()


def render_receipt_pdf(
    payment: Payment,
    invoice: Invoice,
    customer: Customer | None,
    business=None,
) -> bytes:
    """Render a payment receipt to PDF bytes.

    business: optional BusinessProfile (or any object exposing name); when
              present its name is shown in the footer as the receiving party.
    """
    buf = BytesIO()
    doc = SimpleDocTemplate(
        buf,
        pagesize=A4,
        leftMargin=18 * mm,
        rightMargin=18 * mm,
        topMargin=18 * mm,
        bottomMargin=18 * mm,
        title=f"Receipt {receipt_reference(payment)}",
    )
    styles = getSampleStyleSheet()
    h1 = ParagraphStyle(
        "h1",
        parent=styles["Heading1"],
        fontSize=22,
        leading=26,
        textColor=colors.HexColor("#0f172a"),
    )
    label = ParagraphStyle(
        "label",
        parent=styles["Normal"],
        fontSize=8,
        textColor=colors.HexColor("#64748b"),
        spaceAfter=2,
    )
    normal = ParagraphStyle("n", parent=styles["Normal"], fontSize=10, leading=14)
    small = ParagraphStyle(
        "s", parent=styles["Normal"], fontSize=9, textColor=colors.HexColor("#475569")
    )

    story: list = []

    meta_rows = [
        ["RECEIPT #", receipt_reference(payment)],
        ["PAYMENT DATE", str(payment.payment_date) if payment.payment_date else "—"],
        ["INVOICE #", invoice.invoice_number or "—"],
    ]
    meta_tbl = Table(meta_rows, colWidths=[28 * mm, 45 * mm])
    meta_tbl.setStyle(
        TableStyle(
            [
                ("FONTSIZE", (0, 0), (-1, -1), 8),
                ("TEXTCOLOR", (0, 0), (0, -1), colors.HexColor("#64748b")),
                ("TEXTCOLOR", (1, 0), (1, -1), colors.HexColor("#0f172a")),
                ("FONTNAME", (1, 0), (1, -1), "Helvetica-Bold"),
                ("BOTTOMPADDING", (0, 0), (-1, -1), 3),
                ("TOPPADDING", (0, 0), (-1, -1), 3),
            ]
        )
    )

    header_tbl = Table(
        [[Paragraph("PAYMENT RECEIPT", h1), meta_tbl]],
        colWidths=[92 * mm, 78 * mm],
    )
    header_tbl.setStyle(TableStyle([("VALIGN", (0, 0), (-1, -1), "TOP")]))
    story.append(header_tbl)
    story.append(Spacer(1, 8 * mm))

    story.append(Paragraph("RECEIVED FROM", label))
    if customer:
        story.append(Paragraph(f"<b>{customer.name}</b>", normal))
        for ln in _addr_lines(customer):
            story.append(Paragraph(ln, small))
        if customer.contact_email:
            story.append(Paragraph(customer.contact_email, small))
    elif payment.payer_name:
        story.append(Paragraph(f"<b>{payment.payer_name}</b>", normal))
    else:
        story.append(Paragraph("—", small))
    story.append(Spacer(1, 6 * mm))

    detail_rows = [
        ["Amount received", _fmt_amount(payment.amount, payment.currency)],
        ["Applied to invoice", invoice.invoice_number or "—"],
        ["Payment method", _method_label(payment)],
    ]
    if payment.payer_reference:
        detail_rows.append(["Reference", payment.payer_reference])
    detail_tbl = Table(detail_rows, colWidths=[50 * mm, 60 * mm])
    detail_tbl.setStyle(
        TableStyle(
            [
                ("FONTSIZE", (0, 0), (-1, -1), 10),
                ("TEXTCOLOR", (0, 0), (0, -1), colors.HexColor("#475569")),
                ("FONTNAME", (1, 0), (1, 0), "Helvetica-Bold"),
                ("TEXTCOLOR", (1, 0), (1, 0), colors.HexColor("#0f172a")),
                ("LINEBELOW", (0, 0), (-1, 0), 0.5, colors.HexColor("#cbd5e1")),
                ("TOPPADDING", (0, 0), (-1, -1), 5),
                ("BOTTOMPADDING", (0, 0), (-1, -1), 5),
            ]
        )
    )
    story.append(detail_tbl)
    story.append(Spacer(1, 6 * mm))

    story.append(Paragraph(balance_note(invoice), normal))
    story.append(Spacer(1, 10 * mm))

    story.append(Paragraph("Thank you for your payment.", small))
    if business is not None and getattr(business, "name", None):
        story.append(Spacer(1, 2 * mm))
        story.append(Paragraph(f"Received by {business.name}", small))

    doc.build(story)
    return buf.getvalue()
