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
import { api } from "@/api/client";
import { formatAmount } from "@/utils/money";
import {
  useInvoiceForm,
  emptyLineItem,
  type LineItemRow,
  type DiscountType,
} from "@/composables/useInvoiceForm";
import InvoiceLineItemsTable from "@/components/InvoiceLineItemsTable.vue";

const props = defineProps<{ quotationId?: string }>();
const router = useRouter();
const queryClient = useQueryClient();

interface Customer {
  customer_id: string;
  name: string;
  contact_email: string | null;
  default_currency: string | null;
}

interface ProjectLite {
  project_id: string;
  customer_id: string;
  code: string;
  name: string;
  currency: string;
  status: string;
}

interface CatalogItem {
  item_id: string;
  name: string;
  description: string | null;
  default_unit_price: string | null;
  default_currency: string;
}

interface QuotationOut {
  quotation_id: string;
  customer_id: string;
  project_id: string | null;
  quotation_number: string | null;
  po_so_number: string | null;
  currency: string;
  subtotal: string | null;
  discount_type: string | null;
  discount_value: string | null;
  amount: string;
  status: string;
  issue_date: string | null;
  valid_until: string | null;
  payment_terms: string | null;
  notes: string | null;
  footer: string | null;
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

const customerId = ref<string | null>(null);
const projectId = ref<string | null>(null);
const quotationNumber = ref<string | null>(null);
const poSoNumber = ref<string>("");
const issueDate = ref<Date | null>(new Date());
const validUntil = ref<Date | null>(null);
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

const isEdit = computed(() => !!props.quotationId);

const { data: customers } = useQuery<Customer[]>({
  queryKey: ["customers"],
  queryFn: async () => (await api.get<Customer[]>("/customers")).data,
});

const { data: catalog } = useQuery<CatalogItem[]>({
  queryKey: ["items"],
  queryFn: async () => (await api.get<CatalogItem[]>("/items")).data,
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

const { data: existing } = useQuery<QuotationOut | null>({
  queryKey: ["quotation", props.quotationId],
  enabled: isEdit,
  queryFn: async () => {
    if (!props.quotationId) return null;
    return (await api.get<QuotationOut>(`/quotations/${props.quotationId}`)).data;
  },
});

const readOnly = computed(() => {
  const s = existing.value?.status;
  return !!(s && s !== "DRAFT" && s !== "SENT");
});

// Default valid-until to today+30 on fresh creates
watch(issueDate, (d) => {
  if (isEdit.value) return;
  if (d && !validUntil.value) {
    const v = new Date(d);
    v.setDate(v.getDate() + 30);
    validUntil.value = v;
  }
}, { immediate: true });

// Auto-default currency from customer on new
watch(customerId, (newId, oldId) => {
  if (newId !== oldId) projectId.value = null;
  if (isEdit.value) return;
  const c = (customers.value ?? []).find((c) => c.customer_id === newId);
  if (c?.default_currency) currency.value = c.default_currency;
});

watch(projectId, (pid) => {
  if (isEdit.value || !pid) return;
  const p = (customerProjects.value ?? []).find((x) => x.project_id === pid);
  if (p?.currency) currency.value = p.currency;
});

watch(existing, (q) => {
  if (!q) return;
  customerId.value = q.customer_id;
  projectId.value = q.project_id;
  quotationNumber.value = q.quotation_number;
  poSoNumber.value = q.po_so_number ?? "";
  issueDate.value = q.issue_date ? new Date(q.issue_date) : null;
  validUntil.value = q.valid_until ? new Date(q.valid_until) : null;
  currency.value = q.currency;
  notes.value = q.notes ?? "";
  paymentTerms.value = q.payment_terms ?? "On Receipt";
  discountType.value = (q.discount_type as DiscountType | null) ?? null;
  discountValue.value = q.discount_value != null ? Number(q.discount_value) : null;
  discountEnabled.value = !!q.discount_type;
  if (q.line_items?.length) {
    lineItems.value = q.line_items
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
    currency: currency.value,
    quotation_number: quotationNumber.value || null,
    po_so_number: poSoNumber.value || null,
    issue_date: toISODate(issueDate.value),
    valid_until: toISODate(validUntil.value),
    payment_terms: paymentTerms.value || null,
    notes: notes.value || null,
    discount_type: discountEnabled.value ? discountType.value : null,
    discount_value: discountEnabled.value ? discountValue.value : null,
    line_items: clean,
  };
}

const save = useMutation({
  mutationFn: async () => {
    if (isEdit.value) {
      return (await api.patch<QuotationOut>(`/quotations/${props.quotationId}`, buildPayload())).data;
    }
    return (await api.post<QuotationOut>("/quotations", buildPayload())).data;
  },
  onSuccess: (q) => {
    queryClient.invalidateQueries({ queryKey: ["quotations"] });
    queryClient.invalidateQueries({ queryKey: ["quotation", props.quotationId] });
    router.push(`/quotations/${q.quotation_id}`);
  },
});

const canSave = computed(() => {
  if (!currency.value || !customerId.value) return false;
  return lineItems.value.some((ln) => (ln.description?.trim() || ln.item_id));
});

const selectedCustomer = computed(
  () => (customers.value ?? []).find((c) => c.customer_id === customerId.value) ?? null,
);

function statusSeverity(status: string) {
  switch (status) {
    case "ACCEPTED": case "INVOICED": return "success";
    case "SENT": return "info";
    case "EXPIRED": return "warning";
    case "DECLINED": return "danger";
    case "VOID": return "secondary";
    case "DRAFT": return "contrast";
    default: return undefined;
  }
}

function openPdfPreview() {
  if (!props.quotationId) return;
  window.open(`/api/v1/quotations/${props.quotationId}/pdf`, "_blank", "noopener");
}
</script>

<template>
  <section class="quote-form">
    <div class="back">
      <Button label="Quotations" icon="pi pi-arrow-left" text size="small" @click="router.push('/quotations')" />
    </div>

    <header class="page-header">
      <div class="header-title">
        <h1>{{ isEdit ? "Edit quotation" : "New quotation" }}</h1>
        <p v-if="readOnly" class="readonly-note">
          Read-only — {{ existing?.status }} quotations cannot be edited
        </p>
      </div>
      <div class="page-actions">
        <Tag v-if="existing?.status" :value="existing.status" :severity="statusSeverity(existing.status)" />
        <Button
          label="Preview PDF"
          icon="pi pi-file-pdf"
          text
          :disabled="!isEdit"
          :title="isEdit ? 'Preview PDF' : 'Save first to preview'"
          @click="openPdfPreview"
        />
        <Button
          v-if="!readOnly"
          :label="isEdit ? 'Save changes' : 'Save as DRAFT'"
          icon="pi pi-save"
          :loading="save.isPending.value"
          :disabled="!canSave"
          @click="save.mutate()"
        />
      </div>
    </header>

    <div class="top-grid">
      <div class="card card-pad">
        <div class="section-label">Bill to</div>
        <Dropdown
          v-model="customerId"
          :options="customers ?? []"
          option-label="name"
          option-value="customer_id"
          placeholder="Choose a customer"
          filter
          show-clear
          :disabled="readOnly"
          class="full-w"
        />
        <div v-if="selectedCustomer" class="cust-meta">
          <span v-if="selectedCustomer.contact_email">{{ selectedCustomer.contact_email }}</span>
        </div>
      </div>

      <div class="card card-pad">
        <div class="meta-grid">
          <label class="meta-label">Quote #</label>
          <InputText v-model="quotationNumber" :placeholder="`Auto (Q${new Date().getFullYear()}####)`" :disabled="readOnly" />

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

          <label class="meta-label">P.O. / S.O. #</label>
          <InputText v-model="poSoNumber" :disabled="readOnly" />

          <label class="meta-label">Issue date</label>
          <DatePicker v-model="issueDate" date-format="yy-mm-dd" show-icon :disabled="readOnly" />

          <label class="meta-label">Valid until</label>
          <DatePicker v-model="validUntil" date-format="yy-mm-dd" show-icon :disabled="readOnly" />
        </div>
      </div>
    </div>

    <InvoiceLineItemsTable
      v-model="lineItems"
      :catalog="catalog ?? []"
      :currency="currency"
      :read-only="readOnly"
      @add="addLine"
      @remove="(i) => removeLine(i)"
    />

    <div class="totals-wrap">
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
      </div>
    </div>

    <div class="card card-pad">
      <label class="section-label">Notes / Terms</label>
      <Textarea
        v-model="notes"
        rows="5"
        placeholder="Scope, assumptions, payment terms..."
        class="full-w"
        :disabled="readOnly"
      />
    </div>

    <Message v-if="save.error.value" severity="error" :closable="false">
      {{ (save.error.value as any)?.response?.data?.detail ?? 'Save failed' }}
    </Message>
  </section>
</template>

<style scoped>
.quote-form { display: flex; flex-direction: column; gap: 1.25rem; }
.back { margin-bottom: -0.5rem; }
.page-header { display: flex; justify-content: space-between; align-items: center; gap: 1rem; }
.page-header h1 { margin: 0; }
.page-actions { display: flex; gap: 0.5rem; align-items: center; }
.readonly-note { color: #b91c1c; font-size: 0.82rem; margin: 0.25rem 0 0; }

.card { background: var(--color-surface); border: 1px solid var(--color-border, #e2e8f0); border-radius: 12px; box-shadow: 0 1px 2px rgba(15,23,42,0.04); }
.card-pad { padding: 1.25rem 1.5rem; }
.section-label { font-weight: 600; font-size: 0.85rem; color: var(--color-text-muted); text-transform: uppercase; letter-spacing: 0.05em; display: block; margin-bottom: 0.5rem; }
.full-w { width: 100%; }
.full-w :deep(.p-dropdown), .full-w :deep(.p-inputtextarea) { width: 100%; }
.cust-meta { margin-top: 0.5rem; font-size: 0.82rem; color: var(--color-text-muted); }

.top-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 1.25rem; }
@media (max-width: 900px) { .top-grid { grid-template-columns: 1fr; } }

.meta-grid { display: grid; grid-template-columns: max-content 1fr; gap: 0.6rem 1rem; align-items: center; }
.meta-label { font-size: 0.8rem; color: #64748b; font-weight: 500; text-align: right; }
.meta-grid :deep(.p-inputtext), .meta-grid :deep(.p-calendar), .meta-grid :deep(.p-dropdown) { width: 100%; }

.totals-wrap { display: flex; justify-content: flex-end; }
.totals { max-width: 380px; width: 100%; padding: 1.25rem 1.5rem; display: flex; flex-direction: column; gap: 0.5rem; }
.totals-row { display: flex; justify-content: space-between; align-items: center; gap: 0.75rem; min-height: 2rem; }
.totals-label { color: #64748b; font-size: 0.85rem; display: flex; gap: 0.5rem; align-items: center; }
.ccy-select { width: 84px; flex: 0 0 auto; }
.ccy-select :deep(.p-dropdown-label) { padding: 0.35rem 0.5rem; font-size: 0.82rem; font-weight: 600; }
.ccy-select :deep(.p-dropdown-trigger) { width: 1.75rem; }
.totals-row.total { border-top: 1px solid var(--color-border); padding-top: 0.75rem; font-weight: 600; font-size: 1rem; }
.totals-row.total .totals-label { color: #0f172a; font-size: 1rem; }
.discount-input { display: flex; gap: 0.5rem; align-items: center; width: 100%; }
.discount-type { width: 110px; }
.discount-value { flex: 1; }
.discount-value :deep(.p-inputnumber), .discount-value :deep(.p-inputnumber-input) { width: 100%; }
.num { font-variant-numeric: tabular-nums; }
</style>
