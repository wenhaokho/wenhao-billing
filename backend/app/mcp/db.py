"""Per-tool DB session with an OVERRIDABLE factory.

Production: sessions come from SessionLocal (app.db.session). Tests inject a
factory bound to the rollback connection via set_tool_session_factory, so tool
commits are contained in a SAVEPOINT and rolled back per-test.

Commit on success, rollback on error, always close.
"""
from __future__ import annotations

from collections.abc import Callable, Iterator
from contextlib import contextmanager

from sqlalchemy.orm import Session

from app.db.session import SessionLocal

_session_factory: Callable[[], Session] = SessionLocal


def set_tool_session_factory(factory: Callable[[], Session]) -> None:
    """Override the session factory (tests bind it to the test connection)."""
    global _session_factory
    _session_factory = factory


def reset_tool_session_factory() -> None:
    global _session_factory
    _session_factory = SessionLocal


@contextmanager
def tool_session() -> Iterator[Session]:
    db = _session_factory()
    try:
        yield db
        db.commit()
    except Exception:
        db.rollback()
        raise
    finally:
        db.close()
