import pytest
from sqlalchemy.exc import IntegrityError

from app.models.business_profile import BusinessProfile
from app.models.payment_account import PaymentAccount


def test_relationship_orders_and_reads_accounts(db):
    profile = db.get(BusinessProfile, 1)
    assert profile is not None  # migration 0010 seeds id=1
    db.add_all([
        PaymentAccount(business_profile_id=1, currency="USD",
                       instructions="Wells Fargo USD", is_default=False),
        PaymentAccount(business_profile_id=1, currency="IDR",
                       instructions="OCBC NISP IDR", is_default=True),
    ])
    db.flush()
    db.refresh(profile)
    accounts = profile.payment_accounts
    assert [a.currency for a in accounts] == ["IDR", "USD"]  # ordered by currency
    assert [a.is_default for a in accounts] == [True, False]


def test_duplicate_currency_rejected(db):
    db.add(PaymentAccount(business_profile_id=1, currency="SGD",
                          instructions="DBS SGD", is_default=False))
    db.flush()
    db.add(PaymentAccount(business_profile_id=1, currency="SGD",
                          instructions="UOB SGD", is_default=False))
    with pytest.raises(IntegrityError):
        db.flush()


def test_second_default_rejected(db):
    db.add(PaymentAccount(business_profile_id=1, currency="SGD",
                          instructions="DBS SGD", is_default=True))
    db.flush()
    db.add(PaymentAccount(business_profile_id=1, currency="USD",
                          instructions="Wells USD", is_default=True))
    with pytest.raises(IntegrityError):
        db.flush()
