"""Read tools for bills, quotations, projects, catalog (items+vendors), recon.

Covers one test function per tool in Task 6's mapping table:
- list_bills / get_bill
- list_quotations / get_quotation
- list_projects / get_project
- list_items
- list_vendors / resolve_vendor
- get_reconciliation_queue

FastMCP 3.4.3's `@mcp.tool` returns the plain function (no `.fn`) — tools
are called directly, per app/mcp/tools/read_misc.py's established pattern.
"""
from __future__ import annotations

import uuid
from decimal import Decimal


# ---------------------------------------------------------------------------
# bills
# ---------------------------------------------------------------------------

def test_list_bills_returns_list(mcp_tool_db, seed_bill):
    from app.mcp.tools.read_bills import list_bills

    out = list_bills()
    assert isinstance(out, list)
    assert len(out) == 1
    assert out[0]["bill_id"] == str(seed_bill)
    assert out[0]["amount"] == "500.0000"
    assert isinstance(out[0]["amount"], str)


def test_list_bills_filters_by_status(mcp_tool_db, seed_bill, seed_vendor, db):
    from app.models.bill import Bill
    from app.mcp.tools.read_bills import list_bills

    void_bill = Bill(
        vendor_id=seed_vendor,
        currency="USD",
        amount=Decimal("10.0000"),
        balance_due=Decimal("10.0000"),
        status="VOID",
    )
    db.add(void_bill)
    db.flush()

    out = list_bills(status=["OPEN"])
    assert len(out) == 1
    assert out[0]["status"] == "OPEN"


def test_list_bills_filters_by_vendor_id(mcp_tool_db, seed_bill, seed_vendor, db):
    from app.models.bill import Bill
    from app.models.vendor import Vendor
    from app.mcp.tools.read_bills import list_bills

    other_vendor = Vendor(name="Other Vendor")
    db.add(other_vendor)
    db.flush()
    db.add(
        Bill(
            vendor_id=other_vendor.vendor_id,
            currency="USD",
            amount=Decimal("20.0000"),
            balance_due=Decimal("20.0000"),
            status="OPEN",
        )
    )
    db.flush()

    out = list_bills(vendor_id=str(seed_vendor))
    assert len(out) == 1
    assert out[0]["bill_id"] == str(seed_bill)


def test_get_bill_found(mcp_tool_db, seed_bill):
    from app.mcp.tools.read_bills import get_bill

    out = get_bill(bill_id=str(seed_bill))
    assert out["bill_id"] == str(seed_bill)
    assert out["bill_number"] == "BILL-0001"


def test_get_bill_not_found_returns_empty_dict(mcp_tool_db, db):
    from app.mcp.tools.read_bills import get_bill

    out = get_bill(bill_id=str(uuid.uuid4()))
    assert out == {}


# ---------------------------------------------------------------------------
# quotations
# ---------------------------------------------------------------------------

def test_list_quotations_returns_list(mcp_tool_db, seed_quotation):
    from app.mcp.tools.read_quotations import list_quotations

    out = list_quotations()
    assert isinstance(out, list)
    assert len(out) == 1
    assert out[0]["quotation_id"] == str(seed_quotation)
    assert out[0]["amount"] == "750.0000"


def test_list_quotations_filters_by_status(mcp_tool_db, seed_quotation, seed_customer, db):
    from app.models.quotation import Quotation
    from app.mcp.tools.read_quotations import list_quotations

    db.add(
        Quotation(
            customer_id=seed_customer,
            currency="USD",
            amount=Decimal("50.0000"),
            status="SENT",
        )
    )
    db.flush()

    out = list_quotations(status=["DRAFT"])
    assert len(out) == 1
    assert out[0]["status"] == "DRAFT"


def test_list_quotations_filters_by_customer_id(mcp_tool_db, seed_quotation, seed_customer, db):
    from app.models.customer import Customer
    from app.models.quotation import Quotation
    from app.mcp.tools.read_quotations import list_quotations

    other = Customer(name="Other Co", matching_aliases=[])
    db.add(other)
    db.flush()
    db.add(
        Quotation(
            customer_id=other.customer_id,
            currency="USD",
            amount=Decimal("50.0000"),
            status="DRAFT",
        )
    )
    db.flush()

    out = list_quotations(customer_id=str(seed_customer))
    assert len(out) == 1
    assert out[0]["quotation_id"] == str(seed_quotation)


def test_get_quotation_found(mcp_tool_db, seed_quotation):
    from app.mcp.tools.read_quotations import get_quotation

    out = get_quotation(quotation_id=str(seed_quotation))
    assert out["quotation_id"] == str(seed_quotation)
    assert out["quotation_number"] == "QUO-0001"


def test_get_quotation_not_found_returns_empty_dict(mcp_tool_db, db):
    from app.mcp.tools.read_quotations import get_quotation

    out = get_quotation(quotation_id=str(uuid.uuid4()))
    assert out == {}


# ---------------------------------------------------------------------------
# projects
# ---------------------------------------------------------------------------

def test_list_projects_returns_list(mcp_tool_db, seed_project):
    from app.mcp.tools.read_projects import list_projects

    out = list_projects()
    assert isinstance(out, list)
    assert len(out) == 1
    assert out[0]["project_id"] == str(seed_project)
    assert out[0]["code"] == "ATLAS-26"


def test_list_projects_filters_by_customer_id(mcp_tool_db, seed_project, seed_customer, db):
    from app.models.customer import Customer
    from app.models.project import Project
    from app.mcp.tools.read_projects import list_projects

    other = Customer(name="Other Co", matching_aliases=[])
    db.add(other)
    db.flush()
    db.add(
        Project(
            customer_id=other.customer_id,
            code="OTHER-26",
            name="Other Project",
            currency="USD",
        )
    )
    db.flush()

    out = list_projects(customer_id=str(seed_customer))
    assert len(out) == 1
    assert out[0]["project_id"] == str(seed_project)


def test_get_project_found(mcp_tool_db, seed_project):
    from app.mcp.tools.read_projects import get_project

    out = get_project(project_id=str(seed_project))
    assert out["project_id"] == str(seed_project)
    assert out["name"] == "Atlas Project"


def test_get_project_not_found_returns_empty_dict(mcp_tool_db, db):
    from app.mcp.tools.read_projects import get_project

    out = get_project(project_id=str(uuid.uuid4()))
    assert out == {}


# ---------------------------------------------------------------------------
# catalog: items + vendors + resolve_vendor
# ---------------------------------------------------------------------------

def test_list_items_returns_list(mcp_tool_db, seed_item):
    from app.mcp.tools.read_catalog import list_items

    out = list_items()
    assert isinstance(out, list)
    assert len(out) == 1
    assert out[0]["item_id"] == str(seed_item)
    assert out[0]["name"] == "Web Hosting"


def test_list_items_filters_by_query(mcp_tool_db, seed_item, db):
    from app.models.item import Item
    from app.mcp.tools.read_catalog import list_items

    db.add(Item(name="Design Retainer", item_type="SERVICE", default_currency="USD"))
    db.flush()

    out = list_items(query="Hosting")
    assert len(out) == 1
    assert out[0]["item_id"] == str(seed_item)


def test_list_vendors_returns_list(mcp_tool_db, seed_vendor):
    from app.mcp.tools.read_catalog import list_vendors

    out = list_vendors()
    assert isinstance(out, list)
    assert len(out) == 1
    assert out[0]["vendor_id"] == str(seed_vendor)
    assert out[0]["name"] == "Acme Vendor Pte Ltd"


def test_list_vendors_filters_by_query(mcp_tool_db, seed_vendor, db):
    from app.models.vendor import Vendor
    from app.mcp.tools.read_catalog import list_vendors

    db.add(Vendor(name="Zephyr Supplies"))
    db.flush()

    out = list_vendors(query="Acme")
    assert len(out) == 1
    assert out[0]["vendor_id"] == str(seed_vendor)


def test_resolve_vendor_unique_match(mcp_tool_db, seed_vendor):
    from app.mcp.tools.read_catalog import resolve_vendor

    out = resolve_vendor(query="Acme")
    assert out == {"vendor_id": str(seed_vendor)}


def test_resolve_vendor_ambiguous_returns_candidates(mcp_tool_db, seed_two_acme_vendors):
    from app.mcp.tools.read_catalog import resolve_vendor

    out = resolve_vendor(query="Acme")
    assert "candidates" in out
    assert len(out["candidates"]) == 2
    assert "vendor_id" not in out  # took NO other action
    ids = {c["vendor_id"] for c in out["candidates"]}
    assert ids == {str(vid) for vid in seed_two_acme_vendors}
    for c in out["candidates"]:
        assert set(c.keys()) == {"vendor_id", "name"}


def test_resolve_vendor_no_match_returns_empty_candidates(mcp_tool_db, seed_vendor):
    from app.mcp.tools.read_catalog import resolve_vendor

    out = resolve_vendor(query="Nonexistent Supplier")
    assert out == {"candidates": []}


# ---------------------------------------------------------------------------
# reconciliation queue
# ---------------------------------------------------------------------------

def test_get_reconciliation_queue_returns_list(mcp_tool_db, seed_payment_pending_review):
    from app.mcp.tools.read_recon import get_reconciliation_queue

    out = get_reconciliation_queue()
    assert isinstance(out, list)
    assert len(out) == 1
    assert out[0]["payment_id"] == str(seed_payment_pending_review)
    assert out[0]["status"] == "PENDING_MANUAL_REVIEW"


def test_get_reconciliation_queue_excludes_other_statuses(
    mcp_tool_db, seed_payment_pending_review, db
):
    from datetime import date

    from app.models.payment import Payment
    from app.mcp.tools.read_recon import get_reconciliation_queue

    db.add(
        Payment(
            amount=Decimal("100.0000"),
            currency="USD",
            payer_name="Cleared Payer",
            payment_date=date(2026, 1, 10),
            intake_source="EMAIL",
            status="CLEARED",
        )
    )
    db.flush()

    out = get_reconciliation_queue()
    assert len(out) == 1
    assert out[0]["payment_id"] == str(seed_payment_pending_review)


def test_get_reconciliation_queue_respects_limit(mcp_tool_db, seed_payment_pending_review, db):
    from datetime import date

    from app.models.payment import Payment
    from app.mcp.tools.read_recon import get_reconciliation_queue

    for i in range(3):
        db.add(
            Payment(
                amount=Decimal("1.0000"),
                currency="USD",
                payer_name=f"Payer {i}",
                payment_date=date(2026, 1, 1),
                intake_source="EMAIL",
                status="PENDING_MANUAL_REVIEW",
            )
        )
    db.flush()

    out = get_reconciliation_queue(limit=2)
    assert len(out) == 2
