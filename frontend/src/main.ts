import { createApp } from "vue";
import { createPinia } from "pinia";
import { VueQueryPlugin } from "@tanstack/vue-query";
import * as Sentry from "@sentry/vue";
import PrimeVue from "primevue/config";
import ToastService from "primevue/toastservice";
import ConfirmationService from "primevue/confirmationservice";

import App from "./App.vue";
import router from "./router";

import "primeicons/primeicons.css";
import "@/assets/app.css";
import "@/assets/auth-form.css";
import "@/composables/useTheme";

const app = createApp(App);

// Error monitoring. VITE_SENTRY_DSN is baked in at build time; when unset (dev)
// Sentry stays off. No replay and no default PII — this is financial data.
const sentryDsn = import.meta.env.VITE_SENTRY_DSN;
if (sentryDsn) {
  Sentry.init({
    app,
    dsn: sentryDsn,
    environment: import.meta.env.VITE_SENTRY_ENVIRONMENT || import.meta.env.MODE,
    integrations: [Sentry.browserTracingIntegration({ router })],
    tracesSampleRate: Number(import.meta.env.VITE_SENTRY_TRACES_SAMPLE_RATE ?? 0),
    // Propagate trace headers to our own API so frontend and backend traces link.
    tracePropagationTargets: [/^\/api\//],
  });
}
app.use(createPinia());
app.use(router);
app.use(VueQueryPlugin);
app.use(PrimeVue, { ripple: true });
app.use(ToastService);
app.use(ConfirmationService);
app.mount("#app");
