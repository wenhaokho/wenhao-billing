// Hand-written mirrors of the dashboard's /stats responses (backend/openapi.json).
// Money is a decimal string ("1234.5000"); shares and day counts are numbers.

export type Period = "30d" | "90d" | "12m";
export const PERIODS: readonly Period[] = ["30d", "90d", "12m"];
export const DEFAULT_PERIOD: Period = "12m";

export function isPeriod(v: unknown): v is Period {
  return typeof v === "string" && (PERIODS as readonly string[]).includes(v);
}

export interface SeriesPoint {
  label: string;
  amount: string;
}

/** GET /stats/overview */
export interface KPIFlow {
  amount: string;
  previous: string;
  series: SeriesPoint[];
}
export interface KPICollected extends KPIFlow {
  /** Percent of invoiced; null when invoiced is zero. */
  share_of_invoiced: number | null;
}
export interface KPIOpenAR {
  amount: string;
  invoice_count: number;
  customer_count: number;
}
export interface KPIOverdue {
  amount: string;
  /** Percent of open AR; null when open AR is zero. */
  share_of_open_ar: number | null;
}
export interface KPIRecurring {
  amount: string;
  /** Percent billed in a currency other than the base; null when there is no MRR or an MRR currency has no FX rate. */
  foreign_share: number | null;
  top_foreign_currency: string | null;
}
export interface OverviewKPIs {
  invoiced: KPIFlow;
  collected: KPICollected;
  open_ar: KPIOpenAR;
  overdue: KPIOverdue;
  monthly_recurring: KPIRecurring;
}
export interface DaysToPay {
  avg: number | null;
  previous_avg: number | null;
}
export interface ReconHealth {
  auto_cleared: number;
  manual: number;
  held: number;
  avg_confidence: number | null;
  aliases_in_use: number;
  customers_with_aliases: number;
}
export interface TopCustomer {
  customer_id: string;
  name: string;
  amount: string;
  share: number;
  overdue: string;
}
export interface TopOthers {
  count: number;
  amount: string;
  share: number;
}
export interface TopCustomers {
  rows: TopCustomer[] | null;
  others: TopOthers;
  top3_share: number | null;
}
export interface OverviewResponse {
  period: Period;
  from: string;
  to: string;
  previous_from: string;
  previous_to: string;
  base_currency: string;
  unconverted: string[] | null;
  kpis: OverviewKPIs;
  days_to_pay: DaysToPay;
  reconciliation: ReconHealth;
  top_customers: TopCustomers;
}

/** GET /stats/revenue-monthly */
export interface MonthlyRevenueBucket {
  /** YYYY-MM */
  month: string;
  /** Base-currency equivalent per source currency; currencies with no rate are left out. */
  totals: Record<string, string>;
  /** Original-currency amount per source currency. */
  native_totals: Record<string, string>;
  /** Still-owed part of totals (base currency). */
  unpaid_totals: Record<string, string>;
  /** Still-owed part of native_totals. */
  native_unpaid_totals: Record<string, string>;
}
export interface RevenueBestMonth {
  month: string;
  amount: string;
}
export interface RevenueFooter {
  invoiced_base: string;
  foreign_share: number | null;
  best_month: RevenueBestMonth | null;
  fx_exposure: string;
}
export interface MonthlyRevenueResponse {
  base_currency: string;
  currencies: string[];
  unconverted: string[];
  months: MonthlyRevenueBucket[];
  footer: RevenueFooter;
}

/** A native amount in its currency (GET /stats/dashboard open_ar_by_currency). */
export interface CurrencyAmount {
  currency: string;
  amount: string;
}

/** GET /stats/receivables (point in time, no period). */
export type AgingKey = "not_due" | "d1_30" | "d31_60" | "d61_90" | "d90_plus";
export interface AgingBucket {
  key: AgingKey;
  /** Base-currency equivalent. */
  amount: string;
  /** Invoices in the bucket, every currency (unconverted ones included). */
  count: number;
}
export type CashInKey = "d0_30" | "d31_60" | "d61_90";
export interface CashInBucket {
  key: CashInKey;
  /** Open balance not yet overdue, due in the span; base-currency equivalent. */
  open_invoices: string;
  /** Next recurring / usage runs in the span; base-currency equivalent. */
  recurring: string;
  /** True when a usage estimate is among the recurring runs. */
  estimate: boolean;
}
export interface AttentionRow {
  invoice_id: string;
  invoice_number: string | null;
  customer_id: string | null;
  customer_name: string | null;
  /** In the invoice's own currency. */
  balance_due: string;
  /** Balance less withholding not yet booked, in the invoice's own currency. */
  expected_cash: string;
  currency: string;
  /** YYYY-MM-DD */
  due_date: string;
  days_late: number;
  last_reminder_at: string | null;
}
export interface ReceivablesResponse {
  base_currency: string;
  unconverted: string[];
  open_ar: string;
  /** Percent of open AR past due; null when nothing is open. */
  past_due_share: number | null;
  invoice_count: number;
  customer_count: number;
  aging: AgingBucket[];
  cash_in: CashInBucket[];
  overdue_excluded: string;
  needs_attention: AttentionRow[];
}

/** KpiTile's delta line: text plus the colour it reads in. */
export interface KpiDelta {
  text: string;
  tone: "good" | "bad" | "neutral";
}
