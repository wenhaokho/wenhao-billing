"""Normalize an invoice or quotation (plus customer/business) into a single
context dict consumed by templates/document.html.j2."""
from __future__ import annotations

from datetime import date
from decimal import Decimal

from app.services.invoice_pdf import _discount_amount, _fmt_num, _fmt_pct
from app.services.pdf.assets import font_face_css
from app.services.pdf.logo import resolve_logo

_STATUS_BADGE = {
    # invoices
    "DRAFT": "draft", "SENT": "sent", "PAID": "paid",
    "PARTIALLY_PAID": "partial", "PARTIAL": "partial",
    "AWAITING_FINALIZATION": "partial", "OVERDUE": "overdue", "VOID": "draft",
    # quotations
    "ACCEPTED": "paid", "DECLINED": "overdue", "EXPIRED": "overdue",
    "INVOICED": "sent",
}


def status_badge_class(status: str | None) -> str:
    return _STATUS_BADGE.get((status or "").upper(), "sent")


def _status_label(status: str | None) -> str:
    return (status or "—").replace("_", " ").title()


def _fmt_date(d: date | None) -> str:
    return d.strftime("%d %b %Y") if d else "—"


def _addr_lines(customer) -> list[str]:
    if customer is None:
        return []
    lines: list[str] = []
    for attr in ("billing_address1", "billing_address2"):
        v = getattr(customer, attr, None)
        if v:
            lines.append(v)
    city = ", ".join(
        p for p in (
            getattr(customer, "billing_city", None),
            getattr(customer, "billing_state", None),
            getattr(customer, "billing_postal_code", None),
        ) if p
    )
    if city:
        lines.append(city)
    if getattr(customer, "billing_country", None):
        lines.append(customer.billing_country)
    if not lines and getattr(customer, "billing_address", None):
        lines = customer.billing_address.splitlines()
    return lines


def _business_lines(business) -> list[str]:
    if business is None:
        return []
    lines: list[str] = []
    if getattr(business, "address", None):
        lines += [ln for ln in business.address.splitlines() if ln.strip()]
    contact = " · ".join(
        p for p in (
            getattr(business, "contact_email", None),
            getattr(business, "contact_phone", None),
        ) if p
    )
    if contact:
        lines.append(contact)
    return lines


def _item(li) -> dict:
    return {
        "desc": li.description,
        "qty": f"{Decimal(li.quantity):g}",
        "rate": _fmt_num(li.unit_price),
        "amount": _fmt_num(li.amount),
    }


def _discount_row(discount_type, discount_value, subtotal):
    disc = _discount_amount(discount_type, discount_value, subtotal)
    if disc is None:
        return None
    suffix = f" ({_fmt_pct(discount_value)}%)" if discount_type == "PERCENT" else ""
    return {"label": f"Discount{suffix}", "value": f"−{_fmt_num(disc)}", "cls": "discount"}


def _common(doc, customer, business) -> dict:
    to_lines = _addr_lines(customer)
    if getattr(customer, "contact_email", None):
        to_lines = to_lines + [customer.contact_email]
    return {
        "font_face_css": font_face_css(),
        "logo": resolve_logo(business),
        "status_label": _status_label(doc.status),
        "status_class": status_badge_class(doc.status),
        "from_name": getattr(business, "name", None) or "—",
        "from_lines": _business_lines(business),
        "to_name": getattr(customer, "name", None) or "—",
        "to_lines": to_lines,
        "items": [_item(li) for li in doc.line_items],
        "currency": doc.currency,
        "payment_instructions": getattr(business, "payment_instructions", None),
        "notes": doc.notes or getattr(business, "default_notes", None),
        "terms": doc.payment_terms,
        "foot_name": getattr(business, "name", None) or "",
        "foot_meta": [
            p for p in (
                getattr(business, "contact_email", None),
                getattr(business, "contact_phone", None),
            ) if p
        ],
    }


def build_invoice_context(invoice, customer, business=None) -> dict:
    ctx = _common(invoice, customer, business)
    rows = [{"label": "Subtotal", "value": _fmt_num(invoice.subtotal), "cls": "subtotal"}]
    disc = _discount_row(invoice.discount_type, invoice.discount_value, invoice.subtotal)
    if disc:
        rows.append(disc)
    paid = None
    if invoice.amount is not None and invoice.balance_due is not None:
        p = Decimal(invoice.amount) - Decimal(invoice.balance_due)
        if p > 0:
            paid = p.quantize(Decimal("0.01"))
    if paid is not None:
        rows.append({"label": "Total", "value": _fmt_num(invoice.amount), "cls": "total"})
        rows.append({"label": "Paid", "value": f"−{_fmt_num(paid)}", "cls": "paid"})
    ctx.update({
        "doc_title": (getattr(business, "invoice_title", None) or "INVOICE").upper(),
        "ref": invoice.invoice_number or "—",
        "meta": [
            ("Invoice No.", invoice.invoice_number or "—"),
            ("Issue Date", _fmt_date(invoice.issue_date)),
            ("Due Date", _fmt_date(invoice.due_date)),
        ],
        "summary_rows": rows,
        "hero_label": "Total Due",
        "hero_amount": _fmt_num(invoice.balance_due),
    })
    return ctx


def build_quotation_context(quotation, customer, business=None) -> dict:
    ctx = _common(quotation, customer, business)
    rows = [{"label": "Subtotal", "value": _fmt_num(quotation.subtotal), "cls": "subtotal"}]
    disc = _discount_row(quotation.discount_type, quotation.discount_value, quotation.subtotal)
    if disc:
        rows.append(disc)
    ctx.update({
        "doc_title": "QUOTATION",
        "ref": quotation.quotation_number or "—",
        "meta": [
            ("Quote No.", quotation.quotation_number or "—"),
            ("Issue Date", _fmt_date(quotation.issue_date)),
            ("Valid Until", _fmt_date(quotation.valid_until)),
        ],
        "summary_rows": rows,
        "hero_label": "Total",
        "hero_amount": _fmt_num(quotation.amount),
    })
    return ctx
