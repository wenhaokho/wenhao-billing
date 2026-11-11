<script setup lang="ts">
import { computed, defineAsyncComponent } from "vue";
import { useQuery } from "@tanstack/vue-query";
import { useRouter } from "vue-router";
import Tag from "primevue/tag";
import Button from "primevue/button";
import ProgressSpinner from "primevue/progressspinner";
const Chart = defineAsyncComponent(() => import("primevue/chart"));
import { api } from "@/api/client";
import { formatAmount } from "@/utils/money";

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

// Colour follows the currency, never its position in the dataset. The same
// currency sits at a different index in each chart (revenue is ordered by
// currency, the AR pie by amount), so indexing a palette by rank painted IDR
// blue in the bars and amber in the pie. Pinning the hue per currency keeps
// every chart on this page in agreement.
const CURRENCY_COLORS: Record<string, string> = {
  IDR: "#2563eb", // blue
  SGD: "#10b981", // green
  USD: "#f59e0b", // amber
  EUR: "#ef4444", // red
};
// Slots deliberately clear of the pinned hues above, so an unmapped currency is
// never mistaken for one of them.
const FALLBACK_COLORS = ["#8b5cf6", "#14b8a6", "#ec4899", "#64748b"];

function colorForCurrency(ccy: string): string {
  const code = (ccy ?? "").toUpperCase();
  const pinned = CURRENCY_COLORS[code];
  if (pinned) return pinned;
  // Deterministic hash so an unmapped currency still lands on the same colour
  // in every chart, rather than shifting with the dataset order.
  let h = 0;
  for (let i = 0; i < code.length; i++) h = (h * 31 + code.charCodeAt(i)) >>> 0;
  return FALLBACK_COLORS[h % FALLBACK_COLORS.length];
}

const chartData = computed(() => {
  const r = revenueData.value;
  if (!r) return { labels: [], datasets: [] };
  const labels = r.months.map((m) => m.month);
  const paid = r.currencies.map((ccy) => ({
    label: ccy,
    currency: ccy,
    unpaid: false,
    backgroundColor: colorForCurrency(ccy),
    borderColor: colorForCurrency(ccy),
    data: r.months.map(
      (m) => Number(m.totals[ccy] ?? 0) - Number(m.unpaid_totals[ccy] ?? 0),
    ),
    stack: "revenue",
  }));
  // Same hue as the paid segment, faded, stacked above every paid segment so the
  // faded top of a bar reads as the whole unpaid portion of that month. The solid
  // outline keeps the pale fill visible against the card in both themes.
  const unpaid = r.currencies.map((ccy) => ({
    label: `${ccy} unpaid`,
    currency: ccy,
    unpaid: true,
    backgroundColor: `${colorForCurrency(ccy)}4d`,
    borderColor: colorForCurrency(ccy),
    borderWidth: 1,
    data: r.months.map((m) => Number(m.unpaid_totals[ccy] ?? 0)),
    stack: "revenue",
  }));
  return { labels, datasets: [...paid, ...unpaid] };
});

const chartOptions = computed(() => {
  const base = revenueData.value?.base_currency ?? "";
  const months = revenueData.value?.months ?? [];
  return {
  maintainAspectRatio: false,
  responsive: true,
  // One tooltip per bar: hovering anywhere over a month lists all its segments.
  interaction: { mode: "index" as const, intersect: false },
  plugins: {
    legend: {
      position: "bottom" as const,
      labels: { font: { size: 11 }, boxWidth: 12 },
    },
    tooltip: {
      // Most months have only some of the series; a month with none shows no tooltip.
      filter: (ctx: { parsed: { y: number } }) => ctx.parsed.y !== 0,
      callbacks: {
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
    x: { stacked: true, grid: { display: false } },
    y: {
      stacked: true,
      beginAtZero: true,
      ticks: {
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

const arPieData = computed(() => {
  const rows = stats.value?.open_ar_base_by_currency ?? [];
  return {
    labels: rows.map((r) => r.currency),
    datasets: [
      {
        data: rows.map((r) => Number(r.base_amount)),
        backgroundColor: rows.map((r) => colorForCurrency(r.currency)),
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
  return {
    maintainAspectRatio: false,
    responsive: true,
    plugins: {
      legend: { position: "bottom" as const, labels: { font: { size: 11 }, boxWidth: 12 } },
      tooltip: {
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
  const months = ["Jan","Feb","Mar","Apr","May","Jun","Jul","Aug","Sep","Oct","Nov","Dec"];
  return `${Number(d)} ${months[Number(m) - 1]} ${y}`;
}

function modeSeverity(mode: string) {
  return mode === "USAGE" ? "warn" : "info";
}

function actionSeverity(a: string) {
  switch (a) {
    case "CLEARED": return "success";
    case "HELD": return "warn";
    case "MATCHED": return "info";
    case "REVERSED": return "danger";
    case "OVERRIDDEN": return "secondary";
    default: return undefined;
  }
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

</script>

<template>
  <section class="dash">
    <header class="page-header">
      <div>
        <h1>Dashboard</h1>
        <p class="subtitle">Reconciliation health and open AR at a glance.</p>
      </div>
      <div class="page-actions">
        <Button icon="pi pi-refresh" label="Refresh" text @click="refetch()" />
      </div>
    </header>

    <div v-if="isLoading && !stats" class="loading">
      <ProgressSpinner style="width: 40px; height: 40px" />
    </div>

    <div v-else-if="isError" class="card card-pad error">
      Failed to load dashboard stats.
      <Button label="Retry" text @click="refetch()" />
    </div>

    <template v-else-if="stats">
      <!-- stat cards -->
      <div class="stat-grid">
        <div class="stat-card" @click="router.push('/review')" role="button" tabindex="0" @keydown.enter.space.prevent="($event.currentTarget as HTMLElement).click()">
          <div class="stat-icon warn"><i class="pi pi-exclamation-triangle" /></div>
          <div>
            <div class="stat-label">Manual Review</div>
            <div class="stat-value">{{ stats.awaiting_review_count }}</div>
            <div class="stat-hint">Payments held by safe-stop</div>
          </div>
        </div>

        <div class="stat-card" @click="router.push('/queue')" role="button" tabindex="0" @keydown.enter.space.prevent="($event.currentTarget as HTMLElement).click()">
          <div class="stat-icon info"><i class="pi pi-inbox" /></div>
          <div>
            <div class="stat-label">DRAFT Invoices</div>
            <div class="stat-value">{{ stats.draft_count }}</div>
            <div class="stat-hint">Awaiting finalization</div>
          </div>
        </div>

        <div class="stat-card" @click="router.push('/invoices')" role="button" tabindex="0" @keydown.enter.space.prevent="($event.currentTarget as HTMLElement).click()">
          <div class="stat-icon primary"><i class="pi pi-send" /></div>
          <div>
            <div class="stat-label">Open Invoices</div>
            <div class="stat-value">{{ stats.sent_count }}</div>
            <div class="stat-hint">Outstanding to customers</div>
          </div>
        </div>

        <div class="stat-card" @click="router.push('/quotations')" role="button" tabindex="0" @keydown.enter.space.prevent="($event.currentTarget as HTMLElement).click()">
          <div class="stat-icon quote"><i class="pi pi-file-edit" /></div>
          <div>
            <div class="stat-label">Open Quotations</div>
            <div class="stat-value">{{ openQuotationCount }}</div>
            <div class="stat-hint">{{ openQuotationHint }}</div>
          </div>
        </div>

        <div class="stat-card" @click="router.push('/customers')" role="button" tabindex="0" @keydown.enter.space.prevent="($event.currentTarget as HTMLElement).click()">
          <div class="stat-icon neutral"><i class="pi pi-users" /></div>
          <div>
            <div class="stat-label">Customers</div>
            <div class="stat-value">{{ stats.customer_count }}</div>
            <div class="stat-hint">Active</div>
          </div>
        </div>

        <div class="stat-card" @click="router.push('/invoices/recurring')" role="button" tabindex="0" @keydown.enter.space.prevent="($event.currentTarget as HTMLElement).click()">
          <div class="stat-icon info"><i class="pi pi-sync" /></div>
          <div class="stat-body">
            <div class="stat-label">Monthly Recurring</div>
            <div v-if="!stats.mrr_by_currency.length" class="stat-value">0</div>
            <ul v-else class="mrr-list">
              <li v-for="row in stats.mrr_by_currency" :key="row.currency" class="num">
                {{ formatAmount(row.amount, row.currency) }} {{ row.currency }}
              </li>
            </ul>
            <div class="stat-hint">Active recurring templates</div>
          </div>
        </div>
      </div>

      <div class="chart-row">
        <!-- Revenue chart -->
        <div class="card">
          <div class="card-head">
            <div>
              <h3>Revenue — last 12 months</h3>
              <p class="subtitle">
                Invoiced revenue by month, stacked per currency and converted to
                {{ revenueData?.base_currency ?? 'IDR' }}. Faded segments are still unpaid.
              </p>
            </div>
          </div>
          <div v-if="!hasRevenueData" class="empty-state chart-empty">
            <i class="pi pi-chart-bar" />
            <div>No invoiced revenue yet in the last 12 months.</div>
          </div>
          <div v-else class="chart-wrap">
            <Chart type="bar" :data="chartData" :options="chartOptions" />
          </div>
          <p v-if="revenueData && revenueData.unconverted.length" class="pie-note">
            Not shown (no FX rate): {{ revenueData.unconverted.join(', ') }}
          </p>
        </div>

        <!-- Open AR -->
        <div class="card">
          <div class="card-head">
            <div>
              <h3>Open Accounts Receivable</h3>
              <p class="subtitle">Sum of outstanding invoice balances, by currency.</p>
            </div>
            <Tag :value="`${stats.auto_cleared_last_30d} auto-cleared · 30d`" severity="success" />
          </div>
          <div v-if="!stats.open_ar_by_currency.length" class="empty-state">
            <i class="pi pi-check-circle" />
            <div>No open AR. You're all caught up.</div>
          </div>
          <ul v-else class="ar-list">
            <li v-for="row in stats.open_ar_by_currency" :key="row.currency">
              <span class="ar-ccy">{{ row.currency }}</span>
              <span class="ar-amt num">{{ formatAmount(row.amount, row.currency) }}</span>
            </li>
          </ul>
          <div v-if="hasArPie" class="chart-wrap ar-pie">
            <p class="pie-caption">Share of open AR, converted to {{ stats.base_currency }}.</p>
            <Chart type="pie" :data="arPieData" :options="arPieOptions" />
          </div>
          <p v-if="stats.open_ar_unconverted.length" class="pie-note">
            Not shown (no FX rate): {{ stats.open_ar_unconverted.join(', ') }}
          </p>
        </div>
      </div>

      <div class="two-col">
        <!-- Upcoming -->
        <div class="card upcoming-card">
          <div class="card-head">
            <div>
              <h3>Upcoming Invoices</h3>
              <p class="subtitle">Recurring &amp; usage invoices generating in the next 90 days.</p>
            </div>
          </div>
          <div v-if="!stats.upcoming_invoices.length" class="empty-state">
            <i class="pi pi-calendar" />
            <div>Nothing scheduled in the next 90 days.</div>
          </div>
          <ul v-else class="activity upcoming-list">
            <li v-for="u in stats.upcoming_invoices" :key="u.invoice_id">
              <div class="act-head">
                <Tag :value="u.mode === 'USAGE' ? 'Usage' : 'Recurring'" :severity="modeSeverity(u.mode)" />
                <span class="act-payer">{{ u.customer_name ?? '—' }}</span>
                <span class="act-amt num">
                  {{ formatAmount(u.amount, u.currency) }} {{ u.currency }}<template v-if="u.amount_is_estimate"> · est.</template>
                </span>
                <span class="act-time">{{ formatDate(u.next_date) }}</span>
              </div>
            </li>
          </ul>
        </div>

        <!-- Recent activity -->
        <div class="card">
          <div class="card-head">
            <div>
              <h3>Recent Reconciliation Activity</h3>
              <p class="subtitle">Latest 10 engine decisions and manual actions.</p>
            </div>
          </div>
          <div v-if="!stats.recent_activity.length" class="empty-state">
            <i class="pi pi-clock" />
            <div>No activity yet. POST a payment to the intake endpoints to see events here.</div>
          </div>
          <ul v-else class="activity">
            <li v-for="a in stats.recent_activity" :key="a.log_id">
              <div class="act-head">
                <Tag :value="a.action" :severity="actionSeverity(a.action)" />
                <span class="act-payer">{{ a.payer_name ?? 'unknown payer' }}</span>
                <span class="act-amt num">{{ formatAmount(a.amount, a.currency) }} {{ a.currency ?? '' }}</span>
                <span class="act-time">{{ timeAgo(a.created_at) }}</span>
              </div>
              <div class="act-details">
                <RouterLink
                  v-if="a.invoice_id"
                  :to="{ path: `/invoices/${a.invoice_id}/edit`, query: { from: '/dashboard' } }"
                >Invoice {{ a.invoice_number ?? '(no number)' }}</RouterLink>
                <span v-if="a.customer_name">{{ a.customer_name }}</span>
                <span v-if="a.payment_date">Paid {{ formatDate(a.payment_date) }}</span>
                <span v-if="Number(a.wht_amount)">WHT {{ formatAmount(a.wht_amount, a.currency) }} {{ a.currency }}</span>
                <span v-if="a.payer_reference">Ref {{ a.payer_reference }}</span>
                <span v-if="a.confidence_score !== null">Confidence {{ Number(a.confidence_score) }}%</span>
                <span v-if="intakeSourceLabel(a.intake_source)">Via {{ intakeSourceLabel(a.intake_source) }}</span>
                <span v-if="a.actor_name">By {{ a.actor_name }}</span>
              </div>
              <div v-if="a.reasons.length" class="act-reasons">
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
.dash { display: flex; flex-direction: column; gap: 1.25rem; }
.loading { display: grid; place-items: center; padding: 4rem; }
.error { color: var(--color-danger); display: flex; align-items: center; gap: 1rem; }

/* Six across on a desktop; the last column is wider for the MRR amounts. */
.stat-grid {
  display: grid;
  grid-template-columns: repeat(5, minmax(0, 1fr)) minmax(0, 1.35fr);
  gap: 1rem;
}
@media (max-width: 1280px) { .stat-grid { grid-template-columns: repeat(3, minmax(0, 1fr)); } }
@media (max-width: 640px) { .stat-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
.stat-card {
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  padding: 1.1rem 1.25rem;
  display: flex;
  align-items: flex-start;
  gap: 0.9rem;
  cursor: pointer;
  transition: box-shadow 0.15s, transform 0.15s, border-color 0.15s;
}
.stat-card:hover {
  box-shadow: var(--shadow-md);
  border-color: var(--color-border-strong);
  transform: translateY(-1px);
}
.stat-card:focus-visible {
  outline: none;
  box-shadow: var(--focus-ring);
  border-color: var(--color-primary);
}
.stat-icon {
  width: 40px; height: 40px; border-radius: 10px;
  display: grid; place-items: center; font-size: 1.05rem; flex-shrink: 0;
}
.stat-icon.warn { background: var(--color-warning-soft); color: var(--color-warning); }
.stat-icon.info { background: var(--color-primary-soft); color: var(--color-primary); }
.stat-icon.primary { background: #e0e7ff; color: #4338ca; }
.stat-icon.neutral { background: var(--color-neutral-soft); color: var(--color-neutral); }
.stat-icon.quote { background: #ecfdf5; color: #059669; }
.stat-label { font-size: 0.78rem; color: var(--color-text-muted); text-transform: uppercase; letter-spacing: 0.04em; font-weight: 600; }
.stat-value { font-size: 1.9rem; font-weight: 700; color: var(--color-text); line-height: 1.1; margin-top: 0.15rem; letter-spacing: -0.01em; }
.stat-hint { font-size: 0.78rem; color: var(--color-text-subtle); margin-top: 0.2rem; }
.stat-body { min-width: 0; }
.mrr-list { list-style: none; padding: 0; margin: 0.25rem 0 0; font-weight: 700; font-size: 1rem; line-height: 1.35; color: var(--color-text); }
/* Keep labels on the card once six columns get narrow on a laptop screen. */
@media (max-width: 1500px) {
  .stat-card { padding: 0.85rem 0.9rem; gap: 0.6rem; }
  .stat-icon { width: 32px; height: 32px; font-size: 0.9rem; }
  .stat-label { font-size: 0.7rem; letter-spacing: 0.02em; }
  .stat-value { font-size: 1.6rem; }
  .stat-hint { font-size: 0.72rem; }
}

.two-col {
  display: grid;
  grid-template-columns: 1fr 1.5fr;
  gap: 1rem;
}
@media (max-width: 1000px) { .two-col { grid-template-columns: 1fr; } }

/* Bar chart gets the width its 12 months need; it grows to match the AR card's height. */
.chart-row {
  display: grid;
  grid-template-columns: minmax(0, 2fr) minmax(0, 1fr);
  gap: 1rem;
}
@media (max-width: 1000px) { .chart-row { grid-template-columns: minmax(0, 1fr); } }
.chart-row > .card { display: flex; flex-direction: column; }
.chart-row > .card > .chart-wrap:not(.ar-pie) { flex: 1 1 320px; height: auto; min-height: 320px; }

.card-head {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  padding: 1.1rem 1.25rem 0.75rem;
  gap: 1rem;
  border-bottom: 1px solid var(--color-border);
}
.card-head h3 { margin: 0; font-size: 0.95rem; font-weight: 600; }
.card-head .subtitle { margin: 0.15rem 0 0; color: var(--color-text-muted); font-size: 0.8rem; }

.ar-list { list-style: none; padding: 0.5rem 0; margin: 0; }
.ar-list li {
  display: flex; justify-content: space-between; align-items: center;
  padding: 0.75rem 1.25rem;
}
.ar-list li:not(:last-child) { border-bottom: 1px solid var(--color-border); }
.ar-ccy {
  font-weight: 600; font-size: 0.8rem;
  background: var(--color-surface-alt);
  padding: 0.2rem 0.55rem; border-radius: 6px;
  color: var(--color-text-muted);
}
.ar-amt { font-weight: 600; font-size: 1.05rem; font-variant-numeric: tabular-nums; }

.activity { list-style: none; padding: 0; margin: 0; }
.activity li {
  padding: 0.8rem 1.25rem;
}
.activity li:not(:last-child) { border-bottom: 1px solid var(--color-border); }
/* Upcoming list: fill the card, whose height follows the activity card beside it,
   then scroll. height: 0 stops a long list from stretching the row; ~5 rows minimum. */
.upcoming-card { display: flex; flex-direction: column; }
.upcoming-list { flex: 1 1 0; height: 0; min-height: 15.5rem; overflow-y: auto; }
.act-head { display: flex; align-items: center; gap: 0.6rem; flex-wrap: wrap; }
.act-payer { font-weight: 500; }
.act-amt { font-variant-numeric: tabular-nums; color: var(--color-text-muted); font-size: 0.85rem; }
.act-time { margin-left: auto; color: var(--color-text-subtle); font-size: 0.78rem; }
.act-details { margin-top: 0.35rem; color: var(--color-text-muted); font-size: 0.8rem; }
.act-details > * + *::before { content: " · "; color: var(--color-text-subtle); }
.act-reasons { margin-top: 0.35rem; display: flex; flex-wrap: wrap; gap: 0.3rem; }
.chart-wrap { padding: 1rem 1.25rem 1.25rem; height: 320px; position: relative; }
.chart-wrap :deep(.p-chart),
.chart-wrap :deep(.p-chart > *) { height: 100%; width: 100%; }
.chart-wrap :deep(canvas) { height: 100% !important; width: 100% !important; }
.chart-empty { padding: 3rem 1.25rem; display: flex; flex-direction: column; align-items: center; gap: 0.5rem; color: var(--color-text-muted); }
.chart-empty i { font-size: 1.5rem; }

.reason-chip {
  font-size: 0.72rem; padding: 0.15rem 0.45rem;
  background: var(--color-surface-alt);
  border: 1px solid var(--color-border);
  color: var(--color-text-muted);
  border-radius: 4px;
}

.ar-pie { height: 260px; }
.pie-caption { margin: 0 1.25rem 0.25rem; color: var(--color-text-muted); font-size: 0.8rem; }
.pie-note { margin: 0.25rem 1.25rem 0.75rem; color: var(--color-text-subtle); font-size: 0.75rem; }
</style>
