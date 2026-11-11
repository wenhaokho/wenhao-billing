<script setup lang="ts">
// The mockup's empty-state pattern: centred icon, Manrope title, muted
// message, optional call to action (default slot).
import AppIcon from "@/components/AppIcon.vue";
import type { IconName } from "@/theme/icons";

withDefaults(defineProps<{ title: string; message?: string; icon?: IconName }>(), { message: "", icon: "inbox" });
</script>

<template>
  <div class="empty-state wb-empty">
    <AppIcon :name="icon" />
    <div class="wb-empty-title">{{ title }}</div>
    <div v-if="message || $slots.message" class="wb-empty-message"><slot name="message">{{ message }}</slot></div>
    <div v-if="$slots.default" class="wb-empty-cta"><slot /></div>
  </div>
</template>

<style scoped>
.wb-empty {
  padding: 64px 24px;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 10px;
  text-align: center;
}
.wb-empty :deep(.app-icon) {
  width: 28px;
  height: 28px;
  margin: 0 0 4px;
  color: var(--wb-muted-soft);
}
.wb-empty-title {
  font: 500 22px/1.2 var(--wb-font-display);
  letter-spacing: -0.6px;
  color: var(--wb-ink);
}
.wb-empty-message { color: var(--wb-muted); font-size: 15px; line-height: 1.55; max-width: 420px; }
.wb-empty-message :deep(strong) { color: var(--wb-ink); font-weight: 500; }
.wb-empty-cta { margin-top: 10px; }
@media (max-width: 720px) {
  .wb-empty { padding: 48px 16px; }
  .wb-empty-title { font-size: 19px; }
}
</style>
