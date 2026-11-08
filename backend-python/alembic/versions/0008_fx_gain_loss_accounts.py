"""seed FX Gain / FX Loss accounts for cross-currency settlement

Revision ID: 0008_fx_gain_loss_accounts
Revises: 0007_customer_contact_fields
Create Date: 2026-04-21

"""
from __future__ import annotations

import sqlalchemy as sa
from alembic import op

revision = "0008_fx_gain_loss_accounts"
down_revision = "0007_customer_contact_fields"
branch_labels = None
depends_on = None


ACCOUNTS = [
    ("4900", "FX Gain (Realised)", "INCOME"),
    ("5900", "FX Loss (Realised)", "EXPENSE"),
]


def upgrade() -> None:
    coa = sa.table(
        "chart_of_accounts",
        sa.column("code", sa.String),
        sa.column("name", sa.String),
        sa.column("type", sa.String),
    )
    op.bulk_insert(
        coa,
        [{"code": c, "name": n, "type": t} for c, n, t in ACCOUNTS],
    )


def downgrade() -> None:
    op.execute(
        "DELETE FROM chart_of_accounts WHERE code IN ("
        + ", ".join(f"'{c}'" for c, _, _ in ACCOUNTS)
        + ")"
    )
