import { test, expect, type Page } from "@playwright/test";
import { ROUTES, lightSurfaces, openRecordPayment, selectIn } from "./helpers";

test.use({ colorScheme: "light" });

async function goDark(page: Page) {
  await page.goto("/invoices/new");
  await page.evaluate(() => localStorage.removeItem("wb:theme"));
  await page.reload();
  await page.getByRole("button", { name: "Switch to dark theme" }).click();
  await expect(page.getByRole("button", { name: "Switch to light theme" })).toBeVisible();
}

test("dark choice persists across reload", async ({ page }) => {
  await goDark(page);
  await page.reload();
  const html = page.locator("html");
  await expect(html).toHaveClass(/(^|\s)app-dark(\s|$)/);
  await expect(html).toHaveAttribute("data-theme", "dark");
});

test("select panel follows dark mode", async ({ page }) => {
  await goDark(page);
  await page.reload();
  await selectIn(page, "Choose a customer").click();
  const list = page.getByRole("listbox");
  await expect(list).toBeVisible();
  // The select's overlay panel (PrimeVue's data-pc-section="overlay"), not the
  // listbox inside it: the listbox itself is transparent.
  const overlay = list.locator('xpath=ancestor::*[@data-pc-section="overlay"][1]');
  await expect(overlay).toHaveCount(1);
  const bg = await overlay.evaluate((el) => getComputedStyle(el).backgroundColor);
  const m = bg.match(/^rgba?\((\d+), (\d+), (\d+)(?:, ([\d.]+))?\)$/);
  expect(m, `overlay background "${bg}"`).not.toBeNull();
  const [r, g, b] = [m![1], m![2], m![3]].map(Number);
  const alpha = m![4] === undefined ? 1 : Number(m![4]);
  expect(alpha, `overlay background "${bg}" is opaque`).toBe(1);
  // WCAG relative luminance: white is 1, black 0. A dark panel sits well
  // below 0.2 (Aura's dark surface-900 is about 0.01).
  const lin = (c: number) => {
    const s = c / 255;
    return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
  };
  const luminance = 0.2126 * lin(r) + 0.7152 * lin(g) + 0.0722 * lin(b);
  expect(luminance, `overlay background "${bg}" is dark`).toBeLessThan(0.2);
});

test("primary colour is the brand red, light and dark", async ({ page }) => {
  await page.goto("/invoices");
  const button = page.getByRole("button", { name: "New invoice" });
  await expect(button).toBeVisible();
  const bg = () => button.evaluate((el) => getComputedStyle(el).backgroundColor);
  // Brand red #a51c30 in light mode.
  await expect.poll(bg).toBe("rgb(165, 28, 48)");
  await page.getByRole("button", { name: "Switch to dark theme" }).click();
  await expect(page.getByRole("button", { name: "Switch to light theme" })).toBeVisible();
  // The dark-mode red #c9414f (spec "Dark tokens").
  await expect.poll(bg).toBe("rgb(201, 65, 79)");
});

test.describe("dark mode has no light surfaces", () => {
  test.use({ colorScheme: "dark", viewport: { width: 1440, height: 900 } });
  const check = async (page: Page, path: string) => {
    await page.addInitScript(() => localStorage.setItem("wb:theme", "dark"));
    await page.goto(path);
    await expect(page.locator("html")).toHaveClass(/(^|\s)app-dark(\s|$)/);
    await expect(page.locator("h1").first()).toBeVisible();
    await page.waitForLoadState("networkidle");
    expect.soft(await lightSurfaces(page), `${path} light surfaces`).toEqual([]);
    expect(await page.evaluate(() => getComputedStyle(document.body).color), `${path} body text`).toBe("rgb(252, 251, 246)");
  };
  for (const path of ROUTES) test(path, ({ page }) => check(page, path));
  test.describe("signed out", () => {
    test.use({ storageState: { cookies: [], origins: [] } });
    for (const path of ["/login", "/forgot-password", "/reset-password"]) test(path, ({ page }) => check(page, path));
  });
});

test.describe("dark overlays have no light surfaces", () => {
  test.use({ colorScheme: "dark", viewport: { width: 1440, height: 900 } });
  test.beforeEach(async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem("wb:theme", "dark"));
  });

  test("Record payment dialog and its date picker", async ({ page }) => {
    const dialog = await openRecordPayment(page);
    expect.soft(await lightSurfaces(page), "dialog").toEqual([]);
    await dialog.locator('[data-pc-name="datepicker"] input').click();
    await expect(page.getByRole("dialog", { name: "Choose Date" })).toBeVisible();
    expect(await lightSurfaces(page), "date picker").toEqual([]);
  });

  test("user menu", async ({ page }) => {
    await page.goto("/dashboard");
    await page.getByRole("button", { name: "Open account menu" }).click();
    await expect(page.getByRole("menu")).toBeVisible();
    expect(await lightSurfaces(page)).toEqual([]);
  });

  test("navigation drawer at 375", async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 812 });
    await page.goto("/invoices");
    await page.getByRole("button", { name: "Open navigation" }).click();
    await expect(page.locator("#app-sidebar")).toBeVisible();
    await page.waitForLoadState("networkidle");
    expect(await lightSurfaces(page)).toEqual([]);
  });
});
