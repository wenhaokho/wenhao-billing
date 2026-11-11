import { test, expect } from "@playwright/test";
import { statusTone } from "../src/theme/status";

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
