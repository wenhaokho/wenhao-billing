<script setup lang="ts">
import { computed } from "vue";
import Button from "primevue/button";
import Dropdown from "primevue/dropdown";
import InputText from "primevue/inputtext";
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
        <Dropdown
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
        <InputText
          v-model="row.description"
          placeholder="Description"
          class="desc"
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
          icon="pi pi-trash"
          text
          rounded
          severity="danger"
          title="Remove"
          :disabled="rows.length <= 1"
          @click="emit('remove', idx)"
        />
      </div>
    </div>

    <Button
      v-if="!readOnly"
      label="Add an item"
      icon="pi pi-plus"
      text
      class="add-btn"
      @click="emit('add')"
    />
  </div>
</template>

<style scoped>
.card {
  background: var(--color-surface, #ffffff);
  border: 1px solid var(--color-border, #e2e8f0);
  border-radius: 12px;
  padding: 0.25rem 0;
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.04);
  overflow: hidden;
}
.lines-header,
.line-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 110px 140px 140px 40px;
  gap: 0.75rem;
  align-items: center;
  padding: 0.75rem 1.25rem;
}
.line-row {
  align-items: start;
}
.lines-header {
  background: #f1f5f9;
  color: #64748b;
  font-size: 0.7rem;
  font-weight: 600;
  letter-spacing: 0.08em;
  text-transform: uppercase;
  padding-top: 0.6rem;
  padding-bottom: 0.6rem;
}
.lines-header .col-qty,
.lines-header .col-price,
.lines-header .col-amount {
  text-align: right;
  padding-right: 0.6rem;
}
.line-row + .line-row,
.line-row {
  border-top: 1px solid #f1f5f9;
}
.line-row:first-of-type {
  border-top: none;
}
.col-item { display: flex; flex-direction: column; gap: 0.4rem; min-width: 0; }
.item-pill { align-self: flex-start; }
.desc { width: 100%; }
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
  font-weight: 600;
  color: #0f172a;
}
.col-action {
  display: flex;
  justify-content: flex-end;
  align-items: center;
  padding-top: 0.25rem;
}
.num { font-variant-numeric: tabular-nums; }
.add-btn {
  margin: 0.25rem 0.5rem 0.5rem;
  justify-content: flex-start;
}
</style>
