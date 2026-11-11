import { createHmac } from "node:crypto";
import { test, expect, type APIRequestContext } from "@playwright/test";
import { apiPost, uniq } from "./helpers";

// Manual Review: a payment held by the safe-stop gate shows how it arrived
// (via tag) and why it was held (reason tags); the invoice picker finds open
// invoices by customer name, lists the payment's currency unless "All
// currencies" is on, and warns (never blocks) on a currency mismatch or a
// payment above the balance due. Approving through the picker clears it.

test.use({ colorScheme: "light" });

// docker-compose.yml's published dev WEBHOOK_HMAC_SECRET (the dev stack
// only; production refuses it). E2E_WEBHOOK_SECRET overrides it.
const WEBHOOK_SECRET = process.env.E2E_WEBHOOK_SECRET ?? "dev-webhook-secret-change-me";

/** Marks this spec's invoices and held payments, so leftovers can be found. */
const SEED_LINE = "E2E manual review line";
const PAYER_PREFIX = "E2E Review";
/** Below the IDR invoice A's 200,000 and above invoice C's 100,000: never an exact match. */
const PAID = "123457";

interface Payment {
  payment_id: string;
  status: string;
  currency: string;
  payer_name: string;
}

function today(): string {
  return new Date().toISOString().slice(0, 10);
}

/** Posts a signed gateway webhook; returns the stored payment. */
async function webhook(api: APIRequestContext, body: Record<string, string>): Promise<Payment> {
  const raw = JSON.stringify(body);
  const sig = createHmac("sha256", WEBHOOK_SECRET).update(raw).digest("hex");
  const res = await api.post("/api/v1/payments/intake/webhook", {
    data: raw,
    headers: { "Content-Type": "application/json", "X-Signature": sig },
  });
  expect(res.status(), `webhook: ${res.status()} ${await res.text()}`).toBe(201);
  return (await res.json()) as Payment;
}

async function seedInvoice(api: APIRequestContext, created: string[], customerId: string, currency: string, price: string) {
  const inv = await apiPost<{ invoice_id: string }>(api, "/invoices", {
    customer_id: customerId,
    currency,
    issue_date: today(),
    due_date: today(),
    line_items: [{ description: SEED_LINE, quantity: "1", unit_price: price }],
  });
  created.push(inv.invoice_id);
  const fin = await api.post(`/api/v1/invoices/${inv.invoice_id}/finalize`);
  expect(fin.ok(), `finalize: ${fin.status()} ${await fin.text()}`).toBe(true);
  return inv.invoice_id;
}

/**
 * Makes sure an SGD→IDR rate dated on or before today exists, so an SGD
 * invoice can be finalized (dashboard.spec.ts does the same). A rate it
 * creates stays: deleting it could remove one another test relies on.
 */
async function ensureSgdRate(api: APIRequestContext) {
  const res = await api.get("/api/v1/accounting/fx-rates");
  expect(res.ok(), `GET fx-rates: ${res.status()}`).toBe(true);
  const rates = (await res.json()) as { from_currency: string; to_currency: string; as_of_date: string }[];
  if (rates.some((r) => r.from_currency === "SGD" && r.to_currency === "IDR" && r.as_of_date.slice(0, 10) <= today())) return;
  const monthAgo = new Date(Date.now() - 30 * 86_400_000).toISOString().slice(0, 10);
  await apiPost(api, "/accounting/fx-rates", { from_currency: "SGD", to_currency: "IDR", rate: "12000", as_of_date: monthAgo });
}

/** Voids (or, for a draft, deletes) an invoice; true when it worked. */
async function discard(api: APIRequestContext, invoiceId: string): Promise<boolean> {
  const v = await api.post(`/api/v1/invoices/${invoiceId}/void`, { data: { reason: "E2E manual review cleanup" } });
  if (v.ok()) return true;
  return (await api.delete(`/api/v1/invoices/${invoiceId}`)).ok();
}

/**
 * Takes a payment out of play: a held one is approved against `invoiceId`
 * (same currency) and then reversed, a cleared one reversed. Reversal flags
 * it, so the invoice can be voided afterwards.
 */
async function retire(api: APIRequestContext, paymentId: string, invoiceId: string | null) {
  const get = await api.get(`/api/v1/payments/${paymentId}`);
  if (!get.ok()) return;
  let p = (await get.json()) as Payment;
  if (p.status === "PENDING_MANUAL_REVIEW" && invoiceId) {
    p = await apiPost<Payment>(api, `/payments/${paymentId}/manual-review`, { invoice_id: invoiceId, adjustment_type: "NONE" });
  }
  if (p.status === "CLEARED") {
    await apiPost(api, `/payments/${paymentId}/reverse`, { reason: "E2E manual review cleanup" });
  }
}

/** Retires held payments and voids invoices an earlier, interrupted run left behind. */
async function discardLeftovers(api: APIRequestContext) {
  const held = await api.get("/api/v1/payments/awaiting-review");
  expect(held.ok(), `GET awaiting-review: ${held.status()}`).toBe(true);
  const mine = ((await held.json()) as Payment[]).filter((p) => p.payer_name.startsWith(PAYER_PREFIX));
  const swept: string[] = [];
  try {
    if (mine.length) {
      const c = await apiPost<{ customer_id: string }>(api, "/customers", { name: `${PAYER_PREFIX} sweep ${uniq()}`, default_currency: "IDR" });
      for (const p of mine) {
        // A fresh invoice above the payment, so the approval always fits.
        const inv = await seedInvoice(api, swept, c.customer_id, p.currency, "100000000");
        await retire(api, p.payment_id, inv);
      }
    }
  } finally {
    for (const id of swept) await discard(api, id);
  }
  const res = await api.get("/api/v1/invoices?open=true&limit=500");
  expect(res.ok(), `GET invoices: ${res.status()}`).toBe(true);
  const list = (await res.json()) as { invoice_id: string; line_items: { description: string }[] }[];
  for (const inv of list.filter((i) => i.line_items.some((l) => l.description === SEED_LINE))) {
    expect(await discard(api, inv.invoice_id), `discard leftover ${inv.invoice_id}`).toBe(true);
  }
}

test("held payment: via and reason tags, invoice picker with warnings, approve", async ({ page }) => {
  const api = page.request;
  await discardLeftovers(api);
  const created: string[] = [];
  let paymentId: string | null = null;
  let invoiceA: string | null = null;
  try {
    const tag = uniq();
    const name = `${PAYER_PREFIX} ${tag}`;
    const c = await apiPost<{ customer_id: string }>(api, "/customers", { name, default_currency: "IDR" });
    invoiceA = await seedInvoice(api, created, c.customer_id, "IDR", "200000");
    await ensureSgdRate(api);
    const invoiceB = await seedInvoice(api, created, c.customer_id, "SGD", "50");
    const invoiceC = await seedInvoice(api, created, c.customer_id, "IDR", "100000");

    // The payer is the customer's own name, so the engine picks one of its
    // invoices, but the amount matches none: held for review.
    const p = await webhook(api, {
      amount: PAID,
      currency: "IDR",
      payer_name: name,
      payment_date: today(),
      external_ref: `e2e-review-${tag}`,
    });
    paymentId = p.payment_id;
    expect(p.status).toBe("PENDING_MANUAL_REVIEW");

    await page.goto("/review");
    const row = page.locator("tbody tr").filter({ hasText: name });
    await expect(row).toHaveCount(1);
    await expect(row.locator('[data-status="WEBHOOK"]')).toHaveText("Bank webhook");
    await row.click();

    const detail = page.locator("aside.detail");
    await expect(detail.getByTestId("via-tag")).toHaveText("Bank webhook");
    const reasons = detail.getByTestId("held-reasons");
    const mismatch = reasons.locator('li[data-reason="AMOUNT_MISMATCH"]');
    await expect(mismatch).toContainText("amount mismatch: delta=");
    await expect(mismatch.locator('[data-status="AMOUNT_MISMATCH"]')).toHaveAttribute("data-tone", "red");
    await expect(mismatch.locator('[data-status="AMOUNT_MISMATCH"]')).toHaveText("Amount mismatch");
    const low = reasons.locator('li[data-reason="LOW_CONFIDENCE"]');
    await expect(low.locator('[data-status="LOW_CONFIDENCE"]')).toHaveAttribute("data-tone", "amber");

    // Search by customer name: the IDR invoices are listed, the SGD one only
    // with "All currencies".
    const input = detail.locator("#review-invoice");
    const listbox = page.getByRole("listbox");
    await input.fill(name);
    await expect(listbox.locator(`[data-invoice="${invoiceA}"]`)).toBeVisible();
    await expect(listbox.locator(`[data-invoice="${invoiceC}"]`)).toBeVisible();
    await expect(listbox.locator(`[data-invoice="${invoiceB}"]`)).toHaveCount(0);
    // The open list can sit over the toggle (it flips above the input when
    // there is no room below): close it first, as a person would.
    await input.press("Escape");
    await expect(listbox).toBeHidden();
    await detail.getByText("All currencies").click();
    await input.fill("");
    await input.fill(name);
    await listbox.locator(`[data-invoice="${invoiceB}"]`).click();
    await expect(detail.getByTestId("warn-currency")).toContainText("SGD");
    await expect(detail.getByTestId("warn-over-balance")).toHaveCount(0);

    // Invoice C's balance (100,000) is below the payment: warned, not blocked.
    await input.fill(name);
    await listbox.locator(`[data-invoice="${invoiceC}"]`).click();
    await expect(detail.getByTestId("warn-currency")).toHaveCount(0);
    await expect(detail.getByTestId("warn-over-balance")).toBeVisible();
    await expect(detail.getByRole("button", { name: "Approve & Post" })).toBeEnabled();

    // Invoice A: no warning; approving clears the payment and drops the row.
    await input.fill(name);
    await listbox.locator(`[data-invoice="${invoiceA}"]`).click();
    await expect(detail.getByTestId("warn-over-balance")).toHaveCount(0);
    await expect(detail.getByTestId("warn-currency")).toHaveCount(0);
    const approved = page.waitForRequest(
      (r) => r.method() === "POST" && r.url().endsWith(`/api/v1/payments/${paymentId}/manual-review`),
    );
    await detail.getByRole("button", { name: "Approve & Post" }).click();
    const req = await approved;
    expect(req.postDataJSON()).toEqual({ invoice_id: invoiceA, adjustment_type: "NONE" });
    await expect(row).toHaveCount(0);
    const after = await api.get(`/api/v1/payments/${paymentId}`);
    expect(((await after.json()) as Payment).status).toBe("CLEARED");
  } finally {
    if (paymentId) await retire(api, paymentId, invoiceA);
    for (const id of created) await discard(api, id);
  }
});
