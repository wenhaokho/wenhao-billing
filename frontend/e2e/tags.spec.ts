import { test, expect } from "@playwright/test";

test.use({ colorScheme: "light" });

// PrimeVue 4 names the amber tag severity "warn"; "warning" silently falls back
// to the primary (blue) tag. Aura's warn tag is orange.100 on orange.700.
test("amber status tags keep their warning colour", async ({ page }) => {
  await page.goto("/chart-of-accounts");
  const chip = page.locator(".p-tag", { hasText: /^LIABILITY$/ }).first();
  await expect(chip).toBeVisible();
  await expect(chip).toHaveCSS("background-color", "rgb(255, 237, 213)");
  await expect(chip).toHaveCSS("color", "rgb(194, 65, 12)");
});
