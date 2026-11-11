<script setup lang="ts">
// Picks the open invoice a held payment is approved against. Searches
// GET /invoices?open=true&q= (invoice number or customer name) as the user
// types; lists the payment's currency unless "All currencies" is on. Warns,
// never blocks: a currency mismatch or a payment above the balance due is
// still the reviewer's call, and the server's manual-review rules decide.
import { computed, ref, watch } from "vue";
import { useQuery } from "@tanstack/vue-query";
import AutoComplete, { type AutoCompleteCompleteEvent } from "primevue/autocomplete";
import Checkbox from "primevue/checkbox";
import Message from "primevue/message";
import { api } from "@/api/client";
import { formatMoney } from "@/utils/money";
import { decIsZero, decSub } from "@/components/dashboard/format";

interface OpenInvoice {
  invoice_id: string;
  invoice_number: string | null;
  customer_id: string;
  currency: string;
  balance_due: string;
  due_date: string | null;
  status: string;
}

interface Customer {
  customer_id: string;
  name: string;
}

const props = defineProps<{ currency: string; amount: string }>();
const invoiceId = defineModel<string>({ required: true });

// Enough rows for a typed search; the currency filter runs on this page.
const LIMIT = 100;

const { data: customers } = useQuery<Customer[]>({
  queryKey: ["customers"],
  queryFn: async () => (await api.get<Customer[]>("/customers")).data,
});
const customerName = computed(() => {
  const map: Record<string, string> = {};
  for (const c of customers.value ?? []) map[c.customer_id] = c.name;
  return map;
});

const allCurrencies = ref(false);
const results = ref<OpenInvoice[]>([]);
// The AutoComplete's model: the typed text, or the invoice once one is chosen.
const chosen = ref<OpenInvoice | string | null>(null);
let seq = 0;

const suggestions = computed(() =>
  allCurrencies.value ? results.value : results.value.filter((i) => i.currency === props.currency),
);

async function search(e: AutoCompleteCompleteEvent) {
  const mine = ++seq;
  const params = new URLSearchParams({ open: "true", limit: String(LIMIT) });
  const q = e.query.trim();
  if (q) params.set("q", q);
  const rows = (await api.get<OpenInvoice[]>(`/invoices?${params}`)).data;
  // A slower, older search must not replace a newer one's rows.
  if (mine === seq) results.value = rows;
}

const selected = computed(() => (chosen.value && typeof chosen.value === "object" ? chosen.value : null));
watch(selected, (inv) => {
  invoiceId.value = inv?.invoice_id ?? "";
});
// Cleared from outside (after an approval): drop the chosen invoice too.
watch(invoiceId, (id) => {
  if (!id && selected.value) chosen.value = null;
});

function invoiceLabel(inv: OpenInvoice): string {
  const who = customerName.value[inv.customer_id] ?? inv.customer_id.slice(0, 8);
  return `${inv.invoice_number ?? "Draft"} · ${who}`;
}

const currencyMismatch = computed(() => !!selected.value && selected.value.currency !== props.currency);
// Compared only in one currency: across currencies the amounts do not compare.
const overBalance = computed(() => {
  const inv = selected.value;
  if (!inv || currencyMismatch.value) return false;
  const diff = decSub(props.amount, inv.balance_due);
  return !decIsZero(diff) && !diff.startsWith("-");
});
</script>

<template>
  <div class="invoice-picker">
    <!-- Above the input: the open suggestion list would cover it below. -->
    <label class="all-ccy">
      <Checkbox v-model="allCurrencies" binary input-id="review-all-currencies" />
      <span>All currencies</span>
      <span v-if="!allCurrencies" class="ccy-hint">(showing {{ currency }})</span>
    </label>
    <AutoComplete
      v-model="chosen"
      :suggestions="suggestions"
      :option-label="invoiceLabel"
      :delay="250"
      dropdown
      fluid
      input-id="review-invoice"
      placeholder="Invoice number or customer"
      :empty-search-message="allCurrencies ? 'No open invoice matches' : `No open ${currency} invoice matches`"
      @complete="search"
    >
      <template #option="{ option }">
        <div class="opt" :data-invoice="option.invoice_id">
          <span class="opt-number">{{ option.invoice_number ?? 'Draft' }}</span>
          <span class="opt-amount num">{{ formatMoney(option.balance_due, option.currency) }}</span>
          <span class="opt-customer">{{ customerName[option.customer_id] ?? option.customer_id.slice(0, 8) }}</span>
          <span class="opt-due">{{ option.due_date ? `Due ${option.due_date}` : 'No due date' }}</span>
        </div>
      </template>
    </AutoComplete>


    <div v-if="selected" class="picked" data-testid="picked-invoice">
      <strong>{{ selected.invoice_number ?? 'Draft' }}</strong> · balance due <strong class="num">{{ formatMoney(selected.balance_due, selected.currency) }}</strong>
      <template v-if="selected.due_date"> · due {{ selected.due_date }}</template>
    </div>

    <Message v-if="currencyMismatch" severity="warn" :closable="false" data-testid="warn-currency">
      This invoice is in {{ selected!.currency }}; the payment is in {{ currency }}. Approving does not convert it.
    </Message>
    <Message v-if="overBalance" severity="warn" :closable="false" data-testid="warn-over-balance">
      The payment ({{ formatMoney(amount, currency) }}) is more than the balance due
      ({{ formatMoney(selected!.balance_due, selected!.currency) }}).
    </Message>
  </div>
</template>

<style scoped>
.invoice-picker { display: flex; flex-direction: column; gap: 10px; min-width: 0; }
.opt {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: 2px 16px;
  width: 100%;
  min-width: 0;
}
.opt-number { font-weight: 500; color: var(--wb-ink); }
.opt-amount { text-align: right; font-weight: 500; white-space: nowrap; }
.opt-customer, .opt-due { font-size: 13px; color: var(--wb-muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.opt-due { text-align: right; }
.num { font-variant-numeric: tabular-nums; }
.all-ccy { align-self: flex-start; display: inline-flex; align-items: center; gap: 8px; font-size: 13px; color: var(--wb-muted); cursor: pointer; }
.ccy-hint { color: var(--wb-muted-soft); }
.picked { font-size: 13px; color: var(--wb-muted); }
.picked strong { color: var(--wb-ink); font-weight: 500; }
</style>
