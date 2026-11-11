<script setup lang="ts">
import AppIcon from "@/components/AppIcon.vue";
import { ref, watch } from "vue";
import Dialog from "primevue/dialog";
import Button from "primevue/button";
import InputText from "primevue/inputtext";
import { joinCc } from "@/utils/email";
import Textarea from "primevue/textarea";
import Message from "primevue/message";
import { useAuthStore } from "@/stores/auth";

interface Props {
  visible: boolean;
  defaultTo: string | null;
  defaultCc?: string[] | null;
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

const auth = useAuthStore();

const toEmail = ref("");
const ccEmail = ref("");
const subject = ref("");
const message = ref("");

watch(
  () => props.visible,
  (v) => {
    if (v) {
      toEmail.value = props.defaultTo ?? "";
      ccEmail.value = joinCc(props.defaultCc, auth.user?.email);
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
    subject: subject.value.trim() || "Invoice",
    message: message.value,
  });
}
</script>

<template>
  <Dialog
    :visible="visible"
    header="Send invoice by email"
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
        <InputText v-model="ccEmail" placeholder="Separate multiple with commas" />
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
        <AppIcon name="paperclip" /> The invoice PDF will be attached automatically.
      </p>
      <Message v-if="error" severity="error" :closable="false">{{ error }}</Message>
    </div>
    <template #footer>
      <Button label="Cancel" text :disabled="loading" @click="close" />
      <Button
        label="Send"
        :loading="loading"
        :disabled="!toEmail.trim() || loading"
        @click="submit">
        <template #icon><AppIcon name="send" /></template>
      </Button>
    </template>
  </Dialog>
</template>

<style scoped>
.send-grid { display: flex; flex-direction: column; gap: 16px; padding: 4px 0 8px; }
.field { display: flex; flex-direction: column; gap: 6px; font-size: 14px; }
.field > span { font: 500 13px/1.4 var(--wb-font-sans); color: var(--wb-muted); }
.field .muted { font-weight: 400; color: var(--wb-muted-soft); font-size: 12px; }
.field :deep(.p-inputtext), .field :deep(textarea) { width: 100%; }
.note { font-size: 12px; color: var(--wb-muted); margin: 0; line-height: 1.5; }
.note .app-icon { margin-right: 4px; vertical-align: -0.15em; width: 13px; height: 13px; }
</style>
