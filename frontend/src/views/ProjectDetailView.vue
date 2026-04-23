<script setup lang="ts">
import { computed } from "vue";
import { useRouter } from "vue-router";
import { useQuery } from "@tanstack/vue-query";
import DataTable from "primevue/datatable";
import Column from "primevue/column";
import Button from "primevue/button";
import Tag from "primevue/tag";
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

function quoteStatusSeverity(s: string) {
  switch (s) {
    case "ACCEPTED": case "INVOICED": return "success";
    case "SENT": return "info";
    case "EXPIRED": return "warning";
    case "DECLINED": return "danger";
    case "VOID": return "secondary";
    case "DRAFT": return "contrast";
    default: return undefined;
  }
}

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

function statusSeverity(s: string) {
  switch (s) {
    case "ACTIVE": case "PAID": return "success";
    case "ON_HOLD": case "PARTIAL": return "warning";
    case "COMPLETED": case "SENT": return "info";
    case "CANCELLED": case "VOID": return "danger";
    case "DRAFT": return "secondary";
    default: return undefined;
  }
}

function backToList() {
  router.push({ name: "projects" });
}
function openEdit() {
  router.push({ name: "project-edit", params: { projectId: props.projectId } });
}
function openInvoice(row: Invoice) {
  router.push({ name: "invoice-edit", params: { id: row.invoice_id } });
}
</script>

<template>
  <section class="page">
    <div class="back">
      <Button label="Projects" icon="pi pi-arrow-left" text size="small" @click="backToList" />
    </div>

    <div v-if="isLoading">Loading…</div>
    <div v-else-if="project" class="stack">
      <div class="header">
        <div>
          <div class="eyebrow">
            <code>{{ project.code }}</code>
            <Tag :value="project.status" :severity="statusSeverity(project.status)" />
          </div>
          <h1>{{ project.name }}</h1>
          <p v-if="customer" class="subtitle">
            Customer: <strong>{{ customer.name }}</strong>
            · Currency: <span class="mono">{{ project.currency }}</span>
          </p>
        </div>
        <div class="actions">
          <Button label="Edit" icon="pi pi-pencil" outlined @click="openEdit" />
        </div>
      </div>

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
          <h3>Invoices on this project</h3>
        </div>
        <DataTable :value="invoices ?? []" data-key="invoice_id" striped-rows>
          <template #empty>
            <div class="empty-state">No invoices tagged to this project yet.</div>
          </template>
          <Column field="invoice_number" header="Number">
            <template #body="{ data: row }">
              <code v-if="row.invoice_number">{{ row.invoice_number }}</code>
              <span v-else class="muted">(draft)</span>
            </template>
          </Column>
          <Column field="invoice_type" header="Type" />
          <Column header="Issue date">
            <template #body="{ data: row }">
              <span v-if="row.issue_date">{{ row.issue_date }}</span>
              <span v-else class="muted">—</span>
            </template>
          </Column>
          <Column header="Amount">
            <template #body="{ data: row }">
              <span class="num">{{ formatMoney(row.amount, row.currency) }}</span>
            </template>
          </Column>
          <Column header="Balance">
            <template #body="{ data: row }">
              <span class="num">{{ formatMoney(row.balance_due, row.currency) }}</span>
            </template>
          </Column>
          <Column header="Status">
            <template #body="{ data: row }">
              <Tag :value="row.status" :severity="statusSeverity(row.status)" />
            </template>
          </Column>
          <Column header="" :style="{ width: '70px' }">
            <template #body="{ data: row }">
              <Button icon="pi pi-pencil" text rounded @click="openInvoice(row)" />
            </template>
          </Column>
        </DataTable>
      </div>

      <div class="card">
        <div class="card-head">
          <h3>Quotations on this project</h3>
        </div>
        <DataTable :value="quotations ?? []" data-key="quotation_id" striped-rows>
          <template #empty>
            <div class="empty-state">No quotations tagged to this project yet.</div>
          </template>
          <Column header="Quote #">
            <template #body="{ data: row }">
              <code v-if="row.quotation_number">{{ row.quotation_number }}</code>
              <span v-else class="muted">(draft)</span>
            </template>
          </Column>
          <Column header="Status">
            <template #body="{ data: row }">
              <Tag :value="row.status" :severity="quoteStatusSeverity(row.status)" />
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
              <Button icon="pi pi-arrow-right" text rounded @click="openQuotation(row)" />
            </template>
          </Column>
        </DataTable>
      </div>

      <div v-if="project.notes" class="card">
        <h3>Notes</h3>
        <p class="notes">{{ project.notes }}</p>
      </div>
    </div>
  </section>
</template>

<style scoped>
.page { display: flex; flex-direction: column; gap: 1rem; padding-bottom: 2rem; }
.back { margin-bottom: -0.5rem; }
.stack { display: flex; flex-direction: column; gap: 1.25rem; }
.header { display: flex; justify-content: space-between; align-items: flex-start; gap: 1rem; }
.header h1 { margin: 0.2rem 0 0; font-size: 1.5rem; }
.eyebrow { display: flex; gap: 0.6rem; align-items: center; }
.eyebrow code { background: var(--color-surface-alt); padding: 0.15rem 0.5rem; border-radius: 4px; }
.subtitle { margin: 0.25rem 0 0; color: var(--color-text-muted); font-size: 0.9rem; }
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
.actions { display: flex; gap: 0.5rem; }

.stats { display: grid; grid-template-columns: repeat(4, 1fr); gap: 0.85rem; }
.stat {
  background: var(--color-surface); border: 1px solid var(--color-border, #e2e8f0);
  border-radius: 10px; padding: 0.85rem 1rem;
}
.stat-label { font-size: 0.74rem; text-transform: uppercase; letter-spacing: 0.06em; color: var(--color-text-muted); }
.stat-value { font-size: 1.2rem; font-weight: 600; margin-top: 0.2rem; font-variant-numeric: tabular-nums; }
.stat-sub { font-size: 0.78rem; color: var(--color-text-muted); margin-top: 0.2rem; }

.card {
  background: var(--color-surface); border: 1px solid var(--color-border, #e2e8f0);
  border-radius: 12px; padding: 1rem 1.25rem;
}
.card-head { display: flex; justify-content: space-between; align-items: center; margin-bottom: 0.5rem; }
.card h3 { margin: 0; font-size: 1rem; }
.notes { white-space: pre-wrap; color: var(--color-text); }
.num { font-variant-numeric: tabular-nums; }
.muted { color: var(--color-text-muted); }

@media (max-width: 900px) {
  .stats { grid-template-columns: repeat(2, 1fr); }
}
</style>
