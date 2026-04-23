from decimal import Decimal
from uuid import UUID

from fastapi import APIRouter, Depends, HTTPException, Query
from sqlalchemy import func, select
from sqlalchemy.exc import IntegrityError
from sqlalchemy.orm import Session

from app.api.deps import current_admin
from app.db.session import get_db
from app.models.bill import Bill
from app.models.invoice import Invoice
from app.models.project import Project
from app.models.user import User
from app.schemas.invoice import InvoiceOut
from app.schemas.project import (
    ProjectCreate,
    ProjectOut,
    ProjectSummary,
    ProjectUpdate,
)

router = APIRouter(prefix="/projects", tags=["projects"])


def _load(db: Session, project_id: UUID) -> Project:
    project = db.get(Project, project_id)
    if project is None:
        raise HTTPException(status_code=404, detail="project not found")
    return project


@router.post("", response_model=ProjectOut, status_code=201)
def create_project(
    payload: ProjectCreate,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> Project:
    project = Project(
        customer_id=payload.customer_id,
        code=payload.code.strip().upper(),
        name=payload.name.strip(),
        currency=payload.currency.upper(),
        contract_value=payload.contract_value,
        status=payload.status,
        start_date=payload.start_date,
        end_date=payload.end_date,
        notes=payload.notes,
    )
    db.add(project)
    try:
        db.commit()
    except IntegrityError:
        db.rollback()
        raise HTTPException(
            status_code=400, detail=f"project code {project.code!r} already exists"
        )
    db.refresh(project)
    return project


@router.get("", response_model=list[ProjectOut])
def list_projects(
    customer_id: UUID | None = Query(default=None),
    status: str | None = Query(default=None),
    active: bool | None = Query(default=None),
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> list[Project]:
    stmt = select(Project).order_by(Project.created_at.desc())
    if customer_id:
        stmt = stmt.where(Project.customer_id == customer_id)
    if status:
        stmt = stmt.where(Project.status == status)
    if active is not None:
        stmt = stmt.where(Project.active.is_(active))
    return list(db.scalars(stmt))


@router.get("/{project_id}", response_model=ProjectOut)
def get_project(
    project_id: UUID,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> Project:
    return _load(db, project_id)


@router.patch("/{project_id}", response_model=ProjectOut)
def update_project(
    project_id: UUID,
    payload: ProjectUpdate,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> Project:
    project = _load(db, project_id)
    data = payload.model_dump(exclude_unset=True)
    if "code" in data and data["code"] is not None:
        data["code"] = data["code"].strip().upper()
    if "currency" in data and data["currency"] is not None:
        data["currency"] = data["currency"].upper()
    for k, v in data.items():
        setattr(project, k, v)
    try:
        db.commit()
    except IntegrityError:
        db.rollback()
        raise HTTPException(status_code=400, detail="project code already exists")
    db.refresh(project)
    return project


@router.delete("/{project_id}", status_code=204)
def delete_project(
    project_id: UUID,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> None:
    project = _load(db, project_id)
    # Refuse delete if any invoice or bill references it — surface that to the UI
    ref_count = db.scalar(
        select(func.count()).select_from(Invoice).where(Invoice.project_id == project_id)
    ) or 0
    ref_count += db.scalar(
        select(func.count()).select_from(Bill).where(Bill.project_id == project_id)
    ) or 0
    if ref_count > 0:
        raise HTTPException(
            status_code=400,
            detail=f"cannot delete: project is referenced by {ref_count} invoice(s)/bill(s)",
        )
    db.delete(project)
    db.commit()


@router.get("/{project_id}/summary", response_model=ProjectSummary)
def project_summary(
    project_id: UUID,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> ProjectSummary:
    project = _load(db, project_id)

    # Only count invoices that have been issued (not drafts/voids).
    billed_statuses = ("SENT", "PARTIAL", "PAID")
    invoice_stats = db.execute(
        select(
            func.coalesce(func.sum(Invoice.amount), 0),
            func.coalesce(func.sum(Invoice.balance_due), 0),
            func.count(Invoice.invoice_id),
        )
        .where(Invoice.project_id == project_id)
        .where(Invoice.status.in_(billed_statuses))
    ).one()
    invoiced_total = Decimal(invoice_stats[0])
    outstanding_total = Decimal(invoice_stats[1])
    paid_total = invoiced_total - outstanding_total
    invoice_count = int(invoice_stats[2])

    bill_stats = db.execute(
        select(func.coalesce(func.sum(Bill.amount), 0))
        .where(Bill.project_id == project_id)
        .where(Bill.status.in_(("OPEN", "PARTIAL", "PAID")))
    ).one()
    bill_total = Decimal(bill_stats[0])

    cv = Decimal(project.contract_value) if project.contract_value is not None else None
    contract_remaining = (cv - invoiced_total) if cv is not None else None

    return ProjectSummary(
        project_id=project.project_id,
        invoiced_total=invoiced_total,
        paid_total=paid_total,
        outstanding_total=outstanding_total,
        contract_value=cv,
        contract_remaining=contract_remaining,
        invoice_count=invoice_count,
        bill_total=bill_total,
    )


@router.get("/{project_id}/invoices", response_model=list[InvoiceOut])
def project_invoices(
    project_id: UUID,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> list[Invoice]:
    _load(db, project_id)
    return list(
        db.scalars(
            select(Invoice)
            .where(Invoice.project_id == project_id)
            .where(Invoice.is_template.is_(False))
            .order_by(Invoice.created_at.desc())
        )
    )
