import { test, expect, type APIRequestContext, type Page } from "@playwright/test";
import { apiPost, isApi, lightSurfaces, uniq } from "./helpers";

test.use({ colorScheme: "light" });

/** YYYY-MM-DD of today shifted by `months` (UTC). */
function isoMonthsAgo(months: number): string {
  const d = new Date();
  d.setUTCDate(1);
  d.setUTCMonth(d.getUTCMonth() - months);
  return d.toISOString().slice(0, 10);
}

/** Makes sure an SGD→IDR rate dated on or before today exists, so SGD converts. */
async function ensureSgdRate(api: APIRequestContext) {
  const today = new Date().toISOString().slice(0, 10);
  const res = await api.get("/api/v1/accounting/fx-rates");
  expect(res.ok(), `GET fx-rates: ${res.status()}`).toBe(true);
  const rates = (await res.json()) as { from_currency: string; to_currency: string; as_of_date: string }[];
  if (rates.some((r) => r.from_currency === "SGD" && r.to_currency === "IDR" && r.as_of_date.slice(0, 10) <= today)) return;
  await apiPost(api, "/accounting/fx-rates", { from_currency: "SGD", to_currency: "IDR", rate: "12000", as_of_date: isoMonthsAgo(1) });
}

/** Creates a customer and a finalized (open) invoice for it; returns both ids. */
async function seedInvoice(api: APIRequestContext, currency: string, issueDate: string, price: string) {
  const name = `E2E Dash ${currency} ${uniq()}`;
  const c = await apiPost<{ customer_id: string }>(api, "/customers", { name, default_currency: currency });
  const inv = await apiPost<{ invoice_id: string }>(api, "/invoices", {
    customer_id: c.customer_id,
    currency,
    issue_date: issueDate,
    due_date: issueDate,
    line_items: [{ description: "E2E dashboard line", quantity: "1", unit_price: price }],
  });
  const fin = await api.post(`/api/v1/invoices/${inv.invoice_id}/finalize`);
  expect(fin.ok(), `finalize: ${fin.status()} ${await fin.text()}`).toBe(true);
  return { name, customerId: c.customer_id, invoiceId: inv.invoice_id };
}

const CARD_HEADINGS = [
  "Revenue — last 12 months",
  "Receivables aging",
  "Expected cash in",
  "Reconciliation health",
  "Top customers",
  "Needs attention",
  "Upcoming invoices",
  "Recent reconciliation activity",
];

async function openDashboard(page: Page, path = "/dashboard") {
  await page.goto(path);
  await expect(page.locator("main").getByRole("heading", { level: 1, name: "Dashboard" })).toBeVisible();
}

test("period buttons change the URL and refetch the overview", async ({ page }) => {
  await openDashboard(page, "/dashboard?period=12m");
  const refetch = page.waitForResponse(
    (r) => isApi(r.request(), "GET", "/stats/overview") && new URL(r.url()).searchParams.get("period") === "90d",
  );
  await page.locator('[data-period="90d"]').click();
  expect((await refetch).ok()).toBe(true);
  await expect(page).toHaveURL(/[?&]period=90d/);
  await expect(page.locator('[data-period="90d"]')).toHaveAttribute("aria-pressed", "true");
  await expect(page.getByTestId("days-to-pay")).toContainText("90 d");
  await expect(page.getByTestId("recon-card")).toContainText("last 90 days");
  // A reload keeps the period.
  await page.reload();
  await expect(page.locator('[data-period="90d"]')).toHaveAttribute("aria-pressed", "true");
});

test("every card heading is visible", async ({ page }) => {
  await openDashboard(page);
  const main = page.locator("main");
  for (const name of CARD_HEADINGS) {
    await expect(main.getByRole("heading", { level: 2, name }), name).toBeVisible();
  }
  await expect(main.getByRole("link", { name: "Open manual review ↗" })).toHaveAttribute("href", "/review");
  await expect(main.getByRole("link", { name: "All invoices ↗" })).toHaveAttribute("href", "/invoices");
});

test("seeded invoices: SGD tab, KPI delta, overdue row links", async ({ page }) => {
  const api = page.request;
  await ensureSgdRate(api);
  // One SGD invoice in the current window, one IDR invoice in the previous
  // 12-month window (20 months back) so the Invoiced KPI has a base to compare.
  const sgd = await seedInvoice(api, "SGD", isoMonthsAgo(0), "100");
  const old = await seedInvoice(api, "IDR", isoMonthsAgo(20), "1000000");
  try {
    await checkSeeded(page, old);
  } finally {
    // Void both so later runs' rows are not crowded out of the six oldest.
    for (const id of [sgd.invoiceId, old.invoiceId]) {
      const res = await api.post(`/api/v1/invoices/${id}/void`, { data: { reason: "E2E dashboard cleanup" } });
      expect.soft(res.ok(), `void ${id}: ${res.status()} ${await res.text()}`).toBe(true);
    }
  }
});

async function checkSeeded(page: Page, old: { name: string; customerId: string; invoiceId: string }) {
  await openDashboard(page, "/dashboard?period=12m");
  const revenue = page.getByTestId("revenue-card");
  await expect(revenue.locator('[data-ccy="SGD"]')).toBeVisible();
  await revenue.locator('[data-ccy="SGD"]').click();
  await expect(revenue.locator('[data-ccy="SGD"]')).toHaveAttribute("aria-pressed", "true");

  await expect(page.locator('[data-kpi="invoiced"] .kpi-delta')).toHaveText(/[▲▼]/);

  // The 20-month-old invoice is the oldest overdue one: its row links to the
  // invoice and its customer.
  const row = page.getByTestId("needs-attention-card").locator(`[data-invoice="${old.invoiceId}"]`);
  await expect(row).toBeVisible();
  await expect(row.getByRole("link", { name: old.name })).toHaveAttribute("href", `/customers/${old.customerId}`);
  await expect(row.locator(`a[href^="/invoices/${old.invoiceId}/edit"]`)).toBeVisible();
  await expect(row).toContainText(/\d+d late/);
  // No reminder button yet (it comes with the reminders work).
  await expect(row.getByRole("button")).toHaveCount(0);
}

test("no horizontal scroll at 375", async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 812 });
  await openDashboard(page);
  await expect(page.getByTestId("aging-card")).toBeVisible();
  await expect(page.getByTestId("top-customers-card")).toBeVisible();
  await page.waitForLoadState("networkidle");
  const over = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect(over, "page wider than the viewport").toBeLessThanOrEqual(0);
});

test.describe("dark mode", () => {
  test.use({ colorScheme: "dark", viewport: { width: 1440, height: 900 } });
  test("has no light surfaces", async ({ page }) => {
    await page.addInitScript(() => localStorage.setItem("wb:theme", "dark"));
    await openDashboard(page);
    await expect(page.locator("html")).toHaveClass(/(^|\s)app-dark(\s|$)/);
    await expect(page.getByTestId("aging-card")).toBeVisible();
    await expect(page.getByTestId("recon-card")).toBeVisible();
    await page.waitForLoadState("networkidle");
    expect(await lightSurfaces(page)).toEqual([]);
  });
});
