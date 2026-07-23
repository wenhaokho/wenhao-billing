"""Write-tool tests for Task 8's remaining autonomous tools: customers,
projects, items, vendors, bills, quotations, reconciliation.

Same contract as test_mcp_write_invoices.py: FastMCP 3.4.3's `@mcp.tool`
returns the plain function (no `.fn`) — call tools directly. Every test
requests `mcp_tool_db` to bind app.mcp.db.tool_session to the
rollback-safe test connection.
"""
from __future__ import annotations

import uuid
from datetime import date
from decimal import Decimal

from app.models.bill import Bill
from app.models.invoice import Invoice
from app.models.payment import Payment
from app.models.project import Project
from app.models.quotation import Quotation
from app.mcp.tools.write_bills import create_bill, update_bill
from app.mcp.tools.write_catalog import (
    create_item,
    create_vendor,
    update_item,
    update_vendor,
)
from app.mcp.tools.write_customers import create_customer, update_customer
from app.mcp.tools.write_projects import create_project, update_project
from app.mcp.tools.write_quotations import create_quotation, update_quotation
from app.mcp.tools.write_recon import resolve_reconciliation_match


# ---------------------------------------------------------------------------
# customers
# ---------------------------------------------------------------------------


def test_create_customer_sets_fields(mcp_tool_db):
    out = create_customer(name="Beta Pte Ltd", contact_email="ap@beta.com")
    assert out["name"] == "Beta Pte Ltd"
    assert out["contact_email"] == "ap@beta.com"


def test_update_customer_edits_fields(mcp_tool_db, seed_customer, db):
    out = update_customer(customer_id=str(seed_customer), changes={"notes": "VIP"})
    assert out["customer_id"] == str(seed_customer)

    # notes isn't in _CUSTOMER_FIELDS — confirm via a direct DB read.
    from app.models.customer import Customer

    db.expire_all()
    row = db.get(Customer, seed_customer)
    assert row.notes == "VIP"


def test_update_customer_not_found_returns_error(mcp_tool_db):
    out = update_customer(customer_id=str(uuid.uuid4()), changes={"notes": "x"})
    assert "error" in out


# ---------------------------------------------------------------------------
# projects
# ---------------------------------------------------------------------------


def test_create_project_normalizes_code_and_currency(mcp_tool_db, seed_customer):
    out = create_project(
        customer_id=str(seed_customer), code="atlas-30", name="Atlas", currency="usd"
    )
    assert out["code"] == "ATLAS-30"
    assert out["currency"] == "USD"


def test_create_project_duplicate_code_returns_error_and_persists_only_one(
    mcp_tool_db, seed_customer, db
):
    create_project(customer_id=str(seed_customer), code="DUP-9", name="First", currency="USD")
    out = create_project(customer_id=str(seed_customer), code="DUP-9", name="Second", currency="USD")
    assert "error" in out

    db.expire_all()
    count = db.query(Project).filter(Project.code == "DUP-9").count()
    assert count == 1


def test_update_project_edits_fields(mcp_tool_db, seed_project):
    out = update_project(project_id=str(seed_project), changes={"status": "ON_HOLD"})
    assert out["status"] == "ON_HOLD"


# ---------------------------------------------------------------------------
# items / vendors
# ---------------------------------------------------------------------------


def test_create_item_sets_fields(mcp_tool_db):
    out = create_item(name="Consulting", item_type="SERVICE", default_unit_price="150")
    assert out["name"] == "Consulting"
    assert Decimal(out["default_unit_price"]) == Decimal("150")


def test_update_item_edits_fields(mcp_tool_db, seed_item):
    out = update_item(item_id=str(seed_item), changes={"active": False})
    assert out["active"] is False


def test_update_item_not_found_returns_error(mcp_tool_db):
    out = update_item(item_id=str(uuid.uuid4()), changes={"active": False})
    assert "error" in out


def test_create_vendor_sets_fields(mcp_tool_db):
    out = create_vendor(name="Acme Supplies")
    assert out["name"] == "Acme Supplies"


def test_update_vendor_edits_fields(mcp_tool_db, seed_vendor, db):
    out = update_vendor(vendor_id=str(seed_vendor), changes={"notes": "Preferred"})
    assert out["vendor_id"] == str(seed_vendor)

    from app.models.vendor import Vendor

    db.expire_all()
    row = db.get(Vendor, seed_vendor)
    assert row.notes == "Preferred"


# ---------------------------------------------------------------------------
# bills
# ---------------------------------------------------------------------------


def test_create_bill_makes_draft(mcp_tool_db, seed_vendor):
    out = create_bill(
        vendor_id=str(seed_vendor),
        currency="USD",
        line_items=[{"description": "AWS", "quantity": "1", "unit_price": "200"}],
    )
    assert out["status"] == "DRAFT"
    assert out["amount"] == "200.0000"


def test_update_bill_edits_open_bill(mcp_tool_db, seed_bill, db):
    out = update_bill(bill_id=str(seed_bill), changes={"notes": "please settle"})
    assert out["bill_id"] == str(seed_bill)

    db.expire_all()
    row = db.get(Bill, seed_bill)
    assert row.notes == "please settle"


def test_update_bill_not_found_returns_error(mcp_tool_db):
    missing_id = uuid.uuid4()
    out = update_bill(bill_id=str(missing_id), changes={"notes": "x"})
    assert out == {"error": f"bill {missing_id} not found"}


# ---------------------------------------------------------------------------
# quotations
# ---------------------------------------------------------------------------


def test_create_quotation_makes_draft(mcp_tool_db, seed_customer):
    out = create_quotation(
        customer_id=str(seed_customer),
        currency="USD",
        line_items=[{"description": "Design", "quantity": "10", "unit_price": "100"}],
    )
    assert out["status"] == "DRAFT"
    assert out["amount"] == "1000.0000"


def test_update_quotation_edits_draft(mcp_tool_db, seed_quotation, db):
    out = update_quotation(quotation_id=str(seed_quotation), changes={"notes": "revised terms"})
    assert out["quotation_id"] == str(seed_quotation)

    db.expire_all()
    row = db.get(Quotation, seed_quotation)
    assert row.notes == "revised terms"


def test_create_quotation_without_line_items_returns_error(mcp_tool_db, seed_customer):
    out = create_quotation(customer_id=str(seed_customer), currency="USD", line_items=[])
    assert out == {"error": "quotation must have at least one line item"}


# ---------------------------------------------------------------------------
# reconciliation — safe-stop
# ---------------------------------------------------------------------------


def test_resolve_reconciliation_match_exact_match_clears(mcp_tool_db, db, seed_customer):
    invoice = Invoice(
        customer_id=seed_customer,
        invoice_type="MILESTONE",
        invoice_number="INV-RECON-1",
        currency="USD",
        amount=Decimal("500.0000"),
        balance_due=Decimal("500.0000"),
        status="SENT",
        issue_date=date(2026, 6, 1),
        due_date=date(2026, 6, 15),
    )
    payment = Payment(
        amount=Decimal("500.0000"),
        currency="USD",
        payer_name="Ambiguous LLC",
        payment_date=date(2026, 6, 5),
        intake_source="EMAIL",
        status="PENDING_MANUAL_REVIEW",
    )
    db.add_all([invoice, payment])
    db.flush()
    invoice_id, payment_id = invoice.invoice_id, payment.payment_id
    db.commit()

    out = resolve_reconciliation_match(payment_id=str(payment_id), invoice_id=str(invoice_id))

    assert out["status"] == "CLEARED"
    db.expire_all()
    row = db.get(Payment, payment_id)
    assert row.status == "CLEARED"


def test_resolve_reconciliation_match_currency_mismatch_refuses(mcp_tool_db, db, seed_customer):
    invoice = Invoice(
        customer_id=seed_customer,
        invoice_type="MILESTONE",
        invoice_number="INV-RECON-2",
        currency="USD",
        amount=Decimal("500.0000"),
        balance_due=Decimal("500.0000"),
        status="SENT",
        issue_date=date(2026, 6, 1),
        due_date=date(2026, 6, 15),
    )
    payment = Payment(
        amount=Decimal("500.0000"),
        currency="EUR",  # mismatched currency
        payer_name="Ambiguous LLC",
        payment_date=date(2026, 6, 5),
        intake_source="EMAIL",
        status="PENDING_MANUAL_REVIEW",
    )
    db.add_all([invoice, payment])
    db.flush()
    invoice_id, payment_id = invoice.invoice_id, payment.payment_id
    db.commit()

    out = resolve_reconciliation_match(payment_id=str(payment_id), invoice_id=str(invoice_id))

    assert "error" in out
    db.expire_all()
    row = db.get(Payment, payment_id)
    assert row.status == "PENDING_MANUAL_REVIEW", (
        "safe-stop violated: currency mismatch must not change payment status"
    )


def test_resolve_reconciliation_match_amount_mismatch_refuses(mcp_tool_db, db, seed_customer):
    invoice = Invoice(
        customer_id=seed_customer,
        invoice_type="MILESTONE",
        invoice_number="INV-RECON-3",
        currency="USD",
        amount=Decimal("500.0000"),
        balance_due=Decimal("500.0000"),
        status="SENT",
        issue_date=date(2026, 6, 1),
        due_date=date(2026, 6, 15),
    )
    payment = Payment(
        amount=Decimal("450.0000"),  # mismatched amount
        currency="USD",
        payer_name="Ambiguous LLC",
        payment_date=date(2026, 6, 5),
        intake_source="EMAIL",
        status="PENDING_MANUAL_REVIEW",
    )
    db.add_all([invoice, payment])
    db.flush()
    invoice_id, payment_id = invoice.invoice_id, payment.payment_id
    db.commit()

    out = resolve_reconciliation_match(payment_id=str(payment_id), invoice_id=str(invoice_id))

    assert "error" in out
    db.expire_all()
    row = db.get(Payment, payment_id)
    assert row.status == "PENDING_MANUAL_REVIEW", (
        "safe-stop violated: amount mismatch must not change payment status"
    )
