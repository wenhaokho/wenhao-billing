<script setup lang="ts">
import AppIcon from "@/components/AppIcon.vue";
import { computed, ref, watch } from "vue";
import { useQuery, useMutation, useQueryClient } from "@tanstack/vue-query";
import DataTable from "primevue/datatable";
import Column from "primevue/column";
import Tag from "primevue/tag";
import StatusTag from "@/components/StatusTag.vue";
import PageHeader from "@/components/PageHeader.vue";
import EmptyState from "@/components/EmptyState.vue";
import Button from "primevue/button";
import Dialog from "primevue/dialog";
import Textarea from "primevue/textarea";
import Message from "primevue/message";
import { api } from "@/api/client";
import { formatAmount, formatMoney } from "@/utils/money";
import InvoicePicker from "@/components/review/InvoicePicker.vue";
import { reasonLabel, reasonTone } from "@/theme/status";

interface Payment {
  payment_id: string;
  invoice_id: string | null;
  customer_id: string | null;
  amount: string;
  currency: string;
  payer_name: string;
  payer_reference: string | null;
  payment_date: string;
  status: string;
  adjustment_type: string;
  confidence_score: string | null;
  intake_source: string;
  created_at: string;
}

interface LogEntry {
  log_id: string;
  action: string;
  reasons: string[] | null;
  reason_codes: string[];
  created_at: string;
}

// How the payment arrived (payments.intake_source).
const VIA: Record<string, string> = {
  WEBHOOK: "Bank webhook",
  EMAIL_PARSER: "Email",
  MANUAL: "Manual",
};
function viaLabel(source: string): string {
  return VIA[source] ?? source;
}

// confidence_score is on the matching engine's 0–100 scale; 95 is its
// auto-clear threshold (backend internal/matching AutoClearThreshold).
const AUTO_CLEAR = 95;
function scorePct(score: string): number {
  const n = Number(score);
  return Number.isFinite(n) ? Math.max(0, Math.min(100, n)) : 0;
}
function scoreTone(score: string): "ok" | "low" {
  return Number(score) >= AUTO_CLEAR ? "ok" : "low";
}

const queryClient = useQueryClient();
const selected = ref<Payment | null>(null);
const invoiceId = ref("");
const reverseReason = ref("");
const showReverse = ref(false);

const { data, isLoading } = useQuery<Payment[]>({
  queryKey: ["payments-awaiting-review"],
  queryFn: async () => (await api.get<Payment[]>("/payments/awaiting-review")).data,
});

const { data: log } = useQuery<LogEntry[]>({
  queryKey: ["payment-log", () => selected.value?.payment_id],
  queryFn: async () =>
    (await api.get<LogEntry[]>(`/payments/${selected.value!.payment_id}/log`)).data,
  enabled: () => !!selected.value,
});

// The log is oldest first: the reasons are the latest HELD row's, each with
// its code (same order) for the tag.
const heldReasons = computed(() => {
  const held = [...(log.value ?? [])].reverse().find((e) => e.action === "HELD");
  return (held?.reasons ?? []).map((text, i) => ({ text, code: held?.reason_codes[i] ?? "OTHER" }));
});

watch(selected, () => {
  invoiceId.value = "";
});

const approve = useMutation({
  mutationFn: async () => {
    await api.post(`/payments/${selected.value!.payment_id}/manual-review`, {
      invoice_id: invoiceId.value,
      adjustment_type: "NONE",
    });
  },
  onSuccess: () => {
    selected.value = null;
    invoiceId.value = "";
    queryClient.invalidateQueries({ queryKey: ["payments-awaiting-review"] });
  },
});

const reverse = useMutation({
  mutationFn: async () => {
    await api.post(`/payments/${selected.value!.payment_id}/reverse`, {
      reason: reverseReason.value,
    });
  },
  onSuccess: () => {
    showReverse.value = false;
    reverseReason.value = "";
    selected.value = null;
    queryClient.invalidateQueries({ queryKey: ["payments-awaiting-review"] });
  },
});

</script>

<template>
  <section>
    <PageHeader eyebrow="05 / Operations" title="Manual Review">
      <template #lede>
        Payments held by the safe-stop gate — amount/currency mismatch or low confidence.
        Review the reasons, then approve or reverse.
      </template>
    </PageHeader>

    <div class="layout">
      <div class="list card">
        <div class="list-head">
          <span class="card-title">Held payments</span>
          <Tag
            v-if="(data ?? []).length"
            class="status-tag"
            severity="warn"
            :value="`${(data ?? []).length} waiting`"
          />
        </div>
        <!-- Outside the table: inside it the message centres across the table's
             scrolling width and is clipped on a phone. -->
        <EmptyState
          v-if="!isLoading && !(data ?? []).length"
          icon="circle-check"
          title="Nothing held"
          message="All recent payments auto-cleared." />
        <DataTable
          v-else
          :value="data ?? []"
          :loading="isLoading"
          data-key="payment_id"
          selection-mode="single"
          v-model:selection="selected"
          striped-rows
        >
          <Column header="Payer">
            <template #body="{ data: r }">
              <span class="payer">{{ r.payer_name }}</span>
            </template>
          </Column>
          <Column header="Amount" body-class="cell-nowrap" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }">
            <template #body="{ data: r }">
              <span class="num strong">{{ formatAmount(r.amount, r.currency) }}</span>
            </template>
          </Column>
          <Column field="currency" header="Ccy" body-class="cell-nowrap cell-muted" />
          <Column field="payment_date" header="Date" body-class="cell-nowrap cell-muted" />
          <Column header="Via" body-class="cell-nowrap">
            <template #body="{ data: r }">
              <StatusTag :status="r.intake_source" tone="neutral" :label="viaLabel(r.intake_source)" />
            </template>
          </Column>
          <Column header="Score" body-class="cell-nowrap" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }">
            <template #body="{ data: r }">
              <span v-if="r.confidence_score != null" class="num score" :class="scoreTone(r.confidence_score)">{{ r.confidence_score }}</span>
              <span v-else class="muted">—</span>
            </template>
          </Column>
          <Column field="adjustment_type" header="Adjustment">
            <template #body="{ data: r }">
              <Tag :value="r.adjustment_type" severity="info" />
            </template>
          </Column>
        </DataTable>
      </div>

      <aside v-if="selected" class="detail card">
        <div class="detail-head">
          <span class="card-eyebrow">Why it was held</span>
          <StatusTag :status="selected.status" />
        </div>
        <h2 class="detail-payer">{{ selected.payer_name }}</h2>
        <div class="detail-amount num">{{ formatMoney(selected.amount, selected.currency) }}</div>
        <div class="detail-sub">
          Received {{ selected.payment_date }} · payment <code>{{ selected.payment_id.slice(0, 8) }}…</code>
          · via <StatusTag
            class="via-tag"
            data-testid="via-tag"
            :status="selected.intake_source"
            tone="neutral"
            :label="viaLabel(selected.intake_source)" />
        </div>

        <div v-if="selected.confidence_score != null" class="confidence">
          <div class="confidence-row">
            <span>Match confidence</span>
            <span class="num score" :class="scoreTone(selected.confidence_score)">{{ selected.confidence_score }}</span>
          </div>
          <div class="confidence-track">
            <div
              class="confidence-bar"
              :class="scoreTone(selected.confidence_score)"
              :style="{ width: `${scorePct(selected.confidence_score)}%` }"
            />
          </div>
        </div>

        <dl>
          <dt>Payer</dt><dd>{{ selected.payer_name }}</dd>
          <dt>Reference</dt><dd>{{ selected.payer_reference ?? '—' }}</dd>
          <dt>Amount</dt><dd class="num">{{ formatMoney(selected.amount, selected.currency) }}</dd>
          <dt>Date</dt><dd>{{ selected.payment_date }}</dd>
          <dt>Confidence</dt><dd>{{ selected.confidence_score ?? '—' }}</dd>
          <dt>Adjustment</dt><dd><Tag :value="selected.adjustment_type" severity="info" /></dd>
        </dl>

        <div class="section">
          <h3>Reasons</h3>
          <ul v-if="heldReasons.length" class="reasons" data-testid="held-reasons">
            <li v-for="(r, i) in heldReasons" :key="i" :data-reason="r.code">
              <StatusTag :status="r.code" :tone="reasonTone(r.code)" :label="reasonLabel(r.code)" />
              <span class="reason-text">{{ r.text }}</span>
            </li>
          </ul>
          <p v-else class="muted">No reasons logged.</p>
        </div>

        <div class="section">
          <h3>Approve against invoice</h3>
          <InvoicePicker
            :key="selected.payment_id"
            v-model="invoiceId"
            :currency="selected.currency"
            :amount="selected.amount" />
          <div class="approve-row">
            <Button
              label="Approve & Post"
              :disabled="!invoiceId"
              :loading="approve.isPending.value"
              @click="approve.mutate()">
              <template #icon><AppIcon name="check" /></template>
            </Button>
          </div>
          <Message v-if="approve.error.value" severity="error" :closable="false">
            {{ (approve.error.value as any)?.response?.data?.detail ?? 'Approval failed' }}
          </Message>
        </div>

        <div v-if="selected.status === 'CLEARED'" class="section">
          <Button
            label="Reverse this payment"
            severity="danger"
            text
            @click="showReverse = true">
            <template #icon><AppIcon name="undo-2" /></template>
          </Button>
        </div>
      </aside>

      <aside v-else class="card card-pad placeholder">
        <AppIcon name="arrow-left" />
        <div>Select a held payment to see its match reasons and take action.</div>
      </aside>
    </div>

    <Dialog v-model:visible="showReverse" header="Reverse payment" modal :style="{ width: '480px' }">
      <p class="muted">Creates a balancing journal entry and flags the payment.</p>
      <Textarea v-model="reverseReason" rows="3" placeholder="Reason" class="w-full" />
      <template #footer>
        <Button label="Cancel" text @click="showReverse = false" />
        <Button
          label="Confirm reversal"
          severity="danger"
          :disabled="!reverseReason"
          :loading="reverse.isPending.value"
          @click="reverse.mutate()"
        />
      </template>
    </Dialog>
  </section>
</template>

<style scoped>
/* Mockup: held-payments card on the left, "why it was held" on the right. */
.layout {
  display: grid;
  grid-template-columns: minmax(0, 1.3fr) minmax(340px, 1fr);
  gap: 24px;
  align-items: start;
}
@media (max-width: 1100px) { .layout { grid-template-columns: 1fr; } }
/* Stacked, the list sits above the placeholder: drop its left-pointing arrow. */
@media (max-width: 1100px) { .placeholder :deep(.app-icon) { display: none; } }

.list { min-width: 0; }
.list-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  padding: 18px 24px;
  border-bottom: 1px solid var(--wb-line);
}
.list :deep(.p-datatable-table-container) { border: 0; }
.list :deep(.p-datatable-tbody > tr) { cursor: pointer; }
.list :deep(.p-datatable-tbody > tr.p-datatable-row-selected) {
  background: var(--wb-surface-2) !important;
  color: var(--wb-ink);
  box-shadow: inset 3px 0 0 var(--wb-red);
}
.payer { font-weight: 500; }
.strong { font-weight: 500; }
.num { font-variant-numeric: tabular-nums; }
/* Confidence against the 95 auto-clear threshold (matching engine). */
.score { font-weight: 500; }
.score.ok { color: var(--wb-green-fg); }
.score.low { color: var(--wb-amber-fg); }

.detail { padding: 28px; min-width: 0; }
.detail-head { display: flex; justify-content: space-between; align-items: center; gap: 12px; }
.detail .detail-payer {
  margin: 10px 0 0;
  font: 500 24px/1.15 var(--wb-font-display);
  letter-spacing: -0.7px;
  color: var(--wb-ink);
  overflow-wrap: anywhere;
}
.detail-amount {
  font: 500 32px/1.1 var(--wb-font-display);
  letter-spacing: -1.2px;
  color: var(--wb-ink);
  margin-top: 6px;
}
.detail-sub { font-size: 13px; color: var(--wb-muted); margin-top: 4px; }
.detail-sub code { background: var(--wb-tint); color: var(--wb-ink); padding: 1px 5px; font-size: 12px; }

.confidence { margin-top: 24px; }
.confidence-row { display: flex; justify-content: space-between; font-size: 13px; color: var(--wb-muted); }
.confidence-track { height: 6px; background: var(--wb-tint); margin-top: 8px; }
.confidence-bar { height: 100%; }
.confidence-bar.ok { background: var(--wb-green-fg); }
.confidence-bar.low { background: var(--wb-amber-fg); }

dl { display: grid; grid-template-columns: 110px 1fr; gap: 8px 14px; margin: 24px 0 0; font-size: 14px; }
dt { color: var(--wb-muted); }
dd { margin: 0; color: var(--wb-ink); min-width: 0; overflow-wrap: anywhere; }
dd.num { font-variant-numeric: tabular-nums; font-weight: 500; }
.section { margin-top: 24px; padding-top: 24px; border-top: 1px solid var(--wb-line); }
.detail h3 {
  margin: 0 0 12px;
  font: 500 11px/1.4 var(--wb-font-sans);
  color: var(--wb-muted-soft);
  text-transform: uppercase;
  letter-spacing: 1.2px;
}
/* Each logged reason as its own bordered row: its tag, then the sentence (mockup). */
.reasons { list-style: none; padding: 0; margin: 0; display: flex; flex-direction: column; gap: 10px; }
.reasons li {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr);
  gap: 12px;
  align-items: start;
  padding: 14px 16px;
  border: 1px solid var(--wb-line);
  color: var(--wb-muted);
  font-size: 14px;
  line-height: 1.5;
}
.reasons li :deep(.status-tag) { margin-top: 1px; white-space: nowrap; }
.reason-text { min-width: 0; overflow-wrap: anywhere; }
.via-tag { vertical-align: middle; }
@media (max-width: 480px) {
  .reasons li { grid-template-columns: 1fr; gap: 8px; }
  .reasons li :deep(.status-tag) { justify-self: start; }
}
.muted { color: var(--wb-muted); font-size: 14px; margin: 0; }
.approve-row { display: flex; gap: 10px; flex-wrap: wrap; margin-top: 16px; }
.w-full { width: 100%; }
.placeholder {
  display: flex; flex-direction: column; align-items: center; justify-content: center;
  gap: 12px; color: var(--wb-muted); text-align: center;
  min-height: 240px;
}
.placeholder .app-icon { width: 24px; height: 24px; color: var(--wb-muted-soft); }
.card-pad { padding: 28px; }
@media (max-width: 720px) {
  .detail, .card-pad { padding: 20px; }
  .list-head { padding: 16px 20px; }
}
</style>
