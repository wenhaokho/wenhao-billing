# Per-Currency Payment Accounts Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show currency-specific bank/payment details on each invoice & quotation PDF, managed from Account settings, with a default-account fallback.

**Architecture:** A new `payment_account` table (keyed by currency, one default) replaces the single `business_profile.payment_instructions` field. The PDF context resolves the account by the document's `currency` via a `BusinessProfile.payment_accounts` relationship; the Jinja template is unchanged. A bulk-replace API backs a per-currency editor in `AccountView.vue`.

**Tech Stack:** FastAPI · SQLAlchemy 2.0 · Alembic · Pydantic v2 · pytest (real Postgres) · Vue 3 + TypeScript + PrimeVue v3.

## Global Constraints

- Every SELECT/INSERT/UPDATE stays scoped by `business_profile_id` (single-tenant; always `1`).
- All financial/currency data unchanged in shape; currency is a `String(3)` compared **exactly** against `invoice.currency` (`SGD` / `IDR` / `USD`).
- Alembic revision id **< 32 chars** (`0022_payment_accounts` = 21 chars, OK).
- Tests require a real Postgres and apply migrations via `alembic upgrade head` (session-scoped `migrated_engine`) — model and migration MUST agree.
- Run backend tests with `TEST_DATABASE_URL` set; lint with `ruff check app`.
- Allowed currencies for accounts: **SGD, IDR, USD** (validated on both client and API).
- Do NOT change `read_misc.py` `_PROFILE_FIELDS` (it never exposed `payment_instructions`).

---

### Task 1: `payment_account` model, relationship, migration, schema-field removal

**Files:**
- Create: `backend/app/models/payment_account.py`
- Modify: `backend/app/models/business_profile.py` (remove `payment_instructions` column; add `payment_accounts` relationship)
- Modify: `backend/app/models/__init__.py` (register `PaymentAccount`)
- Modify: `backend/app/schemas/business_profile.py` (remove `payment_instructions` from both models)
- Create: `backend/alembic/versions/0022_payment_accounts.py`
- Test: `backend/tests/integration/test_payment_account_model.py`

**Interfaces:**
- Produces: `PaymentAccount` model with attrs `id: int`, `business_profile_id: int`, `currency: str`, `instructions: str`, `is_default: bool`, `updated_at: datetime`.
- Produces: `BusinessProfile.payment_accounts: list[PaymentAccount]` (ordered by currency, `cascade="all, delete-orphan"`, `lazy="selectin"`).
- Produces: DB table `payment_account` with unique `(business_profile_id, currency)` and a partial unique index `uq_payment_account_one_default` (one `is_default=true` per profile).

- [ ] **Step 1: Write the failing test**

Create `backend/tests/integration/test_payment_account_model.py`:

```python
import pytest
from sqlalchemy.exc import IntegrityError

from app.models.business_profile import BusinessProfile
from app.models.payment_account import PaymentAccount


def test_relationship_orders_and_reads_accounts(db):
    profile = db.get(BusinessProfile, 1)
    assert profile is not None  # migration 0010 seeds id=1
    db.add_all([
        PaymentAccount(business_profile_id=1, currency="USD",
                       instructions="Wells Fargo USD", is_default=False),
        PaymentAccount(business_profile_id=1, currency="IDR",
                       instructions="OCBC NISP IDR", is_default=True),
    ])
    db.flush()
    db.refresh(profile)
    accounts = profile.payment_accounts
    assert [a.currency for a in accounts] == ["IDR", "USD"]  # ordered by currency
    assert [a.is_default for a in accounts] == [True, False]


def test_duplicate_currency_rejected(db):
    db.add(PaymentAccount(business_profile_id=1, currency="SGD",
                          instructions="DBS SGD", is_default=False))
    db.flush()
    db.add(PaymentAccount(business_profile_id=1, currency="SGD",
                          instructions="UOB SGD", is_default=False))
    with pytest.raises(IntegrityError):
        db.flush()


def test_second_default_rejected(db):
    db.add(PaymentAccount(business_profile_id=1, currency="SGD",
                          instructions="DBS SGD", is_default=True))
    db.flush()
    db.add(PaymentAccount(business_profile_id=1, currency="USD",
                          instructions="Wells USD", is_default=True))
    with pytest.raises(IntegrityError):
        db.flush()
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && pytest tests/integration/test_payment_account_model.py -v`
Expected: FAIL — `ModuleNotFoundError: app.models.payment_account` (or table missing).

- [ ] **Step 3: Create the model**

Create `backend/app/models/payment_account.py`:

```python
from datetime import datetime

from sqlalchemy import (
    Boolean,
    DateTime,
    ForeignKey,
    Integer,
    String,
    Text,
    UniqueConstraint,
    func,
    text,
)
from sqlalchemy.orm import Mapped, mapped_column

from app.db.base import Base


class PaymentAccount(Base):
    __tablename__ = "payment_account"
    __table_args__ = (
        UniqueConstraint(
            "business_profile_id", "currency", name="uq_payment_account_currency"
        ),
    )

    id: Mapped[int] = mapped_column(Integer, primary_key=True, autoincrement=True)
    business_profile_id: Mapped[int] = mapped_column(
        ForeignKey("business_profile.id", ondelete="CASCADE"),
        nullable=False,
        default=1,
    )
    currency: Mapped[str] = mapped_column(String(3), nullable=False)
    instructions: Mapped[str] = mapped_column(Text, nullable=False)
    is_default: Mapped[bool] = mapped_column(
        Boolean, nullable=False, server_default=text("false")
    )
    updated_at: Mapped[datetime] = mapped_column(
        DateTime(timezone=False),
        server_default=func.current_timestamp(),
        onupdate=func.current_timestamp(),
    )
```

- [ ] **Step 4: Add the relationship and remove the old column in `business_profile.py`**

Replace the full contents of `backend/app/models/business_profile.py` with:

```python
from datetime import datetime

from sqlalchemy import DateTime, Integer, String, Text, func
from sqlalchemy.orm import Mapped, mapped_column, relationship

from app.db.base import Base


class BusinessProfile(Base):
    __tablename__ = "business_profile"

    id: Mapped[int] = mapped_column(Integer, primary_key=True, default=1)
    name: Mapped[str | None] = mapped_column(String(255), nullable=True)
    address: Mapped[str | None] = mapped_column(Text, nullable=True)
    contact_email: Mapped[str | None] = mapped_column(String(255), nullable=True)
    contact_phone: Mapped[str | None] = mapped_column(String(50), nullable=True)
    invoice_title: Mapped[str | None] = mapped_column(String(255), nullable=True)
    invoice_summary: Mapped[str | None] = mapped_column(Text, nullable=True)
    logo_url: Mapped[str | None] = mapped_column(Text, nullable=True)
    default_notes: Mapped[str | None] = mapped_column(Text, nullable=True)
    updated_at: Mapped[datetime] = mapped_column(
        DateTime(timezone=False),
        server_default=func.current_timestamp(),
        onupdate=func.current_timestamp(),
    )

    payment_accounts: Mapped[list["PaymentAccount"]] = relationship(
        "PaymentAccount",
        cascade="all, delete-orphan",
        lazy="selectin",
        order_by="PaymentAccount.currency",
    )
```

Note: the `payment_instructions` column is intentionally gone. `PaymentAccount` is resolved by string name; it is imported in `models/__init__.py` (next step), so the mapper finds it.

- [ ] **Step 5: Register the model in `models/__init__.py`**

In `backend/app/models/__init__.py`, add the import (alphabetically after `Payment`) and the `__all__` entry:

```python
from app.models.payment import Payment
from app.models.payment_account import PaymentAccount
from app.models.project import Project
```

```python
    "Payment",
    "PaymentAccount",
    "Project",
```

- [ ] **Step 6: Remove `payment_instructions` from the schemas**

In `backend/app/schemas/business_profile.py`, delete the `payment_instructions: str | None = None` line from `BusinessProfileUpdate` and the `payment_instructions: str | None` line from `BusinessProfileOut`. Leave everything else unchanged.

- [ ] **Step 7: Create the migration**

Create `backend/alembic/versions/0022_payment_accounts.py`:

```python
"""per-currency payment accounts

Revision ID: 0022_payment_accounts
Revises: mcp_oauth
Create Date: 2026-07-24
"""
from __future__ import annotations

import sqlalchemy as sa
from alembic import op

revision = "0022_payment_accounts"
down_revision = "mcp_oauth"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.create_table(
        "payment_account",
        sa.Column("id", sa.Integer(), primary_key=True, autoincrement=True),
        sa.Column("business_profile_id", sa.Integer(), nullable=False),
        sa.Column("currency", sa.String(3), nullable=False),
        sa.Column("instructions", sa.Text(), nullable=False),
        sa.Column(
            "is_default",
            sa.Boolean(),
            nullable=False,
            server_default=sa.text("false"),
        ),
        sa.Column(
            "updated_at",
            sa.DateTime(timezone=False),
            nullable=False,
            server_default=sa.text("CURRENT_TIMESTAMP"),
        ),
        sa.ForeignKeyConstraint(
            ["business_profile_id"], ["business_profile.id"], ondelete="CASCADE"
        ),
        sa.UniqueConstraint(
            "business_profile_id", "currency", name="uq_payment_account_currency"
        ),
    )
    op.create_index(
        "uq_payment_account_one_default",
        "payment_account",
        ["business_profile_id"],
        unique=True,
        postgresql_where=sa.text("is_default"),
    )
    # Preserve the existing single field as the IDR default account.
    op.execute(
        """
        INSERT INTO payment_account
            (business_profile_id, currency, instructions, is_default)
        SELECT 1, 'IDR', payment_instructions, true
        FROM business_profile
        WHERE id = 1
          AND payment_instructions IS NOT NULL
          AND btrim(payment_instructions) <> ''
        """
    )
    op.drop_column("business_profile", "payment_instructions")


def downgrade() -> None:
    op.add_column(
        "business_profile",
        sa.Column("payment_instructions", sa.Text(), nullable=True),
    )
    op.execute(
        """
        UPDATE business_profile b
        SET payment_instructions = pa.instructions
        FROM payment_account pa
        WHERE b.id = pa.business_profile_id AND pa.is_default
        """
    )
    op.drop_index("uq_payment_account_one_default", table_name="payment_account")
    op.drop_table("payment_account")
```

- [ ] **Step 8: Run the model tests to verify they pass**

Run: `cd backend && pytest tests/integration/test_payment_account_model.py -v`
Expected: PASS (3 tests). The session-scoped `migrated_engine` applies `0022` before the tests run.

- [ ] **Step 9: Run the full backend suite + lint to confirm no regressions**

Run: `cd backend && ruff check app && pytest -q`
Expected: PASS. (`test_pdf_context` still passes here — it uses `SimpleNamespace`, not the DB, and `context.py` is unchanged until Task 2.)

- [ ] **Step 10: Commit**

```bash
git add backend/app/models/payment_account.py backend/app/models/business_profile.py backend/app/models/__init__.py backend/app/schemas/business_profile.py backend/alembic/versions/0022_payment_accounts.py backend/tests/integration/test_payment_account_model.py
git commit -m "feat: add payment_account table keyed by currency with default fallback"
```

---

### Task 2: Currency-aware payment instructions in the PDF context

**Files:**
- Modify: `backend/app/services/pdf/context.py` (add `resolve_payment_instructions`; use it in `_common`)
- Modify: `backend/tests/unit/test_pdf_context.py` (`_biz()` → `payment_accounts`; new selection tests)

**Interfaces:**
- Consumes: `PaymentAccount`-shaped objects (attrs `currency`, `instructions`, `is_default`) via `business.payment_accounts` (Task 1).
- Produces: `resolve_payment_instructions(accounts, currency) -> str | None` in `app.services.pdf.context`.
- Produces: context key `payment_instructions` unchanged in name (template stays stable).

- [ ] **Step 1: Write the failing tests**

In `backend/tests/unit/test_pdf_context.py`, replace the `_biz()` helper (lines ~12–19) with:

```python
def _biz():
    return SimpleNamespace(
        name="Wenhao Development Pte Ltd",
        address="1 Marina Boulevard\nSingapore 018989",
        contact_email="accounts@wenhao.dev", contact_phone="+65 6123 4567",
        invoice_title="Invoice", invoice_summary=None, logo_url=None,
        default_notes="Default note.",
        payment_accounts=[
            SimpleNamespace(currency="SGD", instructions="DBS 012-345678-9",
                            is_default=False),
            SimpleNamespace(currency="IDR", instructions="OCBC NISP IDR",
                            is_default=True),
        ],
    )
```

Then append these tests to the file:

```python
from app.services.pdf.context import resolve_payment_instructions


def test_resolve_payment_instructions_matches_currency():
    accounts = _biz().payment_accounts
    assert resolve_payment_instructions(accounts, "SGD") == "DBS 012-345678-9"


def test_resolve_payment_instructions_falls_back_to_default():
    accounts = _biz().payment_accounts
    # No USD account -> the is_default (IDR) account wins.
    assert resolve_payment_instructions(accounts, "USD") == "OCBC NISP IDR"


def test_resolve_payment_instructions_none_when_empty():
    assert resolve_payment_instructions([], "SGD") is None
    assert resolve_payment_instructions(None, "SGD") is None


def test_invoice_context_selects_default_when_currency_unmatched():
    ctx = build_invoice_context(_inv(currency="USD"), _cust(), _biz())
    assert ctx["payment_instructions"] == "OCBC NISP IDR"


def test_invoice_context_omits_block_when_no_accounts():
    biz = _biz()
    biz.payment_accounts = []
    ctx = build_invoice_context(_inv(), _cust(), biz)
    assert ctx["payment_instructions"] is None
```

(The existing `test_invoice_context_discount_and_paid` still asserts `ctx["payment_instructions"] == "DBS 012-345678-9"`; that invoice is `currency="SGD"`, which now matches the SGD account — no change needed to that assertion.)

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd backend && pytest tests/unit/test_pdf_context.py -v`
Expected: FAIL — `ImportError: cannot import name 'resolve_payment_instructions'`.

- [ ] **Step 3: Add the resolver and wire it into `_common`**

In `backend/app/services/pdf/context.py`, add the resolver above `_common`:

```python
def resolve_payment_instructions(accounts, currency: str | None) -> str | None:
    """Pick the payment-instructions block for `currency`.

    Exact currency match wins; otherwise the account flagged `is_default`;
    otherwise None (the template omits the block).
    """
    rows = list(accounts or [])
    if not rows:
        return None
    for a in rows:
        if a.currency == currency:
            return a.instructions
    for a in rows:
        if a.is_default:
            return a.instructions
    return None
```

Then in `_common`, replace this line:

```python
        "payment_instructions": getattr(business, "payment_instructions", None),
```

with:

```python
        "payment_instructions": resolve_payment_instructions(
            getattr(business, "payment_accounts", None), doc.currency
        ),
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd backend && pytest tests/unit/test_pdf_context.py -v`
Expected: PASS (all existing + 5 new tests).

- [ ] **Step 5: Commit**

```bash
git add backend/app/services/pdf/context.py backend/tests/unit/test_pdf_context.py
git commit -m "feat: select invoice PDF payment instructions by currency"
```

---

### Task 3: Payment-accounts API (schemas + GET/PUT replace)

**Files:**
- Modify: `backend/app/schemas/business_profile.py` (add `ALLOWED_CURRENCIES`, `PaymentAccountIn`, `PaymentAccountOut`)
- Modify: `backend/app/api/v1/routers/business_profile.py` (GET + PUT-replace endpoints)
- Test: `backend/tests/integration/test_payment_accounts_api.py`

**Interfaces:**
- Consumes: `PaymentAccount` model (Task 1); `_get_or_create(db)` (existing in router).
- Produces: `GET /api/v1/business-profile/payment-accounts` → `list[PaymentAccountOut]`.
- Produces: `PUT /api/v1/business-profile/payment-accounts` — body `list[PaymentAccountIn]`, replaces the whole set, returns `list[PaymentAccountOut]`.
- Produces: `ALLOWED_CURRENCIES = {"SGD", "IDR", "USD"}` in `app.schemas.business_profile`.

- [ ] **Step 1: Write the failing tests**

Create `backend/tests/integration/test_payment_accounts_api.py`:

```python
def test_replace_and_list_payment_accounts(admin_session):
    client = admin_session
    body = [
        {"currency": "IDR", "instructions": "OCBC NISP IDR", "is_default": True},
        {"currency": "SGD", "instructions": "DBS SGD", "is_default": False},
    ]
    r = client.put("/api/v1/business-profile/payment-accounts", json=body)
    assert r.status_code == 200, r.text
    got = r.json()
    assert [a["currency"] for a in got] == ["IDR", "SGD"]  # ordered by currency
    assert [a["is_default"] for a in got] == [True, False]

    r2 = client.get("/api/v1/business-profile/payment-accounts")
    assert r2.status_code == 200
    assert {a["currency"] for a in r2.json()} == {"IDR", "SGD"}


def test_replace_is_full_overwrite(admin_session):
    client = admin_session
    client.put("/api/v1/business-profile/payment-accounts",
               json=[{"currency": "USD", "instructions": "Wells", "is_default": True}])
    client.put("/api/v1/business-profile/payment-accounts",
               json=[{"currency": "SGD", "instructions": "DBS", "is_default": True}])
    r = client.get("/api/v1/business-profile/payment-accounts")
    assert {a["currency"] for a in r.json()} == {"SGD"}  # USD gone


def test_reject_duplicate_currency(admin_session):
    r = admin_session.put(
        "/api/v1/business-profile/payment-accounts",
        json=[
            {"currency": "SGD", "instructions": "DBS", "is_default": False},
            {"currency": "SGD", "instructions": "UOB", "is_default": True},
        ],
    )
    assert r.status_code == 422


def test_reject_two_defaults(admin_session):
    r = admin_session.put(
        "/api/v1/business-profile/payment-accounts",
        json=[
            {"currency": "SGD", "instructions": "DBS", "is_default": True},
            {"currency": "USD", "instructions": "Wells", "is_default": True},
        ],
    )
    assert r.status_code == 422


def test_reject_unsupported_currency(admin_session):
    r = admin_session.put(
        "/api/v1/business-profile/payment-accounts",
        json=[{"currency": "EUR", "instructions": "SEPA", "is_default": True}],
    )
    assert r.status_code == 422
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd backend && pytest tests/integration/test_payment_accounts_api.py -v`
Expected: FAIL — 404 (routes not defined yet).

- [ ] **Step 3: Add the schemas**

In `backend/app/schemas/business_profile.py`, add at the top (after the imports) and below the existing classes:

```python
ALLOWED_CURRENCIES = {"SGD", "IDR", "USD"}


class PaymentAccountIn(BaseModel):
    currency: str
    instructions: str
    is_default: bool = False


class PaymentAccountOut(BaseModel):
    model_config = ConfigDict(from_attributes=True)

    id: int
    currency: str
    instructions: str
    is_default: bool
    updated_at: datetime
```

- [ ] **Step 4: Add the endpoints**

In `backend/app/api/v1/routers/business_profile.py`, extend the imports:

```python
from fastapi import APIRouter, Depends, HTTPException
from sqlalchemy import delete, select
from sqlalchemy.orm import Session

from app.api.deps import current_admin
from app.db.session import get_db
from app.models.business_profile import BusinessProfile
from app.models.payment_account import PaymentAccount
from app.models.user import User
from app.schemas.business_profile import (
    ALLOWED_CURRENCIES,
    BusinessProfileOut,
    BusinessProfileUpdate,
    PaymentAccountIn,
    PaymentAccountOut,
)
```

Then append these two endpoints to the file:

```python
def _list_accounts(db: Session) -> list[PaymentAccount]:
    return list(
        db.scalars(
            select(PaymentAccount)
            .where(PaymentAccount.business_profile_id == 1)
            .order_by(PaymentAccount.currency)
        )
    )


@router.get("/payment-accounts", response_model=list[PaymentAccountOut])
def list_payment_accounts(
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> list[PaymentAccount]:
    _get_or_create(db)
    return _list_accounts(db)


@router.put("/payment-accounts", response_model=list[PaymentAccountOut])
def replace_payment_accounts(
    payload: list[PaymentAccountIn],
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> list[PaymentAccount]:
    currencies = [a.currency.upper() for a in payload]
    if len(set(currencies)) != len(currencies):
        raise HTTPException(422, "Duplicate currency in payment accounts")
    unsupported = sorted({c for c in currencies if c not in ALLOWED_CURRENCIES})
    if unsupported:
        raise HTTPException(422, f"Unsupported currency: {', '.join(unsupported)}")
    if sum(1 for a in payload if a.is_default) > 1:
        raise HTTPException(422, "At most one account can be the default")

    _get_or_create(db)
    db.execute(delete(PaymentAccount).where(PaymentAccount.business_profile_id == 1))
    db.flush()
    for a in payload:
        db.add(
            PaymentAccount(
                business_profile_id=1,
                currency=a.currency.upper(),
                instructions=a.instructions,
                is_default=a.is_default,
            )
        )
    db.commit()
    return _list_accounts(db)
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd backend && pytest tests/integration/test_payment_accounts_api.py -v`
Expected: PASS (5 tests).

- [ ] **Step 6: Lint + full suite**

Run: `cd backend && ruff check app && pytest -q`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add backend/app/schemas/business_profile.py backend/app/api/v1/routers/business_profile.py backend/tests/integration/test_payment_accounts_api.py
git commit -m "feat: add payment-accounts list/replace API with validation"
```

---

### Task 4: Update the seed script

**Files:**
- Modify: `backend/scripts/seed_business_profile.py`

**Interfaces:**
- Consumes: `PaymentAccount` model (Task 1).

- [ ] **Step 1: Remove `payment_instructions` from the profile dict and seed an IDR default account**

In `backend/scripts/seed_business_profile.py`:

1. Delete the `"payment_instructions": (...)` entry from the `PROFILE` dict.
2. Add the import near the top:

```python
from app.models.payment_account import PaymentAccount
```

3. Add this module-level constant below `PROFILE`:

```python
# The existing OCBC NISP account (SWIFT NISPIDJA) is an Indonesian bank → IDR.
IDR_ACCOUNT_INSTRUCTIONS = (
    "Acc Name: KARDONO WIJAYA\n"
    "Bank: OCBC\n"
    "Account number: 090810625088\n"
    "SWIFT code: NISPIDJA"
)
```

4. Add a standalone, idempotent account seeder (uses the top-level `PaymentAccount` import from step 2):

```python
def _seed_payment_accounts(db) -> None:
    existing = db.query(PaymentAccount).filter_by(business_profile_id=1).count()
    if existing:
        print(f"skipping payment accounts: {existing} already present")
        return
    db.add(
        PaymentAccount(
            business_profile_id=1,
            currency="IDR",
            instructions=IDR_ACCOUNT_INSTRUCTIONS,
            is_default=True,
        )
    )
    db.commit()
    print("seeded IDR default payment account")
```

5. Call it in `seed()` **regardless of the profile early-return**. The current `seed()` returns early when `profile.name` is already set; move that guard so account seeding still runs. Replace the body of `seed()` with:

```python
def seed() -> None:
    with SessionLocal() as db:
        profile = db.get(BusinessProfile, 1)
        if profile is not None and profile.name:
            print(f"skipping: business profile already set ('{profile.name}')")
        else:
            if profile is None:
                profile = BusinessProfile(id=1)
                db.add(profile)
            for field, value in PROFILE.items():
                setattr(profile, field, value)
            db.commit()
            print(f"business profile seeded ('{PROFILE['name']}')")

        _seed_payment_accounts(db)
```

- [ ] **Step 2: Lint**

Run: `cd backend && ruff check scripts`
Expected: PASS (no unused imports, no undefined names).

- [ ] **Step 3: Verify the script runs against a live DB (manual)**

Run (with the stack up): `docker compose exec backend python -m scripts.seed_business_profile`
Expected output includes either `seeded IDR default payment account` or `skipping payment accounts: N already present`. Re-running is idempotent (no duplicate accounts).

- [ ] **Step 4: Commit**

```bash
git add backend/scripts/seed_business_profile.py
git commit -m "chore: seed IDR default payment account; drop single payment_instructions"
```

---

### Task 5: Account settings — per-currency payment accounts editor

**Files:**
- Modify: `frontend/src/views/AccountView.vue`

**Interfaces:**
- Consumes: `GET /business-profile/payment-accounts`, `PUT /business-profile/payment-accounts` (Task 3).
- Produces: a payment-accounts editor persisted alongside the company profile Save.

- [ ] **Step 1: Add the Dropdown import and account state (script)**

In `frontend/src/views/AccountView.vue` `<script setup>`:

Add to the PrimeVue imports (after `import Textarea ...`):

```ts
import Dropdown from "primevue/dropdown";
```

Remove `payment_instructions` from the `BusinessProfile` type (line ~84), from the `company` ref default (line ~95), and from the `loadCompany` mapping (line ~115).

Add below the `company` ref block:

```ts
const CURRENCY_OPTIONS = ["SGD", "IDR", "USD"] as const;

interface PaymentAccountRow {
  currency: string;
  instructions: string;
  is_default: boolean;
}
const paymentAccounts = ref<PaymentAccountRow[]>([]);

function usedCurrencies(exceptIndex = -1): Set<string> {
  return new Set(
    paymentAccounts.value
      .filter((_, i) => i !== exceptIndex)
      .map((a) => a.currency),
  );
}
function availableCurrencies(index: number): string[] {
  const used = usedCurrencies(index);
  return CURRENCY_OPTIONS.filter((c) => !used.has(c) || c === paymentAccounts.value[index]?.currency);
}
function canAddAccount(): boolean {
  return paymentAccounts.value.length < CURRENCY_OPTIONS.length;
}
function addAccount() {
  const used = usedCurrencies();
  const next = CURRENCY_OPTIONS.find((c) => !used.has(c)) ?? "";
  paymentAccounts.value.push({
    currency: next,
    instructions: "",
    is_default: paymentAccounts.value.length === 0,
  });
}
function removeAccount(index: number) {
  const wasDefault = paymentAccounts.value[index]?.is_default;
  paymentAccounts.value.splice(index, 1);
  if (wasDefault && paymentAccounts.value.length > 0) {
    paymentAccounts.value[0].is_default = true;
  }
}
function setDefault(index: number) {
  paymentAccounts.value.forEach((a, i) => (a.is_default = i === index));
}
```

- [ ] **Step 2: Load accounts on mount (script)**

Inside `loadCompany`, after the `company.value = { ... }` assignment, add a second fetch:

```ts
    const accts = await api.get("/business-profile/payment-accounts");
    paymentAccounts.value = (accts.data as PaymentAccountRow[]).map((a) => ({
      currency: a.currency,
      instructions: a.instructions,
      is_default: a.is_default,
    }));
```

- [ ] **Step 3: Persist accounts on save (script)**

In `saveCompany`, after the existing `await api.put("/business-profile", payload);` line, add:

```ts
    // Client-side guards mirror the API (unique currency, <=1 default).
    const currencies = paymentAccounts.value.map((a) => a.currency);
    if (new Set(currencies).size !== currencies.length) {
      throw new Error("Each currency can only have one payment account.");
    }
    if (paymentAccounts.value.filter((a) => a.is_default).length > 1) {
      throw new Error("Only one account can be the default.");
    }
    await api.put(
      "/business-profile/payment-accounts",
      paymentAccounts.value.map((a) => ({
        currency: a.currency,
        instructions: a.instructions,
        is_default: a.is_default,
      })),
    );
```

Update the `catch` to surface plain `Error` messages too:

```ts
  } catch (e: any) {
    companyError.value =
      e?.response?.data?.detail ?? e?.message ?? "Failed to save company profile";
```

- [ ] **Step 4: Replace the single textarea with the editor (template)**

In the template, replace the whole `payment_instructions` `<label>` block (lines ~362–366) with:

```html
            <div class="pay-accounts">
              <div class="pay-accounts-head">
                <span>Payment accounts</span>
                <Button
                  type="button"
                  label="Add account"
                  icon="pi pi-plus"
                  text
                  size="small"
                  :disabled="!canAddAccount()"
                  @click="addAccount"
                />
              </div>
              <small>Bank / PayNow details per currency. The default is used on invoices whose currency has no specific account.</small>

              <div v-for="(acct, i) in paymentAccounts" :key="i" class="pay-account-row">
                <div class="pay-account-controls">
                  <Dropdown
                    v-model="acct.currency"
                    :options="availableCurrencies(i)"
                    placeholder="Currency"
                    class="pay-currency"
                  />
                  <label class="pay-default">
                    <input
                      type="radio"
                      :checked="acct.is_default"
                      @change="setDefault(i)"
                    />
                    Default
                  </label>
                  <Button
                    type="button"
                    icon="pi pi-trash"
                    severity="danger"
                    text
                    size="small"
                    @click="removeAccount(i)"
                  />
                </div>
                <Textarea v-model="acct.instructions" rows="4" placeholder="Bank / account details" />
              </div>

              <small v-if="paymentAccounts.length === 0" class="pay-empty">
                No payment accounts yet — add one so invoices show where to pay.
              </small>
            </div>
```

- [ ] **Step 5: Add minimal styles (template `<style scoped>`)**

Append to the component's `<style scoped>` block:

```css
.pay-accounts-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.pay-account-row {
  border: 1px solid var(--surface-border, #e4e7ec);
  border-radius: 6px;
  padding: 0.75rem;
  margin-top: 0.5rem;
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}
.pay-account-controls {
  display: flex;
  align-items: center;
  gap: 1rem;
}
.pay-default {
  display: inline-flex;
  align-items: center;
  gap: 0.35rem;
  font-size: 0.85rem;
}
```

- [ ] **Step 6: Typecheck + build**

Run: `cd frontend && npm run build`
Expected: PASS (`vue-tsc -b && vite build` with no type errors — confirms `payment_instructions` references are fully removed and the new refs typecheck).

- [ ] **Step 7: Commit**

```bash
git add frontend/src/views/AccountView.vue
git commit -m "feat: per-currency payment accounts editor in Account settings"
```

---

## Verification (whole feature)

- [ ] `cd backend && ruff check app && pytest -q` — all green.
- [ ] `cd frontend && npm run build` — typecheck + build green.
- [ ] Manual smoke (stack up): in Account settings add SGD/IDR/USD accounts, mark one default, Save; generate an SGD invoice PDF → shows the SGD account; generate an invoice in a currency without an account → shows the default.
