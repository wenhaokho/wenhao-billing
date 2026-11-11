<script setup lang="ts">
// One cell of the dashboard's KPI row (mockup): uppercase label, compact value,
// unit, then a delta/share in its tone and a muted sub line. With `series` a
// faint sparkline sits behind the cell; hovering a bar names it top-right.
import { computed, ref } from "vue";
import { useRouter } from "vue-router";
import Sparkline from "./Sparkline.vue";
import { formatCompact } from "./format";
import type { KpiDelta, SeriesPoint } from "./types";

const props = withDefaults(
  defineProps<{
    label: string;
    value: string;
    unit: string;
    delta?: KpiDelta;
    sub?: string;
    series?: SeriesPoint[];
    /** CSS colour of the sparkline. */
    sparkColor?: string;
    /** Colour of the value itself ("bad" for overdue). */
    valueTone?: "neutral" | "bad";
    /** Full-precision value, shown as a native tooltip on the compact one. */
    valueTitle?: string;
    /** Route the tile opens on click / Enter / Space. */
    to?: string;
  }>(),
  { delta: undefined, sub: "", series: undefined, sparkColor: "var(--wb-red)", valueTone: "neutral", valueTitle: undefined, to: undefined },
);

const router = useRouter();
function open() {
  if (props.to) router.push(props.to);
}

const hovered = ref<number | null>(null);
const tip = computed(() => {
  const p = hovered.value === null ? undefined : props.series?.[hovered.value];
  return p ? `${p.label} · ${formatCompact(p.amount)}` : "";
});
</script>

<template>
  <div
    class="kpi"
    :class="{ link: !!to }"
    data-testid="kpi-tile"
    :role="to ? 'link' : undefined"
    :tabindex="to ? 0 : undefined"
    @click="open"
    @keydown.enter.space.prevent="open"
  >
    <Sparkline v-if="series && series.length" :series="series" :color="sparkColor" @hover="hovered = $event" />
    <div class="kpi-tip" :class="{ on: !!tip }" aria-hidden="true">{{ tip }}</div>
    <div class="kpi-label">{{ label }}</div>
    <div class="kpi-value" :class="{ bad: valueTone === 'bad' }" :title="valueTitle">{{ value }}</div>
    <div class="kpi-unit">{{ unit }}</div>
    <div class="kpi-sub">
      <span v-if="delta" class="kpi-delta" :class="delta.tone">{{ delta.text }}</span>
      {{ sub }}
    </div>
  </div>
</template>

<style scoped>
.kpi {
  position: relative;
  background: var(--wb-surface);
  padding: 20px 18px 0;
  display: flex;
  flex-direction: column;
  min-width: 0;
  overflow: hidden;
  transition: background 0.15s;
}
.kpi:hover { background: var(--wb-surface-2); }
.kpi.link { cursor: pointer; }
.kpi.link:focus-visible { outline: none; box-shadow: var(--focus-ring); }
.kpi > :not(.spark):not(.kpi-tip) { position: relative; z-index: 1; }
.kpi-tip {
  position: absolute;
  top: 16px;
  right: 14px;
  padding: 4px 7px;
  background: var(--wb-ink);
  color: var(--wb-bg);
  font: 400 11px/1.3 var(--wb-font-sans);
  white-space: nowrap;
  opacity: 0;
  pointer-events: none;
  z-index: 3;
  transition: opacity 0.15s;
}
.kpi-tip.on { opacity: 1; }
.kpi-label {
  font: 500 10.5px/1.4 var(--wb-font-sans);
  letter-spacing: 1.2px;
  text-transform: uppercase;
  color: var(--wb-muted-soft);
  min-height: 30px;
}
.kpi-value {
  font: 500 26px/1.1 var(--wb-font-display);
  letter-spacing: -1px;
  color: var(--wb-ink);
  margin-top: 8px;
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}
.kpi-value.bad { color: var(--wb-red); }
.kpi-unit {
  font: 500 11px var(--wb-font-display);
  letter-spacing: 0.2px;
  color: var(--wb-muted-soft);
  margin-top: 3px;
  white-space: nowrap;
}
.kpi-sub {
  font-size: 11.5px;
  line-height: 1.4;
  color: var(--wb-muted);
  margin-top: auto;
  padding: 10px 0 14px;
  pointer-events: none;
}
.kpi-delta { font-weight: 500; white-space: nowrap; color: var(--wb-ink); }
.kpi-delta.good { color: var(--wb-green-fg); }
.kpi-delta.bad { color: var(--wb-red); }
@media (max-width: 640px) {
  .kpi { padding: 16px 14px 0; }
  .kpi-value { font-size: 24px; }
}
</style>
