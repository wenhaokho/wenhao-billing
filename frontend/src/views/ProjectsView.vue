<script setup lang="ts">
import AppIcon from "@/components/AppIcon.vue";
import { computed, ref, watch } from "vue";
import { useRouter, useRoute } from "vue-router";
import { useQuery } from "@tanstack/vue-query";
import DataTable from "primevue/datatable";
import Column from "primevue/column";
import Button from "primevue/button";
import StatusTag from "@/components/StatusTag.vue";
import Tabs from "primevue/tabs";
import TabList from "primevue/tablist";
import Tab from "primevue/tab";
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
const route = useRoute();

// Each tab's value is the status filter it applies: active (ACTIVE), hold
// (ON_HOLD), all. The active tab is reflected in the URL (?tab=active) so it
// is restored when navigating back to this list after opening an item.
const TAB_SLUGS = ["active", "hold", "all"];
function readTabFromQuery(): string {
  const q = String(route.query.tab);
  return TAB_SLUGS.includes(q) ? q : TAB_SLUGS[0];
}
const activeTab = ref<string>(readTabFromQuery());
watch(activeTab, (t) => {
  router.replace({ query: { ...route.query, tab: TAB_SLUGS.includes(t) ? t : TAB_SLUGS[0] } });
});
const TAB_TO_STATUSES: Record<string, string[] | null> = {
  active: ["ACTIVE"],
  hold: ["ON_HOLD"],
  all: null,
};

const { data: projects, isLoading } = useQuery<Project[]>({
  queryKey: ["projects", activeTab],
  queryFn: async () => {
    const statuses = TAB_TO_STATUSES[activeTab.value];
    const params = new URLSearchParams();
    if (statuses) for (const s of statuses) params.append("status", s);
    const qs = params.toString();
    const url = qs ? `/projects?${qs}` : "/projects";
    return (await api.get<Project[]>(url)).data;
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
  router.push({ name: "project-edit", params: { projectId: row.project_id }, query: { from: route.fullPath } });
}
function openDetail(row: Project) {
  router.push({ name: "project-detail", params: { projectId: row.project_id }, query: { from: route.fullPath } });
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
        <Button label="New project" @click="openCreate">
          <template #icon><AppIcon name="plus" /></template>
        </Button>
      </div>
    </header>

    <Tabs v-model:value="activeTab" class="status-tabs">
      <TabList>
        <Tab value="active">Active</Tab>
        <Tab value="hold">On hold</Tab>
        <Tab value="all">All</Tab>
      </TabList>
    </Tabs>

    <DataTable :value="projects ?? []" :loading="isLoading" data-key="project_id" striped-rows>
      <template #empty>
        <div class="empty-state">
          <AppIcon name="folder-open" />
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
          <StatusTag :status="row.status" />
        </template>
      </Column>
      <Column header="" :style="{ width: '140px' }">
        <template #body="{ data: row }">
          <div class="row-actions">
            <Button text rounded title="View" @click="openDetail(row)">
              <template #icon><AppIcon name="eye" /></template>
            </Button>
            <Button text rounded title="Edit" @click="openEdit(row)">
              <template #icon><AppIcon name="pencil" /></template>
            </Button>
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
.status-tabs { margin-bottom: 0.5rem; }
</style>
