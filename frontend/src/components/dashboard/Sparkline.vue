<script setup lang="ts">
// Faint bar sparkline behind a KPI tile (mockup): one bar per bucket, heights
// scaled to the largest bucket. Hovering a bar emits its index so the tile can
// show "label · value"; the bars themselves carry no text.
import { computed, ref } from "vue";
import type { SeriesPoint } from "./types";

const props = defineProps<{ series: SeriesPoint[]; color: string }>();
const emit = defineEmits<{ hover: [index: number | null] }>();

const HEIGHT = 58;
const active = ref<number | null>(null);

const bars = computed(() => {
  // Number() only scales a bar for display.
  const values = props.series.map((p) => Math.max(0, Number(p.amount) || 0));
  const max = Math.max(...values, 0);
  return values.map((v) => (max > 0 ? Math.max(2, Math.round((v / max) * HEIGHT)) : 2));
});

// 30 daily bars need a hairline gap; 12 monthly ones get the mockup's 3px.
const gap = computed(() => (props.series.length > 16 ? 1 : 3));

function enter(i: number) {
  active.value = i;
  emit("hover", i);
}
function leave() {
  active.value = null;
  emit("hover", null);
}
</script>

<template>
  <div class="spark" :style="{ gap: `${gap}px` }" aria-hidden="true" @mouseleave="leave">
    <div v-for="(h, i) in bars" :key="i" class="spark-col" @mouseenter="enter(i)">
      <div
        class="spark-bar"
        :style="{
          height: `${h}px`,
          background: color,
          opacity: active === i ? 0.95 : 0.22,
        }"
      />
    </div>
  </div>
</template>

<style scoped>
.spark {
  position: absolute;
  right: 14px;
  bottom: 0;
  width: min(160px, calc(100% - 32px));
  height: 60px;
  display: flex;
  align-items: flex-end;
  z-index: 0;
}
.spark-col {
  flex: 1;
  min-width: 0;
  height: 100%;
  display: flex;
  align-items: flex-end;
  cursor: default;
}
.spark-bar {
  width: 100%;
  border-radius: 1px 1px 0 0;
  transition: opacity 0.15s;
}
</style>
