<script setup lang="ts">
import { RouterView, useRoute } from "vue-router";
import { computed, ref } from "vue";
import ConfirmDialog from "primevue/confirmdialog";
import { useAuthStore } from "@/stores/auth";
import AppIcon from "@/components/AppIcon.vue";
import AppSidebar from "@/components/shell/AppSidebar.vue";
import AppTopbar from "@/components/shell/AppTopbar.vue";
import type { IconName } from "@/theme/icons";

const auth = useAuthStore();
const route = useRoute();

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
    <AppSidebar :open="mobileNavOpen" @navigate="closeMobileNav" />

    <div class="main-col">
      <AppTopbar :title="pageTitle" @toggle-nav="toggleMobileNav" />
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
  grid-template-columns: auto minmax(0, 1fr);
  min-height: 100vh;
  background: var(--wb-bg);
}

.main-col { display: flex; flex-direction: column; min-width: 0; }

.page {
  flex: 1;
  min-width: 0;
  width: 100%;
  max-width: var(--content-max-width);
  margin: 0 auto;
  padding: 40px 36px 60px;
}
.page:focus { outline: none; }

.skip-link {
  position: fixed;
  top: 0.5rem;
  left: 0.5rem;
  transform: translateY(-150%);
  background: var(--wb-red);
  color: #fff;
  padding: 0.55rem 0.9rem;
  border-radius: 2px;
  font-size: 0.85rem;
  font-weight: 600;
  text-decoration: none;
  z-index: 100;
  transition: transform 0.15s;
}
.skip-link:focus {
  transform: translateY(0);
  outline: 2px solid #fff;
  outline-offset: 2px;
}

.nav-scrim {
  display: none;
  position: fixed;
  inset: 0;
  background: rgba(23, 17, 15, 0.45);
  z-index: 20;
}

.anon-shell { min-height: 100vh; display: flex; }
.anon-shell > :deep(*) { flex: 1; }

@media (max-width: 1100px) {
  .page { padding: 28px 24px 48px; }
}

@media (max-width: 720px) {
  .shell { grid-template-columns: minmax(0, 1fr); }
  .nav-open .nav-scrim { display: block; }
  .page { padding: 20px 16px 40px; }
}
</style>
