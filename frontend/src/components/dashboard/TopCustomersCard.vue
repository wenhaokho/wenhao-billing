<script setup lang="ts">
// Top customers (mockup): the ten largest by invoiced amount in the period,
// base-currency equivalent, each with its share bar and any overdue balance;
// then the top three's share (concentration) and everyone else combined.
import { computed, ref } from "vue";
import EmptyState from "@/components/EmptyState.vue";
import { formatAmount } from "@/utils/money";
import { decIsZero, formatCompact, formatShare } from "./format";
import type { TopCustomers } from "./types";

const props = defineProps<{
  data: TopCustomers;
  base: string;
  periodWords: string;
  unconverted: string[] | null;
}>();

/** Top three share at or above this reads as a concentration risk. */
const CONCENTRATION_RISK = 50;

const rows = computed(() =>
  (props.data.rows ?? []).map((r, i) => ({
    ...r,
    rank: String(i + 1).padStart(2, "0"),
    compact: formatCompact(r.amount),
    title: `${formatAmount(r.amount, props.base)} ${props.base}`,
    pct: formatShare(r.share),
    width: `${Math.min(100, Math.max(0, r.share))}%`,
    color: i < 3 ? "var(--wb-red)" : "var(--wb-gold)",
    overdue: decIsZero(r.overdue) ? "" : `${formatCompact(r.overdue)} overdue`,
  })),
);

const hovered = ref<string | null>(null);

const risky = computed(() => (props.data.top3_share ?? 0) >= CONCENTRATION_RISK);
</script>

<template>
  <div class="card dc" data-testid="top-customers-card">
    <h2 class="dc-title">Top customers</h2>
    <p class="dc-sub">Share of invoiced revenue, last {{ periodWords }}, {{ base }} eq.</p>

    <EmptyState
      v-if="!rows.length"
      icon="users"
      title="No invoices in this period"
      message="Customers you invoice show here, largest first."
    />

    <template v-else>
      <ol class="top-rows">
        <li
          v-for="r in rows"
          :key="r.customer_id"
          class="top-row"
          :class="{ on: hovered === r.customer_id }"
          @mouseenter="hovered = r.customer_id"
          @mouseleave="hovered = null"
        >
          <span class="top-rank num">{{ r.rank }}</span>
          <RouterLink :to="`/customers/${r.customer_id}`" class="top-name" :title="r.name">{{ r.name }}</RouterLink>
          <span class="top-amt num" :title="r.title">
            {{ r.compact }} · <span class="top-pct">{{ r.pct }}</span><span v-if="r.overdue" class="bad top-due"> · {{ r.overdue }}</span>
          </span>
          <div class="top-track" aria-hidden="true">
            <div class="top-fill" :class="{ dim: hovered && hovered !== r.customer_id }" :style="{ width: r.width, background: r.color }" />
          </div>
        </li>
      </ol>

      <div class="dc-foot">
        <div>
          <div class="dc-label">Top 3 share</div>
          <div class="dc-figure num" :class="{ bad: risky }">{{ formatShare(data.top3_share) }}</div>
          <div class="dc-foot-sub">{{ risky ? "concentration risk" : "of invoiced" }}</div>
        </div>
        <div>
          <div class="dc-label">Other customers</div>
          <div class="dc-figure num">
            {{ data.others.count }}
            <span v-if="data.others.count" class="dc-aside">· {{ formatShare(data.others.share) }}</span>
          </div>
          <div class="dc-foot-sub num">{{ data.others.count ? `${formatCompact(data.others.amount)} combined` : "none beyond the top ten" }}</div>
        </div>
      </div>
    </template>

    <p v-if="unconverted?.length" class="dc-note">
      Not converted: {{ unconverted.join(", ") }} (no FX rate)
    </p>
  </div>
</template>

<style scoped src="./dash-card.css"></style>
<style scoped>
.top-rows { list-style: none; padding: 0; margin: 20px 0 0; display: flex; flex-direction: column; gap: 10px; flex: 1; justify-content: space-around; }
.top-row {
  display: grid;
  grid-template-columns: 18px minmax(0, 1fr) auto;
  gap: 4px 10px;
  align-items: center;
  padding: 4px 8px;
  margin: 0 -8px;
  transition: background 0.15s;
}
.top-row.on { background: var(--wb-surface-2); }
.top-rank { font: 500 11px var(--wb-font-mono); color: var(--wb-muted-soft); }
.top-name {
  font-size: 13.5px;
  color: var(--wb-ink);
  text-decoration: none;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.top-name:hover { text-decoration: underline; }
.top-amt { font-size: 13px; color: var(--wb-muted); white-space: nowrap; }
.top-pct { color: var(--wb-ink); font-weight: 500; }
.top-track { grid-column: 2 / span 2; height: 6px; background: var(--wb-tint); }
.top-fill { height: 100%; transition: opacity 0.15s; }
.top-fill.dim { opacity: 0.35; }
@media (max-width: 640px) {
  .top-amt { grid-column: 2 / span 2; white-space: normal; }
}
</style>
