<script setup lang="ts">
import { ref, onMounted } from "vue";
import { useRouter } from "vue-router";
import { useQuery, useMutation, useQueryClient } from "@tanstack/vue-query";
import { useConfirm } from "primevue/useconfirm";
import DataTable from "primevue/datatable";
import Column from "primevue/column";
import Button from "primevue/button";
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

function confirmDelete(row: FxRate) {
  confirm.require({
    message: `Delete rate ${row.from_currency}→${row.to_currency} @ ${row.as_of_date}?`,
    header: "Delete FX rate",
    icon: "pi pi-exclamation-triangle",
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
    <header class="page-header">
      <div>
        <h1>FX Rates</h1>
        <p class="subtitle">
          Manual exchange rates used to translate foreign-currency postings into the base currency
          (<strong>{{ baseCurrency }}</strong>). The most recent rate on or before a posting's date wins.
        </p>
      </div>
      <div class="page-actions">
        <Button label="New rate" icon="pi pi-plus" @click="openCreate" />
      </div>
    </header>

    <DataTable
      :value="data ?? []"
      :loading="isLoading"
      data-key="rate_id"
      striped-rows
      size="small"
    >
      <template #empty>
        <div class="empty-state">
          <i class="pi pi-inbox" />
          <div>No FX rates recorded yet.</div>
        </div>
      </template>
      <Column field="as_of_date" header="As of" :style="{ width: '130px' }" />
      <Column header="Pair" :style="{ width: '130px' }">
        <template #body="{ data: r }">
          <code>{{ r.from_currency }} → {{ r.to_currency }}</code>
        </template>
      </Column>
      <Column header="Rate" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }">
        <template #body="{ data: r }">
          <span class="num">{{ formatRate(r.rate) }}</span>
        </template>
      </Column>
      <Column field="source" header="Source" :style="{ width: '140px' }" />
      <Column field="created_at" header="Created" :style="{ width: '180px' }">
        <template #body="{ data: r }">
          <span class="muted">{{ new Date(r.created_at).toLocaleString() }}</span>
        </template>
      </Column>
      <Column header="" :style="{ width: '70px' }">
        <template #body="{ data: r }">
          <Button
            icon="pi pi-trash"
            text
            rounded
            severity="danger"
            title="Delete"
            :loading="deleteRate.isPending.value"
            @click="confirmDelete(r)"
          />
        </template>
      </Column>
    </DataTable>
  </section>
</template>

<style scoped>
.num { font-variant-numeric: tabular-nums; }
.muted { color: var(--color-text-muted); font-size: 0.85rem; }
code { background: var(--color-surface-alt); padding: 0.1rem 0.4rem; border-radius: 4px; font-size: 0.85em; font-weight: 600; }
</style>
