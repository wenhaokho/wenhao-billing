<script setup lang="ts">
// Receivables aging (mockup): today's open AR, a stacked bar of the five aging
// buckets with their rows, and average days to pay over the selected period.
// Amounts are base-currency equivalents; Number() only scales the bar.
import { computed, ref } from "vue";
import EmptyState from "@/components/EmptyState.vue";
import { formatAmount } from "@/utils/money";
import { decIsZero } from "./format";
import type { AgingKey, DaysToPay, ReceivablesResponse } from "./types";

const props = defineProps<{
  data: ReceivablesResponse;
  /** From the overview; undefined while it loads. */
  daysToPay?: DaysToPay;
  /** "90 d", "12 mo": the period the days-to-pay average covers. */
  periodWords: string;
}>();

const LABELS: Record<AgingKey, string> = {
  not_due: "Not yet due",
  d1_30: "1–30 days",
  d31_60: "31–60 days",
  d61_90: "61–90 days",
  d90_plus: "90+ days",
};
const COLORS: Record<AgingKey, string> = {
  not_due: "var(--wb-green-fg)",
  d1_30: "var(--wb-red)",
  d31_60: "var(--wb-red-soft)",
  d61_90: "var(--wb-amber-fg)",
  d90_plus: "var(--wb-ink)",
};

const base = computed(() => props.data.base_currency);
const total = computed(() => Number(props.data.open_ar));

const buckets = computed(() =>
  props.data.aging.map((b) => {
    const zero = decIsZero(b.amount);
    const share = total.value > 0 ? Math.round((Number(b.amount) / total.value) * 100) : 0;
    return {
      key: b.key,
      label: LABELS[b.key] ?? b.key,
      color: COLORS[b.key] ?? "var(--wb-muted-soft)",
      zero,
      flex: zero ? 0 : Number(b.amount),
      count: b.count ? `${b.count} inv.` : "—",
      amount: zero ? "—" : formatAmount(b.amount, base.value),
      overdue: b.key !== "not_due" && !zero,
      tip: zero && !b.count
        ? `${LABELS[b.key]} · nothing outstanding`
        : `${LABELS[b.key]} · ${formatAmount(b.amount, base.value)} ${base.value} · ${b.count} invoice${b.count === 1 ? "" : "s"}${total.value > 0 ? ` · ${share}%` : ""}`,
    };
  }),
);

const hovered = ref<AgingKey | null>(null);
const tip = computed(() => buckets.value.find((b) => b.key === hovered.value)?.tip ?? "");

const summary = computed(() => {
  const d = props.data;
  const inv = `${d.invoice_count} invoice${d.invoice_count === 1 ? "" : "s"}`;
  const cus = `${d.customer_count} customer${d.customer_count === 1 ? "" : "s"}`;
  return `${inv} · ${cus}`;
});

const pastDue = computed(() => {
  const p = props.data.past_due_share;
  if (p === null) return null;
  return p > 0 && p < 1 ? "<1%" : `${Math.round(p)}%`;
});

// Days to pay: fewer days is better, so a drop reads green.
const dtp = computed(() => {
  const d = props.daysToPay;
  if (!d || d.avg === null) return { value: "—", delta: "", tone: "" };
  const value = d.avg.toFixed(1);
  if (d.previous_avg === null) return { value, delta: "", tone: "" };
  const diff = Math.round((d.avg - d.previous_avg) * 10) / 10;
  if (diff === 0) return { value, delta: "0.0", tone: "" };
  return { value, delta: `${diff < 0 ? "▼" : "▲"} ${Math.abs(diff).toFixed(1)}`, tone: diff < 0 ? "good" : "bad" };
});
</script>

<template>
  <div class="card dc aging" data-testid="aging-card">
    <h2 class="dc-title">Receivables aging</h2>
    <p class="dc-sub">Open balances by days past due.</p>

    <EmptyState
      v-if="!data.invoice_count"
      icon="circle-check"
      title="Nothing open"
      message="Every issued invoice is paid."
    />

    <template v-else>
      <div class="aging-head">
        <div class="dc-label">Open AR · {{ base }} eq.</div>
        <div class="aging-total num" :title="`${formatAmount(data.open_ar, base)} ${base}`">{{ formatAmount(data.open_ar, base) }}</div>
        <div class="aging-line">
          <template v-if="pastDue"><span class="past-due" :class="{ bad: pastDue !== '0%' }">{{ pastDue }}</span> is past due · </template>{{ summary }}
        </div>
      </div>

      <div class="aging-bar-wrap">
        <div v-if="tip" class="dc-tip" role="status">{{ tip }}</div>
        <div class="aging-bar" aria-hidden="true">
          <div
            v-for="b in buckets"
            :key="b.key"
            class="seg"
            :class="{ dim: hovered && hovered !== b.key }"
            :style="{ flex: b.flex, minWidth: b.zero ? '3px' : '0', background: b.color }"
            @mouseenter="hovered = b.key"
            @mouseleave="hovered = null"
          />
        </div>
      </div>

      <ul class="aging-rows">
        <li
          v-for="b in buckets"
          :key="b.key"
          class="aging-row"
          :class="{ on: hovered === b.key }"
          :data-bucket="b.key"
          @mouseenter="hovered = b.key"
          @mouseleave="hovered = null"
        >
          <span class="swatch" :style="{ background: b.color }" />
          <span>{{ b.label }}</span>
          <span class="aging-count num">{{ b.count }}</span>
          <span class="aging-amt num" :class="{ bad: b.overdue }">{{ b.amount }}</span>
        </li>
      </ul>
      <p v-if="data.unconverted.length" class="dc-note">
        Not converted: {{ data.unconverted.join(", ") }} (no FX rate)
      </p>
    </template>

    <div class="dtp" data-testid="days-to-pay">
      <span class="dtp-label">Avg days to pay · {{ periodWords }}</span>
      <span class="dtp-value num">{{ dtp.value }}<span v-if="dtp.delta" class="dtp-delta" :class="dtp.tone">{{ dtp.delta }}</span></span>
    </div>
  </div>
</template>

<style scoped src="./dash-card.css"></style>
<style scoped>
.aging-head { margin-top: 24px; }
.aging-total { font: 500 34px/1.1 var(--wb-font-display); letter-spacing: -1.5px; margin-top: 6px; color: var(--wb-ink); overflow-wrap: anywhere; }
.aging-line { font-size: 13px; color: var(--wb-muted); margin-top: 4px; }
.past-due { font-weight: 500; }
.aging-bar-wrap { position: relative; margin-top: 30px; }
.aging-bar { display: flex; height: 12px; gap: 2px; }
.seg { transition: opacity 0.15s; }
.seg.dim { opacity: 0.3; }
.aging-rows { list-style: none; padding: 0; margin: 14px 0 0; }
.aging-row {
  display: grid;
  grid-template-columns: 12px minmax(0, 1fr) auto auto;
  gap: 12px;
  align-items: center;
  padding: 9px 8px;
  margin: 0 -8px;
  border-bottom: 1px solid var(--wb-line);
  font-size: 13.5px;
  transition: background 0.15s;
}
.aging-row.on { background: var(--wb-surface-2); }
.aging-count { color: var(--wb-muted-soft); font-size: 12px; white-space: nowrap; }
.aging-amt { text-align: right; min-width: 90px; color: var(--wb-ink); white-space: nowrap; }
.aging-amt.bad { color: var(--wb-red-fg); }
.dtp {
  margin-top: auto;
  padding-top: 20px;
  display: flex;
  justify-content: space-between;
  align-items: baseline;
  gap: 12px;
}
.dtp-label { font-size: 13px; color: var(--wb-muted); }
.dtp-value { font: 500 22px var(--wb-font-display); letter-spacing: -0.6px; white-space: nowrap; color: var(--wb-ink); }
.dtp-delta { font: 400 13px var(--wb-font-sans); letter-spacing: 0; margin-left: 6px; color: var(--wb-muted); }
.dtp-delta.good { color: var(--wb-green-fg); }
.dtp-delta.bad { color: var(--wb-red-fg); }
@media (max-width: 640px) {
  .aging-total { font-size: 28px; }
  .aging-amt { min-width: 0; }
}
</style>
