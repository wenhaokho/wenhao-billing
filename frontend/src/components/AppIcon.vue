<script setup lang="ts">
import { icons, type IconName } from "@/theme/icons";

// Decorative Lucide icon. Controls keep their own accessible name (label or
// aria-label), so the glyph is hidden from assistive tech. `class` falls
// through to the svg.
withDefaults(defineProps<{ name: IconName; size?: number }>(), { size: 16 });
</script>

<template>
  <component :is="icons[name]" :size="size" aria-hidden="true" class="app-icon" />
</template>
