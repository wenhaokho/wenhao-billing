<script setup lang="ts">
import { ref } from "vue";
import { useRouter, useRoute, RouterLink } from "vue-router";
import InputText from "primevue/inputtext";
import Password from "primevue/password";
import Button from "primevue/button";
import Message from "primevue/message";
import AuthLayout from "@/components/AuthLayout.vue";
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
  <AuthLayout title="Welcome back" subtitle="Sign in to continue to your dashboard.">
    <form @submit.prevent="submit" class="auth-form">
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

    <template #footer>
      <span class="auth-status-dot">Admin-only workspace · session cookie auth</span>
    </template>
  </AuthLayout>
</template>
