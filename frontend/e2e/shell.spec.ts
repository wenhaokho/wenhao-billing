import { test, expect, type Page } from "@playwright/test";
import { initials } from "../src/utils/initials";

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
