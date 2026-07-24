from fastapi import APIRouter, Depends, HTTPException
from sqlalchemy import delete, select
from sqlalchemy.orm import Session

from app.api.deps import current_admin
from app.db.session import get_db
from app.models.business_profile import BusinessProfile
from app.models.payment_account import PaymentAccount
from app.models.user import User
from app.schemas.business_profile import (
    ALLOWED_CURRENCIES,
    BusinessProfileOut,
    BusinessProfileUpdate,
    PaymentAccountIn,
    PaymentAccountOut,
)

router = APIRouter(prefix="/business-profile", tags=["business-profile"])


def _get_or_create(db: Session) -> BusinessProfile:
    profile = db.get(BusinessProfile, 1)
    if profile is None:
        profile = BusinessProfile(id=1)
        db.add(profile)
        db.flush()
    return profile


@router.get("", response_model=BusinessProfileOut)
def get_profile(
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> BusinessProfile:
    return _get_or_create(db)


@router.put("", response_model=BusinessProfileOut)
def update_profile(
    payload: BusinessProfileUpdate,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> BusinessProfile:
    profile = _get_or_create(db)
    for field, value in payload.model_dump(exclude_unset=True).items():
        setattr(profile, field, value)
    db.commit()
    db.refresh(profile)
    return profile


def _list_accounts(db: Session) -> list[PaymentAccount]:
    return list(
        db.scalars(
            select(PaymentAccount)
            .where(PaymentAccount.business_profile_id == 1)
            .order_by(PaymentAccount.currency)
        )
    )


@router.get("/payment-accounts", response_model=list[PaymentAccountOut])
def list_payment_accounts(
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> list[PaymentAccount]:
    _get_or_create(db)
    return _list_accounts(db)


@router.put("/payment-accounts", response_model=list[PaymentAccountOut])
def replace_payment_accounts(
    payload: list[PaymentAccountIn],
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> list[PaymentAccount]:
    currencies = [a.currency.upper() for a in payload]
    if len(set(currencies)) != len(currencies):
        raise HTTPException(422, "Duplicate currency in payment accounts")
    unsupported = sorted({c for c in currencies if c not in ALLOWED_CURRENCIES})
    if unsupported:
        raise HTTPException(422, f"Unsupported currency: {', '.join(unsupported)}")
    if sum(1 for a in payload if a.is_default) > 1:
        raise HTTPException(422, "At most one account can be the default")

    _get_or_create(db)
    db.execute(delete(PaymentAccount).where(PaymentAccount.business_profile_id == 1))
    db.flush()
    for a in payload:
        db.add(
            PaymentAccount(
                business_profile_id=1,
                currency=a.currency.upper(),
                instructions=a.instructions,
                is_default=a.is_default,
            )
        )
    db.commit()
    return _list_accounts(db)
