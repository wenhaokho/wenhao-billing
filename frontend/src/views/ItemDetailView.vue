<script setup lang="ts">
import AppIcon from "@/components/AppIcon.vue";
import { computed } from "vue";
import { useRouter } from "vue-router";
import { useQuery } from "@tanstack/vue-query";
import Button from "primevue/button";
import Tag from "primevue/tag";
import StatusTag from "@/components/StatusTag.vue";
import PageHeader from "@/components/PageHeader.vue";
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
    case "USAGE": return "warn";
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
      <Button label="Items" text size="small" @click="router.push('/items')">
        <template #icon><AppIcon name="arrow-left" /></template>
      </Button>
    </div>

    <PageHeader eyebrow="04 / Accounting" :title="item?.name ?? (isLoading ? 'Loading…' : 'Item')">
      <template v-if="item" #status>
        <Tag :value="item.item_type" :severity="typeSeverity(item.item_type)" />
        <StatusTag :status="item.active ? 'ACTIVE' : 'INACTIVE'" />
      </template>
      <Button label="Edit" @click="goEdit" :disabled="!item">
        <template #icon><AppIcon name="pencil" /></template>
      </Button>
    </PageHeader>

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
.num { font-variant-numeric: tabular-nums; }
code { background: var(--wb-tint); padding: 2px 6px; font-size: 12px; font-weight: 500; }
.error { color: var(--wb-red-fg); padding: 16px 0; }
</style>
