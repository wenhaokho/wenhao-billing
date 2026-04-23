<script setup lang="ts">
import { ref } from "vue";
import { RouterLink } from "vue-router";
import InputText from "primevue/inputtext";
import Button from "primevue/button";
import Message from "primevue/message";
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
  <div class="login-bg">
    <div class="login-card">
      <div class="brand">
        <div class="brand-mark">WB</div>
        <div>
          <div class="brand-name">wenhao-billing</div>
          <div class="brand-sub">Password recovery</div>
        </div>
      </div>

      <h1>Forgot your password?</h1>
      <p class="subtitle">
        Enter the email on your admin account. If it matches, we'll email you a
        one-hour reset link.
      </p>

      <form v-if="!result" @submit.prevent="submit" class="form">
        <label>
          Email
          <InputText v-model="email" type="email" required autofocus />
        </label>
        <Message v-if="error" severity="error" :closable="false">{{ error }}</Message>
        <Button
          type="submit"
          label="Send reset link"
          icon="pi pi-envelope"
          :loading="submitting"
        />
      </form>

      <div v-else class="result">
        <Message severity="success" :closable="false">
          {{ result.message }}
        </Message>
        <p class="hint">
          Check your inbox (and spam folder). The link expires in 1 hour. If
          SMTP isn't configured on the server, the reset link is written to the
          backend logs instead.
        </p>
      </div>

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

.result { display: flex; flex-direction: column; gap: 0.75rem; }
.result .hint { color: var(--color-text-muted); font-size: 0.82rem; margin: 0; }

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
