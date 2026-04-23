<script setup lang="ts">
import { ref, computed } from "vue";
import { useQuery } from "@tanstack/vue-query";
import { useRouter } from "vue-router";
import DataTable from "primevue/datatable";
import Column from "primevue/column";
import Dropdown from "primevue/dropdown";
import InputText from "primevue/inputtext";
import DatePicker from "primevue/calendar";
import Tag from "primevue/tag";
import Button from "primevue/button";
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

function actionSeverity(a: string) {
  switch (a) {
    case "CLEARED": return "success";
    case "HELD": return "warning";
    case "MATCHED": return "info";
    case "REVERSED": return "danger";
    case "OVERRIDDEN": return "secondary";
    default: return undefined;
  }
}

function formatDate(iso: string) {
  const d = new Date(iso);
  return d.toLocaleString(undefined, { dateStyle: "short", timeStyle: "short" });
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
    <header class="page-header">
      <div>
        <h1>Audit Log</h1>
        <p class="subtitle">
          Reconciliation engine decisions and manual actions on payments.
        </p>
      </div>
      <div class="page-actions">
        <Button icon="pi pi-refresh" text @click="refetch()" />
      </div>
    </header>

    <div class="filters card card-pad">
      <label class="f">
        <span>Action</span>
        <Dropdown
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
      <Button label="Clear" text size="small" @click="clearFilters" />
    </div>

    <DataTable
      :value="data ?? []"
      :loading="isLoading"
      data-key="log_id"
      striped-rows
      size="small"
      :row-hover="true"
      @row-click="(ev) => openPayment(ev.data as AuditRow)"
    >
      <template #empty>
        <div class="empty-state">
          <i class="pi pi-inbox" />
          <div>No audit entries match these filters.</div>
        </div>
      </template>
      <Column header="When" style="width: 150px">
        <template #body="{ data: r }">
          <span class="muted">{{ formatDate(r.created_at) }}</span>
        </template>
      </Column>
      <Column header="Action" style="width: 120px">
        <template #body="{ data: r }">
          <Tag :value="r.action" :severity="actionSeverity(r.action)" />
        </template>
      </Column>
      <Column header="Payer">
        <template #body="{ data: r }">
          <div>{{ r.payer_name ?? '—' }}</div>
          <div v-if="r.intake_source" class="muted small">via {{ r.intake_source }}</div>
        </template>
      </Column>
      <Column header="Amount" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }">
        <template #body="{ data: r }">
          <span class="num">{{ formatAmount(r.amount, r.currency) }} {{ r.currency ?? '' }}</span>
        </template>
      </Column>
      <Column header="Actor">
        <template #body="{ data: r }">
          <span v-if="r.actor_email">{{ r.actor_email }}</span>
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
.audit { display: flex; flex-direction: column; gap: 1rem; }
.filters {
  display: grid;
  grid-template-columns: 200px 1fr 180px 180px auto;
  gap: 0.75rem;
  align-items: end;
}
@media (max-width: 1000px) { .filters { grid-template-columns: 1fr 1fr; } }
.f { display: flex; flex-direction: column; gap: 0.3rem; font-size: 0.8rem; }
.f > span { font-weight: 600; color: var(--color-text-muted); text-transform: uppercase; letter-spacing: 0.04em; font-size: 0.72rem; }
.f :deep(.p-inputtext), .f :deep(.p-dropdown), .f :deep(.p-datepicker) { width: 100%; }

.muted { color: var(--color-text-muted); }
.small { font-size: 0.75rem; }
.num { font-variant-numeric: tabular-nums; }

.reasons { display: flex; flex-wrap: wrap; gap: 0.3rem; }
.reason-chip {
  font-size: 0.72rem; padding: 0.15rem 0.45rem;
  background: var(--color-surface-alt);
  border: 1px solid var(--color-border);
  color: var(--color-text-muted);
  border-radius: 4px;
}
</style>
