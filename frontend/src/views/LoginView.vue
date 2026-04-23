<script setup lang="ts">
import { ref, onMounted } from "vue";
import { useRouter, useRoute, RouterLink } from "vue-router";
import InputText from "primevue/inputtext";
import Password from "primevue/password";
import Button from "primevue/button";
import Message from "primevue/message";
import Checkbox from "primevue/checkbox";
import AuthLayout from "@/components/AuthLayout.vue";
import { useAuthStore } from "@/stores/auth";

const auth = useAuthStore();
const router = useRouter();
const route = useRoute();

const REMEMBER_KEY = "wb:login:email";

const email = ref("");
const password = ref("");
const remember = ref(false);
const error = ref<string | null>(null);
const submitting = ref(false);

onMounted(() => {
  const saved = localStorage.getItem(REMEMBER_KEY);
  if (saved) {
    email.value = saved;
    remember.value = true;
  }
});

function friendlyError(raw: unknown): string {
  const msg = typeof raw === "string" ? raw : "";
  if (/invalid|incorrect|unauthor|credentials/i.test(msg)) {
    return "Email or password is incorrect.";
  }
  if (/disabled|locked|inactive/i.test(msg)) {
    return "This account isn't active. Contact your admin.";
  }
  return msg || "Login failed. Please try again.";
}

async function submit() {
  error.value = null;
  submitting.value = true;
  try {
    await auth.login(email.value, password.value);
    if (remember.value) {
      localStorage.setItem(REMEMBER_KEY, email.value);
    } else {
      localStorage.removeItem(REMEMBER_KEY);
    }
    const next = (route.query.next as string) || "/dashboard";
    router.push(next);
  } catch (e: any) {
    error.value = friendlyError(e?.response?.data?.detail);
  } finally {
    submitting.value = false;
  }
}
</script>

<template>
  <AuthLayout title="Welcome back" subtitle="Sign in to continue to your dashboard.">
    <form @submit.prevent="submit" class="auth-form" novalidate>
      <label class="field">
        <span class="field-label">Email</span>
        <InputText
          v-model="email"
          type="email"
          required
          autofocus
          autocomplete="username"
          placeholder="you@agency.com"
          :aria-invalid="!!error || undefined"
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
          input-id="password"
          placeholder="••••••••"
          :input-props="{ autocomplete: 'current-password', 'aria-invalid': !!error || undefined }"
        />
      </label>

      <label class="remember">
        <Checkbox v-model="remember" :binary="true" input-id="remember" />
        <span>Remember my email on this device</span>
      </label>

      <Message
        v-if="error"
        severity="error"
        :closable="false"
        role="alert"
        aria-live="assertive"
      >
        {{ error }}
      </Message>

      <Button
        type="submit"
        label="Sign in"
        icon="pi pi-arrow-right"
        icon-pos="right"
        :loading="submitting"
        class="submit-btn"
      />
    </form>

    <template #footer>
      <span class="auth-status-dot">Admin-only workspace · session cookie auth</span>
    </template>
  </AuthLayout>
</template>

<style scoped>
.remember {
  display: flex;
  align-items: center;
  gap: 0.6rem;
  font-size: 0.85rem;
  color: var(--color-text-muted);
  cursor: pointer;
  user-select: none;
}
.remember :deep(.p-checkbox) { flex-shrink: 0; }
</style>
