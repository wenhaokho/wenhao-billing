import { defineConfig, loadEnv } from "vite";
import vue from "@vitejs/plugin-vue";
import { fileURLToPath, URL } from "node:url";

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), "");
  const apiTarget = env.VITE_API_PROXY_TARGET || "http://localhost:8000";

  return {
    plugins: [vue()],
    resolve: {
      alias: {
        "@": fileURLToPath(new URL("./src", import.meta.url)),
      },
    },
    server: {
      host: true, // bind 0.0.0.0 so it works in a container
      port: 5173,
      proxy: {
        "/api": {
          target: apiTarget,
          changeOrigin: true,
        },
        // The MCP connector's OAuth server and transport sit at the API's
        // root, as behind nginx in production, so the login bounce
        // (/login?return_to=/oauth/authorize?...) works in development too.
        // The Host is kept (no changeOrigin): the consent POST checks that
        // Origin names it. Regex keys: a plain "/mcp" prefix would also take
        // the SPA's /mcp-integration page.
        "^/oauth/": { target: apiTarget },
        "^/\\.well-known/": { target: apiTarget },
        "^/mcp(/|\\?|$)": { target: apiTarget },
      },
      watch: {
        // Polling for reliable file watching across the Docker bind mount on Windows.
        usePolling: !!env.CHOKIDAR_USEPOLLING,
        interval: 300,
      },
    },
  };
});
