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
import DataTable from "primevue/datatable";
import Column from "primevue/column";
import { api } from "@/api/client";
import { formatAmount } from "@/utils/money";
import RecordPaymentDialog from "@/components/RecordPaymentDialog.vue";

interface Vendor {
  vendor_id: string;
  name: string;
  default_currency: string;
  payment_terms_days: number;
}

interface Account {
  account_id: number;
  code: string;
  name: string;
  type: string;
  active: boolean;
}

interface BillLine {
  line_item_id?: string;
  expense_account_id: number | null;
  description: string;
  quantity: number;
  unit_price: number;
  position: number;
}

interface ProjectLite {
  project_id: string;
  customer_id: string;
  code: string;
  name: string;
  currency: string;
  status: string;
}

interface BillOut {
  bill_id: string;
  vendor_id: string;
  project_id: string | null;
  bill_number: string | null;
  po_number: string | null;
  currency: string;
  amount: string;
  balance_due: string;
  status: string;
  issue_date: string | null;
  due_date: string | null;
  payment_terms: string | null;
  notes: string | null;
  discount_type: string | null;
  discount_value: string | null;
  line_items: {
    line_item_id: string;
    expense_account_id: number | null;
    description: string;
    quantity: string;
    unit_price: string;
    amount: string;
    position: number;
  }[];
}

const props = defineProps<{ billId?: string }>();
const router = useRouter();
const queryClient = useQueryClient();

const currencyOptions = ["IDR", "SGD", "USD"];

const vendorId = ref<string | null>(null);
const projectId = ref<string | null>(null);
const billNumber = ref<string>("");
const poNumber = ref<string>("");
const currency = ref<string>("IDR");
const issueDate = ref<Date | null>(new Date());
const dueDate = ref<Date | null>(null);
const paymentTerms = ref<string>("Net 30");
const notes = ref<string>("");

const lineItems = ref<BillLine[]>([emptyLine(0)]);

function emptyLine(position: number): BillLine {
  return {
    expense_account_id: null,
    description: "",
    quantity: 1,
    unit_price: 0,
    position,
  };
}

const isEdit = computed(() => !!props.billId);

const { data: vendors } = useQuery<Vendor[]>({
  queryKey: ["vendors"],
  queryFn: async () => (await api.get<Vendor[]>("/vendors", { params: { active: true } })).data,
});

const { data: projects } = useQuery<ProjectLite[]>({
  queryKey: ["projects-active"],
  queryFn: async () =>
    (await api.get<ProjectLite[]>("/projects", { params: { status: "ACTIVE" } })).data,
});

const { data: accounts } = useQuery<Account[]>({
  queryKey: ["chart-of-accounts"],
  queryFn: async () => (await api.get<Account[]>("/accounting/chart-of-accounts")).data,
});

const expenseAccountOptions = computed(() =>
  (accounts.value ?? [])
    .filter((a) => a.active && (a.type === "EXPENSE" || a.type === "COGS"))
    .map((a) => ({ label: `${a.code} · ${a.name}`, value: a.account_id })),
);

const { data: existing } = useQuery<BillOut | null>({
  queryKey: ["bill", () => props.billId],
  enabled: isEdit,
  queryFn: async () => {
    if (!props.billId) return null;
    return (await api.get<BillOut>(`/bills/${props.billId}`)).data;
  },
});

const selectedVendor = computed(() => {
  if (!vendorId.value) return null;
  return (vendors.value ?? []).find((v) => v.vendor_id === vendorId.value) ?? null;
});

// Default currency + payment terms from vendor on new bills only
watch(vendorId, (id) => {
  if (isEdit.value) return;
  const v = (vendors.value ?? []).find((v) => v.vendor_id === id);
  if (v?.default_currency) currency.value = v.default_currency;
  if (v?.payment_terms_days != null) paymentTerms.value = `Net ${v.payment_terms_days}`;
});

watch(existing, (b) => {
  if (!b) return;
  vendorId.value = b.vendor_id;
  projectId.value = b.project_id;
  billNumber.value = b.bill_number ?? "";
  poNumber.value = b.po_number ?? "";
  currency.value = b.currency;
  issueDate.value = b.issue_date ? new Date(b.issue_date) : null;
  dueDate.value = b.due_date ? new Date(b.due_date) : null;
  paymentTerms.value = b.payment_terms ?? "";
  notes.value = b.notes ?? "";
  if (b.line_items?.length) {
    lineItems.value = b.line_items
      .slice()
      .sort((a, z) => a.position - z.position)
      .map((ln, idx) => ({
        line_item_id: ln.line_item_id,
        expense_account_id: ln.expense_account_id,
        description: ln.description,
        quantity: Number(ln.quantity),
        unit_price: Number(ln.unit_price),
        position: ln.position ?? idx,
      }));
  } else {
    lineItems.value = [emptyLine(0)];
  }
});

const readOnly = computed(
  () => !!(existing.value?.status && !["DRAFT", "OPEN"].includes(existing.value.status)),
);

const showPaymentDialog = ref(false);
const paymentError = ref<string | null>(null);
const canRecordPayment = computed(() => {
  const s = existing.value?.status;
  return s === "OPEN" || s === "PARTIAL";
});
const recordPayment = useMutation({
  mutationFn: async (payload: {
    amount: number;
    payment_date: string;
    payer_reference: string | null;
    notes: string | null;
  }) => {
    if (!props.billId) throw new Error("no bill id");
    return (await api.post(`/bills/${props.billId}/record-payment`, payload)).data;
  },
  onSuccess: () => {
    paymentError.value = null;
    showPaymentDialog.value = false;
    queryClient.invalidateQueries({ queryKey: ["bill", props.billId] });
    queryClient.invalidateQueries({ queryKey: ["bills"] });
  },
  onError: (e: { response?: { data?: { detail?: string } } }) => {
    paymentError.value = e?.response?.data?.detail ?? "Failed to record payment";
  },
});

const subtotal = computed(() =>
  lineItems.value.reduce((acc, ln) => acc + Number(ln.quantity || 0) * Number(ln.unit_price || 0), 0),
);

function toISODate(d: Date | null): string | null {
  if (!d) return null;
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
}

function addLine() {
  lineItems.value.push(emptyLine(lineItems.value.length));
}

function removeLine(idx: number) {
  lineItems.value.splice(idx, 1);
  if (lineItems.value.length === 0) lineItems.value.push(emptyLine(0));
}

function buildPayload() {
  const clean = lineItems.value
    .filter((ln) => ln.description.trim() || ln.expense_account_id)
    .map((ln, idx) => ({
      expense_account_id: ln.expense_account_id,
      description: ln.description,
      quantity: Number(ln.quantity ?? 0),
      unit_price: Number(ln.unit_price ?? 0),
      position: idx,
    }));
  return {
    vendor_id: vendorId.value,
    project_id: projectId.value,
    bill_number: billNumber.value || null,
    po_number: poNumber.value || null,
    currency: currency.value,
    issue_date: toISODate(issueDate.value),
    due_date: toISODate(dueDate.value),
    payment_terms: paymentTerms.value || null,
    notes: notes.value || null,
    line_items: clean,
  };
}

const save = useMutation({
  mutationFn: async () => {
    const body = buildPayload();
    if (isEdit.value) {
      return (await api.patch<BillOut>(`/bills/${props.billId}`, body)).data;
    }
    return (await api.post<BillOut>("/bills", body)).data;
  },
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ["bills"] });
    router.push("/bills");
  },
});

const canSave = computed(
  () =>
    !!vendorId.value &&
    !!currency.value &&
    lineItems.value.some((ln) => ln.description.trim() && Number(ln.quantity) > 0),
);

function statusSeverity(status: string) {
  switch (status) {
    case "PAID": return "success";
    case "OPEN": return "info";
    case "PARTIAL": return "warning";
    case "VOID": return "secondary";
    case "DRAFT": return "contrast";
    default: return undefined;
  }
}
</script>

<template>
  <section class="bill-form">
    <div class="back">
      <Button label="Bills" icon="pi pi-arrow-left" text size="small" @click="router.push('/bills')" />
    </div>

    <header class="page-header">
      <div class="header-title">
        <h1>{{ isEdit ? "Edit bill" : "New bill" }}</h1>
        <p v-if="readOnly" class="readonly-note">
          Read-only — {{ existing?.status }} bills cannot be edited
        </p>
      </div>
      <div class="page-actions">
        <template v-if="readOnly">
          <Tag v-if="existing?.status" :value="existing.status" :severity="statusSeverity(existing.status)" />
          <Button
            v-if="canRecordPayment"
            label="Record payment"
            icon="pi pi-wallet"
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
      <div class="card card-pad">
        <div class="section-label">Vendor</div>
        <Dropdown
          v-model="vendorId"
          :options="vendors ?? []"
          option-label="name"
          option-value="vendor_id"
          placeholder="Choose a vendor"
          filter
          show-clear
          :disabled="readOnly"
        />
        <div v-if="selectedVendor" class="vendor-meta">
          Default currency: <code>{{ selectedVendor.default_currency }}</code> ·
          Net {{ selectedVendor.payment_terms_days }}
        </div>
      </div>

      <div class="card card-pad">
        <div class="meta-grid">
          <label class="meta-label">Vendor bill #</label>
          <InputText v-model="billNumber" placeholder="As printed on the bill" :disabled="readOnly" />

          <label class="meta-label">Our PO #</label>
          <InputText v-model="poNumber" :disabled="readOnly" />

          <label class="meta-label">Issue date</label>
          <DatePicker v-model="issueDate" date-format="yy-mm-dd" show-icon :disabled="readOnly" />

          <label class="meta-label">Due date</label>
          <DatePicker v-model="dueDate" date-format="yy-mm-dd" show-icon :disabled="readOnly" />

          <label class="meta-label">Payment terms</label>
          <InputText v-model="paymentTerms" :disabled="readOnly" />

          <label class="meta-label">Currency</label>
          <Dropdown v-model="currency" :options="currencyOptions" :disabled="readOnly" />

          <label class="meta-label">Project</label>
          <Dropdown
            v-model="projectId"
            :options="projects ?? []"
            :option-label="(p: ProjectLite) => `${p.code} · ${p.name}`"
            option-value="project_id"
            placeholder="(none)"
            :disabled="readOnly"
            show-clear
            filter
          />
        </div>
      </div>
    </div>

    <div class="card card-pad lines-card">
      <div class="section-label">Line items</div>
      <DataTable :value="lineItems" data-key="position" size="small">
        <Column header="Description" :style="{ minWidth: '260px' }">
          <template #body="{ data, index }">
            <InputText v-model="data.description" :disabled="readOnly" placeholder="What was this for" />
          </template>
        </Column>
        <Column header="Expense account" :style="{ minWidth: '220px' }">
          <template #body="{ data }">
            <Dropdown
              v-model="data.expense_account_id"
              :options="expenseAccountOptions"
              option-label="label"
              option-value="value"
              placeholder="Account"
              filter
              show-clear
              :disabled="readOnly"
            />
          </template>
        </Column>
        <Column header="Qty" :body-style="{ width: '90px' }">
          <template #body="{ data }">
            <InputNumber v-model="data.quantity" :min="0" :max-fraction-digits="4" :disabled="readOnly" />
          </template>
        </Column>
        <Column header="Unit price" :body-style="{ width: '140px' }">
          <template #body="{ data }">
            <InputNumber
              v-model="data.unit_price"
              mode="decimal"
              :min="0"
              :max-fraction-digits="currency === 'IDR' ? 0 : 2"
              :use-grouping="true"
              :disabled="readOnly"
            />
          </template>
        </Column>
        <Column header="Amount" :body-style="{ width: '140px', textAlign: 'right' }" :header-style="{ textAlign: 'right' }">
          <template #body="{ data }">
            <span class="num">
              {{ formatAmount((Number(data.quantity || 0) * Number(data.unit_price || 0)).toString(), currency) }}
            </span>
          </template>
        </Column>
        <Column :style="{ width: '50px' }">
          <template #body="{ index }">
            <Button
              v-if="!readOnly"
              icon="pi pi-times"
              text
              rounded
              severity="danger"
              size="small"
              @click="removeLine(index)"
            />
          </template>
        </Column>
      </DataTable>
      <div v-if="!readOnly" class="add-line">
        <Button label="Add line" icon="pi pi-plus" text size="small" @click="addLine" />
      </div>
    </div>

    <div class="totals-wrap">
      <div class="card totals">
        <div class="totals-row total">
          <div class="totals-label">Total</div>
          <div class="totals-value num">{{ formatAmount(subtotal.toString(), currency) }}</div>
        </div>
      </div>
    </div>

    <div class="card card-pad">
      <label class="section-label">Notes</label>
      <Textarea v-model="notes" rows="4" :disabled="readOnly" />
    </div>

    <Message v-if="save.error.value" severity="error" :closable="false">
      {{ (save.error.value as any)?.response?.data?.detail ?? 'Save failed' }}
    </Message>

    <RecordPaymentDialog
      v-if="existing"
      v-model:visible="showPaymentDialog"
      :balance-due="existing.balance_due"
      :currency="existing.currency"
      mode="AP"
      :loading="recordPayment.isPending.value"
      :error="paymentError"
      @submit="recordPayment.mutate($event)"
    />
  </section>
</template>

<style scoped>
.bill-form { display: flex; flex-direction: column; gap: 1.25rem; }
.back { margin-bottom: -0.5rem; }
.top-grid { display: grid; grid-template-columns: 1fr 1.2fr; gap: 1rem; }
@media (max-width: 900px) { .top-grid { grid-template-columns: 1fr; } }

.section-label { font-weight: 600; font-size: 0.85rem; color: var(--color-text-muted); text-transform: uppercase; letter-spacing: 0.05em; margin-bottom: 0.5rem; display: block; }
.vendor-meta { margin-top: 0.75rem; font-size: 0.82rem; color: var(--color-text-muted); }
.vendor-meta code { background: var(--color-surface-alt); padding: 0.05rem 0.35rem; border-radius: 4px; font-weight: 600; }

.meta-grid { display: grid; grid-template-columns: 140px 1fr; gap: 0.5rem 0.75rem; align-items: center; }
.meta-label { font-size: 0.82rem; color: var(--color-text-muted); font-weight: 500; }
.meta-grid :deep(.p-inputtext),
.meta-grid :deep(.p-dropdown),
.meta-grid :deep(.p-calendar) { width: 100%; }

.lines-card { display: flex; flex-direction: column; gap: 0.5rem; }
.lines-card :deep(.p-inputtext),
.lines-card :deep(.p-inputnumber),
.lines-card :deep(.p-inputnumber-input),
.lines-card :deep(.p-dropdown) { width: 100%; }
.add-line { padding-top: 0.25rem; }

.totals-wrap { display: flex; justify-content: flex-end; }
.totals { min-width: 320px; padding: 1rem 1.25rem; }
.totals-row { display: flex; justify-content: space-between; align-items: center; padding: 0.35rem 0; }
.totals-row.total { border-top: 1px solid var(--color-border); padding-top: 0.75rem; font-weight: 600; font-size: 1rem; }
.num { font-variant-numeric: tabular-nums; }

.readonly-note { color: #b91c1c; font-size: 0.82rem; margin: 0.25rem 0 0; }
.header-title h1 { margin: 0; }
</style>
