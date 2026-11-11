<script setup lang="ts">
import AppIcon from "@/components/AppIcon.vue";
import { computed } from "vue";
import { useRouter } from "vue-router";
import { useQuery, useMutation, useQueryClient } from "@tanstack/vue-query";
import { useConfirm } from "primevue/useconfirm";
import DataTable from "primevue/datatable";
import Column from "primevue/column";
import Button from "primevue/button";
import Tag from "primevue/tag";
import StatusTag from "@/components/StatusTag.vue";
import { api } from "@/api/client";

interface Account {
  account_id: number;
  code: string;
  name: string;
  type: string;
  active: boolean;
}

const router = useRouter();
const queryClient = useQueryClient();
const confirm = useConfirm();

const { data, isLoading } = useQuery<Account[]>({
  queryKey: ["chart-of-accounts"],
  queryFn: async () => (await api.get<Account[]>("/accounting/chart-of-accounts")).data,
});

const grouped = computed(() => {
  const g: Record<string, Account[]> = {};
  for (const a of data.value ?? []) {
    (g[a.type] ??= []).push(a);
  }
  return g;
});

const typeOrder = ["ASSET", "LIABILITY", "EQUITY", "INCOME", "COGS", "EXPENSE"];
const orderedTypes = computed(() =>
  typeOrder.filter((t) => grouped.value[t]?.length),
);

const deactivateAccount = useMutation({
  mutationFn: async (id: number) =>
    api.delete<Account>(`/accounting/chart-of-accounts/${id}`),
  onSuccess: () => queryClient.invalidateQueries({ queryKey: ["chart-of-accounts"] }),
});

const reactivateAccount = useMutation({
  mutationFn: async (id: number) =>
    api.patch<Account>(`/accounting/chart-of-accounts/${id}`, { active: true }),
  onSuccess: () => queryClient.invalidateQueries({ queryKey: ["chart-of-accounts"] }),
});

function openCreate() {
  router.push({ name: "account-new" });
}

function openEdit(row: Account) {
  router.push({ name: "account-edit", params: { accountId: String(row.account_id) } });
}

function confirmDeactivate(row: Account) {
  confirm.require({
    message: `Deactivate "${row.code} · ${row.name}"? Existing journal lines remain; new postings will be blocked.`,
    header: "Deactivate account",
    icon: "triangle-alert",
    acceptLabel: "Deactivate",
    rejectLabel: "Cancel",
    acceptClass: "p-button-danger",
    accept: () => deactivateAccount.mutate(row.account_id),
  });
}

function typeSeverity(t: string) {
  switch (t) {
    case "ASSET": return "info";
    case "LIABILITY": return "warn";
    case "INCOME": return "success";
    case "COGS": return "danger";
    case "EXPENSE": return "danger";
    default: return "secondary";
  }
}
</script>

<template>
  <section>
    <header class="page-header">
      <div>
        <h1>Chart of Accounts</h1>
        <p class="subtitle">
          The foundational ledger structure — every journal line posts against one of these accounts.
        </p>
      </div>
      <div class="page-actions">
        <Button label="New account" @click="openCreate">
          <template #icon><AppIcon name="plus" /></template>
        </Button>
      </div>
    </header>

    <div v-if="isLoading" class="card card-pad">Loading…</div>

    <div v-else class="groups">
      <div v-for="type in orderedTypes" :key="type" class="group">
        <div class="group-head">
          <Tag :value="type" :severity="typeSeverity(type)" />
          <span class="count">{{ grouped[type].length }} accounts</span>
        </div>
        <DataTable :value="grouped[type]" data-key="account_id" size="small" striped-rows>
          <Column field="code" header="Code" :style="{ width: '100px' }">
            <template #body="{ data: row }">
              <code>{{ row.code }}</code>
            </template>
          </Column>
          <Column field="name" header="Name" />
          <Column header="Status" :style="{ width: '110px' }">
            <template #body="{ data: row }">
              <StatusTag :status="row.active ? 'ACTIVE' : 'INACTIVE'" />
            </template>
          </Column>
          <Column header="" :style="{ width: '160px' }">
            <template #body="{ data: row }">
              <div class="row-actions">
                <Button text rounded title="Edit" @click="openEdit(row)">
                  <template #icon><AppIcon name="pencil" /></template>
                </Button>
                <Button
                  v-if="row.active"
                  text
                  rounded
                  severity="danger"
                  title="Deactivate"
                  :loading="deactivateAccount.isPending.value"
                  @click="confirmDeactivate(row)">
                  <template #icon><AppIcon name="ban" /></template>
                </Button>
                <Button
                  v-else
                  text
                  rounded
                  severity="success"
                  title="Reactivate"
                  :loading="reactivateAccount.isPending.value"
                  @click="reactivateAccount.mutate(row.account_id)">
                  <template #icon><AppIcon name="undo-2" /></template>
                </Button>
              </div>
            </template>
          </Column>
        </DataTable>
      </div>
    </div>
  </section>
</template>

<style scoped>
.groups { display: flex; flex-direction: column; gap: 1.25rem; }
.group-head { display: flex; align-items: center; gap: 0.75rem; margin-bottom: 0.5rem; }
.count { color: var(--color-text-muted); font-size: 0.85rem; }
code { background: var(--color-surface-alt); padding: 0.1rem 0.4rem; border-radius: 4px; font-size: 0.85em; font-weight: 600; }
.row-actions { display: flex; gap: 0.15rem; }
</style>
