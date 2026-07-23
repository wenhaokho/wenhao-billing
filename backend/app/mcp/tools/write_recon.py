"""Reconciliation write tool — a *stricter* wrapper over
app.services.reconciliation.approve_manual_match.

`approve_manual_match` is a deliberate ADMIN OVERRIDE: a human using the
ManualReviewView UI can force a PENDING_MANUAL_REVIEW payment onto a
chosen invoice even across a currency or amount mismatch (booking an FX
gain/loss plug — see tests/integration/test_reconciliation_pipeline.py
test_cross_currency_manual_approval_books_fx_*). That override is exactly
the judgment call the PRD's safe-stop policy (matching_engine.py: "any
Amount/Currency mismatch => PENDING_MANUAL_REVIEW") reserves for a human,
so this autonomous tool must NOT make it. Do not "fix" this by loosening
the guard below — that would let an agent silently approve a currency or
amount mismatch, which is the exact failure mode safe-stop exists to
prevent.

This tool adds its own pre-flight guard — reusing matching_engine's own
AMOUNT_EPSILON tolerance rather than inventing a new one — and returns
`{"error": ...}` *without calling the service at all* on any mismatch,
so the payment's status is left completely untouched. Only when payment
and invoice already agree on currency and amount (within AMOUNT_EPSILON)
does this delegate to `reconciliation.approve_manual_match`, which
performs the actual write (ledger post + payment/invoice update).
"""
from __future__ import annotations

from decimal import Decimal
from uuid import UUID

from fastmcp.server.dependencies import get_access_token

from app.mcp.db import tool_session
from app.mcp.serialize import to_dict
from app.mcp.server import mcp
from app.mcp.tools.read_recon import _PAYMENT_FIELDS
from app.models.invoice import Invoice
from app.models.payment import Payment
from app.services import reconciliation
from app.services.matching_engine import AMOUNT_EPSILON


def _actor_user_id() -> UUID | None:
    """Best-effort actor for the ReconciliationLog row — None outside a
    real authenticated MCP request context (e.g. when a test calls the
    tool function directly); the column is nullable."""
    token = get_access_token()
    return UUID(token.subject) if token is not None else None


@mcp.tool
def resolve_reconciliation_match(payment_id: str, invoice_id: str) -> dict:
    """Force-resolve a PENDING_MANUAL_REVIEW payment against a chosen
    invoice. Safe-stop: refuses — returns {"error": ...}, no side effect,
    payment status unchanged — on any currency or amount mismatch between
    the payment and the invoice; a human must resolve those via the
    ManualReviewView UI. Autonomous only for an exact currency+amount
    match (the payer/date ambiguity that put it in the queue)."""
    try:
        with tool_session() as db:
            payment = db.get(Payment, UUID(payment_id))
            if payment is None:
                return {"error": f"payment {payment_id} not found"}
            invoice = db.get(Invoice, UUID(invoice_id))
            if invoice is None:
                return {"error": f"invoice {invoice_id} not found"}

            if payment.currency.upper() != invoice.currency.upper():
                return {
                    "error": (
                        f"currency mismatch: payment {payment.currency} vs "
                        f"invoice {invoice.currency} — refusing autonomous "
                        "resolution, route to manual review"
                    )
                }
            if abs(Decimal(payment.amount) - Decimal(invoice.balance_due)) > AMOUNT_EPSILON:
                return {
                    "error": (
                        f"amount mismatch: payment {payment.amount} vs invoice "
                        f"balance_due {invoice.balance_due} — refusing "
                        "autonomous resolution, route to manual review"
                    )
                }

            resolved = reconciliation.approve_manual_match(
                db,
                UUID(payment_id),
                UUID(invoice_id),
                actor_user_id=_actor_user_id(),
            )
            db.flush()
            return to_dict(resolved, _PAYMENT_FIELDS)
    except ValueError as e:
        return {"error": str(e)}
