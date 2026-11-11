<script setup lang="ts">
import AppIcon from "@/components/AppIcon.vue";
import PageHeader from "@/components/PageHeader.vue";
import { ref, watch, onMounted } from "vue";
import { useRoute, useRouter } from "vue-router";
import InputText from "primevue/inputtext";
import Textarea from "primevue/textarea";
import Password from "primevue/password";
import Button from "primevue/button";
import Message from "primevue/message";
import Tabs from "primevue/tabs";
import TabList from "primevue/tablist";
import Tab from "primevue/tab";
import TabPanels from "primevue/tabpanels";
import TabPanel from "primevue/tabpanel";
import { api } from "@/api/client";
import { useAuthStore } from "@/stores/auth";

const auth = useAuthStore();
const route = useRoute();
const router = useRouter();

// Each tab's value is its ?tab=<name>; the first tab (profile) has no query.
const tabOrder = ["profile", "security"] as const;
type TabName = (typeof tabOrder)[number];
const activeTab = ref<TabName>(tabOrder[0]);

function syncTabFromQuery() {
  const q = route.query.tab as TabName;
  activeTab.value = tabOrder.includes(q) ? q : tabOrder[0];
}
onMounted(syncTabFromQuery);
watch(() => route.query.tab, syncTabFromQuery);

function onTabChange(value: string | number) {
  const tab = tabOrder.includes(value as TabName) ? (value as TabName) : tabOrder[0];
  activeTab.value = tab;
  router.replace({ query: tab === tabOrder[0] ? {} : { tab } });
}

// ----- profile form -----
const profile = ref({
  email: auth.user?.email ?? "",
  display_name: auth.user?.display_name ?? "",
});
const profileSaving = ref(false);
const profileError = ref<string | null>(null);
const profileSaved = ref(false);

watch(
  () => auth.user,
  (u) => {
    if (u) {
      profile.value.email = u.email;
      profile.value.display_name = u.display_name ?? "";
    }
  },
  { immediate: true },
);

async function saveProfile() {
  profileError.value = null;
  profileSaved.value = false;
  profileSaving.value = true;
  try {
    await auth.updateProfile({
      email: profile.value.email,
      display_name: profile.value.display_name.trim() || null,
    });
    profileSaved.value = true;
  } catch (e: any) {
    profileError.value = e?.response?.data?.detail ?? "Failed to update profile";
  } finally {
    profileSaving.value = false;
  }
}

// ----- company form -----
interface BusinessProfile {
  name: string | null;
  address: string | null;
  contact_email: string | null;
  contact_phone: string | null;
  invoice_title: string | null;
  invoice_summary: string | null;
  logo_url: string | null;
  default_notes: string | null;
  npwp: string | null;
  npwp_name: string | null;
}
const company = ref<BusinessProfile>({
  name: "",
  address: "",
  contact_email: "",
  contact_phone: "",
  invoice_title: "",
  invoice_summary: "",
  logo_url: "",
  default_notes: "",
  npwp: "",
  npwp_name: "",
});
const companyLoading = ref(false);
const companySaving = ref(false);
const companyError = ref<string | null>(null);
const companySaved = ref(false);

// Fixed set of supported invoice currencies — one payment-account row each.
const CURRENCIES = ["IDR", "SGD", "USD"] as const;

interface PaymentAccountRow {
  currency: string;
  instructions: string;
  is_default: boolean;
}
// One fixed row per currency (no add/remove). A currency left blank simply
// isn't saved and won't appear on invoices.
const paymentAccounts = ref<PaymentAccountRow[]>(
  CURRENCIES.map((c) => ({ currency: c, instructions: "", is_default: false })),
);

function setDefault(index: number) {
  paymentAccounts.value.forEach((a, i) => (a.is_default = i === index));
}

async function loadCompany() {
  companyLoading.value = true;
  try {
    const { data } = await api.get<BusinessProfile>("/business-profile");
    company.value = {
      name: data.name ?? "",
      address: data.address ?? "",
      contact_email: data.contact_email ?? "",
      contact_phone: data.contact_phone ?? "",
      invoice_title: data.invoice_title ?? "",
      invoice_summary: data.invoice_summary ?? "",
      logo_url: data.logo_url ?? "",
      default_notes: data.default_notes ?? "",
      npwp: data.npwp ?? "",
      npwp_name: data.npwp_name ?? "",
    };
    const accts = await api.get("/business-profile/payment-accounts");
    const byCurrency = new Map(
      (accts.data as PaymentAccountRow[]).map((a) => [a.currency, a]),
    );
    paymentAccounts.value = CURRENCIES.map((c) => {
      const saved = byCurrency.get(c);
      return {
        currency: c,
        instructions: saved?.instructions ?? "",
        is_default: saved?.is_default ?? false,
      };
    });
  } catch (e: any) {
    companyError.value = e?.response?.data?.detail ?? "Failed to load company profile";
  } finally {
    companyLoading.value = false;
  }
}

async function saveCompany() {
  companyError.value = null;
  companySaved.value = false;
  companySaving.value = true;
  try {
    // Persist only currencies that actually have instructions; blank rows are
    // omitted. Ensure one of the saved accounts is the default.
    const accounts = paymentAccounts.value
      .filter((a) => a.instructions.trim())
      .map((a) => ({
        currency: a.currency,
        instructions: a.instructions,
        is_default: a.is_default,
      }));
    if (accounts.length > 0 && !accounts.some((a) => a.is_default)) {
      accounts[0].is_default = true;
    }
    const payload = Object.fromEntries(
      Object.entries(company.value).map(([k, v]) => [k, v && String(v).trim() ? v : null]),
    );
    await api.put("/business-profile", payload);
    await api.put("/business-profile/payment-accounts", accounts);
    companySaved.value = true;
  } catch (e: any) {
    companyError.value =
      e?.response?.data?.detail ?? e?.message ?? "Failed to save company profile";
  } finally {
    companySaving.value = false;
  }
}

onMounted(loadCompany);

// ----- logo upload -----
// Stored as a base64 data URI in company.logo_url and persisted via the
// existing PUT /business-profile — no separate upload endpoint.
const LOGO_MAX_BYTES = 1024 * 1024; // 1 MB
const LOGO_TYPES = ["image/png", "image/jpeg", "image/svg+xml"];
const logoInput = ref<HTMLInputElement | null>(null);

function triggerLogoPicker() {
  companyError.value = null;
  logoInput.value?.click();
}

function onLogoSelect(e: Event) {
  const input = e.target as HTMLInputElement;
  const file = input.files?.[0];
  if (!file) return;
  if (!LOGO_TYPES.includes(file.type)) {
    companyError.value = "Logo must be a PNG, JPEG, or SVG image.";
    input.value = "";
    return;
  }
  if (file.size > LOGO_MAX_BYTES) {
    companyError.value = "Logo must be 1 MB or smaller.";
    input.value = "";
    return;
  }
  const reader = new FileReader();
  reader.onload = () => {
    company.value.logo_url = String(reader.result);
  };
  reader.onerror = () => {
    companyError.value = "Failed to read the selected image.";
  };
  reader.readAsDataURL(file);
  input.value = ""; // allow re-selecting the same file
}

function removeLogo() {
  company.value.logo_url = "";
  if (logoInput.value) logoInput.value.value = "";
}

// ----- password form -----
const pw = ref({ current: "", next: "", confirm: "" });
const pwSaving = ref(false);
const pwError = ref<string | null>(null);
const pwSaved = ref(false);

async function changePassword() {
  pwError.value = null;
  pwSaved.value = false;

  if (pw.value.next.length < 6) {
    pwError.value = "New password must be at least 6 characters.";
    return;
  }
  if (pw.value.next !== pw.value.confirm) {
    pwError.value = "New password and confirmation don't match.";
    return;
  }

  pwSaving.value = true;
  try {
    await auth.changePassword(pw.value.current, pw.value.next);
    pw.value = { current: "", next: "", confirm: "" };
    pwSaved.value = true;
  } catch (e: any) {
    pwError.value = e?.response?.data?.detail ?? "Failed to change password";
  } finally {
    pwSaving.value = false;
  }
}

function fmtDate(iso?: string) {
  if (!iso) return "—";
  return new Date(iso).toLocaleString();
}
</script>

<template>
  <section>
    <PageHeader eyebrow="Your account" title="My Account">
      <template #lede>
        Manage your profile, change your password, and review session details.
      </template>
    </PageHeader>

    <Tabs :value="activeTab" class="account-tabs" @update:value="onTabChange">
      <TabList>
        <Tab value="profile">Profile & Company</Tab>
        <Tab value="security">Password & Session</Tab>
      </TabList>
      <TabPanels>
      <TabPanel value="profile">
        <div class="two-panel">
        <div class="panel">
          <div class="identity">
            <div class="avatar-big">
              {{ (auth.displayLabel || '??').slice(0, 2).toUpperCase() }}
            </div>
            <div>
              <div class="identity-name">{{ auth.displayLabel }}</div>
              <div class="identity-sub">
                {{ auth.user?.role }} · joined {{ fmtDate(auth.user?.created_at) }}
              </div>
            </div>
          </div>

          <form class="form" @submit.prevent="saveProfile">
            <label>
              Display name
              <InputText
                v-model="profile.display_name"
                placeholder="e.g. Wenhao Kho"
                maxlength="255"
              />
              <small>Shown in the topbar and audit logs.</small>
            </label>
            <label>
              Email
              <InputText v-model="profile.email" type="email" required />
              <small>Used for login and password recovery.</small>
            </label>

            <Message v-if="profileError" severity="error" :closable="false">
              {{ profileError }}
            </Message>
            <Message v-if="profileSaved" severity="success" :closable="true">
              Profile updated.
            </Message>

            <div class="actions">
              <Button
                type="submit"
                label="Save changes"
                :loading="profileSaving">
                <template #icon><AppIcon name="check" /></template>
              </Button>
            </div>
          </form>
        </div>

        <div class="panel">
          <h2 class="panel-heading">Company</h2>
          <p class="muted small">
            Business address, contact details, title, summary, and logo — shown on every invoice.
          </p>
          <form class="form" @submit.prevent="saveCompany">
            <label>
              Company name
              <InputText v-model="company.name" maxlength="255" />
            </label>
            <label>
              Business address
              <Textarea v-model="company.address" rows="3" />
            </label>
            <div class="two-col">
              <label>
                Contact email
                <InputText v-model="company.contact_email" type="email" maxlength="255" />
              </label>
              <label>
                Contact phone
                <InputText v-model="company.contact_phone" maxlength="50" />
              </label>
            </div>
            <div class="two-col">
              <label>
                NPWP
                <InputText v-model="company.npwp" maxlength="32" />
                <small>Printed only on invoices with PPh withholding (NIK for individuals).</small>
              </label>
              <label>
                Taxpayer name
                <InputText v-model="company.npwp_name" maxlength="255" />
                <small>Name registered on the NPWP, if different from the company name.</small>
              </label>
            </div>
            <label>
              Invoice title
              <InputText v-model="company.invoice_title" maxlength="255" />
              <small>Heading printed at the top of generated invoices.</small>
            </label>
            <label>
              Invoice summary
              <Textarea v-model="company.invoice_summary" rows="3" />
              <small>Short blurb shown under the title (e.g. tagline, engagement scope).</small>
            </label>
            <div class="logo-field">
              <span class="logo-label">Logo</span>
              <div class="logo-row">
                <div v-if="company.logo_url" class="logo-preview">
                  <img :src="company.logo_url" alt="Company logo preview" />
                </div>
                <div v-else class="logo-preview logo-empty">
                  <AppIcon name="image" />
                </div>
                <div class="logo-buttons">
                  <Button
                    type="button"
                    :label="company.logo_url ? 'Replace image' : 'Choose image'"
                    severity="secondary"
                    outlined
                    size="small"
                    @click="triggerLogoPicker">
                    <template #icon><AppIcon name="upload" /></template>
                  </Button>
                  <Button
                    v-if="company.logo_url"
                    type="button"
                    label="Remove"
                    severity="danger"
                    text
                    size="small"
                    @click="removeLogo">
                    <template #icon><AppIcon name="x" /></template>
                  </Button>
                </div>
              </div>
              <input
                ref="logoInput"
                type="file"
                accept="image/png,image/jpeg,image/svg+xml"
                class="logo-input-hidden"
                @change="onLogoSelect"
              />
              <small>PNG, JPEG, or SVG · up to 1 MB.</small>
            </div>
            <label>
              Default notes / terms
              <Textarea v-model="company.default_notes" rows="10" />
              <small>Appears on each invoice. You can choose to override it when you create an invoice.</small>
            </label>
            <div class="pay-accounts">
              <div class="pay-accounts-head">
                <span>Payment accounts</span>
              </div>
              <small>Bank / PayNow details per currency. The default is used on invoices whose currency has no specific account. Leave a currency blank to omit it.</small>

              <div v-for="(acct, i) in paymentAccounts" :key="acct.currency" class="pay-account-row">
                <div class="pay-account-controls">
                  <span class="pay-currency-label">{{ acct.currency }}</span>
                  <label class="pay-default">
                    <input
                      type="radio"
                      :checked="acct.is_default"
                      @change="setDefault(i)"
                    />
                    Default
                  </label>
                </div>
                <Textarea v-model="acct.instructions" rows="4" placeholder="Bank / account details" />
              </div>
            </div>

            <Message v-if="companyError" severity="error" :closable="false">
              {{ companyError }}
            </Message>
            <Message v-if="companySaved" severity="success" :closable="true">
              Company profile saved.
            </Message>

            <div class="actions">
              <Button
                type="submit"
                label="Save"
                :loading="companySaving"
                :disabled="companyLoading">
                <template #icon><AppIcon name="check" /></template>
              </Button>
            </div>
          </form>
        </div>
        </div>
      </TabPanel>

      <TabPanel value="security">
        <div class="two-panel">
        <div class="panel">
          <h2 class="panel-heading">Change password</h2>
          <form class="form" @submit.prevent="changePassword">
            <label>
              Current password
              <Password
                v-model="pw.current"
                :feedback="false"
                toggle-mask
                required
                input-class="pw-input"
              />
            </label>
            <label>
              New password
              <Password
                v-model="pw.next"
                toggle-mask
                required
                :min-length="6"
                input-class="pw-input"
              />
              <small>Minimum 6 characters.</small>
            </label>
            <label>
              Confirm new password
              <Password
                v-model="pw.confirm"
                :feedback="false"
                toggle-mask
                required
                input-class="pw-input"
              />
            </label>

            <Message v-if="pwError" severity="error" :closable="false">
              {{ pwError }}
            </Message>
            <Message v-if="pwSaved" severity="success" :closable="true">
              Password changed.
            </Message>

            <div class="actions">
              <Button
                type="submit"
                label="Change password"
                :loading="pwSaving">
                <template #icon><AppIcon name="key-round" /></template>
              </Button>
            </div>
          </form>
        </div>

        <div class="panel">
          <h2 class="panel-heading">Session</h2>
          <dl class="session-meta">
            <dt>User ID</dt>
            <dd><code>{{ auth.user?.user_id ?? '—' }}</code></dd>
            <dt>Email</dt>
            <dd>{{ auth.user?.email ?? '—' }}</dd>
            <dt>Role</dt>
            <dd>{{ auth.user?.role ?? '—' }}</dd>
            <dt>Joined</dt>
            <dd>{{ fmtDate(auth.user?.created_at) }}</dd>
            <dt>Auth mechanism</dt>
            <dd>Signed session cookie · argon2id</dd>
          </dl>
        </div>
        </div>
      </TabPanel>
      </TabPanels>
    </Tabs>
  </section>
</template>

<style scoped>
/* Tabs: the brand strip (red active bar), panels on the page background. */
.account-tabs :deep(.p-tablist-tab-list) { background: transparent; border-color: var(--wb-line); }
.account-tabs :deep(.p-tab) { font: 500 14px var(--wb-font-sans); padding: 16px 14px; }
.account-tabs :deep(.p-tab:not(.p-tab-active)) { color: var(--wb-muted); }
.account-tabs :deep(.p-tablist-active-bar) { background: var(--wb-red); height: 2px; }
.account-tabs :deep(.p-tabpanels) { background: transparent; padding: 24px 0 0; }

.panel {
  background: var(--wb-surface);
  border: 1px solid var(--wb-line);
  padding: 28px;
  max-width: 560px;
}
.two-panel {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 560px));
  gap: 24px;
  align-items: start;
}
@media (max-width: 960px) {
  .two-panel { grid-template-columns: 1fr; }
}
@media (max-width: 720px) {
  .panel { padding: 20px; }
  .two-col { grid-template-columns: 1fr; }
}
.panel-heading {
  margin: 0 0 16px;
  font: 500 20px/1.2 var(--wb-font-display);
  letter-spacing: -0.5px;
  color: var(--wb-ink);
}

.identity {
  display: flex;
  align-items: center;
  gap: 16px;
  padding-bottom: 20px;
  margin-bottom: 20px;
  border-bottom: 1px solid var(--wb-line);
}
.avatar-big {
  width: 56px; height: 56px;
  border-radius: 50%;
  background: var(--wb-red);
  color: #fff;
  display: grid; place-items: center;
  font: 500 18px var(--wb-font-display);
  letter-spacing: 0.5px;
}
.identity-name { font: 500 18px/1.2 var(--wb-font-display); letter-spacing: -0.3px; color: var(--wb-ink); }
.identity-sub { color: var(--wb-muted); font-size: 13px; margin-top: 4px; }

.form { display: flex; flex-direction: column; gap: 18px; }
.form label { display: flex; flex-direction: column; gap: 6px; font-size: 13px; color: var(--wb-muted); font-weight: 500; }
.form small { color: var(--wb-muted-soft); font-size: 12px; font-weight: 400; }
.form :deep(.p-password), .form :deep(.p-password-input), .form :deep(.pw-input) { width: 100%; }

.actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: 4px; }

.logo-field { display: flex; flex-direction: column; gap: 6px; font-size: 13px; color: var(--wb-muted); font-weight: 500; }
.logo-label { color: var(--wb-muted); }
.logo-row { display: flex; align-items: center; gap: 16px; }
.logo-preview {
  width: 96px; height: 64px;
  border: 1px solid var(--wb-line);
  display: grid; place-items: center;
  overflow: hidden;
  background: var(--wb-surface-2);
  flex-shrink: 0;
}
.logo-preview img { max-width: 100%; max-height: 100%; object-fit: contain; }
.logo-empty { color: var(--wb-muted-soft); }
.logo-buttons { display: flex; flex-direction: column; align-items: flex-start; gap: 6px; }
.logo-input-hidden { display: none; }
.two-col { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; }
.muted { color: var(--wb-muted); }
.small { font-size: 13px; margin: 0 0 16px; line-height: 1.5; }

.session-meta {
  display: grid;
  grid-template-columns: 160px 1fr;
  gap: 12px 16px;
  margin: 0;
  font-size: 14px;
}
.session-meta dt { color: var(--wb-muted); font-weight: 400; }
.session-meta dd { margin: 0; color: var(--wb-ink); min-width: 0; overflow-wrap: anywhere; }
.session-meta code {
  background: var(--wb-tint);
  padding: 2px 6px;
  font-size: 12px;
}
@media (max-width: 720px) { .session-meta { grid-template-columns: 1fr; gap: 4px; } .session-meta dd { margin-bottom: 8px; } }

.pay-accounts { display: flex; flex-direction: column; gap: 6px; }
.pay-accounts-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-size: 13px;
  color: var(--wb-muted);
  font-weight: 500;
}
.pay-account-row {
  border: 1px solid var(--wb-line);
  padding: 14px;
  margin-top: 8px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.pay-account-controls {
  display: flex;
  align-items: center;
  gap: 16px;
}
.pay-currency-label {
  font: 500 13px var(--wb-font-mono);
  min-width: 2.5rem;
  color: var(--wb-ink);
}
.pay-default {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  font-size: 13px;
  color: var(--wb-muted);
}
.pay-default input { accent-color: var(--wb-red); }
</style>
