import { expect, type APIRequestContext, type Locator, type Page, type Request } from "@playwright/test";

/** Where auth.setup.ts saves the signed-in session the other tests reuse. */
export const STORAGE_STATE = "e2e/.auth/state.json";

/**
 * Every non-parameterised route in src/router/index.ts except the public
 * auth pages (/login, /forgot-password, /reset-password) and the "/" redirect.
 */
export const ROUTES: string[] = [
  "/dashboard",
  "/account",
  "/customers",
  "/customers/new",
  "/invoices",
  "/invoices/new",
  "/invoices/recurring",
  "/invoices/recurring/new",
  "/quotations",
  "/quotations/new",
  "/bills",
  "/bills/new",
  "/queue",
  "/review",
  "/audit-log",
  "/vendors",
  "/vendors/new",
  "/items",
  "/items/new",
  "/projects",
  "/projects/new",
  "/chart-of-accounts",
  "/chart-of-accounts/new",
  "/reports",
  "/fx-rates",
  "/fx-rates/new",
  "/mcp-integration",
  "/users",
  "/users/new",
];

/**
 * Console noise that is not an app error: the Vite dev server's HMR client
 * and its websocket. Nothing else is ignored.
 */
const IGNORED_CONSOLE: RegExp[] = [
  /\[vite\]/i,
  /WebSocket connection to 'wss?:\/\/[^']*' failed/i,
];

/**
 * Starts collecting console errors and uncaught page errors; call the returned
 * function to read what was collected so far.
 */
export function collectConsoleErrors(page: Page): () => string[] {
  const errors: string[] = [];
  page.on("console", (msg) => {
    if (msg.type() !== "error") return;
    const text = msg.text();
    if (IGNORED_CONSOLE.some((re) => re.test(text))) return;
    const loc = msg.location();
    errors.push(loc?.url ? `${text} (${loc.url})` : text);
  });
  page.on("pageerror", (err) => errors.push(`pageerror: ${err.message}`));
  return () => [...errors];
}

/** The page's own heading inside <main>. */
export function mainHeading(page: Page) {
  return page.locator("main h1, main h2").first();
}

/** A unique suffix for records the tests create. */
export function uniq(): string {
  return `${Date.now()}${Math.floor(Math.random() * 1000)}`;
}

/** POSTs JSON to /api/v1`path` with the signed-in session; fails the test unless 2xx. */
export async function apiPost<T = Record<string, unknown>>(api: APIRequestContext, path: string, data: unknown): Promise<T> {
  const res = await api.post(`/api/v1${path}`, { data });
  expect(res.ok(), `POST ${path}: ${res.status()} ${await res.text()}`).toBe(true);
  return (await res.json()) as T;
}

/** True when req is an API call to `path` (under /api/v1) with method. */
export function isApi(req: Request, method: string, path: string | RegExp): boolean {
  if (req.method() !== method) return false;
  const u = new URL(req.url());
  if (!u.pathname.startsWith("/api/v1")) return false;
  const p = u.pathname.slice("/api/v1".length);
  return typeof path === "string" ? p === path : path.test(p);
}

// ---------------------------------------------------------------------------
// PrimeVue widgets. Every spec reaches selects, date pickers, number inputs
// and status chips only through these helpers, so a PrimeVue upgrade changes
// this block and nothing else. They go by ARIA roles, the locale's aria
// labels and PrimeVue's data-p* / data-pc-section attributes, not by CSS class.
// ---------------------------------------------------------------------------

/**
 * The select (PrimeVue Dropdown/Select, role "combobox") inside `scope`. With
 * `name`, the one whose accessible name is exactly that; an empty select is
 * named by its placeholder, a filled one by its selected label.
 */
export function selectIn(scope: Page | Locator, name?: string): Locator {
  return scope.getByRole("combobox", name ? { name, exact: true } : undefined).first();
}

/**
 * Picks `option` in a select through its overlay: opens it, types into its
 * filter when it has one, clicks the option, waits for the overlay to close.
 */
export async function pickOption(page: Page, select: Locator, option: string | RegExp) {
  await select.click();
  const list = page.getByRole("listbox");
  await expect(list).toBeVisible();
  const filter = page.getByRole("searchbox");
  if (typeof option === "string" && (await filter.count()) > 0) await filter.fill(option);
  await page.getByRole("option", { name: option, exact: typeof option === "string" }).first().click();
  await expect(list).toBeHidden();
}

/** Clears a select that has show-clear, through its clear icon. */
export async function clearSelect(select: Locator) {
  await select.locator("xpath=..").locator('[data-pc-section="clearicon"]').click();
}

const MONTHS = [
  "January", "February", "March", "April", "May", "June",
  "July", "August", "September", "October", "November", "December",
];

/**
 * Picks an ISO date (YYYY-MM-DD) in the date picker that opens when `input` is
 * clicked, through its panel (the "Choose Date" dialog: Previous/Next Month
 * buttons and a day cell), never by typing into the input.
 */
export async function pickDate(page: Page, input: Locator, iso: string) {
  const [y, m, d] = iso.split("-").map(Number);
  await input.click();
  const panel = page.getByRole("dialog", { name: "Choose Date" });
  await expect(panel).toBeVisible();
  for (let i = 0; i < 240; i++) {
    const monthName = (await panel.getByRole("button", { name: "Choose Month" }).innerText()).trim();
    const year = Number((await panel.getByRole("button", { name: "Choose Year" }).innerText()).trim());
    const month = MONTHS.indexOf(monthName);
    expect(month, `month button reads "${monthName}"`).toBeGreaterThanOrEqual(0);
    const diff = (y - year) * 12 + (m - 1 - month);
    if (diff === 0) break;
    await panel.getByRole("button", { name: diff > 0 ? "Next Month" : "Previous Month" }).click();
  }
  // Day cells are named by their day number; days of the neighbouring months
  // carry data-p-other-month="true".
  await panel
    .getByRole("gridcell", { name: String(d), exact: true })
    .and(panel.locator(':not([data-p-other-month="true"])'))
    .locator("span")
    .first()
    .click();
  await expect(panel).toBeHidden();
}

/** Replaces the value of a number input (role "spinbutton") by typing `value`. */
export async function fillNumber(input: Locator, value: string) {
  await input.click();
  await input.press("Control+a");
  await input.pressSequentially(value);
  await input.blur();
}

/**
 * The number inputs (role "spinbutton") of the line that holds `anchor`: the
 * nearest ancestor of `anchor` that contains any, in document order.
 */
export function numbersNear(anchor: Locator): Locator {
  return anchor.locator("xpath=ancestor::*[.//*[@role='spinbutton']][1]").getByRole("spinbutton");
}

/**
 * API status codes of the status chips (StatusTag, a PrimeVue Tag carrying
 * data-status) in the rendered table body rows. The chip shows a label
 * ("Partially paid"); data-status keeps the code ("PARTIAL").
 */
export async function rowStatuses(page: Page): Promise<string[]> {
  return page
    .locator('tbody tr [data-pc-name="tag"][data-status]')
    .evaluateAll((els) => els.map((el) => (el.getAttribute("data-status") ?? "").trim()));
}

/** A status chip (StatusTag) by its API status code. */
export function statusChip(scope: Page | Locator, status: string): Locator {
  return scope.locator(`[data-pc-name="tag"][data-status="${status}"]`);
}
