import { test, expect, type Page } from "@playwright/test";
import { initials } from "../src/utils/initials";
import { ROUTES, apiPost } from "./helpers";

// The app shell: burgundy sidebar (252px) on desktop, a 76px icon rail on
// tablet (721–1100px) and an off-canvas drawer on phones (≤720px).

test.use({ colorScheme: "light" });

async function lightTheme(page: Page, path: string) {
  await page.goto(path);
  await page.evaluate(() => localStorage.setItem("wb:theme", "light"));
  await page.reload();
}

// A CSS locator, not getByRole: the closed phone drawer is hidden from the
// accessibility tree, and its box (null when hidden) is what the test reads.
const sidebar = (page: Page) => page.locator('aside[aria-label="Sidebar"]');

test("desktop sidebar", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await lightTheme(page, "/invoices");
  const aside = sidebar(page);
  await expect(aside).toBeVisible();
  const box = await aside.boundingBox();
  expect(box?.width).toBe(252);
  expect(await aside.evaluate((el) => getComputedStyle(el).backgroundColor)).toBe("rgb(80, 25, 33)");

  const eyebrows = aside.locator(".nav-group-label");
  await expect(eyebrows).toHaveText([
    "01 / Overview",
    "02 / Sales",
    "03 / Purchases",
    "04 / Accounting",
    "05 / Operations",
    "06 / Admin",
  ]);

  const active = aside.getByRole("link", { name: "Invoices" });
  await expect(active).toHaveClass(/router-link-active/);
  expect(await active.evaluate((el) => getComputedStyle(el).backgroundColor)).toBe("rgb(165, 28, 48)");

  await expect(aside.getByText("Wenhao Billing", { exact: true })).toBeVisible();
  await expect(aside.getByText("Software house", { exact: true })).toBeVisible();
});

test("tablet rail", async ({ page }) => {
  await page.setViewportSize({ width: 900, height: 1000 });
  await lightTheme(page, "/dashboard");
  const aside = sidebar(page);
  await expect(aside).toBeVisible();
  expect((await aside.boundingBox())?.width).toBe(76);
  const link = aside.getByRole("link", { name: "Customers" });
  await expect(link.locator(".nav-label")).toBeHidden();
  await expect(link.locator("svg")).toBeVisible();
  await expect(aside.locator(".nav-group-label").first()).toBeHidden();
});

test("phone drawer", async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 812 });
  await lightTheme(page, "/dashboard");
  const aside = sidebar(page);
  // Off-canvas: entirely left of the viewport.
  await expect.poll(async () => {
    const b = await aside.boundingBox();
    return b ? b.x + b.width : 0;
  }).toBeLessThanOrEqual(0);
  await expect(aside).toBeHidden();

  const menuButton = page.getByRole("button", { name: "Open navigation" });
  await expect(menuButton).toHaveAttribute("aria-controls", "app-sidebar");
  await expect(menuButton).toHaveAttribute("aria-expanded", "false");
  await menuButton.click();
  await expect(menuButton).toHaveAttribute("aria-expanded", "true");
  await expect.poll(async () => (await aside.boundingBox())?.x).toBe(0);

  await aside.getByRole("link", { name: "Customers" }).click();
  await expect(page).toHaveURL(/\/customers$/);
  await expect.poll(async () => {
    const b = await aside.boundingBox();
    return b ? b.x + b.width : 0;
  }).toBeLessThanOrEqual(0);
  await expect(aside).toBeHidden();
});

for (const path of ["/invoices", "/reports", "/audit-log"]) {
  test(`no horizontal page scroll on phone: ${path}`, async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 812 });
    await lightTheme(page, path);
    await page.waitForLoadState("networkidle");
    const width = await page.evaluate(() => document.documentElement.scrollWidth);
    expect(width).toBeLessThanOrEqual(375);
  });
}

test("topbar", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await lightTheme(page, "/fx-rates");
  const bar = page.getByRole("banner");
  await expect(bar.locator(".topbar-title")).toHaveText("FX Rates");
  await expect(bar.getByRole("button", { name: /Switch to (dark|light) theme/ })).toBeVisible();
  const chip = bar.getByRole("button", { name: "Open account menu" });
  await expect(chip).toBeVisible();
  // The same initials logic the app uses, applied to the name the chip shows.
  const name = (await chip.locator(".user-name").innerText()).trim();
  await expect(chip.locator(".avatar")).toHaveText(initials(name));
  await expect(chip).toHaveAttribute("aria-expanded", "false");
  await chip.click();
  await expect(chip).toHaveAttribute("aria-expanded", "true");
  await expect(page.getByRole("menuitem", { name: "My account" })).toBeVisible();
  await expect(page.getByRole("menuitem", { name: "Sign out" })).toBeVisible();
});

test("every route names itself in the topbar", async ({ page }) => {
  const blank: string[] = [];
  for (const path of ROUTES) {
    await page.goto(path);
    await expect(page.locator("main h1").first()).toBeVisible();
    const title = (await page.locator("header.topbar .topbar-title").innerText()).trim();
    if (!title) blank.push(path);
  }
  expect(blank).toEqual([]);
});

test("designed screens do not scroll sideways at 375", async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 812 });
  const wide: string[] = [];
  for (const path of ["/dashboard", "/invoices", "/invoices/new", "/review", "/reports"]) {
    await page.goto(path);
    await expect(page.locator("main h1").first()).toBeVisible();
    await page.waitForLoadState("networkidle");
    const over = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    if (over > 0) wide.push(`${path} +${over}px`);
  }
  expect(wide).toEqual([]);
});

test("edit routes name themselves like their page heading", async ({ page }) => {
  const c = await apiPost<{ customer_id: string }>(page.request, "/customers", { name: `E2E Title Customer ${Date.now()}`, default_currency: "IDR" });
  const inv = await apiPost<{ invoice_id: string }>(page.request, "/invoices", {
    customer_id: c.customer_id,
    currency: "IDR",
    line_items: [{ description: "E2E title line", quantity: "1", unit_price: "1000" }],
  });
  const accounts = (await (await page.request.get("/api/v1/accounting/chart-of-accounts")).json()) as { account_id: string }[];
  expect(accounts.length, "chart of accounts").toBeGreaterThan(0);
  for (const [path, title] of [
    [`/invoices/${inv.invoice_id}/edit`, "Edit invoice"],
    [`/chart-of-accounts/${accounts[0].account_id}`, "Edit account"],
  ]) {
    await page.goto(path);
    await expect(page.locator("main").getByRole("heading", { level: 1 }).first(), path).toHaveText(title);
    await expect(page.locator("header.topbar .topbar-title"), path).toHaveText(title);
  }
});

test("an issued invoice's topbar says Invoice, not Edit invoice", async ({ page }) => {
  const c = await apiPost<{ customer_id: string }>(page.request, "/customers", { name: `E2E Issued Title ${Date.now()}`, default_currency: "IDR" });
  const inv = await apiPost<{ invoice_id: string }>(page.request, "/invoices", {
    customer_id: c.customer_id,
    currency: "IDR",
    line_items: [{ description: "E2E issued title line", quantity: "1", unit_price: "1000" }],
  });
  const fin = await page.request.post(`/api/v1/invoices/${inv.invoice_id}/finalize`);
  expect(fin.ok(), `finalize: ${fin.status()} ${await fin.text()}`).toBe(true);
  await page.goto(`/invoices/${inv.invoice_id}/edit`);
  await expect(page.locator("main").getByRole("heading", { level: 1 }).first()).toHaveText(/^\s*Invoice \S+/);
  await expect(page.locator("header.topbar .topbar-title")).toHaveText("Invoice");
  // Leaving the invoice in-app drops the override: the next page names itself.
  await sidebar(page).getByRole("link", { name: "Invoices", exact: true }).click();
  await expect(page).toHaveURL(/\/invoices(\?|$)/);
  await expect(page.locator("header.topbar .topbar-title")).toHaveText("Invoices");
});

test("manual review empty state fits its card at 375", async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 812 });
  // Nothing held, whatever the dev database holds.
  await page.route("**/api/v1/payments/awaiting-review", (r) => r.fulfill({ json: [] }));
  await page.goto("/review");
  const empty = page.locator("main .wb-empty");
  await expect(empty).toBeVisible();
  const card = empty.locator("xpath=ancestor::*[contains(concat(' ', normalize-space(@class), ' '), ' card ')][1]");
  const [e, c] = [await empty.getByText("All recent payments auto-cleared.").boundingBox(), await card.boundingBox()];
  expect(e && c).toBeTruthy();
  expect(e!.x, "message left edge").toBeGreaterThanOrEqual(c!.x);
  expect(e!.x + e!.width, "message right edge").toBeLessThanOrEqual(c!.x + c!.width);
  await expect(page.locator("main")).not.toContainText("on the left");
});
