import { ref, watch } from "vue";
import lightThemeUrl from "primevue/resources/themes/aura-light-blue/theme.css?url";
import darkThemeUrl from "primevue/resources/themes/aura-dark-blue/theme.css?url";

export type Theme = "light" | "dark";

const STORAGE_KEY = "wb:theme";
const LINK_ID = "prime-theme";

function resolveInitial(): Theme {
  const stored = localStorage.getItem(STORAGE_KEY);
  if (stored === "light" || stored === "dark") return stored;
  return window.matchMedia?.("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}

const theme = ref<Theme>(resolveInitial());

function ensureLink(): HTMLLinkElement {
  let link = document.getElementById(LINK_ID) as HTMLLinkElement | null;
  if (!link) {
    link = document.createElement("link");
    link.id = LINK_ID;
    link.rel = "stylesheet";
    document.head.appendChild(link);
  }
  return link;
}

function apply(t: Theme) {
  document.documentElement.setAttribute("data-theme", t);
  ensureLink().href = t === "dark" ? darkThemeUrl : lightThemeUrl;
}

apply(theme.value);

watch(theme, (t) => {
  localStorage.setItem(STORAGE_KEY, t);
  apply(t);
});

export function useTheme() {
  return {
    theme,
    toggle: () => {
      theme.value = theme.value === "dark" ? "light" : "dark";
    },
  };
}
