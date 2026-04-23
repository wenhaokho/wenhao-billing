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
}
interface MonthlyRevenueBucket { month: string; totals: Record<string, string> }
interface MonthlyRevenueResponse {
  base_currency: string;
  currencies: string[];
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

const CHART_COLORS = [
  "#2563eb", "#10b981", "#f59e0b", "#ef4444",
  "#8b5cf6", "#14b8a6", "#ec4899", "#64748b",
];

const chartData = computed(() => {
  const r = revenueData.value;
  if (!r) return { labels: [], datasets: [] };
  const labels = r.months.map((m) => m.month);
  const datasets = r.currencies.map((ccy, i) => ({
    label: ccy,
    backgroundColor: CHART_COLORS[i % CHART_COLORS.length],
    borderColor: CHART_COLORS[i % CHART_COLORS.length],
    data: r.months.map((m) => Number(m.totals[ccy] ?? 0)),
    stack: "revenue",
  }));
  return { labels, datasets };
});

const chartOptions = computed(() => ({
  maintainAspectRatio: false,
  responsive: true,
  plugins: {
    legend: {
      position: "bottom" as const,
      labels: { font: { size: 11 }, boxWidth: 12 },
    },
    tooltip: {
      callbacks: {
        label: (ctx: { dataset: { label?: string }; parsed: { y: number } }) => {
          const ccy = ctx.dataset.label ?? "";
          return `${ccy} ${formatAmount(String(ctx.parsed.y), ccy)}`;
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
          if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + "M";
          if (n >= 1_000) return (n / 1_000).toFixed(0) + "K";
          return String(n);
        },
      },
    },
  },
}));

const hasRevenueData = computed(
  () => (revenueData.value?.currencies.length ?? 0) > 0,
);

function actionSeverity(a: string) {
  switch (a) {
    case "CLEARED": return "success";
    case "HELD": return "warning";
    case "MATCHED": return "info";
    case "REVERSED": return "danger";
    case "OVERRIDDEN": return "secondary";
    default: return undefined;
  }
}

function timeAgo(iso: string) {
  const then = new Date(iso).getTime();
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
            <div class="stat-label">SENT Invoices</div>
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
      </div>

      <!-- Revenue chart -->
      <div class="card">
        <div class="card-head">
          <div>
            <h3>Revenue — last 12 months</h3>
            <p class="subtitle">Invoiced revenue (SENT + PARTIAL + PAID) by month, stacked per currency.</p>
          </div>
        </div>
        <div v-if="!hasRevenueData" class="empty-state chart-empty">
          <i class="pi pi-chart-bar" />
          <div>No invoiced revenue yet in the last 12 months.</div>
        </div>
        <div v-else class="chart-wrap">
          <Chart type="bar" :data="chartData" :options="chartOptions" />
        </div>
      </div>

      <div class="two-col">
        <!-- Open AR -->
        <div class="card">
          <div class="card-head">
            <div>
              <h3>Open Accounts Receivable</h3>
              <p class="subtitle">Sum of SENT + PARTIAL invoice balances, by currency.</p>
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

.stat-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  gap: 1rem;
}
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

.two-col {
  display: grid;
  grid-template-columns: 1fr 1.5fr;
  gap: 1rem;
}
@media (max-width: 1000px) { .two-col { grid-template-columns: 1fr; } }

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
.act-head { display: flex; align-items: center; gap: 0.6rem; flex-wrap: wrap; }
.act-payer { font-weight: 500; }
.act-amt { font-variant-numeric: tabular-nums; color: var(--color-text-muted); font-size: 0.85rem; }
.act-time { margin-left: auto; color: var(--color-text-subtle); font-size: 0.78rem; }
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
</style>
