"""AI Matching Engine for incoming payments.

Safe-stop policy (PRD §3, §6):
- Confidence < 95% OR any Amount/Currency mismatch => PENDING_MANUAL_REVIEW
- No ledger posting occurs until a payment reaches CLEARED
- The engine never "guesses" — it scores, then the caller enforces the gate
"""

from __future__ import annotations

from dataclasses import dataclass
from datetime import date
from decimal import Decimal
from difflib import SequenceMatcher
from enum import Enum
from typing import Iterable, Optional
from uuid import UUID

AUTO_CLEAR_THRESHOLD = Decimal("95.0")
AMOUNT_EPSILON = Decimal("0.0001")  # DECIMAL(19,4) floor
DATE_WINDOW_DAYS = 14


class PaymentStatus(str, Enum):
    CLEARED = "CLEARED"
    PENDING_MANUAL_REVIEW = "PENDING_MANUAL_REVIEW"
    FLAGGED = "FLAGGED"


class AdjustmentType(str, Enum):
    NONE = "NONE"
    PARTIAL_PAY = "PARTIAL_PAY"
    CREDIT_ON_ACCOUNT = "CREDIT_ON_ACCOUNT"
    BANK_FEE = "BANK_FEE"


@dataclass(frozen=True)
class IncomingPayment:
    amount: Decimal
    currency: str
    payer_name: str
    payer_reference: Optional[str]
    payment_date: date
    intake_source: str


@dataclass(frozen=True)
class InvoiceCandidate:
    invoice_id: UUID
    customer_id: UUID
    customer_name: str
    customer_aliases: tuple[str, ...]
    currency: str
    balance_due: Decimal
    issue_date: date
    due_date: date
    status: str


def is_exact_match(payment, invoice) -> bool:
    """True iff ``payment`` and ``invoice`` agree on currency AND on amount
    (``payment.amount`` vs ``invoice.balance_due``) within ``AMOUNT_EPSILON``.

    This is THE reconciliation exact-match predicate — the single condition
    under which a payment may auto-clear against an invoice without a human
    override. It is shared by this engine's CLEARED gate and the MCP
    ``resolve_reconciliation_match`` safe-stop guard so the definition lives in
    exactly one place. Duck-typed: works for both this module's dataclasses and
    the ORM ``Payment``/``Invoice`` models (all expose the same attributes).
    """
    currency_ok = payment.currency.upper() == invoice.currency.upper()
    amount_ok = abs(Decimal(payment.amount) - Decimal(invoice.balance_due)) <= AMOUNT_EPSILON
    return currency_ok and amount_ok


@dataclass(frozen=True)
class MatchResult:
    invoice: Optional[InvoiceCandidate]
    confidence: Decimal                   # 0.0000 – 100.0000
    amount_mismatch: bool
    currency_mismatch: bool
    status: PaymentStatus
    adjustment_type: AdjustmentType
    reasons: tuple[str, ...]


def _payer_similarity(payer: str, candidate: InvoiceCandidate) -> Decimal:
    """Fuzzy match payer name against customer name + known aliases."""
    names = (candidate.customer_name, *candidate.customer_aliases)
    best = max(
        SequenceMatcher(None, payer.lower().strip(), n.lower().strip()).ratio()
        for n in names
    )
    return Decimal(str(best))


def _date_proximity(pay_date: date, invoice: InvoiceCandidate) -> Decimal:
    """1.0 at issue/due date, decaying linearly to 0 at DATE_WINDOW_DAYS."""
    anchor = min(
        abs((pay_date - invoice.issue_date).days),
        abs((pay_date - invoice.due_date).days),
    )
    if anchor >= DATE_WINDOW_DAYS:
        return Decimal("0")
    return Decimal(DATE_WINDOW_DAYS - anchor) / Decimal(DATE_WINDOW_DAYS)


def _score(payment: IncomingPayment, invoice: InvoiceCandidate) -> Decimal:
    """Weighted confidence score (0–100).

    Weights: amount 50, currency 20, payer 20, date 10. Amount is the dominant
    signal; currency is a hard precondition (handled at the gate, not scored
    away). A zero on amount or currency collapses confidence below the 95 gate.
    """
    amount_ok = abs(payment.amount - invoice.balance_due) <= AMOUNT_EPSILON
    currency_ok = payment.currency.upper() == invoice.currency.upper()

    amount_component = Decimal("50") if amount_ok else Decimal("0")
    currency_component = Decimal("20") if currency_ok else Decimal("0")
    payer_component = _payer_similarity(payment.payer_name, invoice) * Decimal("20")
    date_component = _date_proximity(payment.payment_date, invoice) * Decimal("10")

    return (amount_component + currency_component + payer_component + date_component).quantize(
        Decimal("0.0001")
    )


def match_payment(
    payment: IncomingPayment,
    candidates: Iterable[InvoiceCandidate],
) -> MatchResult:
    """Score candidates and apply the safe-stop gate.

    Returns a MatchResult whose `status` is the exact value to persist on the
    payments row. The caller is responsible for the DB write and — only when
    status == CLEARED — for posting the journal entry.
    """
    open_candidates = [c for c in candidates if c.status in ("OPEN", "SENT", "PARTIAL")]

    if not open_candidates:
        return MatchResult(
            invoice=None,
            confidence=Decimal("0"),
            amount_mismatch=True,
            currency_mismatch=False,
            status=PaymentStatus.PENDING_MANUAL_REVIEW,
            adjustment_type=AdjustmentType.CREDIT_ON_ACCOUNT,
            reasons=("no open invoice matches payer/currency",),
        )

    scored = sorted(
        ((_score(payment, c), c) for c in open_candidates),
        key=lambda x: x[0],
        reverse=True,
    )
    confidence, best = scored[0]

    amount_mismatch = abs(payment.amount - best.balance_due) > AMOUNT_EPSILON
    currency_mismatch = payment.currency.upper() != best.currency.upper()
    exact_match = is_exact_match(payment, best)

    reasons: list[str] = []
    if currency_mismatch:
        reasons.append(
            f"currency mismatch: payment {payment.currency} vs invoice {best.currency}"
        )
    if amount_mismatch:
        delta = payment.amount - best.balance_due
        reasons.append(f"amount mismatch: delta={delta}")
    if confidence < AUTO_CLEAR_THRESHOLD:
        reasons.append(f"confidence {confidence} below threshold {AUTO_CLEAR_THRESHOLD}")

    # Safe-stop: not an exact match OR sub-threshold => manual review, no ledger
    # update. (``not exact_match`` == ``amount_mismatch or currency_mismatch``.)
    if not exact_match or confidence < AUTO_CLEAR_THRESHOLD:
        if currency_mismatch:
            adjustment = AdjustmentType.NONE  # hard reject per PRD §2
        elif payment.amount < best.balance_due:
            adjustment = AdjustmentType.PARTIAL_PAY
        elif payment.amount > best.balance_due:
            adjustment = AdjustmentType.CREDIT_ON_ACCOUNT
        else:
            adjustment = AdjustmentType.NONE

        return MatchResult(
            invoice=best,
            confidence=confidence,
            amount_mismatch=amount_mismatch,
            currency_mismatch=currency_mismatch,
            status=PaymentStatus.PENDING_MANUAL_REVIEW,
            adjustment_type=adjustment,
            reasons=tuple(reasons),
        )

    return MatchResult(
        invoice=best,
        confidence=confidence,
        amount_mismatch=False,
        currency_mismatch=False,
        status=PaymentStatus.CLEARED,
        adjustment_type=AdjustmentType.NONE,
        reasons=("exact amount+currency, high payer+date confidence",),
    )
