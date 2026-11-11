<script setup lang="ts">
import AppIcon from "@/components/AppIcon.vue";
import StatusTag from "@/components/StatusTag.vue";
import PageHeader from "@/components/PageHeader.vue";
import { computed, defineAsyncComponent, ref, watch } from "vue";
import { useQuery } from "@tanstack/vue-query";
import { useRouter } from "vue-router";
import Tag from "primevue/tag";
import Button from "primevue/button";
import ProgressSpinner from "primevue/progressspinner";
const Chart = defineAsyncComponent(() => import("primevue/chart"));
import { api } from "@/api/client";
import { formatAmount } from "@/utils/money";
import { useTheme } from "@/composables/useTheme";
import { chartColors, currencySlot, withAlpha } from "@/theme/chartColors";

interface CurrencyAmount { currency: string; amount: string }
interface CurrencyBaseAmount { currency: string; base_amount: string }
interface UpcomingInvoiceItem {
  invoice_id: string;
  customer_id: string | null;
  customer_name: string | null;
  mode: "RECURRING" | "USAGE";
  next_date: string;
  amount: string;
  currency: string;
  amount_is_estimate: boolean;
}
interface ActivityItem {
  log_id: string;
  payment_id: string;
  action: string;
  reasons: string[];
  actor_user_id: string | null;
  created_at: string;
  payer_name: string | null;
  amount: string | null;
  currency: string | null;
  payment_date: string | null;
  payer_reference: string | null;
  wht_amount: string | null;
  confidence_score: string | null;
  intake_source: string | null;
  invoice_id: string | null;
  invoice_number: string | null;
  customer_name: string | null;
  actor_name: string | null;
}
interface DashboardStats {
  awaiting_review_count: number;
  draft_count: number;
  sent_count: number;
  customer_count: number;
  auto_cleared_last_30d: number;
  open_ar_by_currency: CurrencyAmount[];
  open_quotation_count: number;
  open_quotation_pipeline: CurrencyAmount[];
  recent_activity: ActivityItem[];
  mrr_by_currency: CurrencyAmount[];
  base_currency: string;
  open_ar_base_by_currency: CurrencyBaseAmount[];
  open_ar_unconverted: string[];
  upcoming_invoices: UpcomingInvoiceItem[];
}
interface MonthlyRevenueBucket {
  month: string;
  totals: Record<string, string>;         // base-currency (IDR) per source currency
  native_totals: Record<string, string>;  // original currency per source currency
  unpaid_totals: Record<string, string>;         // still-owed part of totals
  native_unpaid_totals: Record<string, string>;  // still-owed part of native_totals
}
interface MonthlyRevenueResponse {
  base_currency: string;
  currencies: string[];
  unconverted: string[];
  months: MonthlyRevenueBucket[];
}

const router = useRouter();

const { data, isLoading, isError, refetch } = useQuery<DashboardStats>({
  queryKey: ["dashboard-stats"],
  queryFn: async () => (await api.get<DashboardStats>("/stats/dashboard")).data,
  refetchInterval: 30_000,
});

const stats = computed(() => data.value);

const openQuotationCount = computed(() => stats.value?.open_quotation_count ?? 0);

const openQuotationHint = computed(() => {
  const list = stats.value?.open_quotation_pipeline ?? [];
  if (!list.length) return "No pipeline";
  const [top, ...rest] = list;
  const head = `${top.currency} ${formatAmount(top.amount, top.currency)}`;
  return rest.length ? `${head} +${rest.length} more` : head;
});

const { data: revenueData } = useQuery<MonthlyRevenueResponse>({
  queryKey: ["dashboard-revenue-monthly"],
  queryFn: async () => (await api.get<MonthlyRevenueResponse>("/stats/revenue-monthly")).data,
  refetchInterval: 60_000,
});

// Brand colours come from the CSS tokens. Re-read them after a theme switch
// has been applied to <html> (useTheme's own watcher runs first), so both
// charts rebuild in the new theme's colours.
const { theme } = useTheme();
const colors = ref(chartColors());
watch(theme, () => { colors.value = chartColors(); }, { flush: "post" });

// Colour follows the currency, never its position in the dataset (the revenue
// chart orders currencies one way, the AR pie by amount), so every chart on
// this page agrees: IDR red, SGD gold, USD blue, MYR green (mockup variant A).
function colorForCurrency(ccy: string): string {
  return colors.value.series[currencySlot(ccy)];
}

// The base currency first, so it sits at the bottom of every bar, as in the mockup.
const revenueCurrencies = computed(() => {
  const r = revenueData.value;
  if (!r) return [];
  return [...r.currencies].sort((a, b) => Number(b === r.base_currency) - Number(a === r.base_currency));
});

const chartData = computed(() => {
  const r = revenueData.value;
  if (!r) return { labels: [], datasets: [] };
  const labels = r.months.map((m, i) => monthLabel(m.month, i === 0));
  const ccys = revenueCurrencies.value;
  const paid = ccys.map((ccy) => ({
    label: ccy,
    currency: ccy,
    unpaid: false,
    backgroundColor: colorForCurrency(ccy),
    borderColor: colorForCurrency(ccy),
    borderWidth: 0,
    maxBarThickness: 28,
    data: r.months.map(
      (m) => Number(m.totals[ccy] ?? 0) - Number(m.unpaid_totals[ccy] ?? 0),
    ),
    stack: "revenue",
  }));
  // Same hue as the paid segment, faded, stacked above every paid segment so the
  // faded top of a bar reads as the whole unpaid portion of that month. The solid
  // outline keeps the pale fill visible against the card in both themes.
  const unpaid = ccys.map((ccy) => ({
    label: `${ccy} unpaid`,
    currency: ccy,
    unpaid: true,
    backgroundColor: withAlpha(colorForCurrency(ccy), colors.value.unpaidAlpha),
    borderColor: colorForCurrency(ccy),
    borderWidth: 1,
    maxBarThickness: 28,
    data: r.months.map((m) => Number(m.unpaid_totals[ccy] ?? 0)),
    stack: "revenue",
  }));
  return { labels, datasets: [...paid, ...unpaid] };
});

// Shared look: brand ink text, hairline grid, square ink tooltip (mockup).
const MONTHS = ["Jan","Feb","Mar","Apr","May","Jun","Jul","Aug","Sep","Oct","Nov","Dec"];

// Axis label for a "YYYY-MM" bucket: the short month, with the year under the
// first bar and under every January.
function monthLabel(ym: string, first: boolean): string | string[] {
  const [y, m] = ym.split("-");
  const name = MONTHS[Number(m) - 1] ?? ym;
  return first || m === "01" ? [name, y] : name;
}

function monthTitle(ym: string): string {
  const [y, m] = ym.split("-");
  return `${MONTHS[Number(m) - 1] ?? ym} ${y}`;
}

function cssToken(name: string): string {
  return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
}

const baseChartLook = computed(() => {
  const c = colors.value;
  const onInk = cssToken("--wb-bg");
  return {
    legend: {
      position: "bottom" as const,
      labels: { color: c.text, font: { family: "DM Sans", size: 12 }, boxWidth: 10, boxHeight: 10 },
    },
    tooltip: {
      backgroundColor: c.text,
      titleColor: onInk,
      bodyColor: onInk,
      footerColor: onInk,
      cornerRadius: 0,
      titleFont: { family: "DM Sans", weight: 500 },
      bodyFont: { family: "DM Sans" },
      footerFont: { family: "DM Sans", weight: 500 },
    },
  };
});

const chartOptions = computed(() => {
  const base = revenueData.value?.base_currency ?? "";
  const months = revenueData.value?.months ?? [];
  const c = colors.value;
  const look = baseChartLook.value;
  return {
  maintainAspectRatio: false,
  responsive: true,
  // One tooltip per bar: hovering anywhere over a month lists all its segments.
  interaction: { mode: "index" as const, intersect: false },
  plugins: {
    legend: look.legend,
    tooltip: {
      ...look.tooltip,
      // Most months have only some of the series; a month with none shows no tooltip.
      filter: (ctx: { parsed: { y: number } }) => ctx.parsed.y !== 0,
      callbacks: {
        title: (items: { dataIndex: number }[]) =>
          items.length ? monthTitle(months[items[0].dataIndex]?.month ?? "") : "",
        // Plotted value is the IDR-converted amount; show the native amount
        // alongside it (same shape as the open-AR pie tooltip).
        label: (ctx: {
          dataset: { currency?: string; unpaid?: boolean };
          parsed: { y: number };
          dataIndex: number;
        }) => {
          const ccy = ctx.dataset.currency ?? "";
          const state = ctx.dataset.unpaid ? "unpaid" : "paid";
          const converted = `${formatAmount(String(ctx.parsed.y), base)} ${base}`;
          // The base-currency series is already IDR — a "= … IDR" part would repeat it.
          if (ccy === base) return `${ccy} ${state}: ${converted}`;
          const m = months[ctx.dataIndex];
          const nativeUnpaid = Number(m?.native_unpaid_totals?.[ccy] ?? 0);
          const native = ctx.dataset.unpaid
            ? nativeUnpaid
            : Number(m?.native_totals?.[ccy] ?? 0) - nativeUnpaid;
          return `${ccy} ${state}: ${formatAmount(String(native), ccy)} ${ccy} = ${converted}`;
        },
        footer: (items: { dataset: { unpaid?: boolean }; parsed: { y: number } }[]) => {
          if (!items.length) return "";
          const sum = (rows: typeof items) => rows.reduce((acc, i) => acc + i.parsed.y, 0);
          const unpaid = sum(items.filter((i) => i.dataset.unpaid));
          const lines = [`Total: ${formatAmount(String(sum(items)), base)} ${base}`];
          if (unpaid) lines.push(`Unpaid: ${formatAmount(String(unpaid), base)} ${base}`);
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
        callback: (v: number | string) => {
          const n = Number(v);
          // One decimal, but drop a trailing ".0" (1800.0M -> 1.8B, 600.0M -> 600M).
          const compact = (val: number, suffix: string) =>
            (Number.isInteger(val) ? val.toFixed(0) : val.toFixed(1)) + suffix;
          if (n >= 1_000_000_000) return compact(n / 1_000_000_000, "B");
          if (n >= 1_000_000) return compact(n / 1_000_000, "M");
          if (n >= 1_000) return compact(n / 1_000, "K");
          return String(n);
        },
      },
    },
  },
  };
});

const hasRevenueData = computed(
  () => (revenueData.value?.currencies.length ?? 0) > 0,
);

// Accessible names for the chart canvases (role="img"): what the chart shows;
// the visible list beside the pie carries the figures.
const revenueChartLabel = computed(
  () =>
    `Bar chart of invoiced revenue per month over the last 12 months, stacked by currency, converted to ${revenueData.value?.base_currency ?? "IDR"}.`,
);
const arChartLabel = computed(() => {
  const ccys = (stats.value?.open_ar_base_by_currency ?? []).map((r) => r.currency).join(", ");
  return `Pie chart of open receivables by currency (${ccys}), converted to ${stats.value?.base_currency ?? ""}.`;
});

const arPieData = computed(() => {
  const rows = stats.value?.open_ar_base_by_currency ?? [];
  // colorForCurrency reads colors.value, so the pie also rebuilds on a theme
  // switch, and by then the surface token holds the new theme's value.
  const backgroundColor = rows.map((r) => colorForCurrency(r.currency));
  const surface = cssToken("--wb-surface");
  return {
    labels: rows.map((r) => r.currency),
    datasets: [
      {
        data: rows.map((r) => Number(r.base_amount)),
        backgroundColor,
        borderColor: surface,
        // A hairline between slices; a single slice would show it as a seam.
        borderWidth: rows.length > 1 ? 2 : 0,
      },
    ],
  };
});

const hasArPie = computed(
  () => (stats.value?.open_ar_base_by_currency.length ?? 0) > 0,
);

// Native (original-currency) AR total per currency, for the pie tooltip.
const arNativeByCcy = computed<Record<string, string>>(() => {
  const m: Record<string, string> = {};
  for (const r of stats.value?.open_ar_by_currency ?? []) m[r.currency] = r.amount;
  return m;
});

const arPieOptions = computed(() => {
  const base = stats.value?.base_currency ?? "";
  const look = baseChartLook.value;
  return {
    maintainAspectRatio: false,
    responsive: true,
    plugins: {
      legend: look.legend,
      tooltip: {
        ...look.tooltip,
        callbacks: {
          // Show the native amount and the IDR-converted amount together.
          label: (ctx: { label?: string; parsed: number }) => {
            const ccy = ctx.label ?? "";
            const converted = `${formatAmount(String(ctx.parsed), base)} ${base}`;
            // The base-currency slice is already IDR — a "= … IDR" line would repeat it.
            if (ccy === base) return converted;
            const native = arNativeByCcy.value[ccy];
            return native
              ? [`${formatAmount(native, ccy)} ${ccy}`, `= ${converted}`]
              : converted;
          },
        },
      },
    },
  };
});

function formatDate(iso: string) {
  // next_date is a plain YYYY-MM-DD (no timezone) — format without Date() drift.
  const [y, m, d] = iso.split("-");
  return `${Number(d)} ${MONTHS[Number(m) - 1]} ${y}`;
}

function modeSeverity(mode: string) {
  return mode === "USAGE" ? "warn" : "info";
}

// MANUAL is left out: those rows already carry a "manual-entry" reason chip.
function intakeSourceLabel(source: string | null) {
  if (!source || source === "MANUAL") return null;
  const words = source.replace(/_/g, " ").toLowerCase();
  return words[0].toUpperCase() + words.slice(1);
}

function timeAgo(iso: string) {
  // Backend timestamps are naive UTC (e.g. "2026-07-24T02:00:00") with no
  // timezone designator. JS parses those as *local* time, which skews the
  // relative label by the viewer's UTC offset. Force UTC when none is present.
  const hasTz = /[Zz]|[+-]\d{2}:?\d{2}$/.test(iso);
  const then = new Date(hasTz ? iso : `${iso}Z`).getTime();
  const diff = Date.now() - then;
  const m = Math.floor(diff / 60000);
  if (m < 1) return "just now";
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  const d = Math.floor(h / 24);
  return `${d}d ago`;
}

function go(path: string) {
  router.push(path);
}
</script>

<template>
  <section class="dash">
    <PageHeader eyebrow="01 / Overview" title="Dashboard">
      <template #lede>Reconciliation health, revenue and open receivables at a glance.</template>
      <Button label="Refresh" outlined severity="secondary" @click="refetch()">
        <template #icon><AppIcon name="refresh-cw" /></template>
      </Button>
    </PageHeader>

    <div v-if="isLoading && !stats" class="loading">
      <ProgressSpinner style="width: 40px; height: 40px" />
    </div>

    <div v-else-if="isError" class="card card-pad error">
      Failed to load dashboard stats.
      <Button label="Retry" text @click="refetch()" />
    </div>

    <template v-else-if="stats">
      <!-- KPI strip: one bordered card, cells split by hairlines. -->
      <div class="kpi-strip">
        <div class="kpi" role="button" tabindex="0" @click="go('/review')" @keydown.enter.space.prevent="go('/review')">
          <div class="kpi-label">Manual review</div>
          <div class="kpi-value" :class="{ alert: stats.awaiting_review_count > 0 }">{{ stats.awaiting_review_count }}</div>
          <div class="kpi-hint">Payments held by safe-stop</div>
        </div>
        <div class="kpi" role="button" tabindex="0" @click="go('/queue')" @keydown.enter.space.prevent="go('/queue')">
          <div class="kpi-label">Draft invoices</div>
          <div class="kpi-value">{{ stats.draft_count }}</div>
          <div class="kpi-hint">Awaiting finalization</div>
        </div>
        <div class="kpi" role="button" tabindex="0" @click="go('/invoices')" @keydown.enter.space.prevent="go('/invoices')">
          <div class="kpi-label">Open invoices</div>
          <div class="kpi-value">{{ stats.sent_count }}</div>
          <div class="kpi-hint">Outstanding to customers</div>
        </div>
        <div class="kpi" role="button" tabindex="0" @click="go('/quotations')" @keydown.enter.space.prevent="go('/quotations')">
          <div class="kpi-label">Open quotations</div>
          <div class="kpi-value">{{ openQuotationCount }}</div>
          <div class="kpi-hint num">{{ openQuotationHint }}</div>
        </div>
        <div class="kpi" role="button" tabindex="0" @click="go('/customers')" @keydown.enter.space.prevent="go('/customers')">
          <div class="kpi-label">Customers</div>
          <div class="kpi-value">{{ stats.customer_count }}</div>
          <div class="kpi-hint">Active</div>
        </div>
        <div class="kpi" role="button" tabindex="0" @click="go('/invoices/recurring')" @keydown.enter.space.prevent="go('/invoices/recurring')">
          <div class="kpi-label">Monthly recurring</div>
          <div v-if="!stats.mrr_by_currency.length" class="kpi-value">0</div>
          <ul v-else class="mrr-list">
            <li v-for="row in stats.mrr_by_currency" :key="row.currency" class="num">
              {{ formatAmount(row.amount, row.currency) }} <span class="ccy">{{ row.currency }}</span>
            </li>
          </ul>
          <div class="kpi-hint">Active recurring templates</div>
        </div>
      </div>

      <div class="chart-row">
        <!-- Revenue chart (mockup variant A: stacked by currency). -->
        <div class="card dash-card">
          <div class="dash-card-head">
            <div>
              <h2 class="card-title">Revenue — last 12 months</h2>
              <p class="card-sub">
                Invoiced revenue by month, stacked per currency and converted to
                {{ revenueData?.base_currency ?? 'IDR' }}. Faded segments are still unpaid.
              </p>
            </div>
          </div>
          <div v-if="!hasRevenueData" class="empty-state chart-empty">
            <AppIcon name="chart-column" />
            <div>No invoiced revenue yet in the last 12 months.</div>
          </div>
          <div v-else class="chart-wrap revenue-wrap" data-testid="revenue-chart">
            <Chart
              type="bar"
              :data="chartData"
              :options="chartOptions"
              :canvas-props="{ role: 'img', 'aria-label': revenueChartLabel }" />
          </div>
          <p v-if="revenueData && revenueData.unconverted.length" class="card-note">
            Not shown (no FX rate): {{ revenueData.unconverted.join(', ') }}
          </p>
        </div>

        <!-- Open AR -->
        <div class="card dash-card">
          <div class="dash-card-head">
            <div>
              <h2 class="card-title">Open receivables</h2>
              <p class="card-sub">Outstanding invoice balances, by currency.</p>
            </div>
            <Tag class="head-tag" :value="`${stats.auto_cleared_last_30d} auto-cleared · 30d`" severity="success" />
          </div>
          <div v-if="!stats.open_ar_by_currency.length" class="empty-state">
            <AppIcon name="circle-check" />
            <div>No open AR. You're all caught up.</div>
          </div>
          <ul v-else class="ar-list">
            <li v-for="row in stats.open_ar_by_currency" :key="row.currency">
              <span class="ar-ccy">
                <span class="swatch" :style="{ background: colorForCurrency(row.currency) }" />
                {{ row.currency }}
              </span>
              <span class="ar-amt num">{{ formatAmount(row.amount, row.currency) }}</span>
            </li>
          </ul>
          <div v-if="hasArPie" class="ar-pie-block">
            <p class="card-sub">Share of open AR, converted to {{ stats.base_currency }}.</p>
            <div class="chart-wrap ar-pie" data-testid="ar-chart">
              <Chart
                type="pie"
                :data="arPieData"
                :options="arPieOptions"
                :canvas-props="{ role: 'img', 'aria-label': arChartLabel }" />
            </div>
          </div>
          <p v-if="stats.open_ar_unconverted.length" class="card-note">
            Not shown (no FX rate): {{ stats.open_ar_unconverted.join(', ') }}
          </p>
        </div>
      </div>

      <div class="two-col">
        <!-- Upcoming -->
        <div class="card dash-card list-card upcoming-card">
          <div class="dash-card-head ruled">
            <div>
              <h2 class="card-title">Upcoming invoices</h2>
              <p class="card-sub">Recurring &amp; usage invoices generating in the next 90 days.</p>
            </div>
          </div>
          <div v-if="!stats.upcoming_invoices.length" class="empty-state">
            <AppIcon name="calendar" />
            <div>Nothing scheduled in the next 90 days.</div>
          </div>
          <ul v-else class="rows upcoming-list">
            <li v-for="u in stats.upcoming_invoices" :key="u.invoice_id" class="row">
              <Tag :value="u.mode === 'USAGE' ? 'Usage' : 'Recurring'" :severity="modeSeverity(u.mode)" />
              <div class="row-main">
                <div class="row-title">{{ u.customer_name ?? '—' }}</div>
                <div class="row-amt num">
                  {{ formatAmount(u.amount, u.currency) }} {{ u.currency }}<template v-if="u.amount_is_estimate"> · est.</template>
                </div>
              </div>
              <span class="row-time">{{ formatDate(u.next_date) }}</span>
            </li>
          </ul>
        </div>

        <!-- Recent activity -->
        <div class="card dash-card list-card">
          <div class="dash-card-head ruled">
            <div>
              <h2 class="card-title">Recent reconciliation activity</h2>
              <p class="card-sub">Latest 10 engine decisions and manual actions.</p>
            </div>
            <RouterLink to="/audit-log" class="head-link">Audit log ↗</RouterLink>
          </div>
          <div v-if="!stats.recent_activity.length" class="empty-state">
            <AppIcon name="clock" />
            <div>No activity yet. POST a payment to the intake endpoints to see events here.</div>
          </div>
          <ul v-else class="rows">
            <li v-for="a in stats.recent_activity" :key="a.log_id" class="row">
              <StatusTag :status="a.action" />
              <div class="row-main">
                <div class="row-title">{{ a.payer_name ?? 'unknown payer' }}</div>
                <div class="row-amt num">{{ formatAmount(a.amount, a.currency) }} {{ a.currency ?? '' }}</div>
              </div>
              <span class="row-time">{{ timeAgo(a.created_at) }}</span>
              <div class="row-details">
                <RouterLink
                  v-if="a.invoice_id"
                  :to="{ path: `/invoices/${a.invoice_id}/edit`, query: { from: '/dashboard' } }"
                  class="doc-no"
                >Invoice {{ a.invoice_number ?? '(no number)' }}</RouterLink>
                <span v-if="a.customer_name">{{ a.customer_name }}</span>
                <span v-if="a.payment_date" class="nowrap">Paid {{ formatDate(a.payment_date) }}</span>
                <span v-if="Number(a.wht_amount)" class="nowrap num">WHT {{ formatAmount(a.wht_amount, a.currency) }} {{ a.currency }}</span>
                <span v-if="a.payer_reference">Ref {{ a.payer_reference }}</span>
                <span v-if="a.confidence_score !== null" class="nowrap">Confidence {{ Number(a.confidence_score) }}%</span>
                <span v-if="intakeSourceLabel(a.intake_source)">Via {{ intakeSourceLabel(a.intake_source) }}</span>
                <span v-if="a.actor_name">By {{ a.actor_name }}</span>
              </div>
              <div v-if="a.reasons.length" class="row-reasons">
                <span v-for="r in a.reasons" :key="r" class="reason-chip">{{ r }}</span>
              </div>
            </li>
          </ul>
        </div>
      </div>
    </template>
  </section>
</template>

<style scoped>
.dash { display: flex; flex-direction: column; gap: 24px; }
.loading { display: grid; place-items: center; padding: 4rem; }
.error { color: var(--wb-red-fg); display: flex; align-items: center; gap: 1rem; }
.num { font-variant-numeric: tabular-nums; }
.nowrap { white-space: nowrap; }

/* Page header: the shared PageHeader; the section's 24px gap adds to its margin. */
.dash > .page-head { margin-bottom: 8px; }

/* ---------- KPI strip ---------- */
/* Hairlines between cells: the strip's line-coloured background shows through
   a 1px gap, whatever the number of columns. */
.kpi-strip {
  display: grid;
  grid-template-columns: repeat(5, minmax(0, 1fr)) minmax(0, 1.35fr);
  gap: 1px;
  background: var(--wb-line);
  border: 1px solid var(--wb-line);
}
@media (max-width: 1280px) { .kpi-strip { grid-template-columns: repeat(3, minmax(0, 1fr)); } }
@media (max-width: 640px) { .kpi-strip { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
.kpi {
  background: var(--wb-surface);
  padding: 20px 18px 18px;
  display: flex;
  flex-direction: column;
  min-width: 0;
  cursor: pointer;
  transition: background 0.15s;
}
.kpi:hover { background: var(--wb-surface-2); }
.kpi:focus-visible { outline: none; box-shadow: var(--focus-ring); }
.kpi-label {
  font: 500 10.5px/1.4 var(--wb-font-sans);
  letter-spacing: 1.2px;
  text-transform: uppercase;
  color: var(--wb-muted-soft);
}
.kpi-value {
  font: 500 34px/1.1 var(--wb-font-display);
  letter-spacing: -1.5px;
  color: var(--wb-ink);
  margin-top: 8px;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.kpi-value.alert { color: var(--wb-red); }
.kpi-hint {
  font-size: 12px;
  line-height: 1.4;
  color: var(--wb-muted);
  margin-top: auto;
  padding-top: 10px;
}
.mrr-list {
  list-style: none;
  padding: 0;
  margin: 8px 0 0;
  font: 500 17px/1.35 var(--wb-font-display);
  letter-spacing: -0.4px;
  color: var(--wb-ink);
}
.mrr-list li { white-space: nowrap; }
.mrr-list .ccy { font-size: 12px; color: var(--wb-muted-soft); letter-spacing: 0.2px; }
@media (max-width: 640px) {
  .kpi { padding: 16px 14px 14px; }
  .kpi-value { font-size: 28px; }
}

/* ---------- cards ---------- */
.chart-row {
  display: grid;
  grid-template-columns: minmax(0, 2fr) minmax(0, 1fr);
  gap: 24px;
}
@media (max-width: 1000px) { .chart-row { grid-template-columns: minmax(0, 1fr); } }
.two-col {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(0, 1.5fr);
  gap: 24px;
}
@media (max-width: 1000px) { .two-col { grid-template-columns: minmax(0, 1fr); } }

.dash-card { display: flex; flex-direction: column; min-width: 0; }
.dash-card-head {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  flex-wrap: wrap;
  gap: 12px 16px;
  padding: 28px 28px 0;
}
.dash-card-head.ruled { padding: 24px 28px 18px; border-bottom: 1px solid var(--wb-line); align-items: flex-end; }
.card-title {
  font: 500 20px/1.2 var(--wb-font-display);
  letter-spacing: -0.5px;
  color: var(--wb-ink);
  margin: 0;
}
.card-sub { margin: 4px 0 0; color: var(--wb-muted); font-size: 13px; line-height: 1.5; }
.card-note { margin: 0 28px 20px; color: var(--wb-muted-soft); font-size: 12px; }
.head-tag { flex: none; white-space: nowrap; }
.empty-state .app-icon { margin-left: auto; margin-right: auto; }
.head-link { font-size: 13px; white-space: nowrap; text-decoration: none; }
.head-link:hover { text-decoration: underline; }
@media (max-width: 640px) {
  .dash-card-head { padding: 20px 18px 0; }
  .dash-card-head.ruled { padding: 18px 18px 14px; }
  .card-note { margin: 0 18px 16px; }
}

/* ---------- charts ---------- */
.chart-wrap { position: relative; padding: 22px 28px 24px; }
.chart-wrap :deep(.p-chart),
.chart-wrap :deep(.p-chart > *) { height: 100%; width: 100%; }
.chart-wrap :deep(canvas) { height: 100% !important; width: 100% !important; }
/* Bar chart gets the width its 12 months need; it grows to the AR card's height. */
.revenue-wrap { flex: 1 1 340px; min-height: 340px; }
.chart-empty { padding: 3rem 1.25rem; display: flex; flex-direction: column; align-items: center; gap: 0.5rem; color: var(--wb-muted); }
.chart-empty .app-icon { width: 1.5rem; height: 1.5rem; }
@media (max-width: 640px) { .chart-wrap { padding: 18px 12px 18px; } }

.ar-list { list-style: none; padding: 0; margin: 18px 28px 0; }
.ar-list li {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  padding: 12px 0;
  border-bottom: 1px solid var(--wb-line);
}
.ar-ccy {
  display: inline-flex;
  align-items: center;
  gap: 10px;
  font: 500 11px/1.4 var(--wb-font-sans);
  letter-spacing: 1.2px;
  text-transform: uppercase;
  color: var(--wb-muted-soft);
}
.swatch { width: 10px; height: 10px; flex: none; }
.ar-amt {
  font: 500 20px var(--wb-font-display);
  letter-spacing: -0.6px;
  color: var(--wb-ink);
  white-space: nowrap;
}
.ar-pie-block { padding: 18px 28px 0; }
.ar-pie-block .chart-wrap { padding: 12px 0 24px; }
.ar-pie { height: 260px; }
@media (max-width: 640px) {
  .ar-list { margin: 14px 18px 0; }
  .ar-pie-block { padding: 14px 18px 0; }
}

/* ---------- list cards ---------- */
.rows { list-style: none; padding: 0; margin: 0; }
.row {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto;
  gap: 4px 14px;
  align-items: center;
  padding: 18px 28px;
  border-bottom: 1px solid var(--wb-line);
}
.row:last-child { border-bottom: 0; }
.row:hover { background: var(--wb-surface-2); }
.row-main { min-width: 0; }
.row-title { font-weight: 500; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.row-amt { color: var(--wb-muted); font-size: 13px; white-space: nowrap; }
.row-time { font-size: 12px; color: var(--wb-muted-soft); white-space: nowrap; }
.row-details,
.row-reasons { grid-column: 2 / span 2; }
.row-details { font-size: 13px; color: var(--wb-muted); }
.row-details > * + *::before { content: " · "; color: var(--wb-muted-soft); }
.doc-no { white-space: nowrap; }
.row-reasons { display: flex; flex-wrap: wrap; gap: 6px; margin-top: 4px; }
.reason-chip {
  font-size: 11px;
  padding: 2px 7px;
  background: var(--wb-surface-2);
  border: 1px solid var(--wb-line);
  color: var(--wb-muted);
  border-radius: 2px;
}
@media (max-width: 640px) {
  .row { padding: 14px 18px; gap: 4px 10px; }
  .row-details,
  .row-reasons { grid-column: 1 / -1; }
}

/* Upcoming list: fill the card, whose height follows the activity card beside it,
   then scroll. height: 0 stops a long list from stretching the row; ~5 rows minimum. */
.upcoming-list { flex: 1 1 0; height: 0; min-height: 22rem; overflow-y: auto; }
@media (max-width: 1000px) { .upcoming-list { flex: none; height: auto; max-height: 28rem; min-height: 0; } }
</style>
