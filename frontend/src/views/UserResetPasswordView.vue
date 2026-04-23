<script setup lang="ts">
import { ref, computed } from "vue";
import { useRouter } from "vue-router";
import { useQuery, useMutation } from "@tanstack/vue-query";
import Button from "primevue/button";
import Password from "primevue/password";
import Message from "primevue/message";
import { api } from "@/api/client";

interface User {
  user_id: string;
  email: string;
  role: string;
}

const props = defineProps<{ userId: string }>();
const router = useRouter();

const password = ref("");
const saveError = ref<string | null>(null);

const { data: users } = useQuery<User[]>({
  queryKey: ["users"],
  queryFn: async () => (await api.get<User[]>("/users")).data,
});

const target = computed(
  () => (users.value ?? []).find((u) => u.user_id === props.userId) ?? null,
);

const resetPassword = useMutation({
  mutationFn: () =>
    api.post(`/users/${props.userId}/reset-password`, { password: password.value }),
  onSuccess: () => {
    router.push({ name: "users" });
  },
  onError: (err: any) => {
    saveError.value = err?.response?.data?.detail ?? "Reset failed";
  },
});

function submit() {
  saveError.value = null;
  resetPassword.mutate();
}

function cancel() {
  router.push({ name: "users" });
}
</script>

<template>
  <section class="page">
    <div class="back">
      <Button label="Users" icon="pi pi-arrow-left" text size="small" @click="cancel" />
    </div>

    <div class="header">
      <h1>Reset password</h1>
      <div class="actions">
        <Button label="Cancel" text @click="cancel" />
        <Button
          label="Reset"
          severity="warn"
          :disabled="!password"
          :loading="resetPassword.isPending.value"
          @click="submit"
        />
      </div>
    </div>

    <Message v-if="saveError" severity="error" :closable="true" @close="saveError = null">
      {{ saveError }}
    </Message>

    <div class="card">
      <div v-if="target" class="target-line">
        <span class="muted">User:</span>
        <strong>{{ target.email }}</strong>
      </div>
      <label class="field">
        <span>New password</span>
        <Password v-model="password" :feedback="false" toggle-mask autofocus />
      </label>
    </div>
  </section>
</template>

<style scoped>
.page { display: flex; flex-direction: column; gap: 1rem; padding-bottom: 2rem; }
.back { margin-bottom: -0.5rem; }
.header { display: flex; justify-content: space-between; align-items: center; gap: 1rem; }
.header h1 { margin: 0; font-size: 1.5rem; text-transform: uppercase; letter-spacing: 0.02em; }
.actions { display: flex; gap: 0.5rem; }
.card {
  background: #fff;
  border: 1px solid var(--color-border, #e2e8f0);
  border-radius: 12px;
  padding: 1.25rem;
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.04);
  display: flex;
  flex-direction: column;
  gap: 0.85rem;
  max-width: 460px;
}
.target-line { display: flex; gap: 0.5rem; font-size: 0.9rem; }
.muted { color: var(--color-text-muted, #64748b); }
.field { display: flex; flex-direction: column; gap: 0.3rem; font-size: 0.875rem; color: #334155; }
.field > span { font-weight: 600; font-size: 0.85rem; }
.field :deep(.p-password),
.field :deep(.p-password-input) { width: 100%; }
</style>
