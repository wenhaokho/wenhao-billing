"""bills (AP) tables

Revision ID: 0013_bills_ap
Revises: 0012_customer_extended_fields
Create Date: 2026-04-21

"""
from __future__ import annotations

import sqlalchemy as sa
from alembic import op
from sqlalchemy.dialects import postgresql

revision = "0013_bills_ap"
down_revision = "0012_customer_extended_fields"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.create_table(
        "bills",
        sa.Column("bill_id", postgresql.UUID(as_uuid=True), primary_key=True),
        sa.Column(
            "vendor_id",
            postgresql.UUID(as_uuid=True),
            sa.ForeignKey("vendors.vendor_id"),
            nullable=False,
        ),
        sa.Column("bill_number", sa.String(100), nullable=True),
        sa.Column("po_number", sa.String(100), nullable=True),
        sa.Column("currency", sa.String(3), nullable=False),
        sa.Column("subtotal", sa.Numeric(19, 4), nullable=True),
        sa.Column("discount_type", sa.String(10), nullable=True),
        sa.Column("discount_value", sa.Numeric(19, 4), nullable=True),
        sa.Column("amount", sa.Numeric(19, 4), nullable=False),
        sa.Column("balance_due", sa.Numeric(19, 4), nullable=False),
        sa.Column(
            "status",
            sa.String(20),
            nullable=False,
            server_default=sa.text("'DRAFT'"),
        ),
        sa.Column("issue_date", sa.Date(), nullable=True),
        sa.Column("due_date", sa.Date(), nullable=True),
        sa.Column("payment_terms", sa.String(30), nullable=True),
        sa.Column("notes", sa.Text(), nullable=True),
        sa.Column(
            "created_at",
            sa.DateTime(timezone=False),
            nullable=False,
            server_default=sa.text("CURRENT_TIMESTAMP"),
        ),
    )
    op.create_index("ix_bills_vendor_id", "bills", ["vendor_id"])
    op.create_index("ix_bills_status", "bills", ["status"])

    op.create_table(
        "bill_line_items",
        sa.Column("line_item_id", postgresql.UUID(as_uuid=True), primary_key=True),
        sa.Column(
            "bill_id",
            postgresql.UUID(as_uuid=True),
            sa.ForeignKey("bills.bill_id", ondelete="CASCADE"),
            nullable=False,
        ),
        sa.Column(
            "item_id",
            postgresql.UUID(as_uuid=True),
            sa.ForeignKey("items.item_id"),
            nullable=True,
        ),
        sa.Column(
            "expense_account_id",
            sa.Integer(),
            sa.ForeignKey("chart_of_accounts.account_id"),
            nullable=True,
        ),
        sa.Column("position", sa.Integer(), nullable=False),
        sa.Column("description", sa.Text(), nullable=False),
        sa.Column("quantity", sa.Numeric(19, 4), nullable=False),
        sa.Column("unit_price", sa.Numeric(19, 4), nullable=False),
        sa.Column("amount", sa.Numeric(19, 4), nullable=False),
        sa.Column(
            "created_at",
            sa.DateTime(timezone=False),
            nullable=False,
            server_default=sa.text("CURRENT_TIMESTAMP"),
        ),
    )
    op.create_index("ix_bill_line_items_bill_id", "bill_line_items", ["bill_id"])


def downgrade() -> None:
    op.drop_index("ix_bill_line_items_bill_id", table_name="bill_line_items")
    op.drop_table("bill_line_items")
    op.drop_index("ix_bills_status", table_name="bills")
    op.drop_index("ix_bills_vendor_id", table_name="bills")
    op.drop_table("bills")
