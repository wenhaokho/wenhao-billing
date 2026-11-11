# UI revamp, part 1: PrimeVue 4 upgrade and brand restyle — design

Date: 2026-11-11 · Status: draft for owner approval

## Why

The owner handed over a revamp design (`docs/redesign/revamp/`) on 2026-11-09 and asked for it to wait until the Go
port was done and Python removed. Both are done (PRs #79, #80). The app still wears a generic slate-and-blue PrimeVue
look; the design moves it onto the wenhao.id brand (burgundy, gold, cream, Manrope + DM Sans) so the billing app looks
like the rest of the software house's identity.

The revamp is three projects, in this order (owner agreed 2026-11-11):

1. **This spec — restyle.** Upgrade PrimeVue 3 → 4 and apply the brand look to every screen. No new features.
2. Later spec — new dashboard analytics and screen extras (KPI sparklines, AR aging, cash-in forecast, top customers,
   audit-log filters/CSV, Manual Review split view, invoices KPI strip, FX change cards, "show inactive", "(you)").
3. Later spec — invoice/receipt PDF redesign in the Go renderer (keeping PO/SO cell, footer, uploaded logo).

## Goals

- Every screen uses the brand visual system from the mockup: tokens, fonts, buttons, tables, cards, chips, sidebar,
  topbar — in light and dark mode.
- PrimeVue is on the current major version (4.x), themed through its design-token preset rather than CSS overrides.
- Screens the mockup designs in full look like the mockup (within what the existing data allows).
- Screens the mockup does not design (forms, detail pages, auth, dialogs) follow the same system consistently.

## Non-goals

- No new features, endpoints or data. Where a designed screen shows something the app has no data for (dashboard
  sparklines, aging, FX change cards, audit filters, invoices KPI strip, Manual Review split view), the restyled
  screen keeps today's content in the new look. Those features belong to part 2.
- No backend changes. No PDF changes.
- No change to routes, navigation structure, list-view TabView status filtering, data fetching (vue-query), or form
  behaviour.
- No user-selectable nav style or density settings: the mockup's tweak panel is a design-tool artefact.

## Anti-goals (a failure even if it "works")

- Any behaviour change hidden inside the restyle: a field that stops saving, a filter that stops filtering, a disabled
  field that becomes removed (CLAUDE.md: disable ≠ remove), a TabView pattern replaced by something else.
- The upgrade and the restyle landing as one unreviewable diff.
- Fonts or icons loaded from third-party CDNs at runtime (the mockup loads Google Fonts and Lucide from unpkg).
- Dark mode left half-done (unreadable text, white cards on dark backgrounds).

## Constraints

- Frontend: Vue 3 + TypeScript + Vite, Pinia, vue-router, vue-query, axios, Chart.js 4 (via `primevue/chart` in
  `PaymentIntakeWidget.vue` and the dashboard). `npm run build` (`vue-tsc --noEmit && vite build`) must pass.
- No frontend unit tests exist; verification is the type-check/build plus driving the running app in a browser.
- Every merge to main deploys to production via Coolify; each merge needs the owner's yes.
- Use "software house" wording in user-facing copy, never "agency".
- Current responsive behaviour must be kept: icon rail at 721–1100px, slide-in drawer with a menu button ≤720px
  (`App.vue`). The mockup has no responsive design; the mockup's `rail` nav variant is the tablet look.

## Approaches considered

- **A. Brand theme over PrimeVue 3.** Replace tokens in `app.css`, override ~15 components in one stylesheet, rebuild
  the shell. Lowest risk, but PrimeVue 3 is no longer developed and its themes hard-code values, so overrides fight
  the theme.
- **B. Upgrade to PrimeVue 4, then theme with a preset (chosen by the owner).** PrimeVue 4's styled mode takes a
  design-token preset (`definePreset` over Aura) with light/dark colour schemes built in, so the brand becomes
  configuration rather than overrides, and the app moves off an unmaintained major. Costs a migration that touches
  most screens (renamed components).
- **C. Unstyled PrimeVue with hand-written CSS.** Closest to the mockup, but rewrites styling for every component for
  little visible gain over B.

Owner picked **B** on 2026-11-11.

## Design

### Delivery: two PRs

1. **PR 1 — PrimeVue 4 upgrade, look unchanged in intent.** Upgrade `primevue` to 4.x, add `@primeuix/themes` (Aura),
   and migrate:
   - `Dropdown` → `Select`, `Calendar` → `DatePicker`, `InputSwitch` → `ToggleSwitch`,
     `TabView`/`TabPanel` → `Tabs`/`TabList`/`Tab`/`TabPanels`/`TabPanel` (the deprecated `TabView` still exists in
     v4; migrate anyway so part 2 isn't built on deprecated API), and any other renamed/removed API the build reports.
   - `main.ts`: remove the `primevue/resources` and theme CSS imports; configure `PrimeVue` with
     `theme: { preset, options: { darkModeSelector } }`.
   - `useTheme.ts`: switch dark mode from swapping theme CSS files to toggling the selector class PrimeVue's
     `darkModeSelector` names (keep the persisted user choice and the system default).
   - Every list view keeps its TabView status filtering behaviour exactly.
   Visual result: stock Aura blue, close to today. This PR is reviewable as "same app, new library".
   Owner decision 2026-11-11: PR 1 ships to production on its own (with the owner's merge yes) before PR 2, so
   production runs the stock Aura look in between; a smaller deploy is easier to roll back.
2. **PR 2 — Brand restyle.** Brand preset, global tokens, fonts, icons, shell, and per-screen styling.

### Visual system (from the mockup, `docs/redesign/revamp/` and `uploads/design-tokens.md`)

Light tokens: bg `#fcfbf6`, surface `#fff`, surface-2 `#f6f2ea`, ink `#501921`, muted `#75665f`,
muted-soft `#9d8a6e`, line `#e1d7c8`, tint `#ede7de`, red `#a51c30`, red-deep `#981c2e`, red-soft `#b7545e`,
gold `#d8bf84`, gold-bg `#f4ecd8`. Status pairs (text on background): green `#2f6b4f`/`#e4efe7`,
amber `#8a5a12`/`#f6ecd6`, red `#a51c30`/`#f3dfe1`, blue `#3b5a7a`/`#e3e9ef`. Sidebar bg `#501921`, fg `#f2dde0`,
active `#a51c30`.

Dark tokens: bg `#17110f`, surface `#1f1716`, ink `#fcfbf6`, line `#3b2d2b`, red `#c9414f`, sidebar `#120c0b`;
remaining dark values derived by the builder to keep the same contrast relationships (mockup gives only these).

Typography: Manrope (500/600/800) for headings, KPI figures, brand mark; DM Sans (400/500) for everything else;
`tabular-nums` on all amounts; a monospace stack for document numbers and IDs. Scale: page H1 Manrope 500 40px,
-1.4px tracking; eyebrow DM Sans 500 12px uppercase 1.5px tracking in red; card title Manrope 500 20px; table header
11px uppercase 1.2px tracking muted-soft. Fonts are self-hosted (bundled `@fontsource` packages), not Google Fonts.

Shapes: buttons are pills (radius 30px, height 42px); primary = red fill/white text, hover red-deep; secondary =
transparent with 1px line border, hover tint. Cards and tables: 1px line border, radius 0, no shadow. Status chips:
radius 2px, 10px uppercase, 1px tracking, tinted background + coloured text. Row hover = surface-2. Focus ring: inset
1px. No drop shadows anywhere.

Mapping into PrimeVue: the preset's `primitive`/`semantic` tokens carry colours (primary palette built around
`#a51c30`, surface palette around the cream/ink scale), font family and focus ring; per-component tokens carry the
shapes (button radius/height, datatable header style, tag radius, card radius/shadow). Anything the token API cannot
express goes in one small global stylesheet, not scattered scoped overrides. Status-to-colour mapping (Draft, Sent,
Paid, Overdue, Void, Partially paid, Pending manual review, etc.) lives in one shared helper used by every `Tag`.

Icons (builder-check ruling, Claude's call): PR 1 keeps PrimeIcons untouched. PR 2 replaces every app-authored
`pi pi-*` icon (~212 uses: nav, `Button` `icon` props, `Menu` items, plain markup) with Lucide (`lucide-vue-next`,
bundled), through one small shared wrapper so `Button`/`Menu` usages stay one-liners, then removes `primeicons` from
`package.json` and `main.ts`. One icon set, matching the mockup. PrimeVue components' own internal icons (select
chevrons, calendar, paginator) stay as PrimeVue ships them.

Shapes the mockup does not define (Claude's call): inputs, Select, DatePicker, Textarea use radius 2px with a 1px line
border, matching chips; Dialog, Menu, Select/DatePicker panels, Toast and Message use radius 0 with a 1px line border.
Overlays (dialogs, menus, dropdown panels, toasts) are the one exception to "no shadows": a soft shadow so they separate
from the page.

Mockup elements with no data behind them (Claude's call, restating the non-goals as a rule): drop them; never stub
them, never invent values; keep today's equivalent content in their place. Anything removed this way is listed in the
PR description so part 2 picks it up.

### App shell

- Sidebar 252px, burgundy (`deep` variant), sticky full height: "W" tile + "Wenhao Billing / Software house"; nav in the
  existing six groups with numbered eyebrows ("01 / Overview", "02 / Sales", …) and Lucide icons; active item red.
  No footer environment chip (Claude's call: there is no environment label in the frontend, and adding one would
  touch deploy config for no user value).
- Topbar: page title left; theme toggle and user chip (initials, name, role, chevron → existing user menu) right.
- Content max-width 1480px.
- Tablet (721–1100px): 76px icon rail (mockup's `rail` variant). Phone (≤720px): drawer, as today.

### Screens

- Fully designed in the mockup — match the mockup's layout and styling using existing data: Dashboard, Customers,
  Invoices, Invoice detail/edit, Chart of Accounts, Reports, FX Rates, Manual Review, MCP integration ("AI Assistant"
  in the mockup), Audit Log, Users.
- Placeholder-only in the mockup (Projects, Quotations, Vendors, Bills, Products & Services, Awaiting Finalization) and
  undesigned (all forms, detail pages, login/forgot/reset, dialogs, recurring, account, invites, payment intake
  widget): apply the system — page header with eyebrow + H1, bordered square cards, pill buttons, standard table and
  chip styles, the mockup's empty-state pattern (title, message, primary CTA).
- Dashboard: restyle today's cards and charts (counts, stacked revenue chart, AR pie, upcoming, recent reconciliation)
  in the new look; the revenue chart adopts variant A's styling (stacked by currency, unpaid part hatched if the
  existing data already splits paid/unpaid — it does today as faded segments). New analytics are part 2.
- Chart.js charts use the brand palette from the tokens (read at runtime so dark mode works).

### Mockup source of truth

`docs/redesign/revamp/Wenhao Billing Revamp.html` (rendered in a browser) is authoritative, with the `deep` nav and
`spacious` density settings; `.dc.html` and `export/` are the same design in other wrappers. For the revenue card,
`charts/` variant A. Tokens from `uploads/design-tokens.md`. (Claude's call.)

### Verification

- `npm run build` passes after each PR.
- PR 1 adds a small Playwright smoke suite under `frontend/e2e/` (Claude's call: the upgrade touches value types and
  events of Select/DatePicker/ToggleSwitch/Tabs, and a manual walk alone is a weak bar for "behaviour unchanged"). It
  runs in Docker (the official Playwright image) against the dev stack: log in, visit every route with no console
  errors, switch each list view's status tabs, and create-then-save one record per entity whose form uses a migrated
  component (invoice with a due date, bill, project, FX rate). It stays for parts 2 and 3. Not wired into CI (CI is
  kept off).
- Each PR: run the dev stack and walk every route in the browser in light and dark at desktop, tablet (900px) and
  phone (375px) widths; screenshot the 11 designed screens next to the mockup. PR 1 additionally exercises every
  migrated component in use: each Select, DatePicker, ToggleSwitch and every list view's status tabs, and saves at
  least one form per entity to prove behaviour is unchanged.
- No console errors on any route.

### Left to the builder

- Exact PrimeVue 4 minor and `@primeuix/themes` version; preset structure.
- Dark-mode values not given by the mockup.
- Layout of undesigned screens within the system above.
- Whether the brand "W" tile is CSS or an inline SVG.
