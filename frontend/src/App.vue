<script setup lang="ts">
import { RouterLink, RouterView, useRouter, useRoute } from "vue-router";
import { computed, onMounted, ref } from "vue";
import Button from "primevue/button";
import Avatar from "primevue/avatar";
import Menu from "primevue/menu";
import type { MenuItem } from "primevue/menuitem";
import ConfirmDialog from "primevue/confirmdialog";
import { useAuthStore } from "@/stores/auth";

const auth = useAuthStore();
const router = useRouter();
const route = useRoute();

onMounted(() => auth.fetchMe());

const userMenu = ref<InstanceType<typeof Menu> | null>(null);
const userMenuItems = computed<MenuItem[]>(() => [
  {
    label: auth.displayLabel,
    items: [
      {
        label: "My account",
        icon: "pi pi-id-card",
        command: () => router.push("/account"),
      },
      {
        label: "Change password",
        icon: "pi pi-key",
        command: () => router.push("/account?tab=password"),
      },
      { separator: true },
      {
        label: "Sign out",
        icon: "pi pi-sign-out",
        command: () => logout(),
      },
    ],
  },
]);

function toggleUserMenu(event: Event) {
  userMenu.value?.toggle(event);
}

async function logout() {
  await auth.logout();
  router.push("/login");
}

const navGroups = [
  {
    label: "Overview",
    items: [{ to: "/dashboard", icon: "pi pi-home", label: "Dashboard" }],
  },
  {
    label: "Sales",
    items: [
      { to: "/customers", icon: "pi pi-users", label: "Customers" },
      { to: "/projects", icon: "pi pi-folder", label: "Projects" },
      { to: "/quotations", icon: "pi pi-file-edit", label: "Quotations" },
      { to: "/invoices", icon: "pi pi-file", label: "Invoices" },
    ],
  },
  {
    label: "Purchases",
    items: [
      { to: "/vendors", icon: "pi pi-briefcase", label: "Vendors" },
      { to: "/bills", icon: "pi pi-receipt", label: "Bills" },
    ],
  },
  {
    label: "Operations",
    items: [
      { to: "/queue", icon: "pi pi-inbox", label: "Awaiting Finalization" },
      { to: "/review", icon: "pi pi-exclamation-triangle", label: "Manual Review" },
      { to: "/audit-log", icon: "pi pi-history", label: "Audit Log" },
    ],
  },
  {
    label: "Accounting",
    items: [
      { to: "/items", icon: "pi pi-tags", label: "Products & Services" },
      { to: "/chart-of-accounts", icon: "pi pi-book", label: "Chart of Accounts" },
      { to: "/reports", icon: "pi pi-chart-bar", label: "Reports" },
      { to: "/fx-rates", icon: "pi pi-dollar", label: "FX Rates" },
    ],
  },
  {
    label: "Admin",
    items: [{ to: "/users", icon: "pi pi-user", label: "Users" }],
  },
];

const pageTitle = computed(() => {
  const map: Record<string, string> = {
    dashboard: "Dashboard",
    customers: "Customers",
    "customer-detail": "Customer",
    vendors: "Vendors",
    items: "Products & Services",
    "chart-of-accounts": "Chart of Accounts",
    projects: "Projects",
    "project-new": "New project",
    "project-edit": "Edit project",
    "project-detail": "Project",
    invoices: "Invoices",
    quotations: "Quotations",
    "quotation-new": "New quotation",
    "quotation-edit": "Edit quotation",
    "quotation-detail": "Quotation",
    bills: "Bills",
    "bill-new": "New bill",
    "bill-edit": "Edit bill",
    queue: "Awaiting Finalization",
    review: "Manual Review",
    "review-one": "Manual Review",
    "audit-log": "Audit Log",
    users: "Users",
    reports: "Reports",
    "fx-rates": "FX Rates",
    account: "My Account",
  };
  return map[(route.name as string) ?? ""] ?? "";
});

const userInitials = computed(() => {
  const label = auth.displayLabel;
  if (label.includes("@")) return label.slice(0, 2).toUpperCase();
  // display_name: use initials of the first two words
  const parts = label.trim().split(/\s+/).filter(Boolean);
  if (parts.length >= 2) return (parts[0][0] + parts[1][0]).toUpperCase();
  return label.slice(0, 2).toUpperCase();
});

const isAuthed = computed(() => !!auth.user);
</script>

<template>
  <div v-if="isAuthed" class="shell">
    <aside class="sidebar">
      <div class="brand">
        <div class="brand-mark">WB</div>
        <div class="brand-text">
          <div class="brand-name">wenhao-billing</div>
          <div class="brand-sub">accounting ops</div>
        </div>
      </div>

      <nav class="nav">
        <div v-for="group in navGroups" :key="group.label" class="nav-group">
          <div class="nav-group-label">{{ group.label }}</div>
          <RouterLink
            v-for="item in group.items"
            :key="item.to"
            :to="item.to"
            class="nav-item"
          >
            <i :class="item.icon" />
            <span>{{ item.label }}</span>
          </RouterLink>
        </div>
      </nav>

      <div class="sidebar-footer">
        <div class="env-pill">
          <span class="dot" />
          Single-tenant · Dev
        </div>
      </div>
    </aside>

    <div class="main-col">
      <header class="topbar">
        <div class="topbar-left">
          <h2 class="topbar-title">{{ pageTitle }}</h2>
        </div>
        <div class="topbar-right">
          <button
            type="button"
            class="user-chip"
            aria-haspopup="true"
            aria-label="Open account menu"
            @click="toggleUserMenu"
          >
            <Avatar :label="userInitials" shape="circle" class="avatar" />
            <div class="user-meta">
              <div class="user-email">{{ auth.displayLabel }}</div>
              <div class="user-role">{{ auth.user?.role ?? 'admin' }}</div>
            </div>
            <i class="pi pi-chevron-down chevron" />
          </button>
          <Menu ref="userMenu" :model="userMenuItems" :popup="true" />
        </div>
      </header>
      <main class="page">
        <RouterView />
      </main>
    </div>
  </div>

  <div v-else class="anon-shell">
    <RouterView />
  </div>

  <ConfirmDialog />
</template>

<style scoped>
.shell {
  display: grid;
  grid-template-columns: var(--sidebar-width) 1fr;
  min-height: 100vh;
}

.sidebar {
  background: #0f172a;
  color: #cbd5e1;
  display: flex;
  flex-direction: column;
  padding: 1.25rem 0.75rem;
  position: sticky;
  top: 0;
  height: 100vh;
}

.brand {
  display: flex;
  align-items: center;
  gap: 0.75rem;
  padding: 0 0.5rem 1.25rem;
  border-bottom: 1px solid #1e293b;
  margin-bottom: 1rem;
}
.brand-mark {
  width: 36px; height: 36px; border-radius: 8px;
  background: linear-gradient(135deg, #2563eb, #60a5fa);
  color: white; display: grid; place-items: center;
  font-weight: 700; font-size: 0.9rem; letter-spacing: 0.02em;
}
.brand-name { color: #f1f5f9; font-weight: 600; font-size: 0.95rem; }
.brand-sub { color: #64748b; font-size: 0.72rem; text-transform: uppercase; letter-spacing: 0.06em; }

.nav { flex: 1; display: flex; flex-direction: column; gap: 1rem; overflow-y: auto; }
.nav-group { display: flex; flex-direction: column; gap: 0.15rem; }
.nav-group-label {
  font-size: 0.7rem;
  text-transform: uppercase;
  letter-spacing: 0.08em;
  color: #64748b;
  padding: 0 0.75rem 0.35rem;
}
.nav-item {
  display: flex;
  align-items: center;
  gap: 0.7rem;
  padding: 0.55rem 0.75rem;
  border-radius: 8px;
  color: #cbd5e1;
  text-decoration: none;
  font-size: 0.88rem;
  transition: background 0.15s, color 0.15s;
}
.nav-item:hover { background: #1e293b; color: #f1f5f9; }
.nav-item i { font-size: 0.95rem; width: 16px; text-align: center; color: #94a3b8; }
.nav-item.router-link-active {
  background: #1d4ed8;
  color: white;
}
.nav-item.router-link-active i { color: white; }

.sidebar-footer { padding: 0.75rem 0.5rem 0; border-top: 1px solid #1e293b; }
.env-pill {
  display: inline-flex; align-items: center; gap: 0.5rem;
  padding: 0.35rem 0.6rem;
  background: #1e293b;
  border-radius: 999px;
  font-size: 0.72rem;
  color: #94a3b8;
}
.env-pill .dot {
  width: 6px; height: 6px; border-radius: 50%;
  background: #10b981;
  box-shadow: 0 0 0 3px rgba(16, 185, 129, 0.2);
}

.main-col { display: flex; flex-direction: column; min-width: 0; }

.topbar {
  height: var(--topbar-height);
  background: var(--color-surface);
  border-bottom: 1px solid var(--color-border);
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 1.75rem;
  position: sticky;
  top: 0;
  z-index: 10;
}
.topbar-title { margin: 0; font-size: 1rem; font-weight: 600; color: var(--color-text); }
.topbar-right { display: flex; align-items: center; gap: 0.75rem; }

.user-chip {
  display: flex; align-items: center; gap: 0.6rem;
  padding: 0.25rem 0.75rem 0.25rem 0.25rem;
  border-radius: 999px;
  background: var(--color-surface-alt);
  border: 1px solid var(--color-border);
  cursor: pointer;
  font: inherit;
  color: inherit;
  transition: background 0.15s, border-color 0.15s;
}
.user-chip:hover { background: var(--color-border); border-color: var(--color-text-muted); }
.user-chip:focus-visible { outline: 2px solid var(--color-primary); outline-offset: 2px; }
.user-chip .chevron { font-size: 0.7rem; color: var(--color-text-muted); }
.avatar { background: var(--color-primary); color: white; font-weight: 600; font-size: 0.75rem; width: 28px; height: 28px; }
.user-meta { line-height: 1.1; }
.user-email { font-size: 0.8rem; font-weight: 500; color: var(--color-text); }
.user-role { font-size: 0.68rem; color: var(--color-text-muted); text-transform: uppercase; letter-spacing: 0.05em; }

.page {
  padding: 1.75rem 2rem;
  max-width: 1400px;
  width: 100%;
  margin: 0 auto;
}

.anon-shell { min-height: 100vh; display: grid; place-items: center; }

@media (max-width: 900px) {
  .shell { grid-template-columns: 64px 1fr; }
  .sidebar { padding: 1rem 0.4rem; }
  .brand-text, .nav-group-label, .sidebar-footer, .nav-item span { display: none; }
  .nav-item { justify-content: center; }
  .user-meta { display: none; }
}
</style>
