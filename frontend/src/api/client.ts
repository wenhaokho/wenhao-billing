import axios from "axios";

export const api = axios.create({
  baseURL: "/api/v1",
  withCredentials: true,
});

api.interceptors.response.use(
  (r) => r,
  async (err) => {
    const url: string = err?.config?.url ?? "";
    // /auth/me is the session probe itself — fetchMe already treats a 401 as
    // "signed out". Reacting here would re-enter the router guard, which calls
    // fetchMe again.
    if (err?.response?.status === 401 && !url.includes("/auth/me")) {
      // Imported lazily: router -> stores/auth -> api/client is a module cycle,
      // and a static import would leave `router` undefined at eval time.
      const [{ default: router }, { useAuthStore }] = await Promise.all([
        import("@/router"),
        import("@/stores/auth"),
      ]);
      useAuthStore().user = null;
      // Drop the dead session and let the router guard decide where to go.
      // Never hard-navigate (window.location): that skips the guard, loses the
      // route the user was aiming at, and can ping-pong with its redirect.
      const current = router.currentRoute.value;
      if (!current.meta.public) {
        await router.push({ name: "login", query: { next: current.fullPath } });
      }
    }
    return Promise.reject(err);
  },
);
