"""invoice line items and invoice header fields

Revision ID: 0009_invoice_line_items
Revises: 0008_fx_gain_loss_accounts
Create Date: 2026-04-21

"""
from __future__ import annotations

import sqlalchemy as sa
from alembic import op
from sqlalchemy.dialects import postgresql

revision = "0009_invoice_line_items"
down_revision = "0008_fx_gain_loss_accounts"
branch_labels = None
depends_on = None


def upgrade() -> None:
    # ---------- extend invoices ----------
    op.add_column("invoices", sa.Column("invoice_number", sa.String(50), nullable=True))
    op.add_column("invoices", sa.Column("po_so_number", sa.String(100), nullable=True))
    op.add_column("invoices", sa.Column("subtotal", sa.Numeric(19, 4), nullable=True))
    op.add_column("invoices", sa.Column("discount_type", sa.String(10), nullable=True))
    op.add_column("invoices", sa.Column("discount_value", sa.Numeric(19, 4), nullable=True))
    op.add_column("invoices", sa.Column("notes", sa.Text(), nullable=True))
    op.add_column("invoices", sa.Column("footer", sa.Text(), nullable=True))
    op.add_column("invoices", sa.Column("payment_terms", sa.String(30), nullable=True))
    op.add_column(
        "invoices",
        sa.Column(
            "is_template",
            sa.Boolean(),
            nullable=False,
            server_default=sa.text("false"),
        ),
    )
    op.create_unique_constraint("uq_invoices_invoice_number", "invoices", ["invoice_number"])

    # allow templates / drafts without a customer or dates
    op.alter_column("invoices", "customer_id", existing_type=postgresql.UUID(as_uuid=True), nullable=True)
    op.alter_column("invoices", "issue_date", existing_type=sa.Date(), nullable=True)
    op.alter_column("invoices", "due_date", existing_type=sa.Date(), nullable=True)

    # ---------- invoice_line_items ----------
    op.create_table(
        "invoice_line_items",
        sa.Column("line_item_id", postgresql.UUID(as_uuid=True), primary_key=True),
        sa.Column(
            "invoice_id",
            postgresql.UUID(as_uuid=True),
            sa.ForeignKey("invoices.invoice_id", ondelete="CASCADE"),
            nullable=False,
        ),
        sa.Column(
            "item_id",
            postgresql.UUID(as_uuid=True),
            sa.ForeignKey("items.item_id"),
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
    op.create_index(
        "ix_invoice_line_items_invoice_id", "invoice_line_items", ["invoice_id"]
    )


def downgrade() -> None:
    op.drop_index("ix_invoice_line_items_invoice_id", table_name="invoice_line_items")
    op.drop_table("invoice_line_items")

    op.alter_column("invoices", "due_date", existing_type=sa.Date(), nullable=False)
    op.alter_column("invoices", "issue_date", existing_type=sa.Date(), nullable=False)
    op.alter_column("invoices", "customer_id", existing_type=postgresql.UUID(as_uuid=True), nullable=False)

    op.drop_constraint("uq_invoices_invoice_number", "invoices", type_="unique")
    op.drop_column("invoices", "is_template")
    op.drop_column("invoices", "payment_terms")
    op.drop_column("invoices", "footer")
    op.drop_column("invoices", "notes")
    op.drop_column("invoices", "discount_value")
    op.drop_column("invoices", "discount_type")
    op.drop_column("invoices", "subtotal")
    op.drop_column("invoices", "po_so_number")
    op.drop_column("invoices", "invoice_number")
