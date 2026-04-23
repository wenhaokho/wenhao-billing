from fastapi import APIRouter, Depends
from sqlalchemy.orm import Session

from app.api.deps import current_admin
from app.db.session import get_db
from app.models.business_profile import BusinessProfile
from app.models.user import User
from app.schemas.business_profile import BusinessProfileOut, BusinessProfileUpdate

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
