<script setup lang="ts">
import { computed } from "vue";
import { useRouter } from "vue-router";
import { useQuery } from "@tanstack/vue-query";
import Button from "primevue/button";
import Tag from "primevue/tag";
import { api } from "@/api/client";
import { formatMoney } from "@/utils/money";

interface Item {
  item_id: string;
  sku: string | null;
  name: string;
  item_type: "SERVICE" | "USAGE" | "FIXED_FEE";
  description: string | null;
  default_currency: string;
  default_unit_price: string | null;
  revenue_account_id: number | null;
  active: boolean;
  created_at: string;
}

interface Account {
  account_id: number;
  code: string;
  name: string;
  type: string;
}

const props = defineProps<{ itemId: string }>();
const router = useRouter();

const { data: item, isLoading, error } = useQuery<Item>({
  queryKey: ["item", () => props.itemId],
  queryFn: async () => (await api.get<Item>(`/items/${props.itemId}`)).data,
});

const { data: accounts } = useQuery<Account[]>({
  queryKey: ["chart-of-accounts"],
  queryFn: async () => (await api.get<Account[]>("/accounting/chart-of-accounts")).data,
});

const revenueAccountLabel = computed(() => {
  if (!item.value?.revenue_account_id) return null;
  const a = (accounts.value ?? []).find((a) => a.account_id === item.value!.revenue_account_id);
  return a ? `${a.code} · ${a.name}` : `#${item.value.revenue_account_id}`;
});

function typeSeverity(t: string) {
  switch (t) {
    case "FIXED_FEE": return "info";
    case "SERVICE": return "success";
    case "USAGE": return "warning";
    default: return undefined;
  }
}

function goEdit() {
  router.push({ name: "item-edit", params: { itemId: props.itemId } });
}
</script>

<template>
  <section>
    <div class="back">
      <Button label="Items" icon="pi pi-arrow-left" text size="small" @click="router.push('/items')" />
    </div>

    <header class="page-header">
      <div>
        <h1>{{ item?.name ?? (isLoading ? "Loading…" : "Item") }}</h1>
        <p v-if="item" class="subtitle tag-row">
          <Tag :value="item.item_type" :severity="typeSeverity(item.item_type)" />
          <Tag
            :value="item.active ? 'Active' : 'Inactive'"
            :severity="item.active ? 'success' : 'secondary'"
          />
        </p>
      </div>
      <div class="page-actions">
        <Button label="Edit" icon="pi pi-pencil" @click="goEdit" :disabled="!item" />
      </div>
    </header>

    <div v-if="error" class="error">Failed to load item.</div>

    <div v-if="item" class="card card-pad grid">
      <div class="field">
        <span>SKU</span>
        <p><code v-if="item.sku">{{ item.sku }}</code><span v-else class="muted">—</span></p>
      </div>
      <div class="field"><span>Type</span><p>{{ item.item_type }}</p></div>
      <div class="field"><span>Default currency</span><p><code>{{ item.default_currency }}</code></p></div>
      <div class="field">
        <span>Default unit price</span>
        <p>
          <span v-if="item.default_unit_price" class="num">
            {{ formatMoney(item.default_unit_price, item.default_currency) }}
          </span>
          <span v-else class="muted">—</span>
        </p>
      </div>
      <div class="field full">
        <span>Revenue account</span>
        <p>{{ revenueAccountLabel ?? "—" }}</p>
      </div>
      <div class="field full"><span>Description</span><p class="pre">{{ item.description || "—" }}</p></div>
      <div class="field"><span>Created</span><p class="muted small">{{ new Date(item.created_at).toLocaleString() }}</p></div>
    </div>
  </section>
</template>

<style scoped>
.back { margin-bottom: -0.5rem; }
.tag-row { display: flex; gap: 0.4rem; align-items: center; }
.grid { display: grid; grid-template-columns: 1fr 1fr; gap: 1rem 2rem; }
.field { display: flex; flex-direction: column; gap: 0.2rem; }
.field.full { grid-column: 1 / -1; }
.field > span { font-size: 0.72rem; font-weight: 600; color: var(--color-text-muted); text-transform: uppercase; letter-spacing: 0.05em; }
.field > p { margin: 0; font-size: 0.92rem; }
.pre { white-space: pre-wrap; }
.muted { color: var(--color-text-muted); }
.small { font-size: 0.82rem; }
.num { font-variant-numeric: tabular-nums; }
code { background: var(--color-surface-alt); padding: 0.1rem 0.4rem; border-radius: 4px; font-size: 0.85em; font-weight: 600; }
.error { color: #b91c1c; padding: 1rem; }
</style>
