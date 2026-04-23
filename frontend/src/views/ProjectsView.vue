<script setup lang="ts">
import { computed, ref } from "vue";
import { useRouter } from "vue-router";
import { useQuery } from "@tanstack/vue-query";
import DataTable from "primevue/datatable";
import Column from "primevue/column";
import Button from "primevue/button";
import Tag from "primevue/tag";
import Dropdown from "primevue/dropdown";
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

interface Customer {
  customer_id: string;
  name: string;
}

const router = useRouter();

const statusFilter = ref<string | null>(null);
const statusOptions = [
  { label: "Any status", value: null },
  { label: "Active", value: "ACTIVE" },
  { label: "On hold", value: "ON_HOLD" },
  { label: "Completed", value: "COMPLETED" },
  { label: "Cancelled", value: "CANCELLED" },
];

const { data: projects, isLoading } = useQuery<Project[]>({
  queryKey: ["projects", statusFilter],
  queryFn: async () => {
    const params: Record<string, string> = {};
    if (statusFilter.value) params.status = statusFilter.value;
    return (await api.get<Project[]>("/projects", { params })).data;
  },
});

const { data: customers } = useQuery<Customer[]>({
  queryKey: ["customers"],
  queryFn: async () => (await api.get<Customer[]>("/customers")).data,
});

const customerNameById = computed(() => {
  const map: Record<string, string> = {};
  for (const c of customers.value ?? []) map[c.customer_id] = c.name;
  return map;
});

function openCreate() {
  router.push({ name: "project-new" });
}
function openEdit(row: Project) {
  router.push({ name: "project-edit", params: { projectId: row.project_id } });
}
function openDetail(row: Project) {
  router.push({ name: "project-detail", params: { projectId: row.project_id } });
}

function statusSeverity(s: string) {
  switch (s) {
    case "ACTIVE": return "success";
    case "ON_HOLD": return "warning";
    case "COMPLETED": return "info";
    case "CANCELLED": return "danger";
    default: return undefined;
  }
}
</script>

<template>
  <section>
    <header class="page-header">
      <div>
        <h1>Projects</h1>
        <p class="subtitle">
          Track engagements, contract value, and milestone billing. Each invoice or bill
          can be tagged to a project for consolidated reporting.
        </p>
      </div>
      <div class="page-actions">
        <Dropdown
          v-model="statusFilter"
          :options="statusOptions"
          option-label="label"
          option-value="value"
          placeholder="Any status"
          :show-clear="false"
          style="min-width: 160px"
        />
        <Button label="New project" icon="pi pi-plus" @click="openCreate" />
      </div>
    </header>

    <DataTable :value="projects ?? []" :loading="isLoading" data-key="project_id" striped-rows>
      <template #empty>
        <div class="empty-state">
          <i class="pi pi-folder-open" />
          <div>No projects yet. Create one to tag invoices and bills.</div>
        </div>
      </template>
      <Column header="Code">
        <template #body="{ data: row }">
          <code>{{ row.code }}</code>
        </template>
      </Column>
      <Column field="name" header="Name" />
      <Column header="Customer">
        <template #body="{ data: row }">
          <span class="muted small">
            {{ customerNameById[row.customer_id] ?? row.customer_id.slice(0, 8) }}
          </span>
        </template>
      </Column>
      <Column header="Currency">
        <template #body="{ data: row }">
          <span class="mono">{{ row.currency }}</span>
        </template>
      </Column>
      <Column header="Contract value">
        <template #body="{ data: row }">
          <span v-if="row.contract_value" class="num">
            {{ formatMoney(row.contract_value, row.currency) }}
          </span>
          <span v-else class="muted">—</span>
        </template>
      </Column>
      <Column header="Status">
        <template #body="{ data: row }">
          <Tag :value="row.status" :severity="statusSeverity(row.status)" />
        </template>
      </Column>
      <Column header="" :style="{ width: '140px' }">
        <template #body="{ data: row }">
          <div class="row-actions">
            <Button icon="pi pi-eye" text rounded title="View" @click="openDetail(row)" />
            <Button icon="pi pi-pencil" text rounded title="Edit" @click="openEdit(row)" />
          </div>
        </template>
      </Column>
    </DataTable>
  </section>
</template>

<style scoped>
.muted { color: var(--color-text-muted); }
.small { font-size: 0.82rem; }
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 0.85em; }
code { background: var(--color-surface-alt); padding: 0.1rem 0.4rem; border-radius: 4px; font-size: 0.85em; }
.row-actions { display: flex; gap: 0.15rem; }
.num { font-variant-numeric: tabular-nums; }
.page-actions { display: flex; gap: 0.5rem; align-items: center; }
</style>
