# Manual Send Receipt Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A "Send receipt" button on the invoice detail view for PAID/PARTIAL invoices, opening a dialog to pick a payment and (re)send its receipt email.

**Architecture:** One new read-only endpoint (`GET /invoices/{invoice_id}/payments`) feeds a new `SendReceiptDialog.vue`; sending reuses the existing `send-receipt` endpoint from PR #23 unchanged.

**Tech Stack:** FastAPI + SQLAlchemy (backend), Vue 3 + PrimeVue + @tanstack/vue-query (frontend).

**Spec:** `docs/superpowers/specs/2026-08-07-manual-send-receipt-design.md`

## Global Constraints

- **Everything runs inside Docker** from the repo root:
  - Backend tests: `docker compose exec -T -e TEST_DATABASE_URL="postgresql+psycopg://billing:billing@db:5432/billing_test" backend pytest <args>`
  - Frontend typecheck + build: `docker compose exec -T frontend npm run build`
- Existing send-receipt endpoint is reused as-is — do not modify it.
- `InvoiceFormView.vue` already defines `sendReceipt` (auto-send helper) — new identifiers must be `resendReceipt`, `showReceiptDialog`, `receiptError` to avoid collision.
- No model changes → no migration.

---

### Task 1: Invoice payments list endpoint

**Files:**
- Modify: `backend/app/api/v1/routers/invoices.py` (new endpoint after `record_payment`)
- Test: `backend/tests/integration/test_invoice_payments_list.py`

**Interfaces:**
- Consumes: existing `Payment`, `Invoice` models; `PaymentOut` schema (all already imported in the router).
- Produces (Task 2 depends on it): `GET /api/v1/invoices/{invoice_id}/payments` → `200 list[PaymentOut]` ordered `payment_date` DESC, `created_at` DESC; `404` if invoice not found; `[]` if no payments.

- [ ] **Step 1: Write the failing tests**

Create `backend/tests/integration/test_invoice_payments_list.py`:

```python
"""GET /invoices/{invoice_id}/payments — payments list for the receipt dialog."""
from __future__ import annotations

from uuid import uuid4


def _record(client, invoice_id, amount, day):
    r = client.post(
        f"/api/v1/invoices/{invoice_id}/record-payment",
        json={"amount": amount, "payment_date": day, "payer_name": "Acme"},
    )
    assert r.status_code == 201, r.text
    return r.json()


def test_list_payments_newest_first(client, admin_session, seed_invoice):
    p_old = _record(client, seed_invoice, "100", "2026-08-01")
    p_new = _record(client, seed_invoice, "200", "2026-08-05")

    r = client.get(f"/api/v1/invoices/{seed_invoice}/payments")
    assert r.status_code == 200, r.text
    ids = [p["payment_id"] for p in r.json()]
    assert ids == [p_new["payment_id"], p_old["payment_id"]]


def test_list_payments_empty(client, admin_session, seed_invoice):
    r = client.get(f"/api/v1/invoices/{seed_invoice}/payments")
    assert r.status_code == 200, r.text
    assert r.json() == []


def test_list_payments_unknown_invoice_404(client, admin_session):
    r = client.get(f"/api/v1/invoices/{uuid4()}/payments")
    assert r.status_code == 404, r.text
```

- [ ] **Step 2: Run tests to verify they fail**

Run:
```bash
docker compose exec -T -e TEST_DATABASE_URL="postgresql+psycopg://billing:billing@db:5432/billing_test" backend pytest tests/integration/test_invoice_payments_list.py -v
```
Expected: FAIL (404/405 — endpoint does not exist). NOTE: the 404 test may pass trivially; the other two must fail.

- [ ] **Step 3: Add the endpoint**

In `backend/app/api/v1/routers/invoices.py`, after the `record_payment` endpoint. `select` is available via the existing `from sqlalchemy import ...` import line — verify, and add `select` to it if missing:

```python
@router.get("/{invoice_id}/payments", response_model=list[PaymentOut])
def list_invoice_payments(
    invoice_id: UUID,
    db: Session = Depends(get_db),
    _: User = Depends(current_admin),
) -> list[Payment]:
    """Payments recorded against this invoice, newest first (receipt dialog)."""
    if db.get(Invoice, invoice_id) is None:
        raise HTTPException(status_code=404, detail="invoice not found")
    return list(
        db.scalars(
            select(Payment)
            .where(Payment.invoice_id == invoice_id)
            .order_by(Payment.payment_date.desc(), Payment.created_at.desc())
        )
    )
```

- [ ] **Step 4: Run tests to verify they pass**

Same command as Step 2. Expected: 3 passed. Then run the receipt suites to confirm no regression:
```bash
docker compose exec -T -e TEST_DATABASE_URL="postgresql+psycopg://billing:billing@db:5432/billing_test" backend pytest tests/integration/test_send_receipt.py tests/integration/test_invoice_payments_list.py -v
```

- [ ] **Step 5: Commit**

```bash
git add backend/app/api/v1/routers/invoices.py backend/tests/integration/test_invoice_payments_list.py
git commit -m "feat(receipt): list invoice payments endpoint for receipt dialog"
```

---

### Task 2: SendReceiptDialog + invoice detail button

**Files:**
- Create: `frontend/src/components/SendReceiptDialog.vue`
- Modify: `frontend/src/views/InvoiceFormView.vue`

**Interfaces:**
- Consumes: `GET /invoices/{id}/payments` (Task 1); `POST /invoices/{id}/payments/{payment_id}/send-receipt` (existing); `formatAmount` from `@/utils/money`; `useAuthStore` from `@/stores/auth`.
- Produces: dialog emits `submit` payload `{ payment_id: string; to_email: string; cc_email: string | null }`.

- [ ] **Step 1: Create the dialog component**

Create `frontend/src/components/SendReceiptDialog.vue`:

```vue
<script setup lang="ts">
import { ref, watch, computed } from "vue";
import Dialog from "primevue/dialog";
import Button from "primevue/button";
import InputText from "primevue/inputtext";
import Dropdown from "primevue/dropdown";
import Message from "primevue/message";
import { useAuthStore } from "@/stores/auth";
import { formatAmount } from "@/utils/money";

export interface ReceiptPayment {
  payment_id: string;
  amount: string;
  currency: string;
  payment_date: string;
  payer_reference: string | null;
}

interface Props {
  visible: boolean;
  payments: ReceiptPayment[];
  defaultTo: string | null;
  loading?: boolean;
  error?: string | null;
}

const props = defineProps<Props>();
const emit = defineEmits<{
  (e: "update:visible", v: boolean): void;
  (e: "submit", payload: { payment_id: string; to_email: string; cc_email: string | null }): void;
}>();

const auth = useAuthStore();

const paymentId = ref<string | null>(null);
const toEmail = ref("");
const ccEmail = ref("");

const options = computed(() =>
  props.payments.map((p) => ({
    value: p.payment_id,
    label:
      `${p.payment_date} · ${formatAmount(p.amount, p.currency)} ${p.currency}` +
      (p.payer_reference ? ` · ${p.payer_reference}` : ""),
  })),
);

watch(
  () => props.visible,
  (v) => {
    if (v) {
      paymentId.value = props.payments[0]?.payment_id ?? null;
      toEmail.value = props.defaultTo ?? "";
      ccEmail.value = auth.user?.email ?? "";
    }
  },
  { immediate: true },
);

const canSubmit = computed(() => !!paymentId.value && !!toEmail.value.trim());

function close() {
  emit("update:visible", false);
}

function submit() {
  if (!canSubmit.value || !paymentId.value) return;
  emit("submit", {
    payment_id: paymentId.value,
    to_email: toEmail.value.trim(),
    cc_email: ccEmail.value.trim() || null,
  });
}
</script>

<template>
  <Dialog
    :visible="visible"
    header="Send payment receipt"
    modal
    :style="{ width: '520px' }"
    :closable="!loading"
    @update:visible="emit('update:visible', $event)"
  >
    <div class="send-grid">
      <label class="field">
        <span>Payment</span>
        <Dropdown
          v-model="paymentId"
          :options="options"
          option-label="label"
          option-value="value"
          placeholder="Select payment"
        />
      </label>
      <label class="field">
        <span>To</span>
        <InputText v-model="toEmail" placeholder="customer@example.com" />
      </label>
      <label class="field">
        <span>Cc <span class="muted">(optional)</span></span>
        <InputText v-model="ccEmail" />
      </label>
      <p class="note">
        <i class="pi pi-paperclip" /> The receipt PDF will be attached automatically.
      </p>
      <Message v-if="error" severity="error" :closable="false">{{ error }}</Message>
    </div>
    <template #footer>
      <Button label="Cancel" text :disabled="loading" @click="close" />
      <Button
        label="Send receipt"
        icon="pi pi-envelope"
        :loading="loading"
        :disabled="!canSubmit || loading"
        @click="submit"
      />
    </template>
  </Dialog>
</template>

<style scoped>
.send-grid { display: flex; flex-direction: column; gap: 0.75rem; padding: 0.25rem 0 0.5rem; }
.field { display: flex; flex-direction: column; gap: 0.3rem; font-size: 0.85rem; }
.field > span { font-weight: 600; color: var(--color-text); }
.field .muted { font-weight: 400; color: var(--color-text-muted); font-size: 0.78rem; }
.field :deep(.p-inputtext), .field :deep(.p-dropdown) { width: 100%; }
.note { font-size: 0.78rem; color: var(--color-text-muted); margin: 0; }
.note i { margin-right: 0.3rem; }
</style>
```

- [ ] **Step 2: Wire it into InvoiceFormView**

In `frontend/src/views/InvoiceFormView.vue`:

Add import next to the other dialog imports:

```ts
import SendReceiptDialog, { type ReceiptPayment } from "@/components/SendReceiptDialog.vue";
```

Add state + query + mutation near the existing `recordPayment` mutation (names must NOT collide with the existing `sendReceipt` helper):

```ts
const showReceiptDialog = ref(false);
const receiptError = ref<string | null>(null);
const canSendReceipt = computed(() => {
  const s = existing.value?.status;
  return s === "PAID" || s === "PARTIAL";
});

const { data: invoicePayments } = useQuery<ReceiptPayment[]>({
  queryKey: ["invoice-payments", props.id],
  queryFn: async () =>
    (await api.get<ReceiptPayment[]>(`/invoices/${props.id}/payments`)).data,
  enabled: () => !!props.id && canSendReceipt.value,
});

const resendReceipt = useMutation({
  mutationFn: async (payload: { payment_id: string; to_email: string; cc_email: string | null }) => {
    if (!props.id) throw new Error("no invoice id");
    const { payment_id, ...body } = payload;
    return (await api.post(`/invoices/${props.id}/payments/${payment_id}/send-receipt`, body)).data;
  },
  onSuccess: () => {
    receiptError.value = null;
    showReceiptDialog.value = false;
  },
  onError: (e: { response?: { data?: { detail?: string } } }) => {
    receiptError.value = e?.response?.data?.detail ?? "Failed to send receipt";
  },
});
```

Add the button in the read-only actions template block (after the "Mark as paid" button, inside `<template v-if="readOnly">`):

```html
          <Button
            v-if="canSendReceipt"
            label="Send receipt"
            icon="pi pi-envelope"
            severity="secondary"
            :disabled="!(invoicePayments?.length)"
            :title="invoicePayments?.length ? 'Email a receipt for a recorded payment' : 'No payments recorded'"
            @click="receiptError = null; showReceiptDialog = true"
          />
```

Add the dialog next to the existing `<RecordPaymentDialog>`/`<SendInvoiceDialog>` usages:

```html
    <SendReceiptDialog
      v-if="existing"
      v-model:visible="showReceiptDialog"
      :payments="invoicePayments ?? []"
      :default-to="selectedCustomer?.contact_email ?? null"
      :loading="resendReceipt.isPending.value"
      :error="receiptError"
      @submit="(payload) => resendReceipt.mutate(payload)"
    />
```

- [ ] **Step 3: Typecheck + build**

Run: `docker compose exec -T frontend npm run build`
Expected: clean. (If `useQuery` in this view uses a different queryKey/enabled callback style, match the file's existing conventions — see the `customerProjects` query near the top for the established pattern.)

- [ ] **Step 4: Commit**

```bash
git add frontend/src/components/SendReceiptDialog.vue frontend/src/views/InvoiceFormView.vue
git commit -m "feat(receipt): manual send-receipt button and dialog on invoice detail"
```

---

### Task 3: Verification

- [ ] **Step 1:** Full backend suite (same Docker command, bare `pytest`) — all pass.
- [ ] **Step 2:** `docker compose exec -T frontend npm run build` — clean.
- [ ] **Step 3:** Browser smoke: open the PAID invoice WH20260004 → "Send receipt" button visible; dialog lists its payment, To/Cc prefilled; Send → 200; button absent on a SENT invoice with no payments.
