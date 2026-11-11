<script setup lang="ts">
// Reminder history of one invoice (GET /invoices/{id}/reminders, newest
// first): when it went, to whom, the amount it asked for and whether it was
// sent from the app or through the Claude (MCP) connector.
import { useQuery } from "@tanstack/vue-query";
import Tag from "primevue/tag";
import { api } from "@/api/client";
import { formatAmount } from "@/utils/money";
import type { Reminder } from "@/components/dashboard/types";

const props = defineProps<{ invoiceId: string }>();

const { data: reminders, isError } = useQuery<Reminder[]>({
  queryKey: ["invoice-reminders", () => props.invoiceId],
  queryFn: async () => (await api.get<Reminder[]>(`/invoices/${props.invoiceId}/reminders`)).data,
});

const fmt = new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" });

function sentAt(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : fmt.format(d);
}

function channelLabel(c: string): string {
  return c === "MCP" ? "Claude (MCP)" : c === "REST" ? "App" : c;
}
</script>

<template>
  <div class="card reminders-card" data-testid="invoice-reminders">
    <label class="section-label">Payment reminders</label>
    <p v-if="isError" class="rem-empty">Failed to load the reminder history.</p>
    <p v-else-if="!reminders" class="rem-empty">Loading…</p>
    <p v-else-if="!reminders.length" class="rem-empty">No reminders sent for this invoice yet.</p>
    <div v-else class="rem-scroll">
      <table class="rem-table">
        <thead>
          <tr>
            <th>Sent</th>
            <th>To</th>
            <th class="r">Amount</th>
            <th>Via</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="r in reminders" :key="r.reminder_id">
            <td class="nowrap">{{ sentAt(r.sent_at) }}</td>
            <td class="rem-to">{{ r.recipient }}</td>
            <td class="r num nowrap">{{ formatAmount(r.amount, r.currency) }} {{ r.currency }}</td>
            <td><Tag :value="channelLabel(r.channel)" severity="secondary" /></td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>

<style scoped>
.reminders-card { display: flex; flex-direction: column; gap: 14px; }
.section-label {
  font: 500 18px/1.2 var(--wb-font-display);
  letter-spacing: -0.4px;
  color: var(--wb-ink);
}
.rem-empty { margin: 0; font-size: 14px; color: var(--wb-muted); }
.rem-scroll { overflow-x: auto; }
.rem-table { width: 100%; border-collapse: collapse; font-size: 14px; }
.rem-table th {
  text-align: left;
  font: 500 12px/1.4 var(--wb-font-sans);
  color: var(--wb-muted);
  padding: 0 12px 8px 0;
  border-bottom: 1px solid var(--wb-line);
}
.rem-table td { padding: 10px 12px 10px 0; border-bottom: 1px solid var(--wb-line); color: var(--wb-ink); vertical-align: middle; }
.rem-table tr:last-child td { border-bottom: 0; }
.rem-table .r { text-align: right; }
.rem-to { overflow-wrap: anywhere; }
.nowrap { white-space: nowrap; }
.num { font-variant-numeric: tabular-nums; }
</style>
