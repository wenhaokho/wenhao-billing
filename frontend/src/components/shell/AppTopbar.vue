<script setup lang="ts">
import { computed, ref } from "vue";
import { useRouter } from "vue-router";
import Menu from "primevue/menu";
import type { MenuItem } from "primevue/menuitem";
import { useAuthStore } from "@/stores/auth";
import { useTheme } from "@/composables/useTheme";
import AppIcon from "@/components/AppIcon.vue";
import type { IconName } from "@/theme/icons";

defineProps<{ title: string }>();
const emit = defineEmits<{ "toggle-nav": [] }>();

const { theme, toggle: toggleTheme } = useTheme();
const auth = useAuthStore();
const router = useRouter();

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

const userInitials = computed(() => {
  const label = auth.displayLabel;
  if (label.includes("@")) return label.slice(0, 2).toUpperCase();
  // display_name: use initials of the first two words
  const parts = label.trim().split(/\s+/).filter(Boolean);
  if (parts.length >= 2) return (parts[0][0] + parts[1][0]).toUpperCase();
  return label.slice(0, 2).toUpperCase();
});

const themeLabel = computed(() =>
  theme.value === "dark" ? "Switch to light theme" : "Switch to dark theme",
);
</script>

<template>
  <header class="topbar">
    <div class="topbar-left">
      <button
        type="button"
        class="nav-toggle"
        aria-label="Open navigation"
        @click="emit('toggle-nav')"
      >
        <AppIcon name="menu" />
      </button>
      <h2 class="topbar-title">{{ title }}</h2>
    </div>
    <div class="topbar-right">
      <button
        type="button"
        class="theme-toggle"
        :aria-label="themeLabel"
        :title="themeLabel"
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
        <span class="avatar">{{ userInitials }}</span>
        <span class="user-meta">
          <span class="user-name">{{ auth.displayLabel }}</span>
          <span class="user-role">{{ auth.user?.role ?? "admin" }}</span>
        </span>
        <AppIcon name="chevron-down" :size="16" class="chevron" />
      </button>
      <Menu ref="userMenu" :model="userMenuItems" :popup="true">
        <template #itemicon="{ item, class: iconClass }">
          <AppIcon :name="item.icon as IconName" :class="iconClass" />
        </template>
      </Menu>
    </div>
  </header>
</template>

<style scoped>
.topbar {
  height: var(--topbar-height);
  background: var(--wb-surface);
  border-bottom: 1px solid var(--wb-line);
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 1rem;
  padding: 0 36px;
  position: sticky;
  top: 0;
  z-index: 10;
}
.topbar-left { display: flex; align-items: center; gap: 0.75rem; min-width: 0; }
.topbar-title {
  margin: 0;
  font: 500 16px var(--wb-font-display);
  letter-spacing: -0.2px;
  color: var(--wb-ink);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.topbar-right { display: flex; align-items: center; gap: 14px; flex: none; }

.nav-toggle,
.theme-toggle {
  width: 38px;
  height: 38px;
  flex: none;
  display: grid;
  place-items: center;
  border: 1px solid var(--wb-line);
  background: transparent;
  color: var(--wb-ink);
  cursor: pointer;
  transition: background 0.15s;
}
.nav-toggle { display: none; border-radius: 2px; }
.theme-toggle { border-radius: 50%; }
.nav-toggle:hover,
.theme-toggle:hover { background: var(--wb-tint); }
.nav-toggle:focus-visible,
.theme-toggle:focus-visible,
.user-chip:focus-visible { outline: 2px solid var(--wb-red); outline-offset: 2px; }

.user-chip {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 5px 12px 5px 5px;
  border: 1px solid var(--wb-line);
  border-radius: 30px;
  background: transparent;
  color: var(--wb-ink);
  cursor: pointer;
  font: inherit;
  text-align: left;
  transition: background 0.15s;
}
.user-chip:hover { background: var(--wb-tint); }
.avatar {
  width: 30px;
  height: 30px;
  flex: none;
  border-radius: 50%;
  background: var(--wb-red);
  color: #fff;
  display: grid;
  place-items: center;
  font: 600 12px var(--wb-font-display);
}
.user-meta { display: flex; flex-direction: column; line-height: 1.15; min-width: 0; }
.user-name { font-size: 13px; font-weight: 500; max-width: 200px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.user-role { font-size: 10px; letter-spacing: 1.2px; text-transform: uppercase; color: var(--wb-muted); }
.chevron { color: var(--wb-muted); }

@media (max-width: 1100px) {
  .user-meta { display: none; }
}

@media (max-width: 720px) {
  .topbar { padding: 0 16px; }
  .nav-toggle { display: grid; }
  .user-chip { padding: 4px; gap: 4px; }
  .chevron { display: none; }
}
</style>
