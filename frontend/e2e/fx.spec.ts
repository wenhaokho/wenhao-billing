import { test, expect } from "@playwright/test";

interface FxSummary {
  base_currency: string;
  currencies: { currency: string }[];
  last_sync: { ran_at: string; trigger: "SCHEDULED" | "MANUAL"; source: string } | null;
}

// The FX Rates page shows a card per currency from GET fx-rates/summary (or
// the empty state when there is no rate), and the "Last sync" cell: a time
// ago with weekly/manual, or "Never synced".
test("FX rates page shows the summary cards and last sync", async ({ page }) => {
  const res = await page.request.get("/api/v1/accounting/fx-rates/summary");
  expect(res.ok(), `GET fx-rates/summary: ${res.status()}`).toBe(true);
  const summary = (await res.json()) as FxSummary;

  await page.goto("/fx-rates");
  const cards = page.getByTestId("fx-card");
  if (summary.currencies.length === 0) {
    await expect(page.getByText("No FX rates recorded yet")).toBeVisible();
    await expect(cards).toHaveCount(0);
  } else {
    await expect(cards).toHaveCount(summary.currencies.length);
    for (const [i, c] of summary.currencies.entries()) {
      await expect(cards.nth(i)).toContainText(`${c.currency} → ${summary.base_currency} · latest`);
    }
  }

  const lastSync = page.getByTestId("fx-last-sync");
  await expect(lastSync).toContainText("Last sync");
  if (summary.last_sync) {
    await expect(lastSync).toContainText(/just now|ago/);
    await expect(lastSync).toContainText(summary.last_sync.trigger === "SCHEDULED" ? "weekly" : "manual");
  } else {
    await expect(lastSync).toContainText("Never synced");
  }
});
