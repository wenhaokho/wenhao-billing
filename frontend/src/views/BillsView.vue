<script setup lang="ts">
import AppIcon from "@/components/AppIcon.vue";
import { ref, computed, watch } from "vue";
import { useRouter, useRoute } from "vue-router";
import { useQuery } from "@tanstack/vue-query";
import DataTable from "primevue/datatable";
import Column from "primevue/column";
import Button from "primevue/button";
import Tabs from "primevue/tabs";
import TabList from "primevue/tablist";
import Tab from "primevue/tab";
import StatusTag from "@/components/StatusTag.vue";
import { api } from "@/api/client";
import { formatAmount } from "@/utils/money";

interface Vendor {
  vendor_id: string;
  name: string;
}

interface Bill {
  bill_id: string;
  vendor_id: string;
  bill_number: string | null;
  po_number: string | null;
  currency: string;
  amount: string;
  balance_due: string;
  status: string;
  issue_date: string | null;
  due_date: string | null;
  created_at: string;
}

const router = useRouter();

// Each tab's value is the status filter it applies: unpaid (OPEN + PARTIAL),
// draft, all. The active tab is reflected in the URL (?tab=unpaid) so it is
// restored when navigating back to this list after opening an item.
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
  unpaid: ["OPEN", "PARTIAL"],
  draft: ["DRAFT"],
  all: null,
};

const { data: bills, isLoading } = useQuery<Bill[]>({
  queryKey: ["bills", activeTab],
  queryFn: async () => {
    const statuses = TAB_TO_STATUSES[activeTab.value];
    const params = new URLSearchParams();
    if (statuses) for (const s of statuses) params.append("status", s);
    const qs = params.toString();
    const url = qs ? `/bills?${qs}` : "/bills";
    return (await api.get<Bill[]>(url)).data;
  },
});

const { data: vendors } = useQuery<Vendor[]>({
  queryKey: ["vendors"],
  queryFn: async () => (await api.get<Vendor[]>("/vendors")).data,
});

const vendorNameById = computed(() => {
  const map: Record<string, string> = {};
  for (const v of vendors.value ?? []) map[v.vendor_id] = v.name;
  return map;
});

</script>

<template>
  <section>
    <header class="page-header">
      <div>
        <h1>Bills</h1>
        <p class="subtitle">
          Vendor bills — the AP counterpart to customer invoices. Record spend
          against the expense accounts you posted in the chart of accounts.
        </p>
      </div>
      <div class="page-actions">
        <Button label="New bill" @click="router.push('/bills/new')">
          <template #icon><AppIcon name="plus" /></template>
        </Button>
      </div>
    </header>

    <Tabs v-model:value="activeTab" class="status-tabs">
      <TabList>
        <Tab value="unpaid">Unpaid</Tab>
        <Tab value="draft">Draft</Tab>
        <Tab value="all">All</Tab>
      </TabList>
    </Tabs>

    <DataTable
      :value="bills ?? []"
      :loading="isLoading"
      data-key="bill_id"
      striped-rows
      selection-mode="single"
      @row-click="(ev) => router.push({ path: `/bills/${(ev.data as Bill).bill_id}/edit`, query: { from: route.fullPath } })"
    >
      <template #empty>
        <div class="empty-state">
          <AppIcon name="file-text" />
          <div>No bills match the current filter.</div>
        </div>
      </template>
      <Column header="Bill #">
        <template #body="{ data: row }">
          <code v-if="row.bill_number">{{ row.bill_number }}</code>
          <span v-else class="muted">—</span>
        </template>
      </Column>
      <Column header="Vendor">
        <template #body="{ data: row }">
          {{ vendorNameById[row.vendor_id] ?? row.vendor_id.slice(0, 8) }}
        </template>
      </Column>
      <Column field="po_number" header="PO">
        <template #body="{ data: row }">
          <span v-if="row.po_number">{{ row.po_number }}</span>
          <span v-else class="muted">—</span>
        </template>
      </Column>
      <Column header="Status">
        <template #body="{ data: row }">
          <StatusTag :status="row.status" />
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
    </DataTable>
  </section>
</template>

<style scoped>
.status-tabs { margin-bottom: 0.5rem; }
code { background: var(--color-bg); padding: 0.05rem 0.35rem; border-radius: 4px; font-size: 0.82em; }
.num { font-variant-numeric: tabular-nums; }
.muted { color: var(--color-text-muted, #64748b); }
:deep(.p-datatable-tbody > tr) { cursor: pointer; }
</style>
