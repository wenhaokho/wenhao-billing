<script setup lang="ts">
import { computed } from "vue";
import Tag from "primevue/tag";
import { statusLabel, statusTone, type StatusTone } from "@/theme/status";

// tone and label override the status map, for codes that are not statuses
// (reason codes, intake sources); data-status still carries the code.
const props = defineProps<{ status: string; tone?: StatusTone; label?: string }>();

// The preset gives each of these Tag severities a brand tone pair.
const SEVERITY: Record<StatusTone, "success" | "warn" | "danger" | "info" | "secondary"> = {
  green: "success",
  amber: "warn",
  red: "danger",
  blue: "info",
  neutral: "secondary",
};

const tone = computed(() => props.tone ?? statusTone(props.status));
</script>

<template>
  <Tag
    class="status-tag"
    :value="label ?? statusLabel(status)"
    :severity="SEVERITY[tone]"
    :data-status="status"
    :data-tone="tone"
  />
</template>
