<script setup lang="ts">
import { ref, computed, watch } from "vue";
import { useRouter } from "vue-router";
import { useQuery, useMutation } from "@tanstack/vue-query";
import Button from "primevue/button";
import Calendar from "primevue/calendar";
import Dropdown from "primevue/dropdown";
import InputText from "primevue/inputtext";
import InputNumber from "primevue/inputnumber";
import Textarea from "primevue/textarea";
import Message from "primevue/message";
import { api } from "@/api/client";
import { formatAmount } from "@/utils/money";
import {
  useInvoiceForm,
  type LineItemRow,
  type DiscountType,
} from "@/composables/useInvoiceForm";
import InvoiceLineItemsTable from "@/components/InvoiceLineItemsTable.vue";

const props = defineProps<{ id?: string }>();
const router = useRouter();
const isEdit = computed(() => !!props.id);

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

const currencyOptions = ["IDR", "SGD", "USD"];
const paymentTermsOptions = ["On Receipt", "Net 7", "Net 14", "Net 30", "Net 60"];

type Frequency = "DAILY" | "WEEKLY" | "MONTHLY" | "YEARLY";
type EndMode = "NEVER" | "ON_DATE" | "AFTER_N";
const frequencyOptions: { label: string; value: Frequency }[] = [
  { label: "Daily", value: "DAILY" },
  { label: "Weekly", value: "WEEKLY" },
  { label: "Monthly", value: "MONTHLY" },
  { label: "Yearly", value: "YEARLY" },
];
const endModeOptions: { label: string; value: EndMode }[] = [
  { label: "Never", value: "NEVER" },
  { label: "On date", value: "ON_DATE" },
  { label: "After N cycles", value: "AFTER_N" },
];

const customerId = ref<string | null>(null);
const poSoNumber = ref<string>("");
const currency = ref<string>("IDR");
const paymentTerms = ref<string>("On Receipt");
const notes = ref<string>("");
const footer = ref<string>("");

// Schedule block
const frequency = ref<Frequency>("MONTHLY");
const interval = ref<number>(1);
const startDate = ref<Date | null>(new Date());
const endMode = ref<EndMode>("NEVER");
const endDate = ref<Date | null>(null);
const endAfterCycles = ref<number | null>(null);

function toIsoDate(d: Date | null): string | null {
  if (!d) return null;
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, "0");
  const day = String(d.getDate()).padStart(2, "0");
  return `${y}-${m}-${day}`;
}

const discountEnabled = ref(false);
const discountType = ref<DiscountType | null>(null);
const discountValue = ref<number | null>(null);

const { lineItems, addLine, removeLine, subtotal, total } = useInvoiceForm({
  currency,
  discountType,
  discountValue,
});

const { data: customers } = useQuery<Customer[]>({
  queryKey: ["customers"],
  queryFn: async () => (await api.get<Customer[]>("/customers")).data,
});

const { data: catalog } = useQuery<CatalogItem[]>({
  queryKey: ["items"],
  queryFn: async () => (await api.get<CatalogItem[]>("/items")).data,
});

const selectedCustomer = computed(() => {
  if (!customerId.value) return null;
  return (customers.value ?? []).find((c) => c.customer_id === customerId.value) ?? null;
});

// Auto-default currency from selected customer
watch(customerId, (newId) => {
  const c = (customers.value ?? []).find((c) => c.customer_id === newId);
  if (c?.default_currency) currency.value = c.default_currency;
});

const customerPickerRef = ref<InstanceType<typeof Dropdown> | null>(null);
function focusCustomerPicker() {
  customerPickerRef.value?.show?.();
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
    currency: currency.value,
    po_so_number: poSoNumber.value || null,
    payment_terms: paymentTerms.value,
    notes: notes.value || null,
    footer: footer.value || null,
    discount_type: discountEnabled.value ? discountType.value : null,
    discount_value: discountEnabled.value ? discountValue.value : null,
    line_items: clean,
    schedule: {
      frequency: frequency.value,
      interval: interval.value ?? 1,
      start_date: toIsoDate(startDate.value),
      end_mode: endMode.value,
      end_date: endMode.value === "ON_DATE" ? toIsoDate(endDate.value) : null,
      end_after_cycles:
        endMode.value === "AFTER_N" ? endAfterCycles.value ?? null : null,
    },
  };
}

interface TemplateResponse {
  customer_id: string | null;
  currency: string;
  po_so_number: string | null;
  payment_terms: string | null;
  notes: string | null;
  footer: string | null;
  discount_type: "PERCENT" | "AMOUNT" | null;
  discount_value: string | null;
  billing_cycle_ref: {
    frequency?: Frequency;
    interval?: number;
    start_date?: string;
    end_mode?: EndMode;
    end_date?: string | null;
    end_after_cycles?: number | null;
  } | null;
  line_items: {
    line_item_id: string;
    item_id: string | null;
    description: string;
    quantity: string;
    unit_price: string;
    position: number;
  }[];
}

const { data: templateData } = useQuery<TemplateResponse>({
  queryKey: ["recurring-template", () => props.id],
  queryFn: async () =>
    (await api.get<TemplateResponse>(`/invoices/recurring-templates/${props.id}`)).data,
  enabled: () => !!props.id,
});

watch(
  templateData,
  (t) => {
    if (!t) return;
    customerId.value = t.customer_id;
    currency.value = t.currency;
    poSoNumber.value = t.po_so_number ?? "";
    paymentTerms.value = t.payment_terms ?? "On Receipt";
    notes.value = t.notes ?? "";
    footer.value = t.footer ?? "";
    if (t.discount_type) {
      discountEnabled.value = true;
      discountType.value = t.discount_type;
      discountValue.value = t.discount_value ? Number(t.discount_value) : null;
    }
    const cfg = t.billing_cycle_ref ?? {};
    if (cfg.frequency) frequency.value = cfg.frequency;
    if (cfg.interval) interval.value = cfg.interval;
    if (cfg.start_date) startDate.value = new Date(cfg.start_date + "T00:00:00");
    if (cfg.end_mode) endMode.value = cfg.end_mode;
    if (cfg.end_date) endDate.value = new Date(cfg.end_date + "T00:00:00");
    if (cfg.end_after_cycles) endAfterCycles.value = cfg.end_after_cycles;
    lineItems.value = t.line_items
      .sort((a, b) => a.position - b.position)
      .map((ln) => ({
        item_id: ln.item_id,
        description: ln.description,
        quantity: Number(ln.quantity),
        unit_price: Number(ln.unit_price),
      })) as LineItemRow[];
  },
  { immediate: true },
);

const save = useMutation({
  mutationFn: async () => {
    const body = buildPayload();
    if (props.id) {
      return (await api.put(`/invoices/recurring-templates/${props.id}`, body)).data;
    }
    return (await api.post("/invoices/recurring-template", body)).data;
  },
  onSuccess: () => {
    router.push("/invoices/recurring");
  },
});

const canSave = computed(() => {
  if (!currency.value || !paymentTerms.value) return false;
  if (!startDate.value) return false;
  if (!lineItems.value.some((ln) => ln.description?.trim() || ln.item_id)) return false;
  if (endMode.value === "ON_DATE" && !endDate.value) return false;
  if (endMode.value === "AFTER_N" && !(endAfterCycles.value && endAfterCycles.value > 0))
    return false;
  return true;
});
</script>

<template>
  <section class="invoice-form">
    <div class="back">
      <Button label="Invoices" icon="pi pi-arrow-left" text size="small" @click="router.push('/invoices')" />
    </div>

    <header class="page-header sticky-header">
      <div class="header-title">
        <h1>{{ isEdit ? "Edit recurring invoice" : "New recurring invoice" }}</h1>
      </div>
      <div class="page-actions">
        <Button label="Preview" icon="pi pi-eye" text disabled />
        <Button
          :label="isEdit ? 'Save changes' : 'Save as DRAFT'"
          icon="pi pi-save"
          :loading="save.isPending.value"
          :disabled="!canSave"
          @click="save.mutate()"
        />
      </div>
    </header>

    <div class="top-grid">
      <div class="card customer-card" :class="{ 'is-empty': !selectedCustomer }">
        <template v-if="selectedCustomer">
          <div class="customer-filled">
            <div class="customer-header">
              <div class="customer-label">Bill to</div>
              <Button
                label="Edit"
                icon="pi pi-pencil"
                text
                size="small"
                class="customer-edit"
                @click="focusCustomerPicker"
              />
            </div>
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
          </div>
          <Dropdown
            ref="customerPickerRef"
            v-model="customerId"
            :options="customers ?? []"
            option-label="name"
            option-value="customer_id"
            placeholder="Choose a customer"
            filter
            show-clear
            class="customer-dropdown-hidden"
          />
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
              class="customer-dropdown"
            />
          </div>
        </template>
      </div>

      <div class="card meta-card">
        <div class="meta-grid">
          <label class="meta-label">Invoice number</label>
          <span class="auto">Auto-generated</span>

          <label class="meta-label">P.O. / S.O. number</label>
          <InputText v-model="poSoNumber" />

          <label class="meta-label">Invoice date</label>
          <span class="auto">Auto-generated</span>

          <label class="meta-label">Payment due</label>
          <Dropdown v-model="paymentTerms" :options="paymentTermsOptions" />
        </div>
      </div>
    </div>

    <div class="card schedule-card">
      <label class="section-label">Schedule</label>
      <div class="schedule-grid">
        <label class="meta-label">Frequency</label>
        <Dropdown
          v-model="frequency"
          :options="frequencyOptions"
          option-label="label"
          option-value="value"
          class="schedule-freq"
        />

        <label class="meta-label">Every</label>
        <div class="inline-row">
          <InputNumber
            v-model="interval"
            :min="1"
            :max="99"
            class="schedule-interval"
          />
          <span class="muted small">{{
            frequency === "DAILY"
              ? interval === 1
                ? "day"
                : "days"
              : frequency === "WEEKLY"
                ? interval === 1
                  ? "week"
                  : "weeks"
                : frequency === "MONTHLY"
                  ? interval === 1
                    ? "month"
                    : "months"
                  : interval === 1
                    ? "year"
                    : "years"
          }}</span>
        </div>

        <label class="meta-label">Start date</label>
        <Calendar v-model="startDate" date-format="yy-mm-dd" show-icon />

        <label class="meta-label">Ends</label>
        <Dropdown
          v-model="endMode"
          :options="endModeOptions"
          option-label="label"
          option-value="value"
        />

        <template v-if="endMode === 'ON_DATE'">
          <label class="meta-label">End date</label>
          <Calendar v-model="endDate" date-format="yy-mm-dd" show-icon />
        </template>
        <template v-else-if="endMode === 'AFTER_N'">
          <label class="meta-label">After</label>
          <div class="inline-row">
            <InputNumber v-model="endAfterCycles" :min="1" class="schedule-interval" />
            <span class="muted small">cycles</span>
          </div>
        </template>
      </div>
    </div>

    <InvoiceLineItemsTable
      v-model="lineItems"
      :catalog="catalog ?? []"
      :currency="currency"
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
            />
            <InputNumber
              v-model="discountValue"
              mode="decimal"
              :min="0"
              :max-fraction-digits="4"
              class="discount-value"
            />
            <Button
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
            <Dropdown v-model="currency" :options="currencyOptions" class="ccy-select" />
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
      />
    </div>

    <div class="card notes-card">
      <label class="section-label">Footer</label>
      <Textarea
        v-model="footer"
        rows="3"
        placeholder="Footer shown on every generated invoice."
        class="notes-textarea"
      />
    </div>

    <Message v-if="save.error.value" severity="error" :closable="false">
      {{ (save.error.value as any)?.response?.data?.detail ?? 'Save failed' }}
    </Message>
  </section>
</template>

<style scoped>
.invoice-form {
  display: flex;
  flex-direction: column;
  gap: 1.25rem;
}

.back { margin-bottom: -0.5rem; }

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
.header-title h1 { margin: 0; }
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

.customer-card {
  display: flex;
  align-items: center;
  justify-content: center;
  min-height: 160px;
  position: relative;
}
.customer-card.is-empty {
  border: 1.5px dashed var(--color-border-strong);
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
.customer-dropdown { width: min(280px, 100%); }
.customer-dropdown-hidden { display: none; }
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
.customer-detail a { color: #2563eb; text-decoration: none; }
.customer-detail a:hover { text-decoration: underline; }
.customer-detail.address span { white-space: pre-line; }
.customer-edit { padding: 0.25rem 0.5rem; }

.meta-card { display: flex; flex-direction: column; }
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
.auto {
  color: var(--color-text-subtle);
  font-style: italic;
  font-size: 0.85rem;
}
.meta-grid :deep(.p-inputtext),
.meta-grid :deep(.p-dropdown) {
  width: 100%;
}

.totals-wrap { display: flex; justify-content: flex-end; }
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
.totals-row.total .totals-label { color: var(--color-text); font-size: 1rem; }
.totals-row.due {
  margin-top: 0.25rem;
  padding: 0.75rem 0.9rem;
  border-top: 3px double var(--color-border-strong);
  background: var(--color-primary-soft);
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
.discount-value :deep(.p-inputnumber-input) { width: 100%; }
.ccy-select { width: 110px; }
.ccy-select :deep(.p-dropdown-label) { padding-right: 0.25rem; }
.num { font-variant-numeric: tabular-nums; }

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
.notes-textarea { width: 100%; }

.schedule-card { display: flex; flex-direction: column; gap: 0.75rem; }
.schedule-grid {
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: 0.75rem 1rem;
  align-items: center;
  max-width: 560px;
}
.schedule-freq { width: 200px; }
.schedule-interval { width: 90px; }
.schedule-interval :deep(.p-inputnumber-input) { width: 100%; text-align: center; }
.inline-row { display: flex; align-items: center; gap: 0.5rem; }
.muted { color: var(--color-text-muted); }
.small { font-size: 0.8rem; }
</style>
