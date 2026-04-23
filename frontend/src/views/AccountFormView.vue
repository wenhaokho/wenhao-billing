<script setup lang="ts">
import { ref, computed, watch } from "vue";
import { useRouter } from "vue-router";
import { useQuery, useMutation, useQueryClient } from "@tanstack/vue-query";
import Button from "primevue/button";
import InputText from "primevue/inputtext";
import Dropdown from "primevue/dropdown";
import Message from "primevue/message";
import { api } from "@/api/client";

interface Account {
  account_id: number;
  code: string;
  name: string;
  type: string;
  active: boolean;
}

const props = defineProps<{ accountId?: string }>();
const router = useRouter();
const queryClient = useQueryClient();

const accountTypes = [
  { label: "Asset", value: "ASSET" },
  { label: "Liability", value: "LIABILITY" },
  { label: "Equity", value: "EQUITY" },
  { label: "Income", value: "INCOME" },
  { label: "COGS", value: "COGS" },
  { label: "Expense", value: "EXPENSE" },
];

type FormShape = { code: string; name: string; type: string; active: boolean };
function emptyForm(): FormShape {
  return { code: "", name: "", type: "ASSET", active: true };
}

const form = ref<FormShape>(emptyForm());
const isEdit = computed(() => !!props.accountId);
const saveError = ref<string | null>(null);

const { data: existing, isLoading: loadingExisting } = useQuery<Account>({
  queryKey: ["account", () => props.accountId],
  queryFn: async () =>
    (await api.get<Account>(`/accounting/chart-of-accounts/${props.accountId}`)).data,
  enabled: () => !!props.accountId,
});

watch(
  existing,
  (v) => {
    if (!v) return;
    form.value = { code: v.code, name: v.name, type: v.type, active: v.active };
  },
  { immediate: true },
);

const save = useMutation({
  mutationFn: async () => {
    if (props.accountId) {
      return api.patch<Account>(
        `/accounting/chart-of-accounts/${props.accountId}`,
        form.value,
      );
    }
    return api.post<Account>("/accounting/chart-of-accounts", form.value);
  },
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ["chart-of-accounts"] });
    queryClient.invalidateQueries({ queryKey: ["account"] });
    router.push({ name: "chart-of-accounts" });
  },
  onError: (err: any) => {
    saveError.value = err?.response?.data?.detail ?? "Save failed";
  },
});

function submit() {
  saveError.value = null;
  save.mutate();
}

function cancel() {
  router.push({ name: "chart-of-accounts" });
}
</script>

<template>
  <section class="page">
    <div class="back">
      <Button label="Chart of Accounts" icon="pi pi-arrow-left" text size="small" @click="cancel" />
    </div>

    <div class="header">
      <h1>{{ isEdit ? "Edit account" : "New account" }}</h1>
      <div class="actions">
        <Button label="Cancel" text @click="cancel" />
        <Button
          :label="isEdit ? 'Save changes' : 'Create account'"
          :disabled="!form.code || !form.name || !form.type"
          :loading="save.isPending.value"
          @click="submit"
        />
      </div>
    </div>

    <Message v-if="saveError" severity="error" :closable="true" @close="saveError = null">
      {{ saveError }}
    </Message>

    <div v-if="loadingExisting && isEdit" class="card">Loading…</div>
    <div v-else class="card">
      <div class="two-col">
        <label class="field">
          <span>Code</span>
          <InputText v-model="form.code" maxlength="10" autofocus />
        </label>
        <label class="field">
          <span>Type</span>
          <Dropdown
            v-model="form.type"
            :options="accountTypes"
            option-label="label"
            option-value="value"
          />
        </label>
      </div>
      <label class="field">
        <span>Name</span>
        <InputText v-model="form.name" maxlength="100" />
      </label>
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
.field :deep(.p-inputtext), .field :deep(.p-dropdown) { width: 100%; }
.two-col { display: grid; grid-template-columns: 1fr 1fr; gap: 1rem; min-width: 0; }
.two-col > .field { min-width: 0; }
</style>
