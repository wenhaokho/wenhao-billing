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
import PageHeader from "@/components/PageHeader.vue";
import EmptyState from "@/components/EmptyState.vue";
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

// First and last code in a group, as listed ("codes 1000–1290").
function codeRange(type: string): string {
  const codes = (grouped.value[type] ?? []).map((a) => a.code).sort();
  if (!codes.length) return "";
  return codes.length === 1 ? codes[0] : `${codes[0]}–${codes[codes.length - 1]}`;
}

function scrollToGroup(type: string) {
  document.getElementById(`coa-${type}`)?.scrollIntoView({ behavior: "smooth", block: "start" });
}

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
    <PageHeader eyebrow="04 / Accounting" title="Chart of Accounts">
      <template #lede>
        The foundational ledger structure — every journal line posts against one of these accounts.
      </template>
      <Button label="New account" @click="openCreate">
        <template #icon><AppIcon name="plus" /></template>
      </Button>
    </PageHeader>

    <div v-if="isLoading" class="card card-pad">Loading…</div>

    <div v-else-if="!orderedTypes.length" class="card">
      <EmptyState icon="book-open" title="No accounts yet" message="Add an account to start posting journal lines." />
    </div>

    <div v-else class="coa">
      <nav class="coa-index" aria-label="Account types">
        <button
          v-for="type in orderedTypes"
          :key="type"
          type="button"
          class="coa-index-item"
          :class="`tone-${typeSeverity(type)}`"
          @click="scrollToGroup(type)">
          <span class="coa-index-type">{{ type }}</span>
          <span class="coa-index-count">{{ grouped[type].length }}</span>
        </button>
      </nav>
      <div class="groups">
      <section v-for="type in orderedTypes" :id="`coa-${type}`" :key="type" class="group card">
        <div class="group-head">
          <Tag :value="type" :severity="typeSeverity(type)" />
          <span class="count">{{ grouped[type].length }} accounts · codes {{ codeRange(type) }}</span>
        </div>
        <DataTable :value="grouped[type]" data-key="account_id" striped-rows>
          <Column field="code" header="Code" :style="{ width: '100px' }" body-class="cell-nowrap">
            <template #body="{ data: row }">
              <span class="code">{{ row.code }}</span>
            </template>
          </Column>
          <Column field="name" header="Name" />
          <Column header="Status" :style="{ width: '110px' }">
            <template #body="{ data: row }">
              <StatusTag :status="row.active ? 'ACTIVE' : 'INACTIVE'" />
            </template>
          </Column>
          <Column header="" :style="{ width: '120px' }" :body-style="{ textAlign: 'right' }">
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
      </section>
      </div>
    </div>
  </section>
</template>

<style scoped>
.coa {
  display: grid;
  grid-template-columns: 200px minmax(0, 1fr);
  gap: 40px;
  align-items: start;
}
.coa-index { position: sticky; top: 88px; display: flex; flex-direction: column; gap: 2px; }
.coa-index-item {
  display: flex;
  justify-content: space-between;
  padding: 10px 12px;
  border: 0;
  border-left: 2px solid var(--wb-line);
  background: none;
  font: 500 14px var(--wb-font-sans);
  color: var(--wb-ink);
  cursor: pointer;
  text-align: left;
}
.coa-index-item:hover { background: var(--wb-surface-2); }
.coa-index-item:focus-visible { outline: none; box-shadow: var(--focus-ring); }
.coa-index-item.tone-info { border-left-color: var(--wb-blue-fg); }
.coa-index-item.tone-warn { border-left-color: var(--wb-amber-fg); }
.coa-index-item.tone-success { border-left-color: var(--wb-green-fg); }
.coa-index-item.tone-danger { border-left-color: var(--wb-red-fg); }
.coa-index-count { color: var(--wb-muted-soft); font-weight: 400; font-variant-numeric: tabular-nums; }
@media (max-width: 1000px) {
  .coa { grid-template-columns: minmax(0, 1fr); }
  .coa-index { display: none; }
}
.groups { display: flex; flex-direction: column; gap: 28px; }
.group { scroll-margin-top: 88px; }
.group-head {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
  padding: 18px 24px;
  border-bottom: 1px solid var(--wb-line);
}
.count { color: var(--wb-muted); font-size: 13px; }
.group :deep(.p-datatable-table-container) { border: 0; }
.code { font: 500 13px var(--wb-font-mono); color: var(--wb-muted); }
.row-actions { display: flex; gap: 2px; justify-content: flex-end; }
</style>
