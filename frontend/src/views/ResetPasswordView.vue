<script setup lang="ts">
import { ref, onMounted } from "vue";
import { RouterLink, useRoute, useRouter } from "vue-router";
import Password from "primevue/password";
import Button from "primevue/button";
import Message from "primevue/message";
import InputText from "primevue/inputtext";
import AuthLayout from "@/components/AuthLayout.vue";
import { useAuthStore } from "@/stores/auth";

const auth = useAuthStore();
const route = useRoute();
const router = useRouter();

const token = ref("");
const pw = ref("");
const confirm = ref("");
const submitting = ref(false);
const error = ref<string | null>(null);
const success = ref(false);

onMounted(() => {
  token.value = (route.query.token as string) ?? "";
});

async function submit() {
  error.value = null;
  if (!token.value.trim()) {
    error.value = "Reset token is required.";
    return;
  }
  if (pw.value.length < 6) {
    error.value = "Password must be at least 6 characters.";
    return;
  }
  if (pw.value !== confirm.value) {
    error.value = "Passwords don't match.";
    return;
  }
  submitting.value = true;
  try {
    await auth.resetPassword(token.value.trim(), pw.value);
    success.value = true;
    setTimeout(() => router.push("/login"), 1500);
  } catch (e: any) {
    error.value = e?.response?.data?.detail ?? "Reset failed";
  } finally {
    submitting.value = false;
  }
}
</script>

<template>
  <AuthLayout
    title="Choose a new password"
    subtitle="Tokens are single-use and expire one hour after issue."
  >
    <form v-if="!success" @submit.prevent="submit" class="auth-form">
      <label class="field">
        <span class="field-label">Reset token</span>
        <InputText
          v-model="token"
          :readonly="!!route.query.token"
          required
          autofocus
        />
        <span v-if="route.query.token" class="field-hint">Loaded from your reset link.</span>
      </label>

      <label class="field">
        <span class="field-label">New password</span>
        <Password v-model="pw" toggle-mask required />
        <span class="field-hint">Minimum 6 characters.</span>
      </label>

      <label class="field">
        <span class="field-label">Confirm password</span>
        <Password v-model="confirm" :feedback="false" toggle-mask required />
      </label>

      <Message v-if="error" severity="error" :closable="false">{{ error }}</Message>

      <Button
        type="submit"
        label="Reset password"
        icon="pi pi-check"
        :loading="submitting"
        class="submit-btn"
      />
    </form>

    <Message v-else severity="success" :closable="false">
      Password reset. Redirecting you to login…
    </Message>

    <template #footer>
      <RouterLink to="/login">← Back to login</RouterLink>
    </template>
  </AuthLayout>
</template>
