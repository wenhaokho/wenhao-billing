<script setup lang="ts">
import { ref } from "vue";
import { RouterLink } from "vue-router";
import InputText from "primevue/inputtext";
import Button from "primevue/button";
import Message from "primevue/message";
import AuthLayout from "@/components/AuthLayout.vue";
import { useAuthStore } from "@/stores/auth";

const auth = useAuthStore();

const email = ref("");
const submitting = ref(false);
const error = ref<string | null>(null);
const result = ref<{ message: string } | null>(null);

async function submit() {
  error.value = null;
  result.value = null;
  submitting.value = true;
  try {
    const data = await auth.forgotPassword(email.value);
    result.value = { message: data.message };
  } catch (e: any) {
    error.value = e?.response?.data?.detail ?? "Request failed";
  } finally {
    submitting.value = false;
  }
}
</script>

<template>
  <AuthLayout
    title="Forgot your password?"
    subtitle="Enter the email on your admin account. If it matches, we'll email you a one-hour reset link."
  >
    <form v-if="!result" @submit.prevent="submit" class="auth-form">
      <label class="field">
        <span class="field-label">Email</span>
        <InputText
          v-model="email"
          type="email"
          required
          autofocus
          placeholder="you@agency.com"
        />
      </label>

      <Message v-if="error" severity="error" :closable="false">{{ error }}</Message>

      <Button
        type="submit"
        label="Send reset link"
        icon="pi pi-envelope"
        :loading="submitting"
        class="submit-btn"
      />
    </form>

    <div v-else class="auth-form">
      <Message severity="success" :closable="false">
        {{ result.message }}
      </Message>
      <p class="result-hint">
        Check your inbox (and spam folder). The link expires in 1 hour. If
        SMTP isn't configured on the server, the reset link is written to the
        backend logs instead.
      </p>
    </div>

    <template #footer>
      <RouterLink to="/login">← Back to login</RouterLink>
    </template>
  </AuthLayout>
</template>

<style scoped>
.result-hint {
  color: var(--color-text-muted);
  font-size: 0.85rem;
  margin: 0;
  line-height: 1.5;
}
</style>
