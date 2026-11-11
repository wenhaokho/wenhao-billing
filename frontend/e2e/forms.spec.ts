import { test, expect, type Page, type Request } from "@playwright/test";
import {
  apiPost as post,
  clearSelect,
  collectConsoleErrors,
  fillNumber,
  isApi,
  numbersNear,
  pickDate,
  pickOption,
  selectIn,
  uniq,
} from "./helpers";

/**
 * The <label> wrapping a form control whose caption is exactly `caption`. The
 * app's selects and date pickers are not labelled for getByLabel (a select's
 * accessible name is its value), so their controls are found inside this.
 */
function field(page: Page, caption: string) {
  return page.locator("label").filter({ has: page.getByText(caption, { exact: true }) });
}

/**
 * Clicks `save` and returns the saving request (method + API path) and its
 * response, asserting the response is 2xx.
 */
async function submit(page: Page, save: () => Promise<void>, method: string, path: string | RegExp) {
  const reqP = page.waitForRequest((r) => isApi(r, method, path));
  const resP = page.waitForResponse((r) => isApi(r.request(), method, path));
  await save();
  const req: Request = await reqP;
  const res = await resP;
  expect(res.status(), `${method} ${req.url()}: ${await res.text()}`).toBeGreaterThanOrEqual(200);
  expect(res.status()).toBeLessThan(300);
  return { body: req.postDataJSON() as Record<string, unknown>, response: (await res.json()) as Record<string, unknown> };
}

test.describe("in Asia/Jakarta", () => {
  test.use({ timezoneId: "Asia/Jakarta" });

  test("project dates save as the picked calendar day", async ({ page }) => {
    const customerName = `E2E Project Customer ${uniq()}`;
    await post(page.request, "/customers", { name: customerName, default_currency: "IDR" });
    const errors = collectConsoleErrors(page);
    await page.goto("/projects/new");
    await expect(page.locator("main").getByRole("heading", { name: "New project" })).toBeVisible();

    await pickOption(page, selectIn(field(page, "Customer")), customerName);
    const code = `E2E-${uniq()}`;
    // The Code caption carries a hint span, so these two go by placeholder.
    await page.getByPlaceholder("ATLAS-26").fill(code);
    await page.getByPlaceholder("Atlas CRM rebuild").fill(`E2E Project ${code}`);
    await pickDate(page, field(page, "Start date").getByRole("combobox"), "2026-12-01");
    await pickDate(page, field(page, "End date").getByRole("combobox"), "2026-12-31");

    const { body } = await submit(
      page,
      () => page.getByRole("button", { name: "Create project" }).click(),
      "POST",
      "/projects",
    );
    expect(body.code).toBe(code);
    expect(body.start_date).toBe("2026-12-01");
    expect(body.end_date).toBe("2026-12-31");
    expect(errors()).toEqual([]);
  });
});

test("invoice with due date saves", async ({ page }) => {
  const customerName = `E2E Invoice Customer ${uniq()}`;
  await post(page.request, "/customers", { name: customerName, default_currency: "IDR" });
  const errors = collectConsoleErrors(page);
  await page.goto("/invoices/new");
  await expect(page.locator("main h1, main h2").first()).toBeVisible();

  await pickOption(page, selectIn(page, "Choose a customer"), customerName);
  // The currency select sits in the totals row; .ccy-select is the app's own class.
  await pickOption(page, selectIn(page.locator(".ccy-select")), "USD");

  const description = page.getByPlaceholder("Description").first();
  await description.fill("E2E consulting");
  const [qty, price] = [numbersNear(description).nth(0), numbersNear(description).nth(1)];
  await fillNumber(qty, "2");
  await fillNumber(price, "150");

  await pickDate(page, page.getByPlaceholder("On Receipt if left blank"), "2026-12-31");

  const { body } = await submit(
    page,
    () => page.getByRole("button", { name: "Save as DRAFT" }).click(),
    "POST",
    "/invoices",
  );
  expect(body.due_date).toBe("2026-12-31");
  expect(body.currency).toBe("USD");
  expect(typeof body.customer_id).toBe("string");
  const lines = body.line_items as { description: string; quantity: number; unit_price: number }[];
  expect(lines).toHaveLength(1);
  expect(lines[0]).toMatchObject({ description: "E2E consulting", quantity: 2, unit_price: 150 });
  expect(errors()).toEqual([]);
});

test("bill saves", async ({ page }) => {
  const vendorName = `E2E Bill Vendor ${uniq()}`;
  await post(page.request, "/vendors", { name: vendorName, default_currency: "IDR" });
  const errors = collectConsoleErrors(page);
  await page.goto("/bills/new");
  await expect(page.locator("main h1, main h2").first()).toBeVisible();

  await pickOption(page, selectIn(page, "Choose a vendor"), vendorName);
  const billNumber = `E2E-B${uniq()}`;
  await page.getByPlaceholder("As printed on the bill").fill(billNumber);
  const description = page.getByPlaceholder("What was this for").first();
  await description.fill("E2E hosting");
  await fillNumber(numbersNear(description).nth(0), "1");
  await fillNumber(numbersNear(description).nth(1), "250000");

  const { body } = await submit(
    page,
    () => page.getByRole("button", { name: "Save as DRAFT" }).click(),
    "POST",
    "/bills",
  );
  expect(body.bill_number).toBe(billNumber);
  expect(typeof body.vendor_id).toBe("string");
  const lines = body.line_items as { description: string; quantity: number; unit_price: number }[];
  expect(lines[0]).toMatchObject({ description: "E2E hosting", quantity: 1, unit_price: 250000 });
  expect(errors()).toEqual([]);
});

/**
 * Creates a `from`→`to` rate dated 2026-12-01 through /fx-rates/new and checks
 * the saved body. Each caller uses its own pair: (from, to, as_of_date) is
 * unique, and an earlier run's rate for the pair is deleted first.
 */
async function fxRateSaves(page: Page, from: string, to: string) {
  const date = "2026-12-01";
  const list = (await (await page.request.get("/api/v1/accounting/fx-rates")).json()) as {
    rate_id: string | number;
    from_currency: string;
    to_currency: string;
    as_of_date: string;
  }[];
  for (const r of list.filter((r) => r.from_currency === from && r.to_currency === to)) {
    // The Jakarta bug saves the day before, so clear the neighbouring day too.
    if (!r.as_of_date.startsWith(date) && !r.as_of_date.startsWith("2026-11-30")) continue;
    const del = await page.request.delete(`/api/v1/accounting/fx-rates/${r.rate_id}`);
    expect(del.ok(), await del.text()).toBe(true);
  }

  const errors = collectConsoleErrors(page);
  await page.goto("/fx-rates/new");
  await expect(page.locator("main").getByRole("heading", { name: "New FX rate" })).toBeVisible();
  await pickOption(page, selectIn(field(page, "From")), from);
  await pickOption(page, selectIn(field(page, "To")), to);
  await fillNumber(field(page, "Rate").getByRole("spinbutton"), "0.75");
  await pickDate(page, field(page, "As of").getByRole("combobox"), date);

  const { body } = await submit(page, () => page.getByRole("button", { name: "Create" }).click(), "POST", "/accounting/fx-rates");
  expect(body).toMatchObject({ from_currency: from, to_currency: to, rate: 0.75, as_of_date: date });
  expect(errors()).toEqual([]);
}

test.describe("in UTC", () => {
  test.use({ timezoneId: "UTC" });
  test("fx rate saves", async ({ page }) => {
    await fxRateSaves(page, "SGD", "USD");
  });
});

test.describe("in Asia/Jakarta", () => {
  test.use({ timezoneId: "Asia/Jakarta" });
  // A date picked east of UTC is local midnight; it must save as that day,
  // not the previous UTC day.
  test("fx rate saves the picked day", async ({ page }) => {
    await fxRateSaves(page, "USD", "SGD");
  });
});

test("item saves", async ({ page }) => {
  const errors = collectConsoleErrors(page);
  await page.goto("/items/new");
  await expect(page.locator("main h1, main h2").first()).toBeVisible();
  const name = `E2E Item ${uniq()}`;
  await pickOption(page, selectIn(field(page, "Type")), "Fixed fee");
  await field(page, "Name").locator("input").fill(name);
  const { body } = await submit(page, () => page.getByRole("button", { name: "Create item" }).click(), "POST", "/items");
  expect(body).toMatchObject({ name, item_type: "FIXED_FEE" });
  expect(errors()).toEqual([]);
});

test("vendor saves", async ({ page }) => {
  const errors = collectConsoleErrors(page);
  await page.goto("/vendors/new");
  await expect(page.locator("main h1, main h2").first()).toBeVisible();
  const name = `E2E Vendor ${uniq()}`;
  await field(page, "Vendor name").locator("input").fill(name);
  await pickOption(page, selectIn(field(page, "Default currency")), "SGD");
  const { body } = await submit(page, () => page.getByRole("button", { name: "Create vendor" }).click(), "POST", "/vendors");
  expect(body).toMatchObject({ name, default_currency: "SGD" });
  expect(errors()).toEqual([]);
});

test("customer saves", async ({ page }) => {
  const errors = collectConsoleErrors(page);
  await page.goto("/customers/new");
  await expect(page.locator("main h1, main h2").first()).toBeVisible();
  const name = `E2E Customer ${uniq()}`;
  await page.getByPlaceholder("Acme Pte. Ltd.").fill(name);
  const { body } = await submit(page, () => page.getByRole("button", { name: "Create customer" }).click(), "POST", "/customers");
  expect(body.name).toBe(name);
  expect(errors()).toEqual([]);
});

test("account saves", async ({ page }) => {
  const errors = collectConsoleErrors(page);
  await page.goto("/chart-of-accounts/new");
  await expect(page.locator("main h1, main h2").first()).toBeVisible();
  const code = String(Date.now()).slice(-8);
  const name = `E2E Account ${code}`;
  await field(page, "Code").locator("input").fill(code);
  await pickOption(page, selectIn(field(page, "Type")), "Expense");
  await field(page, "Name").locator("input").fill(name);
  const { body } = await submit(
    page,
    () => page.getByRole("button", { name: "Create account" }).click(),
    "POST",
    "/accounting/chart-of-accounts",
  );
  expect(body).toMatchObject({ code, name, type: "EXPENSE" });
  expect(errors()).toEqual([]);
});

test("clearing a list filter select sends no filter", async ({ page }) => {
  // Invoices has no Select filter today; the Audit Log's Action filter is one.
  const errors = collectConsoleErrors(page);
  const path = "/payments/audit-log";
  await page.goto("/audit-log");
  await expect(page.locator("main").getByRole("heading", { name: "Audit Log" })).toBeVisible();
  await page.waitForLoadState("networkidle");
  const action = selectIn(field(page, "Action"));
  // No action picked: the filter says so rather than rendering blank.
  await expect(action).toHaveText("All actions");

  const picked = page.waitForRequest((r) => isApi(r, "GET", path) && new URL(r.url()).searchParams.get("action") === "HELD");
  await pickOption(page, action, "HELD");
  await picked;

  const cleared = page.waitForRequest((r) => isApi(r, "GET", path));
  await clearSelect(action);
  const req = await cleared;
  expect(new URL(req.url()).searchParams.has("action")).toBe(false);
  await expect(action).not.toContainText("HELD");
  await expect(action).toHaveText("All actions");
  expect(errors()).toEqual([]);
});
