"""PDF façade: HTML→PDF first, reportlab as automatic fallback.

Set settings.pdf_engine="reportlab" to force the legacy renderer.
"""
from __future__ import annotations

import logging

from app.config import get_settings
from app.services.invoice_pdf import render_invoice_pdf as _reportlab_invoice
from app.services.pdf.engine import html_to_pdf
from app.services.pdf.render import (
    render_invoice_html,
    render_quotation_html,
    render_receipt_html,
)
from app.services.quotation_pdf import render_quotation_pdf as _reportlab_quotation
from app.services.receipt_pdf import render_receipt_pdf as _reportlab_receipt

log = logging.getLogger(__name__)

__all__ = ["render_invoice_pdf", "render_quotation_pdf", "render_receipt_pdf"]


def render_invoice_pdf(invoice, customer, business=None) -> bytes:
    if get_settings().pdf_engine == "reportlab":
        return _reportlab_invoice(invoice, customer, business)
    try:
        return html_to_pdf(render_invoice_html(invoice, customer, business))
    except Exception:
        log.exception("HTML invoice render failed; falling back to reportlab")
        return _reportlab_invoice(invoice, customer, business)


def render_quotation_pdf(quotation, customer, business=None) -> bytes:
    if get_settings().pdf_engine == "reportlab":
        return _reportlab_quotation(quotation, customer)
    try:
        return html_to_pdf(render_quotation_html(quotation, customer, business))
    except Exception:
        log.exception("HTML quotation render failed; falling back to reportlab")
        return _reportlab_quotation(quotation, customer)


def render_receipt_pdf(payment, invoice, customer=None, business=None) -> bytes:
    if get_settings().pdf_engine == "reportlab":
        return _reportlab_receipt(payment, invoice, customer, business)
    try:
        return html_to_pdf(render_receipt_html(payment, invoice, customer, business))
    except Exception:
        log.exception("HTML receipt render failed; falling back to reportlab")
        return _reportlab_receipt(payment, invoice, customer, business)
