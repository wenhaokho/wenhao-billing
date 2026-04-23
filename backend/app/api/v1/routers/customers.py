from datetime import date, timedelta
from decimal import Decimal
from uuid import UUID

from fastapi import APIRouter, Depends, HTTPException
from pydantic import BaseModel
from sqlalchemy import select
from sqlalchemy.orm import Session

from app.api.deps import current_admin
from app.db.session import get_db
from app.models.customer import Customer
from app.models.invoice import Invoice
from app.models.payment import Payment
from app.models.user import User
from app.schemas.customer import CustomerCreate, CustomerOut, CustomerUpdate
from app.schemas.invoice import InvoiceOut
from app.services.stats import compute_customer_balances

router = APIRouter(prefix="/customers", tags=["customers"])


class CurrencyAmount(BaseModel):
    currency: str
    amount: Decimal


class BillingSummary(BaseModel):
    milestone: list[InvoiceOut]
    recurring: list[InvoiceOut]
    usage: list[InvoiceOut]
    unpaid_invoices: list[InvoiceOut]
    total_unpaid: list[CurrencyAmount]
    paid_last_12_months: list[CurrencyAmount]
    last_invoice_issue_date: date | None
    last_invoice_id: UUID | None


@router.get("", response_model=list[CustomerOut])
def list_customers(
    active: bool | None = None,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> list[Customer]:
    stmt = select(Customer).order_by(Customer.name.asc())
    if active is not None:
        stmt = stmt.where(Customer.active == active)
    return list(db.scalars(stmt))


class CustomerBalanceRow(BaseModel):
    customer_id: UUID
    currency: str
    balance: Decimal
    overdue: Decimal


@router.get("/balances", response_model=list[CustomerBalanceRow])
def customer_balances(
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> list[CustomerBalanceRow]:
    """Open AR per customer with overdue split out (Wave-parity list column)."""
    rows = compute_customer_balances(db, today=date.today())
    return [
        CustomerBalanceRow(
            customer_id=r.customer_id,
            currency=r.currency,
            balance=r.balance,
            overdue=r.overdue,
        )
        for r in rows
    ]


@router.post("", response_model=CustomerOut, status_code=201)
def create_customer(
    payload: CustomerCreate,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> Customer:
    data = payload.model_dump(exclude_unset=True)
    customer = Customer(**data)
    db.add(customer)
    db.commit()
    db.refresh(customer)
    return customer


@router.get("/{customer_id}", response_model=CustomerOut)
def get_customer(
    customer_id: UUID,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> Customer:
    customer = db.get(Customer, customer_id)
    if customer is None:
        raise HTTPException(status_code=404, detail="customer not found")
    return customer


@router.patch("/{customer_id}", response_model=CustomerOut)
def update_customer(
    customer_id: UUID,
    payload: CustomerUpdate,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> Customer:
    customer = db.get(Customer, customer_id)
    if customer is None:
        raise HTTPException(status_code=404, detail="customer not found")
    for k, v in payload.model_dump(exclude_unset=True).items():
        setattr(customer, k, v)
    db.commit()
    db.refresh(customer)
    return customer


@router.delete("/{customer_id}", response_model=CustomerOut)
def deactivate_customer(
    customer_id: UUID,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> Customer:
    customer = db.get(Customer, customer_id)
    if customer is None:
        raise HTTPException(status_code=404, detail="customer not found")
    customer.active = False
    db.commit()
    db.refresh(customer)
    return customer


def _sum_by_currency(pairs: list[tuple[str, Decimal]]) -> list[CurrencyAmount]:
    totals: dict[str, Decimal] = {}
    for currency, amount in pairs:
        totals[currency] = totals.get(currency, Decimal(0)) + amount
    return [
        CurrencyAmount(currency=c, amount=a)
        for c, a in sorted(totals.items())
    ]


@router.get("/{customer_id}/billing-summary", response_model=BillingSummary)
def billing_summary(
    customer_id: UUID,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> BillingSummary:
    customer = db.get(Customer, customer_id)
    if customer is None:
        raise HTTPException(status_code=404, detail="customer not found")

    open_invoices = list(
        db.scalars(
            select(Invoice)
            .where(Invoice.customer_id == customer_id)
            .where(Invoice.status.in_(("DRAFT", "SENT", "PARTIAL")))
            .order_by(Invoice.due_date.asc())
        )
    )
    latest_invoice = db.scalars(
        select(Invoice)
        .where(Invoice.customer_id == customer_id)
        .order_by(Invoice.issue_date.desc(), Invoice.created_at.desc())
        .limit(1)
    ).first()

    twelve_months_ago = date.today() - timedelta(days=365)
    payments_last_year = list(
        db.scalars(
            select(Payment)
            .where(Payment.customer_id == customer_id)
            .where(Payment.status == "CLEARED")
            .where(Payment.payment_date >= twelve_months_ago)
        )
    )

    return BillingSummary(
        milestone=[
            InvoiceOut.model_validate(r) for r in open_invoices if r.invoice_type == "MILESTONE"
        ],
        recurring=[
            InvoiceOut.model_validate(r) for r in open_invoices if r.invoice_type == "RECURRING"
        ],
        usage=[
            InvoiceOut.model_validate(r) for r in open_invoices if r.invoice_type == "USAGE"
        ],
        unpaid_invoices=[InvoiceOut.model_validate(r) for r in open_invoices],
        total_unpaid=_sum_by_currency(
            [(inv.currency, inv.balance_due) for inv in open_invoices]
        ),
        paid_last_12_months=_sum_by_currency(
            [(p.currency, p.amount) for p in payments_last_year]
        ),
        last_invoice_issue_date=latest_invoice.issue_date if latest_invoice else None,
        last_invoice_id=latest_invoice.invoice_id if latest_invoice else None,
    )


@router.get("/{customer_id}/invoices", response_model=list[InvoiceOut])
def list_customer_invoices(
    customer_id: UUID,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> list[Invoice]:
    customer = db.get(Customer, customer_id)
    if customer is None:
        raise HTTPException(status_code=404, detail="customer not found")
    return list(
        db.scalars(
            select(Invoice)
            .where(Invoice.customer_id == customer_id)
            .order_by(Invoice.issue_date.desc(), Invoice.created_at.desc())
        )
    )
