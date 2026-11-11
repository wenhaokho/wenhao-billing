<script setup lang="ts">
import AppIcon from "@/components/AppIcon.vue";
import { useRouter } from "vue-router";
import { useQuery } from "@tanstack/vue-query";
import Button from "primevue/button";
import StatusTag from "@/components/StatusTag.vue";
import PageHeader from "@/components/PageHeader.vue";
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
      <Button label="Vendors" text size="small" @click="router.push('/vendors')">
        <template #icon><AppIcon name="arrow-left" /></template>
      </Button>
    </div>

    <PageHeader eyebrow="03 / Purchases" :title="vendor?.name ?? (isLoading ? 'Loading…' : 'Vendor')">
      <template v-if="vendor" #status>
        <StatusTag :status="vendor.active ? 'ACTIVE' : 'INACTIVE'" />
      </template>
      <Button label="Edit" @click="goEdit" :disabled="!vendor">
        <template #icon><AppIcon name="pencil" /></template>
      </Button>
    </PageHeader>

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
.back { margin-bottom: 8px; }
.card-pad { padding: 28px; }
.grid { display: grid; grid-template-columns: 1fr 1fr; gap: 22px 40px; max-width: 900px; }
@media (max-width: 720px) { .grid { grid-template-columns: 1fr; } .card-pad { padding: 20px; } }
.field { display: flex; flex-direction: column; gap: 6px; }
.field.full { grid-column: 1 / -1; }
.field > span { font: 500 11px/1.4 var(--wb-font-sans); color: var(--wb-muted-soft); text-transform: uppercase; letter-spacing: 1.2px; }
.field > p { margin: 0; font-size: 15px; color: var(--wb-ink); }
.pre { white-space: pre-wrap; }
.muted { color: var(--wb-muted); }
.small { font-size: 13px; }
code { background: var(--wb-tint); padding: 2px 6px; font-size: 12px; font-weight: 500; }
.error { color: var(--wb-red-fg); padding: 16px 0; }
</style>
