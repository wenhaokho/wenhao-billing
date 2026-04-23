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

const statusFilter = ref<string | null>(null);
const statusOptions = [
  { label: "All", value: null },
  { label: "Draft", value: "DRAFT" },
  { label: "Sent", value: "SENT" },
  { label: "Accepted", value: "ACCEPTED" },
  { label: "Declined", value: "DECLINED" },
  { label: "Expired", value: "EXPIRED" },
  { label: "Invoiced", value: "INVOICED" },
  { label: "Void", value: "VOID" },
];

const { data: quotations, isLoading } = useQuery<Quotation[]>({
  queryKey: ["quotations", statusFilter],
  queryFn: async () => {
    const params: Record<string, string> = {};
    if (statusFilter.value) params.status = statusFilter.value;
    return (await api.get<Quotation[]>("/quotations", { params })).data;
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
.filters { margin: 0 0 1rem; }
code { background: var(--color-bg); padding: 0.05rem 0.35rem; border-radius: 4px; font-size: 0.82em; }
.num { font-variant-numeric: tabular-nums; }
.muted { color: var(--color-text-muted, #64748b); }
:deep(.p-datatable-tbody > tr) { cursor: pointer; }
</style>
