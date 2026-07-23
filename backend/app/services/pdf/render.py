"""Render invoice/quotation context into self-contained HTML via Jinja2."""
from __future__ import annotations

from pathlib import Path

from jinja2 import Environment, FileSystemLoader, select_autoescape

from app.services.pdf.context import build_invoice_context, build_quotation_context

_env = Environment(
    loader=FileSystemLoader(str(Path(__file__).parent / "templates")),
    autoescape=select_autoescape(["html", "j2"]),
)


def render_invoice_html(invoice, customer, business=None) -> str:
    ctx = build_invoice_context(invoice, customer, business)
    return _env.get_template("document.html.j2").render(**ctx)


def render_quotation_html(quotation, customer, business=None) -> str:
    ctx = build_quotation_context(quotation, customer, business)
    return _env.get_template("document.html.j2").render(**ctx)
