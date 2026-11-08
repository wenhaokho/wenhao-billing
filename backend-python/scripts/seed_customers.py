"""Seed real customer records (idempotent by name).

Skips any customer whose exact name already exists, so it is safe to re-run
and safe to run alongside other seed scripts.

Usage:
    docker compose exec backend python -m scripts.seed_customers
"""

from __future__ import annotations

import uuid

from sqlalchemy import select

from app.db.session import SessionLocal
from app.models.customer import Customer

# Each dict maps 1:1 to Customer columns. Indonesian customers default to IDR.
CUSTOMERS: list[dict] = [
    {
        "name": "SMA Maha bodhi",
        "matching_aliases": ["SMA Maha Bodhi", "Maha Bodhi"],
        "contact_first_name": "Jurman",
        "contact_email": "Jurman_pru@yahoo.com",
        "contact_phone": "+6285264513513",
        "default_currency": "IDR",
        "billing_address1": "Jl. Bhakti, Tlk. Air",
        "billing_address2": "Kec. Karimun",
        "billing_city": "Kabupaten Karimun",
        "billing_state": "Kepulauan Riau",
        "billing_postal_code": "29661",
        "billing_country": "Indonesia",
    },
    {
        "name": "PT SINDO MAKMUR SENTOSA",
        "matching_aliases": ["Sindo Makmur Sentosa", "PT Sindo", "Sindo"],
        "contact_first_name": "Hendro",
        "contact_last_name": "Liaw",
        "contact_email": "hendro.liauw@gmail.com",
        "contact_phone": "+62811778089",
        "default_currency": "IDR",
        "billing_address1": "Komp. Hijrah Karya Mandiri Blok F No. 5",
        "billing_city": "Batam",
        "billing_state": "Kepulauan Riau",
        "billing_country": "Indonesia",
    },
]


def seed() -> None:
    with SessionLocal() as db:
        created = 0
        skipped = 0
        for data in CUSTOMERS:
            exists = db.scalar(
                select(Customer.customer_id).where(Customer.name == data["name"])
            )
            if exists:
                print(f"  skip (already exists): {data['name']}")
                skipped += 1
                continue
            db.add(Customer(customer_id=uuid.uuid4(), **data))
            print(f"  add: {data['name']}")
            created += 1

        db.commit()
        print(f"done — created {created}, skipped {skipped}")


if __name__ == "__main__":
    seed()
