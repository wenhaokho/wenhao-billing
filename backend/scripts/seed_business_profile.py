"""Seed the singleton business profile (Company / invoice header settings).

Fills the Company form shown in Account settings: company name, address,
contact details, invoice title/summary, logo URL, default notes, and
payment instructions — all of which are printed on every generated invoice.

Idempotent: skips if the profile already has a company name set.

Usage:
    docker compose exec backend python -m scripts.seed_business_profile
"""

from __future__ import annotations

import base64
from pathlib import Path

from app.db.session import SessionLocal
from app.models.business_profile import BusinessProfile

LOGO_PATH = Path(__file__).parent / "assets" / "business_logo.png"


def _logo_data_uri() -> str:
    """Encode the bundled logo PNG as a base64 data URI, or '' if absent.

    Mirrors how the Account settings upload stores the logo, so the seeded
    value renders identically in the UI preview.
    """
    if not LOGO_PATH.is_file():
        return ""
    encoded = base64.b64encode(LOGO_PATH.read_bytes()).decode("ascii")
    return f"data:image/png;base64,{encoded}"


PROFILE = {
    "name": "Wenhao Development",
    "address": "Lily Garden Residence, Bromelia No. 22",
    "contact_email": "wenhao.kho@gmail.com",
    "contact_phone": "+6282121552288",
    "invoice_title": "Invoice",
    "invoice_summary": "Software development & digital solutions.",
    "logo_url": _logo_data_uri(),
    "default_notes": "Thank you for your business.",
    "payment_instructions": (
        "Acc Name: KARDONO WIJAYA\n"
        "Bank: OCBC\n"
        "Account number: 090810625088\n"
        "SWIFT code: NISPIDJA"
    ),
}


def seed() -> None:
    with SessionLocal() as db:
        profile = db.get(BusinessProfile, 1)
        if profile is not None and profile.name:
            print(f"skipping: business profile already set ('{profile.name}')")
            return

        if profile is None:
            profile = BusinessProfile(id=1)
            db.add(profile)

        for field, value in PROFILE.items():
            setattr(profile, field, value)

        db.commit()
        print(f"business profile seeded ('{PROFILE['name']}')")


if __name__ == "__main__":
    seed()
