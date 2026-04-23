<script setup lang="ts">
import { ref } from "vue";
import { useRouter } from "vue-router";
import { useQuery, useMutation, useQueryClient } from "@tanstack/vue-query";
import { useConfirm } from "primevue/useconfirm";
import DataTable from "primevue/datatable";
import Column from "primevue/column";
import Button from "primevue/button";
import Tag from "primevue/tag";
import InputSwitch from "primevue/inputswitch";
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
  created_at: string;
}

const router = useRouter();
const queryClient = useQueryClient();
const confirm = useConfirm();

const showInactive = ref(false);

const { data, isLoading } = useQuery<Vendor[]>({
  queryKey: ["vendors", showInactive],
  queryFn: async () => {
    const params = showInactive.value ? {} : { active: true };
    return (await api.get<Vendor[]>("/vendors", { params })).data;
  },
});

const deactivateVendor = useMutation({
  mutationFn: async (id: string) => api.delete<Vendor>(`/vendors/${id}`),
  onSuccess: () => queryClient.invalidateQueries({ queryKey: ["vendors"] }),
});

const reactivateVendor = useMutation({
  mutationFn: async (id: string) =>
    api.patch<Vendor>(`/vendors/${id}`, { active: true }),
  onSuccess: () => queryClient.invalidateQueries({ queryKey: ["vendors"] }),
});

function openCreate() {
  router.push({ name: "vendor-new" });
}

function openEdit(row: Vendor) {
  router.push({ name: "vendor-edit", params: { vendorId: row.vendor_id } });
}

function openDetail(row: Vendor) {
  router.push({ name: "vendor-detail", params: { vendorId: row.vendor_id } });
}

function confirmDeactivate(row: Vendor) {
  confirm.require({
    message: `Deactivate "${row.name}"? It will be hidden from active lists but kept for ledger history.`,
    header: "Deactivate vendor",
    icon: "pi pi-exclamation-triangle",
    acceptLabel: "Deactivate",
    rejectLabel: "Cancel",
    acceptClass: "p-button-danger",
    accept: () => deactivateVendor.mutate(row.vendor_id),
  });
}
</script>

<template>
  <section>
    <header class="page-header">
      <div>
        <h1>Vendors</h1>
        <p class="subtitle">
          Third-party suppliers — cloud, AI providers, subcontractors.
          These are the "other side" of your P&amp;L (5xxx COGS accounts).
        </p>
      </div>
      <div class="page-actions">
        <label class="toggle">
          <InputSwitch v-model="showInactive" />
          <span>Show inactive</span>
        </label>
        <Button label="New vendor" icon="pi pi-plus" @click="openCreate" />
      </div>
    </header>

    <DataTable :value="data ?? []" :loading="isLoading" data-key="vendor_id" striped-rows>
      <template #empty>
        <div class="empty-state">
          <i class="pi pi-briefcase" />
          <div>No vendors yet. Add your cloud, AI, and contractor suppliers.</div>
        </div>
      </template>
      <Column field="name" header="Name" />
      <Column header="Contact">
        <template #body="{ data: row }">
          <div v-if="row.contact_name || row.contact_email">
            <div v-if="row.contact_name">{{ row.contact_name }}</div>
            <div v-if="row.contact_email" class="muted small">{{ row.contact_email }}</div>
          </div>
          <span v-else class="muted">—</span>
        </template>
      </Column>
      <Column field="default_currency" header="Ccy" />
      <Column header="Terms">
        <template #body="{ data: row }">
          Net {{ row.payment_terms_days }}
        </template>
      </Column>
      <Column header="Status">
        <template #body="{ data: row }">
          <Tag
            :value="row.active ? 'Active' : 'Inactive'"
            :severity="row.active ? 'success' : 'secondary'"
          />
        </template>
      </Column>
      <Column header="" :style="{ width: '180px' }">
        <template #body="{ data: row }">
          <div class="row-actions">
            <Button icon="pi pi-eye" text rounded title="View" @click="openDetail(row)" />
            <Button icon="pi pi-pencil" text rounded title="Edit" @click="openEdit(row)" />
            <Button
              v-if="row.active"
              icon="pi pi-ban"
              text
              rounded
              severity="danger"
              title="Deactivate"
              :loading="deactivateVendor.isPending.value"
              @click="confirmDeactivate(row)"
            />
            <Button
              v-else
              icon="pi pi-undo"
              text
              rounded
              severity="success"
              title="Reactivate"
              :loading="reactivateVendor.isPending.value"
              @click="reactivateVendor.mutate(row.vendor_id)"
            />
          </div>
        </template>
      </Column>
    </DataTable>
  </section>
</template>

<style scoped>
.toggle { display: inline-flex; align-items: center; gap: 0.5rem; font-size: 0.85rem; color: var(--color-text-muted); }
.muted { color: var(--color-text-muted); }
.small { font-size: 0.8rem; }
.row-actions { display: flex; gap: 0.15rem; }
</style>
