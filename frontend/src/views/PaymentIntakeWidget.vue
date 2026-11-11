<script setup lang="ts">
import AppIcon from "@/components/AppIcon.vue";
import StatusTag from "@/components/StatusTag.vue";
import PageHeader from "@/components/PageHeader.vue";
import PeriodSelector from "@/components/dashboard/PeriodSelector.vue";
import KpiTile from "@/components/dashboard/KpiTile.vue";
import RevenueCard from "@/components/dashboard/RevenueCard.vue";
import AgingCard from "@/components/dashboard/AgingCard.vue";
import CashInCard from "@/components/dashboard/CashInCard.vue";
import ReconDonut from "@/components/dashboard/ReconDonut.vue";
import TopCustomersCard from "@/components/dashboard/TopCustomersCard.vue";
import NeedsAttentionCard from "@/components/dashboard/NeedsAttentionCard.vue";
import { computed, ref, watch } from "vue";
import { keepPreviousData, useQuery } from "@tanstack/vue-query";
import { useRoute, useRouter } from "vue-router";
import Tag from "primevue/tag";
import Button from "primevue/button";
import ProgressSpinner from "primevue/progressspinner";
import { api } from "@/api/client";
import { formatAmount } from "@/utils/money";
import { useTheme } from "@/composables/useTheme";
import { chartColors } from "@/theme/chartColors";
import {
  DEFAULT_PERIOD,
  isPeriod,
  type KpiDelta,
  type MonthlyRevenueResponse,
  type OverviewResponse,
  type Period,
  type ReceivablesResponse,
  type CurrencyAmount,
} from "@/components/dashboard/types";
import { deltaOf, formatCompact, formatShare } from "@/components/dashboard/format";
import { decIsZero } from "@/utils/decimal";

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
// The part of GET /stats/dashboard this view reads.
interface DashboardStats {
  awaiting_review_count: number;
  draft_count: number;
  open_ar_by_currency: CurrencyAmount[];
  open_quotation_count: number;
  recent_activity: ActivityItem[];
  upcoming_invoices: UpcomingInvoiceItem[];
}
const router = useRouter();
const route = useRoute();

// The period lives in the URL (?period=), so a reload or a shared link keeps it.
// replace, not push: switching periods shouldn't fill the back button.
const period = computed<Period>({
  get: () => (isPeriod(route.query.period) ? route.query.period : DEFAULT_PERIOD),
  set: (p) => { router.replace({ query: { ...route.query, period: p } }); },
});

const { data, isError, refetch } = useQuery<DashboardStats>({
  queryKey: ["dashboard-stats"],
  queryFn: async () => (await api.get<DashboardStats>("/stats/dashboard")).data,
  refetchInterval: 30_000,
});

const stats = computed(() => data.value);

const {
  data: overview,
  isError: overviewError,
  refetch: refetchOverview,
} = useQuery<OverviewResponse>({
  queryKey: computed(() => ["stats-overview", period.value]),
  queryFn: async () =>
    (await api.get<OverviewResponse>("/stats/overview", { params: { period: period.value } })).data,
  // Keep the old figures on screen while the new period loads, rather than blanking the row.
  placeholderData: keepPreviousData,
  refetchInterval: 60_000,
});

const {
  data: revenueData,
  isError: revenueError,
  refetch: refetchRevenue,
} = useQuery<MonthlyRevenueResponse>({
  queryKey: ["dashboard-revenue-monthly"],
  queryFn: async () => (await api.get<MonthlyRevenueResponse>("/stats/revenue-monthly")).data,
  refetchInterval: 60_000,
});

// Point in time (today's open AR), so no period in the key.
const {
  data: receivables,
  isError: receivablesError,
  refetch: refetchReceivables,
} = useQuery<ReceivablesResponse>({
  queryKey: ["stats-receivables"],
  queryFn: async () => (await api.get<ReceivablesResponse>("/stats/receivables")).data,
  refetchInterval: 60_000,
});

function refreshAll() {
  refetch();
  refetchOverview();
  refetchRevenue();
  refetchReceivables();
}

const PERIOD_WORDS: Record<Period, string> = { "30d": "30 d", "90d": "90 d", "12m": "12 mo" };
// Card subtitles: "last 90 days", "last 12 months".
const PERIOD_PHRASE: Record<Period, string> = { "30d": "30 days", "90d": "90 days", "12m": "12 months" };
// The overview's own period once loaded (it lags the URL while a new period loads).
const shownPeriod = computed<Period>(() => overview.value?.period ?? period.value);

interface Tile {
  key: string;
  label: string;
  value: string;
  valueTitle: string;
  unit: string;
  delta?: KpiDelta;
  sub: string;
  series?: { label: string; amount: string }[];
  sparkColor?: string;
  valueTone?: "neutral" | "bad";
  to?: string;
}

// The KPI row (mockup): every figure converted to the base currency.
const kpiTiles = computed<Tile[]>(() => {
  const o = overview.value;
  if (!o) return [];
  const k = o.kpis;
  const base = o.base_currency;
  const words = PERIOD_WORDS[o.period] ?? PERIOD_WORDS[period.value];
  const full = (v: string) => `${formatAmount(v, base)} ${base}`;
  const unit = `${base} eq.`;

  const collectedShare =
    k.collected.share_of_invoiced === null ? "" : ` · ${formatShare(k.collected.share_of_invoiced)} of invoiced`;

  const ar = k.open_ar;
  const arSub =
    ar.invoice_count === 0
      ? "nothing open"
      : `${ar.invoice_count} invoice${ar.invoice_count === 1 ? "" : "s"} · ${ar.customer_count} customer${ar.customer_count === 1 ? "" : "s"}`;

  const od = k.overdue;
  const overdueDelta: KpiDelta | undefined =
    od.share_of_open_ar === null ? undefined : { text: formatShare(od.share_of_open_ar), tone: od.share_of_open_ar > 0 ? "bad" : "neutral" };

  // foreign_share covers every non-base currency, so name the largest as "mostly",
  // not as if it were the only one.
  const mrr = k.monthly_recurring;
  let mrrDelta: KpiDelta | undefined;
  let mrrSub: string;
  // A null share with MRR means a recurring currency has no FX rate: the total
  // leaves it out, so never claim everything is billed in the base currency.
  if (mrr.foreign_share && mrr.top_foreign_currency) {
    mrrDelta = { text: formatShare(mrr.foreign_share), tone: "neutral" };
    mrrSub = `billed in foreign ccy, mostly ${mrr.top_foreign_currency}`;
  } else if (mrr.foreign_share === null && !decIsZero(mrr.amount)) {
    mrrSub = "excl. currencies with no FX rate";
  } else {
    mrrSub = mrr.foreign_share === 0 ? `all billed in ${base}` : "no active recurring invoices";
  }

  return [
    {
      key: "invoiced",
      label: `Invoiced · ${words}`,
      value: formatCompact(k.invoiced.amount),
      valueTitle: full(k.invoiced.amount),
      unit,
      delta: deltaOf(k.invoiced.amount, k.invoiced.previous, true),
      sub: `vs prior ${words}`,
      series: k.invoiced.series,
      sparkColor: "var(--wb-red)",
    },
    {
      key: "collected",
      label: `Collected · ${words}`,
      value: formatCompact(k.collected.amount),
      valueTitle: full(k.collected.amount),
      unit,
      delta: deltaOf(k.collected.amount, k.collected.previous, true),
      sub: `vs prior ${words}${collectedShare}`,
      series: k.collected.series,
      sparkColor: "var(--wb-gold)",
    },
    {
      key: "open-ar",
      label: "Open AR",
      value: formatCompact(ar.amount),
      valueTitle: full(ar.amount),
      unit,
      sub: arSub,
      to: "/invoices",
    },
    {
      key: "overdue",
      label: "Overdue",
      value: formatCompact(od.amount),
      valueTitle: full(od.amount),
      unit,
      delta: overdueDelta,
      sub: od.share_of_open_ar === null ? "nothing open" : "of open AR",
      valueTone: !decIsZero(od.amount) ? "bad" : "neutral",
      to: "/invoices",
    },
    {
      key: "mrr",
      label: "Monthly recurring",
      value: formatCompact(mrr.amount),
      valueTitle: full(mrr.amount),
      unit,
      delta: mrrDelta,
      sub: mrrSub,
      to: "/invoices/recurring",
    },
  ];
});

// Work queues from the old KPI strip, kept as a slim row of links. A zero count
// stays visible (muted) so the row never jumps.
const queueLinks = computed(() => {
  const s = stats.value;
  if (!s) return [];
  return [
    { key: "review", to: "/review", count: s.awaiting_review_count, text: "held for review", alert: true },
    { key: "drafts", to: "/queue", count: s.draft_count, text: s.draft_count === 1 ? "draft awaiting finalization" : "drafts awaiting finalization", alert: false },
    { key: "quotations", to: "/quotations", count: s.open_quotation_count, text: s.open_quotation_count === 1 ? "open quotation" : "open quotations", alert: false },
  ];
});

// Brand colours come from the CSS tokens. Re-read them after a theme switch
// has been applied to <html> (useTheme's own watcher runs first), so the
// revenue chart rebuilds in the new theme's colours. The SVG cards use the
// CSS tokens directly and follow the theme by themselves.
const { theme } = useTheme();
const colors = ref(chartColors());
watch(theme, () => { colors.value = chartColors(); }, { flush: "post" });

// Short month names for formatDate (upcoming invoices, payment dates).
const MONTHS = ["Jan","Feb","Mar","Apr","May","Jun","Jul","Aug","Sep","Oct","Nov","Dec"];

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
</script>

<template>
  <section class="dash">
    <PageHeader eyebrow="01 / Overview" title="Dashboard">
      <template #lede>
        Revenue, receivables and reconciliation health. Figures in
        {{ overview?.base_currency ?? revenueData?.base_currency ?? "IDR" }}, foreign currency converted at today's rate.
      </template>
      <PeriodSelector v-model="period" />
      <Button label="Refresh" outlined severity="secondary" @click="refreshAll()">
        <template #icon><AppIcon name="refresh-cw" /></template>
      </Button>
    </PageHeader>

    <!-- Work queues (from the old KPI strip): links with counts, muted at zero. -->
    <nav v-if="stats" class="queues" aria-label="Work queues" data-testid="queue-links">
      <RouterLink
        v-for="q in queueLinks"
        :key="q.key"
        :to="q.to"
        class="queue"
        :class="{ zero: q.count === 0, alert: q.alert && q.count > 0 }"
        :data-queue="q.key"
      ><span class="queue-n num">{{ q.count }}</span> {{ q.text }}</RouterLink>
    </nav>

    <!-- KPI row: one bordered strip, cells split by hairlines. -->
    <!-- A failed refetch (e.g. after a period switch) keeps the last figures, so say so. -->
    <div v-if="overviewError && overview" class="kpi-stale" role="alert">
      Couldn't load the figures for this period; showing the last ones loaded.
      <Button label="Retry" text size="small" @click="refetchOverview()" />
    </div>
    <div v-if="overviewError && !overview" class="card card-pad error">
      Failed to load the KPI figures.
      <Button label="Retry" text @click="refetchOverview()" />
    </div>
    <div v-else-if="!overview" class="card kpi-loading">
      <ProgressSpinner style="width: 32px; height: 32px" />
    </div>
    <div v-else class="kpi-block">
      <div class="kpi-strip" data-testid="kpi-row">
        <KpiTile
          v-for="t in kpiTiles"
          :key="t.key"
          :data-kpi="t.key"
          :label="t.label"
          :value="t.value"
          :value-title="t.valueTitle"
          :unit="t.unit"
          :delta="t.delta"
          :sub="t.sub"
          :series="t.series"
          :spark-color="t.sparkColor"
          :value-tone="t.valueTone"
          :to="t.to"
        />
      </div>
      <p v-if="overview.unconverted?.length" class="dash-note">
        Not converted: {{ overview.unconverted.join(", ") }} (no FX rate)
      </p>
    </div>

    <div class="chart-row">
      <div v-if="revenueError && !revenueData" class="card card-pad error">
        Failed to load revenue.
        <Button label="Retry" text @click="refetchRevenue()" />
      </div>
      <div v-else-if="!revenueData" class="card loading">
        <ProgressSpinner style="width: 40px; height: 40px" />
      </div>
      <RevenueCard v-else :data="revenueData" :colors="colors" />

      <!-- Beside the revenue card: aging, with days to pay. -->
      <div v-if="receivablesError && !receivables" class="card card-pad error">
        Failed to load receivables.
        <Button label="Retry" text @click="refetchReceivables()" />
      </div>
      <div v-else-if="!receivables" class="card loading">
        <ProgressSpinner style="width: 40px; height: 40px" />
      </div>
      <AgingCard
        v-else
        :data="receivables"
        :open-ar-by-currency="stats?.open_ar_by_currency"
        :days-to-pay="overview?.days_to_pay"
        :period-words="PERIOD_WORDS[shownPeriod]"
      />
    </div>

    <!-- The mockup stacks these two under aging; at this app's widths (sidebar
         included) that column is too narrow to pair them, and stacked they ran
         far below the revenue card. A row of their own keeps them side by side. -->
    <div class="grid-2">
      <div v-if="receivablesError && !receivables" class="card card-pad error">
        Failed to load expected cash in.
        <Button label="Retry" text @click="refetchReceivables()" />
      </div>
      <div v-else-if="!receivables" class="card loading">
        <ProgressSpinner style="width: 40px; height: 40px" />
      </div>
      <CashInCard v-else :data="receivables" />
      <div v-if="overviewError && !overview" class="card card-pad error">
        Failed to load reconciliation.
        <Button label="Retry" text @click="refetchOverview()" />
      </div>
      <div v-else-if="!overview" class="card loading">
        <ProgressSpinner style="width: 40px; height: 40px" />
      </div>
      <ReconDonut v-else :data="overview.reconciliation" :period-words="PERIOD_PHRASE[shownPeriod]" />
    </div>

    <div class="grid-2">
      <div v-if="overviewError && !overview" class="card card-pad error">
        Failed to load top customers.
        <Button label="Retry" text @click="refetchOverview()" />
      </div>
      <div v-else-if="!overview" class="card loading">
        <ProgressSpinner style="width: 40px; height: 40px" />
      </div>
      <TopCustomersCard
        v-else
        :data="overview.top_customers"
        :base="overview.base_currency"
        :period-words="PERIOD_PHRASE[shownPeriod]"
        :unconverted="overview.unconverted"
      />

      <div v-if="receivablesError && !receivables" class="card card-pad error">
        Failed to load overdue invoices.
        <Button label="Retry" text @click="refetchReceivables()" />
      </div>
      <div v-else-if="!receivables" class="card loading">
        <ProgressSpinner style="width: 40px; height: 40px" />
      </div>
      <NeedsAttentionCard v-else :rows="receivables.needs_attention" />

      <div v-if="isError && !stats" class="card card-pad error">
        Failed to load dashboard stats.
        <Button label="Retry" text @click="refetch()" />
      </div>
      <div v-else-if="!stats" class="card loading">
        <ProgressSpinner style="width: 40px; height: 40px" />
      </div>
      <template v-else>
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
      </template>
    </div>
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

/* ---------- KPI row ---------- */
/* Hairlines between cells: the strip's line-coloured background shows through
   a 1px gap, whatever the number of columns. Five tiles leave a gap on the
   narrower grids, so the last tile spans two columns there. */
.kpi-strip {
  display: grid;
  grid-template-columns: repeat(5, minmax(0, 1fr));
  gap: 1px;
  background: var(--wb-line);
  border: 1px solid var(--wb-line);
}
@media (max-width: 1100px) {
  .kpi-strip { grid-template-columns: repeat(3, minmax(0, 1fr)); }
  .kpi-strip > :last-child { grid-column: span 2; }
}
@media (max-width: 640px) { .kpi-strip { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
.kpi-loading { display: grid; place-items: center; min-height: 148px; }
.dash-note { margin: 8px 0 0; color: var(--wb-muted-soft); font-size: 12px; }
.kpi-stale {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px 12px;
  margin-bottom: -12px;
  color: var(--wb-red-fg);
  font-size: 13px;
}

/* ---------- work queues ---------- */
.queues { display: flex; flex-wrap: wrap; gap: 8px 24px; margin-top: -16px; }
.queue {
  font-size: 13px;
  color: var(--wb-muted);
  text-decoration: none;
  white-space: nowrap;
}
.queue:hover { color: var(--wb-ink); text-decoration: underline; }
.queue-n { font-weight: 500; color: var(--wb-ink); }
.queue.alert .queue-n { color: var(--wb-red); }
.queue.zero,
.queue.zero .queue-n { color: var(--wb-muted-soft); }

/* ---------- cards ---------- */
.chart-row {
  display: grid;
  grid-template-columns: minmax(0, 2fr) minmax(0, 1fr);
  gap: 24px;
}
@media (max-width: 1000px) { .chart-row { grid-template-columns: minmax(0, 1fr); } }
/* Cash in | reconciliation; top customers | needs attention; upcoming | recent activity. */
.grid-2 {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 24px;
  align-items: stretch;
}
@media (max-width: 1000px) { .grid-2 { grid-template-columns: minmax(0, 1fr); } }

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
.empty-state .app-icon { margin-left: auto; margin-right: auto; }
.head-link { font-size: 13px; white-space: nowrap; text-decoration: none; }
.head-link:hover { text-decoration: underline; }
@media (max-width: 640px) {
  .dash-card-head { padding: 20px 18px 0; }
  .dash-card-head.ruled { padding: 18px 18px 14px; }
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
