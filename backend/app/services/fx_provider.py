"""Fetch reference FX rates from a free provider (Frankfurter / ECB).

Frankfurter (https://frankfurter.dev) exposes ECB reference rates with no API
key and no rate limit, updated once per working day. We request a direct quote
per source currency, e.g. ``/v1/latest?base=USD&symbols=IDR`` -> ``USD->IDR``,
so the stored rate matches what a manual entry would hold. These are
mid-market rates; keep the ``source`` field accurate so a human can tell an
auto-fetched rate from a ``MANUAL`` one during review.
"""

from __future__ import annotations

from dataclasses import dataclass
from datetime import date
from decimal import Decimal

import httpx

from app.config import get_settings

SOURCE = "FRANKFURTER"


class FxProviderError(RuntimeError):
    pass


@dataclass(frozen=True)
class FetchedRate:
    from_currency: str
    to_currency: str
    rate: Decimal
    as_of_date: date


def fetch_latest_rates(
    source_currencies: list[str],
    target_currency: str,
) -> list[FetchedRate]:
    """Return the latest published rate for each ``source -> target`` pair.

    A source equal to the target is skipped. A source the provider has no rate
    for is skipped silently (missing from the returned list) rather than
    failing the whole batch. Network/HTTP failures raise ``FxProviderError``.
    """
    settings = get_settings()
    target = target_currency.upper()
    base_url = settings.fx_provider_base_url.rstrip("/")

    results: list[FetchedRate] = []
    with httpx.Client(timeout=settings.fx_provider_timeout_seconds) as client:
        for raw in source_currencies:
            src = raw.upper()
            if src == target:
                continue
            try:
                response = client.get(
                    f"{base_url}/latest",
                    params={"base": src, "symbols": target},
                )
                response.raise_for_status()
                body = response.json()
            except httpx.HTTPError as exc:
                raise FxProviderError(
                    f"fx provider request failed for {src}->{target}: {exc}"
                ) from exc
            except ValueError as exc:  # non-JSON body
                raise FxProviderError(
                    f"fx provider returned invalid JSON for {src}->{target}: {exc}"
                ) from exc

            rates = body.get("rates") or {}
            value = rates.get(target)
            if value is None:
                # Provider doesn't cover this pair; skip rather than fail.
                continue

            as_of_raw = body.get("date")
            try:
                as_of = date.fromisoformat(as_of_raw)
            except (TypeError, ValueError) as exc:
                raise FxProviderError(
                    f"fx provider returned invalid date for {src}->{target}: "
                    f"{as_of_raw!r}"
                ) from exc

            results.append(
                FetchedRate(
                    from_currency=src,
                    to_currency=target,
                    rate=Decimal(str(value)),
                    as_of_date=as_of,
                )
            )
    return results
