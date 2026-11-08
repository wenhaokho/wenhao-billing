"""Parse gateway webhook payloads into a canonical IntakePayload.

Phase 1 supports a single generic schema. Gateway-specific adapters are added
here as additional `parse_*` functions without changing the orchestrator.
"""

from __future__ import annotations

from datetime import date
from decimal import Decimal
from typing import Any

from app.services.reconciliation import IntakePayload


def parse_generic_webhook(body: dict[str, Any]) -> IntakePayload:
    required = ("amount", "currency", "payer_name", "payment_date", "external_ref")
    missing = [k for k in required if k not in body]
    if missing:
        raise ValueError(f"webhook payload missing fields: {missing}")

    return IntakePayload(
        amount=Decimal(str(body["amount"])),
        currency=str(body["currency"]).upper(),
        payer_name=str(body["payer_name"]),
        payer_reference=body.get("payer_reference"),
        payment_date=date.fromisoformat(body["payment_date"]),
        intake_source="WEBHOOK",
        external_ref=str(body["external_ref"]),
    )
