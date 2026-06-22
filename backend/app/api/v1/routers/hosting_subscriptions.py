from uuid import UUID

from fastapi import APIRouter, Depends, HTTPException, Query
from sqlalchemy import select
from sqlalchemy.orm import Session

from app.api.deps import current_admin
from app.db.session import get_db
from app.models.hosting_subscription import HostingSubscription
from app.models.user import User
from app.schemas.hosting_subscription import (
    HostingSubscriptionCreate,
    HostingSubscriptionOut,
    HostingSubscriptionUpdate,
)
from app.services.hosting_subscriptions import (
    HostingSubscriptionError,
    create_hosting_subscription,
    update_hosting_subscription,
)

router = APIRouter(prefix="/hosting-subscriptions", tags=["hosting-subscriptions"])


@router.post("", response_model=HostingSubscriptionOut, status_code=201)
def create_subscription(
    payload: HostingSubscriptionCreate,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> HostingSubscription:
    try:
        subscription = create_hosting_subscription(db, payload)
    except HostingSubscriptionError as exc:
        raise HTTPException(status_code=400, detail=str(exc))
    db.commit()
    db.refresh(subscription)
    return subscription


@router.get("", response_model=list[HostingSubscriptionOut])
def list_subscriptions(
    customer_id: UUID | None = Query(default=None),
    status: str | None = Query(default=None),
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> list[HostingSubscription]:
    stmt = select(HostingSubscription).order_by(HostingSubscription.created_at.desc())
    if customer_id is not None:
        stmt = stmt.where(HostingSubscription.customer_id == customer_id)
    if status is not None:
        stmt = stmt.where(HostingSubscription.status == status.upper())
    return list(db.scalars(stmt))


@router.get("/{subscription_id}", response_model=HostingSubscriptionOut)
def get_subscription(
    subscription_id: UUID,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> HostingSubscription:
    subscription = db.get(HostingSubscription, subscription_id)
    if subscription is None:
        raise HTTPException(status_code=404, detail="subscription not found")
    return subscription


@router.patch("/{subscription_id}", response_model=HostingSubscriptionOut)
def patch_subscription(
    subscription_id: UUID,
    payload: HostingSubscriptionUpdate,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> HostingSubscription:
    try:
        subscription = update_hosting_subscription(db, subscription_id, payload)
    except HostingSubscriptionError as exc:
        raise HTTPException(status_code=400, detail=str(exc))
    db.commit()
    db.refresh(subscription)
    return subscription
