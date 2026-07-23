"""Serialize SQLAlchemy rows to JSON-safe dicts for MCP tool returns."""
from __future__ import annotations

from datetime import date, datetime
from decimal import Decimal
from typing import Any
from uuid import UUID


def _coerce(v: Any) -> Any:
    if isinstance(v, Decimal):
        return str(v)          # preserve exact money
    if isinstance(v, (datetime, date)):
        return v.isoformat()
    if isinstance(v, UUID):
        return str(v)
    return v


def to_dict(obj: Any, fields: list[str]) -> dict:
    return {f: _coerce(getattr(obj, f)) for f in fields}
