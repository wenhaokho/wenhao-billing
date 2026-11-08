"""withholding tax (PPh 21/23) columns and prepaid-tax account

Revision ID: 0027_withholding_tax
Revises: 0026_customer_cc_emails
Create Date: 2026-10-26

Adds per-customer default withholding settings, per-invoice withholding
snapshot columns (type, rate, gross-up, base, amount, booked), the
``payments.wht_amount`` recognised on a payment, and the CoA account
1300 Prepaid Income Tax.
"""
from __future__ import annotations

import sqlalchemy as sa

from alembic import op

revision = "0027_withholding_tax"
down_revision = "0026_customer_cc_emails"
branch_labels = None
depends_on = None


def upgrade() -> None:
    for prefix, table in (("default_wht", "customers"), ("wht", "invoices")):
        op.add_column(
            table,
            sa.Column(
                f"{prefix}_type", sa.String(10), nullable=False, server_default="NONE"
            ),
        )
        op.add_column(table, sa.Column(f"{prefix}_rate", sa.Numeric(7, 4), nullable=True))
        op.add_column(
            table,
            sa.Column(
                f"{prefix}_gross_up",
                sa.Boolean(),
                nullable=False,
                server_default=sa.false(),
            ),
        )
    op.add_column("invoices", sa.Column("wht_base", sa.Numeric(19, 4), nullable=True))
    for col in ("wht_amount", "wht_booked"):
        op.add_column(
            "invoices",
            sa.Column(col, sa.Numeric(19, 4), nullable=False, server_default="0"),
        )
    op.add_column(
        "payments",
        sa.Column("wht_amount", sa.Numeric(19, 4), nullable=False, server_default="0"),
    )
    op.execute(
        "INSERT INTO chart_of_accounts (code, name, type) VALUES "
        "('1300', 'Prepaid Income Tax (PPh dibayar di muka)', 'ASSET')"
    )


def downgrade() -> None:
    op.execute("DELETE FROM chart_of_accounts WHERE code = '1300'")
    op.drop_column("payments", "wht_amount")
    for col in ("wht_booked", "wht_amount", "wht_base"):
        op.drop_column("invoices", col)
    for prefix, table in (("wht", "invoices"), ("default_wht", "customers")):
        for suffix in ("gross_up", "rate", "type"):
            op.drop_column(table, f"{prefix}_{suffix}")
