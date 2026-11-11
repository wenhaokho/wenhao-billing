<script setup lang="ts">
import AppIcon from "@/components/AppIcon.vue";
import { computed } from "vue";
import Button from "primevue/button";
import Select from "primevue/select";
import Textarea from "primevue/textarea";
import InputNumber from "primevue/inputnumber";
import Tag from "primevue/tag";
import { formatAmount } from "@/utils/money";
import type { LineItemRow } from "@/composables/useInvoiceForm";

interface CatalogItem {
  item_id: string;
  name: string;
  description: string | null;
  default_unit_price: string | null;
}

const props = defineProps<{
  modelValue: LineItemRow[];
  catalog: CatalogItem[];
  currency: string;
  readOnly?: boolean;
}>();

const emit = defineEmits<{
  (e: "update:modelValue", v: LineItemRow[]): void;
  (e: "add"): void;
  (e: "remove", index: number): void;
}>();

const rows = computed({
  get: () => props.modelValue,
  set: (v) => emit("update:modelValue", v),
});

const priceFractionDigits = computed(() => (props.currency === "IDR" ? 0 : 2));

function onItemPicked(row: LineItemRow, value: string | null) {
  row.item_id = value;
  if (!value) return;
  const picked = props.catalog.find((c) => c.item_id === value);
  if (!picked) return;
  if (!row.description) row.description = picked.description ?? picked.name;
  if (row.unit_price == null && picked.default_unit_price != null) {
    row.unit_price = Number(picked.default_unit_price);
  }
}

function catalogNameFor(row: LineItemRow): string | null {
  if (!row.item_id) return null;
  return props.catalog.find((c) => c.item_id === row.item_id)?.name ?? null;
}

function rowAmount(row: LineItemRow): number {
  const q = Number(row.quantity ?? 0);
  const p = Number(row.unit_price ?? 0);
  if (!Number.isFinite(q) || !Number.isFinite(p)) return 0;
  return Math.round(q * p * 10000) / 10000;
}
</script>

<template>
  <div class="lines card">
    <div class="lines-header">
      <div class="col-item">Items</div>
      <div class="col-qty">Quantity</div>
      <div class="col-price">Price</div>
      <div class="col-amount">Amount</div>
      <div class="col-action"></div>
    </div>

    <div
      v-for="(row, idx) in rows"
      :key="idx"
      class="line-row"
    >
      <div class="col-item">
        <Tag
          v-if="readOnly && catalogNameFor(row)"
          :value="catalogNameFor(row)"
          severity="secondary"
          class="item-pill"
        />
        <Select
          v-else
          v-model="row.item_id"
          :options="catalog"
          option-label="name"
          option-value="item_id"
          placeholder="Pick an item (optional)"
          filter
          show-clear
          :disabled="readOnly"
          @update:model-value="(v) => onItemPicked(row, v as string | null)"
        />
        <Textarea
          v-model="row.description"
          placeholder="Description"
          class="desc"
          rows="3"
          auto-resize
          :disabled="readOnly"
        />
      </div>
      <div class="col-qty">
        <InputNumber
          v-model="row.quantity"
          mode="decimal"
          :min="0"
          :min-fraction-digits="0"
          :max-fraction-digits="4"
          :use-grouping="false"
          :disabled="readOnly"
        />
      </div>
      <div class="col-price">
        <InputNumber
          v-model="row.unit_price"
          mode="decimal"
          :min="0"
          :min-fraction-digits="priceFractionDigits"
          :max-fraction-digits="priceFractionDigits"
          :use-grouping="true"
          :disabled="readOnly"
        />
      </div>
      <div class="col-amount num">{{ formatAmount(rowAmount(row), currency) }}</div>
      <div class="col-action">
        <Button
          v-if="!readOnly"
          text
          rounded
          severity="danger"
          title="Remove"
          :disabled="rows.length <= 1"
          @click="emit('remove', idx)">
          <template #icon><AppIcon name="trash-2" /></template>
        </Button>
      </div>
    </div>

    <Button
      v-if="!readOnly"
      label="Add an item"
      text
      class="add-btn"
      @click="emit('add')">
      <template #icon><AppIcon name="plus" /></template>
    </Button>
  </div>
</template>

<style scoped>
/* Square bordered card with the table-header strip (mockup's invoice page). */
.lines.card {
  /* The rows carry their own padding (a parent's .card padding must not add to it). */
  padding: 0;
  overflow: hidden;
}
.lines-header,
.line-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 110px 160px 160px 40px;
  gap: 16px;
  align-items: center;
  padding: 16px 28px;
}
.line-row {
  align-items: start;
  padding-top: 20px;
  padding-bottom: 20px;
}
.lines-header {
  color: var(--wb-muted-soft);
  font: 500 11px/1.4 var(--wb-font-sans);
  letter-spacing: 1.2px;
  text-transform: uppercase;
  border-bottom: 1px solid var(--wb-line);
}
.lines-header .col-qty,
.lines-header .col-price,
.lines-header .col-amount {
  text-align: right;
  padding-right: 0.6rem;
}
.lines-header .col-amount { padding-top: 0; }
.line-row + .line-row {
  border-top: 1px solid var(--wb-line);
}
.col-item { display: flex; flex-direction: column; gap: 8px; min-width: 0; }
.item-pill { align-self: flex-start; }
.desc { width: 100%; resize: none; line-height: 1.5; }
.col-qty, .col-price { padding-top: 0; }
.col-qty :deep(.p-inputnumber),
.col-qty :deep(.p-inputnumber-input),
.col-price :deep(.p-inputnumber),
.col-price :deep(.p-inputnumber-input) {
  width: 100%;
}
.col-qty :deep(.p-inputnumber-input),
.col-price :deep(.p-inputnumber-input) {
  text-align: right;
  font-variant-numeric: tabular-nums;
}
.col-amount {
  text-align: right;
  padding: 0.6rem 0.6rem 0 0;
  font-variant-numeric: tabular-nums;
  font-weight: 500;
  color: var(--wb-ink);
  white-space: nowrap;
}
.col-action {
  display: flex;
  justify-content: flex-end;
  align-items: center;
  padding-top: 0.25rem;
}
.num { font-variant-numeric: tabular-nums; }
.add-btn {
  margin: 4px 16px 16px;
  justify-content: flex-start;
}
/* Phone: each line stacks into a small grid; the header strip would not line
   up, so each cell carries its own caption instead. */
@media (max-width: 720px) {
  .lines-header { display: none; }
  .line-row {
    grid-template-columns: 1fr 1fr;
    gap: 12px;
    padding: 16px 20px;
  }
  .col-item { grid-column: 1 / -1; }
  .line-row .col-qty::before,
  .line-row .col-price::before,
  .line-row .col-amount::before {
    display: block;
    margin-bottom: 6px;
    font: 500 11px/1.4 var(--wb-font-sans);
    letter-spacing: 1.2px;
    text-transform: uppercase;
    color: var(--wb-muted-soft);
    text-align: left;
  }
  .line-row .col-qty::before { content: "Quantity"; }
  .line-row .col-price::before { content: "Price"; }
  .line-row .col-amount::before { content: "Amount"; }
  .col-amount { padding: 0; text-align: left; }
  .col-action { justify-content: flex-end; align-items: flex-end; }
}
</style>
