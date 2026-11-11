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
import PageHeader from "@/components/PageHeader.vue";
import EmptyState from "@/components/EmptyState.vue";
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

// Each tab's value is the status filter it applies: open (DRAFT + SENT),
// accepted (ACCEPTED + INVOICED), all. The active tab is reflected in the URL
// (?tab=open) so it is restored when navigating back to this list after
// opening an item.
const route = useRoute();
const TAB_SLUGS = ["open", "accepted", "all"];
function readTabFromQuery(): string {
  const q = String(route.query.tab);
  return TAB_SLUGS.includes(q) ? q : TAB_SLUGS[0];
}
const activeTab = ref<string>(readTabFromQuery());
watch(activeTab, (t) => {
  router.replace({ query: { ...route.query, tab: TAB_SLUGS.includes(t) ? t : TAB_SLUGS[0] } });
});
const TAB_TO_STATUSES: Record<string, string[] | null> = {
  open: ["DRAFT", "SENT"],
  accepted: ["ACCEPTED", "INVOICED"],
  all: null,
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

</script>

<template>
  <section>
    <PageHeader eyebrow="02 / Sales" title="Quotations">
      <template #lede>
        Estimates sent to customers. Accepted quotes can be converted to a
        <code>DRAFT</code> invoice in one click.
      </template>
      <Button label="New quotation" @click="router.push('/quotations/new')">
        <template #icon><AppIcon name="plus" /></template>
      </Button>
    </PageHeader>

    <Tabs v-model:value="activeTab" class="list-tabs">
      <TabList>
        <Tab value="open">Open</Tab>
        <Tab value="accepted">Accepted</Tab>
        <Tab value="all">All</Tab>
      </TabList>
    </Tabs>

    <DataTable
      :value="quotations ?? []"
      :loading="isLoading"
      data-key="quotation_id"
      striped-rows
      selection-mode="single"
      @row-click="(ev) => router.push({ path: `/quotations/${(ev.data as Quotation).quotation_id}`, query: { from: route.fullPath } })"
    >
      <template #empty>
        <EmptyState icon="file-pen" title="No quotations here" message="Nothing in the pipeline matches this filter.">
          <Button label="New quotation" severity="secondary" outlined @click="router.push('/quotations/new')" />
        </EmptyState>
      </template>
      <Column header="Quote #" body-class="cell-nowrap">
        <template #body="{ data: row }">
          <span v-if="row.quotation_number" class="doc-no">{{ row.quotation_number }}</span>
          <span v-else class="muted">—</span>
        </template>
      </Column>
      <Column header="Customer">
        <template #body="{ data: row }">
          <span class="name">{{ customerNameById[row.customer_id] ?? row.customer_id.slice(0, 8) }}</span>
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
      <Column field="currency" header="Ccy" body-class="cell-nowrap cell-muted" />
      <Column field="issue_date" header="Issued" body-class="cell-nowrap cell-muted" />
      <Column field="valid_until" header="Valid until" body-class="cell-nowrap" />
    </DataTable>
  </section>
</template>

<style scoped>
.num { font-variant-numeric: tabular-nums; }
.name { font-weight: 500; }
.muted { color: var(--wb-muted-soft); }
:deep(.p-datatable-tbody > tr) { cursor: pointer; }
</style>
