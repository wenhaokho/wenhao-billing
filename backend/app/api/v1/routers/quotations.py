from uuid import UUID

from fastapi import APIRouter, Depends, HTTPException, Query
from fastapi.responses import Response
from sqlalchemy import select
from sqlalchemy.orm import Session

from app.api.deps import current_admin
from app.db.session import get_db
from app.models.customer import Customer
from app.models.invoice import Invoice
from app.models.quotation import Quotation
from app.models.user import User
from app.schemas.invoice import InvoiceOut
from app.schemas.quotation import (
    AcceptQuotationRequest,
    ConvertToInvoiceRequest,
    QuotationCreate,
    QuotationOut,
    QuotationUpdate,
    SendQuotationRequest,
)
from app.services import quoting
from app.services.email import send_email
from app.services.quotation_pdf import render_quotation_pdf

router = APIRouter(prefix="/quotations", tags=["quotations"])


@router.post("", response_model=QuotationOut, status_code=201)
def create(
    payload: QuotationCreate,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> Quotation:
    try:
        q = quoting.create_quotation(db, payload)
    except quoting.QuotingError as e:
        raise HTTPException(status_code=400, detail=str(e))
    db.commit()
    db.refresh(q)
    return q


@router.get("", response_model=list[QuotationOut])
def list_quotations(
    status: str | None = Query(default=None),
    customer_id: UUID | None = Query(default=None),
    project_id: UUID | None = Query(default=None),
    limit: int = Query(default=200, le=500),
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> list[Quotation]:
    stmt = select(Quotation).order_by(Quotation.created_at.desc()).limit(limit)
    if status:
        stmt = stmt.where(Quotation.status == status)
    if customer_id:
        stmt = stmt.where(Quotation.customer_id == customer_id)
    if project_id:
        stmt = stmt.where(Quotation.project_id == project_id)
    return list(db.scalars(stmt))


@router.get("/{quotation_id}", response_model=QuotationOut)
def get_one(
    quotation_id: UUID,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> Quotation:
    q = db.get(Quotation, quotation_id)
    if q is None:
        raise HTTPException(status_code=404, detail="quotation not found")
    return q


@router.patch("/{quotation_id}", response_model=QuotationOut)
def update(
    quotation_id: UUID,
    payload: QuotationUpdate,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> Quotation:
    try:
        q = quoting.update_quotation(db, quotation_id, payload)
    except quoting.QuotingError as e:
        raise HTTPException(status_code=400, detail=str(e))
    db.commit()
    db.refresh(q)
    return q


@router.delete("/{quotation_id}", status_code=204)
def delete(
    quotation_id: UUID,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> None:
    try:
        quoting.delete_quotation(db, quotation_id)
    except quoting.QuotingError as e:
        raise HTTPException(status_code=400, detail=str(e))
    db.commit()


@router.post("/{quotation_id}/mark-sent", response_model=QuotationOut)
def mark_sent(
    quotation_id: UUID,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> Quotation:
    try:
        q = quoting.mark_sent(db, quotation_id)
    except quoting.QuotingError as e:
        raise HTTPException(status_code=400, detail=str(e))
    db.commit()
    return q


@router.post("/{quotation_id}/accept", response_model=QuotationOut)
def accept(
    quotation_id: UUID,
    payload: AcceptQuotationRequest,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> Quotation:
    try:
        q = quoting.accept_quotation(db, quotation_id, accepted_by=payload.accepted_by)
    except quoting.QuotingError as e:
        raise HTTPException(status_code=400, detail=str(e))
    db.commit()
    return q


@router.post("/{quotation_id}/decline", response_model=QuotationOut)
def decline(
    quotation_id: UUID,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> Quotation:
    try:
        q = quoting.decline_quotation(db, quotation_id)
    except quoting.QuotingError as e:
        raise HTTPException(status_code=400, detail=str(e))
    db.commit()
    return q


@router.post("/{quotation_id}/void", response_model=QuotationOut)
def void(
    quotation_id: UUID,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> Quotation:
    try:
        q = quoting.void_quotation(db, quotation_id)
    except quoting.QuotingError as e:
        raise HTTPException(status_code=400, detail=str(e))
    db.commit()
    return q


@router.get("/{quotation_id}/pdf")
def quotation_pdf(
    quotation_id: UUID,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> Response:
    q = db.get(Quotation, quotation_id)
    if q is None:
        raise HTTPException(status_code=404, detail="quotation not found")
    customer = db.get(Customer, q.customer_id) if q.customer_id else None
    pdf_bytes = render_quotation_pdf(q, customer)
    filename = f"quotation-{q.quotation_number or str(q.quotation_id)[:8]}.pdf"
    return Response(
        content=pdf_bytes,
        media_type="application/pdf",
        headers={"Content-Disposition": f'inline; filename="{filename}"'},
    )


@router.post("/{quotation_id}/send", response_model=QuotationOut)
def send(
    quotation_id: UUID,
    payload: SendQuotationRequest,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> Quotation:
    q = db.get(Quotation, quotation_id)
    if q is None:
        raise HTTPException(status_code=404, detail="quotation not found")
    if q.status in ("VOID", "INVOICED"):
        raise HTTPException(
            status_code=400, detail=f"cannot send a {q.status} quotation"
        )
    customer = db.get(Customer, q.customer_id) if q.customer_id else None
    to_email = payload.to_email or (customer.contact_email if customer else None)
    if not to_email:
        raise HTTPException(
            status_code=400,
            detail="no recipient: provide to_email or set a contact_email on the customer",
        )

    pdf_bytes = render_quotation_pdf(q, customer)
    filename = f"quotation-{q.quotation_number or str(q.quotation_id)[:8]}.pdf"
    subject = payload.subject or (
        f"Quotation {q.quotation_number}" if q.quotation_number else "Quotation"
    )
    greeting = customer.name if customer else "there"
    default_body = (
        f"Hi {greeting},\n\n"
        f"Please find attached our quotation {q.quotation_number or ''} "
        f"for {q.amount} {q.currency}.\n"
    )
    if q.valid_until:
        default_body += f"Valid until: {q.valid_until}.\n"
    default_body += "\nThank you."
    body_text = payload.message or default_body

    send_email(
        to_email=to_email,
        cc_email=payload.cc_email,
        subject=subject,
        body_text=body_text,
        attachments=[(filename, pdf_bytes, "application/pdf")],
    )

    if q.status == "DRAFT":
        q.status = "SENT"
    from datetime import datetime
    q.last_sent_at = datetime.utcnow()
    db.commit()
    db.refresh(q)
    return q


@router.post(
    "/{quotation_id}/convert-to-invoice",
    response_model=InvoiceOut,
    status_code=201,
)
def convert_to_invoice(
    quotation_id: UUID,
    payload: ConvertToInvoiceRequest,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> Invoice:
    try:
        invoice = quoting.convert_to_invoice(
            db,
            quotation_id,
            issue_date=payload.issue_date,
            due_in_days=payload.due_in_days,
        )
    except quoting.QuotingError as e:
        raise HTTPException(status_code=400, detail=str(e))
    db.commit()
    db.refresh(invoice)
    return invoice
