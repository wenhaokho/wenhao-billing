import { defineConfig, devices } from "@playwright/test";
import { STORAGE_STATE } from "./e2e/helpers";

// Run through `bash scripts/e2e.sh` (repo root): it starts the dev stack,
// provides BASE_URL, E2E_EMAIL and E2E_PASSWORD, and runs this in the
// Playwright image on the stack's network.
const baseURL = process.env.BASE_URL;
if (!baseURL) throw new Error("BASE_URL is not set; run the suite through scripts/e2e.sh");

export default defineConfig({
  testDir: "e2e",
  retries: 0,
  workers: 4,
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  timeout: 60_000,
  expect: { timeout: 10_000 },
  reporter: [["list"], ["html", { open: "never" }]],
  use: {
    baseURL,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  projects: [
    // No trace or screenshot for sign-in: they would keep the typed password
    // (test-results/, and the HTML report copies traces).
    { name: "setup", testMatch: /auth\.setup\.ts/, use: { trace: "off", screenshot: "off" } },
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"], storageState: STORAGE_STATE },
      dependencies: ["setup"],
    },
  ],
});
