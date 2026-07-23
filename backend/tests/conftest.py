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
from sqlalchemy.orm import sessionmaker as _sessionmaker

from app.config import get_settings
from app.mcp.db import reset_tool_session_factory, set_tool_session_factory
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
    """Transactional rollback per test — leaves no residue between cases.

    ``join_transaction_mode="create_savepoint"`` lets code under test call
    ``session.commit()`` (the OAuth endpoints do) without ending the outer
    transaction: each commit releases a SAVEPOINT, and the outer
    ``trans.rollback()`` still discards everything at teardown.
    """
    connection = migrated_engine.connect()
    trans = connection.begin()
    TestSession = sessionmaker(
        bind=connection,
        autoflush=False,
        expire_on_commit=False,
        join_transaction_mode="create_savepoint",
    )
    session = TestSession()
    _seed_test_fx_rates(session)
    try:
        yield session
    finally:
        session.close()
        trans.rollback()
        connection.close()


@pytest.fixture()
def client(db):
    """FastAPI TestClient whose ``get_db`` is pinned to the per-test session,
    so seeded rows are visible to the app and everything rolls back."""
    from fastapi.testclient import TestClient

    from app.db.session import get_db
    from app.main import create_app

    app = create_app()
    app.dependency_overrides[get_db] = lambda: db
    with TestClient(app) as c:
        yield c
    app.dependency_overrides.clear()


@pytest.fixture()
def admin_session(client, db):
    """Seed an admin user and log in through the real ``/api/v1/auth/login``
    endpoint so the session cookie is set on ``client``."""
    from passlib.context import CryptContext

    from app.models.user import User

    pwd = CryptContext(schemes=["bcrypt"], deprecated="auto")
    user = User(
        email="oauth-admin@example.com",
        password_hash=pwd.hash("test-password"),
        role="admin",
    )
    db.add(user)
    db.flush()

    r = client.post(
        "/api/v1/auth/login",
        json={"email": "oauth-admin@example.com", "password": "test-password"},
    )
    assert r.status_code == 200, r.text
    return client


@pytest.fixture()
def seed_admin(db):
    """Insert one admin User into `db`; return its user_id."""
    from passlib.context import CryptContext

    from app.models.user import User

    pwd = CryptContext(schemes=["bcrypt"], deprecated="auto")
    user = User(
        email="mcp-tool-admin@example.com",
        password_hash=pwd.hash("test-password"),
        role="admin",
    )
    db.add(user)
    db.flush()
    return user.user_id


@pytest.fixture()
def seed_business_profile(db):
    """Populate the singleton BusinessProfile row (id=1) in `db`; return its id.

    Migration 0010_business_profile already INSERTs an all-NULL id=1 row (a
    `ck_business_profile_singleton` check constraint enforces id = 1), so
    this fixture updates that existing row rather than inserting a new one.
    """
    from app.models.business_profile import BusinessProfile

    profile = db.get(BusinessProfile, 1)
    assert profile is not None, "expected migration 0010 to have seeded id=1"
    profile.name = "Wenhao Studio"
    profile.address = "1 Raffles Place, Singapore"
    profile.contact_email = "hello@wenhao.example"
    profile.contact_phone = "+65 6000 0000"
    db.flush()
    return profile.id


@pytest.fixture()
def seed_customer(db):
    """Insert one Customer named 'Acme Pte Ltd' into `db`; return its customer_id."""
    from app.models.customer import Customer

    customer = Customer(name="Acme Pte Ltd", matching_aliases=["Acme"])
    db.add(customer)
    db.flush()
    return customer.customer_id


@pytest.fixture()
def seed_two_acme(db):
    """Insert two Customers whose names both match 'Acme' into `db`;
    return a list of their customer_ids (order not significant)."""
    from app.models.customer import Customer

    c1 = Customer(name="Acme Pte Ltd", matching_aliases=["Acme"])
    c2 = Customer(name="Acme Global Inc", matching_aliases=["Acme"])
    db.add_all([c1, c2])
    db.flush()
    return [c1.customer_id, c2.customer_id]


@pytest.fixture()
def seed_invoice(db, seed_customer):
    """Insert one non-template SENT invoice for `seed_customer` into `db`;
    return its invoice_id."""
    from decimal import Decimal

    from app.models.invoice import Invoice

    invoice = Invoice(
        customer_id=seed_customer,
        invoice_type="MILESTONE",
        invoice_number="INV-0001",
        currency="USD",
        amount=Decimal("1000.0000"),
        balance_due=Decimal("1000.0000"),
        status="SENT",
    )
    db.add(invoice)
    db.flush()
    return invoice.invoice_id


_POISON_MESSAGE = (
    "app.mcp.db.tool_session() was called without the `mcp_tool_db` isolation "
    "fixture — add `mcp_tool_db` to this test's arguments to bind tool_session "
    "to the rollback-safe test connection. (Without it, tool commits would hit "
    "the real database.)"
)


def _poison_tool_session():
    """Default tool-session factory for the whole test process (see
    ``_tool_session_poison_guard`` below): raises immediately instead of
    silently opening a session on the real dev/prod database.
    """
    raise RuntimeError(_POISON_MESSAGE)


@pytest.fixture(scope="session", autouse=True)
def _tool_session_poison_guard():
    """Fail-loud default for app.mcp.db.tool_session() across the whole test
    session: any test that calls a tool (directly or via a future MCP tool
    test) without requesting `mcp_tool_db` gets an immediate, clear
    RuntimeError instead of a silent write to the real database.

    `mcp_tool_db` (function-scoped) temporarily swaps in the real
    test-connection-bound factory for tests that opt in, and restores this
    poison factory — not app.mcp.db's SessionLocal default — when it tears
    down, so the guarantee holds between every pair of tests all session
    long.
    """
    set_tool_session_factory(_poison_tool_session)
    yield
    reset_tool_session_factory()  # restore the real module default on exit


@pytest.fixture()
def mcp_tool_db(db):
    """Bind app.mcp.db.tool_session to the test's rolled-back connection.

    Uses the exact same ``join_transaction_mode="create_savepoint"`` join
    style as the `db` fixture itself, bound to the *same* connection. Each
    Session `tool_session()` creates (one per `with tool_session():` block)
    nests its own transaction as a SAVEPOINT via SQLAlchemy's own external-
    transaction-joining machinery and releases it on commit, without ending
    the outer per-test transaction — precisely mirroring how `db` composes
    with `session.commit()` calls from application code under test.

    This is what makes `mcp_tool_db` safely composable with `client` /
    `admin_session`: those also commit against `db`, sharing this
    connection. (An earlier version of this fixture manually called
    `connection.begin_nested()` and layered a raw-Connection event listener
    on top — mixing that bookkeeping with `db`'s own ORM-level savepoint
    tracking corrupted the connection's transaction stack and produced
    `SAWarning: nested transaction already deassociated from connection`
    whenever `db.commit()` ran while a tool_session was active. Letting both
    sides use the same supported `join_transaction_mode` avoids that
    entirely — no manual savepoint bookkeeping is needed.)

    Everything unwinds when the `db` fixture rolls back the outer
    transaction at teardown; this fixture's own teardown restores the
    fail-loud poison factory (see `_tool_session_poison_guard`) rather than
    app.mcp.db's SessionLocal default.
    """
    connection = db.connection()
    factory = _sessionmaker(
        bind=connection,
        autoflush=False,
        expire_on_commit=False,
        join_transaction_mode="create_savepoint",
    )
    set_tool_session_factory(factory)
    try:
        yield
    finally:
        set_tool_session_factory(_poison_tool_session)


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
