<script setup lang="ts">
import AppIcon from "@/components/AppIcon.vue";
import { computed } from "vue";
import { useRouter, useRoute } from "vue-router";
import { useReturnTo } from "@/composables/useReturnTo";
import { useQuery } from "@tanstack/vue-query";
import DataTable from "primevue/datatable";
import Column from "primevue/column";
import Button from "primevue/button";
import StatusTag from "@/components/StatusTag.vue";
import PageHeader from "@/components/PageHeader.vue";
import { api } from "@/api/client";
import { formatMoney } from "@/utils/money";

interface Project {
  project_id: string;
  customer_id: string;
  code: string;
  name: string;
  currency: string;
  contract_value: string | null;
  status: "ACTIVE" | "ON_HOLD" | "COMPLETED" | "CANCELLED";
  start_date: string | null;
  end_date: string | null;
  notes: string | null;
  active: boolean;
  created_at: string;
}

interface ProjectSummary {
  project_id: string;
  invoiced_total: string;
  paid_total: string;
  outstanding_total: string;
  contract_value: string | null;
  contract_remaining: string | null;
  invoice_count: number;
  bill_total: string;
}

interface Invoice {
  invoice_id: string;
  invoice_number: string | null;
  invoice_type: string;
  amount: string;
  balance_due: string;
  status: string;
  currency: string;
  issue_date: string | null;
  due_date: string | null;
}

interface Customer {
  customer_id: string;
  name: string;
}

const props = defineProps<{ projectId: string }>();
const router = useRouter();
const route = useRoute();
const { backTo } = useReturnTo({ name: "projects" });

const { data: project, isLoading } = useQuery<Project>({
  queryKey: ["project", () => props.projectId],
  queryFn: async () => (await api.get<Project>(`/projects/${props.projectId}`)).data,
});

const { data: summary } = useQuery<ProjectSummary>({
  queryKey: ["project-summary", () => props.projectId],
  queryFn: async () =>
    (await api.get<ProjectSummary>(`/projects/${props.projectId}/summary`)).data,
});

const { data: invoices } = useQuery<Invoice[]>({
  queryKey: ["project-invoices", () => props.projectId],
  queryFn: async () =>
    (await api.get<Invoice[]>(`/projects/${props.projectId}/invoices`)).data,
});

interface Quotation {
  quotation_id: string;
  quotation_number: string | null;
  currency: string;
  amount: string;
  status: string;
  issue_date: string | null;
  valid_until: string | null;
}

const { data: quotations } = useQuery<Quotation[]>({
  queryKey: ["project-quotations", () => props.projectId],
  queryFn: async () =>
    (
      await api.get<Quotation[]>("/quotations", {
        params: { project_id: props.projectId },
      })
    ).data,
});

function openQuotation(row: Quotation) {
  router.push({ name: "quotation-detail", params: { quotationId: row.quotation_id } });
}

const { data: customer } = useQuery<Customer>({
  queryKey: ["customer", () => project.value?.customer_id],
  queryFn: async () =>
    (await api.get<Customer>(`/customers/${project.value!.customer_id}`)).data,
  enabled: () => !!project.value?.customer_id,
});

const contractPct = computed(() => {
  if (!summary.value?.contract_value) return null;
  const cv = Number(summary.value.contract_value);
  if (cv <= 0) return null;
  const inv = Number(summary.value.invoiced_total);
  return Math.min(100, (inv / cv) * 100);
});

function backToList() {
  router.push(backTo.value);
}
function openEdit() {
  router.push({ name: "project-edit", params: { projectId: props.projectId }, query: { from: route.fullPath } });
}
function openInvoice(row: Invoice) {
  router.push({ name: "invoice-edit", params: { id: row.invoice_id }, query: { from: route.fullPath } });
}
</script>

<template>
  <section class="page">
    <div class="back">
      <Button label="Projects" text size="small" @click="backToList">
        <template #icon><AppIcon name="arrow-left" /></template>
      </Button>
    </div>

    <div v-if="isLoading">Loading…</div>
    <div v-else-if="project" class="stack">
      <PageHeader eyebrow="02 / Sales" :title="project.name">
        <template #status>
          <code class="project-code">{{ project.code }}</code>
          <StatusTag :status="project.status" />
        </template>
        <template v-if="customer" #lede>
          Customer: <strong>{{ customer.name }}</strong>
          · Currency: <code>{{ project.currency }}</code>
        </template>
        <Button label="Edit" severity="secondary" outlined @click="openEdit">
          <template #icon><AppIcon name="pencil" /></template>
        </Button>
      </PageHeader>

      <div class="stats">
        <div class="stat">
          <div class="stat-label">Invoiced</div>
          <div class="stat-value">
            {{ summary ? formatMoney(summary.invoiced_total, project.currency) : "—" }}
          </div>
          <div class="stat-sub">{{ summary?.invoice_count ?? 0 }} invoices</div>
        </div>
        <div class="stat">
          <div class="stat-label">Paid</div>
          <div class="stat-value">
            {{ summary ? formatMoney(summary.paid_total, project.currency) : "—" }}
          </div>
        </div>
        <div class="stat">
          <div class="stat-label">Outstanding</div>
          <div class="stat-value">
            {{ summary ? formatMoney(summary.outstanding_total, project.currency) : "—" }}
          </div>
        </div>
        <div class="stat">
          <div class="stat-label">Contract value</div>
          <div class="stat-value">
            {{
              summary?.contract_value
                ? formatMoney(summary.contract_value, project.currency)
                : "—"
            }}
          </div>
          <div v-if="contractPct !== null" class="stat-sub">
            {{ contractPct.toFixed(1) }}% billed
            · {{ formatMoney(summary!.contract_remaining, project.currency) }} left
          </div>
        </div>
      </div>

      <div class="card">
        <div class="card-head">
          <h3 class="card-title">Invoices on this project</h3>
        </div>
        <DataTable :value="invoices ?? []" data-key="invoice_id" striped-rows>
          <template #empty>
            <div class="empty-mini">No invoices tagged to this project yet.</div>
          </template>
          <Column field="invoice_number" header="Number" body-class="cell-nowrap">
            <template #body="{ data: row }">
              <span v-if="row.invoice_number" class="doc-no">{{ row.invoice_number }}</span>
              <span v-else class="muted">(draft)</span>
            </template>
          </Column>
          <Column field="invoice_type" header="Type" />
          <Column header="Issue date" body-class="cell-nowrap">
            <template #body="{ data: row }">
              <span v-if="row.issue_date">{{ row.issue_date }}</span>
              <span v-else class="muted">—</span>
            </template>
          </Column>
          <Column header="Amount" body-class="cell-nowrap">
            <template #body="{ data: row }">
              <span class="num">{{ formatMoney(row.amount, row.currency) }}</span>
            </template>
          </Column>
          <Column header="Balance" body-class="cell-nowrap">
            <template #body="{ data: row }">
              <span class="num">{{ formatMoney(row.balance_due, row.currency) }}</span>
            </template>
          </Column>
          <Column header="Status">
            <template #body="{ data: row }">
              <StatusTag :status="row.status" />
            </template>
          </Column>
          <Column header="" :style="{ width: '70px' }">
            <template #body="{ data: row }">
              <Button text rounded aria-label="Edit invoice" @click="openInvoice(row)">
                <template #icon><AppIcon name="pencil" /></template>
              </Button>
            </template>
          </Column>
        </DataTable>
      </div>

      <div class="card">
        <div class="card-head">
          <h3 class="card-title">Quotations on this project</h3>
        </div>
        <DataTable :value="quotations ?? []" data-key="quotation_id" striped-rows>
          <template #empty>
            <div class="empty-mini">No quotations tagged to this project yet.</div>
          </template>
          <Column header="Quote #" body-class="cell-nowrap">
            <template #body="{ data: row }">
              <span v-if="row.quotation_number" class="doc-no">{{ row.quotation_number }}</span>
              <span v-else class="muted">(draft)</span>
            </template>
          </Column>
          <Column header="Status">
            <template #body="{ data: row }">
              <StatusTag :status="row.status" />
            </template>
          </Column>
          <Column header="Issued">
            <template #body="{ data: row }">
              <span v-if="row.issue_date">{{ row.issue_date }}</span>
              <span v-else class="muted">—</span>
            </template>
          </Column>
          <Column header="Valid until">
            <template #body="{ data: row }">
              <span v-if="row.valid_until">{{ row.valid_until }}</span>
              <span v-else class="muted">—</span>
            </template>
          </Column>
          <Column header="Amount">
            <template #body="{ data: row }">
              <span class="num">{{ formatMoney(row.amount, row.currency) }}</span>
            </template>
          </Column>
          <Column header="" :style="{ width: '70px' }">
            <template #body="{ data: row }">
              <Button text rounded aria-label="Open quotation" @click="openQuotation(row)">
                <template #icon><AppIcon name="arrow-right" /></template>
              </Button>
            </template>
          </Column>
        </DataTable>
      </div>

      <div v-if="project.notes" class="card card-body">
        <h3 class="card-title">Notes</h3>
        <p class="notes">{{ project.notes }}</p>
      </div>
    </div>
  </section>
</template>

<style scoped>
.page { display: flex; flex-direction: column; gap: 16px; padding-bottom: 2rem; }
.back { margin-bottom: -8px; }
.stack { display: flex; flex-direction: column; gap: 24px; }
.stack :deep(.page-head) { margin-bottom: 0; }
.project-code {
  font: 500 13px var(--wb-font-mono);
  background: var(--wb-tint);
  color: var(--wb-ink);
  padding: 3px 8px;
}

/* KPI strip (as on Invoices): one bordered strip, hairlines between cells. */
.stats {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 1px;
  background: var(--wb-line);
  border: 1px solid var(--wb-line);
}
.stat { background: var(--wb-surface); padding: 22px 26px; min-width: 0; }
.stat-label { font: 500 11px/1.4 var(--wb-font-sans); text-transform: uppercase; letter-spacing: 1.3px; color: var(--wb-muted-soft); }
.stat-value {
  font: 500 26px/1.15 var(--wb-font-display);
  letter-spacing: -0.8px;
  color: var(--wb-ink);
  margin-top: 8px;
  font-variant-numeric: tabular-nums;
  overflow-wrap: anywhere;
}
.stat-sub { font-size: 13px; color: var(--wb-muted); margin-top: 6px; }

/* Table cards: title strip, then the table edge to edge. */
.card-head { display: flex; justify-content: space-between; align-items: center; padding: 18px 24px; border-bottom: 1px solid var(--wb-line); }
.card :deep(.p-datatable-table-container) { border: 0; }
.card-body { padding: 24px 28px; }
.notes { white-space: pre-wrap; color: var(--wb-ink); margin: 12px 0 0; line-height: 1.6; }
.num { font-variant-numeric: tabular-nums; }
.muted { color: var(--wb-muted-soft); }

@media (max-width: 900px) {
  .stats { grid-template-columns: repeat(2, 1fr); }
}
@media (max-width: 720px) {
  .stat { padding: 16px 18px; }
  .stat-value { font-size: 22px; }
}
</style>
