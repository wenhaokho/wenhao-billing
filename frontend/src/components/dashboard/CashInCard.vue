<script setup lang="ts">
// Expected cash in (mockup): the next 90 days in three 30-day spans, each a bar
// of open invoices falling due (red) under scheduled recurring runs (gold).
// Overdue balances are left out and named below. Sums are exact decimal
// strings; Number() only scales the bars.
import { computed, ref } from "vue";
import EmptyState from "@/components/EmptyState.vue";
import { formatAmount } from "@/utils/money";
import { decIsZero, decSum, formatCompact } from "./format";
import type { CashInKey, ReceivablesResponse } from "./types";

const props = defineProps<{ data: ReceivablesResponse }>();

const LABELS: Record<CashInKey, string> = { d0_30: "0–30 d", d31_60: "31–60 d", d61_90: "61–90 d" };
const BAR_PX = 120;

const base = computed(() => props.data.base_currency);

const totals = computed(() => props.data.cash_in.map((c) => decSum([c.open_invoices, c.recurring])));
const max = computed(() => Math.max(0, ...totals.value.map(Number)));
const empty = computed(() => totals.value.every((t) => decIsZero(t)));

const px = (v: string) => (max.value > 0 ? Math.round((Number(v) / max.value) * BAR_PX) : 0);

const cols = computed(() =>
  props.data.cash_in.map((c, i) => ({
    key: c.key,
    label: LABELS[c.key] ?? c.key,
    total: formatCompact(totals.value[i]),
    title: `${formatAmount(totals.value[i], base.value)} ${base.value}`,
    estimate: c.estimate,
    arH: px(c.open_invoices),
    recH: px(c.recurring),
    arTip: `Open invoices · ${formatAmount(c.open_invoices, base.value)} ${base.value}`,
    recTip: `Recurring runs · ${formatAmount(c.recurring, base.value)} ${base.value}${c.estimate ? " (includes usage estimates)" : ""}`,
  })),
);

// "d0_30:ar" / "d0_30:rec" while a segment is hovered.
const hovered = ref("");
</script>

<template>
  <div class="card dc" data-testid="cash-in-card">
    <h2 class="dc-title">Expected cash in</h2>
    <p class="dc-sub">
      Open invoices by due date plus every scheduled recurring run, counted on its generation date, next 90 days.
    </p>

    <EmptyState
      v-if="empty"
      icon="calendar"
      title="Nothing expected"
      message="No open invoice falls due and no recurring run is scheduled in the next 90 days."
    />

    <template v-else>
      <div class="cash-cols">
        <div v-for="c in cols" :key="c.key" class="cash-col" :data-span="c.key">
          <div class="cash-total num" :title="c.title">{{ c.total }}<span v-if="c.estimate" class="dc-aside"> est.</span></div>
          <div class="cash-bar" aria-hidden="true">
            <div v-if="hovered.startsWith(c.key + ':')" class="dc-tip">{{ hovered.endsWith(':ar') ? c.arTip : c.recTip }}</div>
            <div
              class="cash-seg ar"
              :class="{ dim: hovered && hovered !== c.key + ':ar' }"
              :style="{ height: c.arH + 'px' }"
              @mouseenter="hovered = c.key + ':ar'"
              @mouseleave="hovered = ''"
            />
            <div
              class="cash-seg rec"
              :class="{ dim: hovered && hovered !== c.key + ':rec' }"
              :style="{ height: c.recH + 'px' }"
              @mouseenter="hovered = c.key + ':rec'"
              @mouseleave="hovered = ''"
            />
          </div>
          <div class="cash-label">{{ c.label }}</div>
        </div>
      </div>
      <div class="cash-key">
        <span class="key"><span class="swatch" style="background: var(--wb-red)" />Open invoices</span>
        <span class="key"><span class="swatch" style="background: var(--wb-gold)" />Recurring runs</span>
      </div>
    </template>

    <p v-if="!decIsZero(data.overdue_excluded)" class="cash-overdue">
      Plus <strong class="bad num">{{ formatCompact(data.overdue_excluded) }} {{ base }} overdue</strong> not in forecast until collected.
    </p>
    <p v-if="data.unconverted.length" class="dc-note">
      Not converted: {{ data.unconverted.join(", ") }} (no FX rate)
    </p>
  </div>
</template>

<style scoped src="./dash-card.css"></style>
<style scoped>
.cash-cols { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 14px; margin-top: 24px; align-items: end; }
.cash-col { min-width: 0; }
.cash-total { font: 500 17px var(--wb-font-display); letter-spacing: -0.5px; color: var(--wb-ink); white-space: nowrap; }
.cash-bar { position: relative; display: flex; flex-direction: column-reverse; height: 120px; margin-top: 10px; }
.cash-bar .dc-tip { white-space: normal; width: max-content; max-width: 200px; }
.cash-col:last-child .dc-tip { left: auto; right: 0; }
.cash-seg { transition: opacity 0.15s; }
.cash-seg.ar { background: var(--wb-red); }
.cash-seg.rec { background: var(--wb-gold); }
.cash-seg.dim { opacity: 0.35; }
.cash-label { font-size: 12px; color: var(--wb-muted-soft); margin-top: 8px; letter-spacing: 1px; text-transform: uppercase; white-space: nowrap; }
.cash-key {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 16px;
  font-size: 12px;
  color: var(--wb-muted);
  margin-top: 16px;
  padding-top: 16px;
  border-top: 1px solid var(--wb-line);
}
.key { display: inline-flex; align-items: center; gap: 6px; white-space: nowrap; }
.cash-overdue { margin: 14px 0 0; font-size: 12.5px; color: var(--wb-muted); line-height: 1.5; }
.cash-overdue strong { font-weight: 500; }
</style>
