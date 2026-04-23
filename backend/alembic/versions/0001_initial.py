"""initial schema

Revision ID: 0001_initial
Revises:
Create Date: 2026-04-21

"""
from __future__ import annotations

import sqlalchemy as sa
from alembic import op
from sqlalchemy.dialects import postgresql

revision = "0001_initial"
down_revision = None
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.create_table(
        "chart_of_accounts",
        sa.Column("account_id", sa.Integer, primary_key=True, autoincrement=True),
        sa.Column("code", sa.String(10), nullable=False, unique=True),
        sa.Column("name", sa.String(100), nullable=False),
        sa.Column("type", sa.String(50), nullable=False),
    )

    op.create_table(
        "customers",
        sa.Column("customer_id", postgresql.UUID(as_uuid=True), primary_key=True),
        sa.Column("name", sa.String(255), nullable=False),
        sa.Column(
            "matching_aliases",
            postgresql.JSONB,
            nullable=False,
            server_default=sa.text("'[]'::jsonb"),
        ),
    )

    op.create_table(
        "users",
        sa.Column("user_id", postgresql.UUID(as_uuid=True), primary_key=True),
        sa.Column("email", sa.String(255), nullable=False, unique=True),
        sa.Column("password_hash", sa.String(255), nullable=False),
        sa.Column("role", sa.String(20), nullable=False, server_default="admin"),
        sa.Column(
            "created_at",
            sa.DateTime(timezone=False),
            server_default=sa.func.current_timestamp(),
        ),
    )

    op.create_table(
        "invoices",
        sa.Column("invoice_id", postgresql.UUID(as_uuid=True), primary_key=True),
        sa.Column(
            "customer_id",
            postgresql.UUID(as_uuid=True),
            sa.ForeignKey("customers.customer_id"),
            nullable=False,
        ),
        sa.Column("invoice_type", sa.String(20), nullable=False),
        sa.Column("currency", sa.String(3), nullable=False),
        sa.Column("amount", sa.Numeric(19, 4), nullable=False),
        sa.Column("balance_due", sa.Numeric(19, 4), nullable=False),
        sa.Column("status", sa.String(20), nullable=False),
        sa.Column("billing_cycle_ref", postgresql.JSONB, nullable=True),
        sa.Column("issue_date", sa.Date, nullable=False),
        sa.Column("due_date", sa.Date, nullable=False),
        sa.Column(
            "created_at",
            sa.DateTime(timezone=False),
            server_default=sa.func.current_timestamp(),
        ),
    )
    op.create_index("ix_invoices_status", "invoices", ["status"])
    op.create_index("ix_invoices_customer_id", "invoices", ["customer_id"])

    op.create_table(
        "payments",
        sa.Column("payment_id", postgresql.UUID(as_uuid=True), primary_key=True),
        sa.Column(
            "invoice_id",
            postgresql.UUID(as_uuid=True),
            sa.ForeignKey("invoices.invoice_id"),
            nullable=True,
        ),
        sa.Column(
            "customer_id",
            postgresql.UUID(as_uuid=True),
            sa.ForeignKey("customers.customer_id"),
            nullable=True,
        ),
        sa.Column("amount", sa.Numeric(19, 4), nullable=False),
        sa.Column("currency", sa.String(3), nullable=False),
        sa.Column("payer_name", sa.String(255), nullable=False),
        sa.Column("payer_reference", sa.String(255), nullable=True),
        sa.Column("payment_date", sa.Date, nullable=False),
        sa.Column("intake_source", sa.String(50), nullable=False),
        sa.Column("external_ref", sa.String(255), nullable=True),
        sa.Column("status", sa.String(30), nullable=False),
        sa.Column("adjustment_type", sa.String(30), nullable=False, server_default="NONE"),
        sa.Column("confidence_score", sa.Numeric(5, 2), nullable=True),
        sa.Column(
            "created_at",
            sa.DateTime(timezone=False),
            server_default=sa.func.current_timestamp(),
        ),
        sa.UniqueConstraint("intake_source", "external_ref", name="uq_payments_source_ref"),
    )
    op.create_index("ix_payments_status", "payments", ["status"])

    op.create_table(
        "journal_entries",
        sa.Column("entry_id", postgresql.UUID(as_uuid=True), primary_key=True),
        sa.Column(
            "posted_at",
            sa.DateTime(timezone=False),
            nullable=False,
            server_default=sa.func.current_timestamp(),
        ),
        sa.Column("source_type", sa.String(20), nullable=False),
        sa.Column("source_id", postgresql.UUID(as_uuid=True), nullable=False),
        sa.Column("memo", sa.String(500), nullable=True),
        sa.Column(
            "reversed_by_entry_id",
            postgresql.UUID(as_uuid=True),
            sa.ForeignKey("journal_entries.entry_id"),
            nullable=True,
        ),
    )
    op.create_index("ix_journal_entries_source", "journal_entries", ["source_type", "source_id"])

    op.create_table(
        "journal_lines",
        sa.Column("line_id", postgresql.UUID(as_uuid=True), primary_key=True),
        sa.Column(
            "entry_id",
            postgresql.UUID(as_uuid=True),
            sa.ForeignKey("journal_entries.entry_id"),
            nullable=False,
        ),
        sa.Column(
            "account_id",
            sa.Integer,
            sa.ForeignKey("chart_of_accounts.account_id"),
            nullable=False,
        ),
        sa.Column("debit", sa.Numeric(19, 4), nullable=False, server_default="0"),
        sa.Column("credit", sa.Numeric(19, 4), nullable=False, server_default="0"),
        sa.Column("currency", sa.String(3), nullable=False),
        sa.CheckConstraint(
            "(debit = 0 AND credit > 0) OR (credit = 0 AND debit > 0)",
            name="ck_journal_lines_one_sided",
        ),
    )
    op.create_index("ix_journal_lines_entry_id", "journal_lines", ["entry_id"])

    op.create_table(
        "reconciliation_log",
        sa.Column("log_id", postgresql.UUID(as_uuid=True), primary_key=True),
        sa.Column(
            "payment_id",
            postgresql.UUID(as_uuid=True),
            sa.ForeignKey("payments.payment_id"),
            nullable=False,
        ),
        sa.Column("action", sa.String(30), nullable=False),
        sa.Column(
            "reasons",
            postgresql.ARRAY(sa.Text),
            nullable=False,
            server_default=sa.text("ARRAY[]::text[]"),
        ),
        sa.Column(
            "actor_user_id",
            postgresql.UUID(as_uuid=True),
            sa.ForeignKey("users.user_id"),
            nullable=True,
        ),
        sa.Column(
            "created_at",
            sa.DateTime(timezone=False),
            server_default=sa.func.current_timestamp(),
        ),
    )
    op.create_index(
        "ix_reconciliation_log_payment_id", "reconciliation_log", ["payment_id"]
    )


def downgrade() -> None:
    op.drop_table("reconciliation_log")
    op.drop_table("journal_lines")
    op.drop_table("journal_entries")
    op.drop_table("payments")
    op.drop_table("invoices")
    op.drop_table("users")
    op.drop_table("customers")
    op.drop_table("chart_of_accounts")
