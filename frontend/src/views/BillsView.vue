<script setup lang="ts">
import { ref, computed } from "vue";
import { useRouter } from "vue-router";
import { useQuery } from "@tanstack/vue-query";
import DataTable from "primevue/datatable";
import Column from "primevue/column";
import Button from "primevue/button";
import Dropdown from "primevue/dropdown";
import Tag from "primevue/tag";
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

const statusFilter = ref<string | null>(null);
const statusOptions = [
  { label: "All", value: null },
  { label: "Draft", value: "DRAFT" },
  { label: "Open", value: "OPEN" },
  { label: "Partial", value: "PARTIAL" },
  { label: "Paid", value: "PAID" },
  { label: "Void", value: "VOID" },
];

const { data: bills, isLoading } = useQuery<Bill[]>({
  queryKey: ["bills", statusFilter],
  queryFn: async () => {
    const params: Record<string, string> = {};
    if (statusFilter.value) params.status = statusFilter.value;
    return (await api.get<Bill[]>("/bills", { params })).data;
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
        <Button label="New bill" icon="pi pi-plus" @click="router.push('/bills/new')" />
      </div>
    </header>

    <div class="filters">
      <Dropdown
        v-model="statusFilter"
        :options="statusOptions"
        option-label="label"
        option-value="value"
        placeholder="Filter by status"
        show-clear
      />
    </div>

    <DataTable
      :value="bills ?? []"
      :loading="isLoading"
      data-key="bill_id"
      striped-rows
      selection-mode="single"
      @row-click="(ev) => router.push(`/bills/${(ev.data as Bill).bill_id}/edit`)"
    >
      <template #empty>
        <div class="empty-state">
          <i class="pi pi-file" />
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
    </DataTable>
  </section>
</template>

<style scoped>
.filters { margin: 0 0 1rem; }
code { background: #f1f5f9; padding: 0.05rem 0.35rem; border-radius: 4px; font-size: 0.82em; }
.num { font-variant-numeric: tabular-nums; }
.muted { color: var(--color-text-muted, #64748b); }
:deep(.p-datatable-tbody > tr) { cursor: pointer; }
</style>
