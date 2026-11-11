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
  const bg = await list.evaluate((el) => {
    const panel = el.closest('[data-pc-section="overlay"], .p-select-overlay, .p-dropdown-panel') ?? el;
    return getComputedStyle(panel).backgroundColor;
  });
  expect(bg).not.toBe("rgb(255, 255, 255)");
});
