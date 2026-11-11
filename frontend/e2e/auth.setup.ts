import { test as setup, expect } from "@playwright/test";
import { STORAGE_STATE } from "./helpers";

setup("sign in", async ({ page }) => {
  const email = process.env.E2E_EMAIL;
  const password = process.env.E2E_PASSWORD;
  if (!email || !password) throw new Error("E2E_EMAIL / E2E_PASSWORD not set; run through scripts/e2e.sh");

  await page.goto("/login");
  await page.getByLabel("Email").fill(email);
  await page.locator("#password").fill(password);
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.waitForURL("**/dashboard");
  await expect(page.locator("main h1, main h2").first()).toBeVisible();
  await page.context().storageState({ path: STORAGE_STATE });
});
