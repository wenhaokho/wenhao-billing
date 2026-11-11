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

/** YYYY-MM-DD of `days` before today (UTC). */
function isoDaysAgo(days: number): string {
  return new Date(Date.now() - days * 86_400_000).toISOString().slice(0, 10);
}

/**
 * Makes sure an SGD→IDR rate dated on or before today exists, so SGD converts.
 * When none does, it creates one and leaves it in the dev database: a rate is
 * harmless there, and deleting it could remove one another test relies on.
 */
async function ensureSgdRate(api: APIRequestContext) {
  const today = new Date().toISOString().slice(0, 10);
  const res = await api.get("/api/v1/accounting/fx-rates");
  expect(res.ok(), `GET fx-rates: ${res.status()}`).toBe(true);
  const rates = (await res.json()) as { from_currency: string; to_currency: string; as_of_date: string }[];
  if (rates.some((r) => r.from_currency === "SGD" && r.to_currency === "IDR" && r.as_of_date.slice(0, 10) <= today)) return;
  await apiPost(api, "/accounting/fx-rates", { from_currency: "SGD", to_currency: "IDR", rate: "12000", as_of_date: isoMonthsAgo(1) });
}

/** The line description that marks this spec's invoices, so leftovers can be found. */
const SEED_LINE = "E2E dashboard line";

/** Voids (or, for a draft, deletes) an invoice; true when it worked. */
async function discard(api: APIRequestContext, invoiceId: string): Promise<boolean> {
  const v = await api.post(`/api/v1/invoices/${invoiceId}/void`, { data: { reason: "E2E dashboard cleanup" } });
  if (v.ok()) return true;
  return (await api.delete(`/api/v1/invoices/${invoiceId}`)).ok();
}

/** Voids open invoices an earlier, interrupted run of this spec left behind. */
async function discardLeftovers(api: APIRequestContext) {
  const res = await api.get("/api/v1/invoices?status=OPEN&status=SENT&status=PARTIAL&limit=500");
  expect(res.ok(), `GET invoices: ${res.status()}`).toBe(true);
  const list = (await res.json()) as { invoice_id: string; line_items: { description: string }[] }[];
  for (const inv of list.filter((i) => i.line_items.some((l) => l.description === SEED_LINE))) {
    expect(await discard(api, inv.invoice_id), `discard leftover ${inv.invoice_id}`).toBe(true);
  }
}

/**
 * Creates a customer and a finalized (open) invoice for it. The invoice id is
 * pushed to `created` as soon as it exists, so the caller's cleanup gets it
 * even when finalizing fails.
 */
async function seedInvoice(api: APIRequestContext, created: string[], currency: string, date: string, price: string) {
  const name = `E2E Dash ${currency} ${uniq()}`;
  const c = await apiPost<{ customer_id: string }>(api, "/customers", { name, default_currency: currency });
  const inv = await apiPost<{ invoice_id: string }>(api, "/invoices", {
    customer_id: c.customer_id,
    currency,
    issue_date: date,
    due_date: date,
    line_items: [{ description: SEED_LINE, quantity: "1", unit_price: price }],
  });
  created.push(inv.invoice_id);
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

test("seeded invoices: SGD tab, KPI delta, overdue rows link", async ({ page }) => {
  const api = page.request;
  await discardLeftovers(api);
  await ensureSgdRate(api);
  const created: string[] = [];
  try {
    // One SGD invoice in the current window, one IDR invoice in the previous
    // 12-month window (400-699 days back, a date unlikely to tie with another
    // run's) so the Invoiced KPI has a base to compare against.
    await seedInvoice(api, created, "SGD", isoMonthsAgo(0), "100");
    const old = isoDaysAgo(400 + (Date.now() % 300));
    await seedInvoice(api, created, "IDR", old, "1000000");
    await checkSeeded(page);
  } finally {
    // Void everything created, so later runs start from the same state.
    for (const id of created) expect.soft(await discard(api, id), `discard ${id}`).toBe(true);
  }
});

async function checkSeeded(page: Page) {
  // The rows the page renders come from this response: compare against it,
  // so the check holds whatever other overdue invoices the database has.
  const receivables = page.waitForResponse((r) => isApi(r.request(), "GET", "/stats/receivables"));
  await openDashboard(page, "/dashboard?period=12m");
  const body = (await (await receivables).json()) as {
    needs_attention: { invoice_id: string; customer_id: string | null; days_late: number }[];
  };

  const revenue = page.getByTestId("revenue-card");
  await expect(revenue.locator('[data-ccy="SGD"]')).toBeVisible();
  await revenue.locator('[data-ccy="SGD"]').click();
  await expect(revenue.locator('[data-ccy="SGD"]')).toHaveAttribute("aria-pressed", "true");

  await expect(page.locator('[data-kpi="invoiced"] .kpi-delta')).toHaveText(/[▲▼]/);

  // Both seeded invoices are overdue, so the list cannot be empty; every row
  // links to its invoice, and to its customer when it has one.
  expect(body.needs_attention.length).toBeGreaterThan(0);
  const card = page.getByTestId("needs-attention-card");
  await expect(card.locator("[data-invoice]")).toHaveCount(body.needs_attention.length);
  for (const r of body.needs_attention) {
    const row = card.locator(`[data-invoice="${r.invoice_id}"]`);
    await expect(row.locator(`a[href^="/invoices/${r.invoice_id}/edit"]`)).toBeVisible();
    if (r.customer_id) await expect(row.locator(`a[href="/customers/${r.customer_id}"]`)).toBeVisible();
    else await expect(row).toContainText("No customer");
    await expect(row).toContainText(`${r.days_late}d late`);
  }
  // One "Send reminder" per row (reminders.spec.ts covers sending).
  await expect(card.getByRole("button", { name: "Send reminder" })).toHaveCount(body.needs_attention.length);
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
