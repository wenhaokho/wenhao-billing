from uuid import UUID

from fastapi import APIRouter, Depends, HTTPException, status
from passlib.context import CryptContext
from sqlalchemy import select
from sqlalchemy.orm import Session

from app.api.deps import current_admin
from app.db.session import get_db
from app.models.user import User
from app.schemas.auth import ProfileUpdate
from app.schemas.user import UserCreate, UserOut, UserPasswordReset

router = APIRouter(prefix="/users", tags=["users"])
_pwd = CryptContext(schemes=["bcrypt"], deprecated="auto")


@router.get("", response_model=list[UserOut])
def list_users(
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> list[User]:
    return list(db.scalars(select(User).order_by(User.email.asc())))


@router.patch("/me", response_model=UserOut)
def update_me(
    payload: ProfileUpdate,
    db: Session = Depends(get_db),
    user: User = Depends(current_admin),
) -> User:
    """Self-service profile edit. Does NOT change password — use /auth/change-password."""
    if payload.email is not None and payload.email != user.email:
        # Unique check so the commit doesn't 500 on a unique-violation.
        clash = db.scalar(select(User).where(User.email == payload.email))
        if clash is not None and clash.user_id != user.user_id:
            raise HTTPException(
                status_code=status.HTTP_409_CONFLICT,
                detail="email already in use",
            )
        user.email = payload.email
    if payload.display_name is not None:
        user.display_name = payload.display_name or None
    db.commit()
    db.refresh(user)
    return user


@router.post("", response_model=UserOut, status_code=201)
def create_user(
    payload: UserCreate,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> User:
    if db.scalar(select(User).where(User.email == payload.email)):
        raise HTTPException(status_code=status.HTTP_409_CONFLICT, detail="email already exists")
    user = User(
        email=payload.email,
        password_hash=_pwd.hash(payload.password),
        role=payload.role,
        display_name=payload.display_name,
    )
    db.add(user)
    db.commit()
    db.refresh(user)
    return user


@router.post("/{user_id}/reset-password", response_model=UserOut)
def admin_reset_password(
    user_id: UUID,
    payload: UserPasswordReset,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> User:
    user = db.get(User, user_id)
    if user is None:
        raise HTTPException(status_code=404, detail="user not found")
    user.password_hash = _pwd.hash(payload.password)
    user.password_reset_token = None
    user.password_reset_expires = None
    db.commit()
    db.refresh(user)
    return user
