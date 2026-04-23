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
import { api } from "@/api/client";

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

function buildBody() {
  const body: Record<string, unknown> = { ...form.value };
  for (const k of Object.keys(body)) {
    if (body[k] === "" || body[k] === null) delete body[k];
  }
  body.item_type = form.value.item_type;
  body.name = form.value.name;
  body.default_currency = form.value.default_currency;
  body.active = form.value.active;
  body.is_sold = form.value.is_sold;
  body.is_purchased = form.value.is_purchased;
  // If not sold, clear sales-only fields; same for purchase.
  if (!form.value.is_sold) {
    delete body.default_unit_price;
    delete body.revenue_account_id;
  }
  if (!form.value.is_purchased) {
    delete body.default_purchase_price;
    delete body.expense_account_id;
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
          :disabled="!form.name || !form.item_type"
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
</style>
