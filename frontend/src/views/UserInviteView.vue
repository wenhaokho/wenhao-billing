<script setup lang="ts">
import AppIcon from "@/components/AppIcon.vue";
import PageHeader from "@/components/PageHeader.vue";
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
      <Button label="Users" text size="small" @click="cancel">
        <template #icon><AppIcon name="arrow-left" /></template>
      </Button>
    </div>

    <PageHeader eyebrow="06 / Admin" title="Invite user">
      <Button label="Cancel" severity="secondary" outlined @click="cancel" />
      <Button
        label="Create"
        :disabled="!form.email || !form.password"
        :loading="createUser.isPending.value"
        @click="submit"
      />
    </PageHeader>

    <Message v-if="saveError" severity="error" role="alert" aria-live="assertive" :closable="true" @close="saveError = null">
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
.page { display: flex; flex-direction: column; gap: 16px; padding-bottom: 2rem; }
.back { margin-bottom: -8px; }
.card {
  padding: 28px;
  display: flex;
  flex-direction: column;
  gap: 18px;
  max-width: 520px;
}
.field :deep(.p-inputtext),
.field :deep(.p-password),
.field :deep(.p-password-input) { width: 100%; }
</style>
