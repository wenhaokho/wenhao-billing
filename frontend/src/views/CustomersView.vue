<script setup lang="ts">
import AppIcon from "@/components/AppIcon.vue";
import { ref, computed } from "vue";
import { useRouter } from "vue-router";
import { useQuery, useMutation, useQueryClient } from "@tanstack/vue-query";
import { useConfirm } from "primevue/useconfirm";
import DataTable from "primevue/datatable";
import Column from "primevue/column";
import Button from "primevue/button";
import Chip from "primevue/chip";
import StatusTag from "@/components/StatusTag.vue";
import PageHeader from "@/components/PageHeader.vue";
import EmptyState from "@/components/EmptyState.vue";
import ToggleSwitch from "primevue/toggleswitch";
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
  contact_first_name: string | null;
  contact_last_name: string | null;
  contact_email: string | null;
  contact_phone: string | null;
  billing_address: string | null;
}

function contactName(row: Customer): string | null {
  const joined = [row.contact_first_name, row.contact_last_name]
    .filter(Boolean)
    .join(" ")
    .trim();
  return joined || null;
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
    icon: "triangle-alert",
    acceptLabel: "Deactivate",
    rejectLabel: "Cancel",
    acceptClass: "p-button-danger",
    accept: () => deactivateCustomer.mutate(row.customer_id),
  });
}
</script>

<template>
  <section>
    <PageHeader eyebrow="02 / Sales" title="Customers">
      <template #lede>
        Each customer has optional <strong>matching aliases</strong> — alternate names the AI
        matcher will recognize on incoming payments.
      </template>
      <label class="toggle">
        <ToggleSwitch v-model="showInactive" />
        <span>Show inactive</span>
      </label>
      <Button label="New customer" @click="openCreate">
        <template #icon><AppIcon name="plus" /></template>
      </Button>
    </PageHeader>

    <DataTable
      :value="data ?? []"
      :loading="isLoading"
      data-key="customer_id"
      striped-rows
    >
      <template #empty>
        <EmptyState icon="users" title="No customers yet">
          <template #message>Click <strong>New customer</strong> to create your first.</template>
        </EmptyState>
      </template>
      <Column header="Name">
        <template #body="{ data: row }">
          <span class="name">{{ row.name }}</span>
        </template>
      </Column>
      <Column header="Contact">
        <template #body="{ data: row }">
          <div v-if="contactName(row) || row.contact_email">
            <div v-if="contactName(row)">{{ contactName(row) }}</div>
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
      <Column header="Balance / Overdue" body-class="cell-nowrap" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }">
        <template #body="{ data: row }">
          <div v-if="balancesByCustomer[row.customer_id]?.length" class="balance-cell">
            <div
              v-for="b in balancesByCustomer[row.customer_id]"
              :key="b.currency"
              class="balance-line"
            >
              <div class="num">{{ formatAmount(b.balance, b.currency) }} <span class="ccy">{{ b.currency }}</span></div>
              <div v-if="Number(b.overdue) > 0" class="overdue num">
                {{ formatAmount(b.overdue, b.currency) }} overdue
              </div>
            </div>
          </div>
          <span v-else class="muted">—</span>
        </template>
      </Column>
      <Column header="Status">
        <template #body="{ data: row }">
          <StatusTag :status="row.active ? 'ACTIVE' : 'INACTIVE'" />
        </template>
      </Column>
      <Column header="" :style="{ width: '160px' }" :body-style="{ textAlign: 'right' }">
        <template #body="{ data: row }">
          <div class="row-actions">
            <Button text rounded title="View" @click="openDetail(row)">
              <template #icon><AppIcon name="eye" /></template>
            </Button>
            <Button text rounded title="Edit" @click="openEdit(row)">
              <template #icon><AppIcon name="pencil" /></template>
            </Button>
            <Button
              v-if="row.active"
              text
              rounded
              severity="danger"
              title="Deactivate"
              :loading="deactivateCustomer.isPending.value"
              @click="confirmDeactivate(row)">
              <template #icon><AppIcon name="ban" /></template>
            </Button>
            <Button
              v-else
              text
              rounded
              severity="success"
              title="Reactivate"
              :loading="reactivateCustomer.isPending.value"
              @click="reactivateCustomer.mutate(row.customer_id)">
              <template #icon><AppIcon name="undo-2" /></template>
            </Button>
          </div>
        </template>
      </Column>
    </DataTable>

  </section>
</template>

<style scoped>
.muted { color: var(--wb-muted-soft); }
.small { font-size: 13px; color: var(--wb-muted); }
.name { font-weight: 500; color: var(--wb-ink); }
.num { font-variant-numeric: tabular-nums; }
.ccy { font-size: 12px; color: var(--wb-muted-soft); }
.balance-cell { display: flex; flex-direction: column; gap: 6px; }
.overdue { color: var(--wb-red); font-size: 13px; }
.chips { display: flex; flex-wrap: wrap; gap: 6px; }
.chips :deep(.p-chip) {
  padding: 5px 10px;
  border: 1px solid var(--wb-line);
  border-radius: 2px;
  background: transparent;
  color: var(--wb-muted);
  font-size: 13px;
}
.row-actions { display: flex; gap: 2px; align-items: center; justify-content: flex-end; }
.toggle { display: inline-flex; align-items: center; gap: 10px; font-size: 14px; color: var(--wb-muted); cursor: pointer; }
</style>
