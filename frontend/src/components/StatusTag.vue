<script setup lang="ts">
import { computed } from "vue";
import Tag from "primevue/tag";
import { statusLabel, statusTone, type StatusTone } from "@/theme/status";

const props = defineProps<{ status: string }>();

// The preset gives each of these Tag severities a brand tone pair.
const SEVERITY: Record<StatusTone, "success" | "warn" | "danger" | "info" | "secondary"> = {
  green: "success",
  amber: "warn",
  red: "danger",
  blue: "info",
  neutral: "secondary",
};

const tone = computed(() => statusTone(props.status));
</script>

<template>
  <Tag
    class="status-tag"
    :value="statusLabel(status)"
    :severity="SEVERITY[tone]"
    :data-status="status"
    :data-tone="tone"
  />
</template>
