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
  <div class="login-shell">
    <!-- Left: branded hero -->
    <aside class="hero">
      <div class="hero-inner">
        <div class="brand">
          <div class="brand-mark">WB</div>
          <div class="brand-name">wenhao-billing</div>
        </div>

        <div class="hero-copy">
          <h2>Accounting operations, quietly automated.</h2>
          <p>
            Billing, reconciliations, and client reporting in one workspace built
            for small agency teams.
          </p>
        </div>

        <ul class="hero-points">
          <li><i class="pi pi-check-circle" /> Automated monthly retainers</li>
          <li><i class="pi pi-check-circle" /> Reconciled against bank feeds</li>
          <li><i class="pi pi-check-circle" /> Client-ready statements in one click</li>
        </ul>

        <div class="hero-footer">&copy; {{ new Date().getFullYear() }} wenhao-billing</div>
      </div>
      <div class="hero-glow glow-1" />
      <div class="hero-glow glow-2" />
      <div class="hero-grid" />
    </aside>

    <!-- Right: form -->
    <main class="form-panel">
      <div class="form-wrap">
        <div class="form-head">
          <h1>Welcome back</h1>
          <p>Sign in to continue to your dashboard.</p>
        </div>

        <form @submit.prevent="submit" class="form">
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

          <label class="field">
            <div class="field-label-row">
              <span class="field-label">Password</span>
              <RouterLink to="/forgot-password" class="forgot-link">Forgot password?</RouterLink>
            </div>
            <Password
              v-model="password"
              :feedback="false"
              toggle-mask
              required
              placeholder="••••••••"
              input-class="pw-input"
            />
          </label>

          <Message v-if="error" severity="error" :closable="false">{{ error }}</Message>

          <Button
            type="submit"
            label="Sign in"
            icon="pi pi-arrow-right"
            icon-pos="right"
            :loading="submitting"
            class="submit-btn"
          />
        </form>

        <div class="form-foot">
          <span class="dot" /> Admin-only workspace · session cookie auth
        </div>
      </div>
    </main>
  </div>
</template>

<style scoped>
.login-shell {
  min-height: 100vh;
  display: grid;
  grid-template-columns: 1.05fr 1fr;
  background: var(--color-bg);
}

/* -------- Hero (left) -------- */
.hero {
  position: relative;
  overflow: hidden;
  color: #e5edff;
  background:
    linear-gradient(135deg, #0b1d3a 0%, #122a55 45%, #1e3a8a 100%);
  display: flex;
  align-items: stretch;
}
.hero-inner {
  position: relative;
  z-index: 2;
  padding: 3rem 3.5rem;
  display: flex;
  flex-direction: column;
  width: 100%;
  max-width: 560px;
}
.hero .brand {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  margin-bottom: auto;
}
.hero .brand-mark {
  width: 44px;
  height: 44px;
  border-radius: 12px;
  background: linear-gradient(135deg, #60a5fa, #3b82f6);
  color: white;
  display: grid;
  place-items: center;
  font-weight: 700;
  letter-spacing: 0.02em;
  box-shadow: 0 6px 20px rgba(59, 130, 246, 0.4);
}
.hero .brand-name {
  font-weight: 600;
  font-size: 1.05rem;
  color: #fff;
}

.hero-copy {
  margin: 5rem 0 2rem;
}
.hero-copy h2 {
  color: #fff;
  font-size: 2rem;
  line-height: 1.2;
  letter-spacing: -0.02em;
  font-weight: 600;
  margin: 0 0 1rem;
}
.hero-copy p {
  color: #cbd5e1;
  font-size: 0.95rem;
  line-height: 1.6;
  margin: 0;
  max-width: 36ch;
}
.hero-points {
  list-style: none;
  padding: 0;
  margin: 0 0 auto;
  display: flex;
  flex-direction: column;
  gap: 0.65rem;
}
.hero-points li {
  display: flex;
  align-items: center;
  gap: 0.6rem;
  color: #dbeafe;
  font-size: 0.88rem;
}
.hero-points .pi {
  color: #60a5fa;
  font-size: 0.95rem;
}
.hero-footer {
  margin-top: 2.5rem;
  color: #94a3b8;
  font-size: 0.75rem;
  letter-spacing: 0.02em;
}

.hero-glow {
  position: absolute;
  border-radius: 50%;
  filter: blur(80px);
  opacity: 0.55;
  pointer-events: none;
}
.glow-1 {
  width: 480px; height: 480px;
  background: #3b82f6;
  top: -140px; right: -160px;
}
.glow-2 {
  width: 420px; height: 420px;
  background: #6366f1;
  bottom: -160px; left: -120px;
  opacity: 0.45;
}
.hero-grid {
  position: absolute;
  inset: 0;
  background-image:
    linear-gradient(rgba(255, 255, 255, 0.04) 1px, transparent 1px),
    linear-gradient(90deg, rgba(255, 255, 255, 0.04) 1px, transparent 1px);
  background-size: 48px 48px;
  mask-image: radial-gradient(ellipse at 30% 40%, black 40%, transparent 75%);
  -webkit-mask-image: radial-gradient(ellipse at 30% 40%, black 40%, transparent 75%);
  z-index: 1;
}

/* -------- Form panel (right) -------- */
.form-panel {
  display: grid;
  place-items: center;
  padding: 3rem 2rem;
  background: var(--color-surface);
}
.form-wrap {
  width: 100%;
  max-width: 380px;
}
.form-head h1 {
  font-size: 1.75rem;
  letter-spacing: -0.02em;
  margin: 0 0 0.4rem;
  font-weight: 600;
}
.form-head p {
  margin: 0 0 2rem;
  color: var(--color-text-muted);
  font-size: 0.92rem;
}

.form { display: flex; flex-direction: column; gap: 1.1rem; }
.field { display: flex; flex-direction: column; gap: 0.4rem; }
.field-label {
  font-size: 0.78rem;
  color: var(--color-text);
  font-weight: 600;
  letter-spacing: 0.01em;
}
.field-label-row {
  display: flex;
  justify-content: space-between;
  align-items: baseline;
}
.forgot-link {
  font-size: 0.75rem;
  color: var(--color-primary);
  text-decoration: none;
  font-weight: 500;
}
.forgot-link:hover { text-decoration: underline; }

.form :deep(.p-inputtext) {
  width: 100%;
  padding: 0.7rem 0.85rem;
  border-radius: var(--radius-md);
  font-size: 0.92rem;
  transition: border-color 0.15s, box-shadow 0.15s;
}
.form :deep(.p-inputtext:focus) {
  box-shadow: 0 0 0 3px rgba(37, 99, 235, 0.15);
  border-color: var(--color-primary);
}
.form :deep(.p-password) { width: 100%; }
.form :deep(.p-password-input) { width: 100%; }

.submit-btn {
  margin-top: 0.4rem;
  padding: 0.8rem 1rem;
  border-radius: var(--radius-md);
  font-weight: 600;
  justify-content: center;
  background: linear-gradient(135deg, #2563eb, #3b82f6);
  border: none;
  box-shadow: 0 6px 16px rgba(37, 99, 235, 0.25);
  transition: transform 0.08s ease, box-shadow 0.15s ease;
}
.submit-btn:hover {
  box-shadow: 0 8px 22px rgba(37, 99, 235, 0.35);
}
.submit-btn:active { transform: translateY(1px); }

.form-foot {
  margin-top: 2rem;
  padding-top: 1.25rem;
  border-top: 1px solid var(--color-border);
  color: var(--color-text-subtle);
  font-size: 0.78rem;
  text-align: center;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 0.5rem;
}
.dot {
  width: 6px; height: 6px; border-radius: 50%;
  background: var(--color-success);
  box-shadow: 0 0 0 3px var(--color-success-soft);
}

/* -------- Responsive -------- */
@media (max-width: 900px) {
  .login-shell { grid-template-columns: 1fr; }
  .hero { display: none; }
  .form-panel { padding: 2rem 1.25rem; }
}
</style>

