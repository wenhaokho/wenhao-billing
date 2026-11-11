<script setup lang="ts">
import AppIcon from "@/components/AppIcon.vue";
import { useRouter } from "vue-router";
import { useQuery } from "@tanstack/vue-query";
import DataTable from "primevue/datatable";
import Column from "primevue/column";
import Button from "primevue/button";
import Tag from "primevue/tag";
import PageHeader from "@/components/PageHeader.vue";
import EmptyState from "@/components/EmptyState.vue";
import { api } from "@/api/client";
import { initials } from "@/utils/initials";
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
    <PageHeader eyebrow="06 / Admin" title="Users">
      <template #lede>
        Admin accounts with access to this workspace.
        Phase 1 is single-role (<code>admin</code>).
      </template>
      <Button label="Invite user" @click="openCreate">
        <template #icon><AppIcon name="plus" /></template>
      </Button>
    </PageHeader>

    <DataTable :value="data ?? []" :loading="isLoading" data-key="user_id" striped-rows>
      <template #empty>
        <EmptyState icon="users" title="No users" message="No users — seed one with scripts/seed_admin.py." />
      </template>
      <Column field="email" header="Email">
        <template #body="{ data: row }">
          <div class="who">
            <span class="avatar" aria-hidden="true">{{ initials(row.email) }}</span>
            <span class="email">{{ row.email }}</span>
            <Tag v-if="row.email === auth.user?.email" class="you" value="you" severity="info" />
          </div>
        </template>
      </Column>
      <Column header="Role">
        <template #body="{ data: row }">
          <span class="role">{{ row.role }}</span>
        </template>
      </Column>
      <Column header="Created" body-class="cell-nowrap cell-muted">
        <template #body="{ data: row }">
          {{ fmtDate(row.created_at) }}
        </template>
      </Column>
      <Column header="" :body-style="{ textAlign: 'right' }">
        <template #body="{ data: row }">
          <Button
            label="Reset password"
            size="small"
            text
            @click="openReset(row)">
            <template #icon><AppIcon name="key-round" /></template>
          </Button>
        </template>
      </Column>
    </DataTable>
  </section>
</template>

<style scoped>
.who { display: flex; align-items: center; gap: 12px; min-width: 0; }
.avatar {
  flex: none;
  width: 32px;
  height: 32px;
  border-radius: 50%;
  background: var(--wb-red);
  color: #fff;
  display: grid;
  place-items: center;
  font: 600 12px var(--wb-font-display);
}
.email { overflow-wrap: anywhere; }
.you { font-size: 0.7rem; }
.role { font: 500 13px var(--wb-font-mono); color: var(--wb-muted); }
</style>
