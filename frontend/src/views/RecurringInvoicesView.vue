<script setup lang="ts">
import { computed } from "vue";
import { useRouter } from "vue-router";
import { useQuery, useMutation, useQueryClient } from "@tanstack/vue-query";
import { useConfirm } from "primevue/useconfirm";
import DataTable from "primevue/datatable";
import Column from "primevue/column";
import Button from "primevue/button";
import Tag from "primevue/tag";
import { api } from "@/api/client";
import { formatAmount } from "@/utils/money";

interface RecurringRow {
  template_id: string;
  customer_id: string | null;
  customer_name: string | null;
  currency: string;
  amount: string;
  payment_terms: string | null;
  schedule_description: string;
  frequency: string | null;
  start_date: string | null;
  end_mode: string | null;
  next_run_date: string | null;
  previous_issue_date: string | null;
  generated_count: number;
  status: string;
}

const router = useRouter();
const confirm = useConfirm();
const queryClient = useQueryClient();

const patchTemplate = useMutation({
  mutationFn: async (vars: { templateId: string; action: string }) =>
    (
      await api.patch(`/invoices/recurring-templates/${vars.templateId}`, {
        action: vars.action,
      })
    ).data,
  onSuccess: () =>
    queryClient.invalidateQueries({ queryKey: ["recurring-template-rows"] }),
});

function pauseTemplate(row: RecurringRow) {
  patchTemplate.mutate({ templateId: row.template_id, action: "PAUSE" });
}
function resumeTemplate(row: RecurringRow) {
  patchTemplate.mutate({ templateId: row.template_id, action: "RESUME" });
}
function confirmEnd(row: RecurringRow) {
  confirm.require({
    message: `End "${row.customer_name ?? 'template'}" now? No further invoices will be generated.`,
    header: "End recurring template",
    icon: "pi pi-exclamation-triangle",
    acceptLabel: "End now",
    rejectLabel: "Cancel",
    acceptClass: "p-button-danger",
    accept: () =>
      patchTemplate.mutate({ templateId: row.template_id, action: "END_NOW" }),
  });
}

const { data, isLoading } = useQuery<RecurringRow[]>({
  queryKey: ["recurring-template-rows"],
  queryFn: async () =>
    (await api.get<RecurringRow[]>("/invoices/recurring-templates/rows")).data,
});

const rows = computed(() => data.value ?? []);

function openNew() {
  router.push({ name: "invoice-recurring-new" });
}

function statusSeverity(status: string) {
  return status === "ACTIVE"
    ? "success"
    : status === "ENDED"
      ? "secondary"
      : "warning";
}

function fmtDate(d: string | null) {
  return d ?? "—";
}
</script>

<template>
  <section>
    <header class="page-header">
      <div>
        <h1>Recurring invoices</h1>
        <p class="subtitle">
          Templates that auto-generate DRAFT invoices on a schedule. Each
          generated invoice still needs to be reviewed and sent.
        </p>
      </div>
      <div class="page-actions">
        <Button label="Back to invoices" icon="pi pi-arrow-left" text @click="router.push('/invoices')" />
        <Button label="New recurring invoice" icon="pi pi-plus" @click="openNew" />
      </div>
    </header>

    <DataTable
      :value="rows"
      :loading="isLoading"
      data-key="template_id"
      striped-rows
    >
      <template #empty>
        <div class="empty-state">
          <i class="pi pi-replay" />
          <div>
            No recurring templates yet. Click
            <strong>New recurring invoice</strong> to create one.
          </div>
        </div>
      </template>

      <Column field="customer_name" header="Customer">
        <template #body="{ data: row }">
          <span v-if="row.customer_name">{{ row.customer_name }}</span>
          <span v-else class="muted">—</span>
        </template>
      </Column>

      <Column header="Schedule">
        <template #body="{ data: row }">
          <div>{{ row.schedule_description }}</div>
          <div v-if="row.start_date" class="muted small">Started {{ row.start_date }}</div>
        </template>
      </Column>

      <Column header="Amount" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }">
        <template #body="{ data: row }">
          <span class="num">{{ formatAmount(row.amount, row.currency) }} {{ row.currency }}</span>
        </template>
      </Column>

      <Column header="Previous">
        <template #body="{ data: row }">{{ fmtDate(row.previous_issue_date) }}</template>
      </Column>

      <Column header="Next">
        <template #body="{ data: row }">{{ fmtDate(row.next_run_date) }}</template>
      </Column>

      <Column header="Generated" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }">
        <template #body="{ data: row }">{{ row.generated_count }}</template>
      </Column>

      <Column header="Status">
        <template #body="{ data: row }">
          <Tag :value="row.status" :severity="statusSeverity(row.status)" />
        </template>
      </Column>

      <Column header="" :style="{ width: '220px' }">
        <template #body="{ data: row }">
          <div class="row-actions">
            <Button
              icon="pi pi-pencil"
              text
              rounded
              title="Edit"
              @click="router.push({ name: 'invoice-recurring-edit', params: { id: row.template_id } })"
            />
            <Button
              v-if="row.status === 'ACTIVE'"
              icon="pi pi-pause"
              text
              rounded
              title="Pause"
              :loading="patchTemplate.isPending.value"
              @click="pauseTemplate(row)"
            />
            <Button
              v-else-if="row.status === 'PAUSED'"
              icon="pi pi-play"
              text
              rounded
              severity="success"
              title="Resume"
              :loading="patchTemplate.isPending.value"
              @click="resumeTemplate(row)"
            />
            <Button
              v-if="row.status !== 'ENDED'"
              icon="pi pi-stop-circle"
              text
              rounded
              severity="danger"
              title="End now"
              :loading="patchTemplate.isPending.value"
              @click="confirmEnd(row)"
            />
          </div>
        </template>
      </Column>
    </DataTable>
  </section>
</template>

<style scoped>
.muted { color: #94a3b8; }
.small { font-size: 0.8rem; }
.num { font-variant-numeric: tabular-nums; }
.row-actions { display: flex; gap: 0.15rem; align-items: center; }
</style>
