"""Seed a handful of vendors and items (services catalog) for demo/dev.

Idempotent: skips if at least 2 vendors / items already exist.

Usage:
    docker compose exec backend python -m scripts.seed_master_data
"""
from __future__ import annotations

import uuid
from decimal import Decimal

from sqlalchemy import func, select

from app.db.session import SessionLocal
from app.models.coa import ChartOfAccount
from app.models.item import Item
from app.models.vendor import Vendor


def _get_account(db, code: str) -> int | None:
    acc = db.scalar(select(ChartOfAccount).where(ChartOfAccount.code == code))
    return acc.account_id if acc else None


def seed() -> None:
    with SessionLocal() as db:
        v_count = db.scalar(select(func.count(Vendor.vendor_id))) or 0
        i_count = db.scalar(select(func.count(Item.item_id))) or 0

        if v_count < 2:
            vendors = [
                Vendor(
                    vendor_id=uuid.uuid4(),
                    name="OpenAI Platform",
                    contact_email="billing@openai.com",
                    default_currency="USD",
                    payment_terms_days=0,
                    notes="LLM inference — AI COGS",
                ),
                Vendor(
                    vendor_id=uuid.uuid4(),
                    name="AWS",
                    contact_email="aws-billing@amazon.com",
                    default_currency="USD",
                    payment_terms_days=0,
                    notes="Cloud infra",
                ),
                Vendor(
                    vendor_id=uuid.uuid4(),
                    name="Jane Contractor Ltd",
                    contact_name="Jane Smith",
                    contact_email="jane@janecontractor.co.uk",
                    default_currency="GBP",
                    payment_terms_days=14,
                    notes="Frontend subcontractor",
                ),
                Vendor(
                    vendor_id=uuid.uuid4(),
                    name="Anthropic",
                    contact_email="billing@anthropic.com",
                    default_currency="USD",
                    payment_terms_days=0,
                ),
            ]
            for v in vendors:
                db.add(v)
            print(f"  vendors: +{len(vendors)}")
        else:
            print(f"  vendors: already {v_count}, skipped")

        if i_count < 2:
            rev_fixed = _get_account(db, "4000")
            rev_support = _get_account(db, "4100")
            rev_usage = _get_account(db, "4200")
            items = [
                Item(
                    item_id=uuid.uuid4(),
                    sku="FEE-PHASE-1",
                    name="Phase 1 Fixed Fee",
                    item_type="FIXED_FEE",
                    description="Milestone deliverable — scoping, architecture, kickoff.",
                    default_currency="USD",
                    default_unit_price=Decimal("25000.00"),
                    revenue_account_id=rev_fixed,
                ),
                Item(
                    item_id=uuid.uuid4(),
                    sku="FEE-PHASE-2",
                    name="Phase 2 Fixed Fee",
                    item_type="FIXED_FEE",
                    description="Milestone deliverable — build, ship, iterate.",
                    default_currency="USD",
                    default_unit_price=Decimal("55000.00"),
                    revenue_account_id=rev_fixed,
                ),
                Item(
                    item_id=uuid.uuid4(),
                    sku="SUP-MONTHLY",
                    name="Monthly Support Retainer",
                    item_type="SERVICE",
                    description="Recurring monthly support and maintenance.",
                    default_currency="USD",
                    default_unit_price=Decimal("4500.00"),
                    revenue_account_id=rev_support,
                ),
                Item(
                    item_id=uuid.uuid4(),
                    sku="USG-AI-1K",
                    name="AI Inference — per 1K tokens",
                    item_type="USAGE",
                    description="Metered AI inference cost, billed monthly in arrears.",
                    default_currency="USD",
                    default_unit_price=Decimal("0.0120"),
                    revenue_account_id=rev_usage,
                ),
                Item(
                    item_id=uuid.uuid4(),
                    sku="USG-SEAT",
                    name="Additional user seat",
                    item_type="USAGE",
                    default_currency="USD",
                    default_unit_price=Decimal("15.0000"),
                    revenue_account_id=rev_support,
                ),
            ]
            for it in items:
                db.add(it)
            print(f"  items: +{len(items)}")
        else:
            print(f"  items: already {i_count}, skipped")

        db.commit()
        print("done.")


if __name__ == "__main__":
    seed()
