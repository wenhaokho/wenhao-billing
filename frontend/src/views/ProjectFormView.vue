<script setup lang="ts">
import { ref, computed, watch } from "vue";
import { useRouter } from "vue-router";
import { useQuery, useMutation, useQueryClient } from "@tanstack/vue-query";
import Button from "primevue/button";
import InputText from "primevue/inputtext";
import InputNumber from "primevue/inputnumber";
import Textarea from "primevue/textarea";
import Dropdown from "primevue/dropdown";
import Calendar from "primevue/calendar";
import Message from "primevue/message";
import { api } from "@/api/client";

interface Project {
  project_id: string;
  customer_id: string;
  code: string;
  name: string;
  currency: string;
  contract_value: string | null;
  status: "ACTIVE" | "ON_HOLD" | "COMPLETED" | "CANCELLED";
  start_date: string | null;
  end_date: string | null;
  notes: string | null;
  active: boolean;
}

interface Customer {
  customer_id: string;
  name: string;
  default_currency?: string | null;
}

const props = defineProps<{ projectId?: string }>();
const router = useRouter();
const queryClient = useQueryClient();

const currencyOptions = ["IDR", "SGD", "USD"];
const statusOptions = [
  { label: "Active", value: "ACTIVE" },
  { label: "On hold", value: "ON_HOLD" },
  { label: "Completed", value: "COMPLETED" },
  { label: "Cancelled", value: "CANCELLED" },
];

type FormShape = {
  customer_id: string | null;
  code: string;
  name: string;
  currency: string;
  contract_value: number | null;
  status: Project["status"];
  start_date: Date | null;
  end_date: Date | null;
  notes: string;
  active: boolean;
};

function emptyForm(): FormShape {
  return {
    customer_id: null,
    code: "",
    name: "",
    currency: "IDR",
    contract_value: null,
    status: "ACTIVE",
    start_date: null,
    end_date: null,
    notes: "",
    active: true,
  };
}

const form = ref<FormShape>(emptyForm());
const isEdit = computed(() => !!props.projectId);
const saveError = ref<string | null>(null);

const { data: customers } = useQuery<Customer[]>({
  queryKey: ["customers"],
  queryFn: async () => (await api.get<Customer[]>("/customers")).data,
});

const { data: existing, isLoading: loadingExisting } = useQuery<Project>({
  queryKey: ["project", () => props.projectId],
  queryFn: async () => (await api.get<Project>(`/projects/${props.projectId}`)).data,
  enabled: () => !!props.projectId,
});

function toDate(v: string | null): Date | null {
  if (!v) return null;
  const d = new Date(v);
  return Number.isNaN(d.getTime()) ? null : d;
}

watch(
  existing,
  (v) => {
    if (!v) return;
    form.value = {
      customer_id: v.customer_id,
      code: v.code,
      name: v.name,
      currency: v.currency,
      contract_value: v.contract_value ? Number(v.contract_value) : null,
      status: v.status,
      start_date: toDate(v.start_date),
      end_date: toDate(v.end_date),
      notes: v.notes ?? "",
      active: v.active,
    };
  },
  { immediate: true },
);

// Auto-fill currency from customer default when creating
watch(
  () => form.value.customer_id,
  (cid) => {
    if (isEdit.value || !cid) return;
    const c = customers.value?.find((x) => x.customer_id === cid);
    if (c?.default_currency) form.value.currency = c.default_currency;
  },
);

function toISODate(d: Date | null): string | null {
  if (!d) return null;
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, "0");
  const day = String(d.getDate()).padStart(2, "0");
  return `${y}-${m}-${day}`;
}

function buildBody() {
  const body: Record<string, unknown> = {
    customer_id: form.value.customer_id,
    code: form.value.code.trim().toUpperCase(),
    name: form.value.name.trim(),
    currency: form.value.currency,
    status: form.value.status,
    contract_value: form.value.contract_value ?? null,
    start_date: toISODate(form.value.start_date),
    end_date: toISODate(form.value.end_date),
    notes: form.value.notes || null,
  };
  if (isEdit.value) body.active = form.value.active;
  return body;
}

const save = useMutation({
  mutationFn: async () => {
    if (props.projectId) {
      return api.patch<Project>(`/projects/${props.projectId}`, buildBody());
    }
    return api.post<Project>("/projects", buildBody());
  },
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ["projects"] });
    queryClient.invalidateQueries({ queryKey: ["project"] });
    router.push({ name: "projects" });
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
  router.push({ name: "projects" });
}

const canSave = computed(
  () => !!form.value.customer_id && !!form.value.code.trim() && !!form.value.name.trim(),
);
</script>

<template>
  <section class="page">
    <div class="back">
      <Button label="Projects" icon="pi pi-arrow-left" text size="small" @click="cancel" />
    </div>

    <div class="header">
      <h1>{{ isEdit ? "Edit project" : "New project" }}</h1>
      <div class="actions">
        <Button label="Cancel" text @click="cancel" />
        <Button
          :label="isEdit ? 'Save changes' : 'Create project'"
          :disabled="!canSave"
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
          <span>Customer</span>
          <Dropdown
            v-model="form.customer_id"
            :options="customers ?? []"
            option-label="name"
            option-value="customer_id"
            placeholder="Pick a customer"
            :filter="true"
            :disabled="isEdit"
          />
        </label>
        <label class="field">
          <span>Status</span>
          <Dropdown
            v-model="form.status"
            :options="statusOptions"
            option-label="label"
            option-value="value"
          />
        </label>
      </div>

      <div class="two-col">
        <label class="field">
          <span>Code <span class="hint">(globally unique, e.g. ATLAS-26)</span></span>
          <InputText v-model="form.code" maxlength="40" placeholder="ATLAS-26" />
        </label>
        <label class="field">
          <span>Name</span>
          <InputText v-model="form.name" placeholder="Atlas CRM rebuild" />
        </label>
      </div>

      <div class="two-col">
        <label class="field">
          <span>Currency</span>
          <Dropdown v-model="form.currency" :options="currencyOptions" />
        </label>
        <label class="field">
          <span>Contract value <span class="hint">(optional)</span></span>
          <InputNumber
            v-model="form.contract_value"
            mode="decimal"
            :min-fraction-digits="form.currency === 'IDR' ? 0 : 2"
            :max-fraction-digits="form.currency === 'IDR' ? 0 : 2"
            :use-grouping="true"
            :min="0"
          />
        </label>
      </div>

      <div class="two-col">
        <label class="field">
          <span>Start date</span>
          <Calendar v-model="form.start_date" date-format="yy-mm-dd" show-icon />
        </label>
        <label class="field">
          <span>End date</span>
          <Calendar v-model="form.end_date" date-format="yy-mm-dd" show-icon />
        </label>
      </div>

      <label class="field">
        <span>Notes</span>
        <Textarea v-model="form.notes" rows="3" autoResize />
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
  max-width: 760px;
}
.field { display: flex; flex-direction: column; gap: 0.3rem; font-size: 0.875rem; color: var(--color-text); }
.field > span { font-weight: 600; font-size: 0.85rem; }
.hint { font-weight: 400; color: var(--color-text-muted); font-size: 0.78rem; }
.field :deep(.p-inputtext),
.field :deep(.p-inputtextarea),
.field :deep(.p-inputnumber),
.field :deep(.p-inputnumber-input),
.field :deep(.p-calendar),
.field :deep(.p-dropdown) { width: 100%; }
.two-col { display: grid; grid-template-columns: 1fr 1fr; gap: 1rem; min-width: 0; }
.two-col > .field { min-width: 0; }
</style>
