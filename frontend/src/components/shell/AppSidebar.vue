<script setup lang="ts">
import { RouterLink } from "vue-router";
import AppIcon from "@/components/AppIcon.vue";
import type { IconName } from "@/theme/icons";

defineProps<{ open: boolean }>();
const emit = defineEmits<{ navigate: [] }>();

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

const eyebrow = (i: number, label: string) => `${String(i + 1).padStart(2, "0")} / ${label}`;
</script>

<template>
  <aside id="app-sidebar" class="sidebar" :class="{ open }" aria-label="Sidebar">
    <div class="brand">
      <div class="brand-mark" aria-hidden="true">W</div>
      <div class="brand-text">
        <div class="brand-name">Wenhao Billing</div>
        <div class="brand-sub">Software house</div>
      </div>
    </div>

    <nav class="nav" aria-label="Main navigation">
      <div v-for="(group, i) in navGroups" :key="group.label" class="nav-group">
        <div class="nav-group-label">{{ eyebrow(i, group.label) }}</div>
        <RouterLink
          v-for="item in group.items"
          :key="item.to"
          :to="item.to"
          :title="item.label"
          class="nav-item"
          @click="emit('navigate')"
        >
          <AppIcon :name="item.icon" :size="17" />
          <span class="nav-label">{{ item.label }}</span>
        </RouterLink>
      </div>
    </nav>
  </aside>
</template>

<style scoped>
.sidebar {
  width: var(--sidebar-width);
  background: var(--wb-sidebar-bg);
  color: var(--wb-sidebar-fg);
  display: flex;
  flex-direction: column;
  position: sticky;
  top: 0;
  height: 100vh;
  height: 100dvh; /* phones: the visible viewport, not the one behind the URL bar */
  border-right: 1px solid var(--wb-sidebar-line);
}

.brand {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 22px 20px 18px;
  border-bottom: 1px solid var(--wb-sidebar-line);
}
.brand-mark {
  width: 36px;
  height: 36px;
  flex: none;
  background: var(--wb-red);
  color: #fff;
  display: grid;
  place-items: center;
  font: 800 18px var(--wb-font-display);
  letter-spacing: -1px;
}
.brand-text { min-width: 0; }
.brand-name {
  font: 600 15px/1.2 var(--wb-font-display);
  letter-spacing: -0.3px;
  white-space: nowrap;
}
.brand-sub {
  font: 500 10px/1.4 var(--wb-font-sans);
  letter-spacing: 1.5px;
  text-transform: uppercase;
  color: var(--wb-sidebar-eyebrow);
}

.nav { flex: 1; overflow: auto; padding: 10px 12px 20px; }
.nav-group { margin-top: 14px; padding-top: 10px; }
.nav-group-label {
  font: 500 11px/1 var(--wb-font-sans);
  letter-spacing: 1.5px;
  text-transform: uppercase;
  color: var(--wb-sidebar-eyebrow);
  padding: 10px 14px 8px;
}
.nav-item {
  display: flex;
  align-items: center;
  gap: 12px;
  height: 40px;
  padding: 0 14px;
  border-radius: 2px;
  color: var(--wb-sidebar-fg);
  text-decoration: none;
  font: 400 14px var(--wb-font-sans);
  white-space: nowrap;
  transition: background 0.15s;
}
.nav-item:hover { background: var(--wb-sidebar-hover); color: var(--wb-sidebar-fg); text-decoration: none; }
.nav-item:focus-visible { outline: 2px solid var(--wb-sidebar-eyebrow); outline-offset: -2px; }
.nav-item .app-icon { flex: none; }
.nav-item.router-link-active,
.nav-item.router-link-active:hover {
  background: var(--wb-sidebar-active);
  color: var(--wb-sidebar-active-fg);
}

/* Tablet: the mockup's 76px icon rail. */
@media (max-width: 1100px) and (min-width: 721px) {
  .sidebar { width: var(--sidebar-rail-width); }
  .brand { justify-content: center; padding: 22px 0 18px; }
  .brand-text, .nav-group-label, .nav-label { display: none; }
  .nav-group { border-top: 1px solid var(--wb-sidebar-line); }
  .nav-group:first-child { border-top: 0; }
  .nav-item { justify-content: center; padding: 0; }
}

/* Phone: off-canvas drawer, opened from the topbar's menu button. */
@media (max-width: 720px) {
  .sidebar {
    position: fixed;
    inset: 0 auto 0 0;
    width: 260px;
    z-index: 30;
    transform: translateX(-100%);
    /* Closed: hidden too, so its links are out of the tab order. */
    visibility: hidden;
    transition: transform 0.2s ease, visibility 0s linear 0.2s;
  }
  .sidebar.open {
    transform: translateX(0);
    visibility: visible;
    transition: transform 0.2s ease;
    box-shadow: var(--wb-overlay-shadow);
  }
}
</style>
