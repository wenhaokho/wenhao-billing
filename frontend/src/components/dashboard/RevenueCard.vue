<script setup lang="ts">
// Revenue card (mockup): twelve months of invoiced revenue. "All" stacks every
// currency converted to the base currency; a currency tab shows that currency's
// native amounts alone. Each bar is paid (solid) under unpaid (hatched) of the
// same colour; paid = total − unpaid, computed exactly on the decimal strings.
import { computed, defineAsyncComponent, ref, watch } from "vue";
import EmptyState from "@/components/EmptyState.vue";
import { formatAmount } from "@/utils/money";
import { currencySlot, withAlpha, type ChartColors } from "@/theme/chartColors";
import { decIsZero, decSub, decSum, formatCompact, formatShare, monthLong, monthShort } from "./format";
import type { MonthlyRevenueResponse } from "./types";

const Chart = defineAsyncComponent(() => import("primevue/chart"));

const props = defineProps<{ data: MonthlyRevenueResponse; colors: ChartColors }>();

const base = computed(() => props.data.base_currency);
const months = computed(() => props.data.months);
const converted = computed(() => new Set(props.data.currencies));

function colorFor(ccy: string): string {
  return props.colors.series[currencySlot(ccy)];
}

// Every currency invoiced in the window, converted or not (a native tab needs
// no rate): the base currency first, then alphabetical.
const allCurrencies = computed(() => {
  const set = new Set<string>();
  for (const m of months.value) {
    for (const [c, v] of Object.entries(m.native_totals)) if (!decIsZero(v)) set.add(c);
  }
  return [...set].sort((a, b) => Number(b === base.value) - Number(a === base.value) || a.localeCompare(b));
});

// The stacked view only has the converted currencies, base first so it sits at the bottom.
const stackedCurrencies = computed(() => allCurrencies.value.filter((c) => converted.value.has(c)));

const selected = ref<string>("all");
watch(allCurrencies, (list) => {
  if (selected.value !== "all" && !list.includes(selected.value)) selected.value = "all";
});

const tabs = computed(() => [
  { key: "all", label: `All · ${base.value} eq.` },
  ...allCurrencies.value.map((c) => ({ key: c, label: c })),
]);

const subtitle = computed(() =>
  selected.value === "all"
    ? `Invoiced per month, each currency converted to ${base.value} and stacked. Hatched = still unpaid.`
    : `Invoiced per month in ${selected.value} — native amounts, not converted. Hatched = still unpaid.`,
);

// Per-currency totals over the twelve months, for the legend above the chart.
const legend = computed(() => {
  const list = selected.value === "all" ? allCurrencies.value : [selected.value];
  return list.map((c) => {
    const native = decSum(months.value.map((m) => m.native_totals[c]));
    const nativeUnpaid = decSum(months.value.map((m) => m.native_unpaid_totals[c]));
    const unpaidPart = decIsZero(nativeUnpaid) ? "" : `${formatAmount(nativeUnpaid, c)} unpaid`;
    let conv: string;
    if (c === base.value) conv = unpaidPart || "all paid";
    else if (!converted.value.has(c)) conv = `no FX rate to ${base.value}`;
    else {
      const baseTotal = decSum(months.value.map((m) => m.totals[c]));
      conv = `≈ ${formatCompact(baseTotal)} ${base.value}${unpaidPart ? ` · ${unpaidPart}` : ""}`;
    }
    return { ccy: c, color: colorFor(c), native: `${formatAmount(native, c)} ${c}`, conv };
  });
});

// Diagonal hatch in the currency's colour over a faint wash of it, for the
// unpaid segment. Falls back to a plain faded fill without a 2D context.
function hatch(color: string): CanvasPattern | string {
  const size = 6;
  const cnv = document.createElement("canvas");
  cnv.width = size;
  cnv.height = size;
  const ctx = cnv.getContext("2d");
  if (!ctx) return withAlpha(color, props.colors.unpaidAlpha);
  ctx.fillStyle = withAlpha(color, 0.12);
  ctx.fillRect(0, 0, size, size);
  ctx.strokeStyle = color;
  ctx.lineWidth = 1.5;
  ctx.beginPath();
  // One stripe plus its wrap-around corners, so tiles join seamlessly.
  ctx.moveTo(0, size);
  ctx.lineTo(size, 0);
  ctx.moveTo(-1, 1);
  ctx.lineTo(1, -1);
  ctx.moveTo(size - 1, size + 1);
  ctx.lineTo(size + 1, size - 1);
  ctx.stroke();
  return ctx.createPattern(cnv, "repeat") ?? withAlpha(color, props.colors.unpaidAlpha);
}

interface Segment {
  label: string;
  currency: string;
  unpaid: boolean;
  backgroundColor: string | CanvasPattern;
  borderColor: string;
  borderWidth: number;
  maxBarThickness: number;
  data: number[];
  stack: string;
}

function segment(ccy: string, unpaid: boolean, values: string[]): Segment {
  const color = colorFor(ccy);
  return {
    label: unpaid ? `${ccy} unpaid` : ccy,
    currency: ccy,
    unpaid,
    backgroundColor: unpaid ? hatch(color) : color,
    borderColor: color,
    borderWidth: unpaid ? 1 : 0,
    maxBarThickness: 28,
    // Number() only scales the bar for plotting; the values were split exactly.
    data: values.map((v) => Number(v)),
    stack: "revenue",
  };
}

const chartData = computed(() => {
  const ms = months.value;
  const labels = ms.map((m, i) => (i === 0 || m.month.endsWith("-01") ? [monthShort(m.month), m.month.slice(0, 4)] : monthShort(m.month)));
  if (selected.value === "all") {
    const ccys = stackedCurrencies.value;
    const paid = ccys.map((c) => segment(c, false, ms.map((m) => decSub(m.totals[c], m.unpaid_totals[c]))));
    const unpaid = ccys.map((c) => segment(c, true, ms.map((m) => m.unpaid_totals[c] ?? "0")));
    return { labels, datasets: [...paid, ...unpaid] };
  }
  const c = selected.value;
  return {
    labels,
    datasets: [
      segment(c, false, ms.map((m) => decSub(m.native_totals[c], m.native_unpaid_totals[c]))),
      segment(c, true, ms.map((m) => m.native_unpaid_totals[c] ?? "0")),
    ],
  };
});

function cssToken(name: string): string {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
}

type TooltipItem = { dataset: { currency?: string; unpaid?: boolean }; parsed: { y: number }; dataIndex: number };

const chartOptions = computed(() => {
  const c = props.colors;
  const onInk = cssToken("--wb-bg");
  const all = selected.value === "all";
  // The unit every plotted value is in: the base currency, or the tab's own currency.
  const unit = all ? base.value : selected.value;
  const money = (n: number) => `${formatAmount(String(n), unit)} ${unit}`;
  return {
    maintainAspectRatio: false,
    responsive: true,
    interaction: { mode: "index" as const, intersect: false },
    plugins: {
      legend: { display: false },
      tooltip: {
        backgroundColor: c.text,
        titleColor: onInk,
        bodyColor: onInk,
        footerColor: onInk,
        cornerRadius: 0,
        titleFont: { family: "DM Sans", weight: 500 },
        bodyFont: { family: "DM Sans" },
        footerFont: { family: "DM Sans", weight: 500 },
        filter: (ctx: { parsed: { y: number } }) => ctx.parsed.y !== 0,
        callbacks: {
          title: (items: { dataIndex: number }[]) =>
            items.length ? monthLong(months.value[items[0].dataIndex]?.month ?? "") : "",
          label: (ctx: TooltipItem) => {
            const state = ctx.dataset.unpaid ? "unpaid" : "paid";
            if (!all) return `${state[0].toUpperCase()}${state.slice(1)}: ${money(ctx.parsed.y)}`;
            const ccy = ctx.dataset.currency ?? "";
            // The base-currency series is already in the base — "= … IDR" would repeat it.
            if (ccy === base.value) return `${ccy} ${state}: ${money(ctx.parsed.y)}`;
            const m = months.value[ctx.dataIndex];
            const native = ctx.dataset.unpaid
              ? (m?.native_unpaid_totals[ccy] ?? "0")
              : decSub(m?.native_totals[ccy], m?.native_unpaid_totals[ccy]);
            return `${ccy} ${state}: ${formatAmount(native, ccy)} ${ccy} = ${money(ctx.parsed.y)}`;
          },
          footer: (items: TooltipItem[]) => {
            if (!items.length) return "";
            // Display-only sum of the plotted values already shown above it.
            const sum = (rows: TooltipItem[]) => rows.reduce((acc, i) => acc + i.parsed.y, 0);
            const unpaid = sum(items.filter((i) => i.dataset.unpaid));
            const lines = [`Total: ${money(sum(items))}`];
            if (unpaid && all) lines.push(`Unpaid: ${money(unpaid)}`);
            return lines;
          },
        },
      },
    },
    scales: {
      x: {
        stacked: true,
        grid: { display: false },
        border: { color: c.grid },
        ticks: { color: c.text, font: { family: "DM Sans", size: 11 }, maxRotation: 0, autoSkipPadding: 8 },
      },
      y: {
        stacked: true,
        beginAtZero: true,
        grid: { color: c.grid },
        border: { display: false },
        ticks: {
          color: c.text,
          font: { family: "DM Sans", size: 11 },
          maxTicksLimit: 6,
          callback: (v: number | string) => formatCompact(Number(v)),
        },
      },
    },
  };
});

const keyItems = computed(() => (selected.value === "all" ? stackedCurrencies.value : [selected.value]));

const chartLabel = computed(() =>
  selected.value === "all"
    ? `Bar chart of invoiced revenue per month over the last 12 months, stacked by currency, converted to ${base.value}; unpaid parts hatched.`
    : `Bar chart of invoiced revenue in ${selected.value} per month over the last 12 months, native amounts; unpaid parts hatched.`,
);

const foreignCurrencies = computed(() => props.data.currencies.filter((c) => c !== base.value));
const footer = computed(() => props.data.footer);
// The footer total covers the twelve calendar months shown, not the KPI row's
// rolling year, so name its span: "Dec 2025 – Nov 2026".
const footerSpan = computed(() => {
  const ms = months.value;
  if (!ms.length) return "last 12 months";
  return `${monthLong(ms[0].month)} – ${monthLong(ms[ms.length - 1].month)}`;
});
</script>

<template>
  <div class="card rev" data-testid="revenue-card">
    <div class="rev-head">
      <div>
        <h2 class="rev-title">Revenue — last 12 months</h2>
        <p class="rev-sub">{{ subtitle }}</p>
      </div>
      <div v-if="allCurrencies.length" class="ccy-tabs" role="group" aria-label="Currency">
        <button
          v-for="t in tabs"
          :key="t.key"
          type="button"
          class="ccy-tab"
          :class="{ on: selected === t.key }"
          :aria-pressed="selected === t.key"
          :data-ccy="t.key"
          @click="selected = t.key"
        >{{ t.label }}</button>
      </div>
    </div>

    <EmptyState
      v-if="!allCurrencies.length"
      icon="chart-column"
      title="No invoices in the last 12 months"
      message="Issued invoices show here by month and currency."
    />

    <template v-else>
      <div class="rev-legend">
        <div v-for="l in legend" :key="l.ccy" class="leg">
          <span class="swatch" :style="{ background: l.color }" />
          <div>
            <div class="leg-ccy">{{ l.ccy }}</div>
            <div class="leg-native num">{{ l.native }}</div>
            <div class="leg-conv num">{{ l.conv }}</div>
          </div>
        </div>
      </div>

      <div v-if="selected === 'all' && !stackedCurrencies.length" class="rev-none">
        No currency here has an FX rate to {{ base }} yet; pick a currency to see its native amounts.
      </div>
      <div v-else class="rev-chart" data-testid="revenue-chart">
        <Chart
          type="bar"
          :data="chartData"
          :options="chartOptions"
          :canvas-props="{ role: 'img', 'aria-label': chartLabel }"
        />
      </div>

      <div class="rev-key">
        <span v-for="c in keyItems" :key="c" class="key"><span class="swatch" :style="{ background: colorFor(c) }" />{{ c }}</span>
        <span class="key"><span class="swatch hatch" />Unpaid</span>
      </div>
    </template>

    <p v-if="data.unconverted.length" class="rev-note">
      Not converted: {{ data.unconverted.join(", ") }} (no FX rate)
    </p>

    <div class="rev-foot">
      <div>
        <div class="foot-label" :title="`Invoiced in ${footerSpan}, ${base} equivalent`">Invoiced · {{ footerSpan }}</div>
        <div class="foot-value num">{{ formatCompact(footer.invoiced_base) }}</div>
      </div>
      <div>
        <div class="foot-label">Foreign share</div>
        <div class="foot-value num">
          {{ formatShare(footer.foreign_share) }}
          <span v-if="foreignCurrencies.length" class="foot-aside">{{ foreignCurrencies.join(" · ") }}</span>
        </div>
      </div>
      <div>
        <div class="foot-label">Best month</div>
        <div class="foot-value num">
          <template v-if="footer.best_month">{{ monthShort(footer.best_month.month) }} · {{ formatCompact(footer.best_month.amount) }}</template>
          <template v-else>—</template>
        </div>
      </div>
      <div>
        <div class="foot-label">FX exposure</div>
        <div class="foot-value num">
          {{ formatCompact(footer.fx_exposure) }} <span class="foot-aside">{{ base }} eq. open in foreign currency</span>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.rev { padding: 28px 28px 24px; display: flex; flex-direction: column; min-width: 0; }
.num { font-variant-numeric: tabular-nums; }
.rev-head { display: flex; justify-content: space-between; gap: 16px; flex-wrap: wrap; align-items: flex-start; }
.rev-title { font: 500 20px/1.2 var(--wb-font-display); letter-spacing: -0.5px; color: var(--wb-ink); margin: 0; }
.rev-sub { margin: 4px 0 0; color: var(--wb-muted); font-size: 13px; line-height: 1.5; }
.ccy-tabs { display: flex; flex-wrap: wrap; border: 1px solid var(--wb-line); border-radius: 30px; padding: 3px; }
.ccy-tab {
  height: 30px;
  padding: 0 14px;
  border-radius: 30px;
  border: 0;
  background: transparent;
  color: var(--wb-muted);
  font: 500 12.5px var(--wb-font-sans);
  cursor: pointer;
  white-space: nowrap;
  transition: background 0.15s, color 0.15s;
}
.ccy-tab:hover:not(.on) { color: var(--wb-ink); }
.ccy-tab.on { background: var(--wb-ink); color: var(--wb-bg); }
.ccy-tab:focus-visible { outline: none; box-shadow: var(--focus-ring); }

.rev-legend { display: flex; flex-wrap: wrap; gap: 16px 28px; margin-top: 22px; }
.leg { display: flex; gap: 10px; align-items: flex-start; min-width: 0; }
.swatch { width: 10px; height: 10px; flex: none; display: inline-block; }
.leg .swatch { margin-top: 3px; }
.leg-ccy { font: 500 11px var(--wb-font-sans); letter-spacing: 1.2px; text-transform: uppercase; color: var(--wb-muted-soft); }
.leg-native { font: 500 17px var(--wb-font-display); letter-spacing: -0.5px; margin-top: 3px; color: var(--wb-ink); white-space: nowrap; }
.leg-conv { font-size: 12px; color: var(--wb-muted); margin-top: 2px; }

.rev-chart { position: relative; flex: 1 1 300px; min-height: 300px; margin-top: 22px; }
.rev-chart :deep(.p-chart),
.rev-chart :deep(.p-chart > *) { height: 100%; width: 100%; }
.rev-chart :deep(canvas) { height: 100% !important; width: 100% !important; }
.rev-none { margin-top: 22px; padding: 2rem 0; color: var(--wb-muted); font-size: 13px; text-align: center; }

.rev-key { display: flex; flex-wrap: wrap; gap: 8px 18px; margin-top: 18px; font-size: 12px; color: var(--wb-muted); }
.key { display: inline-flex; align-items: center; gap: 6px; }
.swatch.hatch {
  background: repeating-linear-gradient(135deg, var(--wb-muted-soft) 0 1.5px, transparent 1.5px 4px);
  box-shadow: inset 0 0 0 1px var(--wb-muted-soft);
}
.rev-note { margin: 14px 0 0; color: var(--wb-muted-soft); font-size: 12px; }

.rev-foot {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
  gap: 20px;
  margin-top: 24px;
  padding-top: 20px;
  border-top: 1px solid var(--wb-line);
}
.foot-label { font: 500 11px/1.4 var(--wb-font-sans); letter-spacing: 1.3px; text-transform: uppercase; color: var(--wb-muted-soft); }
.foot-value { font: 500 20px var(--wb-font-display); letter-spacing: -0.6px; margin-top: 6px; color: var(--wb-ink); }
.foot-aside { font-size: 13px; letter-spacing: 0; color: var(--wb-muted-soft); }

@media (max-width: 640px) {
  .rev { padding: 20px 18px 18px; }
  .rev-chart { min-height: 260px; flex-basis: 260px; }
  .rev-foot { grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 16px; }
  .foot-value { font-size: 18px; }
}
</style>
