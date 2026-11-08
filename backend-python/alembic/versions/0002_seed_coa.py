"""seed chart of accounts (PRD §4 baseline)

Revision ID: 0002_seed_coa
Revises: 0001_initial
Create Date: 2026-04-21

"""
from __future__ import annotations

import sqlalchemy as sa
from alembic import op

revision = "0002_seed_coa"
down_revision = "0001_initial"
branch_labels = None
depends_on = None


ACCOUNTS = [
    # Assets (1xxx)
    ("1000", "Operating Bank", "ASSET"),
    ("1100", "Accounts Receivable", "ASSET"),
    # Liabilities (2xxx)
    ("2000", "Accounts Payable", "LIABILITY"),
    ("2100", "Sales Tax Payable", "LIABILITY"),
    ("2200", "Unbilled AR (Accrued Revenue)", "LIABILITY"),
    # Income (4xxx)
    ("4000", "Fixed-Fee Revenue", "INCOME"),
    ("4100", "Support/Maintenance Revenue", "INCOME"),
    ("4200", "Usage-Based Revenue (AI Tokens)", "INCOME"),
    # COGS (5xxx)
    ("5000", "External Contractor Fees", "COGS"),
    ("5100", "Cloud Infrastructure", "COGS"),
    ("5200", "AI Inference Costs", "COGS"),
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
