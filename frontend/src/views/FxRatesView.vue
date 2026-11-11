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

function openCreate() {
  router.push({ name: "fx-rate-new" });
}
</script>

<template>
  <section>
    <PageHeader eyebrow="04 / Accounting" title="FX Rates">
      <template #lede>
        Manual exchange rates used to translate foreign-currency postings into the base currency
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
