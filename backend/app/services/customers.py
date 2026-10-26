"""Customer create/update — extracted from routers/customers.py so the MCP
write tools (write_customers.py) can call the exact same write logic
without duplicating it in the tool layer.

model_dump(exclude_unset=True) then a plain setattr loop. The default
withholding-tax fields are additionally normalised via services.withholding
(raises ValueError on an unknown type or an out-of-range rate).
"""
from __future__ import annotations

from uuid import UUID

from sqlalchemy.orm import Session

from app.models.customer import Customer
from app.schemas.customer import CustomerCreate, CustomerUpdate
from app.services import withholding


class CustomerError(Exception):
    """Raised on domain errors in the customer layer (e.g. not found)."""


_WHT_FIELDS = ("default_wht_type", "default_wht_rate", "default_wht_gross_up")


def _normalize_wht(data: dict, stored: tuple[str, object, bool]) -> None:
    """Overlay supplied wht fields on ``stored`` and write the normalised result into ``data``."""
    if not any(f in data for f in _WHT_FIELDS):
        return
    wht_type = data.get("default_wht_type")
    if "default_wht_rate" in data:
        rate = data["default_wht_rate"]
    elif wht_type is not None and wht_type != stored[0]:
        rate = None  # type switch without a rate: use the new type's default
    else:
        rate = stored[1]
    gross_up = data.get("default_wht_gross_up")
    wht_type, rate, gross_up = withholding.normalize(
        stored[0] if wht_type is None else wht_type,
        rate,
        stored[2] if gross_up is None else gross_up,
    )
    data["default_wht_type"] = wht_type
    data["default_wht_rate"] = rate
    data["default_wht_gross_up"] = gross_up


def create_customer(db: Session, payload: CustomerCreate) -> Customer:
    data = payload.model_dump(exclude_unset=True)
    _normalize_wht(data, ("NONE", None, False))
    customer = Customer(**data)
    db.add(customer)
    db.flush()
    return customer


def update_customer(db: Session, customer_id: UUID, payload: CustomerUpdate) -> Customer:
    customer = db.get(Customer, customer_id)
    if customer is None:
        raise CustomerError(f"customer {customer_id} not found")
    data = payload.model_dump(exclude_unset=True)
    _normalize_wht(
        data,
        (customer.default_wht_type, customer.default_wht_rate, customer.default_wht_gross_up),
    )
    for k, v in data.items():
        setattr(customer, k, v)
    db.flush()
    return customer
