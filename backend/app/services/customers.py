"""Customer create/update — extracted from routers/customers.py so the MCP
write tools (write_customers.py) can call the exact same write logic
without duplicating it in the tool layer.

Behavior preserved exactly: model_dump(exclude_unset=True) then a plain
setattr loop — no extra validation beyond what CustomerCreate/CustomerUpdate
already enforce via Pydantic.
"""
from __future__ import annotations

from uuid import UUID

from sqlalchemy.orm import Session

from app.models.customer import Customer
from app.schemas.customer import CustomerCreate, CustomerUpdate


class CustomerError(Exception):
    """Raised on domain errors in the customer layer (e.g. not found)."""


def create_customer(db: Session, payload: CustomerCreate) -> Customer:
    data = payload.model_dump(exclude_unset=True)
    customer = Customer(**data)
    db.add(customer)
    db.flush()
    return customer


def update_customer(db: Session, customer_id: UUID, payload: CustomerUpdate) -> Customer:
    customer = db.get(Customer, customer_id)
    if customer is None:
        raise CustomerError(f"customer {customer_id} not found")
    for k, v in payload.model_dump(exclude_unset=True).items():
        setattr(customer, k, v)
    db.flush()
    return customer
