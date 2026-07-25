"""Tool-argument validation shared by the MCP `update_*` write tools.

Sits alongside serialize.py/db.py as the third piece of write-tool plumbing:
db.py opens the session, this module vets the `changes` dict before that
session is opened, serialize.py renders the result.
"""
from __future__ import annotations

from pydantic import BaseModel


def reject_unknown_changes(changes: dict, model: type[BaseModel]) -> dict | None:
    """Return an ``{"error": ...}`` dict if `changes` carries keys that `model`
    doesn't define, else ``None``.

    Every `update_*` tool takes a free-form ``changes: dict`` and feeds it to a
    Pydantic ``*Update`` model. Those models are plain ``BaseModel``s, so their
    default ``extra="ignore"`` silently drops a misspelled or wrong-named key —
    ``{"unit_price": ...}`` when the field is ``default_unit_price``, or
    ``{"address": ...}`` on an entity that has no address column — and the tool
    still returns a success dict. The caller sees a saved record and no
    indication that its edit did nothing.

    Setting ``extra="forbid"`` on the schemas would catch this too, but those
    same models back the HTTP routers, which would then start rejecting request
    bodies they accept today. Hence the check lives at the tool boundary.

    Callers must return this dict as-is *before* opening a ``tool_session()``,
    so a bad key never reaches the database and never masks a different error
    (e.g. not-found) that would otherwise be reported first.
    """
    unknown = sorted(set(changes) - set(model.model_fields))
    if not unknown:
        return None
    return {
        "error": (
            f"unknown field(s): {', '.join(unknown)}. "
            f"Valid fields: {', '.join(sorted(model.model_fields))}"
        )
    }
