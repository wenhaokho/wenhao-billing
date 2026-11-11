<script setup lang="ts">
import AppIcon from "@/components/AppIcon.vue";
import type { IconName } from "@/theme/icons";
import { ref, computed } from "vue";
import { useRouter, useRoute } from "vue-router";
import { useReturnTo } from "@/composables/useReturnTo";
import { useQuery, useMutation, useQueryClient } from "@tanstack/vue-query";
import Button from "primevue/button";
import StatusTag from "@/components/StatusTag.vue";
import PageHeader from "@/components/PageHeader.vue";
import Menu from "primevue/menu";
import type { MenuItem } from "primevue/menuitem";
import Message from "primevue/message";
import { useConfirm } from "primevue/useconfirm";
import { api } from "@/api/client";
import { formatAmount } from "@/utils/money";
import SendQuotationDialog from "@/components/SendQuotationDialog.vue";

const props = defineProps<{ quotationId: string }>();
const router = useRouter();
const route = useRoute();
const { goBack } = useReturnTo("/quotations");
const queryClient = useQueryClient();
const confirm = useConfirm();

interface Customer {
  customer_id: string;
  name: string;
  contact_first_name: string | null;
  contact_last_name: string | null;
  contact_email: string | null;
  quotation_cc_emails?: string[];
  billing_cc_emails?: string[];
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
    icon: "triangle-alert",
    acceptClass: "p-button-danger",
    accept: () => voidQuote.mutate(),
  });
}
function confirmDecline() {
  confirm.require({
    message: "Mark this quotation as declined by the customer?",
    header: "Decline quotation",
    icon: "x",
    accept: () => declineQuote.mutate(),
  });
}
function confirmAccept() {
  confirm.require({
    message: "Mark this quotation as accepted by the customer?",
    header: "Accept quotation",
    icon: "check",
    accept: () => acceptQuote.mutate(),
  });
}
function confirmDelete() {
  confirm.require({
    message: "Permanently delete this DRAFT quotation?",
    header: "Delete quotation",
    icon: "trash-2",
    acceptClass: "p-button-danger",
    accept: () => deleteQuote.mutate(),
  });
}
function confirmConvert() {
  confirm.require({
    message: "Convert this quotation into a DRAFT invoice? The quotation will be marked INVOICED.",
    header: "Convert to invoice",
    icon: "file-text",
    accept: () => convertToInvoice.mutate(),
  });
}

const moreMenu = ref<InstanceType<typeof Menu> | null>(null);
const moreMenuItems = computed<MenuItem[]>(() => {
  const items: MenuItem[] = [];
  if (canAccept.value) {
    items.push({ label: "Mark as accepted", icon: "check", command: confirmAccept });
  }
  if (canDecline.value) {
    items.push({ label: "Mark as declined", icon: "x", command: confirmDecline });
  }
  if (canVoid.value) {
    items.push({ separator: true });
    items.push({ label: "Void quotation", icon: "ban", command: confirmVoid });
  }
  if (canDelete.value) {
    items.push({ label: "Delete", icon: "trash-2", command: confirmDelete });
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
        text
        size="small"
        @click="goBack">
        <template #icon><AppIcon name="arrow-left" /></template>
      </Button>
    </div>

    <div v-if="isLoading" class="loading">Loading quotation…</div>

    <template v-else-if="quote">
      <PageHeader eyebrow="02 / Sales" :title="quote.quotation_number ?? 'New quotation'">
        <template #status>
          <StatusTag :status="quote.status" />
        </template>
        <template #lede>
          <span class="subtitle">
            <span v-if="customer">{{ customer.name }}</span>
            <span v-if="customer" class="sep">·</span>
            <span class="num">{{ formatAmount(quote.amount, quote.currency) }}</span>
            <span v-if="quote.valid_until" class="sep">·</span>
            <span v-if="quote.valid_until">Valid until {{ quote.valid_until }}</span>
          </span>
        </template>
        <Button
          label="Preview PDF"
          severity="secondary"
          outlined
          @click="openPdfPreview">
          <template #icon><AppIcon name="file-down" /></template>
        </Button>
        <Button
          v-if="canEdit"
          label="Edit"
          severity="secondary"
          outlined
          @click="router.push({ path: `/quotations/${quote.quotation_id}/edit`, query: { from: route.fullPath } })">
          <template #icon><AppIcon name="pencil" /></template>
        </Button>
        <Button
          label="More actions"
          severity="secondary"
          outlined
          aria-haspopup="true"
          @click="toggleMoreMenu">
          <template #icon><AppIcon name="ellipsis-vertical" /></template>
        </Button>
        <Menu ref="moreMenu" :model="moreMenuItems" :popup="true">
          <template #itemicon="{ item, class: iconClass }">
            <AppIcon :name="item.icon as IconName" :class="iconClass" />
          </template>
        </Menu>
      </PageHeader>

      <Message v-if="errorMsg" severity="error" :closable="true" @close="errorMsg = null">
        {{ errorMsg }}
      </Message>

      <div v-if="quote.status === 'INVOICED' && quote.converted_invoice_id" class="invoiced-banner">
        <AppIcon name="circle-check" />
        <div>
          <div class="banner-title">Converted to invoice</div>
          <div class="banner-sub">This quotation was converted into a draft invoice.</div>
        </div>
        <Button
          label="Open invoice"
          icon-pos="right"
          text
          @click="router.push({ path: `/invoices/${quote.converted_invoice_id}/edit`, query: { from: route.fullPath } })">
          <template #icon><AppIcon name="arrow-right" /></template>
        </Button>
      </div>

      <!-- Workflow steps -->
      <div class="workflow">
        <!-- Step 1: Create -->
        <div class="step" :class="{ done: true }">
          <div class="step-num">
            <AppIcon name="check" />
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
                size="small"
                outlined
                @click="router.push({ path: `/quotations/${quote.quotation_id}/edit`, query: { from: route.fullPath } })">
                <template #icon><AppIcon name="pencil" /></template>
              </Button>
              <Button
                label="Preview PDF"
                size="small"
                text
                @click="openPdfPreview">
                <template #icon><AppIcon name="eye" /></template>
              </Button>
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
            <AppIcon v-if="quote.status !== 'DRAFT'" name="check" />
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
                size="small"
                @click="sendError = null; showSendDialog = true">
                <template #icon><AppIcon name="send" /></template>
              </Button>
              <Button
                v-if="canMarkSent"
                label="Mark as sent"
                size="small"
                outlined
                :loading="markSent.isPending.value"
                @click="markSent.mutate()">
                <template #icon><AppIcon name="check" /></template>
              </Button>
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
            <AppIcon v-if="quote.status === 'INVOICED'" name="check" />
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
                size="small"
                severity="success"
                :loading="convertToInvoice.isPending.value"
                @click="confirmConvert">
                <template #icon><AppIcon name="file-text" /></template>
              </Button>
              <Button
                v-if="quote.status === 'INVOICED' && quote.converted_invoice_id"
                label="Open invoice"
                icon-pos="right"
                size="small"
                text
                @click="router.push({ path: `/invoices/${quote.converted_invoice_id}/edit`, query: { from: route.fullPath } })">
                <template #icon><AppIcon name="arrow-right" /></template>
              </Button>
            </div>
          </div>
        </div>
      </div>

      <!-- Summary card -->
      <div class="summary-grid">
        <div class="card summary-card">
          <h3 class="card-title section-title">Details</h3>
          <dl class="kv">
            <dt>Quote #</dt>
            <dd>
              <span v-if="quote.quotation_number" class="doc-no">{{ quote.quotation_number }}</span>
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
          <h3 class="card-title section-title">Line items</h3>
          <div class="table-scroll">
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
                <td class="desc-col">{{ ln.description }}</td>
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
        </div>

        <div v-if="quote.notes" class="card summary-card notes-card">
          <h3 class="card-title section-title">Notes / Terms</h3>
          <p class="notes-body">{{ quote.notes }}</p>
        </div>
      </div>

      <SendQuotationDialog
        v-model:visible="showSendDialog"
        :default-to="sendDefaults.to"
        :default-cc="customer?.quotation_cc_emails ?? null"
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
  gap: 24px;
}
.quote-detail :deep(.page-head) { margin-bottom: 0; }

.back { margin-bottom: -16px; }
.loading { padding: 2rem; color: var(--wb-muted); text-align: center; }

.subtitle {
  display: inline-flex;
  gap: 8px;
  align-items: center;
  flex-wrap: wrap;
}
.subtitle .sep { color: var(--wb-muted-soft); }
.subtitle .num { font-variant-numeric: tabular-nums; font-weight: 500; color: var(--wb-ink); }

/* Converted banner: the green status pair, square, ruled on the left. */
.invoiced-banner {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 16px 20px;
  background: var(--wb-green-bg);
  border: 1px solid var(--wb-line);
  border-left: 3px solid var(--wb-green-fg);
  color: var(--wb-green-fg);
}
.invoiced-banner > .app-icon {
  width: 20px;
  height: 20px;
  flex: none;
}
.banner-title { font-weight: 500; font-size: 15px; }
.banner-sub { font-size: 13px; color: var(--wb-ink); }
.invoiced-banner > :last-child { margin-left: auto; }

/* Workflow: three square bordered steps; the current one ruled in red. */
.workflow {
  display: grid;
  grid-template-columns: 1fr 1fr 1fr;
  gap: 24px;
}
@media (max-width: 1000px) {
  .workflow { grid-template-columns: 1fr; }
}

.step {
  position: relative;
  display: flex;
  gap: 14px;
  padding: 24px;
  background: var(--wb-surface);
  border: 1px solid var(--wb-line);
  min-height: 140px;
}
.step.active { border-top: 3px solid var(--wb-red); padding-top: 22px; }
.step.done { background: var(--wb-surface-2); }
.step.disabled { opacity: 0.55; }
.step.skipped { opacity: 0.55; }

.step-num {
  width: 32px;
  height: 32px;
  min-width: 32px;
  border-radius: 50%;
  background: var(--wb-tint);
  color: var(--wb-muted);
  display: grid;
  place-items: center;
  font: 500 15px var(--wb-font-display);
}
.step.active .step-num { background: var(--wb-red); color: #fff; }
.step.done .step-num { background: var(--wb-green-bg); color: var(--wb-green-fg); }

.step-body { flex: 1; display: flex; flex-direction: column; gap: 6px; }
.step-title { font: 500 17px/1.25 var(--wb-font-display); letter-spacing: -0.3px; color: var(--wb-ink); }
.step-desc { color: var(--wb-muted); font-size: 14px; line-height: 1.5; flex: 1; }
.step-actions { display: flex; gap: 8px; flex-wrap: wrap; margin-top: 8px; }

.summary-grid {
  display: grid;
  grid-template-columns: 1fr 2fr;
  gap: 24px;
  align-items: start;
}
@media (max-width: 900px) { .summary-grid { grid-template-columns: 1fr; } }
.summary-card.notes-card { grid-column: 1 / -1; }

.card { padding: 24px 28px; min-width: 0; }
.section-title { margin: 0 0 16px; }
.kv {
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: 10px 20px;
  margin: 0;
  font-size: 14px;
}
.kv dt { color: var(--wb-muted); }
.kv dd { margin: 0; color: var(--wb-ink); }
.muted { color: var(--wb-muted-soft); }

.table-scroll { overflow-x: auto; }
.items-table {
  width: 100%;
  border-collapse: collapse;
  font-size: 14px;
}
.items-table th,
.items-table td {
  padding: 12px 10px;
  border-bottom: 1px solid var(--wb-line);
  text-align: left;
}
.items-table th { color: var(--wb-muted-soft); font: 500 11px/1.4 var(--wb-font-sans); text-transform: uppercase; letter-spacing: 1.2px; }
.items-table .num-col { text-align: right; white-space: nowrap; }
.items-table .desc-col { white-space: pre-line; }
.items-table .num { font-variant-numeric: tabular-nums; }
.items-table tfoot td { border-bottom: none; color: var(--wb-muted); padding-top: 12px; }
.items-table tfoot .total-row td { border-top: 1px solid var(--wb-line); color: var(--wb-ink); font: 500 18px var(--wb-font-display); letter-spacing: -0.4px; }
.items-table tfoot .total-row td.num { color: var(--wb-red); font-size: 22px; }

.notes-body { margin: 0; white-space: pre-wrap; color: var(--wb-muted); font-size: 15px; line-height: 1.65; }
</style>
