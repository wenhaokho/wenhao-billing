<script setup lang="ts">
// Confirm dialog for "Send reminder" on a Needs attention row: names the
// recipient, the invoice and the amount (the expected cash, with the balance
// due beside it when they differ) before POST /invoices/{id}/reminders. The
// recipient defaults to the customer's billing (contact) email, the same one
// the backend falls back to; typing another address sends it as to_email.
// A 409 from the 24-hour per-customer guard turns Send into "Send anyway",
// which retries with force: true.
import AppIcon from "@/components/AppIcon.vue";
import { computed, ref, watch } from "vue";
import { useMutation, useQuery } from "@tanstack/vue-query";
import Dialog from "primevue/dialog";
import Button from "primevue/button";
import InputText from "primevue/inputtext";
import Message from "primevue/message";
import { api } from "@/api/client";
import { formatAmount } from "@/utils/money";
import { decIsZero, decSub, timeAgo } from "./format";
import type { AttentionRow, Reminder } from "./types";

const props = defineProps<{ visible: boolean; row: AttentionRow | null }>();
const emit = defineEmits<{
  (e: "update:visible", v: boolean): void;
  (e: "sent", reminder: Reminder): void;
}>();

const customerId = computed(() => props.row?.customer_id ?? null);

// Same key and payload as the customer page, so the two share the cache.
const { data: customer, isPending: customerPending } = useQuery<{ contact_email: string | null }>({
  queryKey: ["customer", customerId],
  queryFn: async () => (await api.get(`/customers/${customerId.value}`)).data,
  enabled: () => props.visible && !!customerId.value,
});

const defaultEmail = computed(() => customer.value?.contact_email?.trim() || "");
const lookingUp = computed(() => !!customerId.value && customerPending.value);

const toEmail = ref("");
const edited = ref(false);
/** The repeat guard's refusal: the earlier invoice and when it went. */
const conflict = ref<{ invoice: string | null; sentAt: string | null } | null>(null);
const error = ref<string | null>(null);

watch(
  () => props.visible,
  (v) => {
    if (!v) return;
    edited.value = false;
    toEmail.value = defaultEmail.value;
    conflict.value = null;
    error.value = null;
  },
);
// The customer may load after the dialog opens: fill the default unless typed over.
watch(defaultEmail, (e) => {
  if (!edited.value) toEmail.value = e;
});

const differs = computed(() => !!props.row && !decIsZero(decSub(props.row.balance_due, props.row.expected_cash)));

interface Problem {
  detail?: string;
  code?: string;
  errors?: { location?: string; message?: string }[];
}

const send = useMutation({
  mutationFn: async (force: boolean) => {
    const row = props.row;
    if (!row) throw new Error("no invoice");
    const to = toEmail.value.trim();
    const body: { to_email?: string; force?: boolean } = {};
    if (to && to !== defaultEmail.value) body.to_email = to;
    if (force) body.force = true;
    return (await api.post<Reminder>(`/invoices/${row.invoice_id}/reminders`, body)).data;
  },
  onSuccess: (reminder) => {
    emit("sent", reminder);
    emit("update:visible", false);
  },
  onError: (e: { response?: { status?: number; data?: Problem } }) => {
    const data = e?.response?.data;
    if (e?.response?.status === 409 && data?.code === "reminder_recent") {
      const field = (name: string) => data.errors?.find((d) => d.location === name)?.message ?? null;
      conflict.value = { invoice: field("invoice_number"), sentAt: field("sent_at") };
      error.value = null;
      return;
    }
    error.value = data?.detail ?? "Failed to send the reminder";
  },
});

const conflictText = computed(() => {
  const c = conflict.value;
  if (!c) return "";
  const who = props.row?.customer_name ?? "This customer";
  const when = c.sentAt ? timeAgo(c.sentAt) : "in the last 24 hours";
  const what = c.invoice ? ` for ${c.invoice}` : "";
  return `Reminded ${who} ${when}${what}. Send anyway?`;
});

const canSend = computed(() => !!props.row && !!toEmail.value.trim() && !send.isPending.value);

function submit() {
  if (!canSend.value) return;
  send.mutate(!!conflict.value);
}

function close() {
  emit("update:visible", false);
}
</script>

<template>
  <Dialog
    :visible="visible"
    header="Send payment reminder"
    modal
    :style="{ width: '480px' }"
    :breakpoints="{ '575px': '94vw' }"
    :closable="!send.isPending.value"
    @update:visible="emit('update:visible', $event)"
  >
    <div v-if="row" class="rd" data-testid="reminder-dialog">
      <label class="rd-field">
        <span>To</span>
        <InputText
          v-model="toEmail"
          type="email"
          :placeholder="lookingUp ? 'Looking up the billing email…' : 'customer\'s billing email'"
          :disabled="send.isPending.value"
          @input="edited = true"
        />
        <small v-if="!lookingUp && !defaultEmail" class="rd-hint">
          {{ row.customer_id ? "The customer has no billing email on file; type one to send to." : "No customer on this invoice; type an address to send to." }}
        </small>
      </label>

      <dl class="rd-facts">
        <div>
          <dt>Invoice</dt>
          <dd>
            <span class="rd-strong">{{ row.invoice_number ?? "(no number)" }}</span>
            <span v-if="row.customer_name" class="rd-muted"> · {{ row.customer_name }}</span>
            <span class="rd-muted"> · {{ row.days_late }}d late</span>
          </dd>
        </div>
        <div>
          <dt>Amount</dt>
          <dd>
            <span class="rd-strong num" data-testid="reminder-amount">{{ formatAmount(row.expected_cash, row.currency) }} {{ row.currency }}</span>
            <span v-if="differs" class="rd-muted num" title="Balance due before withholding tax not yet booked">
              of {{ formatAmount(row.balance_due, row.currency) }} due
            </span>
          </dd>
        </div>
      </dl>

      <Message v-if="conflict" severity="warn" :closable="false" data-testid="reminder-conflict">{{ conflictText }}</Message>
      <Message v-if="error" severity="error" :closable="false">{{ error }}</Message>
    </div>

    <template #footer>
      <Button label="Cancel" text :disabled="send.isPending.value" @click="close" />
      <Button
        :label="conflict ? 'Send anyway' : 'Send'"
        :severity="conflict ? 'warn' : undefined"
        :loading="send.isPending.value"
        :disabled="!canSend"
        @click="submit"
      >
        <template #icon><AppIcon name="send" /></template>
      </Button>
    </template>
  </Dialog>
</template>

<style scoped>
.rd { display: flex; flex-direction: column; gap: 18px; padding: 4px 0 8px; }
.rd-field { display: flex; flex-direction: column; gap: 6px; font-size: 14px; }
.rd-field > span { font: 500 13px/1.4 var(--wb-font-sans); color: var(--wb-muted); }
.rd-field :deep(.p-inputtext) { width: 100%; }
.rd-hint { font-size: 12px; color: var(--wb-muted); }
.rd-facts { display: flex; flex-direction: column; gap: 12px; margin: 0; }
.rd-facts > div { display: grid; grid-template-columns: 72px minmax(0, 1fr); gap: 12px; align-items: baseline; }
.rd-facts dt { font: 500 13px/1.4 var(--wb-font-sans); color: var(--wb-muted); }
.rd-facts dd { margin: 0; font-size: 14px; color: var(--wb-ink); min-width: 0; overflow-wrap: anywhere; }
.rd-strong { font-weight: 500; }
.rd-muted { color: var(--wb-muted); }
</style>
