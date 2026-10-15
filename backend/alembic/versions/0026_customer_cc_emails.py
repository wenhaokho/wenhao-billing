"""default CC recipients on customers

Revision ID: 0026_customer_cc_emails
Revises: 0025_invoice_internal_note
Create Date: 2026-10-15

Adds ``customers.quotation_cc_emails`` and ``customers.billing_cc_emails``
(JSONB lists). The send dialogs prefill CC from them: quotations use the
quotation list, invoices and payment receipts use the billing list.
"""
from __future__ import annotations

import sqlalchemy as sa
from sqlalchemy.dialects import postgresql

from alembic import op

revision = "0026_customer_cc_emails"
down_revision = "0025_invoice_internal_note"
branch_labels = None
depends_on = None


def upgrade() -> None:
    for col in ("quotation_cc_emails", "billing_cc_emails"):
        op.add_column(
            "customers",
            sa.Column(col, postgresql.JSONB(), nullable=False, server_default="[]"),
        )


def downgrade() -> None:
    op.drop_column("customers", "billing_cc_emails")
    op.drop_column("customers", "quotation_cc_emails")
