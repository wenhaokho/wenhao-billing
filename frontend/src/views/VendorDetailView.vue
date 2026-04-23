<script setup lang="ts">
import { useRouter } from "vue-router";
import { useQuery } from "@tanstack/vue-query";
import Button from "primevue/button";
import Tag from "primevue/tag";
import { api } from "@/api/client";

interface Vendor {
  vendor_id: string;
  name: string;
  contact_name: string | null;
  contact_email: string | null;
  default_currency: string;
  payment_terms_days: number;
  tax_id: string | null;
  notes: string | null;
  active: boolean;
  created_at: string;
}

const props = defineProps<{ vendorId: string }>();
const router = useRouter();

const { data: vendor, isLoading, error } = useQuery<Vendor>({
  queryKey: ["vendor", () => props.vendorId],
  queryFn: async () => (await api.get<Vendor>(`/vendors/${props.vendorId}`)).data,
});

function goEdit() {
  router.push({ name: "vendor-edit", params: { vendorId: props.vendorId } });
}
</script>

<template>
  <section>
    <div class="back">
      <Button label="Vendors" icon="pi pi-arrow-left" text size="small" @click="router.push('/vendors')" />
    </div>

    <header class="page-header">
      <div>
        <h1>{{ vendor?.name ?? (isLoading ? "Loading…" : "Vendor") }}</h1>
        <p v-if="vendor" class="subtitle">
          <Tag
            :value="vendor.active ? 'Active' : 'Inactive'"
            :severity="vendor.active ? 'success' : 'secondary'"
          />
        </p>
      </div>
      <div class="page-actions">
        <Button label="Edit" icon="pi pi-pencil" @click="goEdit" :disabled="!vendor" />
      </div>
    </header>

    <div v-if="error" class="error">Failed to load vendor.</div>

    <div v-if="vendor" class="card card-pad grid">
      <div class="field"><span>Contact name</span><p>{{ vendor.contact_name || "—" }}</p></div>
      <div class="field">
        <span>Contact email</span>
        <p>
          <a v-if="vendor.contact_email" :href="`mailto:${vendor.contact_email}`">{{ vendor.contact_email }}</a>
          <span v-else>—</span>
        </p>
      </div>
      <div class="field"><span>Default currency</span><p><code>{{ vendor.default_currency }}</code></p></div>
      <div class="field"><span>Payment terms</span><p>Net {{ vendor.payment_terms_days }}</p></div>
      <div class="field"><span>Tax ID</span><p>{{ vendor.tax_id || "—" }}</p></div>
      <div class="field full"><span>Notes</span><p class="pre">{{ vendor.notes || "—" }}</p></div>
      <div class="field"><span>Created</span><p class="muted small">{{ new Date(vendor.created_at).toLocaleString() }}</p></div>
    </div>
  </section>
</template>

<style scoped>
.back { margin-bottom: -0.5rem; }
.grid { display: grid; grid-template-columns: 1fr 1fr; gap: 1rem 2rem; }
.field { display: flex; flex-direction: column; gap: 0.2rem; }
.field.full { grid-column: 1 / -1; }
.field > span { font-size: 0.72rem; font-weight: 600; color: var(--color-text-muted); text-transform: uppercase; letter-spacing: 0.05em; }
.field > p { margin: 0; font-size: 0.92rem; }
.pre { white-space: pre-wrap; }
.muted { color: var(--color-text-muted); }
.small { font-size: 0.82rem; }
code { background: var(--color-surface-alt); padding: 0.1rem 0.4rem; border-radius: 4px; font-size: 0.85em; font-weight: 600; }
.error { color: #b91c1c; padding: 1rem; }
</style>
