"""collapse hosting_subscriptions onto recurring invoice template

Revision ID: 0018_hosting_on_invoice
Revises: 0017_hosting_subscriptions
Create Date: 2026-06-22
"""
from __future__ import annotations

import sqlalchemy as sa
from alembic import op
from sqlalchemy.dialects import postgresql

revision = "0018_hosting_on_invoice"
down_revision = "0017_hosting_subscriptions"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.drop_constraint("fk_invoices_subscription_id", "invoices", type_="foreignkey")
    op.drop_index("ix_invoices_subscription_id", table_name="invoices")
    op.drop_column("invoices", "subscription_id")

    op.drop_table("cloudflare_targets")

    op.drop_index(
        "ix_hosting_subscriptions_template_invoice_id",
        table_name="hosting_subscriptions",
    )
    op.drop_index("ix_hosting_subscriptions_project_id", table_name="hosting_subscriptions")
    op.drop_index(
        "ix_hosting_subscriptions_last_invoice_id",
        table_name="hosting_subscriptions",
    )
    op.drop_index("ix_hosting_subscriptions_item_id", table_name="hosting_subscriptions")
    op.drop_index(
        "ix_hosting_subscriptions_customer_id",
        table_name="hosting_subscriptions",
    )
    op.drop_table("hosting_subscriptions")

    op.add_column(
        "invoices",
        sa.Column(
            "is_hosting",
            sa.Boolean(),
            nullable=False,
            server_default=sa.false(),
        ),
    )
    op.add_column(
        "invoices",
        sa.Column("hosting_service_name", sa.String(length=255), nullable=True),
    )
    op.add_column(
        "invoices", sa.Column("hosting_domain", sa.String(length=255), nullable=True)
    )
    op.add_column(
        "invoices", sa.Column("hosting_grace_days", sa.Integer(), nullable=True)
    )
    op.add_column(
        "invoices", sa.Column("hosting_suspension_enabled", sa.Boolean(), nullable=True)
    )
    op.add_column(
        "invoices", sa.Column("hosting_status", sa.String(length=20), nullable=True)
    )
    op.add_column(
        "invoices",
        sa.Column("hosting_last_paid_at", sa.DateTime(timezone=False), nullable=True),
    )
    op.add_column(
        "invoices",
        sa.Column("hosting_last_action_at", sa.DateTime(timezone=False), nullable=True),
    )
    op.add_column("invoices", sa.Column("hosting_last_error", sa.Text(), nullable=True))
    op.create_index(
        "ix_invoices_is_hosting",
        "invoices",
        ["is_hosting"],
        postgresql_where=sa.text("is_hosting = true"),
    )

    op.create_table(
        "cloudflare_targets",
        sa.Column("target_id", postgresql.UUID(as_uuid=True), nullable=False),
        sa.Column("invoice_id", postgresql.UUID(as_uuid=True), nullable=False),
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
            ["invoice_id"], ["invoices.invoice_id"], ondelete="CASCADE"
        ),
        sa.PrimaryKeyConstraint("target_id"),
        sa.UniqueConstraint("invoice_id"),
    )


def downgrade() -> None:
    raise NotImplementedError("forward-only refactor of unreleased hosting feature")
