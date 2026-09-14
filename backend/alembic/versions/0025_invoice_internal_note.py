"""team-only internal note on invoices

Revision ID: 0025_invoice_internal_note
Revises: 0024_customer_contact_single
Create Date: 2026-09-14

Adds ``invoices.internal_note``: free text for the team that is never
rendered on the PDF nor included in customer-facing email. Templates copy
it onto each generated child so recurring context (e.g. who to chase)
travels with the invoice.
"""
from __future__ import annotations

import sqlalchemy as sa
from alembic import op

revision = "0025_invoice_internal_note"
down_revision = "0024_customer_contact_single"
branch_labels = None
depends_on = None


def upgrade() -> None:
    op.add_column("invoices", sa.Column("internal_note", sa.Text(), nullable=True))


def downgrade() -> None:
    op.drop_column("invoices", "internal_note")
