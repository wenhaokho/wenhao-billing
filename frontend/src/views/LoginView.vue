<script setup lang="ts">
import { ref } from "vue";
import { useRouter, useRoute, RouterLink } from "vue-router";
import InputText from "primevue/inputtext";
import Password from "primevue/password";
import Button from "primevue/button";
import Message from "primevue/message";
import { useAuthStore } from "@/stores/auth";

const auth = useAuthStore();
const router = useRouter();
const route = useRoute();

const email = ref("");
const password = ref("");
const error = ref<string | null>(null);
const submitting = ref(false);

async function submit() {
  error.value = null;
  submitting.value = true;
  try {
    await auth.login(email.value, password.value);
    const next = (route.query.next as string) || "/dashboard";
    router.push(next);
  } catch (e: any) {
    error.value = e?.response?.data?.detail ?? "Login failed";
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
          <div class="brand-sub">Accounting operations</div>
        </div>
      </div>

      <h1>Welcome back</h1>
      <p class="subtitle">Sign in to continue to your dashboard.</p>

      <form @submit.prevent="submit" class="form">
        <label>
          Email
          <InputText v-model="email" type="email" required autofocus />
        </label>
        <label>
          <div class="pw-label-row">
            <span>Password</span>
            <RouterLink to="/forgot-password" class="forgot-link">Forgot password?</RouterLink>
          </div>
          <Password v-model="password" :feedback="false" toggle-mask required input-class="pw" />
        </label>
        <Message v-if="error" severity="error" :closable="false">{{ error }}</Message>
        <Button type="submit" label="Log in" icon="pi pi-sign-in" :loading="submitting" />
      </form>

      <div class="footer-hint">
        Admin-only workspace · session cookie auth
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
  max-width: 420px;
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
label { display: flex; flex-direction: column; gap: 0.3rem; font-size: 0.8rem; color: var(--color-text-muted); font-weight: 500; }
.form :deep(.p-password) { width: 100%; }
.form :deep(.p-password-input) { width: 100%; }
.pw-label-row { display: flex; justify-content: space-between; align-items: baseline; }
.forgot-link {
  font-size: 0.75rem;
  color: var(--color-primary);
  text-decoration: none;
  font-weight: 500;
  text-transform: none;
  letter-spacing: 0;
}
.forgot-link:hover { text-decoration: underline; }

.footer-hint {
  margin-top: 1.5rem;
  padding-top: 1rem;
  border-top: 1px solid var(--color-border);
  color: var(--color-text-subtle);
  font-size: 0.78rem;
  text-align: center;
}
</style>
