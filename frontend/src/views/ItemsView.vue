<script setup lang="ts">
import { computed } from "vue";
import { useRouter } from "vue-router";
import { useQuery, useMutation, useQueryClient } from "@tanstack/vue-query";
import { useConfirm } from "primevue/useconfirm";
import DataTable from "primevue/datatable";
import Column from "primevue/column";
import Button from "primevue/button";
import Tag from "primevue/tag";
import { api } from "@/api/client";
import { formatMoney } from "@/utils/money";

interface Item {
  item_id: string;
  sku: string | null;
  name: string;
  item_type: "SERVICE" | "USAGE" | "FIXED_FEE";
  description: string | null;
  default_currency: string;
  default_unit_price: string | null;
  default_purchase_price: string | null;
  revenue_account_id: number | null;
  expense_account_id: number | null;
  is_sold: boolean;
  is_purchased: boolean;
  active: boolean;
  created_at: string;
}

interface Account {
  account_id: number;
  code: string;
  name: string;
  type: string;
}

const router = useRouter();
const queryClient = useQueryClient();
const confirm = useConfirm();

const { data: items, isLoading } = useQuery<Item[]>({
  queryKey: ["items"],
  queryFn: async () => (await api.get<Item[]>("/items")).data,
});

const { data: accounts } = useQuery<Account[]>({
  queryKey: ["chart-of-accounts"],
  queryFn: async () => (await api.get<Account[]>("/accounting/chart-of-accounts")).data,
});

const accountNameById = computed(() => {
  const map: Record<number, string> = {};
  for (const a of accounts.value ?? []) map[a.account_id] = `${a.code} · ${a.name}`;
  return map;
});

const deactivateItem = useMutation({
  mutationFn: async (id: string) => api.delete<Item>(`/items/${id}`),
  onSuccess: () => queryClient.invalidateQueries({ queryKey: ["items"] }),
});

const reactivateItem = useMutation({
  mutationFn: async (id: string) => api.patch<Item>(`/items/${id}`, { active: true }),
  onSuccess: () => queryClient.invalidateQueries({ queryKey: ["items"] }),
});

function openCreate() {
  router.push({ name: "item-new" });
}

function openEdit(row: Item) {
  router.push({ name: "item-edit", params: { itemId: row.item_id } });
}

function openDetail(row: Item) {
  router.push({ name: "item-detail", params: { itemId: row.item_id } });
}

function confirmDeactivate(row: Item) {
  confirm.require({
    message: `Deactivate "${row.name}"? It will be hidden from active lists but kept for ledger history.`,
    header: "Deactivate item",
    icon: "pi pi-exclamation-triangle",
    acceptLabel: "Deactivate",
    rejectLabel: "Cancel",
    acceptClass: "p-button-danger",
    accept: () => deactivateItem.mutate(row.item_id),
  });
}

function typeSeverity(t: string) {
  switch (t) {
    case "FIXED_FEE": return "info";
    case "SERVICE": return "success";
    case "USAGE": return "warning";
    default: return undefined;
  }
}
</script>

<template>
  <section>
    <header class="page-header">
      <div>
        <h1>Products &amp; Services</h1>
        <p class="subtitle">
          Catalog of billable services — fixed fees, recurring support, and metered usage.
          Each item maps to a revenue account for posting.
        </p>
      </div>
      <div class="page-actions">
        <Button label="New item" icon="pi pi-plus" @click="openCreate" />
      </div>
    </header>

    <DataTable :value="items ?? []" :loading="isLoading" data-key="item_id" striped-rows>
      <template #empty>
        <div class="empty-state">
          <i class="pi pi-tags" />
          <div>No items yet. Add the services you bill for.</div>
        </div>
      </template>
      <Column field="sku" header="SKU">
        <template #body="{ data: row }">
          <code v-if="row.sku">{{ row.sku }}</code>
          <span v-else class="muted">—</span>
        </template>
      </Column>
      <Column field="name" header="Name" />
      <Column header="Type">
        <template #body="{ data: row }">
          <Tag :value="row.item_type" :severity="typeSeverity(row.item_type)" />
        </template>
      </Column>
      <Column header="Unit price">
        <template #body="{ data: row }">
          <span v-if="row.default_unit_price" class="num">
            {{ formatMoney(row.default_unit_price, row.default_currency) }}
          </span>
          <span v-else class="muted">—</span>
        </template>
      </Column>
      <Column header="Revenue account">
        <template #body="{ data: row }">
          <span v-if="row.revenue_account_id" class="muted small">
            {{ accountNameById[row.revenue_account_id] ?? '#' + row.revenue_account_id }}
          </span>
          <span v-else class="muted">—</span>
        </template>
      </Column>
      <Column header="Sold / Bought">
        <template #body="{ data: row }">
          <div class="flags">
            <Tag v-if="row.is_sold" value="Sold" severity="success" />
            <Tag v-if="row.is_purchased" value="Bought" severity="info" />
            <span v-if="!row.is_sold && !row.is_purchased" class="muted">—</span>
          </div>
        </template>
      </Column>
      <Column header="Status">
        <template #body="{ data: row }">
          <Tag
            :value="row.active ? 'Active' : 'Inactive'"
            :severity="row.active ? 'success' : 'secondary'"
          />
        </template>
      </Column>
      <Column header="" :style="{ width: '180px' }">
        <template #body="{ data: row }">
          <div class="row-actions">
            <Button icon="pi pi-eye" text rounded title="View" @click="openDetail(row)" />
            <Button icon="pi pi-pencil" text rounded title="Edit" @click="openEdit(row)" />
            <Button
              v-if="row.active"
              icon="pi pi-ban"
              text
              rounded
              severity="danger"
              title="Deactivate"
              :loading="deactivateItem.isPending.value"
              @click="confirmDeactivate(row)"
            />
            <Button
              v-else
              icon="pi pi-undo"
              text
              rounded
              severity="success"
              title="Reactivate"
              :loading="reactivateItem.isPending.value"
              @click="reactivateItem.mutate(row.item_id)"
            />
          </div>
        </template>
      </Column>
    </DataTable>
  </section>
</template>

<style scoped>
.muted { color: var(--color-text-muted); }
.small { font-size: 0.82rem; }
code { background: var(--color-surface-alt); padding: 0.1rem 0.4rem; border-radius: 4px; font-size: 0.85em; }
.row-actions { display: flex; gap: 0.15rem; }
.num { font-variant-numeric: tabular-nums; }
.flags { display: flex; gap: 0.3rem; flex-wrap: wrap; }
</style>
