"""quotations + quotation_line_items, source_quote_id on invoices

Revision ID: 0015_quotations
Revises: 0014_projects
Create Date: 2026-04-22

"""
from __future__ import annotations

import sqlalchemy as sa
from alembic import op
from sqlalchemy.dialects import postgresql

revision = "0015_quotations"
down_revision = "0014_projects"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.create_table(
        "quotations",
        sa.Column("quotation_id", postgresql.UUID(as_uuid=True), primary_key=True),
        sa.Column(
            "customer_id",
            postgresql.UUID(as_uuid=True),
            sa.ForeignKey("customers.customer_id"),
            nullable=False,
        ),
        sa.Column(
            "project_id",
            postgresql.UUID(as_uuid=True),
            sa.ForeignKey("projects.project_id"),
            nullable=True,
        ),
        sa.Column("quotation_number", sa.String(50), nullable=True, unique=True),
        sa.Column("po_so_number", sa.String(100), nullable=True),
        sa.Column("currency", sa.String(3), nullable=False),
        sa.Column("subtotal", sa.Numeric(19, 4), nullable=True),
        sa.Column("discount_type", sa.String(10), nullable=True),
        sa.Column("discount_value", sa.Numeric(19, 4), nullable=True),
        sa.Column("amount", sa.Numeric(19, 4), nullable=False),
        sa.Column(
            "status",
            sa.String(20),
            nullable=False,
            server_default=sa.text("'DRAFT'"),
        ),
        sa.Column("issue_date", sa.Date(), nullable=True),
        sa.Column("valid_until", sa.Date(), nullable=True),
        sa.Column("payment_terms", sa.String(30), nullable=True),
        sa.Column("notes", sa.Text(), nullable=True),
        sa.Column("footer", sa.Text(), nullable=True),
        sa.Column("last_sent_at", sa.DateTime(timezone=False), nullable=True),
        sa.Column("accepted_at", sa.DateTime(timezone=False), nullable=True),
        sa.Column("accepted_by", sa.String(255), nullable=True),
        sa.Column(
            "converted_invoice_id",
            postgresql.UUID(as_uuid=True),
            sa.ForeignKey("invoices.invoice_id"),
            nullable=True,
        ),
        sa.Column(
            "created_at",
            sa.DateTime(timezone=False),
            nullable=False,
            server_default=sa.text("CURRENT_TIMESTAMP"),
        ),
    )
    op.create_index("ix_quotations_customer_id", "quotations", ["customer_id"])
    op.create_index("ix_quotations_project_id", "quotations", ["project_id"])
    op.create_index(
        "ix_quotations_converted_invoice_id", "quotations", ["converted_invoice_id"]
    )

    op.create_table(
        "quotation_line_items",
        sa.Column("line_item_id", postgresql.UUID(as_uuid=True), primary_key=True),
        sa.Column(
            "quotation_id",
            postgresql.UUID(as_uuid=True),
            sa.ForeignKey("quotations.quotation_id", ondelete="CASCADE"),
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
        "ix_quotation_line_items_quotation_id",
        "quotation_line_items",
        ["quotation_id"],
    )

    op.add_column(
        "invoices",
        sa.Column(
            "source_quote_id",
            postgresql.UUID(as_uuid=True),
            sa.ForeignKey("quotations.quotation_id"),
            nullable=True,
        ),
    )
    op.create_index("ix_invoices_source_quote_id", "invoices", ["source_quote_id"])


def downgrade() -> None:
    op.drop_index("ix_invoices_source_quote_id", table_name="invoices")
    op.drop_column("invoices", "source_quote_id")
    op.drop_index(
        "ix_quotation_line_items_quotation_id", table_name="quotation_line_items"
    )
    op.drop_table("quotation_line_items")
    op.drop_index("ix_quotations_converted_invoice_id", table_name="quotations")
    op.drop_index("ix_quotations_project_id", table_name="quotations")
    op.drop_index("ix_quotations_customer_id", table_name="quotations")
    op.drop_table("quotations")
