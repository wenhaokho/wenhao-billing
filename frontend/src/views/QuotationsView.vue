<script setup lang="ts">
import { ref, computed } from "vue";
import { useRouter } from "vue-router";
import { useQuery } from "@tanstack/vue-query";
import DataTable from "primevue/datatable";
import Column from "primevue/column";
import Button from "primevue/button";
import TabView from "primevue/tabview";
import TabPanel from "primevue/tabpanel";
import Tag from "primevue/tag";
import { api } from "@/api/client";
import { formatAmount } from "@/utils/money";

interface Customer {
  customer_id: string;
  name: string;
}

interface Quotation {
  quotation_id: string;
  customer_id: string;
  quotation_number: string | null;
  currency: string;
  amount: string;
  status: string;
  issue_date: string | null;
  valid_until: string | null;
  created_at: string;
}

const router = useRouter();

// 0 = Open (DRAFT + SENT), 1 = Accepted (ACCEPTED + INVOICED), 2 = All
const activeTab = ref(0);
const TAB_TO_STATUSES: Record<number, string[] | null> = {
  0: ["DRAFT", "SENT"],
  1: ["ACCEPTED", "INVOICED"],
  2: null,
};

const { data: quotations, isLoading } = useQuery<Quotation[]>({
  queryKey: ["quotations", activeTab],
  queryFn: async () => {
    const statuses = TAB_TO_STATUSES[activeTab.value];
    const params = new URLSearchParams();
    if (statuses) for (const s of statuses) params.append("status", s);
    const qs = params.toString();
    const url = qs ? `/quotations?${qs}` : "/quotations";
    return (await api.get<Quotation[]>(url)).data;
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

function statusSeverity(status: string) {
  switch (status) {
    case "ACCEPTED": return "success";
    case "SENT": return "info";
    case "INVOICED": return "success";
    case "EXPIRED": return "warning";
    case "DECLINED": return "danger";
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
        <h1>Quotations</h1>
        <p class="subtitle">
          Estimates sent to customers. Accepted quotes can be converted to a
          <code>DRAFT</code> invoice in one click.
        </p>
      </div>
      <div class="page-actions">
        <Button label="New quotation" icon="pi pi-plus" @click="router.push('/quotations/new')" />
      </div>
    </header>

    <TabView v-model:active-index="activeTab" class="status-tabs">
      <TabPanel header="Open" />
      <TabPanel header="Accepted" />
      <TabPanel header="All" />
    </TabView>

    <DataTable
      :value="quotations ?? []"
      :loading="isLoading"
      data-key="quotation_id"
      striped-rows
      selection-mode="single"
      @row-click="(ev) => router.push(`/quotations/${(ev.data as Quotation).quotation_id}`)"
    >
      <template #empty>
        <div class="empty-state">
          <i class="pi pi-file-edit" />
          <div>No quotations match the current filter.</div>
        </div>
      </template>
      <Column header="Quote #">
        <template #body="{ data: row }">
          <code v-if="row.quotation_number">{{ row.quotation_number }}</code>
          <span v-else class="muted">—</span>
        </template>
      </Column>
      <Column header="Customer">
        <template #body="{ data: row }">
          {{ customerNameById[row.customer_id] ?? row.customer_id.slice(0, 8) }}
        </template>
      </Column>
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
      <Column field="currency" header="Ccy" />
      <Column field="issue_date" header="Issued" />
      <Column field="valid_until" header="Valid until" />
    </DataTable>
  </section>
</template>

<style scoped>
.status-tabs { margin-bottom: 0.5rem; }
.status-tabs :deep(.p-tabview-panels) { display: none; }
code { background: var(--color-bg); padding: 0.05rem 0.35rem; border-radius: 4px; font-size: 0.82em; }
.num { font-variant-numeric: tabular-nums; }
.muted { color: var(--color-text-muted, #64748b); }
:deep(.p-datatable-tbody > tr) { cursor: pointer; }
</style>
