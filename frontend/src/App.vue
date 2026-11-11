<script setup lang="ts">
import { RouterLink, RouterView, useRouter, useRoute } from "vue-router";
import { computed, ref } from "vue";
import Button from "primevue/button";
import Avatar from "primevue/avatar";
import Menu from "primevue/menu";
import type { MenuItem } from "primevue/menuitem";
import ConfirmDialog from "primevue/confirmdialog";
import { useAuthStore } from "@/stores/auth";
import { useTheme } from "@/composables/useTheme";
import AppIcon from "@/components/AppIcon.vue";
import type { IconName } from "@/theme/icons";

const { theme, toggle: toggleTheme } = useTheme();

const auth = useAuthStore();
const router = useRouter();
const route = useRoute();

const userMenu = ref<InstanceType<typeof Menu> | null>(null);
const userMenuItems = computed<MenuItem[]>(() => [
  {
    label: auth.displayLabel,
    items: [
      {
        label: "My account",
        icon: "id-card",
        command: () => router.push("/account"),
      },
      {
        label: "Change password",
        icon: "key-round",
        command: () => router.push("/account?tab=password"),
      },
      { separator: true },
      {
        label: "Sign out",
        icon: "log-out",
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

const navGroups: { label: string; items: { to: string; icon: IconName; label: string }[] }[] = [
  {
    label: "Overview",
    items: [{ to: "/dashboard", icon: "house", label: "Dashboard" }],
  },
  {
    label: "Sales",
    items: [
      { to: "/customers", icon: "users", label: "Customers" },
      { to: "/projects", icon: "folder", label: "Projects" },
      { to: "/quotations", icon: "file-pen", label: "Quotations" },
      { to: "/invoices", icon: "file-text", label: "Invoices" },
    ],
  },
  {
    label: "Purchases",
    items: [
      { to: "/vendors", icon: "briefcase", label: "Vendors" },
      { to: "/bills", icon: "receipt", label: "Bills" },
    ],
  },
  {
    label: "Accounting",
    items: [
      { to: "/items", icon: "tag", label: "Products & Services" },
      { to: "/chart-of-accounts", icon: "book-open", label: "Chart of Accounts" },
      { to: "/reports", icon: "chart-column", label: "Reports" },
      { to: "/fx-rates", icon: "dollar-sign", label: "FX Rates" },
    ],
  },
  {
    label: "Operations",
    items: [
      { to: "/queue", icon: "inbox", label: "Awaiting Finalization" },
      { to: "/review", icon: "triangle-alert", label: "Manual Review" },
    ],
  },
  {
    label: "Admin",
    items: [
      { to: "/mcp-integration", icon: "sparkles", label: "AI Assistant" },
      { to: "/users", icon: "user", label: "Users" },
      { to: "/audit-log", icon: "history", label: "Audit Log" },
    ],
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
    "mcp-integration": "AI Assistant",
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

// Public pages (sign in, forgot/reset password) always stand alone, even if a
// session happens to be live — the shell must never frame a sign-in form.
const showShell = computed(() => !!auth.user && !route.meta.public);

const mobileNavOpen = ref(false);
function toggleMobileNav() { mobileNavOpen.value = !mobileNavOpen.value; }
function closeMobileNav() { mobileNavOpen.value = false; }
</script>

<template>
  <a class="skip-link" href="#main-content">Skip to content</a>
  <div v-if="showShell" class="shell" :class="{ 'nav-open': mobileNavOpen }">
    <div class="nav-scrim" @click="closeMobileNav" />
    <aside class="sidebar">
      <div class="brand">
        <div class="brand-mark">
          <img src="/favicon.svg" alt="Wenhao Billing logo" />
        </div>
        <div class="brand-text">
          <div class="brand-name">Wenhao Billing</div>
          <div class="brand-sub">software house</div>
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
            @click="closeMobileNav"
          >
            <AppIcon :name="item.icon" :size="17" />
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
          <button
            type="button"
            class="nav-toggle"
            aria-label="Open navigation"
            @click="toggleMobileNav"
          >
            <AppIcon name="menu" />
          </button>
          <h2 class="topbar-title">{{ pageTitle }}</h2>
        </div>
        <div class="topbar-right">
          <button
            type="button"
            class="theme-toggle"
            :aria-label="theme === 'dark' ? 'Switch to light theme' : 'Switch to dark theme'"
            :title="theme === 'dark' ? 'Switch to light theme' : 'Switch to dark theme'"
            @click="toggleTheme"
          >
            <AppIcon :name="theme === 'dark' ? 'sun' : 'moon'" />
          </button>
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
            <AppIcon name="chevron-down" class="chevron" />
          </button>
          <Menu ref="userMenu" :model="userMenuItems" :popup="true">
            <template #itemicon="{ item, class: iconClass }">
              <AppIcon :name="item.icon as IconName" :class="iconClass" />
            </template>
          </Menu>
        </div>
      </header>
      <main id="main-content" class="page" tabindex="-1">
        <RouterView />
      </main>
    </div>
  </div>

  <div v-else class="anon-shell">
    <RouterView />
  </div>

  <ConfirmDialog>
    <template #message="{ message }">
      <AppIcon v-if="message.icon" :name="message.icon as IconName" :size="24" class="p-confirmdialog-icon" />
      <span class="p-confirmdialog-message">{{ message.message }}</span>
    </template>
  </ConfirmDialog>
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
  overflow: hidden; display: grid; place-items: center; flex-shrink: 0;
}
.brand-mark img { width: 100%; height: 100%; object-fit: cover; display: block; }
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
.nav-item:focus-visible { outline: none; box-shadow: 0 0 0 2px #60a5fa; background: #1e293b; color: #f1f5f9; }
.nav-item .app-icon { color: #94a3b8; }
.nav-item.router-link-active {
  background: #1d4ed8;
  color: white;
}
.nav-item.router-link-active .app-icon { color: white; }

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
.topbar-left { display: flex; align-items: center; gap: 0.75rem; min-width: 0; }
.topbar-title { margin: 0; font-size: 1rem; font-weight: 600; color: var(--color-text); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.topbar-right { display: flex; align-items: center; gap: 0.75rem; }

.nav-toggle {
  display: none;
  align-items: center;
  justify-content: center;
  width: 36px; height: 36px;
  border-radius: 8px;
  border: 1px solid var(--color-border);
  background: var(--color-surface-alt);
  color: var(--color-text);
  cursor: pointer;
  font-size: 1rem;
  transition: background 0.15s;
}
.nav-toggle:hover { background: var(--color-border); }
.nav-toggle:focus-visible { outline: none; box-shadow: var(--focus-ring); }

.theme-toggle {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 36px; height: 36px;
  border-radius: 999px;
  border: 1px solid var(--color-border);
  background: var(--color-surface-alt);
  color: var(--color-text);
  cursor: pointer;
  font-size: 0.95rem;
  transition: background 0.15s, border-color 0.15s;
}
.theme-toggle:hover { background: var(--color-border); }
.theme-toggle:focus-visible { outline: none; box-shadow: var(--focus-ring); }

.skip-link {
  position: fixed;
  top: 0.5rem;
  left: 0.5rem;
  transform: translateY(-150%);
  background: var(--color-primary);
  color: white;
  padding: 0.55rem 0.9rem;
  border-radius: 6px;
  font-size: 0.85rem;
  font-weight: 600;
  text-decoration: none;
  z-index: 100;
  transition: transform 0.15s;
}
.skip-link:focus {
  transform: translateY(0);
  outline: 2px solid white;
  outline-offset: 2px;
}
.page:focus { outline: none; }

.nav-scrim {
  display: none;
  position: fixed;
  inset: 0;
  background: rgba(15, 23, 42, 0.45);
  backdrop-filter: blur(2px);
  z-index: 20;
}

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
.user-chip .chevron { width: 12px; height: 12px; color: var(--color-text-muted); }
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

.anon-shell { min-height: 100vh; display: flex; }
.anon-shell > :deep(*) { flex: 1; }

@media (max-width: 1100px) and (min-width: 721px) {
  .shell { grid-template-columns: 72px 1fr; }
  .sidebar { padding: 1rem 0.5rem; }
  .brand-text, .nav-group-label, .sidebar-footer, .nav-item span { display: none; }
  .nav-item { justify-content: center; padding: 0.6rem 0.4rem; }
  .user-meta { display: none; }
}

@media (max-width: 720px) {
  .shell { grid-template-columns: 1fr; }
  .sidebar {
    position: fixed;
    top: 0; left: 0; bottom: 0;
    width: 260px;
    z-index: 30;
    transform: translateX(-100%);
    transition: transform 0.2s ease;
    box-shadow: 0 20px 40px rgba(0, 0, 0, 0.3);
  }
  .nav-open .sidebar { transform: translateX(0); }
  .nav-open .nav-scrim { display: block; }
  .nav-toggle { display: inline-flex; }
  .topbar { padding: 0 1rem; }
  .page { padding: 1.25rem 1rem; }
  .user-meta { display: none; }
  .user-chip { padding: 0.25rem; }
}
</style>
