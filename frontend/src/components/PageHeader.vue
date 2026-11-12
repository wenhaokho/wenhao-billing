<script setup lang="ts">
// Page header from the mockup: red uppercase eyebrow (the nav group, e.g.
// "02 / Sales"), 40px Manrope H1, optional status chip beside it (#status
// slot, as on the mockup's invoice page), optional lede (#lede slot), actions
// on the right (default slot). The #title slot replaces the H1's text when it
// needs markup (the invoice page's italic number); title stays its plain text.
defineProps<{ eyebrow?: string; title: string }>();
</script>

<template>
  <header class="page-head">
    <div class="page-heading">
      <div v-if="eyebrow" class="page-eyebrow">{{ eyebrow }}</div>
      <div v-if="$slots.status" class="page-title-row">
        <h1><slot name="title">{{ title }}</slot></h1>
        <slot name="status" />
      </div>
      <h1 v-else><slot name="title">{{ title }}</slot></h1>
      <p v-if="$slots.lede" class="page-lede"><slot name="lede" /></p>
    </div>
    <div v-if="$slots.default" class="page-head-actions">
      <slot />
    </div>
  </header>
</template>

<style scoped>
.page-head {
  display: flex;
  justify-content: space-between;
  align-items: flex-end;
  gap: 16px 24px;
  flex-wrap: wrap;
  margin-bottom: 32px;
}
.page-heading { flex: 1 1 360px; min-width: 0; max-width: 760px; }
.page-eyebrow {
  font: 500 12px/1.6 var(--wb-font-sans);
  letter-spacing: 1.5px;
  text-transform: uppercase;
  color: var(--wb-red);
}
.page-heading h1 {
  font: 500 40px/1.1 var(--wb-font-display);
  letter-spacing: -1.4px;
  color: var(--wb-ink);
  margin: 6px 0 8px;
}
.page-title-row { display: flex; align-items: center; gap: 8px 14px; flex-wrap: wrap; margin: 6px 0 8px; }
.page-title-row h1 { margin: 0; }
.page-lede { margin: 0; color: var(--wb-muted); font-size: 16px; line-height: 1.6; }
.page-lede :deep(strong) { color: var(--wb-ink); font-weight: 500; }
.page-lede :deep(code) {
  font: 500 12px var(--wb-font-mono);
  letter-spacing: 0.5px;
  padding: 2px 6px;
  background: var(--wb-tint);
  color: var(--wb-ink);
}
.page-head-actions { display: flex; align-items: center; gap: 10px 18px; flex-wrap: wrap; }
@media (max-width: 720px) {
  .page-head { margin-bottom: 24px; }
  .page-heading { flex-basis: 100%; }
  .page-heading h1 { font-size: 32px; letter-spacing: -1px; }
  .page-lede { font-size: 15px; }
}
</style>
