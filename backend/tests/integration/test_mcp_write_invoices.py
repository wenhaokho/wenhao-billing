"""Invoice write-tool tests.

Covers create/update/finalize/void/recurring-template/trigger-recurring
wrappers over app.services.invoicing, plus the error-wrapping contract:
InvoicingError -> {"error": str(e)} with no partial write persisted (the
`with tool_session():` block rolls back before our except catches it).

FastMCP 3.4.3's `@mcp.tool` returns the plain function (no `.fn`) — tools
are called directly. Every test requests `mcp_tool_db` to bind
app.mcp.db.tool_session to the rollback-safe test connection.
"""
from __future__ import annotations

import uuid
from decimal import Decimal

from app.mcp.tools.write_invoices import (
    create_invoice,
    create_recurring_template,
    finalize_invoice,
    trigger_recurring,
    update_invoice,
    void_invoice,
)
from app.models.invoice import Invoice


def test_create_invoice_makes_draft(mcp_tool_db, seed_customer):
    out = create_invoice(
        customer_id=str(seed_customer),
        currency="USD",
        line_items=[{"description": "Design", "quantity": "40", "unit_price": "100"}],
    )
    assert out["status"] == "DRAFT"
    assert out["amount"] == "4000.0000"
    assert out["customer_id"] == str(seed_customer)


def test_create_invoice_applies_percent_discount(mcp_tool_db, seed_customer):
    out = create_invoice(
        customer_id=str(seed_customer),
        currency="USD",
        line_items=[{"description": "Dev", "quantity": "10", "unit_price": "100"}],
        discount_type="PERCENT",
        discount_value="10",
    )
    assert out["amount"] == "900.0000"


def test_finalize_moves_draft_to_open(mcp_tool_db, seed_customer):
    created = create_invoice(
        customer_id=str(seed_customer),
        currency="USD",
        line_items=[{"description": "x", "quantity": "1", "unit_price": "10"}],
    )
    out = finalize_invoice(invoice_id=created["invoice_id"])
    # Finalizing issues the invoice (OPEN); it only becomes SENT once emailed.
    assert out["status"] == "OPEN"


def test_void_invoice_sets_void_and_zeroes_balance(mcp_tool_db, seed_invoice):
    out = void_invoice(invoice_id=str(seed_invoice))
    assert out["status"] == "VOID"
    assert out["balance_due"] == "0"


def test_update_invoice_edits_draft(mcp_tool_db, seed_customer, db):
    created = create_invoice(
        customer_id=str(seed_customer),
        currency="USD",
        line_items=[{"description": "x", "quantity": "1", "unit_price": "10"}],
    )
    out = update_invoice(
        invoice_id=created["invoice_id"], changes={"notes": "please pay promptly"}
    )
    assert out["invoice_id"] == created["invoice_id"]

    # notes isn't in _INVOICE_FIELDS, so confirm via a direct DB read.
    db.expire_all()
    row = db.get(Invoice, uuid.UUID(created["invoice_id"]))
    assert row.notes == "please pay promptly"


def test_create_invoice_without_line_items_returns_error(mcp_tool_db, seed_customer, db):
    before = db.query(Invoice).filter(Invoice.customer_id == seed_customer).count()

    out = create_invoice(customer_id=str(seed_customer), currency="USD", line_items=[])

    assert out == {"error": "invoice must have at least one line item"}
    after = db.query(Invoice).filter(Invoice.customer_id == seed_customer).count()
    assert after == before, "failed create_invoice must not persist any invoice row"


def test_finalize_non_draft_returns_error_and_leaves_state_unchanged(
    mcp_tool_db, seed_invoice, db
):
    # seed_invoice is already SENT — finalizing it must fail cleanly and
    # must not mutate the row (proves the tool_session rollback on error).
    out = finalize_invoice(invoice_id=str(seed_invoice))

    assert out == {"error": "only DRAFT invoices can be finalized (got SENT)"}

    db.expire_all()
    row = db.get(Invoice, seed_invoice)
    assert row.status == "SENT"
    assert row.balance_due == Decimal("1000.0000")


def test_update_invoice_on_non_draft_returns_error_and_leaves_notes_unchanged(
    mcp_tool_db, seed_invoice, db
):
    out = update_invoice(invoice_id=str(seed_invoice), changes={"notes": "attempted change"})

    assert out == {"error": "only DRAFT invoices can be edited (got SENT)"}

    db.expire_all()
    row = db.get(Invoice, seed_invoice)
    assert row.notes is None


def test_create_recurring_template_and_trigger_recurring(mcp_tool_db, seed_customer):
    template = create_recurring_template(
        customer_id=str(seed_customer),
        currency="USD",
        line_items=[{"description": "Hosting", "quantity": "1", "unit_price": "50"}],
        schedule={
            "frequency": "MONTHLY",
            "interval": 1,
            "start_date": "2026-08-01",
            "end_mode": "NEVER",
        },
        payment_terms="Net 14",
    )
    assert template["status"] == "DRAFT"
    assert template["amount"] == "50.0000"

    triggered = trigger_recurring(template_id=template["invoice_id"], cycle_key="2026-08-01")
    assert triggered["status"] == "DRAFT"
    assert triggered["amount"] == "50.0000"
    assert triggered["invoice_id"] != template["invoice_id"]

    # Idempotent: same cycle_key returns the same generated invoice, not a new one.
    triggered_again = trigger_recurring(
        template_id=template["invoice_id"], cycle_key="2026-08-01"
    )
    assert triggered_again["invoice_id"] == triggered["invoice_id"]


def test_trigger_recurring_on_non_recurring_template_returns_error(mcp_tool_db, seed_invoice):
    out = trigger_recurring(template_id=str(seed_invoice), cycle_key="2026-08-01")
    assert out == {"error": f"invoice {seed_invoice} is not RECURRING"}
