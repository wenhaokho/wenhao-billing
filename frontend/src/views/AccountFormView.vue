<script setup lang="ts">
import AppIcon from "@/components/AppIcon.vue";
import PageHeader from "@/components/PageHeader.vue";
import { ref, computed, watch } from "vue";
import { useRouter } from "vue-router";
import { useQuery, useMutation, useQueryClient } from "@tanstack/vue-query";
import Button from "primevue/button";
import InputText from "primevue/inputtext";
import Select from "primevue/select";
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
      <Button label="Chart of Accounts" text size="small" @click="cancel">
        <template #icon><AppIcon name="arrow-left" /></template>
      </Button>
    </div>

    <PageHeader eyebrow="04 / Accounting" :title="isEdit ? 'Edit account' : 'New account'">
      <Button label="Cancel" severity="secondary" outlined @click="cancel" />
      <Button
        :label="isEdit ? 'Save changes' : 'Create account'"
        :disabled="!form.code || !form.name || !form.type"
        :loading="save.isPending.value"
        @click="submit"
      />
    </PageHeader>

    <Message v-if="saveError" severity="error" role="alert" aria-live="assertive" :closable="true" @close="saveError = null">
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
          <Select
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
.page { display: flex; flex-direction: column; gap: 16px; padding-bottom: 2rem; }
.back { margin-bottom: -8px; }
.card {
  padding: 28px;
  display: flex;
  flex-direction: column;
  gap: 18px;
  max-width: 560px;
}
.field :deep(.p-inputtext), .field :deep(.p-select) { width: 100%; }
.two-col { display: grid; grid-template-columns: 1fr 1fr; gap: 1rem; min-width: 0; }
.two-col > .field { min-width: 0; }
</style>
