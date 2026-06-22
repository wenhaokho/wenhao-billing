from uuid import UUID

from fastapi import APIRouter, Depends, HTTPException, Query
from sqlalchemy.orm import Session

from app.api.deps import current_admin
from app.db.session import get_db
from app.models.user import User
from app.schemas.hosting_subscription import (
    HostingSubscriptionCreate,
    HostingSubscriptionOut,
    HostingSubscriptionUpdate,
)
from app.services.hosting_subscriptions import (
    HostingSubscriptionError,
    _require_hosting_template,
    create_hosting_subscription,
    list_hosting_templates,
    project_hosting_subscription,
    update_hosting_subscription,
)

router = APIRouter(prefix="/hosting-subscriptions", tags=["hosting-subscriptions"])


@router.post("", response_model=HostingSubscriptionOut, status_code=201)
def create_subscription(
    payload: HostingSubscriptionCreate,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> dict:
    try:
        template = create_hosting_subscription(db, payload)
    except HostingSubscriptionError as exc:
        raise HTTPException(status_code=400, detail=str(exc))
    db.commit()
    db.refresh(template)
    return project_hosting_subscription(db, template)


@router.get("", response_model=list[HostingSubscriptionOut])
def list_subscriptions(
    customer_id: UUID | None = Query(default=None),
    status: str | None = Query(default=None),
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> list[dict]:
    templates = list_hosting_templates(db, customer_id=customer_id, status=status)
    return [project_hosting_subscription(db, t) for t in templates]


@router.get("/{subscription_id}", response_model=HostingSubscriptionOut)
def get_subscription(
    subscription_id: UUID,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> dict:
    try:
        template = _require_hosting_template(db, subscription_id)
    except HostingSubscriptionError:
        raise HTTPException(status_code=404, detail="subscription not found")
    return project_hosting_subscription(db, template)


@router.patch("/{subscription_id}", response_model=HostingSubscriptionOut)
def patch_subscription(
    subscription_id: UUID,
    payload: HostingSubscriptionUpdate,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> dict:
    try:
        template = update_hosting_subscription(db, subscription_id, payload)
    except HostingSubscriptionError as exc:
        raise HTTPException(status_code=400, detail=str(exc))
    db.commit()
    db.refresh(template)
    return project_hosting_subscription(db, template)
