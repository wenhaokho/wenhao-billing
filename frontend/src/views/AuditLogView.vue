<script setup lang="ts">
import AppIcon from "@/components/AppIcon.vue";
import { ref, computed } from "vue";
import { useQuery } from "@tanstack/vue-query";
import { useRouter } from "vue-router";
import DataTable from "primevue/datatable";
import Column from "primevue/column";
import Select from "primevue/select";
import InputText from "primevue/inputtext";
import DatePicker from "primevue/datepicker";
import Button from "primevue/button";
import PageHeader from "@/components/PageHeader.vue";
import EmptyState from "@/components/EmptyState.vue";
import StatusTag from "@/components/StatusTag.vue";
import { api } from "@/api/client";
import { formatAmount } from "@/utils/money";

interface AuditRow {
  log_id: string;
  payment_id: string;
  action: string;
  reasons: string[];
  actor_user_id: string | null;
  actor_email: string | null;
  created_at: string;
  payer_name: string | null;
  amount: string | null;
  currency: string | null;
  invoice_id: string | null;
  intake_source: string | null;
}

const router = useRouter();

const actionOptions = [
  { label: "All actions", value: null },
  { label: "CLEARED", value: "CLEARED" },
  { label: "MATCHED", value: "MATCHED" },
  { label: "HELD", value: "HELD" },
  { label: "OVERRIDDEN", value: "OVERRIDDEN" },
  { label: "REVERSED", value: "REVERSED" },
];

const action = ref<string | null>(null);
const payer = ref<string>("");
const fromDate = ref<Date | null>(null);
const toDate = ref<Date | null>(null);

const params = computed(() => {
  const p: Record<string, string> = {};
  if (action.value) p.action = action.value;
  if (payer.value.trim()) p.payer = payer.value.trim();
  if (fromDate.value) p.from_date = fromDate.value.toISOString().slice(0, 10);
  if (toDate.value) p.to_date = toDate.value.toISOString().slice(0, 10);
  return p;
});

const { data, isLoading, refetch } = useQuery<AuditRow[]>({
  queryKey: ["audit-log", params],
  queryFn: async () =>
    (await api.get<AuditRow[]>("/payments/audit-log", { params: params.value })).data,
});

function formatDay(iso: string) {
  return new Date(iso).toLocaleDateString(undefined, { dateStyle: "short" });
}

function formatTime(iso: string) {
  return new Date(iso).toLocaleTimeString(undefined, { timeStyle: "short" });
}

function clearFilters() {
  action.value = null;
  payer.value = "";
  fromDate.value = null;
  toDate.value = null;
}

function openPayment(row: AuditRow) {
  if (row.invoice_id) {
    router.push(`/invoices/${row.invoice_id}/edit`);
  } else {
    router.push(`/review/${row.payment_id}`);
  }
}
</script>

<template>
  <section class="audit">
    <PageHeader eyebrow="06 / Admin" title="Audit Log">
      <template #lede>Reconciliation engine decisions and manual actions on payments.</template>
      <Button label="Refresh" severity="secondary" outlined @click="refetch()">
        <template #icon><AppIcon name="refresh-cw" /></template>
      </Button>
    </PageHeader>

    <div class="filters card">
      <label class="f">
        <span>Action</span>
        <Select
          v-model="action"
          :options="actionOptions"
          option-label="label"
          option-value="value"
          show-clear
        />
      </label>
      <label class="f">
        <span>Payer</span>
        <InputText v-model="payer" placeholder="Search payer name…" />
      </label>
      <label class="f">
        <span>From</span>
        <DatePicker v-model="fromDate" date-format="yy-mm-dd" show-icon show-button-bar />
      </label>
      <label class="f">
        <span>To</span>
        <DatePicker v-model="toDate" date-format="yy-mm-dd" show-icon show-button-bar />
      </label>
      <Button label="Clear" text class="clear" @click="clearFilters" />
    </div>

    <DataTable
      :value="data ?? []"
      :loading="isLoading"
      data-key="log_id"
      striped-rows
      :row-hover="true"
      @row-click="(ev) => openPayment(ev.data as AuditRow)"
    >
      <template #empty>
        <EmptyState title="No audit entries" message="No audit entries match these filters." />
      </template>
      <Column header="When" style="width: 150px" body-class="cell-nowrap">
        <template #body="{ data: r }">
          <div>{{ formatDay(r.created_at) }}</div>
          <div class="muted small">{{ formatTime(r.created_at) }}</div>
        </template>
      </Column>
      <Column header="Action" style="width: 140px">
        <template #body="{ data: r }">
          <StatusTag :status="r.action" />
        </template>
      </Column>
      <Column header="Payer">
        <template #body="{ data: r }">
          <div class="payer">{{ r.payer_name ?? '—' }}</div>
          <div v-if="r.intake_source" class="muted small">via {{ r.intake_source }}</div>
        </template>
      </Column>
      <Column header="Amount" body-class="cell-nowrap" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }">
        <template #body="{ data: r }">
          <span class="num">{{ formatAmount(r.amount, r.currency) }} {{ r.currency ?? '' }}</span>
        </template>
      </Column>
      <Column header="Actor">
        <template #body="{ data: r }">
          <span v-if="r.actor_email" class="actor">{{ r.actor_email }}</span>
          <span v-else class="muted">system</span>
        </template>
      </Column>
      <Column header="Reasons">
        <template #body="{ data: r }">
          <div class="reasons">
            <span v-for="reason in r.reasons" :key="reason" class="reason-chip">{{ reason }}</span>
          </div>
        </template>
      </Column>
    </DataTable>
  </section>
</template>

<style scoped>
.audit { display: flex; flex-direction: column; gap: 24px; }
.audit > :deep(.page-head) { margin-bottom: 8px; }
.filters {
  display: grid;
  grid-template-columns: 200px minmax(0, 1fr) 170px 170px auto;
  gap: 16px;
  align-items: end;
  padding: 20px 24px;
}
@media (max-width: 1000px) { .filters { grid-template-columns: 1fr 1fr; } }
@media (max-width: 560px) { .filters { grid-template-columns: minmax(0, 1fr); padding: 16px; } }
.f { display: flex; flex-direction: column; gap: 8px; min-width: 0; }
.f > span {
  font: 500 11px/1 var(--wb-font-sans);
  letter-spacing: 1.2px;
  text-transform: uppercase;
  color: var(--wb-muted-soft);
}
.f :deep(.p-inputtext), .f :deep(.p-select), .f :deep(.p-datepicker) { width: 100%; }
.clear { justify-self: start; }

.muted { color: var(--wb-muted); }
.small { font-size: 13px; }
.num { font-variant-numeric: tabular-nums; }
.payer { font-weight: 500; }
.actor { color: var(--wb-muted); }

.reasons { display: flex; flex-wrap: wrap; gap: 6px; }
.reason-chip {
  font: 500 12px var(--wb-font-mono);
  padding: 4px 8px;
  border: 1px solid var(--wb-line);
  color: var(--wb-muted);
  white-space: nowrap;
}
</style>
