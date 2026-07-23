"""Invoice read tools.

Covers:
- list_invoices: excludes templates, filters by status/customer_id/
  invoice_type, clamps limit to <=500.
- get_invoice: found vs. not-found (returns {}).

FastMCP 3.4.3's `@mcp.tool` returns the plain function (no `.fn`) — tools
are called directly, per app/mcp/tools/read_misc.py's established pattern.
"""
from __future__ import annotations

import uuid
from decimal import Decimal

from app.mcp.tools.read_invoices import get_invoice, list_invoices
from app.models.invoice import Invoice


def test_list_invoices_excludes_templates(mcp_tool_db, seed_invoice, db):
    db.add(
        Invoice(
            invoice_type="MILESTONE",
            currency="USD",
            amount=Decimal("500.0000"),
            balance_due=Decimal("500.0000"),
            status="DRAFT",
            is_template=True,
        )
    )
    db.flush()

    out = list_invoices()
    assert len(out) == 1
    assert out[0]["invoice_id"] == str(seed_invoice)


def test_list_invoices_filters_by_status(mcp_tool_db, seed_customer, db):
    sent = Invoice(
        customer_id=seed_customer,
        invoice_type="MILESTONE",
        currency="USD",
        amount=Decimal("100.0000"),
        balance_due=Decimal("100.0000"),
        status="SENT",
    )
    draft = Invoice(
        customer_id=seed_customer,
        invoice_type="MILESTONE",
        currency="USD",
        amount=Decimal("200.0000"),
        balance_due=Decimal("200.0000"),
        status="DRAFT",
    )
    db.add_all([sent, draft])
    db.flush()

    out = list_invoices(status=["SENT"])
    assert len(out) == 1
    assert out[0]["status"] == "SENT"


def test_list_invoices_filters_by_customer_id(mcp_tool_db, seed_invoice, seed_customer, db):
    from app.models.customer import Customer

    other_customer = Customer(name="Other Co", matching_aliases=[])
    db.add(other_customer)
    db.flush()
    db.add(
        Invoice(
            customer_id=other_customer.customer_id,
            invoice_type="MILESTONE",
            currency="USD",
            amount=Decimal("300.0000"),
            balance_due=Decimal("300.0000"),
            status="SENT",
        )
    )
    db.flush()

    out = list_invoices(customer_id=str(seed_customer))
    assert len(out) == 1
    assert out[0]["invoice_id"] == str(seed_invoice)


def test_list_invoices_filters_by_invoice_type(mcp_tool_db, seed_customer, db):
    milestone = Invoice(
        customer_id=seed_customer,
        invoice_type="MILESTONE",
        currency="USD",
        amount=Decimal("100.0000"),
        balance_due=Decimal("100.0000"),
        status="SENT",
    )
    recurring = Invoice(
        customer_id=seed_customer,
        invoice_type="RECURRING",
        currency="USD",
        amount=Decimal("100.0000"),
        balance_due=Decimal("100.0000"),
        status="SENT",
    )
    db.add_all([milestone, recurring])
    db.flush()

    out = list_invoices(invoice_type="RECURRING")
    assert len(out) == 1
    assert out[0]["invoice_type"] == "RECURRING"


def test_list_invoices_limit_clamped_to_500(mcp_tool_db, seed_invoice, db):
    # A limit above 500 must not raise and must still return results
    # (proves min(limit, 500) is applied rather than passed straight to .limit()).
    out = list_invoices(limit=10_000)
    assert len(out) == 1


def test_list_invoices_returns_money_as_decimal_strings(mcp_tool_db, seed_invoice):
    out = list_invoices()
    assert out[0]["amount"] == "1000.0000"
    assert isinstance(out[0]["amount"], str)


def test_get_invoice_found(mcp_tool_db, seed_invoice):
    out = get_invoice(invoice_id=str(seed_invoice))
    assert out["invoice_id"] == str(seed_invoice)
    assert out["invoice_number"] == "INV-0001"
    assert out["amount"] == "1000.0000"


def test_get_invoice_not_found_returns_empty_dict(mcp_tool_db, db):
    out = get_invoice(invoice_id=str(uuid.uuid4()))
    assert out == {}
