<script setup lang="ts">
// The mockup's pill group for the dashboard window: 30 days, 90 days, 12 months.
import type { Period } from "./types";

const model = defineModel<Period>({ required: true });

const OPTIONS: { value: Period; label: string; short: string }[] = [
  { value: "30d", label: "Last 30 days", short: "30 days" },
  { value: "90d", label: "Last 90 days", short: "90 days" },
  { value: "12m", label: "Last 12 months", short: "12 months" },
];
</script>

<template>
  <div class="period" role="group" aria-label="Period">
    <button
      v-for="o in OPTIONS"
      :key="o.value"
      type="button"
      class="period-btn"
      :class="{ on: model === o.value }"
      :aria-pressed="model === o.value"
      :data-period="o.value"
      @click="model = o.value"
    >
      <span class="long">{{ o.label }}</span><span class="short">{{ o.short }}</span>
    </button>
  </div>
</template>

<style scoped>
.period {
  display: inline-flex;
  border: 1px solid var(--wb-line);
  border-radius: 30px;
  padding: 3px;
  background: var(--wb-surface);
}
.period-btn {
  height: 34px;
  padding: 0 16px;
  border-radius: 30px;
  border: 0;
  background: transparent;
  color: var(--wb-muted);
  font: 500 13px var(--wb-font-sans);
  cursor: pointer;
  white-space: nowrap;
  transition: background 0.15s, color 0.15s;
}
.period-btn:hover:not(.on) { color: var(--wb-ink); }
.period-btn.on { background: var(--wb-ink); color: var(--wb-bg); }
.period-btn:focus-visible { outline: none; box-shadow: var(--focus-ring); }
.short { display: none; }
@media (max-width: 480px) {
  .period-btn { padding: 0 12px; }
  .long { display: none; }
  .short { display: inline; }
}
</style>
