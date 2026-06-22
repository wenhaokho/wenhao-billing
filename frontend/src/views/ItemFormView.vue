<script setup lang="ts">
import { ref, computed, watch } from "vue";
import { useRouter } from "vue-router";
import { useQuery, useMutation, useQueryClient } from "@tanstack/vue-query";
import Button from "primevue/button";
import InputText from "primevue/inputtext";
import InputNumber from "primevue/inputnumber";
import Checkbox from "primevue/checkbox";
import Textarea from "primevue/textarea";
import Dropdown from "primevue/dropdown";
import Message from "primevue/message";
import Tag from "primevue/tag";
import { api } from "@/api/client";

interface CloudflareTarget {
  target_id: string;
  item_id: string;
  zone_id: string;
  record_id: string;
  record_name: string;
  record_type: string;
  live_content: string;
  maintenance_content: string;
  proxied: boolean;
  provider_status: string;
  last_action_at: string | null;
  last_error: string | null;
}

interface Item {
  item_id: string;
  sku: string | null;
  name: string;
  item_type: "SERVICE" | "USAGE" | "FIXED_FEE";
  description: string | null;
  default_currency: string;
  default_unit_price: string | null;
  default_purchase_price: string | null;
  revenue_account_id: number | null;
  expense_account_id: number | null;
  is_sold: boolean;
  is_purchased: boolean;
  active: boolean;
  is_hosting: boolean;
  hosting_domain: string | null;
  hosting_grace_days: number | null;
  hosting_suspension_enabled: boolean | null;
  hosting_status: string | null;
  hosting_last_action_at: string | null;
  hosting_last_error: string | null;
  cloudflare_target: CloudflareTarget | null;
}

type AccountType = "ASSET" | "LIABILITY" | "EQUITY" | "INCOME" | "EXPENSE" | "COGS";

interface Account {
  account_id: number;
  code: string;
  name: string;
  type: AccountType;
}

const props = defineProps<{ itemId?: string }>();
const router = useRouter();
const queryClient = useQueryClient();

const itemTypes = [
  { label: "Fixed fee", value: "FIXED_FEE" },
  { label: "Service (recurring)", value: "SERVICE" },
  { label: "Usage (metered)", value: "USAGE" },
];
const currencyOptions = ["IDR", "SGD", "USD"];
const recordTypes = ["A", "AAAA", "CNAME"];

type FormShape = {
  sku: string;
  name: string;
  item_type: Item["item_type"];
  description: string;
  default_currency: string;
  default_unit_price: number | null;
  default_purchase_price: number | null;
  revenue_account_id: number | null;
  expense_account_id: number | null;
  is_sold: boolean;
  is_purchased: boolean;
  active: boolean;
  is_hosting: boolean;
  hosting_domain: string;
  hosting_grace_days: number | null;
  hosting_suspension_enabled: boolean;
  zone_id: string;
  record_id: string;
  record_name: string;
  record_type: string;
  live_content: string;
  maintenance_content: string;
  proxied: boolean;
};

function emptyForm(): FormShape {
  return {
    sku: "",
    name: "",
    item_type: "SERVICE",
    description: "",
    default_currency: "IDR",
    default_unit_price: null,
    default_purchase_price: null,
    revenue_account_id: null,
    expense_account_id: null,
    is_sold: true,
    is_purchased: false,
    active: true,
    is_hosting: false,
    hosting_domain: "",
    hosting_grace_days: 3,
    hosting_suspension_enabled: true,
    zone_id: "",
    record_id: "",
    record_name: "",
    record_type: "CNAME",
    live_content: "",
    maintenance_content: "",
    proxied: true,
  };
}

const form = ref<FormShape>(emptyForm());
const isEdit = computed(() => !!props.itemId);
const saveError = ref<string | null>(null);

const { data: existing, isLoading: loadingExisting } = useQuery<Item>({
  queryKey: ["item", () => props.itemId],
  queryFn: async () => (await api.get<Item>(`/items/${props.itemId}`)).data,
  enabled: () => !!props.itemId,
});

watch(
  existing,
  (v) => {
    if (!v) return;
    form.value = {
      sku: v.sku ?? "",
      name: v.name,
      item_type: v.item_type,
      description: v.description ?? "",
      default_currency: v.default_currency,
      default_unit_price: v.default_unit_price ? Number(v.default_unit_price) : null,
      default_purchase_price: v.default_purchase_price ? Number(v.default_purchase_price) : null,
      revenue_account_id: v.revenue_account_id,
      expense_account_id: v.expense_account_id,
      is_sold: v.is_sold,
      is_purchased: v.is_purchased,
      active: v.active,
      is_hosting: v.is_hosting ?? false,
      hosting_domain: v.hosting_domain ?? "",
      hosting_grace_days: v.hosting_grace_days ?? 3,
      hosting_suspension_enabled: v.hosting_suspension_enabled ?? true,
      zone_id: v.cloudflare_target?.zone_id ?? "",
      record_id: v.cloudflare_target?.record_id ?? "",
      record_name: v.cloudflare_target?.record_name ?? "",
      record_type: v.cloudflare_target?.record_type ?? "CNAME",
      live_content: v.cloudflare_target?.live_content ?? "",
      maintenance_content: v.cloudflare_target?.maintenance_content ?? "",
      proxied: v.cloudflare_target?.proxied ?? true,
    };
  },
  { immediate: true },
);

const { data: accounts } = useQuery<Account[]>({
  queryKey: ["chart-of-accounts"],
  queryFn: async () => (await api.get<Account[]>("/accounting/chart-of-accounts")).data,
});

const incomeAccounts = computed(
  () => (accounts.value ?? []).filter((a) => a.type === "INCOME"),
);
const expenseAccounts = computed(
  () => (accounts.value ?? []).filter((a) => a.type === "EXPENSE" || a.type === "COGS"),
);

function statusSeverity(s: string | null | undefined) {
  switch (s) {
    case "ACTIVE": return "success";
    case "SUSPENDED": return "danger";
    case "SUSPEND_PENDING": return "warning";
    case "CANCELLED": return "secondary";
    default: return undefined;
  }
}

function buildBody() {
  const body: Record<string, unknown> = {
    name: form.value.name,
    item_type: form.value.item_type,
    default_currency: form.value.default_currency,
    active: form.value.active,
    is_sold: form.value.is_sold,
    is_purchased: form.value.is_purchased,
    is_hosting: form.value.is_hosting,
  };
  if (form.value.sku) body.sku = form.value.sku;
  if (form.value.description) body.description = form.value.description;
  if (form.value.is_sold) {
    if (form.value.default_unit_price != null) body.default_unit_price = form.value.default_unit_price;
    if (form.value.revenue_account_id != null) body.revenue_account_id = form.value.revenue_account_id;
  }
  if (form.value.is_purchased) {
    if (form.value.default_purchase_price != null) body.default_purchase_price = form.value.default_purchase_price;
    if (form.value.expense_account_id != null) body.expense_account_id = form.value.expense_account_id;
  }
  if (form.value.is_hosting) {
    body.hosting_domain = form.value.hosting_domain;
    body.hosting_grace_days = form.value.hosting_grace_days ?? 3;
    body.hosting_suspension_enabled = form.value.hosting_suspension_enabled;
    body.cloudflare_target = {
      zone_id: form.value.zone_id,
      record_id: form.value.record_id,
      record_name: form.value.record_name,
      record_type: form.value.record_type,
      live_content: form.value.live_content,
      maintenance_content: form.value.maintenance_content,
      proxied: form.value.proxied,
    };
  }
  return body;
}

const save = useMutation({
  mutationFn: async () => {
    if (props.itemId) {
      return api.patch<Item>(`/items/${props.itemId}`, buildBody());
    }
    return api.post<Item>("/items", buildBody());
  },
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ["items"] });
    queryClient.invalidateQueries({ queryKey: ["item"] });
    router.push({ name: "items" });
  },
  onError: (err: any) => {
    saveError.value = err?.response?.data?.detail ?? "Save failed";
  },
});

const canSubmit = computed(() => {
  if (!form.value.name || !form.value.item_type) return false;
  if (form.value.is_hosting) {
    return !!(
      form.value.hosting_domain.trim() &&
      form.value.zone_id.trim() &&
      form.value.record_id.trim() &&
      form.value.record_name.trim() &&
      form.value.live_content.trim() &&
      form.value.maintenance_content.trim()
    );
  }
  return true;
});

function submit() {
  saveError.value = null;
  save.mutate();
}

function cancel() {
  router.push({ name: "items" });
}
</script>

<template>
  <section class="page">
    <div class="back">
      <Button label="Products &amp; Services" icon="pi pi-arrow-left" text size="small" @click="cancel" />
    </div>

    <div class="header">
      <h1>{{ isEdit ? "Edit item" : "New item" }}</h1>
      <div class="actions">
        <Button label="Cancel" text @click="cancel" />
        <Button
          :label="isEdit ? 'Save changes' : 'Create item'"
          :disabled="!canSubmit"
          :loading="save.isPending.value"
          @click="submit"
        />
      </div>
    </div>

    <Message v-if="saveError" severity="error" role="alert" aria-live="assertive" :closable="true" @close="saveError = null">
      {{ saveError }}
    </Message>

    <div v-if="loadingExisting && isEdit" class="card">Loading…</div>
    <div v-else class="card">
      <div class="two-col">
        <label class="field">
          <span>SKU</span>
          <InputText v-model="form.sku" maxlength="50" />
        </label>
        <label class="field">
          <span>Type</span>
          <Dropdown
            v-model="form.item_type"
            :options="itemTypes"
            option-label="label"
            option-value="value"
          />
        </label>
      </div>
      <label class="field">
        <span>Name</span>
        <InputText v-model="form.name" autofocus />
      </label>
      <label class="field">
        <span>Description</span>
        <Textarea v-model="form.description" rows="3" autoResize />
      </label>
      <label class="field">
        <span>Default currency</span>
        <Dropdown v-model="form.default_currency" :options="currencyOptions" />
      </label>

      <div class="sub">
        <label class="check">
          <Checkbox v-model="form.is_sold" :binary="true" inputId="is_sold" />
          <span>I sell this item</span>
        </label>
        <div v-if="form.is_sold" class="sub-body">
          <div class="two-col">
            <label class="field">
              <span>Default unit price</span>
              <InputNumber
                v-model="form.default_unit_price"
                mode="decimal"
                :min-fraction-digits="form.default_currency === 'IDR' ? 0 : 2"
                :max-fraction-digits="form.default_currency === 'IDR' ? 0 : 2"
                :use-grouping="true"
                :min="0"
              />
            </label>
            <label class="field">
              <span>Revenue account</span>
              <Dropdown
                v-model="form.revenue_account_id"
                :options="incomeAccounts"
                :option-label="(a: Account) => `${a.code} · ${a.name}`"
                option-value="account_id"
                placeholder="Pick a 4xxx income account"
                show-clear
              />
            </label>
          </div>
        </div>
      </div>

      <div class="sub">
        <label class="check">
          <Checkbox v-model="form.is_purchased" :binary="true" inputId="is_purchased" />
          <span>I buy this item</span>
        </label>
        <div v-if="form.is_purchased" class="sub-body">
          <div class="two-col">
            <label class="field">
              <span>Default purchase price</span>
              <InputNumber
                v-model="form.default_purchase_price"
                mode="decimal"
                :min-fraction-digits="form.default_currency === 'IDR' ? 0 : 2"
                :max-fraction-digits="form.default_currency === 'IDR' ? 0 : 2"
                :use-grouping="true"
                :min="0"
              />
            </label>
            <label class="field">
              <span>Expense account</span>
              <Dropdown
                v-model="form.expense_account_id"
                :options="expenseAccounts"
                :option-label="(a: Account) => `${a.code} · ${a.name}`"
                option-value="account_id"
                placeholder="Pick a 5xxx/6xxx expense or COGS account"
                show-clear
              />
            </label>
          </div>
        </div>
      </div>

      <div class="sub">
        <label class="check">
          <Checkbox v-model="form.is_hosting" :binary="true" inputId="is_hosting" />
          <span>This item provisions a hosted site (DNS suspension)</span>
        </label>
        <div v-if="form.is_hosting" class="sub-body">
          <div v-if="isEdit && existing?.hosting_status" class="status-row">
            <span class="status-label">Status</span>
            <Tag :value="existing.hosting_status" :severity="statusSeverity(existing.hosting_status)" />
            <span v-if="existing.cloudflare_target?.provider_status" class="status-label">Provider</span>
            <Tag
              v-if="existing.cloudflare_target?.provider_status"
              :value="existing.cloudflare_target.provider_status"
              :severity="statusSeverity(existing.cloudflare_target.provider_status)"
            />
            <span v-if="existing.hosting_last_error" class="error-detail">{{ existing.hosting_last_error }}</span>
          </div>

          <div class="two-col">
            <label class="field">
              <span>Domain</span>
              <InputText v-model.trim="form.hosting_domain" placeholder="example.com" />
            </label>
            <label class="field">
              <span>Grace days (overdue → suspend)</span>
              <InputNumber v-model="form.hosting_grace_days" :min="0" :max="365" />
            </label>
          </div>
          <label class="check">
            <Checkbox v-model="form.hosting_suspension_enabled" :binary="true" inputId="suspension_enabled" />
            <span>Enable automatic suspension when invoice is overdue</span>
          </label>

          <div class="hosting-cf">
            <div class="cf-title">Cloudflare DNS record</div>
            <div class="two-col">
              <label class="field">
                <span>Zone ID</span>
                <InputText v-model.trim="form.zone_id" />
              </label>
              <label class="field">
                <span>Record ID</span>
                <InputText v-model.trim="form.record_id" />
              </label>
            </div>
            <div class="two-col">
              <label class="field">
                <span>Record name</span>
                <InputText v-model.trim="form.record_name" placeholder="example.com" />
              </label>
              <label class="field">
                <span>Record type</span>
                <Dropdown v-model="form.record_type" :options="recordTypes" />
              </label>
            </div>
            <div class="two-col">
              <label class="field">
                <span>Live content</span>
                <InputText v-model.trim="form.live_content" placeholder="prod.hosting.net" />
              </label>
              <label class="field">
                <span>Maintenance content</span>
                <InputText v-model.trim="form.maintenance_content" placeholder="maintenance.you.com" />
              </label>
            </div>
            <label class="check">
              <Checkbox v-model="form.proxied" :binary="true" inputId="proxied" />
              <span>Proxied through Cloudflare</span>
            </label>
          </div>
        </div>
      </div>
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
.sub { display: flex; flex-direction: column; gap: 0.6rem; padding: 0.75rem 0; border-top: 1px dashed var(--color-border, #e2e8f0); }
.sub:first-of-type { border-top: none; padding-top: 0.25rem; }
.check { display: flex; align-items: center; gap: 0.5rem; font-weight: 600; font-size: 0.9rem; color: var(--color-text); cursor: pointer; }
.sub-body { padding-left: 1.75rem; display: flex; flex-direction: column; gap: 0.75rem; }
.hosting-cf { display: flex; flex-direction: column; gap: 0.75rem; padding-top: 0.5rem; border-top: 1px dashed var(--color-border, #e2e8f0); }
.cf-title { font-weight: 600; font-size: 0.82rem; color: var(--color-text-muted); text-transform: uppercase; letter-spacing: 0.04em; }
.status-row { display: flex; align-items: center; gap: 0.5rem; flex-wrap: wrap; font-size: 0.85rem; }
.status-label { color: var(--color-text-muted); }
.error-detail { color: var(--color-danger, #c44); font-size: 0.82rem; }
</style>
