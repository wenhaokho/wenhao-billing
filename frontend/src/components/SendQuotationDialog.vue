<script setup lang="ts">
import { ref, watch } from "vue";
import Dialog from "primevue/dialog";
import Button from "primevue/button";
import InputText from "primevue/inputtext";
import Textarea from "primevue/textarea";
import Message from "primevue/message";

interface Props {
  visible: boolean;
  defaultTo: string | null;
  defaultSubject: string;
  defaultMessage: string;
  loading?: boolean;
  error?: string | null;
}

const props = defineProps<Props>();
const emit = defineEmits<{
  (e: "update:visible", v: boolean): void;
  (e: "submit", payload: {
    to_email: string;
    cc_email: string | null;
    subject: string;
    message: string;
  }): void;
}>();

const toEmail = ref("");
const ccEmail = ref("");
const subject = ref("");
const message = ref("");

watch(
  () => props.visible,
  (v) => {
    if (v) {
      toEmail.value = props.defaultTo ?? "";
      ccEmail.value = "";
      subject.value = props.defaultSubject;
      message.value = props.defaultMessage;
    }
  },
);

function close() {
  emit("update:visible", false);
}

function submit() {
  if (!toEmail.value.trim()) return;
  emit("submit", {
    to_email: toEmail.value.trim(),
    cc_email: ccEmail.value.trim() || null,
    subject: subject.value.trim() || "Quotation",
    message: message.value,
  });
}
</script>

<template>
  <Dialog
    :visible="visible"
    header="Send quotation by email"
    modal
    :style="{ width: '520px' }"
    :closable="!loading"
    @update:visible="emit('update:visible', $event)"
  >
    <div class="send-grid">
      <label class="field">
        <span>To</span>
        <InputText v-model="toEmail" placeholder="customer@example.com" />
      </label>
      <label class="field">
        <span>Cc <span class="muted">(optional)</span></span>
        <InputText v-model="ccEmail" />
      </label>
      <label class="field">
        <span>Subject</span>
        <InputText v-model="subject" />
      </label>
      <label class="field">
        <span>Message</span>
        <Textarea v-model="message" rows="6" autoResize />
      </label>
      <p class="note">
        <i class="pi pi-paperclip" /> The quotation PDF will be attached automatically.
      </p>
      <Message v-if="error" severity="error" :closable="false">{{ error }}</Message>
    </div>
    <template #footer>
      <Button label="Cancel" text :disabled="loading" @click="close" />
      <Button
        label="Send"
        icon="pi pi-send"
        :loading="loading"
        :disabled="!toEmail.trim() || loading"
        @click="submit"
      />
    </template>
  </Dialog>
</template>

<style scoped>
.send-grid { display: flex; flex-direction: column; gap: 0.75rem; padding: 0.25rem 0 0.5rem; }
.field { display: flex; flex-direction: column; gap: 0.3rem; font-size: 0.85rem; }
.field > span { font-weight: 600; color: var(--color-text); }
.field .muted { font-weight: 400; color: var(--color-text-muted); font-size: 0.78rem; }
.field :deep(.p-inputtext), .field :deep(textarea) { width: 100%; }
.note { font-size: 0.78rem; color: var(--color-text-muted); margin: 0; }
.note i { margin-right: 0.3rem; }
</style>
