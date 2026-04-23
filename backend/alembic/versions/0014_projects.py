"""projects table + project_id FK on invoices/bills

Revision ID: 0014_projects
Revises: 0013_bills_ap
Create Date: 2026-04-22

"""
from __future__ import annotations

import sqlalchemy as sa
from alembic import op
from sqlalchemy.dialects import postgresql

revision = "0014_projects"
down_revision = "0013_bills_ap"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.create_table(
        "projects",
        sa.Column("project_id", postgresql.UUID(as_uuid=True), primary_key=True),
        sa.Column(
            "customer_id",
            postgresql.UUID(as_uuid=True),
            sa.ForeignKey("customers.customer_id"),
            nullable=False,
        ),
        sa.Column("code", sa.String(40), nullable=False, unique=True),
        sa.Column("name", sa.String(255), nullable=False),
        sa.Column("currency", sa.String(3), nullable=False),
        sa.Column("contract_value", sa.Numeric(19, 4), nullable=True),
        sa.Column(
            "status",
            sa.String(20),
            nullable=False,
            server_default=sa.text("'ACTIVE'"),
        ),
        sa.Column("start_date", sa.Date(), nullable=True),
        sa.Column("end_date", sa.Date(), nullable=True),
        sa.Column("notes", sa.Text(), nullable=True),
        sa.Column(
            "active",
            sa.Boolean(),
            nullable=False,
            server_default=sa.text("true"),
        ),
        sa.Column(
            "created_at",
            sa.DateTime(timezone=False),
            nullable=False,
            server_default=sa.text("CURRENT_TIMESTAMP"),
        ),
    )
    op.create_index("ix_projects_customer_id", "projects", ["customer_id"])
    op.create_index("ix_projects_code", "projects", ["code"], unique=True)

    op.add_column(
        "invoices",
        sa.Column(
            "project_id",
            postgresql.UUID(as_uuid=True),
            sa.ForeignKey("projects.project_id"),
            nullable=True,
        ),
    )
    op.create_index("ix_invoices_project_id", "invoices", ["project_id"])

    op.add_column(
        "bills",
        sa.Column(
            "project_id",
            postgresql.UUID(as_uuid=True),
            sa.ForeignKey("projects.project_id"),
            nullable=True,
        ),
    )
    op.create_index("ix_bills_project_id", "bills", ["project_id"])


def downgrade() -> None:
    op.drop_index("ix_bills_project_id", table_name="bills")
    op.drop_column("bills", "project_id")
    op.drop_index("ix_invoices_project_id", table_name="invoices")
    op.drop_column("invoices", "project_id")
    op.drop_index("ix_projects_code", table_name="projects")
    op.drop_index("ix_projects_customer_id", table_name="projects")
    op.drop_table("projects")
