"""Real-Postgres test harness. No DB mocks for pipeline tests (user preference)."""

from __future__ import annotations

import os
from datetime import date
from decimal import Decimal
from pathlib import Path

import pytest
from alembic import command
from alembic.config import Config
from sqlalchemy import create_engine
from sqlalchemy.orm import Session, sessionmaker

from app.config import get_settings
from app.models.fx import FxRate

BACKEND_DIR = Path(__file__).resolve().parents[1]


def _alembic_cfg(db_url: str) -> Config:
    cfg = Config(str(BACKEND_DIR / "alembic.ini"))
    cfg.set_main_option("script_location", str(BACKEND_DIR / "alembic"))
    cfg.set_main_option("sqlalchemy.url", db_url)
    return cfg


@pytest.fixture(scope="session")
def db_url() -> str:
    # Expect `pytest-postgresql` fixture or TEST_DATABASE_URL env var.
    url = os.environ.get("TEST_DATABASE_URL")
    if url is None:
        pytest.skip("Set TEST_DATABASE_URL to run integration tests against a real Postgres")
    return url


@pytest.fixture(scope="session")
def migrated_engine(db_url: str):
    # alembic/env.py reads DATABASE_URL via app settings and overrides the
    # Config url, so point the whole process at the test DB for migration.
    prior = os.environ.get("DATABASE_URL")
    os.environ["DATABASE_URL"] = db_url
    get_settings.cache_clear()
    try:
        engine = create_engine(db_url, future=True)
        command.upgrade(_alembic_cfg(db_url), "head")
        yield engine
        engine.dispose()
    finally:
        if prior is None:
            os.environ.pop("DATABASE_URL", None)
        else:
            os.environ["DATABASE_URL"] = prior


@pytest.fixture()
def db(migrated_engine) -> Session:
    """Transactional rollback per test — leaves no residue between cases."""
    connection = migrated_engine.connect()
    trans = connection.begin()
    TestSession = sessionmaker(bind=connection, autoflush=False, expire_on_commit=False)
    session = TestSession()
    _seed_test_fx_rates(session)
    try:
        yield session
    finally:
        session.close()
        trans.rollback()
        connection.close()


def _seed_test_fx_rates(session: Session) -> None:
    """Make common non-base currencies convertible to IDR so ledger tests
    don't need to construct rates explicitly. Rollback-safe (per-test txn)."""
    today = date(2026, 1, 1)
    pairs = [("USD", Decimal("16000")), ("SGD", Decimal("12000")), ("EUR", Decimal("17000"))]
    for from_ccy, rate in pairs:
        session.add(
            FxRate(
                from_currency=from_ccy,
                to_currency="IDR",
                rate=rate,
                as_of_date=today,
                source="TEST",
            )
        )
    session.flush()
