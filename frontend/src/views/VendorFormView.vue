<script setup lang="ts">
import { ref, computed, watch } from "vue";
import { useRouter } from "vue-router";
import { useQuery, useMutation, useQueryClient } from "@tanstack/vue-query";
import Button from "primevue/button";
import InputText from "primevue/inputtext";
import InputNumber from "primevue/inputnumber";
import Textarea from "primevue/textarea";
import Dropdown from "primevue/dropdown";
import Message from "primevue/message";
import { api } from "@/api/client";

interface Vendor {
  vendor_id: string;
  name: string;
  contact_email: string | null;
  contact_name: string | null;
  default_currency: string;
  payment_terms_days: number;
  tax_id: string | null;
  notes: string | null;
  active: boolean;
}

const props = defineProps<{ vendorId?: string }>();
const router = useRouter();
const queryClient = useQueryClient();

const currencyOptions = ["IDR", "SGD", "USD"];

type FormShape = {
  name: string;
  contact_name: string;
  contact_email: string;
  default_currency: string;
  payment_terms_days: number;
  tax_id: string;
  notes: string;
  active: boolean;
};

function emptyForm(): FormShape {
  return {
    name: "",
    contact_name: "",
    contact_email: "",
    default_currency: "IDR",
    payment_terms_days: 30,
    tax_id: "",
    notes: "",
    active: true,
  };
}

const form = ref<FormShape>(emptyForm());
const isEdit = computed(() => !!props.vendorId);
const saveError = ref<string | null>(null);

const { data: existing, isLoading: loadingExisting } = useQuery<Vendor>({
  queryKey: ["vendor", () => props.vendorId],
  queryFn: async () => (await api.get<Vendor>(`/vendors/${props.vendorId}`)).data,
  enabled: () => !!props.vendorId,
});

watch(
  existing,
  (v) => {
    if (!v) return;
    form.value = {
      name: v.name,
      contact_name: v.contact_name ?? "",
      contact_email: v.contact_email ?? "",
      default_currency: v.default_currency,
      payment_terms_days: v.payment_terms_days,
      tax_id: v.tax_id ?? "",
      notes: v.notes ?? "",
      active: v.active,
    };
  },
  { immediate: true },
);

function buildBody() {
  const body: Record<string, unknown> = { ...form.value };
  for (const k of Object.keys(body)) {
    if (body[k] === "") delete body[k];
  }
  body.active = form.value.active;
  return body;
}

const save = useMutation({
  mutationFn: async () => {
    if (props.vendorId) {
      return api.patch<Vendor>(`/vendors/${props.vendorId}`, buildBody());
    }
    return api.post<Vendor>("/vendors", buildBody());
  },
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ["vendors"] });
    queryClient.invalidateQueries({ queryKey: ["vendor"] });
    router.push({ name: "vendors" });
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
  router.push({ name: "vendors" });
}
</script>

<template>
  <section class="page">
    <div class="back">
      <Button label="Vendors" icon="pi pi-arrow-left" text size="small" @click="cancel" />
    </div>

    <div class="header">
      <h1>{{ isEdit ? "Edit vendor" : "New vendor" }}</h1>
      <div class="actions">
        <Button label="Cancel" text @click="cancel" />
        <Button
          :label="isEdit ? 'Save changes' : 'Create vendor'"
          :disabled="!form.name"
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
      <label class="field">
        <span>Vendor name</span>
        <InputText v-model="form.name" autofocus />
      </label>
      <div class="two-col">
        <label class="field">
          <span>Contact name</span>
          <InputText v-model="form.contact_name" />
        </label>
        <label class="field">
          <span>Contact email</span>
          <InputText v-model="form.contact_email" type="email" />
        </label>
      </div>
      <div class="two-col">
        <label class="field">
          <span>Default currency</span>
          <Dropdown v-model="form.default_currency" :options="currencyOptions" />
        </label>
        <label class="field">
          <span>Payment terms (days)</span>
          <InputNumber v-model="form.payment_terms_days" :min="0" :max="365" />
        </label>
      </div>
      <label class="field">
        <span>Tax ID / VAT</span>
        <InputText v-model="form.tax_id" />
      </label>
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
.field :deep(.p-inputtext),
.field :deep(.p-inputtextarea),
.field :deep(.p-inputnumber),
.field :deep(.p-inputnumber-input),
.field :deep(.p-dropdown) { width: 100%; }
.two-col { display: grid; grid-template-columns: 1fr 1fr; gap: 1rem; min-width: 0; }
.two-col > .field { min-width: 0; }
</style>
