<script setup lang="ts">
import { ref, computed } from "vue";
import { useRouter } from "vue-router";
import { useQuery, useMutation, useQueryClient } from "@tanstack/vue-query";
import { useConfirm } from "primevue/useconfirm";
import DataTable from "primevue/datatable";
import Column from "primevue/column";
import Button from "primevue/button";
import Chip from "primevue/chip";
import Tag from "primevue/tag";
import InputSwitch from "primevue/inputswitch";
import { api } from "@/api/client";
import { formatAmount } from "@/utils/money";

interface CustomerBalance {
  customer_id: string;
  currency: string;
  balance: string;
  overdue: string;
}

interface Customer {
  customer_id: string;
  name: string;
  matching_aliases: string[];
  active: boolean;
  contact_name: string | null;
  contact_email: string | null;
  contact_phone: string | null;
  billing_address: string | null;
}

const router = useRouter();
const queryClient = useQueryClient();
const confirm = useConfirm();

const showInactive = ref(false);

const { data, isLoading } = useQuery<Customer[]>({
  queryKey: ["customers", showInactive],
  queryFn: async () => {
    const params = showInactive.value ? {} : { active: true };
    return (await api.get<Customer[]>("/customers", { params })).data;
  },
});

const { data: balances } = useQuery<CustomerBalance[]>({
  queryKey: ["customer-balances"],
  queryFn: async () =>
    (await api.get<CustomerBalance[]>("/customers/balances")).data,
});

const balancesByCustomer = computed(() => {
  const map: Record<string, CustomerBalance[]> = {};
  for (const b of balances.value ?? []) {
    (map[b.customer_id] ||= []).push(b);
  }
  return map;
});

const deactivateCustomer = useMutation({
  mutationFn: async (id: string) => api.delete<Customer>(`/customers/${id}`),
  onSuccess: () => queryClient.invalidateQueries({ queryKey: ["customers"] }),
});

const reactivateCustomer = useMutation({
  mutationFn: async (id: string) =>
    api.patch<Customer>(`/customers/${id}`, { active: true }),
  onSuccess: () => queryClient.invalidateQueries({ queryKey: ["customers"] }),
});

function openCreate() {
  router.push({ name: "customer-new" });
}

function openEdit(row: Customer) {
  router.push({
    name: "customer-detail",
    params: { customerId: row.customer_id },
    query: { edit: "1" },
  });
}

function openDetail(row: Customer) {
  router.push({ name: "customer-detail", params: { customerId: row.customer_id } });
}

function confirmDeactivate(row: Customer) {
  confirm.require({
    message: `Deactivate "${row.name}"? It will be hidden from active lists but kept for ledger history.`,
    header: "Deactivate customer",
    icon: "pi pi-exclamation-triangle",
    acceptLabel: "Deactivate",
    rejectLabel: "Cancel",
    acceptClass: "p-button-danger",
    accept: () => deactivateCustomer.mutate(row.customer_id),
  });
}
</script>

<template>
  <section>
    <header class="page-header">
      <div>
        <h1>Customers</h1>
        <p class="subtitle">
          Each customer has optional <strong>matching aliases</strong> — alternate names the AI
          matcher will recognize on incoming payments.
        </p>
      </div>
      <div class="page-actions">
        <label class="toggle">
          <InputSwitch v-model="showInactive" />
          <span>Show inactive</span>
        </label>
        <Button label="New customer" icon="pi pi-plus" @click="openCreate" />
      </div>
    </header>

    <DataTable
      :value="data ?? []"
      :loading="isLoading"
      data-key="customer_id"
      striped-rows
    >
      <template #empty>
        <div class="empty-state">
          <i class="pi pi-users" />
          <div>No customers yet. Click <strong>New customer</strong> to create your first.</div>
        </div>
      </template>
      <Column field="name" header="Name" />
      <Column header="Contact">
        <template #body="{ data: row }">
          <div v-if="row.contact_name || row.contact_email">
            <div v-if="row.contact_name">{{ row.contact_name }}</div>
            <div v-if="row.contact_email" class="muted small">{{ row.contact_email }}</div>
          </div>
          <span v-else class="muted">—</span>
        </template>
      </Column>
      <Column header="Matching aliases">
        <template #body="{ data: row }">
          <span v-if="row.matching_aliases.length === 0" class="muted">—</span>
          <span v-else class="chips">
            <Chip v-for="a in row.matching_aliases" :key="a" :label="a" />
          </span>
        </template>
      </Column>
      <Column header="Balance / Overdue" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }">
        <template #body="{ data: row }">
          <div v-if="balancesByCustomer[row.customer_id]?.length" class="balance-cell">
            <div
              v-for="b in balancesByCustomer[row.customer_id]"
              :key="b.currency"
              class="balance-line"
            >
              <span class="num">{{ formatAmount(b.balance, b.currency) }} {{ b.currency }}</span>
              <span v-if="Number(b.overdue) > 0" class="overdue">
                · {{ formatAmount(b.overdue, b.currency) }} overdue
              </span>
            </div>
          </div>
          <span v-else class="muted">—</span>
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
      <Column header="" :style="{ width: '200px' }">
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
              :loading="deactivateCustomer.isPending.value"
              @click="confirmDeactivate(row)"
            />
            <Button
              v-else
              icon="pi pi-undo"
              text
              rounded
              severity="success"
              title="Reactivate"
              :loading="reactivateCustomer.isPending.value"
              @click="reactivateCustomer.mutate(row.customer_id)"
            />
          </div>
        </template>
      </Column>
    </DataTable>

  </section>
</template>

<style scoped>
.muted { color: var(--color-text-subtle); }
.small { font-size: 0.8rem; }
.num { font-variant-numeric: tabular-nums; }
.balance-cell { display: flex; flex-direction: column; gap: 0.1rem; }
.balance-line { font-size: 0.9rem; }
.overdue { color: #b91c1c; font-weight: 500; font-size: 0.82rem; }
.chips { display: flex; flex-wrap: wrap; gap: 0.25rem; }
.row-actions { display: flex; gap: 0.15rem; align-items: center; }
.toggle { display: inline-flex; align-items: center; gap: 0.5rem; font-size: 0.85rem; color: var(--color-text-muted); }
</style>
