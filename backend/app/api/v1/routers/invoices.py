from datetime import date, datetime, timedelta
from decimal import Decimal
from uuid import UUID, uuid4

from fastapi import APIRouter, Depends, HTTPException, Query
from fastapi.responses import Response
from pydantic import BaseModel
from sqlalchemy import select
from sqlalchemy.orm import Session

from app.services.recurring_schedule import (
    ScheduleError,
    describe,
    is_paused,
    next_cycle_after,
    parse_schedule,
)

from app.api.deps import current_admin
from app.db.session import get_db
from app.models.customer import Customer
from app.models.invoice import Invoice
from app.models.payment import Payment
from app.models.recon_log import ReconciliationLog
from app.models.user import User
from app.schemas.invoice import (
    InvoiceCreate,
    InvoiceOut,
    InvoiceUpdate,
    MilestoneInvoiceCreate,
    RecurringTemplateCreate,
    RecurringTrigger,
    SendInvoiceRequest,
    UsageLockRequest,
    VoidRequest,
)
from app.schemas.payment import PaymentOut, RecordPaymentRequest
from app.services import invoicing
from app.services.email import send_email
from app.services.invoice_pdf import render_invoice_pdf

router = APIRouter(prefix="/invoices", tags=["invoices"])


@router.post("", response_model=InvoiceOut, status_code=201)
def create_invoice(
    payload: InvoiceCreate,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> Invoice:
    try:
        invoice = invoicing.create_invoice(db, payload)
    except invoicing.InvoicingError as e:
        raise HTTPException(status_code=400, detail=str(e))
    db.commit()
    db.refresh(invoice)
    return invoice


@router.post("/milestone", response_model=InvoiceOut, status_code=201)
def create_milestone(
    payload: MilestoneInvoiceCreate,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> Invoice:
    invoice = invoicing.create_milestone_invoice(
        db,
        customer_id=payload.customer_id,
        project_id=payload.project_id,
        currency=payload.currency,
        amount=payload.amount,
        milestone_ref=payload.milestone_ref,
        issue_date=payload.issue_date,
        due_in_days=payload.due_in_days,
    )
    db.commit()
    return invoice


@router.post("/recurring-template", response_model=InvoiceOut, status_code=201)
def create_recurring_template(
    payload: RecurringTemplateCreate,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> Invoice:
    try:
        invoice = invoicing.create_recurring_template(db, payload)
    except invoicing.InvoicingError as e:
        raise HTTPException(status_code=400, detail=str(e))
    db.commit()
    db.refresh(invoice)
    return invoice


@router.get("/recurring-templates", response_model=list[InvoiceOut])
def list_recurring_templates(
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> list[Invoice]:
    return list(
        db.scalars(
            select(Invoice)
            .where(Invoice.is_template.is_(True))
            .order_by(Invoice.created_at.desc())
        )
    )


class RecurringTemplateRow(BaseModel):
    template_id: UUID
    customer_id: UUID | None
    customer_name: str | None
    currency: str
    amount: Decimal
    payment_terms: str | None
    schedule_description: str
    frequency: str | None
    start_date: date | None
    end_mode: str | None
    next_run_date: date | None
    previous_issue_date: date | None
    generated_count: int
    status: str  # ACTIVE / ENDED / MISCONFIGURED (R4 adds PAUSED)


@router.get("/recurring-templates/rows", response_model=list[RecurringTemplateRow])
def list_recurring_template_rows(
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> list[RecurringTemplateRow]:
    """Enriched list for the Recurring Invoices view: schedule description,
    next run date, and latest generated child's issue date."""
    today = date.today()
    templates = list(
        db.scalars(
            select(Invoice)
            .where(Invoice.is_template.is_(True))
            .order_by(Invoice.created_at.desc())
        )
    )
    out: list[RecurringTemplateRow] = []
    for t in templates:
        customer = db.get(Customer, t.customer_id) if t.customer_id else None
        # Latest generated child: same customer, RECURRING, not a template,
        # with matching template_invoice_id in billing_cycle_ref.
        children = list(
            db.scalars(
                select(Invoice)
                .where(Invoice.is_template.is_(False))
                .where(Invoice.invoice_type == "RECURRING")
                .where(
                    Invoice.billing_cycle_ref["template_invoice_id"].astext
                    == str(t.invoice_id)
                )
                .order_by(Invoice.issue_date.desc().nullslast())
            )
        )
        generated_count = len(children)
        previous_issue_date = children[0].issue_date if children else None

        try:
            schedule = parse_schedule(t.billing_cycle_ref)
            schedule_desc = describe(schedule)
            anchor = previous_issue_date or (schedule.start_date - timedelta(days=1))
            next_run = next_cycle_after(schedule, anchor)
            if is_paused(t.billing_cycle_ref):
                status = "PAUSED"
            elif next_run is None:
                status = "ENDED"
            else:
                status = "ACTIVE"
            frequency = schedule.frequency
            start_date = schedule.start_date
            end_mode = schedule.end_mode
        except ScheduleError:
            schedule_desc = "Schedule not configured"
            next_run = None
            status = "MISCONFIGURED"
            frequency = None
            start_date = None
            end_mode = None

        out.append(
            RecurringTemplateRow(
                template_id=t.invoice_id,
                customer_id=t.customer_id,
                customer_name=customer.name if customer else None,
                currency=t.currency,
                amount=t.amount,
                payment_terms=t.payment_terms,
                schedule_description=schedule_desc,
                frequency=frequency,
                start_date=start_date,
                end_mode=end_mode,
                next_run_date=next_run,
                previous_issue_date=previous_issue_date,
                generated_count=generated_count,
                status=status,
            )
        )
    return out


@router.get("/recurring-templates/{template_id}", response_model=InvoiceOut)
def get_recurring_template(
    template_id: UUID,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> Invoice:
    template = db.get(Invoice, template_id)
    if template is None or not template.is_template:
        raise HTTPException(status_code=404, detail="recurring template not found")
    return template


@router.put("/recurring-templates/{template_id}", response_model=InvoiceOut)
def update_recurring_template(
    template_id: UUID,
    payload: RecurringTemplateCreate,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> Invoice:
    try:
        template = invoicing.update_recurring_template(db, template_id, payload)
    except invoicing.InvoicingError as e:
        raise HTTPException(status_code=400, detail=str(e))
    db.commit()
    db.refresh(template)
    return template


class RecurringTemplateAction(BaseModel):
    action: str  # PAUSE | RESUME | END_NOW


@router.patch("/recurring-templates/{template_id}", response_model=InvoiceOut)
def patch_recurring_template(
    template_id: UUID,
    payload: RecurringTemplateAction,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> Invoice:
    template = db.get(Invoice, template_id)
    if template is None or not template.is_template:
        raise HTTPException(status_code=404, detail="recurring template not found")

    cfg: dict = dict(template.billing_cycle_ref or {})
    action = payload.action.upper()
    if action == "PAUSE":
        cfg["paused"] = True
    elif action == "RESUME":
        cfg.pop("paused", None)
    elif action == "END_NOW":
        # Force schedule to stop: set end_mode=ON_DATE, end_date=yesterday.
        cfg["end_mode"] = "ON_DATE"
        cfg["end_date"] = (date.today() - timedelta(days=1)).isoformat()
        cfg.pop("end_after_cycles", None)
    else:
        raise HTTPException(status_code=400, detail=f"unknown action: {payload.action}")

    template.billing_cycle_ref = cfg
    db.commit()
    db.refresh(template)
    return template


@router.post("/recurring/{template_id}/trigger", response_model=InvoiceOut)
def trigger_recurring(
    template_id: UUID,
    payload: RecurringTrigger,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> Invoice:
    try:
        invoice = invoicing.trigger_recurring_cycle(
            db, template_invoice_id=template_id, cycle_key=payload.cycle_key
        )
    except invoicing.InvoicingError as e:
        raise HTTPException(status_code=400, detail=str(e))
    db.commit()
    return invoice


@router.post("/usage/{invoice_id}/lock", response_model=InvoiceOut)
def lock_usage(
    invoice_id: UUID,
    payload: UsageLockRequest,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> Invoice:
    try:
        invoice = invoicing.lock_usage_invoice(
            db,
            invoice_id=invoice_id,
            accrued_amount=payload.accrued_amount,
            cutoff_date=payload.cutoff_date,
        )
    except invoicing.InvoicingError as e:
        raise HTTPException(status_code=400, detail=str(e))
    db.commit()
    return invoice


@router.get("/awaiting-finalization", response_model=list[InvoiceOut])
def awaiting_finalization(
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> list[Invoice]:
    return list(
        db.scalars(
            select(Invoice)
            .where(Invoice.status == "DRAFT")
            .where(Invoice.is_template.is_(False))
            .order_by(Invoice.created_at.desc())
        )
    )


@router.get("", response_model=list[InvoiceOut])
def list_invoices(
    status: list[str] | None = Query(
        default=None,
        description="Filter by status. Repeat the param to combine (e.g. status=SENT&status=PARTIAL).",
    ),
    customer_id: UUID | None = Query(default=None),
    invoice_type: str | None = Query(default=None),
    limit: int = Query(default=200, le=500),
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> list[Invoice]:
    stmt = (
        select(Invoice)
        .where(Invoice.is_template.is_(False))
        .order_by(Invoice.created_at.desc())
        .limit(limit)
    )
    if status:
        stmt = stmt.where(Invoice.status.in_(status))
    if customer_id:
        stmt = stmt.where(Invoice.customer_id == customer_id)
    if invoice_type:
        stmt = stmt.where(Invoice.invoice_type == invoice_type)
    return list(db.scalars(stmt))


@router.post("/{invoice_id}/finalize", response_model=InvoiceOut)
def finalize(
    invoice_id: UUID,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> Invoice:
    try:
        invoice = invoicing.finalize_invoice(db, invoice_id)
    except invoicing.InvoicingError as e:
        raise HTTPException(status_code=400, detail=str(e))
    db.commit()
    return invoice


@router.post("/{invoice_id}/void", response_model=InvoiceOut)
def void(
    invoice_id: UUID,
    payload: VoidRequest,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> Invoice:
    try:
        invoice = invoicing.void_invoice(db, invoice_id)
    except invoicing.InvoicingError as e:
        raise HTTPException(status_code=400, detail=str(e))
    db.commit()
    return invoice


@router.get("/{invoice_id}", response_model=InvoiceOut)
def get_invoice(
    invoice_id: UUID,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> Invoice:
    invoice = db.get(Invoice, invoice_id)
    if invoice is None:
        raise HTTPException(status_code=404, detail="invoice not found")
    return invoice


@router.patch("/{invoice_id}", response_model=InvoiceOut)
def update_invoice(
    invoice_id: UUID,
    payload: InvoiceUpdate,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> Invoice:
    try:
        invoice = invoicing.update_invoice(db, invoice_id, payload)
    except invoicing.InvoicingError as e:
        raise HTTPException(status_code=400, detail=str(e))
    db.commit()
    db.refresh(invoice)
    return invoice


@router.post("/{invoice_id}/record-payment", response_model=PaymentOut, status_code=201)
def record_payment(
    invoice_id: UUID,
    payload: RecordPaymentRequest,
    db: Session = Depends(get_db),
    user: User = Depends(current_admin),
) -> Payment:
    """Manually record a payment received against this invoice.

    Creates a Payment row (status=CLEARED), decrements balance_due, and updates
    invoice status to PARTIAL or PAID accordingly. Refuses if invoice is DRAFT or VOID.
    """
    invoice = db.get(Invoice, invoice_id)
    if invoice is None:
        raise HTTPException(status_code=404, detail="invoice not found")
    if invoice.status in ("DRAFT", "VOID"):
        raise HTTPException(
            status_code=400,
            detail=f"cannot record payment on {invoice.status} invoice",
        )
    amount = Decimal(payload.amount)
    if amount > Decimal(invoice.balance_due):
        raise HTTPException(
            status_code=400,
            detail=f"amount {amount} exceeds balance due {invoice.balance_due}",
        )

    payment = Payment(
        invoice_id=invoice.invoice_id,
        customer_id=invoice.customer_id,
        amount=amount,
        currency=invoice.currency,
        payer_name=payload.payer_name or "(manual entry)",
        payer_reference=payload.payer_reference,
        payment_date=payload.payment_date,
        intake_source="MANUAL",
        external_ref=f"manual-{uuid4().hex[:12]}",
        status="CLEARED",
        adjustment_type="NONE",
    )
    db.add(payment)

    invoice.balance_due = Decimal(invoice.balance_due) - amount
    if invoice.balance_due <= 0:
        invoice.balance_due = Decimal("0")
        invoice.status = "PAID"
    else:
        invoice.status = "PARTIAL"

    db.flush()

    reasons = ["manual-entry"]
    if payload.notes:
        reasons.append(payload.notes[:200])
    db.add(
        ReconciliationLog(
            payment_id=payment.payment_id,
            action="CLEARED",
            reasons=reasons,
            actor_user_id=user.user_id,
        )
    )
    db.commit()
    db.refresh(payment)
    return payment


@router.get("/{invoice_id}/pdf")
def invoice_pdf(
    invoice_id: UUID,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> Response:
    """Render the invoice as a PDF and stream it back."""
    invoice = db.get(Invoice, invoice_id)
    if invoice is None:
        raise HTTPException(status_code=404, detail="invoice not found")
    customer = db.get(Customer, invoice.customer_id) if invoice.customer_id else None
    pdf_bytes = render_invoice_pdf(invoice, customer)
    filename = f"invoice-{invoice.invoice_number or str(invoice.invoice_id)[:8]}.pdf"
    return Response(
        content=pdf_bytes,
        media_type="application/pdf",
        headers={"Content-Disposition": f'inline; filename="{filename}"'},
    )


@router.post("/{invoice_id}/send", response_model=InvoiceOut)
def send_invoice(
    invoice_id: UUID,
    payload: SendInvoiceRequest,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> Invoice:
    """Email the invoice PDF to the customer.

    If status is DRAFT it is auto-promoted to SENT.
    """
    invoice = db.get(Invoice, invoice_id)
    if invoice is None:
        raise HTTPException(status_code=404, detail="invoice not found")
    if invoice.status == "VOID":
        raise HTTPException(status_code=400, detail="cannot send a voided invoice")
    customer = db.get(Customer, invoice.customer_id) if invoice.customer_id else None
    to_email = payload.to_email or (customer.contact_email if customer else None)
    if not to_email:
        raise HTTPException(
            status_code=400,
            detail="no recipient: provide to_email or set a contact_email on the customer",
        )

    pdf_bytes = render_invoice_pdf(invoice, customer)
    filename = f"invoice-{invoice.invoice_number or str(invoice.invoice_id)[:8]}.pdf"
    subject = payload.subject or (
        f"Invoice {invoice.invoice_number}" if invoice.invoice_number else "Invoice"
    )
    greeting_name = (customer.name if customer else "there")
    default_body = (
        f"Hi {greeting_name},\n\n"
        f"Please find attached invoice {invoice.invoice_number or ''} "
        f"for {invoice.amount} {invoice.currency}.\n"
    )
    if invoice.due_date:
        default_body += f"Due date: {invoice.due_date}.\n"
    default_body += "\nThank you."
    body_text = payload.message or default_body

    send_email(
        to_email=to_email,
        cc_email=payload.cc_email,
        subject=subject,
        body_text=body_text,
        attachments=[(filename, pdf_bytes, "application/pdf")],
    )

    if invoice.status == "DRAFT":
        invoice.status = "SENT"
    db.commit()
    db.refresh(invoice)
    return invoice


@router.delete("/{invoice_id}", status_code=204)
def delete_invoice(
    invoice_id: UUID,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> None:
    try:
        invoicing.delete_invoice(db, invoice_id)
    except invoicing.InvoicingError as e:
        raise HTTPException(status_code=400, detail=str(e))
    db.commit()
