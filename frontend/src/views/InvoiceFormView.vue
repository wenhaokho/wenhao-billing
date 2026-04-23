<script setup lang="ts">
import { ref, computed, watch } from "vue";
import { useRouter } from "vue-router";
import { useQuery, useMutation, useQueryClient } from "@tanstack/vue-query";
import Button from "primevue/button";
import Dropdown from "primevue/dropdown";
import InputText from "primevue/inputtext";
import InputNumber from "primevue/inputnumber";
import DatePicker from "primevue/calendar";
import Textarea from "primevue/textarea";
import Message from "primevue/message";
import Tag from "primevue/tag";
import SelectButton from "primevue/selectbutton";
import { api } from "@/api/client";
import { formatAmount } from "@/utils/money";
import {
  useInvoiceForm,
  emptyLineItem,
  type LineItemRow,
  type DiscountType,
} from "@/composables/useInvoiceForm";
import InvoiceLineItemsTable from "@/components/InvoiceLineItemsTable.vue";
import RecordPaymentDialog from "@/components/RecordPaymentDialog.vue";
import SendInvoiceDialog from "@/components/SendInvoiceDialog.vue";

const props = defineProps<{ id?: string }>();
const router = useRouter();

interface Customer {
  customer_id: string;
  name: string;
  contact_name: string | null;
  contact_first_name: string | null;
  contact_last_name: string | null;
  contact_email: string | null;
  contact_phone: string | null;
  billing_address: string | null;
  billing_address1: string | null;
  billing_address2: string | null;
  billing_city: string | null;
  billing_state: string | null;
  billing_postal_code: string | null;
  billing_country: string | null;
  default_currency: string | null;
  ship_to_name: string | null;
}

function composedContactName(c: Customer): string | null {
  const f = (c.contact_first_name ?? "").trim();
  const l = (c.contact_last_name ?? "").trim();
  const composed = [f, l].filter(Boolean).join(" ");
  return composed || c.contact_name || null;
}

function composedBillingAddress(c: Customer): string | null {
  const structured = [
    c.billing_address1,
    c.billing_address2,
    [c.billing_city, c.billing_state, c.billing_postal_code].filter(Boolean).join(", "),
    c.billing_country,
  ]
    .map((v) => (v ?? "").trim())
    .filter(Boolean)
    .join("\n");
  return structured || c.billing_address || null;
}

interface CatalogItem {
  item_id: string;
  name: string;
  description: string | null;
  default_unit_price: string | null;
  default_currency: string;
}

interface ProjectLite {
  project_id: string;
  customer_id: string;
  code: string;
  name: string;
  currency: string;
  status: string;
}

interface InvoiceOut {
  invoice_id: string;
  customer_id: string | null;
  project_id: string | null;
  invoice_type: string;
  invoice_number: string | null;
  po_so_number: string | null;
  currency: string;
  subtotal: string | null;
  discount_type: string | null;
  discount_value: string | null;
  amount: string;
  balance_due: string;
  status: string;
  issue_date: string | null;
  due_date: string | null;
  payment_terms: string | null;
  notes: string | null;
  footer: string | null;
  is_template: boolean;
  line_items: {
    line_item_id: string;
    item_id: string | null;
    description: string;
    quantity: string;
    unit_price: string;
    amount: string;
    position: number;
  }[];
}

const currencyOptions = ["IDR", "SGD", "USD"];

type InvoiceKind = "STANDARD" | "MILESTONE";
const invoiceKind = ref<InvoiceKind>("STANDARD");
const kindOptions = [
  { label: "Standard", value: "STANDARD" },
  { label: "Milestone", value: "MILESTONE" },
];
// Milestone-only fields
const milestoneRef = ref<string>("");
const milestoneAmount = ref<number | null>(null);
const dueInDays = ref<number>(14);

const customerId = ref<string | null>(null);
const projectId = ref<string | null>(null);
const invoiceNumber = ref<string | null>(null);
const poSoNumber = ref<string>("");
const issueDate = ref<Date | null>(new Date());
const dueDate = ref<Date | null>(null);
const currency = ref<string>("IDR");
const notes = ref<string>("");
const paymentTerms = ref<string>("On Receipt");

const discountEnabled = ref(false);
const discountType = ref<DiscountType | null>(null);
const discountValue = ref<number | null>(null);

const { lineItems, addLine, removeLine, subtotal, total } = useInvoiceForm({
  currency,
  discountType,
  discountValue,
});

const isEdit = computed(() => !!props.id);

const { data: customers } = useQuery<Customer[]>({
  queryKey: ["customers"],
  queryFn: async () => (await api.get<Customer[]>("/customers")).data,
});

const { data: customerProjects } = useQuery<ProjectLite[]>({
  queryKey: ["projects-by-customer", customerId],
  queryFn: async () => {
    if (!customerId.value) return [];
    return (
      await api.get<ProjectLite[]>("/projects", {
        params: { customer_id: customerId.value, status: "ACTIVE" },
      })
    ).data;
  },
  enabled: () => !!customerId.value,
});

const { data: catalog } = useQuery<CatalogItem[]>({
  queryKey: ["items"],
  queryFn: async () => (await api.get<CatalogItem[]>("/items")).data,
});

const { data: existing } = useQuery<InvoiceOut | null>({
  queryKey: ["invoice", props.id],
  enabled: isEdit,
  queryFn: async () => {
    if (!props.id) return null;
    return (await api.get<InvoiceOut>(`/invoices/${props.id}`)).data;
  },
});

const { data: businessProfile } = useQuery<{ default_notes: string | null }>({
  queryKey: ["business-profile"],
  queryFn: async () => (await api.get("/business-profile")).data,
});

watch(businessProfile, (bp) => {
  if (!isEdit.value && bp?.default_notes && !notes.value) {
    notes.value = bp.default_notes;
  }
}, { immediate: true });

const readOnly = computed(() => !!(existing.value?.status && existing.value.status !== "DRAFT"));

const queryClient = useQueryClient();
const showPaymentDialog = ref(false);
const paymentError = ref<string | null>(null);
const canRecordPayment = computed(() => {
  const s = existing.value?.status;
  return s === "SENT" || s === "PARTIAL";
});
const recordPayment = useMutation({
  mutationFn: async (payload: {
    amount: number;
    payment_date: string;
    payer_name: string | null;
    payer_reference: string | null;
    notes: string | null;
  }) => {
    if (!props.id) throw new Error("no invoice id");
    return (await api.post(`/invoices/${props.id}/record-payment`, payload)).data;
  },
  onSuccess: () => {
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

const showSendDialog = ref(false);
const sendError = ref<string | null>(null);
const canSendInvoice = computed(() => {
  const s = existing.value?.status;
  return s === "DRAFT" || s === "SENT" || s === "PARTIAL";
});

function openPdfPreview() {
  if (!props.id) return;
  window.open(`/api/v1/invoices/${props.id}/pdf`, "_blank");
}

const sendInvoice = useMutation({
  mutationFn: async (payload: {
    to_email: string;
    cc_email: string | null;
    subject: string;
    message: string;
  }) => {
    if (!props.id) throw new Error("no invoice id");
    return (await api.post(`/invoices/${props.id}/send`, payload)).data;
  },
  onSuccess: () => {
    sendError.value = null;
    showSendDialog.value = false;
    queryClient.invalidateQueries({ queryKey: ["invoice", props.id] });
    queryClient.invalidateQueries({ queryKey: ["invoices"] });
  },
  onError: (e: { response?: { data?: { detail?: string } } }) => {
    sendError.value = e?.response?.data?.detail ?? "Failed to send invoice";
  },
});

const sendDefaults = computed(() => {
  const inv = existing.value;
  const cust = selectedCustomer.value;
  const num = inv?.invoice_number ?? "";
  return {
    to: cust?.contact_email ?? null,
    subject: num ? `Invoice ${num}` : "Invoice",
    message:
      `Hi ${cust?.name ?? "there"},\n\n` +
      `Please find attached invoice ${num}` +
      (inv ? ` for ${inv.amount} ${inv.currency}` : "") + ".\n" +
      (inv?.due_date ? `Due date: ${inv.due_date}.\n` : "") +
      `\nThank you.`,
  };
});

function statusSeverity(status: string) {
  switch (status) {
    case "PAID": return "success";
    case "SENT": return "info";
    case "PARTIAL": return "warning";
    case "VOID": return "secondary";
    case "DRAFT": return "contrast";
    default: return undefined;
  }
}

const selectedCustomer = computed(() => {
  if (!customerId.value) return null;
  return (customers.value ?? []).find((c) => c.customer_id === customerId.value) ?? null;
});

// Auto-default currency from selected customer on new invoices
watch(customerId, (newId, oldId) => {
  // If customer changed, clear project pick so we don't keep a stale cross-customer project
  if (newId !== oldId) projectId.value = null;
  if (isEdit.value) return;
  const c = (customers.value ?? []).find((c) => c.customer_id === newId);
  if (c?.default_currency) currency.value = c.default_currency;
});

// When a project is picked, inherit its currency (only for new invoices)
watch(projectId, (pid) => {
  if (isEdit.value || !pid) return;
  const p = (customerProjects.value ?? []).find((x) => x.project_id === pid);
  if (p?.currency) currency.value = p.currency;
});

const customerPickerRef = ref<InstanceType<typeof Dropdown> | null>(null);
const pickerOpen = ref(false);
function focusCustomerPicker() {
  pickerOpen.value = true;
  // Wait for the dropdown to mount, then open its panel
  setTimeout(() => customerPickerRef.value?.show?.(), 0);
}
// Auto-close the inline picker once a customer is chosen
watch(customerId, () => {
  pickerOpen.value = false;
});

watch(existing, (inv) => {
  if (!inv) return;
  customerId.value = inv.customer_id;
  projectId.value = inv.project_id;
  invoiceNumber.value = inv.invoice_number;
  poSoNumber.value = inv.po_so_number ?? "";
  issueDate.value = inv.issue_date ? new Date(inv.issue_date) : null;
  dueDate.value = inv.due_date ? new Date(inv.due_date) : null;
  currency.value = inv.currency;
  notes.value = inv.notes ?? "";
  paymentTerms.value = inv.payment_terms ?? "On Receipt";
  discountType.value = (inv.discount_type as DiscountType | null) ?? null;
  discountValue.value = inv.discount_value != null ? Number(inv.discount_value) : null;
  discountEnabled.value = !!inv.discount_type;
  if (inv.line_items?.length) {
    lineItems.value = inv.line_items
      .slice()
      .sort((a, b) => a.position - b.position)
      .map((ln, idx) => ({
        item_id: ln.item_id,
        description: ln.description,
        quantity: Number(ln.quantity),
        unit_price: Number(ln.unit_price),
        position: ln.position ?? idx,
      }));
  } else {
    lineItems.value = [emptyLineItem(0)];
  }
});

function toISODate(d: Date | null): string | null {
  if (!d) return null;
  const yyyy = d.getFullYear();
  const mm = String(d.getMonth() + 1).padStart(2, "0");
  const dd = String(d.getDate()).padStart(2, "0");
  return `${yyyy}-${mm}-${dd}`;
}

function buildPayload() {
  const clean = (lineItems.value as LineItemRow[])
    .filter((ln) => (ln.description && ln.description.trim()) || ln.item_id)
    .map((ln, idx) => ({
      item_id: ln.item_id,
      description: ln.description,
      quantity: Number(ln.quantity ?? 0),
      unit_price: Number(ln.unit_price ?? 0),
      position: idx,
    }));
  return {
    customer_id: customerId.value,
    project_id: projectId.value,
    invoice_type: "MILESTONE",
    currency: currency.value,
    invoice_number: invoiceNumber.value || null,
    po_so_number: poSoNumber.value || null,
    issue_date: toISODate(issueDate.value),
    due_date: toISODate(dueDate.value),
    payment_terms: paymentTerms.value || null,
    notes: notes.value || null,
    discount_type: discountEnabled.value ? discountType.value : null,
    discount_value: discountEnabled.value ? discountValue.value : null,
    line_items: clean,
  };
}

function buildMilestonePayload() {
  return {
    customer_id: customerId.value,
    project_id: projectId.value,
    currency: currency.value,
    amount: milestoneAmount.value,
    milestone_ref: milestoneRef.value,
    issue_date: toISODate(issueDate.value),
    due_in_days: dueInDays.value,
  };
}

const save = useMutation({
  mutationFn: async () => {
    if (isEdit.value) {
      return (await api.patch<InvoiceOut>(`/invoices/${props.id}`, buildPayload())).data;
    }
    if (invoiceKind.value === "MILESTONE") {
      return (await api.post<InvoiceOut>("/invoices/milestone", buildMilestonePayload())).data;
    }
    return (await api.post<InvoiceOut>("/invoices", buildPayload())).data;
  },
  onSuccess: () => {
    router.push("/invoices");
  },
});

const canSave = computed(() => {
  if (!currency.value) return false;
  if (!isEdit.value && invoiceKind.value === "MILESTONE") {
    return !!customerId.value && !!milestoneRef.value && !!milestoneAmount.value;
  }
  return lineItems.value.some((ln) => (ln.description?.trim() || ln.item_id));
});
</script>

<template>
  <section class="invoice-form">
    <div class="back">
      <Button label="Invoices" icon="pi pi-arrow-left" text size="small" @click="router.push('/invoices')" />
    </div>

    <header class="page-header sticky-header">
      <div class="header-title">
        <h1>{{ isEdit ? "Edit invoice" : "New invoice" }}</h1>
        <p v-if="readOnly" class="readonly-note">
          Read-only — {{ existing?.status }} invoices cannot be edited
        </p>
        <SelectButton
          v-if="!isEdit && !readOnly"
          v-model="invoiceKind"
          :options="kindOptions"
          option-label="label"
          option-value="value"
          :allow-empty="false"
          class="kind-picker"
        />
      </div>
      <div class="page-actions">
        <Tag
          v-if="existing?.status"
          :value="existing.status"
          :severity="statusSeverity(existing.status)"
        />
        <Button
          label="Preview PDF"
          icon="pi pi-file-pdf"
          text
          :disabled="!isEdit"
          :title="isEdit ? 'Preview PDF' : 'Save first to preview'"
          @click="openPdfPreview"
        />
        <template v-if="readOnly">
          <Button
            v-if="canSendInvoice"
            label="Send invoice"
            icon="pi pi-send"
            @click="sendError = null; showSendDialog = true"
          />
          <Button
            v-if="canRecordPayment"
            label="Record payment"
            icon="pi pi-wallet"
            severity="success"
            @click="paymentError = null; showPaymentDialog = true"
          />
        </template>
        <template v-else>
          <Button
            :label="isEdit ? 'Save changes' : 'Save as DRAFT'"
            icon="pi pi-save"
            :loading="save.isPending.value"
            :disabled="!canSave"
            @click="save.mutate()"
          />
        </template>
      </div>
    </header>

    <div class="top-grid">
      <!-- Customer card -->
      <div class="card customer-card" :class="{ 'is-empty': !selectedCustomer && !readOnly }">
        <template v-if="selectedCustomer">
          <div class="customer-filled">
            <div class="customer-header">
              <div class="customer-label">Bill to</div>
              <Button
                v-if="!readOnly && !pickerOpen"
                label="Edit"
                icon="pi pi-pencil"
                text
                size="small"
                class="customer-edit"
                @click="focusCustomerPicker"
              />
              <Button
                v-if="pickerOpen"
                label="Cancel"
                icon="pi pi-times"
                text
                size="small"
                class="customer-edit"
                @click="pickerOpen = false"
              />
            </div>
            <template v-if="!pickerOpen">
              <div class="customer-name">{{ selectedCustomer.name }}</div>
              <div v-if="composedContactName(selectedCustomer)" class="customer-detail">
                <i class="pi pi-user" />
                <span>{{ composedContactName(selectedCustomer) }}</span>
              </div>
              <div v-if="selectedCustomer.contact_email" class="customer-detail">
                <i class="pi pi-envelope" />
                <a :href="`mailto:${selectedCustomer.contact_email}`">{{ selectedCustomer.contact_email }}</a>
              </div>
              <div v-if="selectedCustomer.contact_phone" class="customer-detail">
                <i class="pi pi-phone" />
                <span>{{ selectedCustomer.contact_phone }}</span>
              </div>
              <div v-if="composedBillingAddress(selectedCustomer)" class="customer-detail address">
                <i class="pi pi-map-marker" />
                <span>{{ composedBillingAddress(selectedCustomer) }}</span>
              </div>
            </template>
            <Dropdown
              v-else
              ref="customerPickerRef"
              v-model="customerId"
              :options="customers ?? []"
              option-label="name"
              option-value="customer_id"
              placeholder="Choose a customer"
              filter
              show-clear
              :disabled="readOnly"
              class="customer-dropdown-inline"
            />
          </div>
        </template>
        <template v-else>
          <div class="customer-empty">
            <i class="pi pi-user-plus" />
            <div class="customer-empty-title">Add a customer</div>
            <Dropdown
              v-model="customerId"
              :options="customers ?? []"
              option-label="name"
              option-value="customer_id"
              placeholder="Choose a customer"
              filter
              show-clear
              :disabled="readOnly"
              class="customer-dropdown"
            />
          </div>
        </template>
      </div>

      <!-- Meta card -->
      <div class="card meta-card">
        <div class="meta-grid">
          <label class="meta-label">Invoice number</label>
          <InputText
            v-model="invoiceNumber"
            :placeholder="`Auto (WH${new Date().getFullYear()}####)`"
            :disabled="readOnly"
          />

          <label class="meta-label">Project</label>
          <Dropdown
            v-model="projectId"
            :options="customerProjects ?? []"
            :option-label="(p: ProjectLite) => `${p.code} · ${p.name}`"
            option-value="project_id"
            placeholder="(none)"
            :disabled="readOnly || !customerId"
            show-clear
            filter
          />

          <label class="meta-label">P.O. / S.O. number</label>
          <InputText v-model="poSoNumber" :disabled="readOnly" />

          <label class="meta-label">Invoice date</label>
          <DatePicker v-model="issueDate" date-format="yy-mm-dd" show-icon :disabled="readOnly" />

          <label class="meta-label">Payment due</label>
          <div class="meta-stack">
            <DatePicker v-model="dueDate" date-format="yy-mm-dd" show-icon :disabled="readOnly" />
            <span class="helper">On Receipt if left blank</span>
          </div>
        </div>
      </div>
    </div>

    <!-- Milestone-only: simplified amount + reference -->
    <div
      v-if="!isEdit && invoiceKind === 'MILESTONE'"
      class="card card-pad milestone-card"
    >
      <div class="milestone-title">Milestone</div>
      <div class="milestone-grid">
        <label class="field">
          <span>Milestone reference</span>
          <InputText v-model="milestoneRef" placeholder="e.g. Phase 2 kickoff" />
        </label>
        <label class="field">
          <span>Amount</span>
          <InputNumber
            v-model="milestoneAmount"
            mode="decimal"
            :min-fraction-digits="currency === 'IDR' ? 0 : 2"
            :max-fraction-digits="currency === 'IDR' ? 0 : 2"
            :use-grouping="true"
          />
        </label>
        <label class="field">
          <span>Due in (days)</span>
          <InputNumber v-model="dueInDays" :min="0" :max="365" />
        </label>
        <label class="field">
          <span>Currency</span>
          <Dropdown v-model="currency" :options="currencyOptions" />
        </label>
      </div>
    </div>

    <InvoiceLineItemsTable
      v-if="isEdit || invoiceKind === 'STANDARD'"
      v-model="lineItems"
      :catalog="catalog ?? []"
      :currency="currency"
      :read-only="readOnly"
      @add="addLine"
      @remove="(i) => removeLine(i)"
    />

    <div v-if="isEdit || invoiceKind === 'STANDARD'" class="totals-wrap">
      <div class="card totals">
        <div class="totals-row">
          <div class="totals-label">Subtotal</div>
          <div class="totals-value num">{{ formatAmount(subtotal, currency) }}</div>
        </div>

        <div v-if="!discountEnabled" class="totals-row">
          <Button
            v-if="!readOnly"
            label="Add a discount"
            text
            icon="pi pi-plus"
            size="small"
            @click="discountEnabled = true; discountType = 'PERCENT'"
          />
        </div>
        <div v-else class="totals-row discount">
          <div class="discount-input">
            <Dropdown
              v-model="discountType"
              :options="[{ label: '%', value: 'PERCENT' }, { label: 'Amount', value: 'AMOUNT' }]"
              option-label="label"
              option-value="value"
              class="discount-type"
              :disabled="readOnly"
            />
            <InputNumber
              v-model="discountValue"
              mode="decimal"
              :min="0"
              :max-fraction-digits="4"
              class="discount-value"
              :disabled="readOnly"
            />
            <Button
              v-if="!readOnly"
              icon="pi pi-times"
              text
              rounded
              size="small"
              @click="discountEnabled = false; discountType = null; discountValue = null"
            />
          </div>
        </div>

        <div class="totals-row total">
          <div class="totals-label">
            <Dropdown
              v-model="currency"
              :options="currencyOptions"
              class="ccy-select"
              :disabled="readOnly"
            />
            <span>Total</span>
          </div>
          <div class="totals-value num">{{ formatAmount(total, currency) }}</div>
        </div>

        <div class="totals-row due">
          <div class="totals-label">Amount Due</div>
          <div class="totals-value num">{{ formatAmount(total, currency) }}</div>
        </div>
      </div>
    </div>

    <div class="card notes-card">
      <label class="section-label">Notes / Terms</label>
      <Textarea
        v-model="notes"
        rows="5"
        placeholder="e.g. Price is included of 11% VAT. Price is excluded of any bank or transfer fees."
        class="notes-textarea"
        :disabled="readOnly"
      />
    </div>

    <Message v-if="save.error.value" severity="error" :closable="false">
      {{ (save.error.value as any)?.response?.data?.detail ?? 'Save failed' }}
    </Message>

    <RecordPaymentDialog
      v-if="existing"
      v-model:visible="showPaymentDialog"
      :balance-due="existing.balance_due"
      :currency="existing.currency"
      mode="AR"
      :loading="recordPayment.isPending.value"
      :error="paymentError"
      @submit="recordPayment.mutate($event)"
    />

    <SendInvoiceDialog
      v-if="existing"
      v-model:visible="showSendDialog"
      :default-to="sendDefaults.to"
      :default-subject="sendDefaults.subject"
      :default-message="sendDefaults.message"
      :loading="sendInvoice.isPending.value"
      :error="sendError"
      @submit="sendInvoice.mutate($event)"
    />
  </section>
</template>

<style scoped>
.invoice-form {
  display: flex;
  flex-direction: column;
  gap: 1.25rem;
}

.back { margin-bottom: -0.5rem; }

.kind-picker { margin-top: 0.5rem; }
.kind-picker :deep(.p-button) { font-size: 0.82rem; padding: 0.4rem 0.9rem; }

.milestone-card { display: flex; flex-direction: column; gap: 0.75rem; }
.milestone-title { font-weight: 600; font-size: 0.85rem; color: var(--color-text-muted); text-transform: uppercase; letter-spacing: 0.05em; }
.milestone-grid { display: grid; grid-template-columns: 2fr 1fr 1fr 1fr; gap: 1rem; }
.milestone-grid .field { display: flex; flex-direction: column; gap: 0.3rem; font-size: 0.875rem; }
.milestone-grid .field > span { font-weight: 600; font-size: 0.8rem; color: var(--color-text); }
.milestone-grid :deep(.p-inputtext),
.milestone-grid :deep(.p-inputnumber),
.milestone-grid :deep(.p-inputnumber-input),
.milestone-grid :deep(.p-dropdown) { width: 100%; }
@media (max-width: 800px) { .milestone-grid { grid-template-columns: 1fr 1fr; } }

.sticky-header {
  position: sticky;
  top: 0;
  z-index: 10;
  background: var(--color-bg, rgba(248, 250, 252, 0.85));
  padding: 1rem 0;
  backdrop-filter: blur(6px);
  -webkit-backdrop-filter: blur(6px);
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 1rem;
}
.header-title h1 {
  margin: 0;
}
.readonly-note {
  margin: 0.25rem 0 0;
  font-size: 0.78rem;
  color: var(--color-text-muted);
}
.page-actions {
  display: flex;
  gap: 0.5rem;
  align-items: center;
}

.card {
  background: var(--color-surface, #ffffff);
  border: 1px solid var(--color-border, #e2e8f0);
  border-radius: 12px;
  padding: 1.5rem 1.75rem;
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.04);
}

.top-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 1.25rem;
  align-items: stretch;
}

/* Customer card */
.customer-card {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 160px;
  position: relative;
}
.customer-card.is-empty {
  border: 1.5px dashed #cbd5e1;
  background: var(--color-surface-alt);
}
.customer-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 0.6rem;
  color: var(--color-text-muted);
  width: 100%;
}
.customer-empty .pi {
  font-size: 1.6rem;
  color: var(--color-text-subtle);
}
.customer-empty-title {
  font-size: 0.95rem;
  font-weight: 600;
  color: var(--color-text);
}
.customer-dropdown {
  width: min(280px, 100%);
}
/* The "filled" state renders the dropdown invisibly but KEEPS it in layout,
   anchored at the top-right of the card (near the Edit button). PrimeVue's
   overlay positions relative to the trigger's bounding rect — if we use
   display:none the rect is 0×0 at the viewport origin and the panel pops
   in the top-left corner. */
/* Inline picker replaces the customer details in-place when Edit is clicked.
   Width is enforced so the overlay anchors to a proper-sized trigger. */
.customer-dropdown-inline {
  width: 100%;
  margin-top: 0.25rem;
}
.customer-dropdown-inline :deep(.p-dropdown) {
  width: 100%;
}
.customer-filled {
  width: 100%;
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
}
.customer-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}
.customer-label {
  font-size: 0.72rem;
  font-weight: 500;
  color: var(--color-text-muted);
  letter-spacing: 0.06em;
  text-transform: uppercase;
}
.customer-name {
  font-size: 1.2rem;
  font-weight: 600;
  color: var(--color-text);
  margin-bottom: 0.25rem;
}
.customer-detail {
  display: flex;
  gap: 0.5rem;
  align-items: flex-start;
  font-size: 0.875rem;
  color: var(--color-text-muted);
  line-height: 1.35;
}
.customer-detail i {
  color: var(--color-text-subtle);
  font-size: 0.85rem;
  margin-top: 0.15rem;
  min-width: 14px;
}
.customer-detail a {
  color: #2563eb;
  text-decoration: none;
}
.customer-detail a:hover { text-decoration: underline; }
.customer-detail.address span { white-space: pre-line; }
.customer-edit {
  padding: 0.25rem 0.5rem;
}

/* Meta card */
.meta-card {
  display: flex;
  flex-direction: column;
}
.meta-grid {
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: 0.75rem 1rem;
  align-items: center;
}
.meta-label {
  font-size: 0.8rem;
  font-weight: 500;
  color: var(--color-text-muted);
  letter-spacing: 0.01em;
  text-align: right;
}
.meta-stack {
  display: flex;
  flex-direction: column;
  gap: 0.2rem;
}
.helper {
  font-size: 0.72rem;
  color: var(--color-text-subtle);
}
.meta-grid :deep(.p-inputtext),
.meta-grid :deep(.p-datepicker-input),
.meta-grid :deep(.p-calendar) {
  width: 100%;
}

/* Totals */
.totals-wrap {
  display: flex;
  justify-content: flex-end;
}
.totals {
  max-width: 380px;
  width: 100%;
  margin-left: auto;
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
  padding: 1.25rem 1.5rem;
}
.totals-row {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 0.75rem;
  min-height: 2rem;
}
.totals-label {
  color: var(--color-text-muted);
  font-size: 0.85rem;
  display: flex;
  gap: 0.5rem;
  align-items: center;
}
.ccy-select {
  width: 84px;
  flex: 0 0 auto;
}
.ccy-select :deep(.p-dropdown-label) {
  padding: 0.35rem 0.5rem;
  font-size: 0.82rem;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}
.ccy-select :deep(.p-dropdown-trigger) {
  width: 1.75rem;
}
.totals-value {
  font-variant-numeric: tabular-nums;
  color: var(--color-text);
  text-align: right;
}
.totals-row.total {
  border-top: 1px solid var(--color-border, #e2e8f0);
  padding-top: 0.75rem;
  margin-top: 0.25rem;
  font-size: 1rem;
  font-weight: 600;
}
.totals-row.total .totals-label {
  color: var(--color-text);
  font-size: 1rem;
}
.totals-row.due {
  margin-top: 0.25rem;
  padding: 0.75rem 0.9rem;
  border-top: 3px double #cbd5e1;
  background: #eff6ff;
  border-radius: 8px;
  font-size: 1.125rem;
  font-weight: 700;
  color: var(--color-text);
}
.totals-row.due .totals-label {
  color: var(--color-text);
  font-weight: 700;
  font-size: 1rem;
}
.discount-input {
  display: flex;
  gap: 0.5rem;
  align-items: center;
  width: 100%;
}
.discount-type { width: 110px; }
.discount-value { flex: 1; }
.discount-value :deep(.p-inputnumber),
.discount-value :deep(.p-inputnumber-input) {
  width: 100%;
}
.ccy-select { width: 90px; }
.num { font-variant-numeric: tabular-nums; }

/* Notes */
.notes-card {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}
.section-label {
  font-size: 0.95rem;
  font-weight: 600;
  color: var(--color-text);
  letter-spacing: 0.01em;
}
.notes-textarea {
  width: 100%;
}
</style>
