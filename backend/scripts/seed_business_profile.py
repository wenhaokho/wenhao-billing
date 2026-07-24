"""Seed the singleton business profile (Company / invoice header settings).

Fills the Company form shown in Account settings: company name, address,
contact details, invoice title/summary, logo URL, and default notes — all of
which are printed on every generated invoice — plus the per-currency payment
accounts (bank / transfer instructions) keyed by currency, with one marked as
the default fallback.

Idempotent: skips the profile fields if the profile already has a company
name set, and skips any payment-account currency that already exists.

Usage:
    docker compose exec backend python -m scripts.seed_business_profile
"""

from __future__ import annotations

import base64
from pathlib import Path

from app.db.session import SessionLocal
from app.models.business_profile import BusinessProfile
from app.models.payment_account import PaymentAccount

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
    "default_notes": None,
}

# Per-currency payment accounts. Instructions are printed on invoices and
# quotations issued in that currency; exactly one account is is_default=True
# and acts as the fallback when no account matches the invoice currency.
PAYMENT_ACCOUNTS = [
    {
        "currency": "IDR",
        "is_default": True,
        "instructions": (
            "Beneficiary: Kardono Wijaya\n"
            "Bank: Bank Central Asia (BCA)\n"
            "Account Number: 8520198131"
        ),
    },
    # OCBC NISP (SWIFT NISPIDJA) — the international account, used for USD.
    {
        "currency": "USD",
        "is_default": False,
        "instructions": (
            "Beneficiary: Kardono Wijaya\n"
            "Bank: OCBC NISP\n"
            "Account Number: 090810625088\n"
            "SWIFT/BIC: NISPIDJA"
        ),
    },
    {
        "currency": "SGD",
        "is_default": False,
        "instructions": (
            "Beneficiary: Kardono Wijaya\n"
            "Bank: OCBC\n"
            "Account Number: 602881245001\n"
            "PayNow: +6282121552288"
        ),
    },
]


def _seed_payment_accounts(db) -> None:
    existing = {
        pa.currency
        for pa in db.query(PaymentAccount).filter_by(business_profile_id=1)
    }
    added: list[str] = []
    for acc in PAYMENT_ACCOUNTS:
        if acc["currency"] in existing:
            continue
        db.add(PaymentAccount(business_profile_id=1, **acc))
        added.append(acc["currency"])

    if not added:
        print(f"skipping payment accounts: {len(existing)} already present")
        return
    db.commit()
    print(f"seeded payment account(s): {', '.join(added)}")


def seed() -> None:
    with SessionLocal() as db:
        profile = db.get(BusinessProfile, 1)
        if profile is not None and profile.name:
            print(f"skipping: business profile already set ('{profile.name}')")
        else:
            if profile is None:
                profile = BusinessProfile(id=1)
                db.add(profile)
            for field, value in PROFILE.items():
                setattr(profile, field, value)
            db.commit()
            print(f"business profile seeded ('{PROFILE['name']}')")

        _seed_payment_accounts(db)


if __name__ == "__main__":
    seed()
