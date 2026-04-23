"""Render a quotation (estimate) to a PDF bytes buffer.

Mirrors invoice_pdf.py with the header changed to QUOTATION and the
'balance due' row replaced by a 'valid until' row.
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
from app.models.quotation import Quotation


def _fmt_amount(value: Decimal | None, currency: str) -> str:
    if value is None:
        return "—"
    q = Decimal(value).quantize(Decimal("0.01"))
    return f"{q:,.2f} {currency}"


def _addr_lines(c: Customer) -> list[str]:
    lines = []
    if c.billing_address1:
        lines.append(c.billing_address1)
    if c.billing_address2:
        lines.append(c.billing_address2)
    city_line = ", ".join(
        p for p in (c.billing_city, c.billing_state, c.billing_postal_code) if p
    )
    if city_line:
        lines.append(city_line)
    if c.billing_country:
        lines.append(c.billing_country)
    if not lines and c.billing_address:
        lines = c.billing_address.splitlines()
    return lines


def render_quotation_pdf(quotation: Quotation, customer: Customer | None) -> bytes:
    buf = BytesIO()
    doc = SimpleDocTemplate(
        buf,
        pagesize=A4,
        leftMargin=18 * mm,
        rightMargin=18 * mm,
        topMargin=18 * mm,
        bottomMargin=18 * mm,
        title=f"Quotation {quotation.quotation_number or ''}".strip(),
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
        ["QUOTE #", quotation.quotation_number or "—"],
        ["ISSUE DATE", str(quotation.issue_date) if quotation.issue_date else "—"],
        ["VALID UNTIL", str(quotation.valid_until) if quotation.valid_until else "—"],
        ["STATUS", quotation.status],
    ]
    if quotation.po_so_number:
        meta_rows.insert(1, ["PO / SO", quotation.po_so_number])
    meta_tbl = Table(meta_rows, colWidths=[25 * mm, 45 * mm])
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
        [[Paragraph("QUOTATION", h1), meta_tbl]],
        colWidths=[95 * mm, 75 * mm],
    )
    header_tbl.setStyle(TableStyle([("VALIGN", (0, 0), (-1, -1), "TOP")]))
    story.append(header_tbl)
    story.append(Spacer(1, 8 * mm))

    bill_lines = []
    if customer:
        bill_lines.append(Paragraph(f"<b>{customer.name}</b>", normal))
        for ln in _addr_lines(customer):
            bill_lines.append(Paragraph(ln, small))
        if customer.contact_email:
            bill_lines.append(Paragraph(customer.contact_email, small))
    else:
        bill_lines.append(Paragraph("—", small))

    story.append(Paragraph("BILL TO", label))
    for ln in bill_lines:
        story.append(ln)
    story.append(Spacer(1, 6 * mm))

    rows = [["#", "Description", "Qty", "Unit price", "Amount"]]
    for li in quotation.line_items:
        rows.append(
            [
                str(li.position),
                li.description,
                f"{Decimal(li.quantity):g}",
                _fmt_amount(li.unit_price, quotation.currency),
                _fmt_amount(li.amount, quotation.currency),
            ]
        )
    items_tbl = Table(
        rows,
        colWidths=[10 * mm, 80 * mm, 18 * mm, 30 * mm, 32 * mm],
        repeatRows=1,
    )
    items_tbl.setStyle(
        TableStyle(
            [
                ("BACKGROUND", (0, 0), (-1, 0), colors.HexColor("#f1f5f9")),
                ("TEXTCOLOR", (0, 0), (-1, 0), colors.HexColor("#334155")),
                ("FONTNAME", (0, 0), (-1, 0), "Helvetica-Bold"),
                ("FONTSIZE", (0, 0), (-1, -1), 9),
                ("ALIGN", (2, 0), (-1, -1), "RIGHT"),
                ("VALIGN", (0, 0), (-1, -1), "TOP"),
                ("BOTTOMPADDING", (0, 0), (-1, -1), 6),
                ("TOPPADDING", (0, 0), (-1, -1), 6),
                ("LINEBELOW", (0, 0), (-1, 0), 0.5, colors.HexColor("#cbd5e1")),
                ("LINEBELOW", (0, -1), (-1, -1), 0.5, colors.HexColor("#e2e8f0")),
            ]
        )
    )
    story.append(items_tbl)
    story.append(Spacer(1, 4 * mm))

    totals_rows = []
    if quotation.subtotal is not None:
        totals_rows.append(
            ["Subtotal", _fmt_amount(quotation.subtotal, quotation.currency)]
        )
    if quotation.discount_type and quotation.discount_value:
        disc_label = (
            f"Discount ({quotation.discount_value}%)"
            if quotation.discount_type == "PERCENT"
            else "Discount"
        )
        totals_rows.append([disc_label, f"-{quotation.discount_value}"])
    totals_rows.append(["Total", _fmt_amount(quotation.amount, quotation.currency)])
    if quotation.valid_until:
        totals_rows.append(["Valid until", str(quotation.valid_until)])

    totals_tbl = Table(totals_rows, colWidths=[40 * mm, 40 * mm], hAlign="RIGHT")
    totals_tbl.setStyle(
        TableStyle(
            [
                ("FONTSIZE", (0, 0), (-1, -1), 10),
                ("ALIGN", (1, 0), (1, -1), "RIGHT"),
                ("TEXTCOLOR", (0, 0), (-1, -2), colors.HexColor("#475569")),
                ("FONTNAME", (0, 2), (-1, 2), "Helvetica-Bold"),
                ("TEXTCOLOR", (0, 2), (-1, 2), colors.HexColor("#0f172a")),
                ("LINEABOVE", (0, 2), (-1, 2), 0.5, colors.HexColor("#cbd5e1")),
                ("TOPPADDING", (0, 0), (-1, -1), 4),
                ("BOTTOMPADDING", (0, 0), (-1, -1), 4),
            ]
        )
    )
    story.append(totals_tbl)
    story.append(Spacer(1, 8 * mm))

    if quotation.payment_terms:
        story.append(Paragraph("PAYMENT TERMS", label))
        story.append(Paragraph(quotation.payment_terms, normal))
        story.append(Spacer(1, 4 * mm))

    if quotation.notes:
        story.append(Paragraph("NOTES", label))
        story.append(Paragraph(quotation.notes.replace("\n", "<br/>"), normal))
        story.append(Spacer(1, 4 * mm))

    if quotation.footer:
        story.append(Spacer(1, 6 * mm))
        story.append(Paragraph(quotation.footer.replace("\n", "<br/>"), small))

    doc.build(story)
    return buf.getvalue()
