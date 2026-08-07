# Payment Receipt Email Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** When staff manually records a payment against an invoice, optionally email the customer a payment receipt (PDF attached), via a pre-checked opt-out checkbox in the Record Payment dialog.

**Architecture:** A new reportlab receipt renderer (`services/receipt_pdf.py`) plus a new `POST /invoices/{invoice_id}/payments/{payment_id}/send-receipt` endpoint that renders the PDF and sends it through the existing `send_email` service. The frontend records the payment first (existing endpoint, unchanged), then makes a second call to send the receipt — so payment recording stays atomic and an email failure can be retried without re-recording. Auto-reconciled (webhook/email-intake) payments are untouched.

**Tech Stack:** FastAPI + SQLAlchemy 2.0 (backend), reportlab (PDF), Resend/SMTP via `app/services/email.py`, Vue 3 + PrimeVue + @tanstack/vue-query (frontend).

**Spec:** `docs/superpowers/specs/2026-08-07-payment-receipt-email-design.md`

## Global Constraints

- **Everything runs inside Docker** — there is no local Python venv or Postgres. Run all commands from the repo root:
  - Backend tests: `docker compose exec -T -e TEST_DATABASE_URL="postgresql+psycopg://billing:billing@db:5432/billing_test" backend pytest <args>`
  - Backend lint: `docker compose exec -T backend ruff check app`
  - Frontend typecheck + build: `docker compose exec -T frontend npm run build`
- All financial amounts are `DECIMAL(19,4)`; never use floats for money.
- No model changes → **no Alembic migration needed** for this feature.
- The frontend has no unit-test runner; frontend verification is `npm run build` (runs `vue-tsc -b`).
- `_fmt_amount(value, currency)` from `app/services/invoice_pdf.py` already appends the currency code (e.g. `"600.00 USD"`) — never append the currency again after calling it.
- The app is single-tenant: business profile is `db.get(BusinessProfile, 1)`; entity lookups are `db.get(Model, id)` with 404 on miss (follow the existing router pattern).

---

### Task 1: Receipt PDF renderer

**Files:**
- Create: `backend/app/services/receipt_pdf.py`
- Test: `backend/tests/unit/test_receipt_pdf.py`

**Interfaces:**
- Consumes: `_fmt_amount(value, currency) -> str` from `app.services.invoice_pdf`; `_addr_lines(customer) -> list[str]` from `app.services.quotation_pdf`.
- Produces (used by Task 2):
  - `receipt_reference(payment: Payment) -> str` — e.g. `"RCT-3F2A9B01"`
  - `balance_note(invoice: Invoice) -> str` — `"This invoice is now paid in full."` or `"Remaining balance: 600.00 USD."`
  - `render_receipt_pdf(payment: Payment, invoice: Invoice, customer: Customer | None, business=None) -> bytes`

- [ ] **Step 1: Write the failing test**

Create `backend/tests/unit/test_receipt_pdf.py`:

```python
"""Receipt PDF renderer — pure unit tests, no DB required."""
from __future__ import annotations

from datetime import date
from decimal import Decimal
from uuid import uuid4

from app.models.customer import Customer
from app.models.invoice import Invoice
from app.models.payment import Payment
from app.services.receipt_pdf import balance_note, receipt_reference, render_receipt_pdf


def _make_objects(balance: str) -> tuple[Payment, Invoice, Customer]:
    customer = Customer(name="Acme Pte Ltd", contact_email="acme@example.com")
    invoice = Invoice(
        invoice_type="MILESTONE",
        invoice_number="INV-0100",
        currency="USD",
        amount=Decimal("1000.0000"),
        balance_due=Decimal(balance),
        status="PARTIAL" if Decimal(balance) > 0 else "PAID",
    )
    payment = Payment(
        payment_id=uuid4(),
        amount=Decimal("400.0000"),
        currency="USD",
        payer_name="Acme Pte Ltd",
        payer_reference="TRF-991",
        payment_date=date(2026, 8, 7),
        intake_source="MANUAL",
        external_ref="manual-abc123",
        status="CLEARED",
        adjustment_type="NONE",
    )
    return payment, invoice, customer


def test_receipt_reference_shape():
    payment, _, _ = _make_objects("600.0000")
    ref = receipt_reference(payment)
    assert ref.startswith("RCT-")
    assert len(ref) == 12  # "RCT-" + 8 hex chars
    assert ref == ref.upper()


def test_balance_note_partial_payment_shows_remaining():
    _, invoice, _ = _make_objects("600.0000")
    note = balance_note(invoice)
    assert note.startswith("Remaining balance:")
    assert "600" in note
    assert "USD" in note


def test_balance_note_full_payment_says_paid_in_full():
    _, invoice, _ = _make_objects("0")
    assert balance_note(invoice) == "This invoice is now paid in full."


def test_render_receipt_pdf_returns_pdf_bytes():
    payment, invoice, customer = _make_objects("600.0000")
    pdf = render_receipt_pdf(payment, invoice, customer)
    assert isinstance(pdf, bytes)
    assert pdf[:5] == b"%PDF-"


def test_render_receipt_pdf_without_customer():
    payment, invoice, _ = _make_objects("0")
    pdf = render_receipt_pdf(payment, invoice, None)
    assert pdf[:5] == b"%PDF-"
```

- [ ] **Step 2: Run test to verify it fails**

Run:
```bash
docker compose exec -T -e TEST_DATABASE_URL="postgresql+psycopg://billing:billing@db:5432/billing_test" backend pytest tests/unit/test_receipt_pdf.py -v
```
Expected: FAIL at collection with `ModuleNotFoundError: No module named 'app.services.receipt_pdf'`.

- [ ] **Step 3: Write the implementation**

Create `backend/app/services/receipt_pdf.py`:

```python
"""Render a payment receipt to a PDF bytes buffer.

Reportlab-only sibling of quotation_pdf.py. Receipts are simple one-page
documents, so there is no HTML variant behind the services/pdf façade —
callers import render_receipt_pdf from here directly.
"""

from __future__ import annotations

from decimal import Decimal
from io import BytesIO

from reportlab.lib import colors
from reportlab.lib.pagesizes import A4
from reportlab.lib.styles import ParagraphStyle, getSampleStyleSheet
from reportlab.lib.units import mm
from reportlab.platypus import Paragraph, SimpleDocTemplate, Spacer, Table, TableStyle

from app.models.customer import Customer
from app.models.invoice import Invoice
from app.models.payment import Payment
# Shared, currency-aware figure formatting (0dp for IDR and other zero-decimal
# currencies, 2dp otherwise) — keeps receipt figures consistent with invoices.
from app.services.invoice_pdf import _fmt_amount
from app.services.quotation_pdf import _addr_lines


def receipt_reference(payment: Payment) -> str:
    """Human-readable receipt reference derived from the payment id."""
    return f"RCT-{payment.payment_id.hex[:8].upper()}"


def balance_note(invoice: Invoice) -> str:
    """One-line balance summary shared by the receipt PDF and the email body."""
    balance = Decimal(invoice.balance_due or 0)
    if balance <= 0:
        return "This invoice is now paid in full."
    return f"Remaining balance: {_fmt_amount(balance, invoice.currency)}."


def _method_label(payment: Payment) -> str:
    if payment.intake_source == "MANUAL":
        return "Manual entry"
    return payment.intake_source.replace("_", " ").title()


def render_receipt_pdf(
    payment: Payment,
    invoice: Invoice,
    customer: Customer | None,
    business=None,
) -> bytes:
    """Render a payment receipt to PDF bytes.

    business: optional BusinessProfile (or any object exposing name); when
              present its name is shown in the footer as the receiving party.
    """
    buf = BytesIO()
    doc = SimpleDocTemplate(
        buf,
        pagesize=A4,
        leftMargin=18 * mm,
        rightMargin=18 * mm,
        topMargin=18 * mm,
        bottomMargin=18 * mm,
        title=f"Receipt {receipt_reference(payment)}",
    )
    styles = getSampleStyleSheet()
    h1 = ParagraphStyle(
        "h1",
        parent=styles["Heading1"],
        fontSize=22,
        leading=26,
        textColor=colors.HexColor("#0f172a"),
    )
    label = ParagraphStyle(
        "label",
        parent=styles["Normal"],
        fontSize=8,
        textColor=colors.HexColor("#64748b"),
        spaceAfter=2,
    )
    normal = ParagraphStyle("n", parent=styles["Normal"], fontSize=10, leading=14)
    small = ParagraphStyle(
        "s", parent=styles["Normal"], fontSize=9, textColor=colors.HexColor("#475569")
    )

    story: list = []

    meta_rows = [
        ["RECEIPT #", receipt_reference(payment)],
        ["PAYMENT DATE", str(payment.payment_date) if payment.payment_date else "—"],
        ["INVOICE #", invoice.invoice_number or "—"],
    ]
    meta_tbl = Table(meta_rows, colWidths=[28 * mm, 45 * mm])
    meta_tbl.setStyle(
        TableStyle(
            [
                ("FONTSIZE", (0, 0), (-1, -1), 8),
                ("TEXTCOLOR", (0, 0), (0, -1), colors.HexColor("#64748b")),
                ("TEXTCOLOR", (1, 0), (1, -1), colors.HexColor("#0f172a")),
                ("FONTNAME", (1, 0), (1, -1), "Helvetica-Bold"),
                ("BOTTOMPADDING", (0, 0), (-1, -1), 3),
                ("TOPPADDING", (0, 0), (-1, -1), 3),
            ]
        )
    )

    header_tbl = Table(
        [[Paragraph("PAYMENT RECEIPT", h1), meta_tbl]],
        colWidths=[92 * mm, 78 * mm],
    )
    header_tbl.setStyle(TableStyle([("VALIGN", (0, 0), (-1, -1), "TOP")]))
    story.append(header_tbl)
    story.append(Spacer(1, 8 * mm))

    story.append(Paragraph("RECEIVED FROM", label))
    if customer:
        story.append(Paragraph(f"<b>{customer.name}</b>", normal))
        for ln in _addr_lines(customer):
            story.append(Paragraph(ln, small))
        if customer.contact_email:
            story.append(Paragraph(customer.contact_email, small))
    elif payment.payer_name:
        story.append(Paragraph(f"<b>{payment.payer_name}</b>", normal))
    else:
        story.append(Paragraph("—", small))
    story.append(Spacer(1, 6 * mm))

    detail_rows = [
        ["Amount received", _fmt_amount(payment.amount, payment.currency)],
        ["Applied to invoice", invoice.invoice_number or "—"],
        ["Payment method", _method_label(payment)],
    ]
    if payment.payer_reference:
        detail_rows.append(["Reference", payment.payer_reference])
    detail_tbl = Table(detail_rows, colWidths=[50 * mm, 60 * mm])
    detail_tbl.setStyle(
        TableStyle(
            [
                ("FONTSIZE", (0, 0), (-1, -1), 10),
                ("TEXTCOLOR", (0, 0), (0, -1), colors.HexColor("#475569")),
                ("FONTNAME", (1, 0), (1, 0), "Helvetica-Bold"),
                ("TEXTCOLOR", (1, 0), (1, 0), colors.HexColor("#0f172a")),
                ("LINEBELOW", (0, 0), (-1, 0), 0.5, colors.HexColor("#cbd5e1")),
                ("TOPPADDING", (0, 0), (-1, -1), 5),
                ("BOTTOMPADDING", (0, 0), (-1, -1), 5),
            ]
        )
    )
    story.append(detail_tbl)
    story.append(Spacer(1, 6 * mm))

    story.append(Paragraph(balance_note(invoice), normal))
    story.append(Spacer(1, 10 * mm))

    story.append(Paragraph("Thank you for your payment.", small))
    if business is not None and getattr(business, "name", None):
        story.append(Spacer(1, 2 * mm))
        story.append(Paragraph(f"Received by {business.name}", small))

    doc.build(story)
    return buf.getvalue()
```

- [ ] **Step 4: Run test to verify it passes**

Run:
```bash
docker compose exec -T -e TEST_DATABASE_URL="postgresql+psycopg://billing:billing@db:5432/billing_test" backend pytest tests/unit/test_receipt_pdf.py -v
```
Expected: 5 passed.

- [ ] **Step 5: Lint and commit**

Run: `docker compose exec -T backend ruff check app` — expected: no errors.

```bash
git add backend/app/services/receipt_pdf.py backend/tests/unit/test_receipt_pdf.py
git commit -m "feat(receipt): reportlab payment receipt PDF renderer"
```

---

### Task 2: send-receipt endpoint

**Files:**
- Modify: `backend/app/schemas/payment.py` (add `SendReceiptRequest` after `RecordPaymentRequest`, ~line 54)
- Modify: `backend/app/api/v1/routers/invoices.py` (imports + new endpoint after `mark_paid`, ~line 525)
- Test: `backend/tests/integration/test_send_receipt.py`

**Interfaces:**
- Consumes (from Task 1): `render_receipt_pdf(payment, invoice, customer, business)`, `receipt_reference(payment)`, `balance_note(invoice)` from `app.services.receipt_pdf`.
- Consumes (already in `invoices.py`): `send_email` (module-level import, line ~40), `_fmt_amount`, `Payment`, `Customer`, `BusinessProfile` models — all already imported; verify before adding duplicates.
- Produces (used by Tasks 4–5): `POST /api/v1/invoices/{invoice_id}/payments/{payment_id}/send-receipt` with JSON body `{"to_email": str | null, "cc_email": str | null}` → `200 {"sent_to": "<email>"}`; errors: 404 (invoice or payment not on invoice), 400 (no recipient), 502 (email dispatch failed).

- [ ] **Step 1: Write the failing tests**

Create `backend/tests/integration/test_send_receipt.py`:

```python
"""POST /invoices/{invoice_id}/payments/{payment_id}/send-receipt.

Sends a payment-receipt email (PDF attached) for a manually recorded payment.
Email dispatch is monkeypatched — no real Resend/SMTP calls.
"""
from __future__ import annotations

from decimal import Decimal
from uuid import uuid4

from app.models.customer import Customer
from app.models.invoice import Invoice


def _record_payment(client, invoice_id, amount="400"):
    r = client.post(
        f"/api/v1/invoices/{invoice_id}/record-payment",
        json={"amount": amount, "payment_date": "2026-08-07", "payer_name": "Acme"},
    )
    assert r.status_code == 201, r.text
    return r.json()


def _patch_send_email(monkeypatch, calls):
    def fake_send_email(**kwargs):
        calls.append(kwargs)

    monkeypatch.setattr("app.api.v1.routers.invoices.send_email", fake_send_email)


def test_send_receipt_happy_path(client, admin_session, db, seed_invoice, monkeypatch):
    calls = []
    _patch_send_email(monkeypatch, calls)
    payment = _record_payment(client, seed_invoice)

    r = client.post(
        f"/api/v1/invoices/{seed_invoice}/payments/{payment['payment_id']}/send-receipt",
        json={"to_email": "acme@example.com", "cc_email": "me@example.com"},
    )
    assert r.status_code == 200, r.text
    assert r.json() == {"sent_to": "acme@example.com"}

    assert len(calls) == 1
    kw = calls[0]
    assert kw["to_email"] == "acme@example.com"
    assert kw["cc_email"] == "me@example.com"
    filename, content, mime = kw["attachments"][0]
    assert filename.startswith("receipt-RCT-") and filename.endswith(".pdf")
    assert content[:5] == b"%PDF-"
    assert mime == "application/pdf"
    # 1000 - 400 leaves 600 remaining; both figures appear in the body.
    assert "400" in kw["body_text"]
    assert "600" in kw["body_text"]


def test_send_receipt_defaults_to_customer_contact_email(
    client, admin_session, db, seed_customer, seed_invoice, monkeypatch
):
    calls = []
    _patch_send_email(monkeypatch, calls)
    db.get(Customer, seed_customer).contact_email = "billing@acme.example"
    db.flush()
    payment = _record_payment(client, seed_invoice)

    r = client.post(
        f"/api/v1/invoices/{seed_invoice}/payments/{payment['payment_id']}/send-receipt",
        json={},
    )
    assert r.status_code == 200, r.text
    assert r.json() == {"sent_to": "billing@acme.example"}
    assert calls[0]["to_email"] == "billing@acme.example"


def test_send_receipt_no_recipient_400(client, admin_session, db, seed_invoice, monkeypatch):
    # seed_customer has no contact_email and no to_email is provided.
    calls = []
    _patch_send_email(monkeypatch, calls)
    payment = _record_payment(client, seed_invoice)

    r = client.post(
        f"/api/v1/invoices/{seed_invoice}/payments/{payment['payment_id']}/send-receipt",
        json={},
    )
    assert r.status_code == 400, r.text
    assert calls == []


def test_send_receipt_payment_not_on_invoice_404(
    client, admin_session, db, seed_customer, seed_invoice, monkeypatch
):
    calls = []
    _patch_send_email(monkeypatch, calls)
    payment = _record_payment(client, seed_invoice)

    other = Invoice(
        customer_id=seed_customer,
        invoice_type="MILESTONE",
        invoice_number="INV-OTHER",
        currency="USD",
        amount=Decimal("500.0000"),
        balance_due=Decimal("500.0000"),
        status="SENT",
    )
    db.add(other)
    db.flush()

    r = client.post(
        f"/api/v1/invoices/{other.invoice_id}/payments/{payment['payment_id']}/send-receipt",
        json={"to_email": "acme@example.com"},
    )
    assert r.status_code == 404, r.text
    assert calls == []


def test_send_receipt_unknown_payment_404(client, admin_session, seed_invoice, monkeypatch):
    calls = []
    _patch_send_email(monkeypatch, calls)
    r = client.post(
        f"/api/v1/invoices/{seed_invoice}/payments/{uuid4()}/send-receipt",
        json={"to_email": "acme@example.com"},
    )
    assert r.status_code == 404, r.text


def test_send_receipt_email_failure_502(client, admin_session, db, seed_invoice, monkeypatch):
    def boom(**kwargs):
        raise RuntimeError("resend down")

    monkeypatch.setattr("app.api.v1.routers.invoices.send_email", boom)
    payment = _record_payment(client, seed_invoice)

    r = client.post(
        f"/api/v1/invoices/{seed_invoice}/payments/{payment['payment_id']}/send-receipt",
        json={"to_email": "acme@example.com"},
    )
    assert r.status_code == 502, r.text
```

- [ ] **Step 2: Run tests to verify they fail**

Run:
```bash
docker compose exec -T -e TEST_DATABASE_URL="postgresql+psycopg://billing:billing@db:5432/billing_test" backend pytest tests/integration/test_send_receipt.py -v
```
Expected: all FAIL with 404/405 responses (endpoint does not exist yet).

- [ ] **Step 3: Add the schema**

In `backend/app/schemas/payment.py`, directly after `RecordPaymentRequest` (after ~line 53):

```python
class SendReceiptRequest(BaseModel):
    """Email a payment receipt (PDF) to the customer."""
    to_email: str | None = Field(
        default=None, description="Override recipient (defaults to customer contact_email)"
    )
    cc_email: str | None = None
```

`Field` is already imported in this module (used by `RecordPaymentRequest.amount`); if not, add it to the existing pydantic import.

- [ ] **Step 4: Add the endpoint**

In `backend/app/api/v1/routers/invoices.py`:

Add to the existing imports (do not duplicate — `Payment`, `Customer`, `BusinessProfile`, `send_email`, `_fmt_amount` are already imported):

```python
from app.schemas.payment import PaymentOut, RecordPaymentRequest, SendReceiptRequest
from app.services.receipt_pdf import balance_note, receipt_reference, render_receipt_pdf
```

(The first line replaces the existing `from app.schemas.payment import PaymentOut, RecordPaymentRequest` import.)

Add the endpoint after `mark_paid` (~line 525):

```python
@router.post("/{invoice_id}/payments/{payment_id}/send-receipt")
def send_payment_receipt(
    invoice_id: UUID,
    payment_id: UUID,
    payload: SendReceiptRequest,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> dict:
    """Email a payment-receipt PDF to the customer for a recorded payment.

    Separate from record-payment so recording stays atomic: an email failure
    here never rolls back the payment, and the call can be retried safely.
    """
    invoice = db.get(Invoice, invoice_id)
    if invoice is None:
        raise HTTPException(status_code=404, detail="invoice not found")
    payment = db.get(Payment, payment_id)
    if payment is None or payment.invoice_id != invoice.invoice_id:
        raise HTTPException(status_code=404, detail="payment not found on this invoice")

    customer = db.get(Customer, invoice.customer_id) if invoice.customer_id else None
    to_email = payload.to_email or (customer.contact_email if customer else None)
    if not to_email:
        raise HTTPException(
            status_code=400,
            detail="no recipient: provide to_email or set a contact_email on the customer",
        )

    business = db.get(BusinessProfile, 1)
    pdf_bytes = render_receipt_pdf(payment, invoice, customer, business)
    filename = f"receipt-{receipt_reference(payment)}.pdf"
    subject = (
        f"Payment receipt for invoice {invoice.invoice_number}"
        if invoice.invoice_number
        else "Payment receipt"
    )
    greeting_name = customer.name if customer else "there"
    body_text = (
        f"Hi {greeting_name},\n\n"
        f"We have received your payment of "
        f"{_fmt_amount(payment.amount, payment.currency)} "
        f"against invoice {invoice.invoice_number or ''} "
        f"on {payment.payment_date}.\n"
        f"{balance_note(invoice)}\n\n"
        "A PDF receipt is attached for your records.\n\n"
        "Thank you."
    )
    try:
        send_email(
            to_email=to_email,
            cc_email=payload.cc_email,
            subject=subject,
            body_text=body_text,
            attachments=[(filename, pdf_bytes, "application/pdf")],
        )
    except HTTPException:
        raise
    except Exception:
        raise HTTPException(status_code=502, detail="failed to send receipt email")
    return {"sent_to": to_email}
```

- [ ] **Step 5: Run tests to verify they pass**

Run:
```bash
docker compose exec -T -e TEST_DATABASE_URL="postgresql+psycopg://billing:billing@db:5432/billing_test" backend pytest tests/integration/test_send_receipt.py tests/unit/test_receipt_pdf.py -v
```
Expected: all passed.

- [ ] **Step 6: Lint and commit**

Run: `docker compose exec -T backend ruff check app` — expected: no errors.

```bash
git add backend/app/schemas/payment.py backend/app/api/v1/routers/invoices.py backend/tests/integration/test_send_receipt.py
git commit -m "feat(receipt): send-receipt endpoint emails payment receipt PDF"
```

---

### Task 3: Receipt opt-out in RecordPaymentDialog

**Files:**
- Modify: `frontend/src/components/RecordPaymentDialog.vue`

**Interfaces:**
- Produces (used by Tasks 4–5): new optional prop `customerEmail?: string | null`; the `submit` payload gains a `receipt: { to_email: string; cc_email: string | null } | null` field. `receipt` is always `null` in AP mode (BillFormView) — no receipt UI is rendered there.
- Consumes: `useAuthStore` from `@/stores/auth` (same Cc-default pattern as `SendInvoiceDialog.vue`).

Note: BillFormView passes the payload straight through to the bills record-payment endpoint; the extra `receipt: null` key is ignored by pydantic (`RecordPaymentRequest` uses default extra-ignore), so no BillFormView change is needed.

- [ ] **Step 1: Update the script section**

In `frontend/src/components/RecordPaymentDialog.vue`:

Add imports (after the existing PrimeVue imports):

```ts
import Checkbox from "primevue/checkbox";
import { useAuthStore } from "@/stores/auth";
```

Add to `Props`:

```ts
  customerEmail?: string | null;
```

Replace the `submit` emit signature with:

```ts
  (e: "submit", payload: {
    amount: number;
    payment_date: string;
    payer_name: string | null;
    payer_reference: string | null;
    notes: string | null;
    receipt: { to_email: string; cc_email: string | null } | null;
  }): void;
```

Add state (after the existing `notes` ref):

```ts
const auth = useAuthStore();
const sendReceipt = ref(false);
const receiptTo = ref<string>("");
const receiptCc = ref<string>("");
```

Extend the `watch(() => props.visible, ...)` reset block — inside `if (v) { ... }` add:

```ts
      sendReceipt.value = props.mode === "AR";
      receiptTo.value = props.customerEmail ?? "";
      receiptCc.value = auth.user?.email ?? "";
```

Replace `canSubmit` with:

```ts
const canSubmit = computed(
  () =>
    !!amount.value &&
    amount.value > 0 &&
    amount.value <= balanceNum.value &&
    !!paymentDate.value &&
    (props.mode !== "AR" || !sendReceipt.value || !!receiptTo.value.trim()),
);
```

In `submit()`, extend the emitted payload:

```ts
  emit("submit", {
    amount: amount.value,
    payment_date: iso,
    payer_name: payerName.value.trim() || null,
    payer_reference: payerReference.value.trim() || null,
    notes: notes.value.trim() || null,
    receipt:
      props.mode === "AR" && sendReceipt.value
        ? { to_email: receiptTo.value.trim(), cc_email: receiptCc.value.trim() || null }
        : null,
  });
```

- [ ] **Step 2: Update the template**

After the Notes field `</label>` and before the `<Message v-if="error" ...>` line, add:

```html
      <template v-if="mode === 'AR'">
        <label class="field receipt-check">
          <span class="receipt-row">
            <Checkbox v-model="sendReceipt" binary :disabled="loading" />
            Email receipt (PDF) to customer
          </span>
        </label>
        <template v-if="sendReceipt">
          <label class="field">
            <span>To</span>
            <InputText v-model="receiptTo" placeholder="customer@example.com" />
          </label>
          <label class="field">
            <span>Cc <span class="muted">(optional)</span></span>
            <InputText v-model="receiptCc" />
          </label>
          <p class="note"><i class="pi pi-paperclip" /> A PDF receipt will be attached automatically.</p>
        </template>
      </template>
```

Add to the scoped styles:

```css
.receipt-check { border-top: 1px solid var(--color-border); padding-top: 0.6rem; }
.receipt-row { display: flex; align-items: center; gap: 0.5rem; font-weight: 600; }
.note { font-size: 0.78rem; color: var(--color-text-muted); margin: 0; }
.note i { margin-right: 0.3rem; }
```

- [ ] **Step 3: Typecheck**

Run: `docker compose exec -T frontend npm run build`
Expected: `vue-tsc -b` fails in `InvoicesView.vue`/`InvoiceFormView.vue` ONLY if the new payload field breaks their mutation types — it should NOT: the `submit` payload is a superset and both views type their `mutationFn` parameter explicitly, so the extra `receipt` key is accepted structurally. Expected: build succeeds. If it fails on the views' payload types, that is fixed properly in Tasks 4–5; a temporary failure here is acceptable ONLY on those exact lines — note it and continue.

- [ ] **Step 4: Commit**

```bash
git add frontend/src/components/RecordPaymentDialog.vue
git commit -m "feat(receipt): receipt opt-out checkbox with To/Cc in RecordPaymentDialog"
```

---

### Task 4: Wire receipt send-and-retry in InvoicesView

**Files:**
- Modify: `frontend/src/views/InvoicesView.vue`

**Interfaces:**
- Consumes: `POST /invoices/{invoice_id}/payments/{payment_id}/send-receipt` (Task 2); dialog `receipt` payload field and `customerEmail` prop (Task 3); `useConfirm` from `primevue/useconfirm` (global `<ConfirmDialog />` is already mounted in `App.vue`).
- Record-payment response JSON includes `payment_id` (from `PaymentOut`).

- [ ] **Step 1: Extend the Customer interface and add the receipt helpers**

In `frontend/src/views/InvoicesView.vue`:

Extend the local `Customer` interface (line ~15):

```ts
interface Customer {
  customer_id: string;
  name: string;
  contact_email: string | null;
}
```

Add import:

```ts
import { useConfirm } from "primevue/useconfirm";
```

In the `// ---- inline record-payment ----` section add:

```ts
const confirm = useConfirm();

const paymentCustomerEmail = computed(() => {
  const cid = paymentTarget.value?.customer_id;
  if (!cid) return null;
  return (customers.value ?? []).find((c) => c.customer_id === cid)?.contact_email ?? null;
});

async function sendReceipt(
  invoiceId: string,
  paymentId: string,
  receipt: { to_email: string; cc_email: string | null },
) {
  try {
    await api.post(`/invoices/${invoiceId}/payments/${paymentId}/send-receipt`, receipt);
  } catch (e) {
    const detail =
      (e as { response?: { data?: { detail?: string } } })?.response?.data?.detail ??
      "the email service returned an error";
    confirm.require({
      header: "Receipt email failed",
      message: `The payment was recorded, but the receipt email to ${receipt.to_email} failed: ${detail}. Retry?`,
      icon: "pi pi-envelope",
      acceptLabel: "Retry",
      rejectLabel: "Dismiss",
      accept: () => void sendReceipt(invoiceId, paymentId, receipt),
    });
  }
}
```

- [ ] **Step 2: Chain the receipt send after a successful record-payment**

Update the `recordPayment` mutation. The `mutationFn` payload type gains the `receipt` field, the record-payment POST body must NOT include it, and `onSuccess` fires the receipt call before clearing `paymentTarget`:

```ts
const recordPayment = useMutation({
  mutationFn: async (payload: {
    amount: number;
    payment_date: string;
    payer_name: string | null;
    payer_reference: string | null;
    notes: string | null;
    receipt: { to_email: string; cc_email: string | null } | null;
  }) => {
    const id = paymentTarget.value?.invoice_id;
    if (!id) throw new Error("no invoice id");
    const { receipt: _receipt, ...body } = payload;
    return (await api.post(`/invoices/${id}/record-payment`, body)).data;
  },
  onSuccess: (data: { payment_id: string }, variables) => {
    const invoiceId = paymentTarget.value?.invoice_id;
    if (variables.receipt && invoiceId) {
      void sendReceipt(invoiceId, data.payment_id, variables.receipt);
    }
    paymentError.value = null;
    paymentDialogVisible.value = false;
    paymentTarget.value = null;
    queryClient.invalidateQueries({ queryKey: ["invoices"] });
    queryClient.invalidateQueries({ queryKey: ["invoices-summary"] });
    queryClient.invalidateQueries({ queryKey: ["dashboard-stats"] });
  },
  onError: (e: { response?: { data?: { detail?: string } } }) => {
    paymentError.value = e?.response?.data?.detail ?? "Failed to record payment";
  },
});
```

- [ ] **Step 3: Pass the customer email to the dialog**

In the template, on the `<RecordPaymentDialog ...>` usage (~line 265), add:

```html
      :customer-email="paymentCustomerEmail"
```

- [ ] **Step 4: Typecheck + build**

Run: `docker compose exec -T frontend npm run build`
Expected: build succeeds with no type errors.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/views/InvoicesView.vue
git commit -m "feat(receipt): send receipt after record-payment in invoices list"
```

---

### Task 5: Wire receipt send-and-retry in InvoiceFormView

**Files:**
- Modify: `frontend/src/views/InvoiceFormView.vue`

**Interfaces:**
- Consumes: same endpoint/dialog contracts as Task 4. This view already has `selectedCustomer` (computed, includes `contact_email`) and `props.id` (the invoice id).

- [ ] **Step 1: Add the receipt helper**

In `frontend/src/views/InvoiceFormView.vue`, add the import:

```ts
import { useConfirm } from "primevue/useconfirm";
```

Near the `recordPayment` mutation (~line 306) add — identical logic to Task 4's helper:

```ts
const confirm = useConfirm();

async function sendReceipt(
  invoiceId: string,
  paymentId: string,
  receipt: { to_email: string; cc_email: string | null },
) {
  try {
    await api.post(`/invoices/${invoiceId}/payments/${paymentId}/send-receipt`, receipt);
  } catch (e) {
    const detail =
      (e as { response?: { data?: { detail?: string } } })?.response?.data?.detail ??
      "the email service returned an error";
    confirm.require({
      header: "Receipt email failed",
      message: `The payment was recorded, but the receipt email to ${receipt.to_email} failed: ${detail}. Retry?`,
      icon: "pi pi-envelope",
      acceptLabel: "Retry",
      rejectLabel: "Dismiss",
      accept: () => void sendReceipt(invoiceId, paymentId, receipt),
    });
  }
}
```

(If `useConfirm` is already imported/instantiated in this view, reuse the existing instance instead of adding a second one.)

- [ ] **Step 2: Chain the receipt send in the recordPayment mutation**

Update the existing `recordPayment` mutation (~line 306): add `receipt` to the payload type, strip it from the POST body, and fire the receipt call in `onSuccess`:

```ts
const recordPayment = useMutation({
  mutationFn: async (payload: {
    amount: number;
    payment_date: string;
    payer_name: string | null;
    payer_reference: string | null;
    notes: string | null;
    receipt: { to_email: string; cc_email: string | null } | null;
  }) => {
    if (!props.id) throw new Error("no invoice id");
    const { receipt: _receipt, ...body } = payload;
    return (await api.post(`/invoices/${props.id}/record-payment`, body)).data;
  },
  onSuccess: (data: { payment_id: string }, variables) => {
    if (variables.receipt && props.id) {
      void sendReceipt(props.id, data.payment_id, variables.receipt);
    }
    paymentError.value = null;
    showPaymentDialog.value = false;
    queryClient.invalidateQueries({ queryKey: ["invoice", props.id] });
    queryClient.invalidateQueries({ queryKey: ["invoices"] });
    queryClient.invalidateQueries({ queryKey: ["dashboard-stats"] });
  },
  onError: (e: { response?: { data?: { detail?: string } } }) => {
    paymentError.value = e?.response?.data?.detail ?? "Failed to record payment";
  },
});
```

- [ ] **Step 3: Pass the customer email to the dialog**

On the `<RecordPaymentDialog ...>` usage (~line 1007), add:

```html
      :customer-email="selectedCustomer?.contact_email ?? null"
```

- [ ] **Step 4: Typecheck + build**

Run: `docker compose exec -T frontend npm run build`
Expected: build succeeds with no type errors.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/views/InvoiceFormView.vue
git commit -m "feat(receipt): send receipt after record-payment in invoice detail"
```

---

### Task 6: Full verification

**Files:** none (verification only; fix-forward if anything fails)

- [ ] **Step 1: Full backend test suite**

Run:
```bash
docker compose exec -T -e TEST_DATABASE_URL="postgresql+psycopg://billing:billing@db:5432/billing_test" backend pytest
```
Expected: all tests pass (including the pre-existing suites — nothing this feature touched changes existing behavior).

- [ ] **Step 2: Backend lint**

Run: `docker compose exec -T backend ruff check app`
Expected: no errors.

- [ ] **Step 3: Frontend typecheck + production build**

Run: `docker compose exec -T frontend npm run build`
Expected: `vue-tsc -b && vite build` completes cleanly.

- [ ] **Step 4: Manual smoke test (browser preview)**

1. Open http://localhost:5173, log in, open the Invoices list.
2. Record a partial payment on an OPEN/SENT invoice with the receipt checkbox ON and a test To address. Expected: payment recorded, dialog closes; backend logs show either a Resend send or the `[email disabled] would send ...` fallback line with a `receipt-RCT-*.pdf` attachment (check with `docker compose logs backend --tail 50`).
3. Record a payment on a bill (AP side). Expected: no receipt checkbox appears.
4. Record a payment with the checkbox OFF. Expected: no email attempt in the backend logs.

- [ ] **Step 5: Fix anything that failed, re-run, commit fixes**

Any fixes get their own commits (`fix(receipt): ...`). Re-run the failed step until clean.
