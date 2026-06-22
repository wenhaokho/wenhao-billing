<script setup lang="ts">
import { computed, reactive, ref, watch } from "vue";
import { useQuery, useMutation, useQueryClient } from "@tanstack/vue-query";
import Button from "primevue/button";
import Checkbox from "primevue/checkbox";
import Column from "primevue/column";
import DataTable from "primevue/datatable";
import DatePicker from "primevue/calendar";
import Dialog from "primevue/dialog";
import Dropdown from "primevue/dropdown";
import InputNumber from "primevue/inputnumber";
import InputText from "primevue/inputtext";
import TabPanel from "primevue/tabpanel";
import TabView from "primevue/tabview";
import Tag from "primevue/tag";
import { api } from "@/api/client";

type SubscriptionStatus = "ACTIVE" | "SUSPEND_PENDING" | "SUSPENDED" | "CANCELLED";

interface Customer {
  customer_id: string;
  name: string;
  default_currency: string | null;
}

interface ProjectLite {
  project_id: string;
  customer_id: string;
  code: string;
  name: string;
  currency: string;
  status: string;
}

interface ItemLite {
  item_id: string;
  name: string;
  default_currency: string;
  default_unit_price: string | null;
  active: boolean;
  is_sold: boolean;
}

interface CloudflareTarget {
  zone_id: string;
  record_id: string;
  record_name: string;
  record_type: string;
  live_content: string;
  maintenance_content: string;
  proxied: boolean;
  provider_status: string;
  last_error: string | null;
}

interface HostingSubscription {
  subscription_id: string;
  customer_id: string;
  project_id: string | null;
  template_invoice_id: string | null;
  item_id: string | null;
  service_name: string;
  domain_name: string;
  currency: string;
  bundle_months: number;
  payment_terms: string;
  billing_anchor_date: string;
  grace_days: number;
  suspension_enabled: boolean;
  status: SubscriptionStatus;
  last_invoice_id: string | null;
  last_paid_at: string | null;
  cloudflare_target: CloudflareTarget | null;
}

type FormState = {
  subscription_id: string | null;
  customer_id: string | null;
  project_id: string | null;
  item_id: string | null;
  service_name: string;
  domain_name: string;
  currency: string;
  bundle_months: number;
  payment_terms: string;
  billing_anchor_date: Date | null;
  grace_days: number;
  suspension_enabled: boolean;
  status: SubscriptionStatus;
  zone_id: string;
  record_id: string;
  record_name: string;
  record_type: string;
  live_content: string;
  maintenance_content: string;
  proxied: boolean;
};

const statusTabs: Array<{ label: string; value: SubscriptionStatus | null }> = [
  { label: "All", value: null },
  { label: "Active", value: "ACTIVE" },
  { label: "Pending", value: "SUSPEND_PENDING" },
  { label: "Suspended", value: "SUSPENDED" },
  { label: "Cancelled", value: "CANCELLED" },
];
const paymentTermsOptions = ["On Receipt", "Net 7", "Net 14", "Net 30", "Net 60"];
const recordTypeOptions = ["A", "AAAA", "CNAME", "TXT"];
const currencyOptions = ["IDR", "SGD", "USD", "EUR"];

const queryClient = useQueryClient();
const activeTab = ref(0);
const dialogOpen = ref(false);
const saveError = ref<string | null>(null);

const form = reactive<FormState>({
  subscription_id: null,
  customer_id: null,
  project_id: null,
  item_id: null,
  service_name: "",
  domain_name: "",
  currency: "USD",
  bundle_months: 1,
  payment_terms: "Net 7",
  billing_anchor_date: new Date(),
  grace_days: 3,
  suspension_enabled: true,
  status: "ACTIVE",
  zone_id: "",
  record_id: "",
  record_name: "",
  record_type: "CNAME",
  live_content: "",
  maintenance_content: "",
  proxied: true,
});

const activeStatus = computed(() => statusTabs[activeTab.value]?.value ?? null);
const isEditing = computed(() => !!form.subscription_id);

const { data: customers } = useQuery<Customer[]>({
  queryKey: ["customers"],
  queryFn: async () => (await api.get<Customer[]>("/customers")).data,
});

const { data: items } = useQuery<ItemLite[]>({
  queryKey: ["items"],
  queryFn: async () => (await api.get<ItemLite[]>("/items")).data,
});

const { data: projects } = useQuery<ProjectLite[]>({
  queryKey: ["hosting-projects", () => form.customer_id],
  enabled: () => !!form.customer_id,
  queryFn: async () => {
    if (!form.customer_id) return [];
    return (
      await api.get<ProjectLite[]>("/projects", {
        params: { customer_id: form.customer_id, status: "ACTIVE" },
      })
    ).data;
  },
});

const { data: subscriptions, isLoading } = useQuery<HostingSubscription[]>({
  queryKey: ["hosting-subscriptions", activeStatus],
  queryFn: async () => {
    const params = activeStatus.value ? { status: activeStatus.value } : undefined;
    return (await api.get<HostingSubscription[]>("/hosting-subscriptions", { params })).data;
  },
});

const saveMutation = useMutation({
  mutationFn: async () => {
    const body = {
      customer_id: form.customer_id,
      project_id: form.project_id || null,
      item_id: form.item_id,
      service_name: form.service_name,
      domain_name: form.domain_name,
      currency: form.currency,
      bundle_months: form.bundle_months,
      payment_terms: form.payment_terms,
      billing_anchor_date: toISODate(form.billing_anchor_date),
      grace_days: form.grace_days,
      suspension_enabled: form.suspension_enabled,
      status: form.status,
      cloudflare_target: {
        zone_id: form.zone_id,
        record_id: form.record_id,
        record_name: form.record_name,
        record_type: form.record_type,
        live_content: form.live_content,
        maintenance_content: form.maintenance_content,
        proxied: form.proxied,
      },
    };
    if (form.subscription_id) {
      return (
        await api.patch(`/hosting-subscriptions/${form.subscription_id}`, body)
      ).data;
    }
    return (await api.post("/hosting-subscriptions", body)).data;
  },
  onSuccess: () => {
    saveError.value = null;
    dialogOpen.value = false;
    queryClient.invalidateQueries({ queryKey: ["hosting-subscriptions"] });
    queryClient.invalidateQueries({ queryKey: ["invoices"] });
  },
  onError: (error: { response?: { data?: { detail?: string } } }) => {
    saveError.value = error?.response?.data?.detail ?? "Failed to save hosting subscription";
  },
});

watch(
  () => form.customer_id,
  (customerId, previousCustomerId) => {
    if (customerId !== previousCustomerId) form.project_id = null;
    const customer = (customers.value ?? []).find((row) => row.customer_id === customerId);
    if (customer?.default_currency && !isEditing.value) form.currency = customer.default_currency;
  },
);

watch(
  () => form.item_id,
  (itemId) => {
    const item = (items.value ?? []).find((row) => row.item_id === itemId);
    if (!item || isEditing.value) return;
    if (!form.service_name) form.service_name = item.name;
    if (item.default_currency) form.currency = item.default_currency;
  },
);

function toISODate(value: Date | null): string | null {
  if (!value) return null;
  const yyyy = value.getFullYear();
  const mm = String(value.getMonth() + 1).padStart(2, "0");
  const dd = String(value.getDate()).padStart(2, "0");
  return `${yyyy}-${mm}-${dd}`;
}

function resetForm() {
  form.subscription_id = null;
  form.customer_id = null;
  form.project_id = null;
  form.item_id = null;
  form.service_name = "";
  form.domain_name = "";
  form.currency = "USD";
  form.bundle_months = 1;
  form.payment_terms = "Net 7";
  form.billing_anchor_date = new Date();
  form.grace_days = 3;
  form.suspension_enabled = true;
  form.status = "ACTIVE";
  form.zone_id = "";
  form.record_id = "";
  form.record_name = "";
  form.record_type = "CNAME";
  form.live_content = "";
  form.maintenance_content = "";
  form.proxied = true;
}

function openNew() {
  saveError.value = null;
  resetForm();
  dialogOpen.value = true;
}

function openEdit(subscription: HostingSubscription) {
  saveError.value = null;
  form.subscription_id = subscription.subscription_id;
  form.customer_id = subscription.customer_id;
  form.project_id = subscription.project_id;
  form.item_id = subscription.item_id;
  form.service_name = subscription.service_name;
  form.domain_name = subscription.domain_name;
  form.currency = subscription.currency;
  form.bundle_months = subscription.bundle_months;
  form.payment_terms = subscription.payment_terms;
  form.billing_anchor_date = new Date(`${subscription.billing_anchor_date}T00:00:00`);
  form.grace_days = subscription.grace_days;
  form.suspension_enabled = subscription.suspension_enabled;
  form.status = subscription.status;
  form.zone_id = subscription.cloudflare_target?.zone_id ?? "";
  form.record_id = subscription.cloudflare_target?.record_id ?? "";
  form.record_name = subscription.cloudflare_target?.record_name ?? "";
  form.record_type = subscription.cloudflare_target?.record_type ?? "CNAME";
  form.live_content = subscription.cloudflare_target?.live_content ?? "";
  form.maintenance_content = subscription.cloudflare_target?.maintenance_content ?? "";
  form.proxied = subscription.cloudflare_target?.proxied ?? true;
  dialogOpen.value = true;
}

function statusSeverity(status: SubscriptionStatus) {
  if (status === "ACTIVE") return "success";
  if (status === "SUSPENDED") return "danger";
  if (status === "SUSPEND_PENDING") return "warning";
  return "secondary";
}

function providerSeverity(providerStatus: string | null | undefined) {
  if (providerStatus === "ACTIVE") return "success";
  if (providerStatus === "SUSPENDED") return "danger";
  return "warning";
}

function cadenceLabel(months: number) {
  return months === 1 ? "Every month" : `Every ${months} months`;
}

const customerNameMap = computed(() => {
  const map: Record<string, string> = {};
  for (const customer of customers.value ?? []) map[customer.customer_id] = customer.name;
  return map;
});

const canSave = computed(() => {
  return Boolean(
    form.customer_id &&
      form.item_id &&
      form.service_name.trim() &&
      form.domain_name.trim() &&
      form.currency &&
      form.payment_terms &&
      form.billing_anchor_date &&
      form.zone_id.trim() &&
      form.record_id.trim() &&
      form.record_name.trim() &&
      form.record_type &&
      form.live_content.trim() &&
      form.maintenance_content.trim(),
  );
});
</script>

<template>
  <section>
    <header class="page-header">
      <div>
        <h1>Hosting subscriptions</h1>
        <p class="subtitle">
          Prepaid hosting bundles that generate recurring invoices and can suspend DNS after the due date plus grace period.
        </p>
      </div>
      <div class="page-actions">
        <Button label="New hosting subscription" icon="pi pi-plus" @click="openNew" />
      </div>
    </header>

    <TabView v-model:active-index="activeTab" class="status-tabs">
      <TabPanel v-for="tab in statusTabs" :key="tab.label" :header="tab.label" />
    </TabView>

    <DataTable :value="subscriptions ?? []" :loading="isLoading" data-key="subscription_id" striped-rows>
      <template #empty>
        <div class="empty-state">
          <i class="pi pi-globe" />
          <div>No hosting subscriptions in this view.</div>
        </div>
      </template>

      <Column header="Customer">
        <template #body="{ data: row }">
          {{ customerNameMap[row.customer_id] ?? "—" }}
        </template>
      </Column>

      <Column field="domain_name" header="Domain" />
      <Column field="service_name" header="Service" />

      <Column header="Billing">
        <template #body="{ data: row }">
          <div>{{ cadenceLabel(row.bundle_months) }}</div>
          <div class="muted small">{{ row.payment_terms }} · anchor {{ row.billing_anchor_date }}</div>
        </template>
      </Column>

      <Column header="Cloudflare">
        <template #body="{ data: row }">
          <Tag
            :value="row.cloudflare_target?.provider_status ?? 'UNSET'"
            :severity="providerSeverity(row.cloudflare_target?.provider_status)"
          />
        </template>
      </Column>

      <Column header="Status">
        <template #body="{ data: row }">
          <Tag :value="row.status" :severity="statusSeverity(row.status)" />
        </template>
      </Column>

      <Column header="" :style="{ width: '96px' }">
        <template #body="{ data: row }">
          <Button icon="pi pi-pencil" text rounded title="Edit" @click="openEdit(row)" />
        </template>
      </Column>
    </DataTable>

    <Dialog
      v-model:visible="dialogOpen"
      modal
      :header="isEditing ? 'Edit hosting subscription' : 'New hosting subscription'"
      :style="{ width: 'min(960px, 95vw)' }"
    >
      <div class="form-grid">
        <div class="field">
          <label>Customer</label>
          <Dropdown
            v-model="form.customer_id"
            :options="customers ?? []"
            option-label="name"
            option-value="customer_id"
            placeholder="Select customer"
            filter
          />
        </div>

        <div class="field">
          <label>Project</label>
          <Dropdown
            v-model="form.project_id"
            :options="projects ?? []"
            option-label="name"
            option-value="project_id"
            placeholder="Optional project"
            show-clear
            filter
          />
        </div>

        <div class="field">
          <label>Item</label>
          <Dropdown
            v-model="form.item_id"
            :options="(items ?? []).filter((row) => row.active && row.is_sold)"
            option-label="name"
            option-value="item_id"
            placeholder="Billable item"
            filter
          />
        </div>

        <div class="field">
          <label>Currency</label>
          <Dropdown v-model="form.currency" :options="currencyOptions" />
        </div>

        <div class="field field-span-2">
          <label>Service name</label>
          <InputText v-model.trim="form.service_name" />
        </div>

        <div class="field field-span-2">
          <label>Domain</label>
          <InputText v-model.trim="form.domain_name" placeholder="example.com" />
        </div>

        <div class="field">
          <label>Bundle months</label>
          <InputNumber v-model="form.bundle_months" :min="1" :max="60" show-buttons />
        </div>

        <div class="field">
          <label>Payment terms</label>
          <Dropdown v-model="form.payment_terms" :options="paymentTermsOptions" />
        </div>

        <div class="field">
          <label>Billing anchor date</label>
          <DatePicker v-model="form.billing_anchor_date" date-format="yy-mm-dd" show-icon />
        </div>

        <div class="field">
          <label>Grace days</label>
          <InputNumber v-model="form.grace_days" :min="0" :max="365" show-buttons />
        </div>

        <div class="field checkbox-field">
          <Checkbox v-model="form.suspension_enabled" binary input-id="suspension-enabled" />
          <label for="suspension-enabled">Suspend after overdue grace</label>
        </div>

        <div class="field checkbox-field">
          <Checkbox v-model="form.proxied" binary input-id="proxied" />
          <label for="proxied">Cloudflare proxied</label>
        </div>

        <div class="field">
          <label>Zone ID</label>
          <InputText v-model.trim="form.zone_id" />
        </div>

        <div class="field">
          <label>Record ID</label>
          <InputText v-model.trim="form.record_id" />
        </div>

        <div class="field">
          <label>Record name</label>
          <InputText v-model.trim="form.record_name" />
        </div>

        <div class="field">
          <label>Record type</label>
          <Dropdown v-model="form.record_type" :options="recordTypeOptions" />
        </div>

        <div class="field field-span-2">
          <label>Live target</label>
          <InputText v-model.trim="form.live_content" placeholder="live.example.net" />
        </div>

        <div class="field field-span-2">
          <label>Maintenance target</label>
          <InputText v-model.trim="form.maintenance_content" placeholder="maintenance.example.net" />
        </div>
      </div>

      <div v-if="saveError" class="dialog-error">
        {{ saveError }}
      </div>

      <template #footer>
        <Button label="Cancel" text @click="dialogOpen = false" />
        <Button
          :label="isEditing ? 'Save changes' : 'Create subscription'"
          :loading="saveMutation.isPending.value"
          :disabled="!canSave"
          @click="saveMutation.mutate()"
        />
      </template>
    </Dialog>
  </section>
</template>

<style scoped>
.muted { color: var(--color-text-subtle); }
.small { font-size: 0.82rem; }

.form-grid {
  display: grid;
  gap: 1rem;
  grid-template-columns: repeat(2, minmax(0, 1fr));
}

.field {
  display: flex;
  flex-direction: column;
  gap: 0.35rem;
}

.field-span-2 {
  grid-column: span 2;
}

.checkbox-field {
  flex-direction: row;
  align-items: center;
  gap: 0.6rem;
  padding-top: 1.8rem;
}

.dialog-error {
  margin-top: 1rem;
  color: var(--red-600, #dc2626);
}

@media (max-width: 720px) {
  .form-grid {
    grid-template-columns: 1fr;
  }

  .field-span-2 {
    grid-column: span 1;
  }

  .checkbox-field {
    padding-top: 0;
  }
}
</style>
