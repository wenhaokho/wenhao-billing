<script setup lang="ts">
import { ref, onMounted } from "vue";
import { RouterLink, useRoute, useRouter } from "vue-router";
import Password from "primevue/password";
import Button from "primevue/button";
import Message from "primevue/message";
import InputText from "primevue/inputtext";
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
    // Short pause so the user sees the success state, then bounce to login.
    setTimeout(() => router.push("/login"), 1500);
  } catch (e: any) {
    error.value = e?.response?.data?.detail ?? "Reset failed";
  } finally {
    submitting.value = false;
  }
}
</script>

<template>
  <div class="login-bg">
    <div class="login-card">
      <div class="brand">
        <div class="brand-mark">WB</div>
        <div>
          <div class="brand-name">wenhao-billing</div>
          <div class="brand-sub">Reset password</div>
        </div>
      </div>

      <h1>Choose a new password</h1>
      <p class="subtitle">
        Tokens are single-use and expire one hour after issue.
      </p>

      <form v-if="!success" @submit.prevent="submit" class="form">
        <label>
          Reset token
          <InputText
            v-model="token"
            :readonly="!!route.query.token"
            required
            autofocus
          />
          <small v-if="route.query.token">Loaded from your reset link.</small>
        </label>
        <label>
          New password
          <Password v-model="pw" toggle-mask required input-class="pw-input" />
          <small>Minimum 6 characters.</small>
        </label>
        <label>
          Confirm password
          <Password v-model="confirm" :feedback="false" toggle-mask required input-class="pw-input" />
        </label>

        <Message v-if="error" severity="error" :closable="false">{{ error }}</Message>

        <Button
          type="submit"
          label="Reset password"
          icon="pi pi-check"
          :loading="submitting"
        />
      </form>

      <Message v-else severity="success" :closable="false">
        Password reset. Redirecting you to login…
      </Message>

      <div class="footer-hint">
        <RouterLink to="/login">← Back to login</RouterLink>
      </div>
    </div>
  </div>
</template>

<style scoped>
.login-bg {
  min-height: 100vh;
  display: grid;
  place-items: center;
  background:
    radial-gradient(circle at 20% 20%, rgba(37, 99, 235, 0.08), transparent 50%),
    radial-gradient(circle at 80% 80%, rgba(99, 102, 241, 0.08), transparent 50%),
    var(--color-bg);
  padding: 2rem;
}
.login-card {
  width: 100%;
  max-width: 460px;
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-lg);
  box-shadow: var(--shadow-md);
  padding: 2rem;
}
.brand { display: flex; align-items: center; gap: 0.75rem; margin-bottom: 1.75rem; }
.brand-mark {
  width: 42px; height: 42px; border-radius: 10px;
  background: linear-gradient(135deg, #2563eb, #60a5fa);
  color: white; display: grid; place-items: center;
  font-weight: 700; letter-spacing: 0.02em;
}
.brand-name { font-weight: 600; font-size: 1rem; }
.brand-sub { color: var(--color-text-muted); font-size: 0.78rem; text-transform: uppercase; letter-spacing: 0.05em; }

h1 { margin: 0 0 0.25rem; font-size: 1.4rem; letter-spacing: -0.01em; }
.subtitle { margin: 0 0 1.5rem; color: var(--color-text-muted); font-size: 0.9rem; }

.form { display: flex; flex-direction: column; gap: 0.85rem; }
.form label { display: flex; flex-direction: column; gap: 0.3rem; font-size: 0.8rem; color: var(--color-text-muted); font-weight: 500; }
.form small { color: var(--color-text-muted); font-size: 0.72rem; font-weight: 400; }
.form :deep(.p-password), .form :deep(.p-password-input), .form :deep(.pw-input) { width: 100%; }

.footer-hint {
  margin-top: 1.5rem;
  padding-top: 1rem;
  border-top: 1px solid var(--color-border);
  text-align: center;
  font-size: 0.82rem;
}
.footer-hint a { color: var(--color-text-muted); text-decoration: none; }
.footer-hint a:hover { color: var(--color-primary); }
</style>
