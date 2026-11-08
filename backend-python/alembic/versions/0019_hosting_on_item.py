"""move hosting from invoice template onto item

Revision ID: 0019_hosting_on_item
Revises: 0018_hosting_on_invoice
Create Date: 2026-06-22
"""
from __future__ import annotations

import sqlalchemy as sa
from alembic import op
from sqlalchemy.dialects import postgresql

revision = "0019_hosting_on_item"
down_revision = "0018_hosting_on_invoice"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.drop_table("cloudflare_targets")

    op.drop_index("ix_invoices_is_hosting", table_name="invoices")
    op.drop_column("invoices", "hosting_last_error")
    op.drop_column("invoices", "hosting_last_action_at")
    op.drop_column("invoices", "hosting_last_paid_at")
    op.drop_column("invoices", "hosting_status")
    op.drop_column("invoices", "hosting_suspension_enabled")
    op.drop_column("invoices", "hosting_grace_days")
    op.drop_column("invoices", "hosting_domain")
    op.drop_column("invoices", "hosting_service_name")
    op.drop_column("invoices", "is_hosting")

    op.add_column(
        "items",
        sa.Column(
            "is_hosting",
            sa.Boolean(),
            nullable=False,
            server_default=sa.false(),
        ),
    )
    op.add_column(
        "items", sa.Column("hosting_domain", sa.String(length=255), nullable=True)
    )
    op.add_column(
        "items", sa.Column("hosting_grace_days", sa.Integer(), nullable=True)
    )
    op.add_column(
        "items", sa.Column("hosting_suspension_enabled", sa.Boolean(), nullable=True)
    )
    op.add_column(
        "items", sa.Column("hosting_status", sa.String(length=20), nullable=True)
    )
    op.add_column(
        "items",
        sa.Column("hosting_last_paid_at", sa.DateTime(timezone=False), nullable=True),
    )
    op.add_column(
        "items",
        sa.Column("hosting_last_action_at", sa.DateTime(timezone=False), nullable=True),
    )
    op.add_column("items", sa.Column("hosting_last_error", sa.Text(), nullable=True))
    op.create_index(
        "ix_items_is_hosting",
        "items",
        ["is_hosting"],
        postgresql_where=sa.text("is_hosting = true"),
    )

    op.create_table(
        "cloudflare_targets",
        sa.Column("target_id", postgresql.UUID(as_uuid=True), nullable=False),
        sa.Column("item_id", postgresql.UUID(as_uuid=True), nullable=False),
        sa.Column("zone_id", sa.String(length=100), nullable=False),
        sa.Column("record_id", sa.String(length=100), nullable=False),
        sa.Column("record_name", sa.String(length=255), nullable=False),
        sa.Column("record_type", sa.String(length=20), nullable=False),
        sa.Column("live_content", sa.Text(), nullable=False),
        sa.Column("maintenance_content", sa.Text(), nullable=False),
        sa.Column("proxied", sa.Boolean(), nullable=False, server_default=sa.true()),
        sa.Column(
            "provider_status",
            sa.String(length=20),
            nullable=False,
            server_default="ACTIVE",
        ),
        sa.Column("last_action_at", sa.DateTime(timezone=False), nullable=True),
        sa.Column("last_error", sa.Text(), nullable=True),
        sa.Column(
            "created_at",
            sa.DateTime(timezone=False),
            nullable=False,
            server_default=sa.func.current_timestamp(),
        ),
        sa.Column(
            "updated_at",
            sa.DateTime(timezone=False),
            nullable=False,
            server_default=sa.func.current_timestamp(),
        ),
        sa.ForeignKeyConstraint(
            ["item_id"], ["items.item_id"], ondelete="CASCADE"
        ),
        sa.PrimaryKeyConstraint("target_id"),
        sa.UniqueConstraint("item_id"),
    )


def downgrade() -> None:
    raise NotImplementedError("forward-only refactor of unreleased hosting feature")
