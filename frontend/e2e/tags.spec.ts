import { test, expect } from "@playwright/test";
import { apiPost as post, statusChip, uniq } from "./helpers";

test.use({ colorScheme: "light" });

// Brand amber chip: text #8a5a12 on #f6ecd6 (spec "Visual system").
const AMBER_FG = "rgb(138, 90, 18)";
const AMBER_BG = "rgb(246, 236, 214)";

test("amber status chips use the brand amber pair", async ({ page }) => {
  // A paused recurring template is an amber status.
  const api = page.request;
  const customerName = `E2E Tags Customer ${uniq()}`;
  const c = await post<{ customer_id: string }>(api, "/customers", { name: customerName, default_currency: "IDR" });
  await post(api, "/invoices/recurring-template", {
    customer_id: c.customer_id,
    currency: "IDR",
    payment_terms: "",
    schedule: { frequency: "MONTHLY", start_date: "2027-01-01" },
    line_items: [{ description: "E2E tags line", quantity: "1", unit_price: "100000" }],
  });
  const rows = (await (await api.get("/api/v1/invoices/recurring-templates/rows")).json()) as {
    template_id: string;
    customer_name: string | null;
  }[];
  const mine = rows.find((r) => r.customer_name === customerName);
  expect(mine).toBeTruthy();
  const p = await api.patch(`/api/v1/invoices/recurring-templates/${mine!.template_id}`, { data: { action: "PAUSE" } });
  expect(p.ok(), await p.text()).toBe(true);

  await page.goto("/invoices/recurring");
  await page.getByRole("tab", { name: "Paused", exact: true }).click();
  const row = page.locator("tbody tr", { hasText: customerName });
  const chip = statusChip(row, "PAUSED");
  await expect(chip).toBeVisible();
  await expect(chip).toHaveCSS("color", AMBER_FG);
  await expect(chip).toHaveCSS("background-color", AMBER_BG);
  await expect(chip).toHaveCSS("border-radius", "2px");
  await expect(chip).toHaveCSS("text-transform", "uppercase");
});

// PrimeVue 4 names the amber tag severity "warn"; "warning" silently falls back
// to the primary tag. Non-status chips with "warn" share the brand amber pair.
test("warn tags share the brand amber pair", async ({ page }) => {
  await page.goto("/chart-of-accounts");
  const chip = page.locator(".p-tag", { hasText: /^LIABILITY$/i }).first();
  await expect(chip).toBeVisible();
  await expect(chip).toHaveCSS("color", AMBER_FG);
  await expect(chip).toHaveCSS("background-color", AMBER_BG);
});
