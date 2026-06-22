"""hosting subscriptions and cloudflare targets

Revision ID: 0017_hosting_subscriptions
Revises: 0016_items_sell_buy
Create Date: 2026-06-05
"""
from __future__ import annotations

import sqlalchemy as sa
from alembic import op
from sqlalchemy.dialects import postgresql

revision = "0017_hosting_subscriptions"
down_revision = "0016_items_sell_buy"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.create_table(
        "hosting_subscriptions",
        sa.Column("subscription_id", postgresql.UUID(as_uuid=True), nullable=False),
        sa.Column("customer_id", postgresql.UUID(as_uuid=True), nullable=False),
        sa.Column("project_id", postgresql.UUID(as_uuid=True), nullable=True),
        sa.Column("template_invoice_id", postgresql.UUID(as_uuid=True), nullable=True),
        sa.Column("item_id", postgresql.UUID(as_uuid=True), nullable=True),
        sa.Column("service_name", sa.String(length=255), nullable=False),
        sa.Column("domain_name", sa.String(length=255), nullable=False),
        sa.Column("currency", sa.String(length=3), nullable=False),
        sa.Column("bundle_months", sa.Integer(), nullable=False),
        sa.Column("payment_terms", sa.String(length=30), nullable=False),
        sa.Column("billing_anchor_date", sa.Date(), nullable=False),
        sa.Column("grace_days", sa.Integer(), nullable=False, server_default="3"),
        sa.Column(
            "suspension_enabled", sa.Boolean(), nullable=False, server_default=sa.true()
        ),
        sa.Column("status", sa.String(length=20), nullable=False, server_default="ACTIVE"),
        sa.Column("last_invoice_id", postgresql.UUID(as_uuid=True), nullable=True),
        sa.Column("last_paid_at", sa.DateTime(timezone=False), nullable=True),
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
        sa.ForeignKeyConstraint(["customer_id"], ["customers.customer_id"]),
        sa.ForeignKeyConstraint(["item_id"], ["items.item_id"]),
        sa.ForeignKeyConstraint(["last_invoice_id"], ["invoices.invoice_id"]),
        sa.ForeignKeyConstraint(["project_id"], ["projects.project_id"]),
        sa.ForeignKeyConstraint(["template_invoice_id"], ["invoices.invoice_id"]),
        sa.PrimaryKeyConstraint("subscription_id"),
    )
    op.create_index(
        "ix_hosting_subscriptions_customer_id",
        "hosting_subscriptions",
        ["customer_id"],
    )
    op.create_index(
        "ix_hosting_subscriptions_item_id",
        "hosting_subscriptions",
        ["item_id"],
    )
    op.create_index(
        "ix_hosting_subscriptions_last_invoice_id",
        "hosting_subscriptions",
        ["last_invoice_id"],
    )
    op.create_index(
        "ix_hosting_subscriptions_project_id",
        "hosting_subscriptions",
        ["project_id"],
    )
    op.create_index(
        "ix_hosting_subscriptions_template_invoice_id",
        "hosting_subscriptions",
        ["template_invoice_id"],
    )

    op.create_table(
        "cloudflare_targets",
        sa.Column("target_id", postgresql.UUID(as_uuid=True), nullable=False),
        sa.Column("subscription_id", postgresql.UUID(as_uuid=True), nullable=False),
        sa.Column("zone_id", sa.String(length=100), nullable=False),
        sa.Column("record_id", sa.String(length=100), nullable=False),
        sa.Column("record_name", sa.String(length=255), nullable=False),
        sa.Column("record_type", sa.String(length=20), nullable=False),
        sa.Column("live_content", sa.Text(), nullable=False),
        sa.Column("maintenance_content", sa.Text(), nullable=False),
        sa.Column("proxied", sa.Boolean(), nullable=False, server_default=sa.true()),
        sa.Column(
            "provider_status", sa.String(length=20), nullable=False, server_default="ACTIVE"
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
            ["subscription_id"],
            ["hosting_subscriptions.subscription_id"],
            ondelete="CASCADE",
        ),
        sa.PrimaryKeyConstraint("target_id"),
        sa.UniqueConstraint("subscription_id"),
    )

    op.add_column(
        "invoices",
        sa.Column("subscription_id", postgresql.UUID(as_uuid=True), nullable=True),
    )
    op.add_column("invoices", sa.Column("coverage_start", sa.Date(), nullable=True))
    op.add_column("invoices", sa.Column("coverage_end", sa.Date(), nullable=True))
    op.create_index("ix_invoices_subscription_id", "invoices", ["subscription_id"])
    op.create_foreign_key(
        "fk_invoices_subscription_id",
        "invoices",
        "hosting_subscriptions",
        ["subscription_id"],
        ["subscription_id"],
    )


def downgrade() -> None:
    op.drop_constraint("fk_invoices_subscription_id", "invoices", type_="foreignkey")
    op.drop_index("ix_invoices_subscription_id", table_name="invoices")
    op.drop_column("invoices", "coverage_end")
    op.drop_column("invoices", "coverage_start")
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
