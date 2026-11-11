<script setup lang="ts">
import { ref, computed } from "vue";
import { useQuery } from "@tanstack/vue-query";
import DataTable from "primevue/datatable";
import Column from "primevue/column";
import DatePicker from "primevue/datepicker";
import Tabs from "primevue/tabs";
import TabList from "primevue/tablist";
import Tab from "primevue/tab";
import TabPanels from "primevue/tabpanels";
import TabPanel from "primevue/tabpanel";
import Tag from "primevue/tag";
import Message from "primevue/message";
import PageHeader from "@/components/PageHeader.vue";
import EmptyState from "@/components/EmptyState.vue";
import { api } from "@/api/client";
import { formatAmount } from "@/utils/money";

interface TrialBalanceRow {
  account_id: number;
  code: string;
  name: string;
  type: string;
  currency: string;
  debit: string;
  credit: string;
  net: string;
  base_debit: string;
  base_credit: string;
  base_net: string;
}
interface TrialBalanceReport {
  as_of: string;
  base_currency: string;
  rows: TrialBalanceRow[];
  total_debit_base: string;
  total_credit_base: string;
}

interface BalanceSheetLine {
  account_id: number;
  code: string;
  name: string;
  base_balance: string;
}
interface BalanceSheetGroup {
  type: string;
  total_base: string;
  lines: BalanceSheetLine[];
}
interface BalanceSheetReport {
  as_of: string;
  base_currency: string;
  groups: BalanceSheetGroup[];
  assets_total_base: string;
  liabilities_total_base: string;
  equity_total_base: string;
  income_total_base: string;
  expense_total_base: string;
}

interface FxRevaluationLine {
  account_id: number;
  code: string;
  name: string;
  type: string;
  currency: string;
  face_balance: string;
  original_base: string;
  revalued_base: string | null;
  rate: string | null;
  rate_as_of: string | null;
  unrealised_gain_loss: string | null;
  rate_missing: boolean;
}
interface FxRevaluationReport {
  as_of: string;
  base_currency: string;
  lines: FxRevaluationLine[];
  total_unrealised_gain: string;
  total_unrealised_loss: string;
  missing_rates: string[];
}

interface AgingBuckets {
  current: string;
  d_1_30: string;
  d_31_60: string;
  d_61_90: string;
  d_90_plus: string;
  total: string;
}
interface AgingRow {
  party_id: string;
  party_name: string;
  currency: string;
  buckets: AgingBuckets;
}
interface AgingReport {
  as_of: string;
  kind: "AR" | "AP";
  rows: AgingRow[];
  totals_by_currency: Record<string, AgingBuckets>;
}

const asOf = ref<Date>(new Date());
const asOfStr = computed(() => asOf.value.toISOString().slice(0, 10));

const trialBalance = useQuery<TrialBalanceReport>({
  queryKey: ["trial-balance", asOfStr],
  queryFn: async () =>
    (await api.get<TrialBalanceReport>("/accounting/reports/trial-balance", {
      params: { as_of: asOfStr.value },
    })).data,
});

const balanceSheet = useQuery<BalanceSheetReport>({
  queryKey: ["balance-sheet", asOfStr],
  queryFn: async () =>
    (await api.get<BalanceSheetReport>("/accounting/reports/balance-sheet", {
      params: { as_of: asOfStr.value },
    })).data,
});

const fxReval = useQuery<FxRevaluationReport>({
  queryKey: ["fx-revaluation", asOfStr],
  queryFn: async () =>
    (await api.get<FxRevaluationReport>("/accounting/reports/fx-revaluation", {
      params: { as_of: asOfStr.value },
    })).data,
});

const arAging = useQuery<AgingReport>({
  queryKey: ["ar-aging", asOfStr],
  queryFn: async () =>
    (await api.get<AgingReport>("/accounting/reports/ar-aging", {
      params: { as_of: asOfStr.value },
    })).data,
});

const apAging = useQuery<AgingReport>({
  queryKey: ["ap-aging", asOfStr],
  queryFn: async () =>
    (await api.get<AgingReport>("/accounting/reports/ap-aging", {
      params: { as_of: asOfStr.value },
    })).data,
});

function formatRate(v: string | null) {
  if (v === null) return "—";
  const n = Number(v);
  return Number.isFinite(n)
    ? n.toLocaleString(undefined, { minimumFractionDigits: 2, maximumFractionDigits: 8 })
    : v;
}

function gainLossSeverity(v: string | null) {
  if (v === null) return "secondary";
  const n = Number(v);
  if (n > 0) return "success";
  if (n < 0) return "danger";
  return "secondary";
}

function typeSeverity(t: string) {
  switch (t) {
    case "ASSET": return "info";
    case "LIABILITY": return "warn";
    case "EQUITY": return "success";
    case "INCOME": return "success";
    case "EXPENSE":
    case "COGS": return "danger";
    default: return undefined;
  }
}
</script>

<template>
  <section>
    <PageHeader eyebrow="04 / Accounting" title="Accounting Reports">
      <template #lede>
        Trial balance and balance sheet, consolidated in the base currency.
        Per-currency breakdown is shown on the trial balance.
      </template>
      <label class="as-of">
        As of
        <DatePicker v-model="asOf" date-format="yy-mm-dd" show-icon />
      </label>
    </PageHeader>

    <Tabs value="trial-balance" class="card-tabs">
      <TabList>
        <Tab value="trial-balance">Trial Balance</Tab>
        <Tab value="balance-sheet">Balance Sheet</Tab>
        <Tab value="fx-revaluation">FX Revaluation</Tab>
        <Tab value="ar-aging">AR Aging</Tab>
        <Tab value="ap-aging">AP Aging</Tab>
      </TabList>
      <TabPanels>
      <TabPanel value="trial-balance">
        <div v-if="trialBalance.data.value" class="meta">
          <span>Base currency <strong>{{ trialBalance.data.value.base_currency }}</strong></span>
          <span>Total debit <strong class="num">{{ formatAmount(trialBalance.data.value.total_debit_base, trialBalance.data.value.base_currency) }}</strong></span>
          <span>Total credit <strong class="num">{{ formatAmount(trialBalance.data.value.total_credit_base, trialBalance.data.value.base_currency) }}</strong></span>
        </div>
        <DataTable
          :value="trialBalance.data.value?.rows ?? []"
          :loading="trialBalance.isLoading.value"
          data-key="account_id"
          striped-rows
        >
          <template #empty>
            <EmptyState icon="inbox" title="Nothing posted yet" message="No postings as of this date." />
          </template>
          <Column header="Code" body-class="cell-nowrap">
            <template #body="{ data: r }"><span class="code">{{ r.code }}</span></template>
          </Column>
          <Column field="name" header="Account" />
          <Column header="Type">
            <template #body="{ data: r }">
              <Tag :value="r.type" :severity="typeSeverity(r.type)" />
            </template>
          </Column>
          <Column field="currency" header="Ccy" body-class="cell-nowrap cell-muted" />
          <Column header="Debit" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }" body-class="cell-nowrap">
            <template #body="{ data: r }">
              <span class="num">{{ formatAmount(r.debit, r.currency) }}</span>
            </template>
          </Column>
          <Column header="Credit" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }" body-class="cell-nowrap">
            <template #body="{ data: r }">
              <span class="num">{{ formatAmount(r.credit, r.currency) }}</span>
            </template>
          </Column>
          <Column header="Base Debit" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }" body-class="cell-nowrap">
            <template #body="{ data: r }">
              <span class="num muted">{{ formatAmount(r.base_debit, trialBalance.data.value?.base_currency) }}</span>
            </template>
          </Column>
          <Column header="Base Credit" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }" body-class="cell-nowrap">
            <template #body="{ data: r }">
              <span class="num muted">{{ formatAmount(r.base_credit, trialBalance.data.value?.base_currency) }}</span>
            </template>
          </Column>
        </DataTable>
      </TabPanel>

      <TabPanel value="balance-sheet">
        <div v-if="balanceSheet.data.value" class="meta">
          <span>Base currency <strong>{{ balanceSheet.data.value.base_currency }}</strong></span>
          <span>Assets <strong class="num">{{ formatAmount(balanceSheet.data.value.assets_total_base, balanceSheet.data.value.base_currency) }}</strong></span>
          <span>Liabilities <strong class="num">{{ formatAmount(balanceSheet.data.value.liabilities_total_base, balanceSheet.data.value.base_currency) }}</strong></span>
          <span>Equity <strong class="num">{{ formatAmount(balanceSheet.data.value.equity_total_base, balanceSheet.data.value.base_currency) }}</strong></span>
        </div>
        <div v-if="balanceSheet.isLoading.value" class="empty-mini">Loading…</div>
        <EmptyState v-else-if="!(balanceSheet.data.value?.groups?.length)" title="No balances" message="No balances as of this date." />
        <div v-else class="bs-groups">
          <div v-for="g in balanceSheet.data.value.groups" :key="g.type" class="bs-group">
            <div class="bs-group-head">
              <Tag :value="g.type" :severity="typeSeverity(g.type)" />
              <span class="bs-total num">
                {{ formatAmount(g.total_base, balanceSheet.data.value.base_currency) }}
                {{ balanceSheet.data.value.base_currency }}
              </span>
            </div>
            <ul class="bs-lines">
              <li v-for="line in g.lines" :key="line.account_id">
                <span class="bs-code code">{{ line.code }}</span>
                <span class="bs-name">{{ line.name }}</span>
                <span class="bs-amt num">{{ formatAmount(line.base_balance, balanceSheet.data.value.base_currency) }}</span>
              </li>
            </ul>
          </div>
        </div>
      </TabPanel>

      <TabPanel value="fx-revaluation">
        <div v-if="fxReval.data.value" class="meta">
          <span class="meta-note">
            Unrealised gain/loss on open foreign-currency balances at the latest
            rate on or before <strong class="num">{{ fxReval.data.value.as_of }}</strong>.
          </span>
          <span>Base currency <strong>{{ fxReval.data.value.base_currency }}</strong></span>
          <span>Total gain
            <strong class="num gain">{{ formatAmount(fxReval.data.value.total_unrealised_gain, fxReval.data.value.base_currency) }}</strong>
          </span>
          <span>Total loss
            <strong class="num loss">{{ formatAmount(fxReval.data.value.total_unrealised_loss, fxReval.data.value.base_currency) }}</strong>
          </span>
        </div>
        <Message
          v-if="fxReval.data.value?.missing_rates?.length"
          severity="warn"
          :closable="false"
          class="panel-message"
        >
          Missing rates for: {{ fxReval.data.value.missing_rates.join(", ") }}.
          Add them on the FX Rates page so those balances can be revalued.
        </Message>
        <DataTable
          :value="fxReval.data.value?.lines ?? []"
          :loading="fxReval.isLoading.value"
          data-key="account_id"
          striped-rows
        >
          <template #empty>
            <EmptyState icon="inbox" title="Nothing to revalue" message="No open foreign-currency balances as of this date." />
          </template>
          <Column header="Code" body-class="cell-nowrap">
            <template #body="{ data: r }"><span class="code">{{ r.code }}</span></template>
          </Column>
          <Column field="name" header="Account" />
          <Column header="Type">
            <template #body="{ data: r }">
              <Tag :value="r.type" :severity="typeSeverity(r.type)" />
            </template>
          </Column>
          <Column field="currency" header="Ccy" body-class="cell-nowrap cell-muted" />
          <Column header="Face balance" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }" body-class="cell-nowrap">
            <template #body="{ data: r }">
              <span class="num">{{ formatAmount(r.face_balance, r.currency) }}</span>
            </template>
          </Column>
          <Column header="Original base" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }" body-class="cell-nowrap">
            <template #body="{ data: r }">
              <span class="num muted">{{ formatAmount(r.original_base, fxReval.data.value?.base_currency) }}</span>
            </template>
          </Column>
          <Column header="Rate" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }" body-class="cell-nowrap">
            <template #body="{ data: r }">
              <span class="num muted">{{ formatRate(r.rate) }}</span>
              <span v-if="r.rate_as_of" class="rate-date">@{{ r.rate_as_of }}</span>
            </template>
          </Column>
          <Column header="Revalued base" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }" body-class="cell-nowrap">
            <template #body="{ data: r }">
              <span v-if="r.revalued_base !== null" class="num">
                {{ formatAmount(r.revalued_base, fxReval.data.value?.base_currency) }}
              </span>
              <span v-else class="muted">—</span>
            </template>
          </Column>
          <Column header="Unrealised" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }" body-class="cell-nowrap">
            <template #body="{ data: r }">
              <Tag
                v-if="r.unrealised_gain_loss !== null"
                :value="formatAmount(r.unrealised_gain_loss, fxReval.data.value?.base_currency)"
                :severity="gainLossSeverity(r.unrealised_gain_loss)"
              />
              <span v-else class="muted">rate missing</span>
            </template>
          </Column>
        </DataTable>
      </TabPanel>

      <TabPanel value="ar-aging">
        <div v-if="arAging.data.value" class="meta">
          <span class="meta-note">Open customer balances (SENT + PARTIAL) bucketed by days past due.</span>
          <span v-for="(b, ccy) in arAging.data.value.totals_by_currency" :key="ccy">
            {{ ccy }} <strong class="num">{{ formatAmount(b.total, ccy) }}</strong>
          </span>
        </div>
        <DataTable
          :value="arAging.data.value?.rows ?? []"
          :loading="arAging.isLoading.value"
          data-key="party_id"
          striped-rows
        >
          <template #empty>
            <EmptyState icon="circle-check" title="No open receivables" message="No open AR as of this date." />
          </template>
          <Column field="party_name" header="Customer" />
          <Column field="currency" header="Ccy" style="width: 70px" body-class="cell-nowrap cell-muted" />
          <Column header="Current" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }" body-class="cell-nowrap">
            <template #body="{ data: r }"><span class="num">{{ formatAmount(r.buckets.current, r.currency) }}</span></template>
          </Column>
          <Column header="1–30" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }" body-class="cell-nowrap">
            <template #body="{ data: r }"><span class="num">{{ formatAmount(r.buckets.d_1_30, r.currency) }}</span></template>
          </Column>
          <Column header="31–60" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }" body-class="cell-nowrap">
            <template #body="{ data: r }"><span class="num">{{ formatAmount(r.buckets.d_31_60, r.currency) }}</span></template>
          </Column>
          <Column header="61–90" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }" body-class="cell-nowrap">
            <template #body="{ data: r }"><span class="num">{{ formatAmount(r.buckets.d_61_90, r.currency) }}</span></template>
          </Column>
          <Column header="90+" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }" body-class="cell-nowrap">
            <template #body="{ data: r }"><span class="num loss">{{ formatAmount(r.buckets.d_90_plus, r.currency) }}</span></template>
          </Column>
          <Column header="Total" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }" body-class="cell-nowrap">
            <template #body="{ data: r }"><span class="num strong">{{ formatAmount(r.buckets.total, r.currency) }}</span></template>
          </Column>
        </DataTable>
      </TabPanel>

      <TabPanel value="ap-aging">
        <div v-if="apAging.data.value" class="meta">
          <span class="meta-note">Open vendor bills (OPEN + PARTIAL) bucketed by days past due.</span>
          <span v-for="(b, ccy) in apAging.data.value.totals_by_currency" :key="ccy">
            {{ ccy }} <strong class="num">{{ formatAmount(b.total, ccy) }}</strong>
          </span>
        </div>
        <DataTable
          :value="apAging.data.value?.rows ?? []"
          :loading="apAging.isLoading.value"
          data-key="party_id"
          striped-rows
        >
          <template #empty>
            <EmptyState icon="circle-check" title="No open payables" message="No open AP as of this date." />
          </template>
          <Column field="party_name" header="Vendor" />
          <Column field="currency" header="Ccy" style="width: 70px" body-class="cell-nowrap cell-muted" />
          <Column header="Current" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }" body-class="cell-nowrap">
            <template #body="{ data: r }"><span class="num">{{ formatAmount(r.buckets.current, r.currency) }}</span></template>
          </Column>
          <Column header="1–30" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }" body-class="cell-nowrap">
            <template #body="{ data: r }"><span class="num">{{ formatAmount(r.buckets.d_1_30, r.currency) }}</span></template>
          </Column>
          <Column header="31–60" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }" body-class="cell-nowrap">
            <template #body="{ data: r }"><span class="num">{{ formatAmount(r.buckets.d_31_60, r.currency) }}</span></template>
          </Column>
          <Column header="61–90" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }" body-class="cell-nowrap">
            <template #body="{ data: r }"><span class="num">{{ formatAmount(r.buckets.d_61_90, r.currency) }}</span></template>
          </Column>
          <Column header="90+" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }" body-class="cell-nowrap">
            <template #body="{ data: r }"><span class="num loss">{{ formatAmount(r.buckets.d_90_plus, r.currency) }}</span></template>
          </Column>
          <Column header="Total" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }" body-class="cell-nowrap">
            <template #body="{ data: r }"><span class="num strong">{{ formatAmount(r.buckets.total, r.currency) }}</span></template>
          </Column>
        </DataTable>
      </TabPanel>
      </TabPanels>
    </Tabs>
  </section>
</template>

<style scoped>
.as-of { display: flex; align-items: center; gap: 12px; font-size: 13px; color: var(--wb-muted); }
.meta {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 32px;
  padding: 18px 24px;
  font-size: 13px;
  color: var(--wb-muted);
  border-bottom: 1px solid var(--wb-line);
}
.meta strong { color: var(--wb-ink); font-weight: 500; }
.meta-note { flex-basis: 100%; }
.num { font-variant-numeric: tabular-nums; white-space: nowrap; }
.muted { color: var(--wb-muted); }
.strong { font-weight: 500; }
.meta .gain, .gain { color: var(--wb-green-fg); }
.meta .loss, .loss { color: var(--wb-red-fg); }
.code { font: 500 13px var(--wb-font-mono); color: var(--wb-muted); }
.panel-message { margin: 16px 24px; }

.bs-groups { display: flex; flex-direction: column; gap: 20px; padding: 24px; }
.bs-group { border: 1px solid var(--wb-line); }
.bs-group-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  padding: 16px 20px;
  border-bottom: 1px solid var(--wb-line);
}
.bs-total {
  font: 500 18px var(--wb-font-display);
  letter-spacing: -0.4px;
  color: var(--wb-ink);
}
.bs-lines { list-style: none; margin: 0; padding: 0; }
.bs-lines li {
  display: grid;
  grid-template-columns: 80px minmax(0, 1fr) auto;
  gap: 16px;
  align-items: center;
  padding: 12px 20px;
  border-bottom: 1px solid var(--wb-line);
}
.bs-lines li:last-child { border-bottom: 0; }
.bs-lines li:hover { background: var(--wb-surface-2); }
.bs-amt { font-weight: 500; }
@media (max-width: 720px) {
  .meta { padding: 14px 16px; gap: 6px 20px; }
  .bs-groups { padding: 16px; }
  .bs-group-head, .bs-lines li { padding-left: 14px; padding-right: 14px; }
  .bs-lines li { grid-template-columns: 64px minmax(0, 1fr) auto; gap: 10px; }
}

.rate-date { margin-left: 0.4rem; color: var(--wb-muted); font-size: 0.78em; }
</style>
