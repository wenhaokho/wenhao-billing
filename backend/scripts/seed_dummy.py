"""Seed realistic dummy data so the dashboard has something to show.

Idempotent-ish: skips seeding if at least 3 customers already exist.

Usage:
    docker compose exec backend python -m scripts.seed_dummy
"""

from __future__ import annotations

import uuid
from datetime import date, datetime, timedelta
from decimal import Decimal

from sqlalchemy import func, select

from app.db.session import SessionLocal
from app.models.coa import ChartOfAccount
from app.models.customer import Customer
from app.models.invoice import Invoice
from app.models.journal import JournalEntry, JournalLine
from app.models.payment import Payment
from app.models.recon_log import ReconciliationLog


def _today() -> date:
    return date.today()


def seed() -> None:
    with SessionLocal() as db:
        existing = db.scalar(select(func.count(Customer.customer_id))) or 0
        if existing >= 3:
            print(f"already have {existing} customers — skipping dummy seed")
            return

        # ---------- customers ----------
        customers = [
            Customer(
                customer_id=uuid.uuid4(),
                name="Acme Corporation",
                matching_aliases=["Acme Corp", "ACME", "Acme Inc"],
            ),
            Customer(
                customer_id=uuid.uuid4(),
                name="Globex Industries",
                matching_aliases=["Globex", "Globex Ind"],
            ),
            Customer(
                customer_id=uuid.uuid4(),
                name="Initech Solutions",
                matching_aliases=["Initech"],
            ),
            Customer(
                customer_id=uuid.uuid4(),
                name="Umbrella Ventures",
                matching_aliases=["Umbrella", "Umbrella LLC"],
            ),
            Customer(
                customer_id=uuid.uuid4(),
                name="Stark Holdings",
                matching_aliases=["Stark", "Stark Industries"],
            ),
        ]
        for c in customers:
            db.add(c)
        db.flush()

        # ---------- invoices ----------
        # mix of DRAFT, SENT, PARTIAL, PAID across USD/EUR/SGD
        invoices = [
            # Acme — milestone, SENT, large USD
            Invoice(
                invoice_id=uuid.uuid4(),
                customer_id=customers[0].customer_id,
                invoice_type="MILESTONE",
                currency="USD",
                amount=Decimal("24000.00"),
                balance_due=Decimal("24000.00"),
                status="SENT",
                issue_date=_today() - timedelta(days=10),
                due_date=_today() + timedelta(days=4),
            ),
            # Acme — recurring, PARTIAL
            Invoice(
                invoice_id=uuid.uuid4(),
                customer_id=customers[0].customer_id,
                invoice_type="RECURRING",
                currency="USD",
                amount=Decimal("4500.00"),
                balance_due=Decimal("1500.00"),
                status="PARTIAL",
                issue_date=_today() - timedelta(days=22),
                due_date=_today() - timedelta(days=8),
            ),
            # Globex — usage, DRAFT awaiting finalization
            Invoice(
                invoice_id=uuid.uuid4(),
                customer_id=customers[1].customer_id,
                invoice_type="USAGE",
                currency="USD",
                amount=Decimal("8742.33"),
                balance_due=Decimal("8742.33"),
                status="DRAFT",
                issue_date=_today() - timedelta(days=1),
                due_date=_today() + timedelta(days=14),
            ),
            # Globex — milestone, PAID
            Invoice(
                invoice_id=uuid.uuid4(),
                customer_id=customers[1].customer_id,
                invoice_type="MILESTONE",
                currency="USD",
                amount=Decimal("18000.00"),
                balance_due=Decimal("0.00"),
                status="PAID",
                issue_date=_today() - timedelta(days=45),
                due_date=_today() - timedelta(days=31),
            ),
            # Initech — EUR milestone SENT
            Invoice(
                invoice_id=uuid.uuid4(),
                customer_id=customers[2].customer_id,
                invoice_type="MILESTONE",
                currency="EUR",
                amount=Decimal("12500.00"),
                balance_due=Decimal("12500.00"),
                status="SENT",
                issue_date=_today() - timedelta(days=5),
                due_date=_today() + timedelta(days=9),
            ),
            # Initech — DRAFT recurring
            Invoice(
                invoice_id=uuid.uuid4(),
                customer_id=customers[2].customer_id,
                invoice_type="RECURRING",
                currency="EUR",
                amount=Decimal("3200.00"),
                balance_due=Decimal("3200.00"),
                status="DRAFT",
                issue_date=_today(),
                due_date=_today() + timedelta(days=14),
            ),
            # Umbrella — SGD usage SENT
            Invoice(
                invoice_id=uuid.uuid4(),
                customer_id=customers[3].customer_id,
                invoice_type="USAGE",
                currency="SGD",
                amount=Decimal("6420.50"),
                balance_due=Decimal("6420.50"),
                status="SENT",
                issue_date=_today() - timedelta(days=3),
                due_date=_today() + timedelta(days=11),
            ),
            # Stark — milestone DRAFT
            Invoice(
                invoice_id=uuid.uuid4(),
                customer_id=customers[4].customer_id,
                invoice_type="MILESTONE",
                currency="USD",
                amount=Decimal("55000.00"),
                balance_due=Decimal("55000.00"),
                status="DRAFT",
                issue_date=_today(),
                due_date=_today() + timedelta(days=30),
            ),
            # Stark — recurring SENT, overdue
            Invoice(
                invoice_id=uuid.uuid4(),
                customer_id=customers[4].customer_id,
                invoice_type="RECURRING",
                currency="USD",
                amount=Decimal("7500.00"),
                balance_due=Decimal("7500.00"),
                status="SENT",
                issue_date=_today() - timedelta(days=35),
                due_date=_today() - timedelta(days=21),
            ),
        ]
        for inv in invoices:
            db.add(inv)
        db.flush()

        # ---------- payments + recon log ----------
        # CLEARED payment for Acme PARTIAL (the 3000 that got paid)
        p1 = Payment(
            payment_id=uuid.uuid4(),
            invoice_id=invoices[1].invoice_id,
            customer_id=customers[0].customer_id,
            amount=Decimal("3000.00"),
            currency="USD",
            payer_name="Acme Corporation",
            payer_reference="INV-ACME-2026-003",
            payment_date=_today() - timedelta(days=6),
            intake_source="WEBHOOK",
            external_ref="stripe_pi_3N1abc",
            status="CLEARED",
            adjustment_type="NONE",
            confidence_score=Decimal("98.20"),
            created_at=datetime.utcnow() - timedelta(days=6),
        )

        # CLEARED payment for Globex PAID invoice
        p2 = Payment(
            payment_id=uuid.uuid4(),
            invoice_id=invoices[3].invoice_id,
            customer_id=customers[1].customer_id,
            amount=Decimal("18000.00"),
            currency="USD",
            payer_name="Globex Industries",
            payer_reference="INV-GLO-2026-001",
            payment_date=_today() - timedelta(days=30),
            intake_source="WEBHOOK",
            external_ref="stripe_pi_3N2def",
            status="CLEARED",
            adjustment_type="NONE",
            confidence_score=Decimal("100.00"),
            created_at=datetime.utcnow() - timedelta(days=30),
        )

        # PENDING_MANUAL_REVIEW — payer-name mismatch (safe-stop held it)
        p3 = Payment(
            payment_id=uuid.uuid4(),
            invoice_id=None,
            customer_id=None,
            amount=Decimal("12500.00"),
            currency="EUR",
            payer_name="Initech European Branch",
            payer_reference="wire-ref-8821",
            payment_date=_today() - timedelta(days=2),
            intake_source="EMAIL",
            external_ref="email_msg_8821",
            status="PENDING_MANUAL_REVIEW",
            adjustment_type="NONE",
            confidence_score=Decimal("72.40"),
            created_at=datetime.utcnow() - timedelta(days=2),
        )

        # PENDING_MANUAL_REVIEW — amount mismatch
        p4 = Payment(
            payment_id=uuid.uuid4(),
            invoice_id=None,
            customer_id=None,
            amount=Decimal("24123.45"),
            currency="USD",
            payer_name="Acme Corp",
            payer_reference="unexpected overpay",
            payment_date=_today() - timedelta(days=1),
            intake_source="WEBHOOK",
            external_ref="stripe_pi_3N3ghi",
            status="PENDING_MANUAL_REVIEW",
            adjustment_type="OVERPAYMENT",
            confidence_score=Decimal("88.10"),
            created_at=datetime.utcnow() - timedelta(hours=6),
        )

        # PENDING_MANUAL_REVIEW — currency mismatch
        p5 = Payment(
            payment_id=uuid.uuid4(),
            invoice_id=None,
            customer_id=None,
            amount=Decimal("6420.50"),
            currency="USD",  # Umbrella invoice was SGD
            payer_name="Umbrella Ventures",
            payer_reference="usd-wire-332",
            payment_date=_today(),
            intake_source="EMAIL",
            external_ref="email_msg_332",
            status="PENDING_MANUAL_REVIEW",
            adjustment_type="CURRENCY_MISMATCH",
            confidence_score=Decimal("65.00"),
            created_at=datetime.utcnow() - timedelta(hours=2),
        )

        for p in [p1, p2, p3, p4, p5]:
            db.add(p)
        db.flush()

        # ---------- reconciliation log ----------
        logs = [
            ReconciliationLog(
                log_id=uuid.uuid4(),
                payment_id=p1.payment_id,
                action="CLEARED",
                reasons=["exact amount match on partial balance", "payer alias matched"],
                actor_user_id=None,
                created_at=p1.created_at,
            ),
            ReconciliationLog(
                log_id=uuid.uuid4(),
                payment_id=p2.payment_id,
                action="CLEARED",
                reasons=["exact amount + currency match", "confidence 100.00"],
                actor_user_id=None,
                created_at=p2.created_at,
            ),
            ReconciliationLog(
                log_id=uuid.uuid4(),
                payment_id=p3.payment_id,
                action="HELD",
                reasons=[
                    "payer name 'Initech European Branch' fuzzy-matched at 0.72",
                    "confidence 72.40 < threshold 95",
                ],
                actor_user_id=None,
                created_at=p3.created_at,
            ),
            ReconciliationLog(
                log_id=uuid.uuid4(),
                payment_id=p4.payment_id,
                action="HELD",
                reasons=[
                    "amount 24123.45 exceeds invoice balance 24000.00",
                    "overpayment adjustment — manual review required",
                ],
                actor_user_id=None,
                created_at=p4.created_at,
            ),
            ReconciliationLog(
                log_id=uuid.uuid4(),
                payment_id=p5.payment_id,
                action="HELD",
                reasons=[
                    "currency USD does not match any open invoice in SGD",
                    "safe-stop gate: currency mismatch never auto-clears",
                ],
                actor_user_id=None,
                created_at=p5.created_at,
            ),
        ]
        for l in logs:
            db.add(l)
        db.flush()

        # ---------- ledger postings for the 2 CLEARED payments ----------
        bank_acc = db.scalar(select(ChartOfAccount).where(ChartOfAccount.code == "1000"))
        ar_acc = db.scalar(select(ChartOfAccount).where(ChartOfAccount.code == "1100"))
        if not bank_acc or not ar_acc:
            raise RuntimeError("chart of accounts seed missing (codes 1000 / 1100)")

        # Acme partial 3000 USD: DR Bank / CR AR
        e1 = JournalEntry(
            entry_id=uuid.uuid4(),
            posted_at=p1.created_at,
            source_type="PAYMENT",
            source_id=p1.payment_id,
            memo=f"Payment from {p1.payer_name}",
        )
        db.add(e1)
        db.flush()
        # Dummy FX: treats USD 1:1 as base currency for seed purposes only.
        # Real deployments must seed fx_rates (USD -> IDR) before posting.
        db.add(JournalLine(entry_id=e1.entry_id, account_id=bank_acc.account_id, debit=p1.amount, credit=Decimal("0"), currency="USD", base_amount_debit=p1.amount, base_amount_credit=Decimal("0"), fx_rate=Decimal("1")))
        db.add(JournalLine(entry_id=e1.entry_id, account_id=ar_acc.account_id, debit=Decimal("0"), credit=p1.amount, currency="USD", base_amount_debit=Decimal("0"), base_amount_credit=p1.amount, fx_rate=Decimal("1")))

        # Globex 18000 USD
        e2 = JournalEntry(
            entry_id=uuid.uuid4(),
            posted_at=p2.created_at,
            source_type="PAYMENT",
            source_id=p2.payment_id,
            memo=f"Payment from {p2.payer_name}",
        )
        db.add(e2)
        db.flush()
        db.add(JournalLine(entry_id=e2.entry_id, account_id=bank_acc.account_id, debit=p2.amount, credit=Decimal("0"), currency="USD", base_amount_debit=p2.amount, base_amount_credit=Decimal("0"), fx_rate=Decimal("1")))
        db.add(JournalLine(entry_id=e2.entry_id, account_id=ar_acc.account_id, debit=Decimal("0"), credit=p2.amount, currency="USD", base_amount_debit=Decimal("0"), base_amount_credit=p2.amount, fx_rate=Decimal("1")))

        db.commit()
        print("seeded:")
        print(f"  customers: {len(customers)}")
        print(f"  invoices:  {len(invoices)}  (draft/sent/partial/paid mix)")
        print(f"  payments:  5 (2 CLEARED, 3 PENDING_MANUAL_REVIEW)")
        print(f"  recon log: {len(logs)} entries")
        print(f"  ledger:    2 balanced journal entries")


if __name__ == "__main__":
    seed()
