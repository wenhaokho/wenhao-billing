<script setup lang="ts">
import { useRouter } from "vue-router";
import { useQuery } from "@tanstack/vue-query";
import DataTable from "primevue/datatable";
import Column from "primevue/column";
import Button from "primevue/button";
import Tag from "primevue/tag";
import { api } from "@/api/client";
import { useAuthStore } from "@/stores/auth";

interface User {
  user_id: string;
  email: string;
  role: string;
  created_at: string;
}

const router = useRouter();
const auth = useAuthStore();

const { data, isLoading } = useQuery<User[]>({
  queryKey: ["users"],
  queryFn: async () => (await api.get<User[]>("/users")).data,
});

function openCreate() {
  router.push({ name: "user-new" });
}

function openReset(u: User) {
  router.push({ name: "user-reset-password", params: { userId: u.user_id } });
}

function fmtDate(iso: string) {
  return new Date(iso).toLocaleDateString();
}
</script>

<template>
  <section>
    <header class="page-header">
      <div>
        <h1>Users</h1>
        <p class="subtitle">
          Admin accounts with access to this workspace.
          Phase 1 is single-role (<code>admin</code>).
        </p>
      </div>
      <div class="page-actions">
        <Button label="Invite user" icon="pi pi-plus" @click="openCreate" />
      </div>
    </header>

    <DataTable :value="data ?? []" :loading="isLoading" data-key="user_id" striped-rows>
      <template #empty>
        <div class="empty-state">
          <i class="pi pi-users" />
          <div>No users — seed one with scripts/seed_admin.py.</div>
        </div>
      </template>
      <Column field="email" header="Email">
        <template #body="{ data: row }">
          <span>{{ row.email }}</span>
          <Tag v-if="row.email === auth.user?.email" class="you" value="you" severity="info" />
        </template>
      </Column>
      <Column header="Role">
        <template #body="{ data: row }">
          <Tag :value="row.role" severity="secondary" />
        </template>
      </Column>
      <Column header="Created">
        <template #body="{ data: row }">
          {{ fmtDate(row.created_at) }}
        </template>
      </Column>
      <Column header="">
        <template #body="{ data: row }">
          <Button
            label="Reset password"
            icon="pi pi-key"
            size="small"
            text
            @click="openReset(row)"
          />
        </template>
      </Column>
    </DataTable>
  </section>
</template>

<style scoped>
.you { margin-left: 0.5rem; font-size: 0.7rem; }
code { background: var(--color-surface-alt); padding: 0.05rem 0.3rem; border-radius: 4px; }
</style>
