<script setup lang="ts">
import AppIcon from "@/components/AppIcon.vue";
import { ref, computed, watch } from "vue";
import { useRouter, useRoute } from "vue-router";
import { useQuery, useMutation, useQueryClient } from "@tanstack/vue-query";
import DataTable from "primevue/datatable";
import Column from "primevue/column";
import Button from "primevue/button";
import Tabs from "primevue/tabs";
import TabList from "primevue/tablist";
import Tab from "primevue/tab";
import StatusTag from "@/components/StatusTag.vue";
import PageHeader from "@/components/PageHeader.vue";
import EmptyState from "@/components/EmptyState.vue";
import RecordPaymentDialog from "@/components/RecordPaymentDialog.vue";
import { whtOutstanding } from "@/composables/withholding";
import { useConfirm } from "primevue/useconfirm";
import { api } from "@/api/client";
import { formatAmount } from "@/utils/money";

interface Customer {
  customer_id: string;
  name: string;
  contact_email: string | null;
  quotation_cc_emails?: string[];
  billing_cc_emails?: string[];
}

interface Invoice {
  invoice_id: string;
  customer_id: string;
  invoice_type: string;
  invoice_number: string | null;
  currency: string;
  amount: string;
  balance_due: string;
  wht_amount?: string;
  wht_booked?: string;
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
// Each tab's value is the status filter it applies: unpaid (OPEN + SENT +
// PARTIAL), draft, all. The active tab is reflected in the URL (?tab=unpaid)
// so it is restored when navigating back to this list after opening an item.
const route = useRoute();
const TAB_SLUGS = ["unpaid", "draft", "all"];
function readTabFromQuery(): string {
  const q = String(route.query.tab);
  return TAB_SLUGS.includes(q) ? q : TAB_SLUGS[0];
}
const activeTab = ref<string>(readTabFromQuery());
watch(activeTab, (t) => {
  router.replace({ query: { ...route.query, tab: TAB_SLUGS.includes(t) ? t : TAB_SLUGS[0] } });
});
const TAB_TO_STATUSES: Record<string, string[] | null> = {
  unpaid: ["OPEN", "SENT", "PARTIAL"],
  draft: ["DRAFT"],
  all: null,
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

const confirm = useConfirm();

const paymentCustomer = computed(() => {
  const cid = paymentTarget.value?.customer_id;
  if (!cid) return null;
  return (customers.value ?? []).find((c) => c.customer_id === cid) ?? null;
});
// PPh still expected to be withheld by the customer (not yet booked).
const paymentWhtOutstanding = computed(() =>
  paymentTarget.value ? whtOutstanding(paymentTarget.value) : 0,
);
const paymentCustomerEmail = computed(() => paymentCustomer.value?.contact_email ?? null);

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
      icon: "mail",
      acceptLabel: "Retry",
      rejectLabel: "Dismiss",
      accept: () => void sendReceipt(invoiceId, paymentId, receipt),
    });
  }
}

const recordPayment = useMutation({
  mutationFn: async (payload: {
    amount: number;
    wht_amount: number;
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

function canRecordPayment(row: Invoice): boolean {
  return (row.status === "OPEN" || row.status === "SENT" || row.status === "PARTIAL")
    && Number(row.balance_due) > 0;
}

</script>

<template>
  <section>
    <PageHeader eyebrow="02 / Sales" title="Invoices">
      <template #lede>
        All invoices across milestone, recurring, and usage-locked cycles.
        New ones land as <code>DRAFT</code> in the Awaiting Finalization queue.
      </template>
      <Button label="Recurring templates" severity="secondary" outlined @click="router.push('/invoices/recurring')">
        <template #icon><AppIcon name="repeat" /></template>
      </Button>
      <Button label="New invoice" @click="router.push('/invoices/new')">
        <template #icon><AppIcon name="plus" /></template>
      </Button>
    </PageHeader>

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

    <Tabs v-model:value="activeTab" class="list-tabs">
      <TabList>
        <Tab value="unpaid">Unpaid</Tab>
        <Tab value="draft">Draft</Tab>
        <Tab value="all">All</Tab>
      </TabList>
    </Tabs>

    <DataTable
      :value="invoices ?? []"
      :loading="isLoading"
      data-key="invoice_id"
      striped-rows
      selection-mode="single"
      @row-click="(ev) => router.push({ path: `/invoices/${(ev.data as Invoice).invoice_id}/edit`, query: { from: route.fullPath } })"
    >
      <template #empty>
        <EmptyState icon="file-text" title="No invoices here" message="No invoices match the current filter." />
      </template>
      <Column header="Invoice #" body-class="cell-nowrap">
        <template #body="{ data: row }">
          <span v-if="row.invoice_number" class="doc-no">{{ row.invoice_number }}</span>
          <span v-else class="muted">—</span>
        </template>
      </Column>
      <Column header="Customer">
        <template #body="{ data: row }">
          <span class="customer">{{ customerNameById[row.customer_id] ?? row.customer_id.slice(0, 8) }}</span>
        </template>
      </Column>
      <Column header="Type">
        <template #body="{ data: row }">
          <span class="type">{{ row.invoice_type }}</span>
        </template>
      </Column>
      <Column header="Status">
        <template #body="{ data: row }">
          <StatusTag :status="row.status" />
        </template>
      </Column>
      <Column header="Amount" body-class="cell-nowrap" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }">
        <template #body="{ data: row }">
          <span class="num">{{ formatAmount(row.amount, row.currency) }}</span>
        </template>
      </Column>
      <Column header="Balance" body-class="cell-nowrap" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }">
        <template #body="{ data: row }">
          <span class="num strong">{{ formatAmount(row.balance_due, row.currency) }}</span>
        </template>
      </Column>
      <Column field="currency" header="Ccy" body-class="cell-nowrap cell-muted" />
      <Column field="issue_date" header="Issued" body-class="cell-nowrap cell-muted" />
      <Column field="due_date" header="Due" body-class="cell-nowrap" />
      <Column header="" :style="{ width: '150px' }" body-class="cell-nowrap" :body-style="{ textAlign: 'right' }">
        <template #body="{ data: row }">
          <Button
            v-if="canRecordPayment(row)"
            label="Record payment"
            size="small"
            text
            @click.stop="openRecordPayment(row)">
            <template #icon><AppIcon name="dollar-sign" /></template>
          </Button>
        </template>
      </Column>
    </DataTable>

    <RecordPaymentDialog
      v-if="paymentTarget"
      :visible="paymentDialogVisible"
      :balance-due="paymentTarget.balance_due"
      :wht-outstanding="paymentWhtOutstanding"
      :currency="paymentTarget.currency"
      :customer-email="paymentCustomerEmail"
      :customer-cc="paymentCustomer?.billing_cc_emails ?? null"
      mode="AR"
      :loading="recordPayment.isPending.value"
      :error="paymentError"
      @update:visible="(v) => (paymentDialogVisible = v)"
      @submit="(payload) => recordPayment.mutate(payload)"
    />
  </section>
</template>

<style scoped>
/* KPI strip (mockup): one bordered strip, hairlines between cells; the
   overdue cell gets the red rule when anything is overdue. */
.summary-tiles {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  gap: 1px;
  background: var(--wb-line);
  border: 1px solid var(--wb-line);
  margin: 0 0 24px;
}
.tile { background: var(--wb-surface); padding: 22px 26px; min-width: 0; }
.tile.alert { border-left: 4px solid var(--wb-red); padding-left: 22px; }
.tile-label {
  font: 500 11px/1.4 var(--wb-font-sans);
  letter-spacing: 1.3px;
  text-transform: uppercase;
  color: var(--wb-muted-soft);
}
.tile-value {
  font: 500 28px/1.1 var(--wb-font-display);
  letter-spacing: -1px;
  color: var(--wb-ink);
  margin-top: 8px;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.tile.alert .tile-value { color: var(--wb-red); }
@media (max-width: 720px) {
  .tile { padding: 16px 18px; }
  .tile-value { font-size: 24px; }
}
.customer { font-weight: 500; }
.type { font-size: 12px; letter-spacing: 1px; text-transform: uppercase; color: var(--wb-muted); }
.num { font-variant-numeric: tabular-nums; }
.strong { font-weight: 500; }
.muted { color: var(--wb-muted-soft); }
:deep(.p-datatable-tbody > tr) { cursor: pointer; }
</style>
