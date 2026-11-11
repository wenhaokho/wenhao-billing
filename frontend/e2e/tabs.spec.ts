import { test, expect, type Page } from "@playwright/test";
import { apiPost as post, collectConsoleErrors, isApi, uniq } from "./helpers";

function tab(page: Page, name: string) {
  return page.getByRole("tab", { name, exact: true });
}

/** Status chips (PrimeVue Tag) of the rendered table rows. */
async function rowStatuses(page: Page): Promise<string[]> {
  return page.locator("tbody tr .p-tag").allInnerTexts().then((t) => t.map((s) => s.trim()));
}

// ---- views that filter server-side: the tab sets ?status=... on the list request ----

const SERVER_TABS: { view: string; path: string; api: string; tabs: [string, string[] | null][] }[] = [
  {
    view: "Invoices",
    path: "/invoices",
    api: "/invoices",
    tabs: [["Unpaid", ["OPEN", "SENT", "PARTIAL"]], ["Draft", ["DRAFT"]], ["All", null]],
  },
  {
    view: "Quotations",
    path: "/quotations",
    api: "/quotations",
    tabs: [["Open", ["DRAFT", "SENT"]], ["Accepted", ["ACCEPTED", "INVOICED"]], ["All", null]],
  },
  {
    view: "Bills",
    path: "/bills",
    api: "/bills",
    tabs: [["Unpaid", ["OPEN", "PARTIAL"]], ["Draft", ["DRAFT"]], ["All", null]],
  },
  {
    view: "Projects",
    path: "/projects",
    api: "/projects",
    tabs: [["Active", ["ACTIVE"]], ["On hold", ["ON_HOLD"]], ["All", null]],
  },
];

for (const v of SERVER_TABS) {
  test(`${v.view} status tabs filter the list request`, async ({ page }) => {
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
      if (statuses) {
        await page.waitForLoadState("networkidle");
        for (const s of await rowStatuses(page)) expect(statuses).toContain(s);
      }
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
  await post(api, "/invoices/recurring-template", body);
  await post(api, "/invoices/recurring-template", body);
  const rows = (await (await api.get("/api/v1/invoices/recurring-templates/rows")).json()) as {
    template_id: string;
    customer_name: string | null;
  }[];
  const mine = rows.filter((r) => r.customer_name === customerName);
  expect(mine).toHaveLength(2);
  const p = await api.patch(`/api/v1/invoices/recurring-templates/${mine[0].template_id}`, { data: { action: "PAUSE" } });
  expect(p.ok(), await p.text()).toBe(true);

  const errors = collectConsoleErrors(page);
  await page.goto("/invoices/recurring");
  await expect(tab(page, "Active")).toHaveAttribute("aria-selected", "true");
  await expect(page.locator("tbody tr .p-tag").first()).toBeVisible();
  const tabs: [string, string[] | null][] = [
    ["Paused", ["PAUSED", "MISCONFIGURED"]],
    ["Ended", ["ENDED"]],
    ["All", null],
    ["Active", ["ACTIVE"]],
  ];
  const seen = new Set<string>();
  for (const [name, statuses] of tabs) {
    await tab(page, name).click();
    await expect(tab(page, name)).toHaveAttribute("aria-selected", "true");
    const got = await rowStatuses(page);
    got.forEach((s) => seen.add(s));
    if (statuses) for (const s of got) expect(statuses).toContain(s);
    if (name === "Paused" || name === "Active") expect(got.length).toBeGreaterThan(0);
  }
  expect(seen).toContain("ACTIVE");
  expect(seen).toContain("PAUSED");
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
