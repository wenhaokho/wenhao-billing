import { test, expect, type APIRequestContext } from "@playwright/test";
import { apiPost, isApi, uniq } from "./helpers";

// Sending a payment reminder from the dashboard's Needs attention card. The
// dev stack's email sender only logs, so nothing leaves the machine. The
// 24-hour repeat guard is per customer: every run seeds a fresh customer, so
// earlier runs never block this one.

test.use({ colorScheme: "light" });

/** The line description that marks this spec's invoices, so leftovers can be found. */
const SEED_LINE = "E2E reminders line";

/** YYYY-MM-DD of `days` before today (UTC). */
function isoDaysAgo(days: number): string {
  return new Date(Date.now() - days * 86_400_000).toISOString().slice(0, 10);
}

/** Voids (or, for a draft, deletes) an invoice; true when it worked. */
async function discard(api: APIRequestContext, invoiceId: string): Promise<boolean> {
  const v = await api.post(`/api/v1/invoices/${invoiceId}/void`, { data: { reason: "E2E reminders cleanup" } });
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
 * A finalized invoice for `customerId`, due `daysAgo` days back. The id goes
 * into `created` as soon as it exists, so cleanup gets it even when
 * finalizing fails.
 */
async function seedOverdue(api: APIRequestContext, created: string[], customerId: string, daysAgo: number) {
  const date = isoDaysAgo(daysAgo);
  const inv = await apiPost<{ invoice_id: string }>(api, "/invoices", {
    customer_id: customerId,
    currency: "IDR",
    issue_date: date,
    due_date: date,
    line_items: [{ description: SEED_LINE, quantity: "1", unit_price: "250000" }],
  });
  created.push(inv.invoice_id);
  const fin = await api.post(`/api/v1/invoices/${inv.invoice_id}/finalize`);
  expect(fin.ok(), `finalize: ${fin.status()} ${await fin.text()}`).toBe(true);
  const body = (await fin.json()) as { invoice_number: string | null };
  return { invoiceId: inv.invoice_id, number: body.invoice_number ?? "" };
}

test("send a reminder from the dashboard; a second one asks first", async ({ page }) => {
  const api = page.request;
  await discardLeftovers(api);
  const created: string[] = [];
  try {
    const tag = uniq();
    const name = `E2E Remind ${tag}`;
    const email = `e2e-remind-${tag}@example.com`;
    const c = await apiPost<{ customer_id: string }>(api, "/customers", {
      name,
      default_currency: "IDR",
      contact_email: email,
    });
    // Due thousands of days back, so both sort to the top of Needs attention
    // (oldest first, at most six rows) whatever else the database holds.
    const back = 9000 + (Date.now() % 500);
    const first = await seedOverdue(api, created, c.customer_id, back + 1);
    const second = await seedOverdue(api, created, c.customer_id, back);

    await page.goto("/dashboard");
    const card = page.getByTestId("needs-attention-card");
    const row1 = card.locator(`[data-invoice="${first.invoiceId}"]`);
    const row2 = card.locator(`[data-invoice="${second.invoiceId}"]`);
    await expect(row1).toBeVisible();
    await expect(row2).toBeVisible();
    await expect(row1.getByTestId("reminded")).toHaveText("");

    // The dialog names the recipient, the invoice and the amount.
    await row1.getByRole("button", { name: "Send reminder" }).click();
    const dialog = page.getByRole("dialog", { name: "Send payment reminder" });
    await expect(dialog).toBeVisible();
    await expect(dialog.getByRole("textbox")).toHaveValue(email);
    await expect(dialog).toContainText(first.number);
    await expect(dialog.getByTestId("reminder-amount")).toHaveText("250,000 IDR");

    // The amount binds the send. Stand in for a payment since the page loaded:
    // the first POST goes to the real backend with a stale expected_amount,
    // so it answers 409 reminder_amount_changed naming the current amount.
    // The dialog shows it and sends nothing until Send is pressed again.
    const shown = { to_email: email, expected_amount: "250000.0000" };
    const remindersUrl = `**/api/v1/invoices/${first.invoiceId}/reminders`;
    let original: unknown = null;
    await page.route(
      remindersUrl,
      async (route) => {
        original = route.request().postDataJSON();
        await route.continue({ postData: JSON.stringify({ ...shown, expected_amount: "1.0000" }) });
      },
      { times: 1 },
    );
    const stale = page.waitForResponse((r) => isApi(r.request(), "POST", `/invoices/${first.invoiceId}/reminders`));
    await dialog.getByRole("button", { name: "Send", exact: true }).click();
    const staleRes = await stale;
    expect(staleRes.status()).toBe(409);
    expect(((await staleRes.json()) as { code: string }).code).toBe("reminder_amount_changed");
    expect(original).toEqual(shown);
    await page.unroute(remindersUrl);
    await expect(dialog.getByTestId("reminder-amount-changed")).toHaveText(
      "The amount changed to 250,000 IDR since this page loaded.",
    );
    await expect(dialog.getByTestId("reminder-amount")).toHaveText("250,000 IDR");
    await expect(dialog).toBeVisible();

    // Send again: the shown recipient and amount go in the body.
    const sent = page.waitForResponse((r) => isApi(r.request(), "POST", `/invoices/${first.invoiceId}/reminders`));
    await dialog.getByRole("button", { name: "Send", exact: true }).click();
    const res = await sent;
    expect(res.status()).toBe(201);
    expect(res.request().postDataJSON()).toEqual(shown);
    expect(((await res.json()) as { recipient: string }).recipient).toBe(email);
    await expect(dialog).toBeHidden();

    // Every row of that customer reads "reminded just now".
    await expect(row1.getByTestId("reminded")).toHaveText(/reminded just now/);
    await expect(row2.getByTestId("reminded")).toHaveText(/reminded just now/);

    // A second reminder to the same customer within 24 hours asks first.
    await row2.getByRole("button", { name: "Send reminder" }).click();
    await expect(dialog).toBeVisible();
    await expect(dialog.getByTestId("reminder-conflict")).toBeHidden();
    const refused = page.waitForResponse((r) => isApi(r.request(), "POST", `/invoices/${second.invoiceId}/reminders`));
    await dialog.getByRole("button", { name: "Send", exact: true }).click();
    const refusedRes = await refused;
    expect(refusedRes.status()).toBe(409);
    expect(refusedRes.request().postDataJSON()).toEqual(shown);
    await expect(dialog.getByTestId("reminder-conflict")).toHaveText(
      `Reminded ${name} just now for ${first.number}. Send anyway?`,
    );

    // "Send anyway" retries with force.
    const forced = page.waitForResponse((r) => isApi(r.request(), "POST", `/invoices/${second.invoiceId}/reminders`));
    await dialog.getByRole("button", { name: "Send anyway" }).click();
    const forcedRes = await forced;
    expect(forcedRes.status()).toBe(201);
    expect(forcedRes.request().postDataJSON()).toEqual({ ...shown, force: true });
    await expect(dialog).toBeHidden();

    // The invoice page lists the reminder.
    await page.goto(`/invoices/${first.invoiceId}/edit`);
    const history = page.getByTestId("invoice-reminders");
    await expect(history.locator("tbody tr")).toHaveCount(1);
    await expect(history.locator("tbody tr")).toContainText(email);
    await expect(history.locator("tbody tr")).toContainText("250,000 IDR");
    await expect(history.locator("tbody tr")).toContainText("App");
  } finally {
    for (const id of created) expect.soft(await discard(api, id), `discard ${id}`).toBe(true);
  }
});
