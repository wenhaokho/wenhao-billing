"""Render an invoice to a PDF bytes buffer using reportlab.

Design: "Editorial Ledger" — a refined, single-brand financial document.
Charcoal ink on paper, one disciplined emerald-teal accent, monospaced
figures that align like a real ledger, a full-bleed accent masthead, a
status chip that colours by state, and the Balance Due as the hero.

Portable by design: uses only reportlab's built-in base-14 fonts
(Helvetica for text, Courier for figures) so output is identical in a
Linux Docker container and on a local machine — no font files required.
"""

from __future__ import annotations

from decimal import Decimal
from io import BytesIO

from reportlab.lib import colors
from reportlab.lib.enums import TA_RIGHT
from reportlab.lib.pagesizes import A4
from reportlab.lib.styles import ParagraphStyle, getSampleStyleSheet
from reportlab.lib.units import mm
from reportlab.platypus import (
    Paragraph,
    SimpleDocTemplate,
    Spacer,
    Table,
    TableStyle,
)

from app.models.customer import Customer
from app.models.invoice import Invoice

# ---------------------------------------------------------------------------
# Palette — one accent, held with discipline.
# ---------------------------------------------------------------------------
INK = colors.HexColor("#15181C")       # near-black, primary text
INK_SOFT = colors.HexColor("#3A4048")  # secondary text
MUTE = colors.HexColor("#727A85")      # labels
FAINT = colors.HexColor("#A2AAB4")     # footer / de-emphasised
HAIR = colors.HexColor("#E4E7EC")      # hairline rules
PANEL = colors.HexColor("#F6F6F3")     # warm off-white panel fill
ZEBRA = colors.HexColor("#FAFAF8")     # barely-there alternate row
ACCENT = colors.HexColor("#0E7C66")    # emerald teal
ACCENT_DK = colors.HexColor("#0A5C4C")
PAPER = colors.white

# Status → chip colour. Anything unknown falls back to the accent.
STATUS_COLORS = {
    "PAID": colors.HexColor("#0E7C66"),
    "SENT": colors.HexColor("#15181C"),
    "DRAFT": colors.HexColor("#727A85"),
    "PARTIAL": colors.HexColor("#B45309"),
    "OVERDUE": colors.HexColor("#B42318"),
    "VOID": colors.HexColor("#9CA3AF"),
    "AWAITING_FINALIZATION": colors.HexColor("#B45309"),
}

MARGIN = 18 * mm
CONTENT_W = A4[0] - 2 * MARGIN


def _fmt_amount(value: Decimal | None, currency: str) -> str:
    if value is None:
        return "—"
    q = Decimal(value).quantize(Decimal("0.01"))
    # group thousands
    return f"{q:,.2f} {currency}"


def _fmt_num(value: Decimal | None) -> str:
    """Bare grouped number, no currency suffix (currency stated once per doc)."""
    if value is None:
        return "—"
    return f"{Decimal(value).quantize(Decimal('0.01')):,.2f}"


def _fmt_pct(value: Decimal) -> str:
    """Render a percentage without trailing zeros (5.0000 -> '5', 7.5000 -> '7.5')."""
    s = f"{Decimal(value):f}"
    if "." in s:
        s = s.rstrip("0").rstrip(".")
    return s


def _discount_amount(
    discount_type: str | None,
    discount_value: Decimal | None,
    subtotal: Decimal | None,
) -> Decimal | None:
    """Monetary discount to display, matching services.invoicing totals math.

    PERCENT -> subtotal * value/100; AMOUNT -> value. Returns None when no
    discount applies (mirrors the truthiness guard used at invoice creation).
    """
    if not discount_type or not discount_value:
        return None
    if discount_type == "PERCENT":
        base = Decimal(subtotal or 0)
        return (base * Decimal(discount_value) / Decimal("100")).quantize(Decimal("0.01"))
    return Decimal(discount_value).quantize(Decimal("0.01"))


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


# ---------------------------------------------------------------------------
# Paragraph styles
# ---------------------------------------------------------------------------
def _styles() -> dict[str, ParagraphStyle]:
    base = getSampleStyleSheet()["Normal"]
    return {
        "title": ParagraphStyle(
            "title", parent=base, fontName="Helvetica-Bold",
            fontSize=30, leading=32, textColor=INK,
        ),
        "brand": ParagraphStyle(
            "brand", parent=base, fontName="Helvetica-Bold",
            fontSize=11, leading=15, textColor=ACCENT_DK,
        ),
        "tagline": ParagraphStyle(
            "tagline", parent=base, fontName="Helvetica-Oblique",
            fontSize=8.5, leading=12, textColor=MUTE,
        ),
        "label": ParagraphStyle(
            "label", parent=base, fontName="Helvetica-Bold",
            fontSize=7, leading=10, textColor=MUTE, spaceAfter=3,
        ),
        "name": ParagraphStyle(
            "name", parent=base, fontName="Helvetica-Bold",
            fontSize=11, leading=15, textColor=INK,
        ),
        "body": ParagraphStyle(
            "body", parent=base, fontName="Helvetica",
            fontSize=9, leading=13, textColor=INK_SOFT,
        ),
        "meta_k": ParagraphStyle(
            "meta_k", parent=base, fontName="Helvetica",
            fontSize=8, leading=12, textColor=MUTE,
        ),
        "meta_v": ParagraphStyle(
            "meta_v", parent=base, fontName="Helvetica-Bold",
            fontSize=9, leading=12, textColor=INK, alignment=TA_RIGHT,
        ),
        "notes": ParagraphStyle(
            "notes", parent=base, fontName="Helvetica",
            fontSize=9, leading=13.5, textColor=INK_SOFT,
        ),
        "cell": ParagraphStyle(
            "cell", parent=base, fontName="Helvetica",
            fontSize=9, leading=12.5, textColor=INK,
        ),
    }


def _business_lines(business) -> list[str]:
    if business is None:
        return []
    out: list[str] = []
    if getattr(business, "address", None):
        out += [ln for ln in business.address.splitlines() if ln.strip()]
    if getattr(business, "contact_email", None):
        out.append(business.contact_email)
    if getattr(business, "contact_phone", None):
        out.append(business.contact_phone)
    return out


def _status_chip(status: str) -> Table:
    """A small filled pill carrying the invoice status, coloured by state."""
    color = STATUS_COLORS.get((status or "").upper(), ACCENT)
    txt = ParagraphStyle(
        "chip", fontName="Helvetica-Bold", fontSize=8, leading=10,
        textColor=PAPER, alignment=TA_RIGHT,
    )
    label = (status or "—").replace("_", " ").upper()
    chip = Table([[Paragraph(label, txt)]], hAlign="RIGHT")
    chip.setStyle(
        TableStyle(
            [
                ("BACKGROUND", (0, 0), (-1, -1), color),
                ("LEFTPADDING", (0, 0), (-1, -1), 8),
                ("RIGHTPADDING", (0, 0), (-1, -1), 8),
                ("TOPPADDING", (0, 0), (-1, -1), 4),
                ("BOTTOMPADDING", (0, 0), (-1, -1), 4),
            ]
        )
    )
    return chip


def _page_furniture(invoice: Invoice):
    """Return an onPage callback drawing the full-bleed masthead + footer."""
    number = invoice.invoice_number or ""

    def draw(canvas, doc):
        w, h = A4
        canvas.saveState()
        # Full-bleed accent masthead stripe at the very top edge.
        canvas.setFillColor(ACCENT)
        canvas.rect(0, h - 6 * mm, w, 6 * mm, fill=1, stroke=0)
        canvas.setFillColor(INK)
        canvas.rect(0, h - 6.7 * mm, w, 0.7 * mm, fill=1, stroke=0)
        # Footer hairline + small meta.
        canvas.setStrokeColor(HAIR)
        canvas.setLineWidth(0.5)
        canvas.line(MARGIN, 14 * mm, w - MARGIN, 14 * mm)
        canvas.setFont("Helvetica", 7.5)
        canvas.setFillColor(FAINT)
        canvas.drawString(MARGIN, 10.5 * mm, f"Invoice {number}".strip())
        canvas.drawRightString(
            w - MARGIN, 10.5 * mm, f"Page {canvas.getPageNumber()}"
        )
        canvas.restoreState()

    return draw


def render_invoice_pdf(
    invoice: Invoice,
    customer: Customer | None,
    business=None,
) -> bytes:
    """Render an invoice to PDF bytes.

    business: optional BusinessProfile (or any object exposing name / address /
              contact_email / contact_phone / invoice_title / invoice_summary).
              When omitted, the "From" block is skipped and the masthead reads
              "INVOICE".
    """
    buf = BytesIO()
    doc = SimpleDocTemplate(
        buf,
        pagesize=A4,
        leftMargin=MARGIN,
        rightMargin=MARGIN,
        topMargin=20 * mm,
        bottomMargin=20 * mm,
        title=f"Invoice {invoice.invoice_number or ''}".strip(),
    )
    s = _styles()
    story: list = []

    # -- Masthead: title + brand on the left, status chip + meta on the right --
    title_txt = (getattr(business, "invoice_title", None) or "INVOICE").upper()
    left_stack: list = [Paragraph(title_txt, s["title"])]
    if business is not None and getattr(business, "name", None):
        left_stack.append(Spacer(1, 4))
        left_stack.append(Paragraph(business.name, s["brand"]))
    if business is not None and getattr(business, "invoice_summary", None):
        left_stack.append(Paragraph(business.invoice_summary, s["tagline"]))

    meta_rows = [
        [Paragraph("Invoice&nbsp;no.", s["meta_k"]),
         Paragraph(invoice.invoice_number or "—", s["meta_v"])],
        [Paragraph("Issue&nbsp;date", s["meta_k"]),
         Paragraph(str(invoice.issue_date) if invoice.issue_date else "—", s["meta_v"])],
        [Paragraph("Due&nbsp;date", s["meta_k"]),
         Paragraph(str(invoice.due_date) if invoice.due_date else "—", s["meta_v"])],
    ]
    if invoice.po_so_number:
        meta_rows.append(
            [Paragraph("PO&nbsp;/&nbsp;SO", s["meta_k"]),
             Paragraph(invoice.po_so_number, s["meta_v"])]
        )
    meta_tbl = Table(meta_rows, colWidths=[26 * mm, 40 * mm])
    meta_tbl.setStyle(
        TableStyle(
            [
                ("BACKGROUND", (0, 0), (-1, -1), PANEL),
                ("LINEBELOW", (0, 0), (-1, -2), 0.4, HAIR),
                ("VALIGN", (0, 0), (-1, -1), "MIDDLE"),
                ("LEFTPADDING", (0, 0), (-1, -1), 10),
                ("RIGHTPADDING", (0, 0), (-1, -1), 10),
                ("TOPPADDING", (0, 0), (-1, -1), 6),
                ("BOTTOMPADDING", (0, 0), (-1, -1), 6),
            ]
        )
    )
    right_stack = [_status_chip(invoice.status), Spacer(1, 6), meta_tbl]

    masthead = Table(
        [[left_stack, right_stack]],
        colWidths=[CONTENT_W - 66 * mm, 66 * mm],
    )
    masthead.setStyle(TableStyle([("VALIGN", (0, 0), (-1, -1), "TOP")]))
    story.append(masthead)
    story.append(Spacer(1, 12 * mm))

    # -- From / Bill-to columns --
    from_cell: list = [Paragraph("FROM", s["label"])]
    if business is not None and getattr(business, "name", None):
        from_cell.append(Paragraph(business.name, s["name"]))
        for ln in _business_lines(business):
            from_cell.append(Paragraph(ln, s["body"]))
    else:
        from_cell.append(Paragraph("—", s["body"]))

    bill_cell: list = [Paragraph("BILL TO", s["label"])]
    if customer:
        bill_cell.append(Paragraph(customer.name, s["name"]))
        for ln in _addr_lines(customer):
            bill_cell.append(Paragraph(ln, s["body"]))
        if customer.contact_email:
            bill_cell.append(Paragraph(customer.contact_email, s["body"]))
    else:
        bill_cell.append(Paragraph("—", s["body"]))

    parties = Table(
        [[from_cell, bill_cell]],
        colWidths=[CONTENT_W / 2, CONTENT_W / 2],
    )
    parties.setStyle(
        TableStyle(
            [
                ("VALIGN", (0, 0), (-1, -1), "TOP"),
                ("LEFTPADDING", (0, 0), (0, -1), 0),
                ("LEFTPADDING", (1, 0), (1, -1), 10),
                ("RIGHTPADDING", (0, 0), (-1, -1), 6),
                ("LINEBEFORE", (1, 0), (1, -1), 0.5, HAIR),
            ]
        )
    )
    story.append(parties)
    story.append(Spacer(1, 10 * mm))

    # -- Line items --
    rows = [["#", "DESCRIPTION", "QTY", "UNIT PRICE", f"AMOUNT · {invoice.currency}"]]
    for li in invoice.line_items:
        rows.append(
            [
                str(li.position),
                Paragraph(li.description, s["cell"]),
                f"{Decimal(li.quantity):g}",
                _fmt_num(li.unit_price),
                _fmt_num(li.amount),
            ]
        )
    items_tbl = Table(
        rows,
        colWidths=[9 * mm, CONTENT_W - 9 * mm - 18 * mm - 33 * mm - 33 * mm,
                   18 * mm, 33 * mm, 33 * mm],
        repeatRows=1,
    )
    items_style = [
        # header
        ("BACKGROUND", (0, 0), (-1, 0), INK),
        ("TEXTCOLOR", (0, 0), (-1, 0), PAPER),
        ("FONTNAME", (0, 0), (-1, 0), "Helvetica-Bold"),
        ("FONTSIZE", (0, 0), (-1, 0), 7.5),
        ("TOPPADDING", (0, 0), (-1, 0), 7),
        ("BOTTOMPADDING", (0, 0), (-1, 0), 7),
        # body defaults
        ("FONTNAME", (0, 1), (0, -1), "Helvetica"),
        ("FONTNAME", (2, 1), (-1, -1), "Courier"),  # figures align like a ledger
        ("FONTSIZE", (0, 1), (-1, -1), 9),
        ("TEXTCOLOR", (0, 1), (-1, -1), INK),
        ("TEXTCOLOR", (0, 1), (0, -1), MUTE),
        ("ALIGN", (2, 0), (-1, -1), "RIGHT"),
        ("ALIGN", (0, 0), (0, -1), "CENTER"),
        ("VALIGN", (0, 0), (-1, -1), "TOP"),
        ("TOPPADDING", (0, 1), (-1, -1), 7),
        ("BOTTOMPADDING", (0, 1), (-1, -1), 7),
        ("LEFTPADDING", (0, 0), (-1, -1), 6),
        ("RIGHTPADDING", (0, 0), (-1, -1), 6),
    ]
    # zebra striping on alternate body rows
    for r in range(1, len(rows)):
        if r % 2 == 0:
            items_style.append(("BACKGROUND", (0, r), (-1, r), ZEBRA))
        items_style.append(("LINEBELOW", (0, r), (-1, r), 0.4, HAIR))
    items_tbl.setStyle(TableStyle(items_style))
    story.append(items_tbl)
    story.append(Spacer(1, 6 * mm))

    # -- Totals (right-aligned block) with Balance Due as the hero panel --
    def money(v):
        return _fmt_amount(v, invoice.currency)

    small_rows = []
    if invoice.subtotal is not None:
        small_rows.append(["Subtotal", money(invoice.subtotal)])
    disc_amount = _discount_amount(
        invoice.discount_type, invoice.discount_value, invoice.subtotal
    )
    if disc_amount is not None:
        if invoice.discount_type == "PERCENT":
            disc_label = f"Discount ({_fmt_pct(invoice.discount_value)}%)"
        else:
            disc_label = "Discount"
        small_rows.append([disc_label, f"-{money(disc_amount)}"])
    small_rows.append(["Total", money(invoice.amount)])

    totals_inner = Table(small_rows, colWidths=[42 * mm, 42 * mm])
    ti_style = [
        ("FONTNAME", (0, 0), (0, -1), "Helvetica"),
        ("FONTNAME", (1, 0), (1, -1), "Courier"),
        ("FONTSIZE", (0, 0), (-1, -1), 9.5),
        ("TEXTCOLOR", (0, 0), (0, -1), MUTE),
        ("TEXTCOLOR", (1, 0), (1, -1), INK),
        ("ALIGN", (1, 0), (1, -1), "RIGHT"),
        ("TOPPADDING", (0, 0), (-1, -1), 4),
        ("BOTTOMPADDING", (0, 0), (-1, -1), 4),
        ("LEFTPADDING", (0, 0), (-1, -1), 0),
        # emphasise the Total line
        ("FONTNAME", (0, -1), (0, -1), "Helvetica-Bold"),
        ("TEXTCOLOR", (0, -1), (-1, -1), INK),
        ("LINEABOVE", (0, -1), (-1, -1), 0.5, HAIR),
        ("TOPPADDING", (0, -1), (-1, -1), 7),
    ]
    totals_inner.setStyle(TableStyle(ti_style))

    # Hero panel — Balance Due, accent fill, white figures.
    hero_label = ParagraphStyle(
        "hero_label", fontName="Helvetica-Bold", fontSize=8.5, leading=11,
        textColor=PAPER,
    )
    hero_val = ParagraphStyle(
        "hero_val", fontName="Courier-Bold", fontSize=16, leading=18,
        textColor=PAPER, alignment=TA_RIGHT,
    )
    hero_amount = (
        f"{_fmt_num(invoice.balance_due)}"
        f'<font face="Helvetica-Bold" size="9"> {invoice.currency}</font>'
    )
    balance = Table(
        [[Paragraph("BALANCE&nbsp;DUE", hero_label),
          Paragraph(hero_amount, hero_val)]],
        colWidths=[26 * mm, 58 * mm],
    )
    balance.setStyle(
        TableStyle(
            [
                ("BACKGROUND", (0, 0), (-1, -1), ACCENT),
                ("VALIGN", (0, 0), (-1, -1), "MIDDLE"),
                ("LEFTPADDING", (0, 0), (0, -1), 12),
                ("LEFTPADDING", (1, 0), (1, -1), 6),
                ("RIGHTPADDING", (0, 0), (-1, -1), 12),
                ("TOPPADDING", (0, 0), (-1, -1), 13),
                ("BOTTOMPADDING", (0, 0), (-1, -1), 13),
            ]
        )
    )

    totals_stack = [totals_inner, Spacer(1, 5), balance]
    totals_wrap = Table(
        [["", totals_stack]],
        colWidths=[CONTENT_W - 84 * mm, 84 * mm],
    )
    totals_wrap.setStyle(TableStyle([("VALIGN", (0, 0), (-1, -1), "TOP")]))
    story.append(totals_wrap)
    story.append(Spacer(1, 12 * mm))

    # -- Payment terms / notes --
    detail_cells = []
    if invoice.payment_terms:
        detail_cells.append(
            [Paragraph("PAYMENT TERMS", s["label"]),
             Paragraph(invoice.payment_terms, s["notes"])]
        )
    # Fall back to the business profile's default notes when the invoice
    # itself carries none.
    notes_text = invoice.notes or getattr(business, "default_notes", None)
    if notes_text:
        detail_cells.append(
            [Paragraph("NOTES", s["label"]),
             Paragraph(notes_text.replace("\n", "<br/>"), s["notes"])]
        )
    if len(detail_cells) == 2:
        details = Table(
            [[detail_cells[0], detail_cells[1]]],
            colWidths=[CONTENT_W / 2, CONTENT_W / 2],
        )
        details.setStyle(
            TableStyle(
                [
                    ("VALIGN", (0, 0), (-1, -1), "TOP"),
                    ("LEFTPADDING", (0, 0), (0, -1), 0),
                    ("LEFTPADDING", (1, 0), (1, -1), 12),
                    ("RIGHTPADDING", (0, 0), (-1, -1), 6),
                ]
            )
        )
        story.append(details)
    elif detail_cells:
        for block in detail_cells:
            for el in block:
                story.append(el)
            story.append(Spacer(1, 4 * mm))

    if invoice.footer:
        story.append(Spacer(1, 8 * mm))
        story.append(
            Paragraph(
                invoice.footer.replace("\n", "<br/>"),
                ParagraphStyle(
                    "foot", fontName="Helvetica", fontSize=8, leading=12,
                    textColor=FAINT,
                ),
            )
        )

    furniture = _page_furniture(invoice)
    doc.build(story, onFirstPage=furniture, onLaterPages=furniture)
    return buf.getvalue()
