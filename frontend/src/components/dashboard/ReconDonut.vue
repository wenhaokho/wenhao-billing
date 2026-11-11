<script setup lang="ts">
// Reconciliation health (mockup): payments received in the period split into
// auto-cleared by the engine, cleared by an admin, and held for review, as an
// SVG donut (colours are CSS tokens, so it follows the theme by itself) with
// its legend rows; then average engine confidence and matching aliases.
import { computed, ref } from "vue";
import EmptyState from "@/components/EmptyState.vue";
import type { ReconHealth } from "./types";

const props = defineProps<{ data: ReconHealth; periodWords: string }>();

const R = 43;
const C = 2 * Math.PI * R;
/** Gap between slices, in the circle's length units. */
const GAP = 3;

const total = computed(() => props.data.auto_cleared + props.data.manual + props.data.held);
const pct = (n: number) => (total.value ? Math.round((n / total.value) * 100) : 0);

const rows = computed(() => {
  const d = props.data;
  const list = [
    { key: "auto", label: "Auto-cleared", n: d.auto_cleared, color: "var(--wb-green-fg)" },
    { key: "manual", label: "Cleared manually", n: d.manual, color: "var(--wb-gold)" },
    { key: "held", label: "Held for review", n: d.held, color: "var(--wb-red)" },
  ];
  const slices = list.filter((r) => r.n > 0).length;
  let at = 0;
  return list.map((r) => {
    const len = total.value ? (r.n / total.value) * C : 0;
    const start = at;
    at += len;
    // A lone slice is the whole ring: no gap.
    const drawn = slices > 1 ? Math.max(len - GAP, 0.5) : len;
    return { ...r, pct: `${pct(r.n)}%`, dash: `${drawn} ${C - drawn}`, offset: -(start + (slices > 1 ? GAP / 2 : 0)) };
  });
});

const hovered = ref<string | null>(null);
const center = computed(() => {
  const h = rows.value.find((r) => r.key === hovered.value);
  return h ? { text: String(h.n), color: h.color } : { text: `${pct(props.data.auto_cleared)}%`, color: "var(--wb-ink)" };
});

const chartLabel = computed(() => {
  const r = rows.value;
  return `Donut chart of ${total.value} payments over the last ${props.periodWords}: ${r.map((x) => `${x.label} ${x.n} (${x.pct})`).join(", ")}.`;
});

const confidence = computed(() => {
  const c = props.data.avg_confidence;
  return c === null ? "—" : `${c.toFixed(1).replace(/\.0$/, "")}%`;
});
</script>

<template>
  <div class="card dc" data-testid="recon-card">
    <h2 class="dc-title">Reconciliation health</h2>
    <p class="dc-sub">Payments received in the last {{ periodWords }}, by how they cleared.</p>

    <EmptyState
      v-if="!total"
      icon="wallet"
      title="No payments"
      :message="`No payment came in during the last ${periodWords}.`"
    />

    <div v-else class="recon-body">
      <div class="donut" data-testid="recon-chart">
        <svg viewBox="0 0 100 100" role="img" :aria-label="chartLabel">
          <circle cx="50" cy="50" :r="R" class="track" />
          <circle
            v-for="r in rows"
            v-show="r.n > 0"
            :key="r.key"
            cx="50"
            cy="50"
            :r="R"
            class="slice"
            :class="{ on: hovered === r.key, dim: hovered && hovered !== r.key }"
            :stroke="r.color"
            :stroke-dasharray="r.dash"
            :stroke-dashoffset="r.offset"
            @mouseenter="hovered = r.key"
            @mouseleave="hovered = null"
          />
        </svg>
        <span class="donut-center num" :style="{ color: center.color }">{{ center.text }}</span>
      </div>
      <ul class="recon-rows">
        <li
          v-for="r in rows"
          :key="r.key"
          class="recon-row"
          :class="{ on: hovered === r.key }"
          :data-recon="r.key"
          @mouseenter="hovered = r.key"
          @mouseleave="hovered = null"
        >
          <span class="recon-name"><span class="swatch" :style="{ background: r.color }" />{{ r.label }}</span>
          <strong class="num">{{ r.n }} <span class="recon-pct">{{ r.pct }}</span></strong>
        </li>
      </ul>
    </div>

    <div class="dc-foot">
      <div>
        <div class="dc-label">Avg confidence</div>
        <div class="dc-figure num">{{ confidence }}</div>
      </div>
      <div>
        <div class="dc-label">Aliases in use</div>
        <div class="dc-figure num">
          {{ data.aliases_in_use }}
          <span class="dc-aside">· {{ data.customers_with_aliases }} customer{{ data.customers_with_aliases === 1 ? "" : "s" }}</span>
        </div>
      </div>
    </div>
    <RouterLink to="/review" class="dc-link recon-link">Open manual review ↗</RouterLink>
  </div>
</template>

<style scoped src="./dash-card.css"></style>
<style scoped>
.recon-body { display: flex; align-items: center; gap: 22px; margin-top: 24px; }
.donut { position: relative; width: 100px; height: 100px; flex: none; display: grid; place-items: center; }
.donut svg { position: absolute; inset: 0; width: 100%; height: 100%; overflow: visible; transform: rotate(-90deg); }
.track { fill: none; stroke: var(--wb-tint); stroke-width: 14; }
.slice {
  fill: none;
  stroke-width: 14;
  cursor: default;
  transition: opacity 0.15s, stroke-width 0.15s;
}
.slice.on { stroke-width: 17; }
.slice.dim { opacity: 0.3; }
.donut-center { position: relative; font: 500 20px/1 var(--wb-font-display); letter-spacing: -0.6px; text-align: center; }
.recon-rows { list-style: none; padding: 0; margin: 0; display: flex; flex-direction: column; gap: 4px; font-size: 13.5px; flex: 1; min-width: 0; }
.recon-row {
  display: flex;
  justify-content: space-between;
  gap: 10px;
  padding: 4px 8px;
  margin: 0 -8px;
  transition: background 0.15s;
}
.recon-row.on { background: var(--wb-surface-2); }
.recon-name { display: flex; align-items: center; gap: 8px; min-width: 0; }
.recon-row strong { font-weight: 500; white-space: nowrap; color: var(--wb-ink); }
.recon-pct { color: var(--wb-muted-soft); font-weight: 400; }
.recon-link { margin-top: auto; padding-top: 18px; align-self: flex-start; }
@media (max-width: 400px) {
  .recon-body { flex-direction: column; align-items: stretch; }
  .donut { align-self: center; }
}
</style>
