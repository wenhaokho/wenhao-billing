<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useRouter } from "vue-router";
import { useQuery, useMutation, useQueryClient } from "@tanstack/vue-query";
import DataTable from "primevue/datatable";
import Column from "primevue/column";
import Button from "primevue/button";
import Chip from "primevue/chip";
import Tag from "primevue/tag";
import TabView from "primevue/tabview";
import TabPanel from "primevue/tabpanel";
import Message from "primevue/message";
import { api } from "@/api/client";
import { formatAmount, formatMoney } from "@/utils/money";
import CustomerForm from "@/components/CustomerForm.vue";
import {
  emptyCustomerForm,
  buildCustomerPayload,
  type CustomerFormData,
} from "@/components/customerForm";

const props = defineProps<{ customerId: string }>();
const router = useRouter();

interface Customer {
  customer_id: string;
  name: string;
  matching_aliases: string[];
  active: boolean;
  contact_name: string | null;
  contact_first_name: string | null;
  contact_last_name: string | null;
  contact_email: string | null;
  contact_phone: string | null;
  contact_phone_2: string | null;
  account_number: string | null;
  website: string | null;
  notes: string | null;
  default_currency: string | null;
  billing_address: string | null;
  billing_address1: string | null;
  billing_address2: string | null;
  billing_country: string | null;
  billing_state: string | null;
  billing_city: string | null;
  billing_postal_code: string | null;
  ship_to_name: string | null;
  shipping_address1: string | null;
  shipping_address2: string | null;
  shipping_country: string | null;
  shipping_state: string | null;
  shipping_city: string | null;
  shipping_postal_code: string | null;
  shipping_phone: string | null;
  shipping_delivery_instructions: string | null;
}

interface InvoiceRow {
  invoice_id: string;
  invoice_type: string;
  currency: string;
  amount: string;
  balance_due: string;
  status: string;
  issue_date: string;
  due_date: string;
}

interface CurrencyAmount {
  currency: string;
  amount: string;
}

interface BillingSummary {
  milestone: InvoiceRow[];
  recurring: InvoiceRow[];
  usage: InvoiceRow[];
  unpaid_invoices: InvoiceRow[];
  total_unpaid: CurrencyAmount[];
  paid_last_12_months: CurrencyAmount[];
  last_invoice_issue_date: string | null;
  last_invoice_id: string | null;
}

const customerQuery = useQuery<Customer>({
  queryKey: ["customer", () => props.customerId],
  queryFn: async () =>
    (await api.get<Customer>(`/customers/${props.customerId}`)).data,
});

const billingQuery = useQuery<BillingSummary>({
  queryKey: ["billing-summary", () => props.customerId],
  queryFn: async () =>
    (await api.get<BillingSummary>(`/customers/${props.customerId}/billing-summary`)).data,
});

const invoicesQuery = useQuery<InvoiceRow[]>({
  queryKey: ["customer-invoices", () => props.customerId],
  queryFn: async () =>
    (await api.get<InvoiceRow[]>(`/customers/${props.customerId}/invoices`)).data,
});

interface QuotationRow {
  quotation_id: string;
  quotation_number: string | null;
  currency: string;
  amount: string;
  status: string;
  issue_date: string | null;
  valid_until: string | null;
}

const quotationsQuery = useQuery<QuotationRow[]>({
  queryKey: ["customer-quotations", () => props.customerId],
  queryFn: async () =>
    (
      await api.get<QuotationRow[]>("/quotations", {
        params: { customer_id: props.customerId },
      })
    ).data,
});

function quotationStatusSeverity(status: string): "success" | "info" | "warning" | "secondary" | "danger" | "contrast" | undefined {
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

const today = new Date();

function daysUntilDue(dueDate: string): number {
  const d = new Date(dueDate + "T00:00:00");
  const diffMs = d.getTime() - today.getTime();
  return Math.round(diffMs / (1000 * 60 * 60 * 24));
}

function isOverdue(row: InvoiceRow): boolean {
  if (row.status !== "SENT" && row.status !== "PARTIAL") return false;
  return daysUntilDue(row.due_date) < 0;
}

function dueLabel(row: InvoiceRow): string {
  const days = daysUntilDue(row.due_date);
  if (days < 0) return `Due ${Math.abs(days)} days ago`;
  if (days === 0) return "Due today";
  return `Due in ${days} days`;
}

function statusSeverity(status: string): "success" | "info" | "warning" | "secondary" | "danger" {
  switch (status) {
    case "PAID": return "success";
    case "SENT": return "info";
    case "PARTIAL": return "warning";
    case "DRAFT": return "secondary";
    case "VOID": return "danger";
    default: return "secondary";
  }
}

function invoiceTypeLabel(type: string): string {
  return type.charAt(0) + type.slice(1).toLowerCase();
}

function formatSumList(list: CurrencyAmount[] | undefined): string {
  if (!list || list.length === 0) return "—";
  return list.map((c) => formatMoney(c.amount, c.currency)).join(" · ");
}

const lastItemLabel = computed(() => {
  const s = billingQuery.data.value;
  if (!s || !s.last_invoice_issue_date) return null;
  const d = new Date(s.last_invoice_issue_date + "T00:00:00");
  return `Invoice on ${d.toLocaleDateString(undefined, { month: "short", day: "numeric", year: "numeric" })}`;
});

function displayContactName(c: Customer): string {
  const full = [c.contact_first_name, c.contact_last_name].filter(Boolean).join(" ").trim();
  return full || c.contact_name || "";
}

function composedBillingAddress(c: Customer): string {
  const cityLine = [c.billing_city, c.billing_state, c.billing_postal_code]
    .filter(Boolean)
    .join(", ");
  const parts = [c.billing_address1, c.billing_address2, cityLine, c.billing_country].filter(
    Boolean,
  );
  const composed = parts.join("\n");
  return composed || c.billing_address || "";
}

function goBack() {
  router.push({ name: "customers" });
}

const queryClient = useQueryClient();
const activeTab = ref(0);
const form = ref<CustomerFormData>(emptyCustomerForm());
const saveError = ref<string | null>(null);
const saveOk = ref(false);

function customerToForm(c: Customer): CustomerFormData {
  return {
    name: c.name ?? "",
    matching_aliases: [...(c.matching_aliases ?? [])],
    active: c.active,
    contact_name: c.contact_name ?? "",
    contact_first_name: c.contact_first_name ?? "",
    contact_last_name: c.contact_last_name ?? "",
    contact_email: c.contact_email ?? "",
    contact_phone: c.contact_phone ?? "",
    contact_phone_2: c.contact_phone_2 ?? "",
    account_number: c.account_number ?? "",
    website: c.website ?? "",
    notes: c.notes ?? "",
    default_currency: c.default_currency ?? "IDR",
    billing_address: c.billing_address ?? "",
    billing_address1: c.billing_address1 ?? "",
    billing_address2: c.billing_address2 ?? "",
    billing_country: c.billing_country ?? "",
    billing_state: c.billing_state ?? "",
    billing_city: c.billing_city ?? "",
    billing_postal_code: c.billing_postal_code ?? "",
    ship_to_name: c.ship_to_name ?? "",
    shipping_address1: c.shipping_address1 ?? "",
    shipping_address2: c.shipping_address2 ?? "",
    shipping_country: c.shipping_country ?? "",
    shipping_state: c.shipping_state ?? "",
    shipping_city: c.shipping_city ?? "",
    shipping_postal_code: c.shipping_postal_code ?? "",
    shipping_phone: c.shipping_phone ?? "",
    shipping_delivery_instructions: c.shipping_delivery_instructions ?? "",
  };
}

watch(
  () => customerQuery.data.value,
  (c) => {
    if (c) form.value = customerToForm(c);
  },
  { immediate: true },
);

const saveCustomer = useMutation({
  mutationFn: async () =>
    api.patch<Customer>(
      `/customers/${props.customerId}`,
      buildCustomerPayload(form.value),
    ),
  onSuccess: () => {
    saveOk.value = true;
    saveError.value = null;
    queryClient.invalidateQueries({ queryKey: ["customer", () => props.customerId] });
    queryClient.invalidateQueries({ queryKey: ["customers"] });
    queryClient.invalidateQueries({ queryKey: ["billing-summary", () => props.customerId] });
  },
  onError: (err: unknown) => {
    saveOk.value = false;
    const detail = (err as { response?: { data?: { detail?: string } } })?.response?.data?.detail;
    saveError.value = detail ?? "Save failed";
  },
});

// Auto-open Edit tab if navigated here with ?edit=1
watch(
  [() => router.currentRoute.value.query.edit, () => customerQuery.data.value],
  ([flag, cust]) => {
    if (flag && cust) {
      activeTab.value = 3;
      router.replace({ query: {} });
    }
  },
  { immediate: true },
);
</script>

<template>
  <section>
    <div class="back">
      <Button
        label="Customers"
        icon="pi pi-arrow-left"
        text
        size="small"
        @click="goBack"
      />
    </div>

    <Message v-if="customerQuery.error.value" severity="error" :closable="false">
      Failed to load customer.
    </Message>

    <div v-if="customerQuery.data.value" class="cd-header">
      <h1 class="cd-title">{{ customerQuery.data.value.name }}</h1>
      <div class="cd-actions">
        <Button
          v-if="activeTab === 3"
          label="Save changes"
          :disabled="!form.name"
          :loading="saveCustomer.isPending.value"
          @click="saveCustomer.mutate()"
        />
        <Button v-else label="Edit customer" outlined @click="activeTab = 3" />
      </div>
    </div>

    <div v-if="customerQuery.data.value" class="cd-layout">
      <aside class="cd-sidebar">
        <div class="cd-card">
          <div class="cd-avatar">
            <i class="pi pi-user" />
          </div>

          <div class="cd-group">
            <div class="cd-label">Primary contact</div>
            <div v-if="displayContactName(customerQuery.data.value)" class="cd-strong">
              {{ displayContactName(customerQuery.data.value) }}
            </div>
            <div v-else class="muted">No contact set</div>
            <div v-if="customerQuery.data.value.contact_email" class="cd-line">
              <i class="pi pi-envelope" />
              <a :href="`mailto:${customerQuery.data.value.contact_email}`">
                {{ customerQuery.data.value.contact_email }}
              </a>
            </div>
            <div v-if="customerQuery.data.value.contact_phone" class="cd-line">
              <i class="pi pi-phone" />
              <span>{{ customerQuery.data.value.contact_phone }}</span>
            </div>
            <div v-if="customerQuery.data.value.website" class="cd-line">
              <i class="pi pi-link" />
              <a :href="customerQuery.data.value.website" target="_blank" rel="noopener">
                {{ customerQuery.data.value.website }}
              </a>
            </div>
          </div>

          <div class="cd-divider" />

          <div class="cd-group">
            <div class="cd-label">Billing address</div>
            <div v-if="composedBillingAddress(customerQuery.data.value)" class="cd-address">
              {{ composedBillingAddress(customerQuery.data.value) }}
            </div>
            <div v-else class="muted">No address set</div>
          </div>

          <div
            v-if="customerQuery.data.value.account_number || customerQuery.data.value.default_currency"
            class="cd-divider"
          />

          <div v-if="customerQuery.data.value.account_number" class="cd-group">
            <div class="cd-label">Account number</div>
            <div>{{ customerQuery.data.value.account_number }}</div>
          </div>

          <div v-if="customerQuery.data.value.default_currency" class="cd-group">
            <div class="cd-label">Default currency</div>
            <div>{{ customerQuery.data.value.default_currency }}</div>
          </div>

          <div class="cd-divider" />

          <div class="cd-group">
            <div class="cd-label">Status</div>
            <Tag
              :value="customerQuery.data.value.active ? 'Active' : 'Inactive'"
              :severity="customerQuery.data.value.active ? 'success' : 'secondary'"
            />
          </div>

          <div
            v-if="customerQuery.data.value.matching_aliases.length"
            class="cd-group"
          >
            <div class="cd-label">Matching aliases</div>
            <div class="chips">
              <Chip
                v-for="a in customerQuery.data.value.matching_aliases"
                :key="a"
                :label="a"
              />
            </div>
          </div>

          <div class="cd-group">
            <div class="cd-label">Customer ID</div>
            <code class="small">{{ customerQuery.data.value.customer_id }}</code>
          </div>
        </div>
      </aside>

      <div class="cd-main">
        <TabView v-model:activeIndex="activeTab">
          <TabPanel header="Overview">
            <div class="kpi-row">
              <div class="kpi">
                <div class="kpi-label">Paid last 12 months</div>
                <div class="kpi-value">
                  {{ formatSumList(billingQuery.data.value?.paid_last_12_months) }}
                </div>
              </div>
              <div class="kpi">
                <div class="kpi-label">Total unpaid</div>
                <div class="kpi-value">
                  {{ formatSumList(billingQuery.data.value?.total_unpaid) }}
                </div>
              </div>
              <div class="kpi">
                <div class="kpi-label">Last item sent</div>
                <div class="kpi-value kpi-link">
                  {{ lastItemLabel ?? "—" }}
                </div>
              </div>
            </div>

            <Message
              v-if="billingQuery.error.value"
              severity="error"
              :closable="false"
            >
              Failed to load billing summary.
            </Message>

            <div class="card-section">
              <div class="section-head">
                <h3>Unpaid invoices</h3>
              </div>
              <DataTable
                :value="billingQuery.data.value?.unpaid_invoices ?? []"
                :loading="billingQuery.isLoading.value"
                size="small"
                striped-rows
                data-key="invoice_id"
              >
                <template #empty>
                  <div class="empty-mini">No unpaid invoices.</div>
                </template>
                <Column header="Status" :style="{ width: '120px' }">
                  <template #body="{ data: row }">
                    <Tag
                      v-if="isOverdue(row)"
                      value="Overdue"
                      severity="danger"
                    />
                    <Tag
                      v-else
                      :value="row.status"
                      :severity="statusSeverity(row.status)"
                    />
                  </template>
                </Column>
                <Column header="Type" :style="{ width: '110px' }">
                  <template #body="{ data: row }">
                    {{ invoiceTypeLabel(row.invoice_type) }}
                  </template>
                </Column>
                <Column header="Invoice">
                  <template #body="{ data: row }">
                    <code class="small">{{ row.invoice_id.slice(0, 8) }}</code>
                  </template>
                </Column>
                <Column header="Due">
                  <template #body="{ data: row }">
                    <span :class="{ 'text-danger': isOverdue(row) }">
                      {{ dueLabel(row) }}
                    </span>
                  </template>
                </Column>
                <Column header="Amount" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }">
                  <template #body="{ data: row }">
                    <span class="num">{{ formatAmount(row.balance_due, row.currency) }}</span>
                    <span class="ccy"> {{ row.currency }}</span>
                  </template>
                </Column>
              </DataTable>
            </div>
          </TabPanel>

          <TabPanel header="Invoices">
            <DataTable
              :value="invoicesQuery.data.value ?? []"
              :loading="invoicesQuery.isLoading.value"
              size="small"
              striped-rows
              data-key="invoice_id"
              :rows="20"
              paginator
            >
              <template #empty>
                <div class="empty-mini">No invoices for this customer.</div>
              </template>
              <Column header="Status" :style="{ width: '120px' }">
                <template #body="{ data: row }">
                  <Tag
                    v-if="isOverdue(row)"
                    value="Overdue"
                    severity="danger"
                  />
                  <Tag
                    v-else
                    :value="row.status"
                    :severity="statusSeverity(row.status)"
                  />
                </template>
              </Column>
              <Column header="Type">
                <template #body="{ data: row }">
                  {{ invoiceTypeLabel(row.invoice_type) }}
                </template>
              </Column>
              <Column field="issue_date" header="Issued" />
              <Column field="due_date" header="Due" />
              <Column header="Amount" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }">
                <template #body="{ data: row }">
                  <span class="num">{{ formatAmount(row.amount, row.currency) }}</span>
                  <span class="ccy"> {{ row.currency }}</span>
                </template>
              </Column>
              <Column header="Balance" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }">
                <template #body="{ data: row }">
                  <span class="num">{{ formatAmount(row.balance_due, row.currency) }}</span>
                  <span class="ccy"> {{ row.currency }}</span>
                </template>
              </Column>
            </DataTable>
          </TabPanel>

          <TabPanel header="Quotations">
            <DataTable
              :value="quotationsQuery.data.value ?? []"
              :loading="quotationsQuery.isLoading.value"
              size="small"
              striped-rows
              data-key="quotation_id"
              :rows="20"
              paginator
              selection-mode="single"
              @row-click="(ev) => router.push(`/quotations/${(ev.data as QuotationRow).quotation_id}`)"
            >
              <template #empty>
                <div class="empty-mini">No quotations for this customer.</div>
              </template>
              <Column header="Quote #">
                <template #body="{ data: row }">
                  <code v-if="row.quotation_number" class="small">{{ row.quotation_number }}</code>
                  <span v-else class="muted">—</span>
                </template>
              </Column>
              <Column header="Status" :style="{ width: '120px' }">
                <template #body="{ data: row }">
                  <Tag :value="row.status" :severity="quotationStatusSeverity(row.status)" />
                </template>
              </Column>
              <Column field="issue_date" header="Issued" />
              <Column field="valid_until" header="Valid until" />
              <Column header="Amount" :body-style="{ textAlign: 'right' }" :header-style="{ textAlign: 'right' }">
                <template #body="{ data: row }">
                  <span class="num">{{ formatAmount(row.amount, row.currency) }}</span>
                  <span class="ccy"> {{ row.currency }}</span>
                </template>
              </Column>
            </DataTable>
          </TabPanel>

          <TabPanel header="Edit">
            <div class="edit-wrap">
              <Message v-if="saveOk" severity="success" :closable="true" @close="saveOk = false">
                Customer updated.
              </Message>
              <Message v-if="saveError" severity="error" :closable="true" @close="saveError = null">
                {{ saveError }}
              </Message>

              <CustomerForm v-model="form" />
            </div>
          </TabPanel>
        </TabView>
      </div>
    </div>
  </section>
</template>

<style scoped>
.back { margin-bottom: -0.5rem; }

.cd-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  margin-bottom: 1.25rem;
}
.cd-title { margin: 0; font-size: 1.5rem; text-transform: uppercase; letter-spacing: 0.02em; }
.cd-actions { display: flex; gap: 0.5rem; }

.cd-layout {
  display: grid;
  grid-template-columns: 300px 1fr;
  gap: 1.25rem;
  align-items: start;
}
@media (max-width: 900px) {
  .cd-layout { grid-template-columns: 1fr; }
}

.cd-sidebar { position: sticky; top: 1rem; }
.cd-card {
  background: var(--color-surface, #fff);
  border: 1px solid var(--color-border, #e2e8f0);
  border-radius: 10px;
  padding: 1rem;
  display: flex;
  flex-direction: column;
  gap: 0.9rem;
}
.cd-avatar {
  width: 120px; height: 120px;
  border-radius: 50%;
  background: #dbeafe;
  color: #2563eb;
  display: grid; place-items: center;
  font-size: 3.5rem;
  align-self: center;
  border: 2px dashed #93c5fd;
}

.cd-group { display: flex; flex-direction: column; gap: 0.3rem; }
.cd-label {
  font-size: 0.72rem;
  text-transform: uppercase;
  letter-spacing: 0.06em;
  color: var(--color-text-muted);
  font-weight: 600;
}
.cd-strong { font-weight: 600; color: var(--color-text); }
.cd-line { display: flex; align-items: center; gap: 0.5rem; font-size: 0.88rem; color: var(--color-text); }
.cd-line i { color: var(--color-text-muted); font-size: 0.8rem; }
.cd-line a { color: inherit; text-decoration: none; }
.cd-line a:hover { text-decoration: underline; }
.cd-address { font-size: 0.88rem; white-space: pre-line; color: var(--color-text); }
.cd-divider { height: 1px; background: var(--color-border, #e2e8f0); margin: 0.1rem 0; }

.cd-main { min-width: 0; }

.kpi-row {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 1rem;
  padding: 0.75rem 0 1rem;
  border-bottom: 1px solid var(--color-border, #e2e8f0);
  margin-bottom: 1rem;
}
@media (max-width: 720px) { .kpi-row { grid-template-columns: 1fr; } }
.kpi { display: flex; flex-direction: column; gap: 0.25rem; }
.kpi-label { color: var(--color-text-muted); font-size: 0.82rem; }
.kpi-value { font-size: 1.25rem; font-weight: 600; color: var(--color-text); font-variant-numeric: tabular-nums; }
.kpi-link { color: #2563eb; font-size: 1rem; }

.card-section { margin-top: 0.5rem; }
.section-head {
  display: flex; justify-content: space-between; align-items: center;
  margin-bottom: 0.5rem;
}
.section-head h3 { margin: 0; font-size: 1rem; color: var(--color-text); }

.muted { color: var(--color-text-muted); }
.small { font-size: 0.82rem; }
.chips { display: flex; flex-wrap: wrap; gap: 0.25rem; }
.empty-mini { padding: 0.75rem; text-align: center; color: var(--color-text-muted); font-size: 0.88rem; }
.num { font-variant-numeric: tabular-nums; }
.ccy { color: var(--color-text-muted); font-size: 0.8rem; margin-left: 0.15rem; }
.text-danger { color: #dc2626; }
code { background: var(--color-surface-alt); padding: 0.1rem 0.4rem; border-radius: 4px; font-size: 0.85em; }

.edit-wrap {
  display: flex;
  flex-direction: column;
  gap: 0.85rem;
  padding-top: 0.25rem;
}
.sticky-header {
  position: sticky;
  top: 0;
  z-index: 5;
  background: #fff;
  border: 1px solid var(--color-border, #e2e8f0);
  border-radius: 10px;
  padding: 0.75rem 1rem;
  display: flex;
  justify-content: space-between;
  align-items: center;
  box-shadow: 0 1px 2px rgba(15, 23, 42, 0.04);
}
.sticky-actions { display: flex; gap: 0.5rem; }
</style>
