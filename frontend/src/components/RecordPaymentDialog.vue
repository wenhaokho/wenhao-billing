<script setup lang="ts">
import { ref, watch, computed } from "vue";
import Dialog from "primevue/dialog";
import Button from "primevue/button";
import InputText from "primevue/inputtext";
import InputNumber from "primevue/inputnumber";
import DatePicker from "primevue/calendar";
import Textarea from "primevue/textarea";
import Message from "primevue/message";
import { formatAmount } from "@/utils/money";

interface Props {
  visible: boolean;
  balanceDue: string | number;
  currency: string;
  // "AR" = invoice side (needs payer_name), "AP" = bill side
  mode: "AR" | "AP";
  loading?: boolean;
  error?: string | null;
}

const props = defineProps<Props>();
const emit = defineEmits<{
  (e: "update:visible", v: boolean): void;
  (e: "submit", payload: {
    amount: number;
    payment_date: string;
    payer_name: string | null;
    payer_reference: string | null;
    notes: string | null;
  }): void;
}>();

const amount = ref<number | null>(null);
const paymentDate = ref<Date>(new Date());
const payerName = ref<string>("");
const payerReference = ref<string>("");
const notes = ref<string>("");

const balanceNum = computed(() => Number(props.balanceDue || 0));

watch(
  () => props.visible,
  (v) => {
    if (v) {
      amount.value = balanceNum.value;
      paymentDate.value = new Date();
      payerName.value = "";
      payerReference.value = "";
      notes.value = "";
    }
  },
);

const canSubmit = computed(
  () =>
    !!amount.value && amount.value > 0 && amount.value <= balanceNum.value && !!paymentDate.value,
);

function close() {
  emit("update:visible", false);
}

function submit() {
  if (!canSubmit.value || !amount.value || !paymentDate.value) return;
  const iso = paymentDate.value.toISOString().slice(0, 10);
  emit("submit", {
    amount: amount.value,
    payment_date: iso,
    payer_name: payerName.value.trim() || null,
    payer_reference: payerReference.value.trim() || null,
    notes: notes.value.trim() || null,
  });
}
</script>

<template>
  <Dialog
    :visible="visible"
    :header="mode === 'AR' ? 'Record customer payment' : 'Record bill payment'"
    modal
    :style="{ width: '460px' }"
    :closable="!loading"
    @update:visible="emit('update:visible', $event)"
  >
    <div class="rp-grid">
      <div class="rp-balance">
        Balance due: <strong>{{ formatAmount(balanceDue, currency) }} {{ currency }}</strong>
      </div>
      <label class="field">
        <span>Amount</span>
        <InputNumber
          v-model="amount"
          :min-fraction-digits="0"
          :max-fraction-digits="4"
          :use-grouping="true"
          :min="0"
          :max="balanceNum"
        />
      </label>
      <label class="field">
        <span>Payment date</span>
        <DatePicker v-model="paymentDate" date-format="yy-mm-dd" show-icon />
      </label>
      <label v-if="mode === 'AR'" class="field">
        <span>Payer name <span class="muted">(optional)</span></span>
        <InputText v-model="payerName" placeholder="e.g. ACME Corp" />
      </label>
      <label class="field">
        <span>Reference <span class="muted">(optional)</span></span>
        <InputText v-model="payerReference" placeholder="e.g. bank transfer ID" />
      </label>
      <label class="field">
        <span>Notes <span class="muted">(optional)</span></span>
        <Textarea v-model="notes" rows="2" autoResize />
      </label>
      <Message v-if="error" severity="error" :closable="false">{{ error }}</Message>
    </div>
    <template #footer>
      <Button label="Cancel" text :disabled="loading" @click="close" />
      <Button
        label="Record payment"
        icon="pi pi-check"
        :loading="loading"
        :disabled="!canSubmit || loading"
        @click="submit"
      />
    </template>
  </Dialog>
</template>

<style scoped>
.rp-grid { display: flex; flex-direction: column; gap: 0.75rem; padding: 0.25rem 0 0.5rem; }
.rp-balance { font-size: 0.88rem; color: var(--color-text-muted); padding-bottom: 0.25rem; border-bottom: 1px solid var(--color-border); margin-bottom: 0.25rem; }
.field { display: flex; flex-direction: column; gap: 0.3rem; font-size: 0.85rem; }
.field > span { font-weight: 600; color: var(--color-text); }
.field .muted { font-weight: 400; color: var(--color-text-muted); font-size: 0.78rem; }
.field :deep(.p-inputtext),
.field :deep(.p-inputnumber),
.field :deep(.p-datepicker) { width: 100%; }
</style>
