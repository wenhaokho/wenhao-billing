"""Reconciliation orchestrator — the ONLY caller of matching_engine.match_payment.

Safe-stop gate lives here. Every incoming payment flows through one transaction:
  load candidates -> score -> write payments row -> (CLEARED only) post ledger -> write audit log.
Anything off the happy path lands in PENDING_MANUAL_REVIEW with no ledger impact.
"""

from __future__ import annotations

from dataclasses import dataclass
from datetime import date
from decimal import Decimal
from typing import Iterable
from uuid import UUID

from sqlalchemy import select
from sqlalchemy.orm import Session

from app.models.customer import Customer
from app.models.invoice import Invoice
from app.models.journal import JournalEntry
from app.models.payment import Payment
from app.models.recon_log import ReconciliationLog
from app.services import ledger
from app.services.matching_engine import (
    AdjustmentType,
    IncomingPayment,
    InvoiceCandidate,
    MatchResult,
    PaymentStatus,
    match_payment,
)


@dataclass(frozen=True)
class IntakePayload:
    amount: Decimal
    currency: str
    payer_name: str
    payer_reference: str | None
    payment_date: date
    intake_source: str
    external_ref: str | None


def _load_candidates(db: Session, currency: str) -> list[InvoiceCandidate]:
    rows = db.execute(
        select(Invoice, Customer)
        .join(Customer, Customer.customer_id == Invoice.customer_id)
        .where(Invoice.status.in_(("SENT", "PARTIAL")))
        .where(Invoice.currency == currency)
    ).all()
    return [
        InvoiceCandidate(
            invoice_id=invoice.invoice_id,
            customer_id=customer.customer_id,
            customer_name=customer.name,
            customer_aliases=tuple(customer.matching_aliases or ()),
            currency=invoice.currency,
            balance_due=invoice.balance_due,
            issue_date=invoice.issue_date,
            due_date=invoice.due_date,
            status=invoice.status,
        )
        for invoice, customer in rows
    ]


def _to_incoming(p: IntakePayload) -> IncomingPayment:
    return IncomingPayment(
        amount=p.amount,
        currency=p.currency,
        payer_name=p.payer_name,
        payer_reference=p.payer_reference,
        payment_date=p.payment_date,
        intake_source=p.intake_source,
    )


def _log_action(
    db: Session, payment_id: UUID, result: MatchResult, actor_user_id: UUID | None
) -> None:
    action = "CLEARED" if result.status is PaymentStatus.CLEARED else "HELD"
    db.add(
        ReconciliationLog(
            payment_id=payment_id,
            action=action,
            reasons=list(result.reasons),
            actor_user_id=actor_user_id,
        )
    )


def process_incoming_payment(
    db: Session, payload: IntakePayload, actor_user_id: UUID | None = None
) -> Payment:
    """Single-transaction reconciliation. Caller commits."""
    incoming = _to_incoming(payload)
    candidates = _load_candidates(db, payload.currency)
    result = match_payment(incoming, candidates)

    matched_invoice_id = result.invoice.invoice_id if result.invoice else None
    matched_customer_id = result.invoice.customer_id if result.invoice else None

    payment = Payment(
        invoice_id=matched_invoice_id,
        customer_id=matched_customer_id,
        amount=payload.amount,
        currency=payload.currency,
        payer_name=payload.payer_name,
        payer_reference=payload.payer_reference,
        payment_date=payload.payment_date,
        intake_source=payload.intake_source,
        external_ref=payload.external_ref,
        status=result.status.value,
        adjustment_type=result.adjustment_type.value,
        confidence_score=result.confidence,
    )
    db.add(payment)
    db.flush()

    if result.status is PaymentStatus.CLEARED:
        invoice = db.get(Invoice, matched_invoice_id)
        if invoice is None:
            raise RuntimeError("CLEARED result without invoice — engine invariant violated")
        ledger.post_payment_cleared(db, payment, invoice)
        invoice.balance_due = Decimal("0")
        if invoice.status != "PAID":
            invoice.status = "PAID"

    _log_action(db, payment.payment_id, result, actor_user_id)
    return payment


def approve_manual_match(
    db: Session,
    payment_id: UUID,
    invoice_id: UUID,
    actor_user_id: UUID,
    adjustment_type: AdjustmentType = AdjustmentType.NONE,
) -> Payment:
    """Admin override: force a PENDING payment to CLEARED against a chosen invoice.

    Cross-currency manual approvals are allowed — the ledger books an FX
    gain/loss plug and the invoice is treated as fully settled (operator has
    explicitly chosen this match). Same-currency keeps the partial-pay semantics.
    """
    payment = db.get(Payment, payment_id)
    if payment is None:
        raise ValueError(f"payment {payment_id} not found")
    if payment.status == "CLEARED":
        raise ValueError(f"payment {payment_id} already CLEARED")

    invoice = db.get(Invoice, invoice_id)
    if invoice is None:
        raise ValueError(f"invoice {invoice_id} not found")

    payment.invoice_id = invoice.invoice_id
    payment.customer_id = invoice.customer_id
    payment.status = PaymentStatus.CLEARED.value
    payment.adjustment_type = adjustment_type.value
    db.flush()

    ledger.post_payment_cleared(db, payment, invoice)
    if invoice.currency == payment.currency:
        invoice.balance_due = max(Decimal("0"), invoice.balance_due - payment.amount)
    else:
        # Cross-currency manual approval settles the full balance; the
        # base-currency difference is already in the FX gain/loss plug.
        invoice.balance_due = Decimal("0")
    if invoice.balance_due == Decimal("0"):
        invoice.status = "PAID"

    db.add(
        ReconciliationLog(
            payment_id=payment.payment_id,
            action="OVERRIDDEN",
            reasons=[f"manual approval against invoice {invoice.invoice_id}"],
            actor_user_id=actor_user_id,
        )
    )
    return payment


def reverse_payment(db: Session, payment_id: UUID, actor_user_id: UUID, reason: str) -> Payment:
    """Undo a CLEARED payment: post reversing journal entry, flag payment, log action."""
    payment = db.get(Payment, payment_id)
    if payment is None:
        raise ValueError(f"payment {payment_id} not found")
    if payment.status != "CLEARED":
        raise ValueError(f"only CLEARED payments can be reversed (got {payment.status})")

    original_entry = db.scalar(
        select(JournalEntry)
        .where(JournalEntry.source_id == payment.payment_id)
        .where(JournalEntry.source_type == "PAYMENT")
        .where(JournalEntry.reversed_by_entry_id.is_(None))
    )
    if original_entry is None:
        raise ValueError(f"no active journal entry to reverse for payment {payment_id}")

    ledger.post_reversal(db, original_entry.entry_id, memo=f"Reversal: {reason}")
    payment.status = "FLAGGED"

    if payment.invoice_id is not None:
        invoice = db.get(Invoice, payment.invoice_id)
        if invoice is not None:
            invoice.balance_due = invoice.balance_due + payment.amount
            invoice.status = "SENT"

    db.add(
        ReconciliationLog(
            payment_id=payment.payment_id,
            action="REVERSED",
            reasons=[reason],
            actor_user_id=actor_user_id,
        )
    )
    return payment
