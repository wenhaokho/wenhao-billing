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

const MONTHS = [
  "January", "February", "March", "April", "May", "June",
  "July", "August", "September", "October", "November", "December",
];

/**
 * Picks an ISO date (YYYY-MM-DD) in the PrimeVue date picker that opens when
 * `input` is clicked, through the calendar panel (prev/next month buttons and
 * a day cell), never by typing into the input.
 */
export async function pickDate(page: Page, input: Locator, iso: string) {
  const [y, m, d] = iso.split("-").map(Number);
  await input.click();
  const panel = page.locator(".p-datepicker:visible").last();
  await expect(panel).toBeVisible();
  for (let i = 0; i < 240; i++) {
    const title = (await panel.locator(".p-datepicker-title").first().innerText()).replace(/\s+/g, " ");
    const month = MONTHS.findIndex((name) => title.includes(name));
    const year = Number(title.match(/\d{4}/)?.[0]);
    const diff = (y - year) * 12 + (m - 1 - month);
    if (month >= 0 && diff === 0) break;
    await panel.getByRole("button", { name: diff > 0 ? "Next Month" : "Previous Month" }).click();
  }
  await panel
    .locator("td:not(.p-datepicker-other-month)")
    .filter({ hasText: new RegExp(`^\\s*${d}\\s*$`) })
    .first()
    .locator("span")
    .first()
    .click();
}

/**
 * Picks `option` in a PrimeVue select (Dropdown) through its overlay: opens
 * it, types into its filter when it has one, clicks the option.
 */
export async function pickOption(page: Page, trigger: Locator, option: string | RegExp) {
  await trigger.click();
  const overlay = page.locator(".p-dropdown-panel:visible, .p-select-overlay:visible").last();
  await expect(overlay).toBeVisible();
  const filter = overlay.locator("input[type=text]");
  if (typeof option === "string" && (await filter.count()) > 0) await filter.fill(option);
  await overlay.getByRole("option", { name: option }).first().click();
  await expect(overlay).toBeHidden();
}
