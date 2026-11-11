<script setup lang="ts">
// Needs attention (mockup): overdue invoices, oldest due date first (the API
// sends at most six). Amounts are native, in the invoice's own currency: the
// expected cash (balance less withholding not yet booked), with the balance due
// beside it when the two differ. "Send reminder" comes with the reminders work.
import EmptyState from "@/components/EmptyState.vue";
import Tag from "primevue/tag";
import { formatAmount } from "@/utils/money";
import { decSub, decIsZero } from "./format";
import type { AttentionRow } from "./types";

defineProps<{ rows: AttentionRow[] }>();

const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

/** "2026-11-02" -> "2 Nov 2026", without Date() timezone drift. */
function formatDate(iso: string): string {
  const [y, m, d] = iso.slice(0, 10).split("-");
  return `${Number(d)} ${MONTHS[Number(m) - 1] ?? m} ${y}`;
}

function differs(r: AttentionRow): boolean {
  return !decIsZero(decSub(r.balance_due, r.expected_cash));
}
</script>

<template>
  <div class="card na" data-testid="needs-attention-card">
    <div class="na-head">
      <div>
        <h2 class="dc-title">Needs attention</h2>
        <p class="dc-sub">Overdue invoices, oldest first.</p>
      </div>
      <RouterLink to="/invoices" class="dc-link">All invoices ↗</RouterLink>
    </div>

    <EmptyState
      v-if="!rows.length"
      icon="circle-check"
      title="Nothing overdue"
      message="Every open invoice is within its due date."
    />

    <ul v-else class="na-rows">
      <li v-for="r in rows" :key="r.invoice_id" class="na-row" :data-invoice="r.invoice_id">
        <div class="na-who">
          <Tag class="na-late" severity="danger" :value="`${r.days_late}d late`" />
          <RouterLink v-if="r.customer_id" :to="`/customers/${r.customer_id}`" class="na-customer">{{ r.customer_name ?? "Unnamed customer" }}</RouterLink>
          <span v-else class="na-customer none">No customer</span>
        </div>
        <span class="na-amt num">{{ formatAmount(r.expected_cash, r.currency) }} {{ r.currency }}</span>
        <div class="na-meta">
          <RouterLink :to="{ path: `/invoices/${r.invoice_id}/edit`, query: { from: '/dashboard' } }" class="na-doc">{{ r.invoice_number ?? "(no number)" }}</RouterLink>
          · due {{ formatDate(r.due_date) }}
        </div>
        <span v-if="differs(r)" class="na-balance num" title="Balance due before withholding tax not yet booked">of {{ formatAmount(r.balance_due, r.currency) }} due</span>
      </li>
    </ul>
  </div>
</template>

<style scoped src="./dash-card.css"></style>
<style scoped>
.na { display: flex; flex-direction: column; min-width: 0; }
.na-head {
  display: flex;
  justify-content: space-between;
  align-items: flex-end;
  flex-wrap: wrap;
  gap: 12px 16px;
  padding: 24px 28px 18px;
  border-bottom: 1px solid var(--wb-line);
}
.na-rows { list-style: none; padding: 0; margin: 0; }
.na-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: 4px 14px;
  align-items: center;
  padding: 16px 28px;
  border-bottom: 1px solid var(--wb-line);
}
.na-row:last-child { border-bottom: 0; }
.na-row:hover { background: var(--wb-surface-2); }
.na-who { display: flex; align-items: center; gap: 10px; min-width: 0; }
.na-late { flex: none; white-space: nowrap; }
.na-customer {
  font-weight: 500;
  color: var(--wb-ink);
  text-decoration: none;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
a.na-customer:hover { text-decoration: underline; }
.na-customer.none { color: var(--wb-muted); font-weight: 400; }
.na-amt { font-weight: 500; white-space: nowrap; text-align: right; color: var(--wb-ink); }
.na-meta { font-size: 13px; color: var(--wb-muted); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; min-width: 0; }
.na-doc { color: var(--wb-muted); }
.na-balance { font-size: 12px; color: var(--wb-muted-soft); white-space: nowrap; text-align: right; }
@media (max-width: 640px) {
  .na-head { padding: 18px 18px 14px; }
  .na-row { padding: 14px 18px; gap: 4px 10px; }
}
</style>
