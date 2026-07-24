"""single source of truth for customer primary contact name

Revision ID: 0024_customer_contact_single
Revises: 0023_country_full_name
Create Date: 2026-07-25

``customers.contact_name`` was kept as a legacy compatibility field by
0012_customer_extended_fields alongside the structured
``contact_first_name`` / ``contact_last_name`` pair. That left two
writable sources for the same value: the customer edit form writes
first/last, while the MCP ``create_customer`` tool wrote the combined
field. Records created through the tool showed a contact name in the
list/detail views (which fell back to ``contact_name``) but an empty
Primary contact section in the edit form, and saving the form could not
clear the stale legacy value.

This migration backfills any name that only exists in the legacy column
and then drops it, leaving first/last as the single source of truth.

Note: vendors keep their own ``contact_name``; that table only ever had
the one field, so it is already unambiguous and is untouched here.
"""
from __future__ import annotations

import sqlalchemy as sa
from alembic import op

revision = "0024_customer_contact_single"
down_revision = "0023_country_full_name"
branch_labels = None
depends_on = None


def upgrade() -> None:
    # Rescue names that live only in the legacy column. 0012 ran the same
    # split, but rows written through the MCP create_customer tool after
    # that migration never got one.
    op.execute(
        "UPDATE customers "
        "SET contact_first_name = split_part(contact_name, ' ', 1), "
        "    contact_last_name  = NULLIF(regexp_replace(contact_name, '^[^ ]+\\s*', ''), '') "
        "WHERE contact_name IS NOT NULL "
        "  AND contact_first_name IS NULL "
        "  AND contact_last_name IS NULL"
    )
    op.drop_column("customers", "contact_name")


def downgrade() -> None:
    op.add_column(
        "customers", sa.Column("contact_name", sa.String(length=255), nullable=True)
    )
    op.execute(
        "UPDATE customers "
        "SET contact_name = NULLIF(TRIM(BOTH ' ' FROM "
        "    COALESCE(contact_first_name, '') || ' ' || COALESCE(contact_last_name, '')"
        "), '')"
    )
