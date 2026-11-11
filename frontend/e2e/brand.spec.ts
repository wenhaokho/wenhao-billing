import { test, expect, type APIRequestContext, type Locator } from "@playwright/test";
import { statusTone } from "../src/theme/status";
import { ROUTES, apiPost, uniq } from "./helpers";

test.use({ colorScheme: "light" });

test("primary button is the brand red pill", async ({ page }) => {
  await page.goto("/invoices");
  const button = page.getByRole("button", { name: "New invoice" });
  await expect(button).toBeVisible();
  await expect(button).toHaveCSS("background-color", "rgb(165, 28, 48)");
  await expect(button).toHaveCSS("border-radius", "30px");
  await expect(button).toHaveCSS("height", "42px");
});

test("body font is DM Sans and h1 is Manrope", async ({ page }) => {
  const external: string[] = [];
  page.on("request", (r) => {
    const host = new URL(r.url()).hostname;
    if (host === "fonts.googleapis.com" || host === "fonts.gstatic.com" || host === "unpkg.com") external.push(r.url());
  });
  await page.goto("/invoices");
  const h1 = page.getByRole("heading", { level: 1, name: "Invoices" });
  await expect(h1).toBeVisible();
  const font = (sel: string) => page.locator(sel).first().evaluate((el) => getComputedStyle(el).fontFamily);
  expect(await font("body")).toMatch(/^"DM Sans"/);
  expect(await h1.evaluate((el) => getComputedStyle(el).fontFamily)).toMatch(/^"?Manrope/);
  // The bundled faces load from the app's own origin. fonts.load() resolves to
  // the faces it loaded; empty means no such face is registered (check() would
  // pass then too).
  const loaded = await page.evaluate(async () => {
    const faces = async (spec: string) =>
      (await document.fonts.load(spec)).map((f) => `${f.family} ${f.weight} ${f.status}`);
    return { dmSans: await faces('500 16px "DM Sans"'), manrope: await faces("500 16px Manrope") };
  });
  expect(loaded.dmSans.length, "DM Sans 500 faces").toBeGreaterThan(0);
  expect(loaded.manrope.length, "Manrope 500 faces").toBeGreaterThan(0);
  expect([...loaded.dmSans, ...loaded.manrope].every((f) => f.endsWith("loaded"))).toBe(true);
  expect(external).toEqual([]);
});

test("cards and tables are square with a line border", async ({ page }) => {
  await page.goto("/invoices");
  const wrapper = page.locator(".p-datatable-table-container").first();
  await expect(wrapper).toBeVisible();
  await expect(wrapper).toHaveCSS("border-radius", "0px");
  await expect(wrapper).toHaveCSS("border-top-color", "rgb(225, 215, 200)");
});

// Every status the API defines (backend/openapi.json enums, the Go status
// constants for payments, recurring templates and invoices) plus the ones the
// frontend derives (Overdue, Active/Inactive).
const API_STATUSES = {
  invoice: ["DRAFT", "OPEN", "SENT", "PARTIAL", "PAID", "VOID", "OVERDUE"],
  quotation: ["DRAFT", "SENT", "ACCEPTED", "DECLINED", "EXPIRED", "INVOICED", "VOID"],
  bill: ["DRAFT", "OPEN", "PARTIAL", "PAID", "VOID"],
  billPayment: ["CLEARED", "REVERSED"],
  project: ["ACTIVE", "ON_HOLD", "COMPLETED", "CANCELLED"],
  payment: ["CLEARED", "PENDING_MANUAL_REVIEW", "FLAGGED", "REVERSED"],
  recurringTemplate: ["ACTIVE", "PAUSED", "ENDED", "MISCONFIGURED"],
  hosting: ["ACTIVE", "SUSPEND_PENDING", "SUSPENDED", "CANCELLED"],
  record: ["ACTIVE", "INACTIVE"],
};
// The spec maps Draft/Void/Inactive to neutral; ruling R7 adds the finished states.
const NEUTRAL = new Set(["DRAFT", "VOID", "INACTIVE", "CANCELLED", "REVERSED", "ENDED"]);

test("every status gets a defined tone", () => {
  for (const [kind, statuses] of Object.entries(API_STATUSES)) {
    for (const s of statuses) {
      const tone = statusTone(s);
      if (NEUTRAL.has(s)) expect(tone, `${kind} ${s}`).toBe("neutral");
      else expect(tone, `${kind} ${s}`).not.toBe("neutral");
    }
  }
  // The spec's named examples, in any spelling the UI passes.
  expect(statusTone("Paid")).toBe("green");
  expect(statusTone("Sent")).toBe("blue");
  expect(statusTone("Overdue")).toBe("red");
  expect(statusTone("Partially paid")).toBe("amber");
  expect(statusTone("Pending manual review")).toBe("amber");
  expect(statusTone("Awaiting finalization")).toBe("amber");
  expect(statusTone("Inactive")).toBe("neutral");
});

/**
 * A Chart.js setting of the chart drawn on `canvas`. PrimeVue's Chart keeps the
 * Chart.js instance on its component (`chart`); the dev server exposes the
 * component on the canvas element as __vueParentComponent.
 */
async function chartValue(canvas: Locator, pick: string): Promise<unknown> {
  return canvas.evaluate((el, path) => {
    const inst = (el as unknown as { __vueParentComponent?: { proxy?: { chart?: unknown } } })
      .__vueParentComponent;
    let v: unknown = inst?.proxy?.chart;
    for (const key of path.split(".")) v = (v as Record<string, unknown> | undefined)?.[key];
    return v ?? null;
  }, pick);
}

test("dashboard charts use brand colours and follow theme", async ({ page }) => {
  await page.goto("/dashboard");
  await page.evaluate(() => localStorage.setItem("wb:theme", "light"));
  await page.reload();
  const cssVar = (name: string) =>
    page.evaluate((n) => getComputedStyle(document.documentElement).getPropertyValue(n).trim(), name);

  const revenue = page.getByTestId("revenue-chart").locator("canvas");
  await expect(revenue).toBeVisible();
  // Light: the first series (the base currency, at the bottom of each bar) is the brand red.
  await expect.poll(() => chartValue(revenue, "data.datasets.0.backgroundColor")).toBe("#a51c30");
  expect(await cssVar("--wb-red")).toBe("#a51c30");

  await page.getByRole("button", { name: "Switch to dark theme" }).click();
  await expect(page.locator("html")).toHaveClass(/(^|\s)app-dark(\s|$)/);
  const darkInk = await cssVar("--wb-ink");
  expect(darkInk).toBe("#fcfbf6");
  await expect.poll(() => chartValue(revenue, "options.scales.y.ticks.color")).toBe(darkInk);
  await expect.poll(() => chartValue(revenue, "data.datasets.0.backgroundColor")).toBe(await cssVar("--wb-red"));
});

test("no PrimeIcons font is loaded", async ({ page }) => {
  const requested: string[] = [];
  page.on("request", (r) => {
    if (r.url().includes("primeicons")) requested.push(r.url());
  });
  for (const path of ["/dashboard", "/invoices", "/invoices/new"]) {
    await page.goto(path);
    await expect(page.locator("main#main-content")).toBeVisible();
    await page.waitForLoadState("networkidle");
    expect(await page.locator(".pi").count(), `.pi elements on ${path}`).toBe(0);
  }
  expect(requested).toEqual([]);
});

// The seven designed list screens (spec "Screens"): the shared page header
// (eyebrow + 40px Manrope H1) and the 11px uppercase table header.
const LIST_SCREENS = ["/customers", "/invoices", "/chart-of-accounts", "/reports", "/fx-rates", "/audit-log", "/users"];

test("list pages use the page header", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  for (const path of LIST_SCREENS) {
    await page.goto(path);
    const main = page.locator("main#main-content");
    const h1 = main.getByRole("heading", { level: 1 });
    await expect(h1, path).toBeVisible();
    await expect(h1, path).toHaveCSS("font-size", "40px");
    await expect(h1, path).toHaveCSS("font-weight", "500");
    const eyebrow = main.locator(".page-eyebrow");
    await expect(eyebrow, path).toBeVisible();
    await expect(eyebrow, path).toHaveCSS("text-transform", "uppercase");
    await expect(eyebrow, path).toHaveCSS("font-size", "12px");
    await expect(eyebrow, path).toHaveCSS("color", "rgb(165, 28, 48)");
    const th = main.locator("thead th:visible").first();
    await expect(th, path).toBeVisible();
    await expect(th, path).toHaveCSS("font-size", "11px");
    await expect(th, path).toHaveCSS("text-transform", "uppercase");
  }
});

test("table dates stay on one line", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto("/invoices?tab=all");
  const main = page.locator("main#main-content");
  await expect(main.locator("tbody tr").first()).toBeVisible();
  const headers = (await main.locator("thead th").allInnerTexts()).map((t) => t.trim().toLowerCase());
  const col = headers.indexOf("issued");
  expect(col, "Issued column").toBeGreaterThanOrEqual(0);
  const cell = main.locator("tbody tr").first().locator("td").nth(col);
  await expect(cell).toHaveText(/^\d{4}-\d{2}-\d{2}/);
  const { height, line } = await cell.evaluate((td) => {
    const range = document.createRange();
    range.selectNodeContents(td);
    const style = getComputedStyle(td);
    const lh = parseFloat(style.lineHeight);
    return { height: range.getBoundingClientRect().height, line: Number.isFinite(lh) ? lh : parseFloat(style.fontSize) * 1.6 };
  });
  expect(height, "date cell text height").toBeLessThan(line * 1.5);
});

test("empty state centres its icon", async ({ page }) => {
  await page.goto("/audit-log");
  const main = page.locator("main#main-content");
  await main.getByPlaceholder("Search payer name…").fill(`no-such-payer-${Date.now()}`);
  const empty = main.locator(".empty-state");
  await expect(empty).toBeVisible();
  const box = await empty.boundingBox();
  const icon = await empty.locator(".app-icon").first().boundingBox();
  expect(box && icon).toBeTruthy();
  const centre = (b: { x: number; width: number }) => b.x + b.width / 2;
  expect(Math.abs(centre(icon!) - centre(box!))).toBeLessThan(2);
});

// ---- every route: brand chrome (task 10) ----

/**
 * Every element in `scope` whose computed corner radius breaks the system
 * (spec "Shapes"): cards, panels, tables, fields, dialogs, menus and tags are
 * square or 2px. Exempt (ruling R4): buttons (30px pills), anything at 50%
 * (avatars, dots), and PrimeVue checkbox/radio/toggle/spinner internals.
 */
async function roundedOffenders(scope: Locator): Promise<string[]> {
  return scope.evaluate((root) => {
    const out: string[] = [];
    const exempt = ".p-checkbox, .p-radiobutton, .p-toggleswitch, .p-progressspinner, .p-avatar";
    const ok = (c: string) => c.split(" ").every((v) => parseFloat(v) <= 2);
    for (const el of [root, ...Array.from(root.querySelectorAll("*"))] as HTMLElement[]) {
      const r = el.getBoundingClientRect();
      if (r.width === 0 || r.height === 0) continue;
      const cs = getComputedStyle(el);
      if (cs.visibility === "hidden") continue;
      const corners = [cs.borderTopLeftRadius, cs.borderTopRightRadius, cs.borderBottomLeftRadius, cs.borderBottomRightRadius];
      if (corners.every(ok)) continue;
      if (corners.every((c) => c === "50%")) continue;
      if (el.closest(exempt)) continue;
      if (el.closest(".p-button, button") && corners.every((c) => c === "30px")) continue;
      const cls = typeof el.className === "string" ? el.className.trim().split(/\s+/).slice(0, 3).join(".") : "";
      out.push(`${el.tagName.toLowerCase()}${cls ? "." + cls : ""} radius=${cs.borderRadius}`);
    }
    return [...new Set(out)];
  });
}

/** The first `key` value found anywhere in a JSON response body. */
function firstValue(body: unknown, key: string): string | undefined {
  if (Array.isArray(body)) {
    for (const v of body) {
      const hit = firstValue(v, key);
      if (hit) return hit;
    }
  } else if (body && typeof body === "object") {
    const o = body as Record<string, unknown>;
    if (typeof o[key] === "string" || typeof o[key] === "number") return String(o[key]);
    for (const v of Object.values(o)) {
      const hit = firstValue(v, key);
      if (hit) return hit;
    }
  }
  return undefined;
}

async function getFirst(api: APIRequestContext, path: string, key: string): Promise<string | undefined> {
  const res = await api.get(`/api/v1${path}`);
  expect(res.ok(), `GET ${path}`).toBe(true);
  return firstValue(await res.json(), key);
}

const BRAND_LINE = [{ description: "E2E brand line", quantity: "1", unit_price: "100000" }];
async function brandCustomer(api: APIRequestContext) {
  const c = await apiPost<{ customer_id: string }>(api, "/customers", { name: `E2E Brand Customer ${uniq()}`, default_currency: "IDR" });
  return c.customer_id;
}
async function brandVendor(api: APIRequestContext) {
  const v = await apiPost<{ vendor_id: string }>(api, "/vendors", { name: `E2E Brand Vendor ${uniq()}`, default_currency: "IDR" });
  return v.vendor_id;
}
async function brandQuotation(api: APIRequestContext) {
  const q = await apiPost<{ quotation_id: string }>(api, "/quotations", { customer_id: await brandCustomer(api), currency: "IDR", line_items: BRAND_LINE });
  return q.quotation_id;
}
async function brandItem(api: APIRequestContext) {
  const i = await apiPost<{ item_id: string }>(api, "/items", { name: `E2E Brand Item ${uniq()}`, item_type: "SERVICE", default_currency: "IDR" });
  return i.item_id;
}
async function brandProject(api: APIRequestContext) {
  const p = await apiPost<{ project_id: string }>(api, "/projects", { customer_id: await brandCustomer(api), code: `E2E-B${uniq()}`, name: "E2E Brand Project", currency: "IDR" });
  return p.project_id;
}

/** Entity detail routes: each resolves its path from a freshly seeded (or the first existing) record. */
const DETAIL_ROUTES: Record<string, (api: APIRequestContext) => Promise<string | undefined>> = {
  "/customers/:id": async (api) => `/customers/${await brandCustomer(api)}`,
  "/invoices/:id/edit": async (api) => {
    const inv = await apiPost<{ invoice_id: string }>(api, "/invoices", { customer_id: await brandCustomer(api), currency: "IDR", line_items: BRAND_LINE });
    return `/invoices/${inv.invoice_id}/edit`;
  },
  "/quotations/:id": async (api) => `/quotations/${await brandQuotation(api)}`,
  "/quotations/:id/edit": async (api) => `/quotations/${await brandQuotation(api)}/edit`,
  "/bills/:id/edit": async (api) => {
    const b = await apiPost<{ bill_id: string }>(api, "/bills", { vendor_id: await brandVendor(api), currency: "IDR", line_items: BRAND_LINE });
    return `/bills/${b.bill_id}/edit`;
  },
  "/vendors/:id": async (api) => `/vendors/${await brandVendor(api)}`,
  "/vendors/:id/edit": async (api) => `/vendors/${await brandVendor(api)}/edit`,
  "/items/:id": async (api) => `/items/${await brandItem(api)}`,
  "/items/:id/edit": async (api) => `/items/${await brandItem(api)}/edit`,
  "/projects/:id": async (api) => `/projects/${await brandProject(api)}`,
  "/projects/:id/edit": async (api) => `/projects/${await brandProject(api)}/edit`,
  "/chart-of-accounts/:id": async (api) => {
    const id = await getFirst(api, "/accounting/chart-of-accounts", "account_id");
    return id && `/chart-of-accounts/${id}`;
  },
  "/users/:id/reset-password": async (api) => {
    const id = await getFirst(api, "/users", "user_id");
    return id && `/users/${id}/reset-password`;
  },
  "/invoices/recurring/:id/edit": async (api) => {
    const customerName = `E2E Brand Customer ${uniq()}`;
    const c = await apiPost<{ customer_id: string }>(api, "/customers", { name: customerName, default_currency: "IDR" });
    await apiPost(api, "/invoices/recurring-template", {
      customer_id: c.customer_id,
      currency: "IDR",
      payment_terms: "",
      schedule: { frequency: "MONTHLY", start_date: "2027-01-01" },
      line_items: BRAND_LINE,
    });
    const rows = (await (await api.get("/api/v1/invoices/recurring-templates/rows")).json()) as { template_id: string; customer_name: string | null }[];
    const id = rows.find((r) => r.customer_name === customerName)?.template_id;
    return id && `/invoices/recurring/${id}/edit`;
  },
};

test.describe("every route uses brand chrome", () => {
  test.use({ viewport: { width: 1440, height: 900 } });
  const cases: [string, (api: APIRequestContext) => Promise<string | undefined>][] = [
    ...ROUTES.map((p): [string, () => Promise<string>] => [p, async () => p]),
    ...Object.entries(DETAIL_ROUTES),
  ];
  for (const [name, resolve] of cases) {
    test(name, async ({ page }) => {
      const path = await resolve(page.request);
      expect(path, `a record to open for ${name}`).toBeTruthy();
      await page.goto(path!);
      const main = page.locator("main#main-content");
      const h1 = main.getByRole("heading", { level: 1 }).first();
      await expect(h1, path).toBeVisible();
      await page.waitForLoadState("networkidle");
      expect.soft(await h1.evaluate((el) => getComputedStyle(el).fontFamily), `${path} h1 font`).toMatch(/^"?Manrope/);
      expect.soft(await main.locator(".page-eyebrow").count(), `${path} eyebrow`).toBeGreaterThan(0);
      expect.soft(await roundedOffenders(main), `${path} rounded elements`).toEqual([]);
    });
  }
});

test.describe("auth pages use brand chrome", () => {
  test.use({ storageState: { cookies: [], origins: [] }, viewport: { width: 1440, height: 900 } });
  for (const path of ["/login", "/forgot-password", "/reset-password"]) {
    test(path, async ({ page }) => {
      await page.goto(path);
      const card = page.locator(".auth-card");
      await expect(card).toBeVisible();
      await expect(card).toHaveCSS("border-radius", "0px");
      await expect(card).toHaveCSS("border-top-color", "rgb(225, 215, 200)");
      await expect(card).toHaveCSS("border-top-width", "1px");
      expect(await card.locator("h1").evaluate((el) => getComputedStyle(el).fontFamily)).toMatch(/^"?Manrope/);
      const submit = card.locator('button[type="submit"]');
      if ((await submit.count()) > 0) {
        await expect(submit.first()).toHaveCSS("background-color", "rgb(165, 28, 48)");
        await expect(submit.first()).toHaveCSS("border-radius", "30px");
        await expect(submit.first()).toHaveCSS("box-shadow", "none");
      }
      expect(await roundedOffenders(page.locator("body")), `${path} rounded elements`).toEqual([]);
    });
  }
});
