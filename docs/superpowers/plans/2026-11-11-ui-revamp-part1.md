# UI Revamp Part 1 (PrimeVue 4 + Brand Restyle) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move the frontend to PrimeVue 4 with no behaviour change (PR 1), then restyle every screen to the wenhao.id brand from the revamp mockup (PR 2).

**Architecture:** A Playwright smoke suite is written first against today's app and is the behaviour baseline. PR 1 swaps the library and renamed components until that suite passes unchanged. PR 2 themes through a PrimeVue 4 `definePreset` over Aura plus one global stylesheet, swaps PrimeIcons for Lucide, rebuilds the shell, then restyles screens group by group.

**Tech Stack:** Vue 3 + TypeScript + Vite, PrimeVue 4 + `@primeuix/themes`, `lucide-vue-next`, `@fontsource/manrope` + `@fontsource/dm-sans`, Chart.js 4, `@playwright/test` (run in the official Playwright Docker image).

**Spec:** `docs/superpowers/specs/2026-11-11-ui-revamp-restyle-design.md`

## Global Constraints

- Two PRs. PR 1 (Tasks 1–4) ships to production alone, with the owner's merge yes; PR 2 (Tasks 5–11) branches from main after PR 1 is merged. Every merge to main deploys via Coolify and needs the owner's yes.
- No new features, endpoints, data or backend changes. Mockup elements with no data behind them are dropped (never stubbed, never invented); list each one dropped in the PR 2 description.
- Keep: routes, navigation groups, TabView-style status filtering on list views (as `Tabs` in v4), vue-query data fetching, form behaviour, disabled-not-removed fields.
- `npm run build` (`vue-tsc --noEmit && vite build`) passes at the end of every task.
- Nothing runs on the host: npm, build and Playwright run in Docker (the `frontend` compose service, or the Playwright image on the `wenhao-billing_default` network). Never create a new Compose project or network.
- No runtime CDN loads: fonts and icons are bundled npm packages.
- User-facing copy says "software house", never "agency".
- Responsive behaviour kept: 76px icon rail at 721–1100px, drawer with a menu button at ≤720px.
- Tokens, type scale and shapes: exactly the values in the spec's "Visual system" section.
- CI stays off: the e2e suite is run by `scripts/e2e.sh`, not wired into GitHub Actions.
- Test credentials are generated per run and never committed or printed.

## Review Focus

1. A date picked in DatePicker reaches the API as the same calendar day (`YYYY-MM-DD`), not shifted by the browser timezone (owner is in WIB, UTC+7) — pinned in Task 1's form test and must still pass after Task 3.
2. Clearing a Select (back to "all"/empty) still sends no filter / null rather than an empty object or the string "null" — pinned in Task 1.
3. The saved theme choice survives a reload, and PrimeVue overlays (Select panel, DatePicker panel, Dialog, Menu) follow dark mode — pinned in Task 2 and Task 11.
4. Wide tables at 375px scroll inside their card; the page itself never scrolls horizontally — pinned in Task 7.
5. Status chips for every invoice/bill/payment status the API can return get a defined tone (no unstyled fallback) — pinned in Task 5.

---

## PR 1 — PrimeVue 4 upgrade (branch `feat/ui-primevue4`)

### Task 1: Playwright smoke suite (baseline on PrimeVue 3)

**Files:**
- Create: `frontend/playwright.config.ts`, `frontend/e2e/auth.setup.ts`, `frontend/e2e/routes.spec.ts`, `frontend/e2e/tabs.spec.ts`, `frontend/e2e/forms.spec.ts`, `frontend/e2e/helpers.ts`, `scripts/e2e.sh`
- Modify: `frontend/package.json` (devDependency `@playwright/test`, pinned to the same version as the Docker image tag), `frontend/.gitignore` (`test-results/`, `playwright-report/`, `e2e/.auth/`)

**Interfaces:**
- Produces: `bash scripts/e2e.sh [playwright args]` — the command every later task runs. `e2e/helpers.ts` exports `collectConsoleErrors(page): () => string[]` and `ROUTES: string[]`, reused by Tasks 2–11.

- [ ] **Step 1: Write `scripts/e2e.sh`.** Same safety shape as `scripts/compare-local.sh` (`set -euo pipefail`, `MSYS_NO_PATHCONV=1`, `COMPOSE_PROJECT_NAME=wenhao-billing`). It: brings up `db backend frontend` with `--wait`; ensures user `e2e@wenhao.test` exists in the dev `billing` DB (insert via `psql`, copying `business_id` and role from the first admin row, if missing); generates a random password with `openssl rand`, sets it with `printf '%s\n' "$PW" | docker compose exec -T backend /api set-password e2e@wenhao.test`; runs `mcr.microsoft.com/playwright:v<version>-noble` with `--network wenhao-billing_default`, the `frontend/` dir mounted, `BASE_URL=http://frontend:5173`, and the email/password passed via `-e` names read from the environment (never in argv values); exits with Playwright's status. Password never echoed.
- [ ] **Step 2: Write `auth.setup.ts`** (logs in through `/login`, saves storage state to `e2e/.auth/state.json`) and `playwright.config.ts` (Chromium only, `baseURL` from `BASE_URL`, setup project dependency, `retries: 0`, `workers: 4`, trace on failure).
- [ ] **Step 3: Write `routes.spec.ts`.** For each static path in `ROUTES` (every non-parameterised route in `src/router/index.ts` except `/login`, `/forgot-password`, `/reset-password`, which get their own unauthenticated test): `test('route %s renders without errors')` asserts response OK, `page.locator('main h1, main h2').first()` visible, and `collectConsoleErrors(page)()` equals `[]` (ignoring only Vite HMR/websocket noise, listed explicitly in the helper). Plus one detail route per entity reached by clicking the first row of its list (customer, invoice edit, quotation, bill, vendor, item, project, account) with the same assertions.
- [ ] **Step 4: Write `tabs.spec.ts`.** For each list view with status tabs (Invoices, Quotations, Bills, Projects, Customers, Vendors, Recurring, Reports, Audit Log — whichever of these have `TabView` today): click each tab and assert (a) the tab becomes selected (`aria-selected="true"`), (b) a network request fires whose URL or the rendered rows change to match that tab (assert the status query param when the view filters server-side, the row chips' text when client-side). Plus `test('customers show-inactive switch toggles inactive rows')` and the same for vendors.
- [ ] **Step 5: Write `forms.spec.ts`** — each test creates a uniquely named record (suffix `Date.now()`) and asserts the saving request body:
  - `test('project dates save as the picked calendar day')`: on `/projects/new` pick start `2026-12-01` and end `2026-12-31` through the DatePicker UI (not by typing into the input), save, assert request body `start_date === "2026-12-01"` and `end_date === "2026-12-31"`. Run with `timezoneId: 'Asia/Jakarta'`.
  - `test('invoice with due date saves')`: `/invoices/new`, select customer and currency via the Select UI, one line item, pick due date `2026-12-31`, save draft; assert body `due_date === "2026-12-31"` and response 2xx.
  - `test('bill saves')`, `test('fx rate saves')` (dated `2026-12-01`, assert `effective_date`/date field equals it), `test('item saves')`, `test('vendor saves')`, `test('customer saves')`, `test('account saves')`.
  - `test('clearing a list filter select sends no filter')`: on any list view with a Select filter (e.g. Invoices customer filter if present, else Audit Log), pick a value, clear it, assert the next request has no such param (or `null` in body, matching today).
  - Field names are whatever today's request bodies use — read them from the view's API call, do not change them.
- [ ] **Step 6: Run `bash scripts/e2e.sh` on unmodified PrimeVue 3.** Expected: all pass. Any test that fails on today's app is a bug in the test (or a pre-existing app bug: record it in the PR description and mark that test `test.fixme` with the reason). This green run is the baseline.
- [ ] **Step 7: Commit** — `test(e2e): Playwright smoke suite — routes, status tabs, form saves`.

### Task 2: PrimeVue 4 core switch

**Files:**
- Modify: `frontend/package.json` (`primevue@^4`, add `@primeuix/themes`; keep `primeicons`), `frontend/src/main.ts`, `frontend/src/composables/useTheme.ts`, `frontend/src/assets/app.css` (only where it referenced v3 theme variables), `frontend/src/theme/preset.ts` (create)
- Test: `frontend/e2e/theme.spec.ts` (create)

**Interfaces:**
- Produces: `src/theme/preset.ts` exporting `const AppPreset` (in PR 1: `definePreset(Aura, {})`); dark mode selector class `.app-dark` on `<html>`; `useTheme()` keeps its signature `{ theme: Ref<'light'|'dark'>, toggle(): void }` and still sets `data-theme` on `<html>`.

- [ ] **Step 1: Write `theme.spec.ts`:** `test('dark choice persists across reload')` — toggle theme via the topbar button, reload, assert `html` has class `app-dark` and `data-theme="dark"`; `test('select panel follows dark mode')` — in dark mode open a Select's overlay and assert its computed background is not `rgb(255, 255, 255)`.
- [ ] **Step 2: Run** `bash scripts/e2e.sh theme.spec.ts` — expected FAIL (no `app-dark` class on v3).
- [ ] **Step 3: Upgrade deps** inside the frontend container (`docker compose exec frontend npm install primevue@^4 @primeuix/themes`), then recreate the frontend container with a fresh `node_modules` volume (`docker compose up -d --build -V frontend`) so the named volume picks up the new lockfile.
- [ ] **Step 4: `main.ts`:** `app.use(PrimeVue, { ripple: true, theme: { preset: AppPreset, options: { darkModeSelector: '.app-dark', cssLayer: false } } })`; drop `primevue/resources` imports. **`useTheme.ts`:** stop injecting theme `<link>`s; `apply(t)` toggles `app-dark` on `document.documentElement` and keeps `data-theme`. Same storage key `wb:theme`, same system default.
- [ ] **Step 5: Run** `npm run build` in the container, then `bash scripts/e2e.sh`. Expected: `theme.spec.ts` passes; other failures are only from renamed components (Task 3) — note them, don't fix here unless the build itself fails.
- [ ] **Step 6: Commit** — `feat(ui): PrimeVue 4 with Aura preset; dark mode via .app-dark`.

### Task 3: Migrate renamed components and selectors

**Files:** the 23 files importing `primevue/dropdown|calendar|inputswitch|tabview` (listed by `grep -rlE "primevue/(dropdown|calendar|inputswitch|tabview)" frontend/src`), plus any file with `:deep(.p-…)` or `.p-…` selectors that no longer exist in v4.

**Interfaces:**
- Consumes: Task 1 suite as the acceptance test.

- [ ] **Step 1: Run** `bash scripts/e2e.sh` — expected FAIL on routes/forms touching the renamed components (the baseline for this task).
- [ ] **Step 2: Rename:** `Dropdown`→`Select` (`primevue/select`), `Calendar`→`DatePicker` (`primevue/datepicker`), `InputSwitch`→`ToggleSwitch` (`primevue/toggleswitch`), `TabView`/`TabPanel`→`Tabs`/`TabList`/`Tab`/`TabPanels`/`TabPanel` with `value` keys equal to the status each tab filters on. Keep every `v-model`, emitted value type, `date-format`, `option-label`/`option-value`, `show-clear` and disabled state as is. Where a view reads the active tab index (`activeIndex`), switch to the tab `value` and keep the same downstream status mapping.
- [ ] **Step 3: Fix selectors:** replace v3 class names in scoped/`:deep` CSS with their v4 equivalents (`p-dropdown*`→`p-select*`, `p-calendar`→`p-datepicker`, `p-inputswitch`→`p-toggleswitch`, `p-tabview*`→`p-tabs`/`p-tablist`/`p-tab`/`p-tabpanels`, `p-inputtextarea`→`p-textarea`; DataTable classes per v4 markup). Verify each against v4 rendered DOM, not memory.
- [ ] **Step 4: Run** `npm run build` then `bash scripts/e2e.sh`. Expected: full suite PASS, unchanged from the Task 1 baseline (the DatePicker timezone test included).
- [ ] **Step 5: Commit** — `refactor(ui): migrate to PrimeVue 4 component names`.

### Task 4: PR 1 verification and PR

- [ ] **Step 1:** `grep -rnE "primevue/(dropdown|calendar|inputswitch|tabview|resources)" frontend/src` → no output.
- [ ] **Step 2:** `bash scripts/check.sh` → passes (Go steps unaffected; frontend build green).
- [ ] **Step 3:** In the browser pane, walk every route in light and dark at 1440px, 900px and 375px; screenshot dashboard, invoices list, invoice form with an open DatePicker, and a dialog. No console errors.
- [ ] **Step 4:** Push `feat/ui-primevue4`, open PR "PrimeVue 4 upgrade (revamp part 1, PR 1)" with: what changed, e2e result, screenshots, how to run `scripts/e2e.sh`. Ask the owner for the merge yes; after merge, confirm the Coolify deploy finished and the site loads.

---

## PR 2 — Brand restyle (branch `feat/ui-brand-restyle`, from main after PR 1 merges)

### Task 5: Brand preset, tokens, fonts, status chips

**Files:**
- Modify: `frontend/src/theme/preset.ts`, `frontend/src/assets/app.css` (replace `--color-*`, `--radius-*`, `--shadow-*` with the spec's tokens, light + `.app-dark`), `frontend/src/main.ts` (import fonts), `frontend/package.json` (`@fontsource/manrope`, `@fontsource/dm-sans`)
- Create: `frontend/src/theme/status.ts`, `frontend/src/components/StatusTag.vue`, `frontend/src/theme/overrides.css` (only what tokens can't express)
- Test: `frontend/e2e/brand.spec.ts`

**Interfaces:**
- Produces: `statusTone(status: string): 'green'|'amber'|'red'|'blue'|'neutral'` and `statusLabel(status: string): string` in `theme/status.ts`; `<StatusTag :status="s" />` renders a chip with the tone's text/background pair. CSS variables `--wb-bg, --wb-surface, --wb-surface-2, --wb-ink, --wb-muted, --wb-muted-soft, --wb-line, --wb-tint, --wb-red, --wb-red-deep, --wb-red-soft, --wb-gold, --wb-gold-bg` and `--wb-{green,amber,red,blue}-{fg,bg}` used by all later tasks.

- [ ] **Step 1: Write `brand.spec.ts`:**
  - `test('primary button is the brand red pill')`: on `/invoices` the primary "New invoice" button has computed `background-color` `rgb(165, 28, 48)`, `border-radius` `30px`, `height` `42px`.
  - `test('body font is DM Sans and h1 is Manrope')`: computed `font-family` of `body` starts with `"DM Sans"`, of page `h1` starts with `Manrope`; no request to `fonts.googleapis.com` or `unpkg.com` during the run.
  - `test('cards and tables are square with a line border')`: a DataTable wrapper has `border-radius: 0px` and `border-top-color` `rgb(225, 215, 200)` in light mode.
  - `test('every status gets a defined tone')`: unit-style test importing `statusTone` (Playwright test without a page) asserts it returns a non-`neutral` tone for every status string the API defines (enumerate from `frontend/src/api` types / constants: invoice, quotation, bill, project, payment and reconciliation statuses) except the ones the spec maps to neutral (Draft, Void).
- [ ] **Step 2: Run** `bash scripts/e2e.sh brand.spec.ts` — expected FAIL.
- [ ] **Step 3: Implement** the preset (primary palette built around `#a51c30`; surface palette from the cream/ink scale; `colorScheme.light`/`.dark` semantic tokens; component tokens for button radius/height, card/datatable radius 0 and border, tag radius 2px, input radius 2px, overlay radius 0 + soft shadow), the `app.css` tokens, font imports (Manrope 500/600/800, DM Sans 400/500), and `status.ts`/`StatusTag.vue`. Tone mapping: Paid/Cleared/Active/Approved→green; Sent/Finalized/Open→blue; Overdue/Rejected/Held→red; Partially paid/Pending manual review/Awaiting finalization→amber; Draft/Void/Inactive→neutral. Replace every status `Tag` usage with `StatusTag`.
- [ ] **Step 4: Run** build + full `bash scripts/e2e.sh` → PASS.
- [ ] **Step 5: Commit** — `feat(ui): brand preset, tokens, fonts and status chips`.

### Task 6: Lucide icons replace PrimeIcons

**Files:** Create `frontend/src/components/AppIcon.vue`, `frontend/src/theme/icons.ts`; modify every file with `pi pi-` (≈212 uses); remove `primeicons` from `package.json` and `main.ts`.

**Interfaces:**
- Produces: `icons.ts` maps the app's icon names to Lucide components (`export const icons: Record<IconName, Component>`; `IconName` is a string union of the names actually used). `<AppIcon name="..." :size="16" />`. For `Button`: use its `#icon` slot with `AppIcon`; for `Menu` model items: `icon: IconName` rendered through the Menu `#itemicon` slot.

- [ ] **Step 1:** Add to `brand.spec.ts`: `test('no PrimeIcons font is loaded')` — no request whose URL contains `primeicons` during a dashboard visit, and `document.querySelectorAll('.pi').length === 0` on `/dashboard`, `/invoices`, `/invoices/new`.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement** the wrapper and replace all usages; pick the Lucide icon the mockup uses for each nav item (read from the mockup), nearest equivalent elsewhere.
- [ ] **Step 4:** `grep -rn "pi pi-\|primeicons" frontend/src frontend/package.json` → no output; build + full e2e → PASS.
- [ ] **Step 5: Commit** — `feat(ui): Lucide icons; drop PrimeIcons`.

### Task 7: App shell — sidebar, topbar, responsive

**Files:** Modify `frontend/src/App.vue` (split the shell into `frontend/src/components/shell/AppSidebar.vue` and `AppTopbar.vue` if `App.vue` would exceed ~300 lines). Test: `frontend/e2e/shell.spec.ts`.

- [ ] **Step 1: Write `shell.spec.ts`:**
  - `test('desktop sidebar')` at 1440×900: sidebar width `252px`, background `rgb(80, 25, 33)`, six group eyebrows reading `01 / Overview` … `06 / Admin` (group names as today), active nav item background `rgb(165, 28, 48)`, brand text "Wenhao Billing" and "Software house".
  - `test('tablet rail')` at 900×1000: sidebar width `76px`, nav labels hidden, icons visible.
  - `test('phone drawer')` at 375×812: sidebar off-canvas; menu button opens it; clicking a nav link closes it and navigates.
  - `test('no horizontal page scroll on phone')` at 375×812 for `/invoices`, `/reports`, `/audit-log`: `document.documentElement.scrollWidth <= 375`.
  - `test('topbar')`: page title text equals the route title; theme toggle and user chip (initials) present; user chip opens the existing user menu.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement** per the spec's "App shell" section; content max-width 1480px; wide tables get an `overflow-x: auto` card wrapper.
- [ ] **Step 4:** build + full e2e → PASS.
- [ ] **Step 5: Commit** — `feat(ui): branded app shell`.

### Task 8: Dashboard and charts

**Files:** `frontend/src/views/PaymentIntakeWidget.vue` (this is the `/dashboard` route's view; keep its name), create `frontend/src/theme/chartColors.ts`. Test: extend `brand.spec.ts`.

**Interfaces:** `chartColors(): { series: string[]; unpaidAlpha: number; grid: string; text: string }` reads the CSS variables at call time; charts re-render on theme change.

- [ ] **Step 1: Test** `test('dashboard charts use brand colours and follow theme')`: the revenue chart's first dataset `backgroundColor` (read via the Chart.js instance on the canvas) equals the brand red in light mode; after toggling dark, the chart's tick colour equals `--wb-ink` in dark.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement** the mockup's dashboard layout (page header with eyebrow + H1, card grid, card title style, KPI figure style) around today's content only; revenue chart styled as variant A (stacked by currency, unpaid as faded/hatched segment as today). Drop the mockup's period selector, sparklines, aging, cash-in, reconciliation donut, top customers (part 2) — list them for the PR description.
- [ ] **Step 4:** build + full e2e → PASS; screenshot vs mockup.
- [ ] **Step 5: Commit** — `feat(ui): brand dashboard`.

### Task 9: Designed list screens

**Files:** `CustomersView.vue`, `InvoicesView.vue`, `ChartOfAccountsView.vue`, `ReportsView.vue`, `FxRatesView.vue`, `AuditLogView.vue`, `UsersView.vue`; create `frontend/src/components/PageHeader.vue` (`props: { eyebrow?: string; title: string }`, default slot for actions) and `frontend/src/components/EmptyState.vue` (`props: { title: string; message: string }`, slot for the CTA).

- [ ] **Step 1: Test** in `brand.spec.ts`: `test('list pages use the page header')` — on each of the seven routes, `h1` computed `font-size` `40px` and an eyebrow element with `text-transform: uppercase`; table header cells `font-size` `11px` uppercase.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement** each screen to the mockup within today's data and controls (tabs, filters and actions that exist today stay; mockup-only ones dropped and listed).
- [ ] **Step 4:** build + full e2e → PASS; screenshots vs mockup.
- [ ] **Step 5: Commit** — `feat(ui): brand list screens`.

### Task 10: Invoice form, Manual Review, MCP page, and all undesigned screens

**Files:** `InvoiceFormView.vue`, `ManualReviewView.vue`, `McpIntegrationView.vue`, every remaining view and dialog (forms, detail pages, auth views via `AuthLayout.vue` + `auth-form.css`, the four dialogs, `PaymentIntakeWidget.vue`, the six placeholder-designed lists using `EmptyState`).

- [ ] **Step 1: Test** in `brand.spec.ts`: `test('every route uses brand chrome')` — for every path in `ROUTES` plus the entity detail routes, `h1` font-family starts with `Manrope` and no element has a computed `border-radius` > 2px except buttons (`30px`), avatars and toggles. Auth pages: login card square with line border, primary button red pill.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement** with `PageHeader`, `EmptyState`, `StatusTag`, square bordered cards. Disabled fields stay disabled, nothing removed.
- [ ] **Step 4:** build + full e2e → PASS.
- [ ] **Step 5: Commit** — `feat(ui): brand forms, detail pages, dialogs and auth`.

### Task 11: Dark mode pass, verification, PR

- [ ] **Step 1: Test** `test('dark mode has no light surfaces')` in `theme.spec.ts`: in dark mode on every route, no visible element larger than 200×100px has computed background `rgb(255, 255, 255)` or `rgb(252, 251, 246)`; body text colour equals `rgb(252, 251, 246)`.
- [ ] **Step 2: Run** → fix failures until PASS.
- [ ] **Step 3:** `bash scripts/check.sh` passes; full `bash scripts/e2e.sh` passes.
- [ ] **Step 4:** Browser walk at 1440/900/375 in light and dark; screenshot the 11 designed screens beside the mockup.
- [ ] **Step 5:** Push `feat/ui-brand-restyle`, open PR "Brand restyle (revamp part 1, PR 2)" with screenshots and the list of mockup elements deferred to part 2. Ask the owner for the merge yes; confirm the deploy after merge.
