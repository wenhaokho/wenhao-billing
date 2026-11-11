import { expect, test } from "@playwright/test";
import { collectConsoleErrors, isApi, openRecordPayment, pickDate } from "./helpers";

// The business runs on Jakarta time (UTC+7). A date picked in a DatePicker is
// local midnight, so converting it with toISOString() gives the previous UTC
// day. Every date the app sends must be the calendar day the user picked (or,
// for "today" defaults, today's local date).
test.use({ timezoneId: "Asia/Jakarta" });

test("record payment sends the picked payment date", async ({ page }) => {
  const errors = collectConsoleErrors(page);
  const dialog = await openRecordPayment(page);
  await dialog.getByRole("button", { name: "Full balance" }).click();
  // The test customer has no email: don't send a receipt (it would block submit).
  await dialog.locator('[data-pc-name="checkbox"] input').uncheck();
  await pickDate(page, dialog.locator("label", { hasText: "Payment date" }).locator("input"), "2026-11-05");
  const sent = page.waitForRequest((r) => r.method() === "POST" && /\/api\/v1\/.*payment/.test(r.url()));
  await dialog.getByRole("button", { name: "Record payment" }).click();
  expect((await sent).postDataJSON()).toMatchObject({ payment_date: "2026-11-05" });
  expect(errors()).toEqual([]);
});

test("audit log date filters send the picked days", async ({ page }) => {
  const errors = collectConsoleErrors(page);
  const path = "/payments/audit-log";
  await page.goto("/audit-log");
  await expect(page.locator("main").getByRole("heading", { name: "Audit Log" })).toBeVisible();
  await page.waitForLoadState("networkidle");
  const filters = page.locator(".filters");

  const from = page.waitForRequest((r) => isApi(r, "GET", path) && new URL(r.url()).searchParams.has("from_date"));
  await pickDate(page, filters.locator("label", { hasText: "From" }).locator("input"), "2026-11-01");
  expect(new URL((await from).url()).searchParams.get("from_date")).toBe("2026-11-01");

  const to = page.waitForRequest((r) => isApi(r, "GET", path) && new URL(r.url()).searchParams.has("to_date"));
  await pickDate(page, filters.locator("label", { hasText: "To" }).locator("input"), "2026-11-05");
  expect(new URL((await to).url()).searchParams.get("to_date")).toBe("2026-11-05");
  expect(errors()).toEqual([]);
});

test("reports default to today's local date", async ({ page }) => {
  // 06:00 on 12 Nov in Jakarta is still 11 Nov in UTC.
  await page.clock.setFixedTime(new Date("2026-11-11T23:00:00Z"));
  const asOf = page.waitForRequest((r) => isApi(r, "GET", "/accounting/reports/trial-balance"));
  await page.goto("/reports");
  expect(new URL((await asOf).url()).searchParams.get("as_of")).toBe("2026-11-12");
});
