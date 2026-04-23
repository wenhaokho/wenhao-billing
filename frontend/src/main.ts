import { createApp } from "vue";
import { createPinia } from "pinia";
import { VueQueryPlugin } from "@tanstack/vue-query";
import PrimeVue from "primevue/config";
import ToastService from "primevue/toastservice";
import ConfirmationService from "primevue/confirmationservice";

import App from "./App.vue";
import router from "./router";

import "primevue/resources/themes/aura-light-blue/theme.css";
import "primeicons/primeicons.css";
import "@/assets/app.css";
import "@/assets/auth-form.css";

const app = createApp(App);
app.use(createPinia());
app.use(router);
app.use(VueQueryPlugin);
app.use(PrimeVue, { ripple: true });
app.use(ToastService);
app.use(ConfirmationService);
app.mount("#app");
