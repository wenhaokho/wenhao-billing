<script setup lang="ts">
import AppIcon from "@/components/AppIcon.vue";
import { ref, onMounted } from "vue";
import { useRouter } from "vue-router";
import { useQuery, useMutation, useQueryClient } from "@tanstack/vue-query";
import { useConfirm } from "primevue/useconfirm";
import DataTable from "primevue/datatable";
import Column from "primevue/column";
import Button from "primevue/button";
import Message from "primevue/message";
import PageHeader from "@/components/PageHeader.vue";
import EmptyState from "@/components/EmptyState.vue";
import { api } from "@/api/client";
import { timeAgo } from "@/components/dashboard/format";

interface FxRate {
  rate_id: number;
  from_currency: string;
  to_currency: string;
  rate: string;
  as_of_date: string;
  source: string;
  created_at: string;
}

interface FxSyncResult {
  created: number;
  updated: number;
  fetched: number;
  pairs: string[];
  source: string;
}

/** One currency's newest rate to the base currency (GET fx-rates/summary). */
interface FxCurrencySummary {
  currency: string;
  latest_rate: string;
  latest_as_of: string;
  previous_rate: string | null;
  previous_as_of: string | null;
  change_pct: number | null;
}

interface FxLastSync {
  ran_at: string;
  trigger: "SCHEDULED" | "MANUAL";
  source: string;
}

interface FxSummary {
  base_currency: string;
  currencies: FxCurrencySummary[];
  last_sync: FxLastSync | null;
}

const router = useRouter();
const queryClient = useQueryClient();
const confirm = useConfirm();

const baseCurrency = ref("IDR");
onMounted(async () => {
  try {
    const { data } = await api.get<{ base_currency: string }>("/accounting/base-currency");
    baseCurrency.value = data.base_currency;
  } catch {
    // leave default
  }
});

const { data, isLoading } = useQuery<FxRate[]>({
  queryKey: ["fx-rates"],
  queryFn: async () => (await api.get<FxRate[]>("/accounting/fx-rates")).data,
});

// Keyed under ["fx-rates"], so every invalidation of it (create, delete,
// sync) refreshes the cards too.
const summary = useQuery<FxSummary>({
  queryKey: ["fx-rates", "summary"],
  queryFn: async () => (await api.get<FxSummary>("/accounting/fx-rates/summary")).data,
});

const deleteRate = useMutation({
  mutationFn: async (id: number) => api.delete(`/accounting/fx-rates/${id}`),
  onSuccess: () => queryClient.invalidateQueries({ queryKey: ["fx-rates"] }),
});

const syncMessage = ref<{ severity: "success" | "error"; text: string } | null>(null);

const syncRates = useMutation({
  mutationFn: async () =>
    (await api.post<FxSyncResult>("/accounting/fx-rates/sync")).data,
  onSuccess: (r) => {
    queryClient.invalidateQueries({ queryKey: ["fx-rates"] });
    const parts = [`${r.created} added`, `${r.updated} updated`];
    if (r.fetched === 0) parts.push("no rates returned by provider");
    syncMessage.value = {
      severity: "success",
      text: `Synced from ${r.source}: ${parts.join(", ")}.`,
    };
  },
  onError: (err: any) => {
    syncMessage.value = {
      severity: "error",
      text: err?.response?.data?.detail ?? "Sync failed",
    };
  },
});

function confirmDelete(row: FxRate) {
  confirm.require({
    message: `Delete rate ${row.from_currency}→${row.to_currency} @ ${row.as_of_date}?`,
    header: "Delete FX rate",
    icon: "triangle-alert",
    acceptLabel: "Delete",
    rejectLabel: "Cancel",
    acceptClass: "p-button-danger",
    accept: () => deleteRate.mutate(row.rate_id),
  });
}

function formatRate(v: string) {
  const n = Number(v);
  if (!Number.isFinite(n)) return v;
  return n.toLocaleString(undefined, {
    minimumFractionDigits: 2,
    maximumFractionDigits: 8,
  });
}

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

/** "2026-01-08" -> "8 Jan 2026", read as a calendar day (no time-zone shift). */
function formatDay(iso: string): string {
  const [y, m, d] = iso.split("-").map(Number);
  if (!y || !m || !d) return iso;
  return `${d} ${MONTHS[m - 1]} ${y}`;
}

/** "▲ 5.00%" / "▼ 0.51%" / "0.00%", and its tone: a dearer foreign currency reads green. */
function changeOf(pct: number | null): { text: string; tone: "up" | "down" | "flat" } | null {
  if (pct === null) return null;
  const rounded = Math.round(pct * 100) / 100;
  if (rounded === 0) return { text: "0.00%", tone: "flat" };
  const up = rounded > 0;
  return { text: `${up ? "▲" : "▼"} ${Math.abs(rounded).toFixed(2)}%`, tone: up ? "up" : "down" };
}

function syncTrigger(s: FxLastSync): string {
  return s.trigger === "SCHEDULED" ? "weekly" : "manual";
}

function openCreate() {
  router.push({ name: "fx-rate-new" });
}
</script>

<template>
  <section>
    <PageHeader eyebrow="04 / Accounting" title="FX Rates">
      <template #lede>
        Exchange rates used to translate foreign-currency postings into the base currency
        (<strong>{{ baseCurrency }}</strong>). The most recent rate on or before a posting's date wins.
      </template>
        <Button
          label="Sync now"
          severity="secondary"
          outlined
          :loading="syncRates.isPending.value"
          title="Fetch the latest reference rates from the provider"
          @click="syncRates.mutate()">
          <template #icon><AppIcon name="refresh-cw" /></template>
        </Button>
        <Button label="New rate" @click="openCreate">
          <template #icon><AppIcon name="plus" /></template>
        </Button>
    </PageHeader>

    <Message
      v-if="syncMessage"
      class="sync-message"
      :severity="syncMessage.severity"
      role="status"
      aria-live="polite"
      :closable="true"
      @close="syncMessage = null"
    >
      {{ syncMessage.text }}
    </Message>

    <div v-if="summary.data.value" class="fx-summary" data-testid="fx-summary">
      <div v-for="c in summary.data.value.currencies" :key="c.currency" class="fx-cell" data-testid="fx-card">
        <div class="fx-label">{{ c.currency }} <span class="arrow">→</span> {{ summary.data.value.base_currency }} · latest</div>
        <div class="fx-value num">{{ formatRate(c.latest_rate) }}</div>
        <div class="fx-sub">as of {{ formatDay(c.latest_as_of) }}</div>
        <div v-if="c.previous_rate && c.previous_as_of" class="fx-sub">
          <span v-if="changeOf(c.change_pct)" :class="['fx-change', `fx-${changeOf(c.change_pct)!.tone}`]">
            {{ changeOf(c.change_pct)!.text }}
          </span>
          vs {{ formatRate(c.previous_rate) }} on {{ formatDay(c.previous_as_of) }}
        </div>
        <div v-else class="fx-sub muted-soft">No earlier rate</div>
      </div>
      <div class="fx-cell" data-testid="fx-last-sync">
        <div class="fx-label">Last sync</div>
        <template v-if="summary.data.value.last_sync">
          <div class="fx-value" :title="new Date(summary.data.value.last_sync.ran_at).toLocaleString()">
            {{ timeAgo(summary.data.value.last_sync.ran_at) }}
          </div>
          <div class="fx-sub">
            {{ syncTrigger(summary.data.value.last_sync) }} · source {{ summary.data.value.last_sync.source }}
          </div>
        </template>
        <template v-else>
          <div class="fx-value">Never synced</div>
          <div class="fx-sub">Weekly on Mondays, or Sync now</div>
        </template>
      </div>
    </div>

    <DataTable
      :value="data ?? []"
      :loading="isLoading"
      data-key="rate_id"
      striped-rows
    >
      <template #empty>
        <EmptyState title="No FX rates recorded yet" message="Sync from the provider or add a rate by hand." />
      </template>
      <Column field="as_of_date" header="As of" :style="{ width: '130px' }" body-class="cell-nowrap" />
      <Column header="Pair" :style="{ width: '150px' }" body-class="cell-nowrap">
        <template #body="{ data: r }">
          <span class="doc-no">{{ r.from_currency }} <span class="arrow">→</span> {{ r.to_currency }}</span>
        </template>
      </Column>
      <Column header="Rate" body-class="cell-nowrap" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }">
        <template #body="{ data: r }">
          <span class="num rate">{{ formatRate(r.rate) }}</span>
        </template>
      </Column>
      <Column header="Source" :style="{ width: '140px' }">
        <template #body="{ data: r }">
          <span class="source">{{ r.source }}</span>
        </template>
      </Column>
      <Column field="created_at" header="Created" :style="{ width: '200px' }" body-class="cell-nowrap">
        <template #body="{ data: r }">
          <span class="muted">{{ new Date(r.created_at).toLocaleString() }}</span>
        </template>
      </Column>
      <Column header="" :style="{ width: '70px' }" :body-style="{ textAlign: 'right' }">
        <template #body="{ data: r }">
          <Button
            text
            rounded
            severity="danger"
            title="Delete"
            :loading="deleteRate.isPending.value"
            @click="confirmDelete(r)">
            <template #icon><AppIcon name="trash-2" /></template>
          </Button>
        </template>
      </Column>
    </DataTable>
  </section>
</template>

<style scoped>
.sync-message { margin: 0 0 16px; }
.fx-summary {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
  background: var(--wb-surface);
  border: 1px solid var(--wb-line);
  margin: 0 0 24px;
}
.fx-cell {
  padding: 22px 26px;
  border-right: 1px solid var(--wb-line);
  border-bottom: 1px solid var(--wb-line);
  margin: 0 -1px -1px 0;
}
.fx-label {
  font: 500 11px/1.4 var(--wb-font-sans);
  letter-spacing: 1.3px;
  text-transform: uppercase;
  color: var(--wb-muted-soft);
}
.fx-value {
  font: 500 28px/1.1 var(--wb-font-display);
  letter-spacing: -1px;
  margin-top: 8px;
  color: var(--wb-ink);
  font-variant-numeric: tabular-nums;
}
.fx-sub { font-size: 13px; color: var(--wb-muted); margin-top: 4px; }
.muted-soft { color: var(--wb-muted-soft); }
.fx-change { font-weight: 500; }
.fx-up { color: var(--wb-green-fg); }
.fx-down { color: var(--wb-red); }
.fx-flat { color: var(--wb-muted); }
.num { font-variant-numeric: tabular-nums; }
.rate { font-weight: 500; }
.muted { color: var(--wb-muted); font-size: 13px; }
.arrow { color: var(--wb-muted-soft); }
.source {
  display: inline-block;
  padding: 4px 8px;
  font: 500 10px/1 var(--wb-font-sans);
  letter-spacing: 1px;
  text-transform: uppercase;
  border-radius: 2px;
  background: var(--wb-tint);
  color: var(--wb-muted);
  white-space: nowrap;
}
</style>
