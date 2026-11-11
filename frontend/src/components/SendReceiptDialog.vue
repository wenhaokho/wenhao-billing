<script setup lang="ts">
import AppIcon from "@/components/AppIcon.vue";
import { ref, watch, computed } from "vue";
import Dialog from "primevue/dialog";
import Button from "primevue/button";
import InputText from "primevue/inputtext";
import { joinCc } from "@/utils/email";
import Select from "primevue/select";
import Message from "primevue/message";
import { useAuthStore } from "@/stores/auth";
import { formatAmount } from "@/utils/money";

export interface ReceiptPayment {
  payment_id: string;
  amount: string;
  currency: string;
  payment_date: string;
  payer_reference: string | null;
}

interface Props {
  visible: boolean;
  payments: ReceiptPayment[];
  defaultTo: string | null;
  defaultCc?: string[] | null;
  loading?: boolean;
  error?: string | null;
}

const props = defineProps<Props>();
const emit = defineEmits<{
  (e: "update:visible", v: boolean): void;
  (e: "submit", payload: { payment_id: string; to_email: string; cc_email: string | null }): void;
}>();

const auth = useAuthStore();

const paymentId = ref<string | null>(null);
const toEmail = ref("");
const ccEmail = ref("");

const options = computed(() =>
  props.payments.map((p) => ({
    value: p.payment_id,
    label:
      `${p.payment_date} · ${formatAmount(p.amount, p.currency)} ${p.currency}` +
      (p.payer_reference ? ` · ${p.payer_reference}` : ""),
  })),
);

watch(
  () => props.visible,
  (v) => {
    if (v) {
      paymentId.value = props.payments[0]?.payment_id ?? null;
      toEmail.value = props.defaultTo ?? "";
      ccEmail.value = joinCc(props.defaultCc, auth.user?.email);
    }
  },
  { immediate: true },
);

const canSubmit = computed(() => !!paymentId.value && !!toEmail.value.trim());

function close() {
  emit("update:visible", false);
}

function submit() {
  if (!canSubmit.value || !paymentId.value) return;
  emit("submit", {
    payment_id: paymentId.value,
    to_email: toEmail.value.trim(),
    cc_email: ccEmail.value.trim() || null,
  });
}
</script>

<template>
  <Dialog
    :visible="visible"
    header="Send payment receipt"
    modal
    :style="{ width: '520px' }"
    :closable="!loading"
    @update:visible="emit('update:visible', $event)"
  >
    <div class="send-grid">
      <label class="field">
        <span>Payment</span>
        <Select
          v-model="paymentId"
          :options="options"
          option-label="label"
          option-value="value"
          placeholder="Select payment"
        />
      </label>
      <label class="field">
        <span>To</span>
        <InputText v-model="toEmail" placeholder="customer@example.com" />
      </label>
      <label class="field">
        <span>Cc <span class="muted">(optional)</span></span>
        <InputText v-model="ccEmail" placeholder="Separate multiple with commas" />
      </label>
      <p class="note">
        <AppIcon name="paperclip" /> The receipt PDF will be attached automatically.
      </p>
      <Message v-if="error" severity="error" :closable="false">{{ error }}</Message>
    </div>
    <template #footer>
      <Button label="Cancel" text :disabled="loading" @click="close" />
      <Button
        label="Send receipt"
        :loading="loading"
        :disabled="!canSubmit || loading"
        @click="submit">
        <template #icon><AppIcon name="mail" /></template>
      </Button>
    </template>
  </Dialog>
</template>

<style scoped>
.send-grid { display: flex; flex-direction: column; gap: 16px; padding: 4px 0 8px; }
.field { display: flex; flex-direction: column; gap: 6px; font-size: 14px; }
.field > span { font: 500 13px/1.4 var(--wb-font-sans); color: var(--wb-muted); }
.field .muted { font-weight: 400; color: var(--wb-muted-soft); font-size: 12px; }
.field :deep(.p-inputtext), .field :deep(.p-select) { width: 100%; }
.note { font-size: 12px; color: var(--wb-muted); margin: 0; line-height: 1.5; }
.note .app-icon { margin-right: 4px; vertical-align: -0.15em; width: 13px; height: 13px; }
</style>
