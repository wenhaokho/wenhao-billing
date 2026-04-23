<script setup lang="ts">
import { ref, computed } from "vue";
import { useQuery } from "@tanstack/vue-query";
import DataTable from "primevue/datatable";
import Column from "primevue/column";
import DatePicker from "primevue/calendar";
import TabView from "primevue/tabview";
import TabPanel from "primevue/tabpanel";
import Tag from "primevue/tag";
import Message from "primevue/message";
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
    case "LIABILITY": return "warning";
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
    <header class="page-header">
      <div>
        <h1>Accounting Reports</h1>
        <p class="subtitle">
          Trial balance and balance sheet, consolidated in the base currency.
          Per-currency breakdown is shown on the trial balance.
        </p>
      </div>
      <div class="page-actions">
        <label class="as-of">
          As of
          <DatePicker v-model="asOf" date-format="yy-mm-dd" show-icon />
        </label>
      </div>
    </header>

    <TabView>
      <TabPanel header="Trial Balance">
        <div v-if="trialBalance.data.value" class="meta">
          Base currency:
          <strong>{{ trialBalance.data.value.base_currency }}</strong>
          · Total debit
          <span class="num">{{ formatAmount(trialBalance.data.value.total_debit_base, trialBalance.data.value.base_currency) }}</span>
          · Total credit
          <span class="num">{{ formatAmount(trialBalance.data.value.total_credit_base, trialBalance.data.value.base_currency) }}</span>
        </div>
        <DataTable
          :value="trialBalance.data.value?.rows ?? []"
          :loading="trialBalance.isLoading.value"
          data-key="account_id"
          striped-rows
          size="small"
        >
          <template #empty>
            <div class="empty-state">
              <i class="pi pi-inbox" />
              <div>No postings as of this date.</div>
            </div>
          </template>
          <Column field="code" header="Code" />
          <Column field="name" header="Account" />
          <Column header="Type">
            <template #body="{ data: r }">
              <Tag :value="r.type" :severity="typeSeverity(r.type)" />
            </template>
          </Column>
          <Column field="currency" header="Ccy" />
          <Column header="Debit" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }">
            <template #body="{ data: r }">
              <span class="num">{{ formatAmount(r.debit, r.currency) }}</span>
            </template>
          </Column>
          <Column header="Credit" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }">
            <template #body="{ data: r }">
              <span class="num">{{ formatAmount(r.credit, r.currency) }}</span>
            </template>
          </Column>
          <Column header="Base Debit" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }">
            <template #body="{ data: r }">
              <span class="num muted">{{ formatAmount(r.base_debit, trialBalance.data.value?.base_currency) }}</span>
            </template>
          </Column>
          <Column header="Base Credit" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }">
            <template #body="{ data: r }">
              <span class="num muted">{{ formatAmount(r.base_credit, trialBalance.data.value?.base_currency) }}</span>
            </template>
          </Column>
        </DataTable>
      </TabPanel>

      <TabPanel header="Balance Sheet">
        <div v-if="balanceSheet.data.value" class="meta">
          Base currency:
          <strong>{{ balanceSheet.data.value.base_currency }}</strong>
          · Assets
          <span class="num">{{ formatAmount(balanceSheet.data.value.assets_total_base, balanceSheet.data.value.base_currency) }}</span>
          · Liabilities
          <span class="num">{{ formatAmount(balanceSheet.data.value.liabilities_total_base, balanceSheet.data.value.base_currency) }}</span>
          · Equity
          <span class="num">{{ formatAmount(balanceSheet.data.value.equity_total_base, balanceSheet.data.value.base_currency) }}</span>
        </div>
        <div v-if="balanceSheet.isLoading.value" class="empty-state">Loading…</div>
        <div v-else-if="!(balanceSheet.data.value?.groups?.length)" class="empty-state">
          <i class="pi pi-inbox" />
          <div>No balances as of this date.</div>
        </div>
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
                <span class="bs-code"><code>{{ line.code }}</code></span>
                <span class="bs-name">{{ line.name }}</span>
                <span class="bs-amt num">{{ formatAmount(line.base_balance, balanceSheet.data.value.base_currency) }}</span>
              </li>
            </ul>
          </div>
        </div>
      </TabPanel>

      <TabPanel header="FX Revaluation">
        <div v-if="fxReval.data.value" class="meta">
          Unrealised gain/loss on open foreign-currency balances at the latest
          rate on or before <strong>{{ fxReval.data.value.as_of }}</strong>.
          Base currency: <strong>{{ fxReval.data.value.base_currency }}</strong>
          · Total gain
          <span class="num" style="color: #059669">
            {{ formatAmount(fxReval.data.value.total_unrealised_gain, fxReval.data.value.base_currency) }}
          </span>
          · Total loss
          <span class="num" style="color: #dc2626">
            {{ formatAmount(fxReval.data.value.total_unrealised_loss, fxReval.data.value.base_currency) }}
          </span>
        </div>
        <Message
          v-if="fxReval.data.value?.missing_rates?.length"
          severity="warn"
          :closable="false"
        >
          Missing rates for: {{ fxReval.data.value.missing_rates.join(", ") }}.
          Add them on the FX Rates page so those balances can be revalued.
        </Message>
        <DataTable
          :value="fxReval.data.value?.lines ?? []"
          :loading="fxReval.isLoading.value"
          data-key="account_id"
          striped-rows
          size="small"
        >
          <template #empty>
            <div class="empty-state">
              <i class="pi pi-inbox" />
              <div>No open foreign-currency balances as of this date.</div>
            </div>
          </template>
          <Column field="code" header="Code" />
          <Column field="name" header="Account" />
          <Column header="Type">
            <template #body="{ data: r }">
              <Tag :value="r.type" :severity="typeSeverity(r.type)" />
            </template>
          </Column>
          <Column field="currency" header="Ccy" />
          <Column header="Face balance" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }">
            <template #body="{ data: r }">
              <span class="num">{{ formatAmount(r.face_balance, r.currency) }}</span>
            </template>
          </Column>
          <Column header="Original base" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }">
            <template #body="{ data: r }">
              <span class="num muted">{{ formatAmount(r.original_base, fxReval.data.value?.base_currency) }}</span>
            </template>
          </Column>
          <Column header="Rate" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }">
            <template #body="{ data: r }">
              <span class="num muted">{{ formatRate(r.rate) }}</span>
              <span v-if="r.rate_as_of" class="rate-date">@{{ r.rate_as_of }}</span>
            </template>
          </Column>
          <Column header="Revalued base" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }">
            <template #body="{ data: r }">
              <span v-if="r.revalued_base !== null" class="num">
                {{ formatAmount(r.revalued_base, fxReval.data.value?.base_currency) }}
              </span>
              <span v-else class="muted">—</span>
            </template>
          </Column>
          <Column header="Unrealised" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }">
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

      <TabPanel header="AR Aging">
        <div v-if="arAging.data.value" class="meta">
          Open customer balances (SENT + PARTIAL) bucketed by days past due.
          <template v-for="(b, ccy) in arAging.data.value.totals_by_currency" :key="ccy">
            · <strong>{{ ccy }}</strong>
            <span class="num">{{ formatAmount(b.total, ccy) }}</span>
          </template>
        </div>
        <DataTable
          :value="arAging.data.value?.rows ?? []"
          :loading="arAging.isLoading.value"
          data-key="party_id"
          striped-rows
          size="small"
        >
          <template #empty>
            <div class="empty-state">
              <i class="pi pi-check-circle" />
              <div>No open AR as of this date.</div>
            </div>
          </template>
          <Column field="party_name" header="Customer" />
          <Column field="currency" header="Ccy" style="width: 70px" />
          <Column header="Current" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }">
            <template #body="{ data: r }"><span class="num">{{ formatAmount(r.buckets.current, r.currency) }}</span></template>
          </Column>
          <Column header="1–30" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }">
            <template #body="{ data: r }"><span class="num">{{ formatAmount(r.buckets.d_1_30, r.currency) }}</span></template>
          </Column>
          <Column header="31–60" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }">
            <template #body="{ data: r }"><span class="num">{{ formatAmount(r.buckets.d_31_60, r.currency) }}</span></template>
          </Column>
          <Column header="61–90" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }">
            <template #body="{ data: r }"><span class="num">{{ formatAmount(r.buckets.d_61_90, r.currency) }}</span></template>
          </Column>
          <Column header="90+" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }">
            <template #body="{ data: r }"><span class="num" style="color: #dc2626">{{ formatAmount(r.buckets.d_90_plus, r.currency) }}</span></template>
          </Column>
          <Column header="Total" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }">
            <template #body="{ data: r }"><span class="num" style="font-weight:600">{{ formatAmount(r.buckets.total, r.currency) }}</span></template>
          </Column>
        </DataTable>
      </TabPanel>

      <TabPanel header="AP Aging">
        <div v-if="apAging.data.value" class="meta">
          Open vendor bills (OPEN + PARTIAL) bucketed by days past due.
          <template v-for="(b, ccy) in apAging.data.value.totals_by_currency" :key="ccy">
            · <strong>{{ ccy }}</strong>
            <span class="num">{{ formatAmount(b.total, ccy) }}</span>
          </template>
        </div>
        <DataTable
          :value="apAging.data.value?.rows ?? []"
          :loading="apAging.isLoading.value"
          data-key="party_id"
          striped-rows
          size="small"
        >
          <template #empty>
            <div class="empty-state">
              <i class="pi pi-check-circle" />
              <div>No open AP as of this date.</div>
            </div>
          </template>
          <Column field="party_name" header="Vendor" />
          <Column field="currency" header="Ccy" style="width: 70px" />
          <Column header="Current" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }">
            <template #body="{ data: r }"><span class="num">{{ formatAmount(r.buckets.current, r.currency) }}</span></template>
          </Column>
          <Column header="1–30" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }">
            <template #body="{ data: r }"><span class="num">{{ formatAmount(r.buckets.d_1_30, r.currency) }}</span></template>
          </Column>
          <Column header="31–60" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }">
            <template #body="{ data: r }"><span class="num">{{ formatAmount(r.buckets.d_31_60, r.currency) }}</span></template>
          </Column>
          <Column header="61–90" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }">
            <template #body="{ data: r }"><span class="num">{{ formatAmount(r.buckets.d_61_90, r.currency) }}</span></template>
          </Column>
          <Column header="90+" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }">
            <template #body="{ data: r }"><span class="num" style="color: #dc2626">{{ formatAmount(r.buckets.d_90_plus, r.currency) }}</span></template>
          </Column>
          <Column header="Total" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }">
            <template #body="{ data: r }"><span class="num" style="font-weight:600">{{ formatAmount(r.buckets.total, r.currency) }}</span></template>
          </Column>
        </DataTable>
      </TabPanel>
    </TabView>
  </section>
</template>

<style scoped>
.as-of { display: flex; align-items: center; gap: 0.5rem; font-size: 0.85rem; color: #475569; }
.meta { font-size: 0.85rem; color: #475569; margin: 0 0 0.75rem; }
.meta .num { font-weight: 600; color: var(--color-text); }
.num { font-variant-numeric: tabular-nums; }
.muted { color: var(--color-text-muted); }

.bs-groups { display: flex; flex-direction: column; gap: 1.25rem; }
.bs-group {
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  padding: 0.9rem 1.1rem;
}
.bs-group-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding-bottom: 0.5rem;
  margin-bottom: 0.5rem;
  border-bottom: 1px solid var(--color-border);
}
.bs-total { font-weight: 700; font-size: 1.05rem; }
.bs-lines { list-style: none; margin: 0; padding: 0; }
.bs-lines li {
  display: grid;
  grid-template-columns: 80px 1fr auto;
  gap: 0.75rem;
  padding: 0.25rem 0;
  font-size: 0.88rem;
}
.bs-code code { background: var(--color-surface-alt); padding: 0.05rem 0.35rem; border-radius: 4px; font-size: 0.82em; }
.bs-amt { font-weight: 500; }

.rate-date { margin-left: 0.4rem; color: var(--color-text-muted); font-size: 0.78em; }
</style>
