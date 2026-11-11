import { test, expect, type APIRequestContext, type Page } from "@playwright/test";
import { ROUTES, apiPost as post, collectConsoleErrors, mainHeading, uniq } from "./helpers";

for (const path of ROUTES) {
  test(`route ${path} renders without errors`, async ({ page }) => {
    const errors = collectConsoleErrors(page);
    const res = await page.goto(path);
    expect(res?.ok()).toBe(true);
    await expect(page).toHaveURL(new RegExp(`${path.replace(/[/]/g, "\\/")}(\\?|$)`));
    await expect(mainHeading(page)).toBeVisible();
    await page.waitForLoadState("networkidle");
    expect(errors()).toEqual([]);
  });
}

test.describe("public auth pages", () => {
  test.use({ storageState: { cookies: [], origins: [] } });
  for (const path of ["/login", "/forgot-password", "/reset-password"]) {
    test(`unauthenticated route ${path} renders without errors`, async ({ page }) => {
      const errors = collectConsoleErrors(page);
      const res = await page.goto(path);
      expect(res?.ok()).toBe(true);
      await expect(page).toHaveURL(new RegExp(`${path.replace(/[/]/g, "\\/")}(\\?|$)`));
      await expect(page.locator("h1, h2").first()).toBeVisible();
      await page.waitForLoadState("networkidle");
      // Signed out, the router's session probe (GET /auth/me) answers 401 by
      // design, and Chromium logs every failed request as a console error.
      const sessionProbe = /status of 401 .*\/api\/v1\/auth\/me\)$/;
      expect(errors().filter((e) => !sessionProbe.test(e))).toEqual([]);
    });
  }
});

// ---- detail routes: reached by clicking the first row of the entity's list ----

async function newCustomer(api: APIRequestContext) {
  return post<{ customer_id: string }>(api, "/customers", { name: `E2E Route Customer ${uniq()}`, default_currency: "IDR" });
}
async function newVendor(api: APIRequestContext) {
  return post<{ vendor_id: string }>(api, "/vendors", { name: `E2E Route Vendor ${uniq()}`, default_currency: "IDR" });
}
const LINE = [{ description: "E2E line", quantity: "1", unit_price: "100000" }];

/** Each entity makes sure its list has at least one row, then opens the list. */
const DETAILS: {
  name: string;
  seed: (api: APIRequestContext) => Promise<unknown>;
  list: string;
  open: (page: Page) => Promise<void>;
  url: RegExp;
}[] = [
  {
    name: "customer",
    seed: newCustomer,
    list: "/customers",
    open: (page) => page.locator("tbody tr").first().getByTitle("View").click(),
    url: /\/customers\/[0-9a-f-]{36}$/,
  },
  {
    name: "invoice edit",
    seed: async (api) => {
      const c = await newCustomer(api);
      return post(api, "/invoices", { customer_id: c.customer_id, currency: "IDR", line_items: LINE });
    },
    list: "/invoices?tab=all",
    open: (page) => page.locator("tbody tr").first().locator("td").nth(1).click(),
    url: /\/invoices\/[0-9a-f-]{36}\/edit/,
  },
  {
    name: "quotation",
    seed: async (api) => {
      const c = await newCustomer(api);
      return post(api, "/quotations", { customer_id: c.customer_id, currency: "IDR", line_items: LINE });
    },
    list: "/quotations?tab=all",
    open: (page) => page.locator("tbody tr").first().locator("td").nth(1).click(),
    url: /\/quotations\/[0-9a-f-]{36}(\?|$)/,
  },
  {
    name: "bill",
    seed: async (api) => {
      const v = await newVendor(api);
      return post(api, "/bills", { vendor_id: v.vendor_id, currency: "IDR", line_items: LINE });
    },
    list: "/bills?tab=all",
    open: (page) => page.locator("tbody tr").first().locator("td").nth(1).click(),
    url: /\/bills\/[0-9a-f-]{36}\/edit/,
  },
  {
    name: "vendor",
    seed: newVendor,
    list: "/vendors",
    open: (page) => page.locator("tbody tr").first().getByTitle("View").click(),
    url: /\/vendors\/[0-9a-f-]{36}$/,
  },
  {
    name: "item",
    seed: (api) => post(api, "/items", { name: `E2E Route Item ${uniq()}`, item_type: "SERVICE", default_currency: "IDR" }),
    list: "/items",
    open: (page) => page.locator("tbody tr").first().getByTitle("View").click(),
    url: /\/items\/[0-9a-f-]{36}$/,
  },
  {
    name: "project",
    seed: async (api) => {
      const c = await newCustomer(api);
      return post(api, "/projects", { customer_id: c.customer_id, code: `E2E-R${uniq()}`, name: "E2E Route Project", currency: "IDR" });
    },
    list: "/projects?tab=all",
    open: (page) => page.locator("tbody tr").first().getByTitle("View").click(),
    url: /\/projects\/[0-9a-f-]{36}(\?|$)/,
  },
  {
    name: "account",
    // The chart of accounts is seeded by the baseline migration.
    seed: async () => undefined,
    list: "/chart-of-accounts",
    open: (page) => page.locator("tbody tr").first().getByTitle("Edit").click(),
    url: /\/chart-of-accounts\/[^/?]+$/,
  },
];

for (const d of DETAILS) {
  test(`detail route for ${d.name} renders without errors`, async ({ page }) => {
    await d.seed(page.request);
    const errors = collectConsoleErrors(page);
    const res = await page.goto(d.list);
    expect(res?.ok()).toBe(true);
    await expect(page.locator("tbody tr").first()).toBeVisible();
    await d.open(page);
    await expect(page).toHaveURL(d.url);
    await expect(mainHeading(page)).toBeVisible();
    await page.waitForLoadState("networkidle");
    expect(errors()).toEqual([]);
  });
}

test("audit log Export CSV link returns the filtered CSV", async ({ page }) => {
  await page.goto("/audit-log");
  await expect(mainHeading(page)).toBeVisible();
  await page.waitForLoadState("networkidle");
  const link = page.getByTestId("export-csv");
  await expect(link).toHaveAttribute("href", "/api/v1/payments/audit-log.csv");
  // The link carries the current filters.
  await page.getByPlaceholder("Search payer name…").fill("acme");
  await expect(link).toHaveAttribute("href", "/api/v1/payments/audit-log.csv?payer=acme");
  const res = await page.request.get((await link.getAttribute("href")) as string);
  expect(res.status()).toBe(200);
  expect(res.headers()["content-type"]).toContain("text/csv");
  expect(res.headers()["content-disposition"]).toMatch(/^attachment; filename="audit-log-\d{4}-\d{2}-\d{2}\.csv"$/);
  expect((await res.text()).split("\n")[0]).toBe(
    "created_at,action,reason_codes,reasons,payer_name,amount,currency,payment_date,invoice_number,customer_name,actor_name,intake_source,confidence_score",
  );
});

test("invoices tiles show their second lines", async ({ page }) => {
  // The live summary carries the counts (other specs add invoices meanwhile,
  // so only the fields are checked here)...
  const live = (await (await page.request.get("/api/v1/stats/invoices-summary")).json()) as Record<string, unknown>;
  for (const k of ["overdue_count", "due_30d_count", "oldest_overdue_days"]) expect(live, k).toHaveProperty(k);
  // ...and fixed summaries pin the wording.
  const base = { overdue: [{ currency: "IDR", amount: "1000.0000" }], due_30d: [{ currency: "IDR", amount: "500.0000" }], avg_days_to_pay: 3.9 };
  await page.route("**/api/v1/stats/invoices-summary", (r) =>
    r.fulfill({ json: { ...base, overdue_count: 7, due_30d_count: 1, oldest_overdue_days: 12 } }));
  await page.goto("/invoices");
  await expect(page.getByTestId("overdue-sub")).toHaveText("7 invoices past due · oldest 12 days overdue");
  await expect(page.getByTestId("due30d-sub")).toHaveText("1 invoice");
  await expect(page.getByText("Last 90 days")).toBeVisible();
  await page.unroute("**/api/v1/stats/invoices-summary");
  await page.route("**/api/v1/stats/invoices-summary", (r) =>
    r.fulfill({ json: { overdue: [], due_30d: [], avg_days_to_pay: null, overdue_count: 0, due_30d_count: 0, oldest_overdue_days: null } }));
  await page.reload();
  await expect(page.getByTestId("overdue-sub")).toHaveText("Nothing past due");
  await expect(page.getByTestId("due30d-sub")).toHaveText("No invoices");
});

test("awaiting finalization shows the next batch or scheduler off", async ({ page }) => {
  await page.goto("/queue");
  const line = page.getByTestId("next-batch");
  await expect(line).toBeVisible();
  const nb = (await (await page.request.get("/api/v1/invoices/next-batch")).json()) as {
    scheduler_enabled: boolean; next_batch_at: string | null;
  };
  await expect(line).toHaveText(nb.next_batch_at ? /Next batch: .*\d/ : "Scheduler off");
  // Both forms render, whatever the dev stack's SCHEDULER_ENABLED.
  await page.route("**/api/v1/invoices/next-batch", (r) =>
    r.fulfill({ json: { scheduler_enabled: true, next_batch_at: "2026-11-13T02:00:00Z" } }));
  await page.reload();
  await expect(line).toHaveText(/Next batch: .*2026/);
  await page.unroute("**/api/v1/invoices/next-batch");
  await page.route("**/api/v1/invoices/next-batch", (r) =>
    r.fulfill({ json: { scheduler_enabled: false, next_batch_at: null } }));
  await page.reload();
  await expect(line).toHaveText("Scheduler off");
});

test("AI Assistant renders its badges and icons", async ({ page }) => {
  await page.goto("/mcp-integration");
  const cards = page.getByTestId("ai-capability");
  await expect(cards).toHaveCount(3);
  for (const [i, label] of ["Reads", "Writes", "Confirm first"].entries()) {
    await expect(cards.nth(i).locator(".status-tag")).toHaveText(label);
  }
  // Every capability item carries its decorative icon.
  await expect(cards.locator(".cap-icon svg")).toHaveCount(5);
});

test("a paid invoice names who cleared it", async ({ page }) => {
  const c = await newCustomer(page.request);
  const inv = await post<{ invoice_id: string; invoice_number: string | null }>(page.request, "/invoices", {
    customer_id: c.customer_id, currency: "IDR", line_items: LINE,
  });
  const fin = await post<{ invoice_number: string }>(page.request, `/invoices/${inv.invoice_id}/finalize`, {});
  await page.route(`**/api/v1/invoices/${inv.invoice_id}`, async (r) => {
    if (r.request().method() !== "GET") return r.continue();
    const res = await r.fetch();
    const body = await res.json();
    await r.fulfill({ response: res, json: { ...body, status: "PAID", cleared_at: "2026-11-01T07:05:00Z", cleared_by: null } });
  });
  await page.goto(`/invoices/${inv.invoice_id}/edit`);
  await expect(page.locator("main h1 .title-number")).toHaveText(fin.invoice_number);
  await expect(page.getByTestId("cleared-line")).toHaveText(/Paid · cleared by the matching engine on .*2026\.$/);
  await expect(page.getByText("Printed on the PDF.")).toBeVisible();
  await expect(page.getByText("Visible to your team only.")).toBeVisible();
});
