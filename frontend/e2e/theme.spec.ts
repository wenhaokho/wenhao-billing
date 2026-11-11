import { test, expect, type Page } from "@playwright/test";
import { selectIn } from "./helpers";

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
