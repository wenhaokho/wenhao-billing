import { test, expect } from "@playwright/test";

interface FxSummary {
  base_currency: string;
  currencies: { currency: string }[];
  last_sync: { ran_at: string; trigger: "SCHEDULED" | "MANUAL"; source: string } | null;
}

// The FX Rates page shows a card per currency from GET fx-rates/summary, and
// the "Last sync" cell: a time ago with weekly/manual, or "Never synced". The
// expectations come from the summary the page itself loaded, so another spec
// adding a rate or syncing meanwhile cannot make them stale.
test("FX rates page shows the summary cards and last sync", async ({ page }) => {
  const [res] = await Promise.all([
    page.waitForResponse("**/accounting/fx-rates/summary"),
    page.goto("/fx-rates"),
  ]);
  expect(res.ok(), `GET fx-rates/summary: ${res.status()}`).toBe(true);
  const summary = (await res.json()) as FxSummary;

  const cards = page.getByTestId("fx-card");
  // Wait for the summary to render before counting (the last-sync cell is
  // always there once it has).
  const lastSync = page.getByTestId("fx-last-sync");
  await expect(lastSync).toContainText("Last sync");
  if (summary.currencies.length === 0) {
    await expect(cards).toHaveCount(0);
  } else {
    await expect(cards).toHaveCount(summary.currencies.length);
    for (const [i, c] of summary.currencies.entries()) {
      await expect(cards.nth(i)).toContainText(`${c.currency} → ${summary.base_currency} · latest`);
    }
  }

  if (summary.last_sync) {
    await expect(lastSync).toContainText(/just now|ago/);
    await expect(lastSync).toContainText(summary.last_sync.trigger === "SCHEDULED" ? "weekly" : "manual");
  } else {
    await expect(lastSync).toContainText("Never synced");
  }
});
