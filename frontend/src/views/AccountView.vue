<script setup lang="ts">
import { ref, watch, onMounted } from "vue";
import { useRoute, useRouter } from "vue-router";
import InputText from "primevue/inputtext";
import Textarea from "primevue/textarea";
import Password from "primevue/password";
import Button from "primevue/button";
import Message from "primevue/message";
import TabView from "primevue/tabview";
import TabPanel from "primevue/tabpanel";
import { api } from "@/api/client";
import { useAuthStore } from "@/stores/auth";

const auth = useAuthStore();
const route = useRoute();
const router = useRouter();

// TabView uses 0-indexed activeIndex; map ?tab=<name> to/from index.
const tabOrder = ["profile", "security"] as const;
type TabName = (typeof tabOrder)[number];
const activeIndex = ref(0);

function syncTabFromQuery() {
  const q = route.query.tab;
  const idx = tabOrder.indexOf(q as TabName);
  activeIndex.value = idx === -1 ? 0 : idx;
}
onMounted(syncTabFromQuery);
watch(() => route.query.tab, syncTabFromQuery);

function onTabChange(e: { index: number }) {
  activeIndex.value = e.index;
  const tab = e.index === 0 ? undefined : tabOrder[e.index];
  router.replace({ query: tab ? { tab } : {} });
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
});
const companyLoading = ref(false);
const companySaving = ref(false);
const companyError = ref<string | null>(null);
const companySaved = ref(false);

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
    };
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
    const payload = Object.fromEntries(
      Object.entries(company.value).map(([k, v]) => [k, v && String(v).trim() ? v : null]),
    );
    await api.put("/business-profile", payload);
    companySaved.value = true;
  } catch (e: any) {
    companyError.value = e?.response?.data?.detail ?? "Failed to save company profile";
  } finally {
    companySaving.value = false;
  }
}

onMounted(loadCompany);

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
    <header class="page-header">
      <div>
        <h1>My Account</h1>
        <p class="subtitle">
          Manage your profile, change your password, and review session details.
        </p>
      </div>
    </header>

    <TabView :active-index="activeIndex" @tab-change="onTabChange">
      <TabPanel header="Profile & Company">
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
                icon="pi pi-check"
                :loading="profileSaving"
              />
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
            <label>
              Logo URL
              <InputText v-model="company.logo_url" placeholder="https://..." />
            </label>
            <label>
              Default notes / terms
              <Textarea v-model="company.default_notes" rows="10" />
              <small>Appears on each invoice. You can choose to override it when you create an invoice.</small>
            </label>

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
                icon="pi pi-check"
                :loading="companySaving"
                :disabled="companyLoading"
              />
            </div>
          </form>
        </div>
        </div>
      </TabPanel>

      <TabPanel header="Password & Session">
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
                icon="pi pi-key"
                :loading="pwSaving"
              />
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
            <dd>Server-side session cookie · bcrypt</dd>
          </dl>
        </div>
        </div>
      </TabPanel>
    </TabView>
  </section>
</template>

<style scoped>
.panel {
  background: var(--color-surface);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-md);
  padding: 1.5rem;
  max-width: 560px;
}
.two-panel {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 560px));
  gap: 1.25rem;
  align-items: start;
}
@media (max-width: 960px) {
  .two-panel { grid-template-columns: 1fr; }
}
.panel-heading {
  margin: 0 0 1rem;
  font-size: 1rem;
  font-weight: 600;
  color: var(--color-text);
}

.identity {
  display: flex;
  align-items: center;
  gap: 1rem;
  padding-bottom: 1.25rem;
  margin-bottom: 1.25rem;
  border-bottom: 1px solid var(--color-border);
}
.avatar-big {
  width: 56px; height: 56px;
  border-radius: 50%;
  background: linear-gradient(135deg, #2563eb, #60a5fa);
  color: white;
  display: grid; place-items: center;
  font-weight: 700; font-size: 1.1rem;
  letter-spacing: 0.02em;
}
.identity-name { font-weight: 600; font-size: 1rem; }
.identity-sub { color: var(--color-text-muted); font-size: 0.82rem; margin-top: 0.1rem; }

.form { display: flex; flex-direction: column; gap: 1rem; }
.form label { display: flex; flex-direction: column; gap: 0.35rem; font-size: 0.85rem; color: #475569; font-weight: 500; }
.form small { color: var(--color-text-muted); font-size: 0.75rem; font-weight: 400; }
.form :deep(.p-password), .form :deep(.p-password-input), .form :deep(.pw-input) { width: 100%; }

.actions { display: flex; justify-content: flex-end; gap: 0.5rem; margin-top: 0.25rem; }
.two-col { display: grid; grid-template-columns: 1fr 1fr; gap: 1rem; }
.muted { color: var(--color-text-muted, #64748b); }
.small { font-size: 0.82rem; margin-bottom: 1rem; }

.session-meta {
  display: grid;
  grid-template-columns: 160px 1fr;
  gap: 0.5rem 1rem;
  margin: 0;
  font-size: 0.88rem;
}
.session-meta dt { color: var(--color-text-muted); font-weight: 500; }
.session-meta dd { margin: 0; color: var(--color-text); }
.session-meta code {
  background: var(--color-surface-alt);
  padding: 0.1rem 0.35rem;
  border-radius: 4px;
  font-size: 0.82em;
}
</style>
