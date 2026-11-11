import { test, expect, type APIRequestContext, type Locator } from "@playwright/test";
import { statusTone } from "../src/theme/status";
import { ROUTES, apiPost, openRecordPayment, uniq } from "./helpers";

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
  // Ruling R11: held = waiting on a person, so amber like Manual Review.
  expect(statusTone("HELD")).toBe("amber");
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
 * (spec "Shapes", ruling R4). Only R4's categories are checked: cards, panels,
 * tables, fields (inputs, selects, date pickers, textareas), dialogs, menus
 * and tags must be square or 2px; PrimeVue buttons must be 30px pills.
 * Exempt: anything at 50% (avatars, dots, round icon buttons), PrimeVue
 * checkbox/radio/toggle/spinner/scrollbar internals, and anything inside an
 * element marked [data-radius-exempt].
 */
async function roundedOffenders(scope: Locator): Promise<string[]> {
  return scope.evaluate((root) => {
    const square = [
      // cards and panels
      ".card", ".p-card", ".p-panel", ".p-fieldset", ".p-accordion", ".p-tabs", ".p-message", ".p-toast-message",
      // tables
      "table", ".p-datatable", ".p-datatable-table-container", ".p-treetable",
      // fields
      "input:not([type=checkbox]):not([type=radio])", "select", "textarea", ".p-inputtext", ".p-select",
      ".p-multiselect", ".p-autocomplete", ".p-inputnumber", ".p-datepicker", ".p-datepicker-panel", ".p-textarea",
      ".p-password", ".p-inputgroup",
      // dialogs
      ".p-dialog", "[role=dialog]", "[role=alertdialog]",
      // menus and overlay panels
      ".p-menu", ".p-tieredmenu", ".p-contextmenu", ".p-menubar", "[role=menu]", ".p-select-overlay",
      ".p-multiselect-overlay", ".p-autocomplete-overlay", ".p-popover",
      // tags
      ".p-tag", ".p-chip", ".p-badge",
    ].join(", ");
    const exempt =
      "[data-radius-exempt], .p-checkbox, .p-radiobutton, .p-toggleswitch, .p-progressspinner, .p-avatar, .p-scrollpanel-bar";
    const ok = (c: string) => c.split(" ").every((v) => parseFloat(v) <= 2);
    const out: string[] = [];
    // A button joined to a field (the date picker's calendar button, an input
    // group addon) takes the field's shape, not the pill.
    const field = ".p-datepicker, .p-inputgroup, .p-inputnumber, .p-select, .p-autocomplete";
    for (const el of [root, ...Array.from(root.querySelectorAll("*"))] as HTMLElement[]) {
      const isButton = el.matches(".p-button") && !el.parentElement?.closest(field);
      if (!isButton && !el.matches(square) && !(el.matches(".p-button") && el.parentElement?.closest(field))) continue;
      const r = el.getBoundingClientRect();
      if (r.width === 0 || r.height === 0) continue;
      const cs = getComputedStyle(el);
      if (cs.visibility === "hidden") continue;
      if (el.closest(exempt)) continue;
      const corners = [cs.borderTopLeftRadius, cs.borderTopRightRadius, cs.borderBottomLeftRadius, cs.borderBottomRightRadius];
      if (corners.every((c) => c === "50%")) continue;
      if (isButton ? corners.every((c) => c === "30px") : corners.every(ok)) continue;
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
      // Each auth page has exactly one submit: the red pill.
      const submit = card.locator('button[type="submit"]');
      await expect(submit, `${path} submit button`).toHaveCount(1);
      await expect(submit).toHaveCSS("background-color", "rgb(165, 28, 48)");
      await expect(submit).toHaveCSS("border-radius", "30px");
      await expect(submit).toHaveCSS("box-shadow", "none");
      expect(await roundedOffenders(page.locator("body")), `${path} rounded elements`).toEqual([]);
    });
  }
});

test.describe("overlays are square with the overlay shadow", () => {
  test.use({ viewport: { width: 1440, height: 900 } });
  const shadowed = async (el: Locator) => {
    const shadow = await el.evaluate((n) => getComputedStyle(n).boxShadow);
    expect(shadow, "overlay shadow").not.toBe("none");
    // --wb-overlay-shadow: two layers, the first 0 8px 24px.
    expect(shadow, "overlay shadow").toMatch(/0px 8px 24px/);
  };

  test("Record payment dialog", async ({ page }) => {
    const dialog = await openRecordPayment(page);
    await expect(dialog).toHaveCSS("border-radius", "0px");
    await shadowed(dialog);
    expect(await roundedOffenders(dialog), "rounded elements in the dialog").toEqual([]);
  });

  test("user menu", async ({ page }) => {
    await page.goto("/dashboard");
    await page.getByRole("button", { name: "Open account menu" }).click();
    const menu = page.getByRole("menu");
    await expect(menu).toBeVisible();
    // The popup Menu's root (data-pc-name="menu") holds the role="menu" list.
    const panel = menu.locator('xpath=ancestor-or-self::*[@data-pc-name="menu"][1]');
    await expect(panel).toHaveCSS("border-radius", "0px");
    await shadowed(panel);
    expect(await roundedOffenders(panel), "rounded elements in the menu").toEqual([]);
  });
});

// Ruling R10: a destructive (danger) button must not look like a routine
// primary one, in either theme: primary is a filled pill, danger is outlined
// (no fill, a line in its own text colour), its text at 4.5:1 or better on the
// dialog surface.
test("danger button differs from the primary button, light and dark", async ({ page }) => {
  const name = `E2E Danger Customer ${uniq()}`;
  await apiPost(page.request, "/customers", { name, default_currency: "IDR" });
  await page.goto("/customers");
  const style = (el: Locator) =>
    el.evaluate((n) => {
      const cs = getComputedStyle(n);
      return { bg: cs.backgroundColor, border: cs.borderTopColor, color: cs.color };
    });
  const lum = (rgb: string) => {
    const [r, g, b] = rgb.match(/\d+(\.\d+)?/g)!.slice(0, 3).map(Number);
    const lin = (c: number) => ((c /= 255) <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4);
    return 0.2126 * lin(r) + 0.7152 * lin(g) + 0.0722 * lin(b);
  };
  const contrast = (a: string, b: string) => {
    const [hi, lo] = [lum(a), lum(b)].sort((x, y) => y - x);
    return (hi + 0.05) / (lo + 0.05);
  };
  for (const theme of ["light", "dark"] as const) {
    if (theme === "dark") await page.getByRole("button", { name: "Switch to dark theme" }).click();
    const primary = page.getByRole("button", { name: "New customer" });
    await expect(primary).toBeVisible();
    await page.locator("tbody tr", { hasText: name }).getByTitle("Deactivate").click();
    const dialog = page.getByRole("alertdialog", { name: "Deactivate customer" });
    await expect(dialog).toBeVisible();
    const danger = dialog.getByRole("button", { name: "Deactivate" });
    await expect(danger).toHaveClass(/p-button-danger/);
    const p = await style(primary);
    const d = await style(danger);
    const alpha = (c: string) => {
      const m = c.match(/^rgba\(.*,\s*([\d.]+)\)$/);
      return m ? Number(m[1]) : c === "transparent" ? 0 : 1;
    };
    expect(alpha(p.bg), `${theme}: primary is filled (${p.bg})`).toBe(1);
    expect(alpha(d.bg), `${theme}: danger has no fill (${d.bg})`).toBe(0);
    expect(d.border, `${theme}: danger is outlined in its text colour`).toBe(d.color);
    expect(d.color, `${theme}: danger text is not the primary's white`).not.toBe(p.color);
    const surface = await dialog.evaluate((n) => getComputedStyle(n).backgroundColor);
    expect(contrast(d.color, surface), `${theme}: danger text on ${surface}`).toBeGreaterThanOrEqual(4.5);
    // Cancel is the quiet secondary pill, not a filled primary competing with the action.
    const cancel = await style(dialog.getByRole("button", { name: "Cancel" }));
    expect(alpha(cancel.bg), `${theme}: Cancel has no fill (${cancel.bg})`).toBe(0);
    expect(cancel.border, `${theme}: Cancel is not red-edged`).not.toBe(d.border);
    await page.keyboard.press("Escape");
    await expect(dialog).toBeHidden();
  }
});

test("recurring template row actions stay on one line at 1440", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  const customerName = `E2E Recurring Actions ${uniq()}`;
  const c = await apiPost<{ customer_id: string }>(page.request, "/customers", { name: customerName, default_currency: "IDR" });
  await apiPost(page.request, "/invoices/recurring-template", {
    customer_id: c.customer_id,
    currency: "IDR",
    payment_terms: "",
    schedule: { frequency: "MONTHLY", start_date: "2027-01-01" },
    line_items: BRAND_LINE,
  });
  await page.goto("/invoices/recurring");
  const row = page.locator("tbody tr", { hasText: customerName });
  await expect(row).toBeVisible();
  const actions = row.locator(".row-actions");
  // An active template shows all three: Edit, Pause, End now.
  await expect(actions.getByRole("button")).toHaveCount(3);
  const fit = await actions.evaluate((el) => {
    const cell = el.closest("td")!;
    const tops = Array.from(el.querySelectorAll("button")).map((b) => Math.round(b.getBoundingClientRect().top));
    const cellBox = cell.getBoundingClientRect();
    const box = el.getBoundingClientRect();
    return { tops, overflow: el.scrollWidth - el.clientWidth, left: box.left - cellBox.left, right: cellBox.right - box.right };
  });
  expect(new Set(fit.tops).size, `button tops ${fit.tops}`).toBe(1);
  expect(fit.overflow, "actions overflow").toBeLessThanOrEqual(0);
  expect(fit.left, "actions inside the cell (left)").toBeGreaterThanOrEqual(0);
  expect(fit.right, "actions inside the cell (right)").toBeGreaterThanOrEqual(0);
});

test("account page names the real password hash", async ({ page }) => {
  await page.goto("/account");
  await page.getByRole("tab", { name: "Password & Session" }).click();
  const main = page.locator("main#main-content");
  await expect(main.getByText("Auth mechanism")).toBeVisible();
  await expect(main).toContainText("argon2id");
  await expect(main).not.toContainText(/bcrypt/i);
});

test("dashboard charts are named images under the page heading", async ({ page }) => {
  await page.goto("/dashboard");
  const main = page.locator("main#main-content");
  await expect(main.getByRole("heading", { level: 1, name: "Dashboard" })).toBeVisible();
  // The page H1 is the first heading on the page: the topbar title is not one.
  const levels = await page.evaluate(() =>
    Array.from(document.querySelectorAll("h1, h2, h3, h4, h5, h6, [role=heading]")).map((h) => h.tagName.toLowerCase()),
  );
  expect(levels[0], `heading order ${levels.join(",")}`).toBe("h1");
  expect(levels.filter((l) => l === "h1")).toHaveLength(1);
  await expect(page.locator("header.topbar").getByRole("heading")).toHaveCount(0);
  await expect(main.getByRole("heading", { level: 2, name: "Receivables aging" })).toBeVisible();
  // Each chart (the revenue canvas, the reconciliation SVG) is an image with a
  // short description. The revenue chart must be there (the dev database has
  // IDR revenue); the donut only when the period had payments.
  const revenue = main.getByTestId("revenue-chart").locator("canvas");
  await expect(revenue).toHaveAttribute("role", "img");
  await expect(revenue).toHaveAttribute("aria-label", /.{20,}/);
  const donut = main.getByTestId("recon-chart").locator("svg");
  if ((await donut.count()) > 0) {
    await expect(donut).toHaveAttribute("role", "img");
    await expect(donut).toHaveAttribute("aria-label", /.{20,}/);
  }
});

// WCAG 2.4.7: the keyboard focus ring must be visible on every control. The
// global ring is inset 1px (spec) and sits on the control's own border, which
// vanishes on filled buttons and checked toggles, so those take an outside ring.
test("keyboard focus ring is visible on buttons, checkbox and toggle, light and dark", async ({ page }) => {
  const name = `E2E Focus Customer ${uniq()}`;
  await apiPost(page.request, "/customers", { name, default_currency: "IDR" });
  const ring = (el: Locator) =>
    el.evaluate((n) => {
      const cs = getComputedStyle(n);
      return { style: cs.outlineStyle, width: parseFloat(cs.outlineWidth), offset: parseFloat(cs.outlineOffset), color: cs.outlineColor };
    });
  const expectOutside = async (el: Locator, what: string) => {
    await expect(el, what).toBeVisible();
    const r = await ring(el);
    expect(r.style, `${what}: outline style (${JSON.stringify(r)})`).not.toBe("none");
    expect(r.width, `${what}: outline width`).toBeGreaterThanOrEqual(2);
    expect(r.offset, `${what}: outline offset`).toBeGreaterThan(0);
  };
  for (const theme of ["light", "dark"] as const) {
    await page.goto("/invoices");
    if (theme === "dark") await page.getByRole("button", { name: "Switch to dark theme" }).click();
    // A key press first so :focus-visible applies to the focus() below.
    await page.keyboard.press("Tab");
    const primary = page.getByRole("button", { name: "New invoice" });
    await primary.focus();
    await expectOutside(primary, `${theme}: primary button`);

    await page.goto("/customers");
    await page.locator("tbody tr", { hasText: name }).getByTitle("Deactivate").click();
    const dialog = page.getByRole("alertdialog", { name: "Deactivate customer" });
    await expect(dialog).toBeVisible();
    await page.keyboard.press("Tab");
    const danger = dialog.getByRole("button", { name: "Deactivate" });
    await danger.focus();
    await expectOutside(danger, `${theme}: danger button`);
    await page.keyboard.press("Escape");
    await expect(dialog).toBeHidden();

    const toggle = page.getByRole("switch");
    await toggle.check();
    await page.keyboard.press("Tab");
    await toggle.focus();
    await expectOutside(page.locator(".p-toggleswitch-slider").first(), `${theme}: checked toggle switch`);

    await page.goto("/items/new");
    await page.keyboard.press("Tab");
    const check = page.getByRole("checkbox", { name: "I sell this item" });
    await expect(check).toBeChecked();
    await check.focus();
    await expectOutside(page.locator(".p-checkbox-box").first(), `${theme}: checked checkbox`);
  }
});

// Ruling R12: "Change password" in the user menu opens the Security tab.
test("user menu Change password opens the security tab with the password form", async ({ page }) => {
  await page.goto("/dashboard");
  await page.getByRole("button", { name: "Open account menu" }).click();
  await page.getByRole("menuitem", { name: "Change password" }).click();
  await expect(page).toHaveURL(/\/account\?tab=security/);
  await expect(page.getByRole("tab", { name: "Password & Session" })).toHaveAttribute("aria-selected", "true");
  await expect(page.locator("main#main-content").locator("input[type=password]").first()).toBeVisible();
});
