import { ref, watch } from "vue";

export type Theme = "light" | "dark";

const STORAGE_KEY = "wb:theme";

function resolveInitial(): Theme {
  const stored = localStorage.getItem(STORAGE_KEY);
  if (stored === "light" || stored === "dark") return stored;
  return window.matchMedia?.("(prefers-color-scheme: dark)").matches ? "dark" : "light";
}

const theme = ref<Theme>(resolveInitial());

// PrimeVue 4 switches its dark tokens on the .app-dark class (darkModeSelector
// in main.ts); app.css keys off data-theme.
function apply(t: Theme) {
  const root = document.documentElement;
  root.setAttribute("data-theme", t);
  root.classList.toggle("app-dark", t === "dark");
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
