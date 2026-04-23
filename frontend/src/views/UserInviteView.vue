<script setup lang="ts">
import { ref } from "vue";
import { useRouter } from "vue-router";
import { useMutation, useQueryClient } from "@tanstack/vue-query";
import Button from "primevue/button";
import InputText from "primevue/inputtext";
import Password from "primevue/password";
import Message from "primevue/message";
import { api } from "@/api/client";

const router = useRouter();
const queryClient = useQueryClient();

const form = ref({ email: "", password: "", role: "admin" });
const saveError = ref<string | null>(null);

const createUser = useMutation({
  mutationFn: () => api.post("/users", form.value),
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ["users"] });
    router.push({ name: "users" });
  },
  onError: (err: any) => {
    saveError.value = err?.response?.data?.detail ?? "Create failed";
  },
});

function submit() {
  saveError.value = null;
  createUser.mutate();
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
      <h1>Invite user</h1>
      <div class="actions">
        <Button label="Cancel" text @click="cancel" />
        <Button
          label="Create"
          :disabled="!form.email || !form.password"
          :loading="createUser.isPending.value"
          @click="submit"
        />
      </div>
    </div>

    <Message v-if="saveError" severity="error" :closable="true" @close="saveError = null">
      {{ saveError }}
    </Message>

    <div class="card">
      <label class="field">
        <span>Email</span>
        <InputText v-model="form.email" type="email" autofocus />
      </label>
      <label class="field">
        <span>Initial password</span>
        <Password v-model="form.password" :feedback="false" toggle-mask />
      </label>
      <label class="field">
        <span>Role</span>
        <InputText v-model="form.role" />
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
  background: var(--color-surface);
  border: 1px solid var(--color-border, #e2e8f0);
  border-radius: 12px;
  padding: 1.25rem;
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.04);
  display: flex;
  flex-direction: column;
  gap: 0.85rem;
  max-width: 520px;
}
.field { display: flex; flex-direction: column; gap: 0.3rem; font-size: 0.875rem; color: var(--color-text); }
.field > span { font-weight: 600; font-size: 0.85rem; }
.field :deep(.p-inputtext),
.field :deep(.p-password),
.field :deep(.p-password-input) { width: 100%; }
</style>
