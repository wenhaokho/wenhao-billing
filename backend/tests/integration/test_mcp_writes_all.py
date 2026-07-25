"""Write-tool tests for Task 8's remaining autonomous tools: customers,
projects, items, vendors, bills, quotations, invoices, reconciliation.

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
from app.mcp.tools.write_invoices import update_invoice
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


def test_create_customer_stores_phone_and_address_in_their_own_columns(mcp_tool_db, db):
    """Phone/address args must reach their real columns — before this existed
    the tool had no such args, so an assistant asked for a customer "with
    phone X at address Y" could only stuff both into `notes`."""
    from app.models.customer import Customer

    out = create_customer(
        name="Gamma Ltd",
        contact_phone="+62 811 2233",
        billing_address1="Jl. Sudirman 52",
        billing_city="Jakarta",
        billing_postal_code="12190",
        billing_country="Indonesia",
        website="https://gamma.example",
    )

    db.expire_all()
    row = db.get(Customer, uuid.UUID(out["customer_id"]))
    assert row.contact_phone == "+62 811 2233"
    assert row.billing_address1 == "Jl. Sudirman 52"
    assert row.billing_city == "Jakarta"
    assert row.billing_postal_code == "12190"
    assert row.billing_country == "Indonesia"
    assert row.website == "https://gamma.example"
    assert row.notes is None


def test_create_customer_echoes_back_contact_and_address(mcp_tool_db):
    """The tool return has to show the address/contact fields, otherwise a
    caller has no way to notice data that never landed."""
    out = create_customer(
        name="Delta Ltd", contact_phone="+62 811 0000", billing_city="Bandung"
    )
    assert out["contact_phone"] == "+62 811 0000"
    assert out["billing_city"] == "Bandung"


def test_update_customer_rejects_unknown_field_names(mcp_tool_db, seed_customer, db):
    """`changes` keys that aren't CustomerUpdate fields must error, not be
    silently dropped by Pydantic's default extra="ignore"."""
    from app.models.customer import Customer

    out = update_customer(
        customer_id=str(seed_customer),
        changes={"phone": "+62 811 4455", "address": "Jl. Thamrin 1"},
    )
    assert "error" in out
    assert "phone" in out["error"] and "address" in out["error"]

    db.expire_all()
    row = db.get(Customer, seed_customer)
    assert row.contact_phone is None
    assert row.billing_address1 is None


def test_update_customer_writes_address_fields(mcp_tool_db, seed_customer, db):
    from app.models.customer import Customer

    out = update_customer(
        customer_id=str(seed_customer),
        changes={"contact_phone": "+62 811 9999", "billing_address1": "Jl. Thamrin 1"},
    )
    assert "error" not in out

    db.expire_all()
    row = db.get(Customer, seed_customer)
    assert row.contact_phone == "+62 811 9999"
    assert row.billing_address1 == "Jl. Thamrin 1"


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


def test_update_project_rejects_unknown_field_names(mcp_tool_db, seed_project, db):
    """`changes` keys that aren't ProjectUpdate fields must error, not be
    silently dropped by Pydantic's default extra="ignore"."""
    out = update_project(
        project_id=str(seed_project),
        changes={"project_name": "Renamed", "budget": "5000"},
    )
    assert "error" in out
    assert "project_name" in out["error"] and "budget" in out["error"]

    db.expire_all()
    row = db.get(Project, seed_project)
    assert row.name != "Renamed"


def test_create_project_echoes_notes(mcp_tool_db, seed_customer):
    """Single-record returns must echo notes back, otherwise a caller has no
    way to see that supplied data landed."""
    out = create_project(
        customer_id=str(seed_customer),
        code="ECHO-1",
        name="Echo",
        currency="USD",
        notes="phase one only",
    )
    assert out["notes"] == "phase one only"


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


def test_update_item_rejects_unknown_field_names(mcp_tool_db, seed_item, db):
    """`changes` keys that aren't ItemUpdate fields must error, not be
    silently dropped by Pydantic's default extra="ignore". `unit_price` is
    the realistic near-miss: the real field is `default_unit_price`."""
    from app.models.item import Item

    out = update_item(
        item_id=str(seed_item),
        changes={"unit_price": "99", "price_currency": "USD"},
    )
    assert "error" in out
    assert "unit_price" in out["error"] and "price_currency" in out["error"]

    db.expire_all()
    row = db.get(Item, seed_item)
    assert row.default_unit_price != Decimal("99")


def test_create_item_sets_account_ids(mcp_tool_db, db):
    """revenue_account_id/expense_account_id are real ItemCreate fields — the
    tool must expose them rather than leaving callers no way to set them."""
    from app.models.coa import ChartOfAccount
    from app.models.item import Item

    revenue = db.query(ChartOfAccount).filter(ChartOfAccount.type == "INCOME").first()
    expense = db.query(ChartOfAccount).filter(ChartOfAccount.type == "EXPENSE").first()
    assert revenue is not None and expense is not None, "chart of accounts not seeded"

    out = create_item(
        name="Retainer",
        item_type="SERVICE",
        revenue_account_id=revenue.account_id,
        expense_account_id=expense.account_id,
    )
    assert "error" not in out

    db.expire_all()
    row = db.get(Item, uuid.UUID(out["item_id"]))
    assert row.revenue_account_id == revenue.account_id
    assert row.expense_account_id == expense.account_id


def test_create_item_echoes_description_and_flags(mcp_tool_db):
    """Single-record returns must echo the supplied description/flags back."""
    out = create_item(
        name="Support Plan",
        item_type="SERVICE",
        description="24/7 cover",
        is_purchased=True,
    )
    assert out["description"] == "24/7 cover"
    assert out["is_purchased"] is True
    assert out["is_sold"] is True


def test_create_vendor_sets_fields(mcp_tool_db):
    out = create_vendor(name="Acme Supplies")
    assert out["name"] == "Acme Supplies"


def test_create_vendor_can_set_active(mcp_tool_db, db):
    """`active` is a real writable Vendor column — creating an inactive vendor
    must be possible without a follow-up update_vendor call."""
    from app.models.vendor import Vendor

    out = create_vendor(name="Dormant Supplies", active=False)

    db.expire_all()
    row = db.get(Vendor, uuid.UUID(out["vendor_id"]))
    assert row.active is False


def test_create_vendor_echoes_tax_id_and_notes(mcp_tool_db):
    """Single-record returns must echo tax_id/notes back — they are accepted
    args, so a caller needs to see they landed."""
    out = create_vendor(
        name="Beta Supplies", tax_id="01.234.567.8-901.000", notes="net 45 agreed"
    )
    assert out["tax_id"] == "01.234.567.8-901.000"
    assert out["notes"] == "net 45 agreed"


def test_update_vendor_edits_fields(mcp_tool_db, seed_vendor, db):
    out = update_vendor(vendor_id=str(seed_vendor), changes={"notes": "Preferred"})
    assert out["vendor_id"] == str(seed_vendor)

    from app.models.vendor import Vendor

    db.expire_all()
    row = db.get(Vendor, seed_vendor)
    assert row.notes == "Preferred"


def test_update_vendor_rejects_unknown_field_names(mcp_tool_db, seed_vendor, db):
    """`changes` keys that aren't VendorUpdate fields must error rather than be
    dropped. Vendor genuinely has no phone/address columns, so rejecting is the
    only honest answer — silently succeeding would strand the data."""
    from app.models.vendor import Vendor

    out = update_vendor(
        vendor_id=str(seed_vendor),
        changes={"phone": "+62 811 4455", "address": "Jl. Thamrin 1"},
    )
    assert "error" in out
    assert "phone" in out["error"] and "address" in out["error"]

    db.expire_all()
    row = db.get(Vendor, seed_vendor)
    assert row.notes is None


def test_create_item_duplicate_sku_returns_error(mcp_tool_db, db):
    """A duplicate SKU (unique constraint) must surface as {"error": ...}
    rather than propagating the DB IntegrityError, and must not persist a
    second row."""
    from app.models.item import Item

    first = create_item(name="First", item_type="SERVICE", sku="DUP-SKU-1")
    assert "error" not in first
    out = create_item(name="Second", item_type="SERVICE", sku="DUP-SKU-1")
    assert "error" in out

    db.expire_all()
    assert db.query(Item).filter(Item.sku == "DUP-SKU-1").count() == 1


def test_create_vendor_integrity_error_returns_error(mcp_tool_db):
    """A DB IntegrityError raised while creating a vendor is wrapped as
    {"error": ...} rather than propagating (vendors have no unique constraint,
    so the constraint violation is simulated at the service boundary)."""
    from unittest.mock import patch

    from sqlalchemy.exc import IntegrityError

    from app.mcp.tools import write_catalog

    boom = IntegrityError("INSERT INTO vendors ...", {}, Exception("duplicate vendor"))
    with patch.object(write_catalog.vendor_service, "create_vendor", side_effect=boom):
        out = create_vendor(name="Acme Supplies")
    assert "error" in out
    assert "duplicate vendor" in out["error"]


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


def test_update_bill_rejects_unknown_field_names(mcp_tool_db, seed_bill, db):
    """`changes` keys that aren't BillUpdate fields must error, not be silently
    dropped by Pydantic's default extra="ignore"."""
    out = update_bill(
        bill_id=str(seed_bill),
        changes={"note": "please settle", "terms": "NET30"},
    )
    assert "error" in out
    assert "note" in out["error"] and "terms" in out["error"]

    db.expire_all()
    row = db.get(Bill, seed_bill)
    assert row.notes is None
    assert row.payment_terms is None


def test_update_bill_unknown_field_check_precedes_not_found(mcp_tool_db):
    """The unknown-key guard runs before the DB is touched, so a bad key on a
    missing bill reports the bad key rather than the not-found."""
    out = update_bill(bill_id=str(uuid.uuid4()), changes={"note": "x"})
    assert "error" in out
    assert "note" in out["error"]
    assert "not found" not in out["error"]


def test_update_bill_echoes_notes(mcp_tool_db, seed_bill):
    """Single-record returns must echo notes back so a caller can see the edit
    landed without a second read."""
    out = update_bill(bill_id=str(seed_bill), changes={"notes": "please settle"})
    assert out["notes"] == "please settle"


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


def test_update_quotation_rejects_unknown_field_names(mcp_tool_db, seed_quotation, db):
    """`changes` keys that aren't QuotationUpdate fields must error, not be
    silently dropped by Pydantic's default extra="ignore"."""
    out = update_quotation(
        quotation_id=str(seed_quotation),
        changes={"note": "revised terms", "valid_till": "2026-12-31"},
    )
    assert "error" in out
    assert "note" in out["error"] and "valid_till" in out["error"]

    db.expire_all()
    row = db.get(Quotation, seed_quotation)
    assert row.notes is None


def test_update_quotation_echoes_notes(mcp_tool_db, seed_quotation):
    """Single-record returns must echo notes back."""
    out = update_quotation(
        quotation_id=str(seed_quotation), changes={"notes": "revised terms"}
    )
    assert out["notes"] == "revised terms"


# ---------------------------------------------------------------------------
# invoices
# ---------------------------------------------------------------------------


def test_update_invoice_rejects_unknown_field_names(mcp_tool_db, seed_draft_invoice, db):
    """`changes` keys that aren't InvoiceUpdate fields must error, not be
    silently dropped by Pydantic's default extra="ignore"."""
    out = update_invoice(
        invoice_id=str(seed_draft_invoice),
        changes={"note": "pay promptly", "terms": "NET30"},
    )
    assert "error" in out
    assert "note" in out["error"] and "terms" in out["error"]

    db.expire_all()
    row = db.get(Invoice, seed_draft_invoice)
    assert row.notes is None
    assert row.payment_terms is None


def test_update_invoice_echoes_notes(mcp_tool_db, seed_draft_invoice):
    """Single-record returns must echo notes back."""
    out = update_invoice(
        invoice_id=str(seed_draft_invoice), changes={"notes": "pay promptly"}
    )
    assert out["notes"] == "pay promptly"


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
        currency="GBP",  # mismatched currency
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
