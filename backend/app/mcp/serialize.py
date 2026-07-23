"""Serialize SQLAlchemy rows to JSON-safe dicts for MCP tool returns."""
from __future__ import annotations

from datetime import date, datetime
from decimal import Decimal
from typing import Any
from uuid import UUID


def coerce_value(v: Any) -> Any:
    """Coerce a single value to a JSON-safe type. Public — other tool
    modules that build dicts by hand (not via `to_dict`) should call this
    instead of reaching into a private helper."""
    if isinstance(v, Decimal):
        return str(v)          # preserve exact money
    if isinstance(v, (datetime, date)):
        return v.isoformat()
    if isinstance(v, UUID):
        return str(v)
    return v


def to_dict(obj: Any, fields: list[str]) -> dict:
    return {f: coerce_value(getattr(obj, f)) for f in fields}
