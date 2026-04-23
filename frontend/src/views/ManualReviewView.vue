<script setup lang="ts">
import { ref } from "vue";
import { useQuery, useMutation, useQueryClient } from "@tanstack/vue-query";
import DataTable from "primevue/datatable";
import Column from "primevue/column";
import Tag from "primevue/tag";
import Button from "primevue/button";
import Dialog from "primevue/dialog";
import InputText from "primevue/inputtext";
import Textarea from "primevue/textarea";
import Message from "primevue/message";
import { api } from "@/api/client";
import { formatAmount, formatMoney } from "@/utils/money";

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
  created_at: string;
}

interface LogEntry {
  log_id: string;
  action: string;
  reasons: string[];
  created_at: string;
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

function statusSeverity(status: string) {
  if (status === "CLEARED") return "success";
  if (status === "FLAGGED") return "warning";
  return "danger";
}
</script>

<template>
  <section>
    <header class="page-header">
      <div>
        <h1>Manual Review</h1>
        <p class="subtitle">
          Payments held by the safe-stop gate — amount/currency mismatch or low confidence.
          Review the reasons, then approve or reverse.
        </p>
      </div>
    </header>

    <div class="layout">
      <div class="list">
      <DataTable
        :value="data ?? []"
        :loading="isLoading"
        data-key="payment_id"
        selection-mode="single"
        v-model:selection="selected"
        striped-rows
      >
        <template #empty>
          <div class="empty-state">
            <i class="pi pi-check-circle" />
            <div>Nothing held. All recent payments auto-cleared.</div>
          </div>
        </template>
        <Column field="payer_name" header="Payer" />
        <Column header="Amount" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }">
          <template #body="{ data: r }">
            <span class="num">{{ formatAmount(r.amount, r.currency) }}</span>
          </template>
        </Column>
        <Column field="currency" header="Ccy" />
        <Column field="payment_date" header="Date" />
        <Column field="confidence_score" header="Score" />
        <Column field="adjustment_type" header="Adjustment">
          <template #body="{ data: r }">
            <Tag :value="r.adjustment_type" severity="info" />
          </template>
        </Column>
      </DataTable>
    </div>

      <aside v-if="selected" class="detail card">
        <div class="detail-head">
          <h2>Payment {{ selected.payment_id.slice(0, 8) }}…</h2>
          <Tag :value="selected.status" :severity="statusSeverity(selected.status)" />
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
          <ul v-if="(log?.[0]?.reasons ?? []).length" class="reasons">
            <li v-for="(r, i) in (log?.[0]?.reasons ?? [])" :key="i">{{ r }}</li>
          </ul>
          <p v-else class="muted">No reasons logged.</p>
        </div>

        <div class="section">
          <h3>Approve against invoice</h3>
          <div class="approve-row">
            <InputText v-model="invoiceId" placeholder="invoice UUID" />
            <Button
              label="Approve & Post"
              icon="pi pi-check"
              :disabled="!invoiceId"
              :loading="approve.isPending.value"
              @click="approve.mutate()"
            />
          </div>
          <Message v-if="approve.error.value" severity="error" :closable="false">
            {{ (approve.error.value as any)?.response?.data?.detail ?? 'Approval failed' }}
          </Message>
        </div>

        <div v-if="selected.status === 'CLEARED'" class="section">
          <Button
            label="Reverse this payment"
            icon="pi pi-undo"
            severity="danger"
            text
            @click="showReverse = true"
          />
        </div>
      </aside>

      <aside v-else class="detail card card-pad placeholder">
        <i class="pi pi-arrow-left" />
        <div>Select a payment on the left to see its match reasons and take action.</div>
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
.layout { display: grid; grid-template-columns: 1.5fr 1fr; gap: 1.25rem; }
@media (max-width: 1100px) { .layout { grid-template-columns: 1fr; } }

.detail { padding: 1.1rem 1.25rem; }
.detail-head { display: flex; justify-content: space-between; align-items: center; margin-bottom: 0.75rem; }
.detail h2 { margin: 0; font-size: 1rem; font-weight: 600; }
.detail h3 { margin: 0 0 0.5rem; font-size: 0.85rem; color: var(--color-text-muted); text-transform: uppercase; letter-spacing: 0.04em; font-weight: 600; }
.section { margin-top: 1.1rem; padding-top: 1.1rem; border-top: 1px solid var(--color-border); }
dl { display: grid; grid-template-columns: 110px 1fr; gap: 0.4rem 0.75rem; margin: 0; font-size: 0.88rem; }
dt { color: var(--color-text-muted); }
dd { margin: 0; color: var(--color-text); }
dd.num { font-variant-numeric: tabular-nums; font-weight: 500; }
.reasons { padding-left: 1.1rem; color: #475569; margin: 0; font-size: 0.88rem; }
.reasons li { margin-bottom: 0.2rem; }
.muted { color: var(--color-text-muted); font-size: 0.85rem; margin: 0; }
.approve-row { display: flex; gap: 0.5rem; }
.approve-row .p-inputtext { flex: 1; }
.w-full { width: 100%; }
.placeholder {
  display: flex; flex-direction: column; align-items: center; justify-content: center;
  gap: 0.75rem; color: var(--color-text-muted); text-align: center;
  min-height: 200px;
}
.placeholder i { font-size: 1.5rem; color: var(--color-text-subtle); }
</style>
