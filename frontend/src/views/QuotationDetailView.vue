<script setup lang="ts">
import { ref, computed } from "vue";
import { useRouter } from "vue-router";
import { useQuery, useMutation, useQueryClient } from "@tanstack/vue-query";
import Button from "primevue/button";
import Tag from "primevue/tag";
import Menu from "primevue/menu";
import type { MenuItem } from "primevue/menuitem";
import Message from "primevue/message";
import { useConfirm } from "primevue/useconfirm";
import { api } from "@/api/client";
import { formatAmount } from "@/utils/money";
import SendQuotationDialog from "@/components/SendQuotationDialog.vue";

const props = defineProps<{ quotationId: string }>();
const router = useRouter();
const queryClient = useQueryClient();
const confirm = useConfirm();

interface Customer {
  customer_id: string;
  name: string;
  contact_name: string | null;
  contact_first_name: string | null;
  contact_last_name: string | null;
  contact_email: string | null;
}

interface QuotationLineItem {
  line_item_id: string;
  item_id: string | null;
  description: string;
  quantity: string;
  unit_price: string;
  amount: string;
  position: number;
}

interface QuotationOut {
  quotation_id: string;
  customer_id: string;
  project_id: string | null;
  quotation_number: string | null;
  po_so_number: string | null;
  currency: string;
  subtotal: string | null;
  discount_type: string | null;
  discount_value: string | null;
  amount: string;
  status: string;
  issue_date: string | null;
  valid_until: string | null;
  payment_terms: string | null;
  notes: string | null;
  footer: string | null;
  last_sent_at: string | null;
  accepted_at: string | null;
  accepted_by: string | null;
  converted_invoice_id: string | null;
  line_items: QuotationLineItem[];
}

const { data: quote, isLoading } = useQuery<QuotationOut>({
  queryKey: ["quotation", props.quotationId],
  queryFn: async () =>
    (await api.get<QuotationOut>(`/quotations/${props.quotationId}`)).data,
});

const { data: customers } = useQuery<Customer[]>({
  queryKey: ["customers"],
  queryFn: async () => (await api.get<Customer[]>("/customers")).data,
});

const customer = computed(() => {
  if (!quote.value) return null;
  return (
    (customers.value ?? []).find(
      (c) => c.customer_id === quote.value!.customer_id,
    ) ?? null
  );
});

function statusSeverity(status: string) {
  switch (status) {
    case "ACCEPTED": return "success";
    case "SENT": return "info";
    case "INVOICED": return "success";
    case "EXPIRED": return "warning";
    case "DECLINED": return "danger";
    case "VOID": return "secondary";
    case "DRAFT": return "contrast";
    default: return undefined;
  }
}

const canEdit = computed(() => {
  const s = quote.value?.status;
  return s === "DRAFT" || s === "SENT";
});
const canMarkSent = computed(() => quote.value?.status === "DRAFT");
const canSendEmail = computed(() => {
  const s = quote.value?.status;
  return s === "DRAFT" || s === "SENT";
});
const canAccept = computed(() => {
  const s = quote.value?.status;
  return s === "DRAFT" || s === "SENT";
});
const canDecline = computed(() => {
  const s = quote.value?.status;
  return s === "DRAFT" || s === "SENT";
});
const canConvert = computed(() => {
  const s = quote.value?.status;
  return (s === "ACCEPTED" || s === "SENT" || s === "DRAFT") && !quote.value?.converted_invoice_id;
});
const canVoid = computed(() => {
  const s = quote.value?.status;
  return s && s !== "VOID" && s !== "INVOICED";
});
const canDelete = computed(() => quote.value?.status === "DRAFT");

const errorMsg = ref<string | null>(null);

function onError(e: { response?: { data?: { detail?: string } } }, fallback: string) {
  errorMsg.value = e?.response?.data?.detail ?? fallback;
}

function openPdfPreview() {
  window.open(`/api/v1/quotations/${props.quotationId}/pdf`, "_blank");
}

const markSent = useMutation({
  mutationFn: async () =>
    (await api.post(`/quotations/${props.quotationId}/mark-sent`)).data,
  onSuccess: () => {
    errorMsg.value = null;
    queryClient.invalidateQueries({ queryKey: ["quotation", props.quotationId] });
    queryClient.invalidateQueries({ queryKey: ["quotations"] });
  },
  onError: (e) => onError(e as any, "Failed to mark as sent"),
});

const showSendDialog = ref(false);
const sendError = ref<string | null>(null);
const sendQuote = useMutation({
  mutationFn: async (payload: {
    to_email: string;
    cc_email: string | null;
    subject: string;
    message: string;
  }) =>
    (await api.post(`/quotations/${props.quotationId}/send`, payload)).data,
  onSuccess: () => {
    sendError.value = null;
    showSendDialog.value = false;
    queryClient.invalidateQueries({ queryKey: ["quotation", props.quotationId] });
    queryClient.invalidateQueries({ queryKey: ["quotations"] });
  },
  onError: (e: { response?: { data?: { detail?: string } } }) => {
    sendError.value = e?.response?.data?.detail ?? "Failed to send quotation";
  },
});

const sendDefaults = computed(() => {
  const q = quote.value;
  const cust = customer.value;
  const num = q?.quotation_number ?? "";
  return {
    to: cust?.contact_email ?? null,
    subject: num ? `Quotation ${num}` : "Quotation",
    message:
      `Hi ${cust?.name ?? "there"},\n\n` +
      `Please find attached quotation ${num}` +
      (q ? ` for ${q.amount} ${q.currency}` : "") + ".\n" +
      (q?.valid_until ? `Valid until: ${q.valid_until}.\n` : "") +
      `\nThank you.`,
  };
});

const acceptQuote = useMutation({
  mutationFn: async () =>
    (await api.post(`/quotations/${props.quotationId}/accept`, { accepted_by: null })).data,
  onSuccess: () => {
    errorMsg.value = null;
    queryClient.invalidateQueries({ queryKey: ["quotation", props.quotationId] });
    queryClient.invalidateQueries({ queryKey: ["quotations"] });
  },
  onError: (e) => onError(e as any, "Failed to accept quotation"),
});

const declineQuote = useMutation({
  mutationFn: async () =>
    (await api.post(`/quotations/${props.quotationId}/decline`)).data,
  onSuccess: () => {
    errorMsg.value = null;
    queryClient.invalidateQueries({ queryKey: ["quotation", props.quotationId] });
    queryClient.invalidateQueries({ queryKey: ["quotations"] });
  },
  onError: (e) => onError(e as any, "Failed to decline quotation"),
});

const voidQuote = useMutation({
  mutationFn: async () =>
    (await api.post(`/quotations/${props.quotationId}/void`)).data,
  onSuccess: () => {
    errorMsg.value = null;
    queryClient.invalidateQueries({ queryKey: ["quotation", props.quotationId] });
    queryClient.invalidateQueries({ queryKey: ["quotations"] });
  },
  onError: (e) => onError(e as any, "Failed to void quotation"),
});

const deleteQuote = useMutation({
  mutationFn: async () => (await api.delete(`/quotations/${props.quotationId}`)).data,
  onSuccess: () => {
    queryClient.invalidateQueries({ queryKey: ["quotations"] });
    router.push("/quotations");
  },
  onError: (e) => onError(e as any, "Failed to delete quotation"),
});

const convertToInvoice = useMutation({
  mutationFn: async () =>
    (await api.post(`/quotations/${props.quotationId}/convert-to-invoice`, {})).data,
  onSuccess: (inv: { invoice_id: string }) => {
    errorMsg.value = null;
    queryClient.invalidateQueries({ queryKey: ["quotation", props.quotationId] });
    queryClient.invalidateQueries({ queryKey: ["quotations"] });
    queryClient.invalidateQueries({ queryKey: ["invoices"] });
    router.push(`/invoices/${inv.invoice_id}/edit`);
  },
  onError: (e) => onError(e as any, "Failed to convert to invoice"),
});

function confirmVoid() {
  confirm.require({
    message: "Void this quotation? It will no longer be valid.",
    header: "Void quotation",
    icon: "pi pi-exclamation-triangle",
    acceptClass: "p-button-danger",
    accept: () => voidQuote.mutate(),
  });
}
function confirmDecline() {
  confirm.require({
    message: "Mark this quotation as declined by the customer?",
    header: "Decline quotation",
    icon: "pi pi-times",
    accept: () => declineQuote.mutate(),
  });
}
function confirmAccept() {
  confirm.require({
    message: "Mark this quotation as accepted by the customer?",
    header: "Accept quotation",
    icon: "pi pi-check",
    accept: () => acceptQuote.mutate(),
  });
}
function confirmDelete() {
  confirm.require({
    message: "Permanently delete this DRAFT quotation?",
    header: "Delete quotation",
    icon: "pi pi-trash",
    acceptClass: "p-button-danger",
    accept: () => deleteQuote.mutate(),
  });
}
function confirmConvert() {
  confirm.require({
    message: "Convert this quotation into a DRAFT invoice? The quotation will be marked INVOICED.",
    header: "Convert to invoice",
    icon: "pi pi-file",
    accept: () => convertToInvoice.mutate(),
  });
}

const moreMenu = ref<InstanceType<typeof Menu> | null>(null);
const moreMenuItems = computed<MenuItem[]>(() => {
  const items: MenuItem[] = [];
  if (canAccept.value) {
    items.push({ label: "Mark as accepted", icon: "pi pi-check", command: confirmAccept });
  }
  if (canDecline.value) {
    items.push({ label: "Mark as declined", icon: "pi pi-times", command: confirmDecline });
  }
  if (canVoid.value) {
    items.push({ separator: true });
    items.push({ label: "Void quotation", icon: "pi pi-ban", command: confirmVoid });
  }
  if (canDelete.value) {
    items.push({ label: "Delete", icon: "pi pi-trash", command: confirmDelete });
  }
  if (!items.length) items.push({ label: "No actions available", disabled: true });
  return items;
});
function toggleMoreMenu(event: Event) {
  moreMenu.value?.toggle(event);
}
</script>

<template>
  <section class="quote-detail">
    <div class="back">
      <Button
        label="Quotations"
        icon="pi pi-arrow-left"
        text
        size="small"
        @click="router.push('/quotations')"
      />
    </div>

    <div v-if="isLoading" class="loading">Loading quotation…</div>

    <template v-else-if="quote">
      <header class="page-header">
        <div class="header-title">
          <div class="title-row">
            <h1>
              {{ quote.quotation_number ?? "New quotation" }}
            </h1>
            <Tag :value="quote.status" :severity="statusSeverity(quote.status)" />
          </div>
          <p class="subtitle">
            <span v-if="customer">{{ customer.name }}</span>
            <span v-if="customer" class="sep">·</span>
            <span class="num">{{ formatAmount(quote.amount, quote.currency) }}</span>
            <span v-if="quote.valid_until" class="sep">·</span>
            <span v-if="quote.valid_until">Valid until {{ quote.valid_until }}</span>
          </p>
        </div>
        <div class="page-actions">
          <Button
            label="Preview PDF"
            icon="pi pi-file-pdf"
            text
            @click="openPdfPreview"
          />
          <Button
            v-if="canEdit"
            label="Edit"
            icon="pi pi-pencil"
            outlined
            @click="router.push(`/quotations/${quote.quotation_id}/edit`)"
          />
          <Button
            label="More actions"
            icon="pi pi-ellipsis-v"
            severity="secondary"
            outlined
            aria-haspopup="true"
            @click="toggleMoreMenu"
          />
          <Menu ref="moreMenu" :model="moreMenuItems" :popup="true" />
        </div>
      </header>

      <Message v-if="errorMsg" severity="error" :closable="true" @close="errorMsg = null">
        {{ errorMsg }}
      </Message>

      <div v-if="quote.status === 'INVOICED' && quote.converted_invoice_id" class="invoiced-banner">
        <i class="pi pi-check-circle" />
        <div>
          <div class="banner-title">Converted to invoice</div>
          <div class="banner-sub">This quotation was converted into a draft invoice.</div>
        </div>
        <Button
          label="Open invoice"
          icon="pi pi-arrow-right"
          icon-pos="right"
          text
          @click="router.push(`/invoices/${quote.converted_invoice_id}/edit`)"
        />
      </div>

      <!-- Workflow steps -->
      <div class="workflow">
        <!-- Step 1: Create -->
        <div class="step" :class="{ done: true }">
          <div class="step-num">
            <i class="pi pi-check" />
          </div>
          <div class="step-body">
            <div class="step-title">Create quotation</div>
            <div class="step-desc">
              Estimate drafted for {{ customer?.name ?? "customer" }}.
            </div>
            <div class="step-actions">
              <Button
                v-if="canEdit"
                label="Edit estimate"
                icon="pi pi-pencil"
                size="small"
                outlined
                @click="router.push(`/quotations/${quote.quotation_id}/edit`)"
              />
              <Button
                label="Preview PDF"
                icon="pi pi-eye"
                size="small"
                text
                @click="openPdfPreview"
              />
            </div>
          </div>
        </div>

        <!-- Step 2: Send -->
        <div
          class="step"
          :class="{
            done: quote.status !== 'DRAFT',
            active: quote.status === 'DRAFT',
            skipped: quote.status === 'VOID' && !quote.last_sent_at,
          }"
        >
          <div class="step-num">
            <i v-if="quote.status !== 'DRAFT'" class="pi pi-check" />
            <span v-else>2</span>
          </div>
          <div class="step-body">
            <div class="step-title">Send to customer</div>
            <div class="step-desc">
              <template v-if="quote.last_sent_at">
                Last sent {{ new Date(quote.last_sent_at).toLocaleString() }}.
              </template>
              <template v-else-if="quote.status === 'DRAFT'">
                Email the quotation PDF, or mark it as sent if you've already delivered it.
              </template>
              <template v-else>
                Quotation has been shared with the customer.
              </template>
            </div>
            <div class="step-actions">
              <Button
                v-if="canSendEmail"
                label="Send estimate"
                icon="pi pi-send"
                size="small"
                @click="sendError = null; showSendDialog = true"
              />
              <Button
                v-if="canMarkSent"
                label="Mark as sent"
                icon="pi pi-check"
                size="small"
                outlined
                :loading="markSent.isPending.value"
                @click="markSent.mutate()"
              />
            </div>
          </div>
        </div>

        <!-- Step 3: Convert to invoice -->
        <div
          class="step"
          :class="{
            done: quote.status === 'INVOICED',
            active:
              quote.status === 'ACCEPTED' ||
              (quote.status === 'SENT' && !quote.converted_invoice_id),
            disabled: quote.status === 'VOID' || quote.status === 'DECLINED' || quote.status === 'EXPIRED',
          }"
        >
          <div class="step-num">
            <i v-if="quote.status === 'INVOICED'" class="pi pi-check" />
            <span v-else>3</span>
          </div>
          <div class="step-body">
            <div class="step-title">Convert to invoice</div>
            <div class="step-desc">
              <template v-if="quote.status === 'INVOICED'">
                A DRAFT invoice has been created from this quotation.
              </template>
              <template v-else-if="quote.status === 'VOID' || quote.status === 'DECLINED' || quote.status === 'EXPIRED'">
                Not available — quotation is {{ quote.status.toLowerCase() }}.
              </template>
              <template v-else>
                When the customer accepts, convert this estimate into a DRAFT invoice in one click.
              </template>
            </div>
            <div class="step-actions">
              <Button
                v-if="canConvert"
                label="Convert to invoice"
                icon="pi pi-file"
                size="small"
                severity="success"
                :loading="convertToInvoice.isPending.value"
                @click="confirmConvert"
              />
              <Button
                v-if="quote.status === 'INVOICED' && quote.converted_invoice_id"
                label="Open invoice"
                icon="pi pi-arrow-right"
                icon-pos="right"
                size="small"
                text
                @click="router.push(`/invoices/${quote.converted_invoice_id}/edit`)"
              />
            </div>
          </div>
        </div>
      </div>

      <!-- Summary card -->
      <div class="summary-grid">
        <div class="card summary-card">
          <h3 class="section-title">Details</h3>
          <dl class="kv">
            <dt>Quote #</dt>
            <dd>
              <code v-if="quote.quotation_number">{{ quote.quotation_number }}</code>
              <span v-else class="muted">—</span>
            </dd>
            <dt>P.O. / S.O.</dt>
            <dd>{{ quote.po_so_number || "—" }}</dd>
            <dt>Issue date</dt>
            <dd>{{ quote.issue_date || "—" }}</dd>
            <dt>Valid until</dt>
            <dd>{{ quote.valid_until || "—" }}</dd>
            <dt>Currency</dt>
            <dd>{{ quote.currency }}</dd>
            <dt v-if="quote.accepted_at">Accepted</dt>
            <dd v-if="quote.accepted_at">
              {{ new Date(quote.accepted_at).toLocaleString() }}
              <span v-if="quote.accepted_by" class="muted"> · {{ quote.accepted_by }}</span>
            </dd>
          </dl>
        </div>

        <div class="card summary-card">
          <h3 class="section-title">Line items</h3>
          <table class="items-table">
            <thead>
              <tr>
                <th>Description</th>
                <th class="num-col">Qty</th>
                <th class="num-col">Unit price</th>
                <th class="num-col">Amount</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="ln in quote.line_items.slice().sort((a, b) => a.position - b.position)" :key="ln.line_item_id">
                <td>{{ ln.description }}</td>
                <td class="num-col num">{{ ln.quantity }}</td>
                <td class="num-col num">{{ formatAmount(ln.unit_price, quote.currency) }}</td>
                <td class="num-col num">{{ formatAmount(ln.amount, quote.currency) }}</td>
              </tr>
            </tbody>
            <tfoot>
              <tr v-if="quote.subtotal">
                <td colspan="3" class="num-col">Subtotal</td>
                <td class="num-col num">{{ formatAmount(quote.subtotal, quote.currency) }}</td>
              </tr>
              <tr v-if="quote.discount_type && quote.discount_value">
                <td colspan="3" class="num-col">
                  Discount ({{ quote.discount_type === "PERCENT" ? `${quote.discount_value}%` : "fixed" }})
                </td>
                <td class="num-col num">—</td>
              </tr>
              <tr class="total-row">
                <td colspan="3" class="num-col">Total</td>
                <td class="num-col num">{{ formatAmount(quote.amount, quote.currency) }}</td>
              </tr>
            </tfoot>
          </table>
        </div>

        <div v-if="quote.notes" class="card summary-card notes-card">
          <h3 class="section-title">Notes / Terms</h3>
          <p class="notes-body">{{ quote.notes }}</p>
        </div>
      </div>

      <SendQuotationDialog
        v-model:visible="showSendDialog"
        :default-to="sendDefaults.to"
        :default-subject="sendDefaults.subject"
        :default-message="sendDefaults.message"
        :loading="sendQuote.isPending.value"
        :error="sendError"
        @submit="sendQuote.mutate($event)"
      />
    </template>
  </section>
</template>

<style scoped>
.quote-detail {
  display: flex;
  flex-direction: column;
  gap: 1.25rem;
}

.back { margin-bottom: -0.5rem; }
.loading { padding: 2rem; color: var(--color-text-muted); text-align: center; }

.page-header {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 1rem;
}
.title-row { display: flex; align-items: center; gap: 0.75rem; }
.title-row h1 { margin: 0; }
.subtitle {
  margin: 0.35rem 0 0;
  color: var(--color-text-muted, #64748b);
  font-size: 0.9rem;
  display: flex;
  gap: 0.5rem;
  align-items: center;
  flex-wrap: wrap;
}
.subtitle .sep { color: var(--color-border-strong); }
.subtitle .num { font-variant-numeric: tabular-nums; font-weight: 600; color: var(--color-text); }
.page-actions { display: flex; gap: 0.5rem; align-items: center; }

.invoiced-banner {
  display: flex;
  align-items: center;
  gap: 0.9rem;
  padding: 0.9rem 1.1rem;
  background: #ecfdf5;
  border: 1px solid #86efac;
  border-radius: 10px;
  color: #065f46;
}
.invoiced-banner > i {
  font-size: 1.3rem;
  color: #10b981;
}
.banner-title { font-weight: 600; font-size: 0.95rem; }
.banner-sub { font-size: 0.82rem; color: #047857; }
.invoiced-banner > :last-child { margin-left: auto; }

.workflow {
  display: grid;
  grid-template-columns: 1fr 1fr 1fr;
  gap: 1rem;
}
@media (max-width: 1000px) {
  .workflow { grid-template-columns: 1fr; }
}

.step {
  position: relative;
  display: flex;
  gap: 0.9rem;
  padding: 1.1rem 1.25rem;
  background: var(--color-surface, #fff);
  border: 1px solid var(--color-border, #e2e8f0);
  border-radius: 12px;
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.04);
  min-height: 140px;
}
.step.active { border-color: #2563eb; box-shadow: 0 0 0 3px rgba(37, 99, 235, 0.12); }
.step.done { background: var(--color-surface-alt); }
.step.disabled { opacity: 0.55; }
.step.skipped { opacity: 0.55; }

.step-num {
  width: 32px;
  height: 32px;
  min-width: 32px;
  border-radius: 50%;
  background: #e2e8f0;
  color: var(--color-text-muted);
  display: grid;
  place-items: center;
  font-weight: 600;
  font-size: 0.9rem;
}
.step.active .step-num { background: #2563eb; color: white; }
.step.done .step-num { background: #10b981; color: white; }

.step-body { flex: 1; display: flex; flex-direction: column; gap: 0.4rem; }
.step-title { font-weight: 600; color: var(--color-text); font-size: 0.98rem; }
.step-desc { color: var(--color-text-muted); font-size: 0.85rem; line-height: 1.45; flex: 1; }
.step-actions { display: flex; gap: 0.5rem; flex-wrap: wrap; margin-top: 0.35rem; }

.summary-grid {
  display: grid;
  grid-template-columns: 1fr 2fr;
  gap: 1rem;
  align-items: start;
}
@media (max-width: 900px) { .summary-grid { grid-template-columns: 1fr; } }
.summary-card.notes-card { grid-column: 1 / -1; }

.card {
  background: var(--color-surface, #fff);
  border: 1px solid var(--color-border, #e2e8f0);
  border-radius: 12px;
  padding: 1.25rem 1.5rem;
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.04);
}
.section-title {
  margin: 0 0 0.9rem;
  font-size: 0.82rem;
  font-weight: 600;
  color: var(--color-text-muted);
  text-transform: uppercase;
  letter-spacing: 0.06em;
}
.kv {
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: 0.5rem 1rem;
  margin: 0;
  font-size: 0.875rem;
}
.kv dt { color: var(--color-text-muted); }
.kv dd { margin: 0; color: var(--color-text); }
code { background: var(--color-bg); padding: 0.05rem 0.35rem; border-radius: 4px; font-size: 0.82em; }
.muted { color: var(--color-text-subtle); }

.items-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 0.88rem;
}
.items-table th,
.items-table td {
  padding: 0.55rem 0.6rem;
  border-bottom: 1px solid #f1f5f9;
  text-align: left;
}
.items-table th { color: var(--color-text-muted); font-weight: 600; font-size: 0.78rem; text-transform: uppercase; letter-spacing: 0.04em; }
.items-table .num-col { text-align: right; }
.items-table .num { font-variant-numeric: tabular-nums; }
.items-table tfoot td { border-bottom: none; color: var(--color-text-muted); padding-top: 0.7rem; }
.items-table tfoot .total-row td { font-weight: 700; color: var(--color-text); font-size: 1rem; border-top: 2px solid var(--color-border); }

.notes-body { margin: 0; white-space: pre-wrap; color: var(--color-text); font-size: 0.9rem; line-height: 1.5; }
</style>
