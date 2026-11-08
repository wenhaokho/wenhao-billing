"""Seed sample hosting items + recurring templates + a few generated invoices
in different states (current / paid / overdue → suspended).

Idempotent: skips entirely if any hosting items already exist.

Usage:
    docker compose exec backend python -m scripts.seed_hosting
"""

from __future__ import annotations

from datetime import date, datetime, timedelta
from decimal import Decimal

from sqlalchemy import func, select

from app.db.session import SessionLocal
from app.models.cloudflare_target import CloudflareTarget
from app.models.coa import ChartOfAccount
from app.models.customer import Customer
from app.models.invoice import Invoice
from app.models.item import Item
from app.schemas.invoice import InvoiceLineItemIn, RecurringSchedule, RecurringTemplateCreate
from app.services import invoicing


HOSTING_SAMPLES = [
    {
        "customer_name": "Acme Corp",
        "item_name": "Acme web hosting",
        "domain": "acme.example.com",
        "price": Decimal("240.0000"),
        "currency": "USD",
        "bundle_months": 3,
        "grace_days": 3,
        "zone_id": "zone_acme_demo",
        "record_id": "rec_acme_demo",
        "live_content": "prod.hosting.example.net",
        "maintenance_content": "maintenance.example.net",
        # Recently issued, not overdue.
        "scenario": "current",
        "anchor_offset_days": -30,
    },
    {
        "customer_name": "Globex Industries",
        "item_name": "Globex API hosting",
        "domain": "api.globex.example.com",
        "price": Decimal("480.0000"),
        "currency": "USD",
        "bundle_months": 6,
        "grace_days": 5,
        "zone_id": "zone_globex_demo",
        "record_id": "rec_globex_demo",
        "live_content": "api-prod.hosting.example.net",
        "maintenance_content": "maintenance.example.net",
        # Last invoice paid, in good standing.
        "scenario": "paid",
        "anchor_offset_days": -90,
    },
    {
        "customer_name": "Stark Holdings",
        "item_name": "Stark portal hosting",
        "domain": "portal.stark.example.com",
        "price": Decimal("3600000.0000"),
        "currency": "IDR",
        "bundle_months": 3,
        "grace_days": 3,
        "zone_id": "zone_stark_demo",
        "record_id": "rec_stark_demo",
        "live_content": "portal.hosting.example.net",
        "maintenance_content": "maintenance.example.net",
        # Overdue past grace — pre-marked as SUSPENDED to show the state in UI.
        "scenario": "suspended",
        "anchor_offset_days": -60,
    },
]


def _today() -> date:
    return date.today()


def _income_account(db) -> int | None:
    # 4100 is "Support/Maintenance Revenue" in the master COA — closest fit
    # for hosting. Fall back to any INCOME account if it's missing.
    acct = db.scalar(select(ChartOfAccount).where(ChartOfAccount.code == "4100"))
    if acct is None:
        acct = db.scalar(select(ChartOfAccount).where(ChartOfAccount.type == "INCOME"))
    return acct.account_id if acct is not None else None


def _customer_by_name(db, name: str) -> Customer | None:
    return db.scalar(select(Customer).where(Customer.name == name))


def _payment_terms(grace_days: int) -> str:
    return "Net 7"


def seed() -> None:
    with SessionLocal() as db:
        existing = db.scalar(
            select(func.count(Item.item_id)).where(Item.is_hosting.is_(True))
        ) or 0
        if existing > 0:
            print(f"skipping: {existing} hosting items already exist")
            return

        revenue_acct = _income_account(db)
        today = _today()

        for sample in HOSTING_SAMPLES:
            customer = _customer_by_name(db, sample["customer_name"])
            if customer is None:
                print(f"  - missing customer '{sample['customer_name']}' — skipping")
                continue

            item = Item(
                name=sample["item_name"],
                item_type="SERVICE",
                description=f"Managed hosting for {sample['domain']}",
                default_currency=sample["currency"],
                default_unit_price=sample["price"],
                revenue_account_id=revenue_acct,
                is_sold=True,
                is_purchased=False,
                active=True,
                is_hosting=True,
                hosting_domain=sample["domain"],
                hosting_grace_days=sample["grace_days"],
                hosting_suspension_enabled=True,
                hosting_status="ACTIVE",
            )
            item.cloudflare_target = CloudflareTarget(
                zone_id=sample["zone_id"],
                record_id=sample["record_id"],
                record_name=sample["domain"],
                record_type="CNAME",
                live_content=sample["live_content"],
                maintenance_content=sample["maintenance_content"],
                proxied=True,
            )
            db.add(item)
            db.flush()

            anchor = today + timedelta(days=sample["anchor_offset_days"])
            template_payload = RecurringTemplateCreate(
                customer_id=customer.customer_id,
                currency=sample["currency"],
                payment_terms=_payment_terms(sample["grace_days"]),
                notes=f"Prepaid hosting bundle for {sample['domain']}",
                line_items=[
                    InvoiceLineItemIn(
                        item_id=item.item_id,
                        description=f"{sample['item_name']} - {sample['domain']}",
                        quantity=Decimal("1.0000"),
                        unit_price=sample["price"],
                        position=0,
                    )
                ],
                schedule=RecurringSchedule(
                    frequency="MONTHLY",
                    interval=sample["bundle_months"],
                    start_date=anchor,
                    end_mode="NEVER",
                ),
            )
            template = invoicing.create_recurring_template(db, template_payload)
            db.flush()

            invoice = invoicing.trigger_recurring_cycle(
                db,
                template_invoice_id=template.invoice_id,
                cycle_key=anchor.isoformat(),
            )
            db.flush()

            scenario = sample["scenario"]
            if scenario == "paid":
                invoice.status = "PAID"
                invoice.balance_due = Decimal("0")
                invoice.issue_date = anchor
                invoice.due_date = anchor + timedelta(days=7)
                item.hosting_last_paid_at = datetime.combine(today, datetime.min.time())
            elif scenario == "current":
                invoice.status = "SENT"
                invoice.issue_date = anchor
                invoice.due_date = today + timedelta(days=14)
            elif scenario == "suspended":
                invoice.status = "SENT"
                invoice.issue_date = anchor
                invoice.due_date = today - timedelta(days=10)
                item.hosting_status = "SUSPENDED"
                item.cloudflare_target.provider_status = "SUSPENDED"

            print(
                f"  + {sample['item_name']} ({sample['domain']})"
                f" — {scenario} — invoice {invoice.invoice_number}"
            )

        db.commit()
        print("hosting sample data seeded")


if __name__ == "__main__":
    seed()
