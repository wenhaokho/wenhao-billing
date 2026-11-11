<script setup lang="ts">
import AppIcon from "@/components/AppIcon.vue";
import PageHeader from "@/components/PageHeader.vue";
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
      <Button label="Users" text size="small" @click="cancel">
        <template #icon><AppIcon name="arrow-left" /></template>
      </Button>
    </div>

    <PageHeader eyebrow="06 / Admin" title="Reset password">
      <Button label="Cancel" severity="secondary" outlined @click="cancel" />
      <Button
        label="Reset"
        severity="warn"
        :disabled="!password"
        :loading="resetPassword.isPending.value"
        @click="submit"
      />
    </PageHeader>

    <Message v-if="saveError" severity="error" role="alert" aria-live="assertive" :closable="true" @close="saveError = null">
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
.page { display: flex; flex-direction: column; gap: 16px; padding-bottom: 2rem; }
.back { margin-bottom: -8px; }
.card {
  padding: 28px;
  display: flex;
  flex-direction: column;
  gap: 18px;
  max-width: 460px;
}
.target-line { display: flex; gap: 8px; font-size: 14px; }
.target-line strong { font-weight: 500; }
.muted { color: var(--wb-muted); }
.field :deep(.p-password),
.field :deep(.p-password-input) { width: 100%; }
</style>
