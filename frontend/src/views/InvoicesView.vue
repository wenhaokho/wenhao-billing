<script setup lang="ts">
import { ref, computed } from "vue";
import { useRouter } from "vue-router";
import { useQuery, useMutation, useQueryClient } from "@tanstack/vue-query";
import DataTable from "primevue/datatable";
import Column from "primevue/column";
import Button from "primevue/button";
import TabView from "primevue/tabview";
import TabPanel from "primevue/tabpanel";
import Tag from "primevue/tag";
import RecordPaymentDialog from "@/components/RecordPaymentDialog.vue";
import { api } from "@/api/client";
import { formatAmount } from "@/utils/money";

interface Customer {
  customer_id: string;
  name: string;
}

interface Invoice {
  invoice_id: string;
  customer_id: string;
  invoice_type: string;
  invoice_number: string | null;
  currency: string;
  amount: string;
  balance_due: string;
  status: string;
  issue_date: string;
  due_date: string;
  created_at: string;
}

interface CurrencyAmount {
  currency: string;
  amount: string;
}

interface InvoicesSummary {
  overdue: CurrencyAmount[];
  due_30d: CurrencyAmount[];
  avg_days_to_pay: number | null;
}

const router = useRouter();

// ---- tab filter (Wave parity: Unpaid / Draft / All) ----
// 0 = Unpaid (SENT + PARTIAL), 1 = Draft, 2 = All
const activeTab = ref(0);
const TAB_TO_STATUSES: Record<number, string[] | null> = {
  0: ["SENT", "PARTIAL"],
  1: ["DRAFT"],
  2: null,
};

const { data: invoices, isLoading } = useQuery<Invoice[]>({
  queryKey: ["invoices", activeTab],
  queryFn: async () => {
    const statuses = TAB_TO_STATUSES[activeTab.value];
    const params = new URLSearchParams();
    if (statuses) for (const s of statuses) params.append("status", s);
    const qs = params.toString();
    const url = qs ? `/invoices?${qs}` : "/invoices";
    return (await api.get<Invoice[]>(url)).data;
  },
});

const { data: customers } = useQuery<Customer[]>({
  queryKey: ["customers"],
  queryFn: async () => (await api.get<Customer[]>("/customers")).data,
});

const { data: summary } = useQuery<InvoicesSummary>({
  queryKey: ["invoices-summary"],
  queryFn: async () =>
    (await api.get<InvoicesSummary>("/stats/invoices-summary")).data,
});

function formatBucket(rows: CurrencyAmount[] | undefined): string {
  if (!rows || rows.length === 0) return "—";
  const [top, ...rest] = [...rows].sort((a, b) => Number(b.amount) - Number(a.amount));
  const head = `${top.currency} ${formatAmount(top.amount, top.currency)}`;
  return rest.length ? `${head} +${rest.length}` : head;
}

const overdueDisplay = computed(() => formatBucket(summary.value?.overdue));
const due30dDisplay = computed(() => formatBucket(summary.value?.due_30d));
const avgDaysDisplay = computed(() => {
  const v = summary.value?.avg_days_to_pay;
  if (v === null || v === undefined) return "—";
  return `${v.toFixed(1)} days`;
});
const hasOverdue = computed(() => (summary.value?.overdue?.length ?? 0) > 0);

const customerNameById = computed(() => {
  const map: Record<string, string> = {};
  for (const c of customers.value ?? []) map[c.customer_id] = c.name;
  return map;
});

// ---- inline record-payment ----
const queryClient = useQueryClient();
const paymentTarget = ref<Invoice | null>(null);
const paymentDialogVisible = ref(false);
const paymentError = ref<string | null>(null);

function openRecordPayment(row: Invoice) {
  paymentTarget.value = row;
  paymentError.value = null;
  paymentDialogVisible.value = true;
}

const recordPayment = useMutation({
  mutationFn: async (payload: {
    amount: number;
    payment_date: string;
    payer_name: string | null;
    payer_reference: string | null;
    notes: string | null;
  }) => {
    const id = paymentTarget.value?.invoice_id;
    if (!id) throw new Error("no invoice id");
    return (await api.post(`/invoices/${id}/record-payment`, payload)).data;
  },
  onSuccess: () => {
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

function canRecordPayment(row: Invoice): boolean {
  return (row.status === "SENT" || row.status === "PARTIAL")
    && Number(row.balance_due) > 0;
}

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
</script>

<template>
  <section>
    <header class="page-header">
      <div>
        <h1>Invoices</h1>
        <p class="subtitle">
          All invoices across milestone, recurring, and usage-locked cycles.
          New ones land as <code>DRAFT</code> in the Awaiting Finalization queue.
        </p>
      </div>
      <div class="page-actions">
        <Button label="Recurring templates" icon="pi pi-replay" severity="secondary" outlined @click="router.push('/invoices/recurring')" />
        <Button label="New invoice" icon="pi pi-plus" @click="router.push('/invoices/new')" />
        <Button label="New recurring" icon="pi pi-refresh" severity="secondary" @click="router.push('/invoices/recurring/new')" />
      </div>
    </header>

    <div class="summary-tiles">
      <div class="tile" :class="{ alert: hasOverdue }">
        <div class="tile-label">Overdue</div>
        <div class="tile-value">{{ overdueDisplay }}</div>
      </div>
      <div class="tile">
        <div class="tile-label">Due within 30 days</div>
        <div class="tile-value">{{ due30dDisplay }}</div>
      </div>
      <div class="tile">
        <div class="tile-label">Avg. time to get paid</div>
        <div class="tile-value">{{ avgDaysDisplay }}</div>
      </div>
    </div>

    <TabView v-model:active-index="activeTab" class="status-tabs">
      <TabPanel header="Unpaid" />
      <TabPanel header="Draft" />
      <TabPanel header="All" />
    </TabView>

    <DataTable
      :value="invoices ?? []"
      :loading="isLoading"
      data-key="invoice_id"
      striped-rows
      selection-mode="single"
      @row-click="(ev) => router.push(`/invoices/${(ev.data as Invoice).invoice_id}/edit`)"
    >
      <template #empty>
        <div class="empty-state">
          <i class="pi pi-file" />
          <div>No invoices match the current filter.</div>
        </div>
      </template>
      <Column header="Invoice #">
        <template #body="{ data: row }">
          <code v-if="row.invoice_number">{{ row.invoice_number }}</code>
          <span v-else class="muted">—</span>
        </template>
      </Column>
      <Column header="Customer">
        <template #body="{ data: row }">
          {{ customerNameById[row.customer_id] ?? row.customer_id.slice(0, 8) }}
        </template>
      </Column>
      <Column field="invoice_type" header="Type" />
      <Column header="Status">
        <template #body="{ data: row }">
          <Tag :value="row.status" :severity="statusSeverity(row.status)" />
        </template>
      </Column>
      <Column header="Amount" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }">
        <template #body="{ data: row }">
          <span class="num">{{ formatAmount(row.amount, row.currency) }}</span>
        </template>
      </Column>
      <Column header="Balance" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }">
        <template #body="{ data: row }">
          <span class="num">{{ formatAmount(row.balance_due, row.currency) }}</span>
        </template>
      </Column>
      <Column field="currency" header="Ccy" />
      <Column field="issue_date" header="Issued" />
      <Column field="due_date" header="Due" />
      <Column header="" :style="{ width: '150px' }" :body-style="{ textAlign: 'right' }">
        <template #body="{ data: row }">
          <Button
            v-if="canRecordPayment(row)"
            label="Record payment"
            icon="pi pi-dollar"
            size="small"
            text
            @click.stop="openRecordPayment(row)"
          />
        </template>
      </Column>
    </DataTable>

    <RecordPaymentDialog
      v-if="paymentTarget"
      :visible="paymentDialogVisible"
      :balance-due="paymentTarget.balance_due"
      :currency="paymentTarget.currency"
      mode="AR"
      :loading="recordPayment.isPending.value"
      :error="paymentError"
      @update:visible="(v) => (paymentDialogVisible = v)"
      @submit="(payload) => recordPayment.mutate(payload)"
    />
  </section>
</template>

<style scoped>
.summary-tiles {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 0.85rem;
  margin: 0 0 1.25rem;
}
.tile {
  background: #fff;
  border: 1px solid var(--color-border, #e2e8f0);
  border-radius: 10px;
  padding: 0.85rem 1rem;
}
.tile.alert {
  border-color: #fca5a5;
  background: #fef2f2;
}
.tile-label {
  font-size: 0.74rem;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--color-text-muted, #64748b);
}
.tile-value {
  font-size: 1.15rem;
  font-weight: 600;
  margin-top: 0.25rem;
  font-variant-numeric: tabular-nums;
}
.tile.alert .tile-value { color: #b91c1c; }
@media (max-width: 900px) {
  .summary-tiles { grid-template-columns: 1fr; }
}
.filters { margin: 0 0 1rem; }
.status-tabs { margin-bottom: 0.5rem; }
.status-tabs :deep(.p-tabview-panels) { display: none; }
code { background: #f1f5f9; padding: 0.05rem 0.35rem; border-radius: 4px; font-size: 0.82em; }
.num { font-variant-numeric: tabular-nums; }
.muted { color: var(--color-text-muted, #64748b); }
:deep(.p-datatable-tbody > tr) { cursor: pointer; }
</style>
