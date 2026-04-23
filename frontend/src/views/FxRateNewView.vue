<script setup lang="ts">
import { ref, computed, onMounted } from "vue";
import { useRouter } from "vue-router";
import { useMutation, useQueryClient } from "@tanstack/vue-query";
import Button from "primevue/button";
import Dropdown from "primevue/dropdown";
import InputText from "primevue/inputtext";
import InputNumber from "primevue/inputnumber";
import DatePicker from "primevue/calendar";
import Message from "primevue/message";
import { api } from "@/api/client";

const router = useRouter();
const queryClient = useQueryClient();

const currencyOptions = ["IDR", "SGD", "USD", "EUR"];
const baseCurrency = ref("IDR");

onMounted(async () => {
  try {
    const { data } = await api.get<{ base_currency: string }>("/accounting/base-currency");
    baseCurrency.value = data.base_currency;
    if (!form.value.to_currency) form.value.to_currency = baseCurrency.value;
  } catch {
    /* leave default */
  }
});

const form = ref({
  from_currency: "USD",
  to_currency: "IDR",
  rate: null as number | null,
  as_of_date: new Date(),
  source: "MANUAL",
});

const saveError = ref<string | null>(null);

const createRate = useMutation({
  mutationFn: async () => {
    const payload = {
      from_currency: form.value.from_currency,
      to_currency: form.value.to_currency,
      rate: form.value.rate,
      as_of_date: form.value.as_of_date.toISOString().slice(0, 10),
      source: form.value.source || "MANUAL",
    };
    return api.post("/accounting/fx-rates", payload);
  },
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ["fx-rates"] });
    router.push({ name: "fx-rates" });
  },
  onError: (err: any) => {
    saveError.value = err?.response?.data?.detail ?? "Save failed";
  },
});

const canSubmit = computed(
  () =>
    !!form.value.from_currency &&
    !!form.value.to_currency &&
    form.value.from_currency !== form.value.to_currency &&
    !!form.value.rate &&
    (form.value.rate ?? 0) > 0 &&
    !!form.value.as_of_date,
);

function submit() {
  saveError.value = null;
  createRate.mutate();
}

function cancel() {
  router.push({ name: "fx-rates" });
}
</script>

<template>
  <section class="page">
    <div class="back">
      <Button label="FX Rates" icon="pi pi-arrow-left" text size="small" @click="cancel" />
    </div>

    <div class="header">
      <h1>New FX rate</h1>
      <div class="actions">
        <Button label="Cancel" text @click="cancel" />
        <Button
          label="Create"
          :disabled="!canSubmit"
          :loading="createRate.isPending.value"
          @click="submit"
        />
      </div>
    </div>

    <Message v-if="saveError" severity="error" :closable="true" @close="saveError = null">
      {{ saveError }}
    </Message>

    <div class="card">
      <div class="two-col">
        <label class="field">
          <span>From</span>
          <Dropdown v-model="form.from_currency" :options="currencyOptions" />
        </label>
        <label class="field">
          <span>To</span>
          <Dropdown v-model="form.to_currency" :options="currencyOptions" />
        </label>
      </div>
      <label class="field">
        <span>Rate</span>
        <InputNumber
          v-model="form.rate"
          :min-fraction-digits="2"
          :max-fraction-digits="8"
          :use-grouping="true"
          :min="0"
          placeholder="e.g. 16000"
        />
      </label>
      <div class="two-col">
        <label class="field">
          <span>As of</span>
          <DatePicker v-model="form.as_of_date" date-format="yy-mm-dd" show-icon />
        </label>
        <label class="field">
          <span>Source</span>
          <InputText v-model="form.source" maxlength="50" placeholder="MANUAL" />
        </label>
      </div>
      <Message
        v-if="form.from_currency === form.to_currency"
        severity="warn"
        :closable="false"
      >
        From and to currencies must differ.
      </Message>
    </div>
  </section>
</template>

<style scoped>
.page { display: flex; flex-direction: column; gap: 1rem; padding-bottom: 2rem; }
.back { margin-bottom: -0.5rem; }
.header { display: flex; justify-content: space-between; align-items: center; gap: 1rem; }
.header h1 { margin: 0; font-size: 1.5rem; text-transform: uppercase; letter-spacing: 0.02em; }
.actions { display: flex; gap: 0.5rem; }
.card {
  background: var(--color-surface);
  border: 1px solid var(--color-border, #e2e8f0);
  border-radius: 12px;
  padding: 1.25rem;
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.04);
  display: flex;
  flex-direction: column;
  gap: 0.85rem;
  max-width: 560px;
}
.field { display: flex; flex-direction: column; gap: 0.3rem; font-size: 0.875rem; color: var(--color-text); }
.field > span { font-weight: 600; font-size: 0.85rem; }
.field :deep(.p-inputtext),
.field :deep(.p-inputnumber),
.field :deep(.p-inputnumber-input),
.field :deep(.p-dropdown),
.field :deep(.p-calendar) { width: 100%; }
.two-col { display: grid; grid-template-columns: 1fr 1fr; gap: 1rem; min-width: 0; }
.two-col > .field { min-width: 0; }
</style>
