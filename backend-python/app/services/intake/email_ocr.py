"""Parse inbound-SMTP webhook bodies (SendGrid/Postmark) into IntakePayload.

Phase 1 extracts structured fields either from the webhook's parsed form
payload (preferred — the inbound service does MIME parsing for us) or from
OCR'd attachment text. OCR itself is pluggable via `ocr_fn`; the default
stub here raises NotImplementedError to force a conscious integration choice.
"""

from __future__ import annotations

import re
from datetime import date
from decimal import Decimal
from typing import Any, Callable

from app.services.reconciliation import IntakePayload

AMOUNT_RE = re.compile(r"(?i)(?:amount|total|paid)\s*[:=]?\s*([A-Z]{3})?\s*([\d,]+\.\d{2})")
REF_RE = re.compile(r"(?i)(?:ref|reference|txn|id)\s*[:#]?\s*([A-Za-z0-9\-]{4,})")


def _ocr_not_configured(_: bytes) -> str:
    raise NotImplementedError("Attach an OCR backend (tesseract/textract/etc) before calling")


def parse_inbound_email(
    body: dict[str, Any],
    *,
    ocr_fn: Callable[[bytes], str] = _ocr_not_configured,
) -> IntakePayload:
    """Accepts the parsed JSON body from SendGrid/Postmark inbound webhooks.

    Expected fields (common to both):
      - `from` (envelope sender) — becomes payer_name fallback
      - `subject`, `text`, `html` — searched for amount/currency/ref
      - `attachments` — list of {filename, content_type, content (base64)}; PDFs/JPGs
        are OCR'd and the extracted text is appended to the search corpus.
    """
    corpus = " ".join(
        filter(None, [body.get("subject"), body.get("text"), body.get("html")])
    )
    for att in body.get("attachments", []) or []:
        content = att.get("content")
        if content is None:
            continue
        raw = content.encode() if isinstance(content, str) else content
        try:
            corpus += "\n" + ocr_fn(raw)
        except NotImplementedError:
            raise

    amount, currency = _extract_amount(corpus)
    ref_match = REF_RE.search(corpus)

    return IntakePayload(
        amount=amount,
        currency=currency,
        payer_name=str(body.get("from") or body.get("payer_name") or "UNKNOWN"),
        payer_reference=ref_match.group(1) if ref_match else None,
        payment_date=date.fromisoformat(body["payment_date"])
        if body.get("payment_date")
        else date.today(),
        intake_source="EMAIL_PARSER",
        external_ref=str(body.get("message_id") or (ref_match.group(1) if ref_match else "")),
    )


def _extract_amount(corpus: str) -> tuple[Decimal, str]:
    match = AMOUNT_RE.search(corpus)
    if match is None:
        raise ValueError("could not locate amount in email body or attachments")
    currency = (match.group(1) or "IDR").upper()
    amount = Decimal(match.group(2).replace(",", ""))
    return amount, currency
