import { test, expect, type APIRequestContext, type Page } from "@playwright/test";
import { apiPost as post, collectConsoleErrors, isApi, rowStatuses, uniq } from "./helpers";

function tab(page: Page, name: string) {
  return page.getByRole("tab", { name, exact: true });
}

const LINE = [{ description: "E2E tabs line", quantity: "1", unit_price: "100000" }];

async function newCustomer(api: APIRequestContext) {
  return post<{ customer_id: string }>(api, "/customers", { name: `E2E Tabs Customer ${uniq()}`, default_currency: "IDR" });
}

async function ok(res: Awaited<ReturnType<APIRequestContext["post"]>>) {
  expect(res.ok(), `${res.url()}: ${res.status()} ${await res.text()}`).toBe(true);
}

// ---- views that filter server-side: the tab sets ?status=... on the list request ----

const SERVER_TABS: {
  view: string;
  path: string;
  api: string;
  tabs: [string, string[] | null][];
  /** Makes sure every status-filtered tab has at least one row. */
  seed: (api: APIRequestContext) => Promise<void>;
}[] = [
  {
    view: "Invoices",
    path: "/invoices",
    api: "/invoices",
    tabs: [["Unpaid", ["OPEN", "SENT", "PARTIAL"]], ["Draft", ["DRAFT"]], ["All", null]],
    seed: async (api) => {
      const c = await newCustomer(api);
      const body = { customer_id: c.customer_id, currency: "IDR", line_items: LINE };
      await post(api, "/invoices", body);
      const open = await post<{ invoice_id: string }>(api, "/invoices", body);
      await ok(await api.post(`/api/v1/invoices/${open.invoice_id}/finalize`));
    },
  },
  {
    view: "Quotations",
    path: "/quotations",
    api: "/quotations",
    tabs: [["Open", ["DRAFT", "SENT"]], ["Accepted", ["ACCEPTED", "INVOICED"]], ["All", null]],
    seed: async (api) => {
      const c = await newCustomer(api);
      const body = { customer_id: c.customer_id, currency: "IDR", line_items: LINE };
      await post(api, "/quotations", body);
      const accepted = await post<{ quotation_id: string }>(api, "/quotations", body);
      await ok(await api.post(`/api/v1/quotations/${accepted.quotation_id}/accept`, { data: {} }));
    },
  },
  {
    view: "Bills",
    path: "/bills",
    api: "/bills",
    tabs: [["Unpaid", ["OPEN", "PARTIAL"]], ["Draft", ["DRAFT"]], ["All", null]],
    seed: async (api) => {
      const v = await post<{ vendor_id: string }>(api, "/vendors", { name: `E2E Tabs Vendor ${uniq()}`, default_currency: "IDR" });
      const body = { vendor_id: v.vendor_id, currency: "IDR", line_items: LINE };
      await post(api, "/bills", body);
      const open = await post<{ bill_id: string }>(api, "/bills", body);
      await ok(await api.patch(`/api/v1/bills/${open.bill_id}`, { data: { status: "OPEN" } }));
    },
  },
  {
    view: "Projects",
    path: "/projects",
    api: "/projects",
    tabs: [["Active", ["ACTIVE"]], ["On hold", ["ON_HOLD"]], ["All", null]],
    seed: async (api) => {
      const c = await newCustomer(api);
      for (const status of ["ACTIVE", "ON_HOLD"]) {
        await post(api, "/projects", { customer_id: c.customer_id, code: `E2E-T${uniq()}`, name: "E2E Tabs Project", currency: "IDR", status });
      }
    },
  },
];

for (const v of SERVER_TABS) {
  test(`${v.view} status tabs filter the list request`, async ({ page }) => {
    await v.seed(page.request);
    const errors = collectConsoleErrors(page);
    await page.goto(v.path);
    await expect(tab(page, v.tabs[0][0])).toHaveAttribute("aria-selected", "true");
    // Visit every tab but the first, then come back to the first.
    const order = [...v.tabs.slice(1), v.tabs[0]];
    for (const [name, statuses] of order) {
      const req = page.waitForRequest((r) => {
        if (!isApi(r, "GET", v.api)) return false;
        const got = new URL(r.url()).searchParams.getAll("status").sort();
        return JSON.stringify(got) === JSON.stringify([...(statuses ?? [])].sort());
      });
      const res = page.waitForResponse((r) => isApi(r.request(), "GET", v.api));
      await tab(page, name).click();
      await req;
      await res;
      await expect(tab(page, name)).toHaveAttribute("aria-selected", "true");
      // The seed guarantees rows on every tab; each chip must belong to the tab.
      await expect
        .poll(async () => {
          const got = await rowStatuses(page);
          return got.length > 0 && got.every((s) => !statuses || statuses.includes(s));
        }, { message: `${v.view} / ${name}: rows present and all of ${statuses ?? "any status"}` })
        .toBe(true);
    }
    expect(errors()).toEqual([]);
  });
}

// ---- Recurring templates: one list request, the tab filters it client-side ----

test("Recurring status tabs filter the rows", async ({ page }) => {
  const api = page.request;
  const customerName = `E2E Tabs Customer ${uniq()}`;
  const c = await post<{ customer_id: string }>(api, "/customers", { name: customerName, default_currency: "IDR" });
  const body = {
    customer_id: c.customer_id,
    currency: "IDR",
    payment_terms: "",
    schedule: { frequency: "MONTHLY", start_date: "2027-01-01" },
    line_items: [{ description: "E2E recurring line", quantity: "1", unit_price: "100000" }],
  };
  // One template stays active, one is paused, one ended.
  for (let i = 0; i < 3; i++) await post(api, "/invoices/recurring-template", body);
  const rows = (await (await api.get("/api/v1/invoices/recurring-templates/rows")).json()) as {
    template_id: string;
    customer_name: string | null;
  }[];
  const mine = rows.filter((r) => r.customer_name === customerName);
  expect(mine).toHaveLength(3);
  for (const [i, action] of [[0, "PAUSE"], [1, "END_NOW"]] as const) {
    const p = await api.patch(`/api/v1/invoices/recurring-templates/${mine[i].template_id}`, { data: { action } });
    expect(p.ok(), await p.text()).toBe(true);
  }

  const errors = collectConsoleErrors(page);
  await page.goto("/invoices/recurring");
  await expect(tab(page, "Active")).toHaveAttribute("aria-selected", "true");
  await expect.poll(async () => (await rowStatuses(page)).length).toBeGreaterThan(0);
  const tabs: [string, string[] | null][] = [
    ["Paused", ["PAUSED", "MISCONFIGURED"]],
    ["Ended", ["ENDED"]],
    ["All", null],
    ["Active", ["ACTIVE"]],
  ];
  for (const [name, statuses] of tabs) {
    await tab(page, name).click();
    await expect(tab(page, name)).toHaveAttribute("aria-selected", "true");
    // Seeded: every tab has rows; each chip must belong to the tab.
    await expect
      .poll(async () => {
        const got = await rowStatuses(page);
        return got.length > 0 && got.every((s) => !statuses || statuses.includes(s));
      }, { message: `Recurring / ${name}: rows present and all of ${statuses ?? "any status"}` })
      .toBe(true);
  }
  await tab(page, "All").click();
  await expect
    .poll(async () => {
      const all = new Set(await rowStatuses(page));
      return ["ACTIVE", "PAUSED", "ENDED"].every((s) => all.has(s));
    }, { message: "Recurring / All shows ACTIVE, PAUSED and ENDED rows" })
    .toBe(true);
  expect(errors()).toEqual([]);
});

// ---- Reports: each tab shows its own report panel ----

test("Reports tabs switch the report panel", async ({ page }) => {
  const errors = collectConsoleErrors(page);
  await page.goto("/reports");
  const names = ["Trial Balance", "Balance Sheet", "FX Revaluation", "AR Aging", "AP Aging"];
  await expect(tab(page, names[0])).toHaveAttribute("aria-selected", "true");
  await page.waitForLoadState("networkidle");
  const panel = page.locator("[role=tabpanel]:visible");
  const seen = new Map<string, string>();
  for (const name of [...names.slice(1), names[0]]) {
    await tab(page, name).click();
    await expect(tab(page, name)).toHaveAttribute("aria-selected", "true");
    await expect(panel).toHaveCount(1);
    const id = await tab(page, name).getAttribute("aria-controls");
    if (id) await expect(panel).toHaveAttribute("id", id);
    seen.set(name, await panel.innerHTML());
  }
  // Five tabs, five different panels.
  expect(new Set(seen.values()).size).toBe(names.length);
  expect(errors()).toEqual([]);
});

// ---- Customers / Vendors: the show-inactive switch ----

for (const kind of ["customers", "vendors"] as const) {
  test(`${kind} show-inactive switch toggles inactive rows`, async ({ page }) => {
    const api = page.request;
    const idKey = kind === "customers" ? "customer_id" : "vendor_id";
    const name = `E2E Inactive ${kind} ${uniq()}`;
    const created = await post<Record<string, string>>(api, `/${kind}`, { name, default_currency: "IDR" });
    const del = await api.delete(`/api/v1/${kind}/${created[idKey]}`);
    expect(del.ok(), await del.text()).toBe(true);

    const errors = collectConsoleErrors(page);
    const first = page.waitForRequest((r) => isApi(r, "GET", `/${kind}`));
    await page.goto(`/${kind}`);
    expect(new URL((await first).url()).searchParams.get("active")).toBe("true");
    await page.waitForLoadState("networkidle");
    await expect(page.getByText(name)).toHaveCount(0);

    const toggle = page.getByRole("switch");
    const on = page.waitForRequest((r) => isApi(r, "GET", `/${kind}`) && !new URL(r.url()).searchParams.has("active"));
    await toggle.click();
    await on;
    await expect(toggle).toBeChecked();
    await expect(page.getByText(name)).toBeVisible();

    const off = page.waitForRequest((r) => isApi(r, "GET", `/${kind}`) && new URL(r.url()).searchParams.get("active") === "true");
    await toggle.click();
    await off;
    await expect(toggle).not.toBeChecked();
    await expect(page.getByText(name)).toHaveCount(0);
    expect(errors()).toEqual([]);
  });
}
