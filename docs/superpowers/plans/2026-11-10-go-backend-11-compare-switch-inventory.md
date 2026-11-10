# Go backend 11: comparison and switch-over inventory

Read-only survey, first taken 2026-11-09 on `feat/go-payments-recon`; **refreshed 2026-11-10 against `main` at
`9fb4981`** (plans 1–10 merged; `feat/go-send-finalized-only` treated as landed). It supports plan 11: the
comparison of both backends, running side by side on Coolify, and the switch-over. Python file:line cites are
stable (`backend-python/` is frozen); Go cites were re-checked on `9fb4981` and name the symbol where a line may
move. "Spec" is `docs/superpowers/specs/2026-11-08-go-backend-design.md`; "PN n" is note n of
`backend/PORT-NOTES.md` (159 notes). Nothing here was run against Coolify, production or any remote database.

## §1 What exists for the comparison and the switch, and what is missing

### Exists

| Piece | Where | State |
|---|---|---|
| Go production image (distroless, port 8000, self-probing healthcheck) | `backend/Dockerfile:4-14` | `CGO_ENABLED=0 go build -mod=vendor -trimpath -ldflags="-s -w"` builds `./cmd/api` only (`:7`); `EXPOSE 8000` (`:11`); `HEALTHCHECK ... CMD ["/api","healthcheck"]` every 30 s (`:13`); `ENTRYPOINT ["/api"]` (`:14`). No `Dockerfile.prod` for Go. |
| Subcommands | `backend/cmd/api/main.go:188-215` (dispatch), header `:1-15` | `serve` (no argument), `adopt-baseline` (`adoptBaseline`, `:363`), `set-password <email>` (stdin, one line; `setPasswordCmd`, `:432`), `issue-session <email>` (8 h admin token, writes nothing; `issueSessionCmd`, `:441`), `healthcheck` (`:378`), `openapi` (`:396`). |
| `adopt-baseline` and the serve refusal | `backend/migrations/adopt.go:16` (`AlembicHead = "0028_business_npwp"`), `:20` (`ErrNotAdopted`), `CheckAdopted` `:45`, `Adopt` `:85` | Adopt checks the Alembic head and the schema snapshot, records goose versions 0 and 1, refuses existing goose history. Serve refuses an un-adopted Alembic DB, then runs goose (`prepareDatabase`, `main.go:348`). |
| Goose migrations | `backend/migrations/00001_baseline.sql` … `00007_oauth_refresh_user_idx.sql` | 00002 `users.session_epoch`, 00003 `login_attempts`, 00004 `scheduled_job_runs`, 00005 `oauth_spent_codes`, 00006 `mcp_spent_confirm_tokens`, 00007 index `oauth_refresh_tokens(user_id)`. All additive. |
| Scheduler | `backend/internal/jobs/` (`jobs.go`, `runner.go`, `slots.go`); `startScheduler` `main.go:314`, `scheduledJobs` list | Four jobs, UTC slots: recurring 02:00, usage-lock 03:00, hosting 04:00, FX Monday 05:00 (`jobs.go:21-31`). Lock key `crc32(job_id)` (`slots.go:29-31`) = Python's (`backend-python/app/workers/scheduler.py:41`). Runs a slot once (row in `scheduled_job_runs`), catches up the latest slot ≤ 24 h old (`runner.go:28`, `MaxSlotAge`). Off unless `SCHEDULER_ENABLED=true` (Go default false, `config.go:103`). |
| Email | `backend/internal/email/resend.go`; `pickSender` `main.go:228` | Resend when `RESEND_API_KEY` is set, else the logging sender. `IdempotencyKey` (`resend.go:47-54`): document id + SHA-256 of the message + 10-minute UTC window. Python sends no idempotency key. |
| Hosting / Cloudflare | `backend/internal/hosting/`; `newServices` `main.go:93` | Restore on PAID and the 04:00 enforcement job. An empty `CLOUDFLARE_API_TOKEN` skips Cloudflare (`cloudflare.go:72`). |
| MCP and OAuth | `backend/internal/mcp`, `backend/internal/oauth` (`routes.go:57-63`); `oauthRoot` `main.go:172` | Mounted only when `MCP_ENABLED` (Go default true). Paths `/.well-known/oauth-authorization-server`, `/.well-known/oauth-protected-resource`, `/oauth/{register,authorize,authorize/consent,token,revoke}`, `/mcp`. Tokens are HS256 with Python's claim order (`oauth/tokens.go:55-79`), signed with `MCP_TOKEN_SECRET`, refresh rotation over Python's `oauth_refresh_tokens`. Host/Origin guard from `MCP_PUBLIC_BASE_URL` (`mcp/guard.go`). |
| Baseline tooling | `backend/port/make-baseline.sh`, `clean-dump.pl`, `backend/migrations/testdata/` | Local dev stack only. |
| Route and test inventories | `backend/port/inventory.py`, `backend/ROUTES.md`, `backend/TESTS.md` | ROUTES: 107 `/api/v1` + 7 OAuth/discovery + `/mcp`, `/mcp/`: **all `ported`**. TESTS: 520 functions / 527 collected; 503 `ported` (each with a mutation), 13 not ported (each with a reason), **4 `todo`** (plan 11 Task 1). |
| Local side-by-side service | `docker-compose.yml:146-159` (`api-go`, profile `tools`, `${GO_API_HOST_PORT:-8001}:8000`, `ENVIRONMENT: development`, `GO_DATABASE_URL` defaulting to the nonexistent `billing_go_scratch`) | Scratch database only (`backend/README.md`). |
| nginx upstream variable | `frontend/nginx.conf.template:9-12`; default `backend:8000` in `frontend/Dockerfile.prod:29` | In Coolify a **literal** `BACKEND_UPSTREAM: wenhao-billing-api:8000` (`docker-compose.coolify.yml:91`). |
| Checks | `scripts/check.sh` | Compose paths, frozen Python tests (`:25-43`), frontend build, frontend `return_to` check, Go lint + generated + test (`backend/dev`, `-p 8 -parallel 8`). No comparison step. |

### Missing

1. **`backend/cmd/compare`** and `backend/internal/compare`: `backend/cmd/` holds only `api/`.
2. **`backend/port/allowed-diffs.yaml`, `compare-corpus.yaml`, `compare-writes.yaml`**: `backend/port/` holds only `clean-dump.pl`, `inventory.py`, `make-baseline.sh`.
3. **A Go service in `docker-compose.coolify.yml`**: only `wenhao-billing-api` (Python, `:21-74`) and `wenhao-billing-web` (`:76-100`).
4. **`BACKEND_UPSTREAM` as a Coolify variable**: literal at `:91`.
5. **Separate scheduler variables**: Python's `SCHEDULER_ENABLED` is not set in the Coolify compose (Python default true, `backend-python/app/config.py:28`). Coolify variables are shared across the compose's services, so the two backends need distinct interpolation names (`PY_SCHEDULER_ENABLED`, `GO_SCHEDULER_ENABLED`).
6. **`depends_on` for the web container** names only the Python API (`:92-94`); nginx resolves `server ${BACKEND_UPSTREAM}` at start-up. Keep it on Python until Python is removed.
7. **A local Python instance for a second database**: the dev `backend` service is pinned to `billing` (`docker-compose.yml:23`) with the scheduler on by default; the compare needs a scheduler-off Python on a dump copy.
8. **An FX stub** for local runs.
9. **Production `TimeZone` check** (spec "Dates") not done; the Go pool sets no `TimeZone` (grep: none in `backend/internal`, `backend/cmd`).
10. **`docs/runbooks/`**: does not exist; no switch or way-back runbook, rehearsal script, or memory procedure.
11. **4 `TESTS.md` `todo` rows**: `test_bill_payment_schema_still_requires_positive_amount`, `test_fix_recurring_cycle_keys_plan_and_apply`, `test_fix_script_ignores_void_and_fixes_live_key`, `test_create_app_does_not_enable_sentry_under_pytest`.

## §2 Frontend calls: the comparison corpus

Client: axios `baseURL: "/api/v1"`, `withCredentials: true` (`frontend/src/api/client.ts:3-6`); any 401 except
`/auth/me` signs the user out (`client.ts:15-29`). Decimals in bodies go as JS numbers (`Number(...)`, e.g.
`InvoiceFormView.vue:657-658`). Every route below is `ported` on `main` (`backend/ROUTES.md`). R = read (GET, safe for the production read
comparison), W = write (local two-copy comparison only).

### Auth and users (all ported, `auth`)
| R/W | Call | Shape | Site |
|---|---|---|---|
| W | POST `/auth/login` | `{email,password,remember}` | `stores/auth.ts:29` |
| R | GET `/auth/me` | — | `stores/auth.ts:35` |
| W | POST `/auth/logout` | — | `stores/auth.ts:43` |
| W | PATCH `/users/me` | `{email?, display_name?: string\|null}` | `stores/auth.ts:46-48`; `AccountView.vue:64` sends `display_name:null` (PN 23) |
| W | POST `/auth/change-password` | `{current_password,new_password}` | `stores/auth.ts:53-57` |
| W | POST `/auth/forgot-password` | `{email}` (sends mail) | `stores/auth.ts:62-65` |
| W | POST `/auth/reset-password` | `{token,new_password}` | `stores/auth.ts:69-72` |
| R | GET `/users` | — | `UsersView.vue:23`, `UserResetPasswordView.vue:24` |
| W | POST `/users` | `{email,password,role:"admin"}` | `UserInviteView.vue:14,18` |
| W | POST `/users/{id}/reset-password` | `{password}` | `UserResetPasswordView.vue:33` |

### Chart of accounts, FX (ported: `accounts`, `fx`)
| R/W | Call | Shape | Site |
|---|---|---|---|
| R | GET `/accounting/chart-of-accounts` | — | `ChartOfAccountsView.vue:26`, `BillFormView.vue:124`, `ItemsView.vue:48`, `ItemFormView.vue:174`, `ItemDetailView.vue:40` |
| R | GET `/accounting/chart-of-accounts/{id}` | — | `AccountFormView.vue:44` |
| W | POST `/accounting/chart-of-accounts` | account form | `AccountFormView.vue:65` |
| W | PATCH `/accounting/chart-of-accounts/{id}` | form, or `{active:true}` | `AccountFormView.vue:60`, `ChartOfAccountsView.vue:50` |
| W | DELETE `/accounting/chart-of-accounts/{id}` | — | `ChartOfAccountsView.vue:44` |
| R | GET `/accounting/base-currency` | — | `FxRatesView.vue:37`, `FxRateNewView.vue:21` |
| R | GET `/accounting/fx-rates` | — | `FxRatesView.vue:46` |
| W | POST `/accounting/fx-rates` | `{from_currency,to_currency,rate,as_of_date,source}` | `FxRateNewView.vue:41-48` |
| W | POST `/accounting/fx-rates/sync` | — (calls Frankfurter) | `FxRatesView.vue:58` |
| W | DELETE `/accounting/fx-rates/{rate_id}` | — | `FxRatesView.vue:50` |

### Reports (ported: `reports`)
| R | GET `/accounting/reports/{trial-balance,balance-sheet,fx-revaluation,ar-aging,ap-aging}` | `?as_of=YYYY-MM-DD` (browser's UTC date, `toISOString().slice(0,10)`) | `ReportsView.vue:101-142` |
|---|---|---|---|

### Business profile (ported: `business`)
| R/W | Call | Shape | Site |
|---|---|---|---|
| R | GET `/business-profile` | — | `AccountView.vue:125`, `InvoiceFormView.vue:346` |
| R | GET `/business-profile/payment-accounts` | — | `AccountView.vue:138` |
| W | PUT `/business-profile` | every field, blanks as `null` | `AccountView.vue:174-177` (PN 24) |
| W | PUT `/business-profile/payment-accounts` | array of accounts, one `is_default` | `AccountView.vue:178` |

### Customers (ported: `customers`, `invoices`)
| R/W | Call | Shape | Site |
|---|---|---|---|
| R | GET `/customers` | none, or `?active=true` | `CustomersView.vue:51-52`; unfiltered at `AwaitingFinalizationView.vue:37`, `InvoiceFormView.vue:235`, `InvoicesView.vue:88`, `ProjectFormView.vue:82`, `ProjectsView.vue:69`, `QuotationDetailView.vue:75`, `QuotationFormView.vue:107`, `QuotationsView.vue:66` |
| R | GET `/customers/balances` | — | `CustomersView.vue:59` |
| R | GET `/customers/{id}` | — | `CustomerDetailView.vue:93`, `ProjectDetailView.vue:117` |
| R | GET `/customers/{id}/billing-summary` | — | `CustomerDetailView.vue:99` |
| R | GET `/customers/{id}/invoices` | — | `CustomerDetailView.vue:105` |
| W | POST `/customers` | `buildCustomerPayload` (blanks `null`, `default_currency` default `IDR`) | `CustomerNewView.vue:28`; builder `components/customerForm.ts:84-` |
| W | PATCH `/customers/{id}` | same, or `{active:true}` | `CustomerDetailView.vue:268-271`, `CustomersView.vue:77` |
| W | DELETE `/customers/{id}` | — | `CustomersView.vue:71` |

### Vendors (ported)
| R | GET `/vendors` | none or `?active=true` | `VendorsView.vue:35-36`, `BillsView.vue:68`, `BillFormView.vue:113` (`active:true`) |
|---|---|---|---|
| R | GET `/vendors/{id}` | — | `VendorDetailView.vue:26`, `VendorFormView.vue:71` |
| W | POST / PATCH `/vendors[/{id}]` | form with `""` keys dropped, `active` | `VendorFormView.vue:93-107`; `{active:true}` `VendorsView.vue:47` |
| W | DELETE `/vendors/{id}` | — | `VendorsView.vue:41` |

### Items (ported)
| R | GET `/items` | — | `ItemsView.vue:43`, `InvoiceFormView.vue:253`, `QuotationFormView.vue:112` |
|---|---|---|---|
| R | GET `/items/{id}` | — | `ItemDetailView.vue:35`, `ItemFormView.vue:135` |
| W | POST / PATCH `/items[/{id}]` | `{name,item_type,default_currency,active,is_sold,is_purchased,is_hosting, sku?,description?,default_unit_price?,revenue_account_id?,default_purchase_price?,expense_account_id?,hosting_domain?...}` | `ItemFormView.vue:194-236`; `{active:true}` `ItemsView.vue:63` |
| W | DELETE `/items/{id}` | — | `ItemsView.vue:58` |

### Projects (ported)
| R | GET `/projects` | repeated `?status=` (`ACTIVE`/`ON_HOLD`/none); or `?customer_id=&status=ACTIVE`; or `?status=ACTIVE` | `ProjectsView.vue:50-63`, `InvoiceFormView.vue:243-245`, `QuotationFormView.vue:120-122`, `BillFormView.vue:119` |
|---|---|---|---|
| R | GET `/projects/{id}`, `/projects/{id}/summary`, `/projects/{id}/invoices` | — | `ProjectDetailView.vue:63,69,75`, `ProjectFormView.vue:87` |
| W | POST / PATCH `/projects[/{id}]` | `{customer_id,code(upper),name,currency,status,contract_value,start_date,end_date,notes, active (edit)}` | `ProjectFormView.vue:135-156` |

### Invoices (ported: `invoices`, `payments`)
| R/W | Call | Shape | Site | Go |
|---|---|---|---|---|
| R | GET `/invoices` | repeated `?status=` (`OPEN,SENT,PARTIAL` / `DRAFT` / none) | `InvoicesView.vue:68-82` | ported |
| R | GET `/invoices/awaiting-finalization` | — | `AwaitingFinalizationView.vue:52` | ported |
| R | GET `/invoices/{id}` | — | `InvoiceFormView.vue:261` | ported |
| R | GET `/invoices/{id}/payments` | — | `InvoiceFormView.vue:517` | ported |
| R | GET `/invoices/{id}/pdf` | `window.open`, binary | `InvoiceFormView.vue:467` | ported |
| R | GET `/invoices/recurring-templates/rows` | — | `RecurringInvoicesView.vue:91`, `InvoiceFormView.vue:361` | ported |
| R | GET `/invoices/recurring-templates/{id}` | — | `InvoiceFormView.vue:301` | ported |
| W | POST `/invoices` | `buildPayload`: customer, project, `invoice_type:"MILESTONE"`, currency, number, dates, terms, notes, internal_note, discount, withholding, `line_items[{item_id,description,quantity,unit_price,position}]` | `InvoiceFormView.vue:651-680`, `:728` | ported |
| W | PATCH `/invoices/{id}` | same without `invoice_type` | `InvoiceFormView.vue:726` | ported |
| W | POST `/invoices/recurring-template` / PUT `/invoices/recurring-templates/{id}` | template payload + `schedule{frequency,interval,start_date,end_mode,end_date,end_after_cycles}` | `InvoiceFormView.vue:682-723` | ported |
| W | PATCH `/invoices/recurring-templates/{id}` | `{action}` (PAUSE/RESUME/END_NOW) | `RecurringInvoicesView.vue:58-64` | ported |
| W | POST `/invoices/{id}/finalize` | — | `AwaitingFinalizationView.vue:56` | ported |
| W | POST `/invoices/{id}/void` | `{reason}` | `AwaitingFinalizationView.vue:62` | ported |
| W | POST `/invoices/{id}/mark-paid` | — | `InvoiceFormView.vue:445` | ported |
| W | POST `/invoices/{id}/record-payment` | `{amount,wht_amount,payment_date,payer_name,payer_reference,notes}` | `InvoiceFormView.vue:412-424`, `InvoicesView.vue:168-179` | ported |
| W | POST `/invoices/{id}/payments/{pid}/send-receipt` | `{to_email,cc_email}` (sends mail) | `InvoiceFormView.vue:396,525`, `InvoicesView.vue:150` | ported |
| W | POST `/invoices/{id}/send` | `{to_email,cc_email,subject,message}` (sends mail) | `InvoiceFormView.vue:470-478` | ported |

### Quotations (ported: `quotations`)
| R/W | Call | Shape | Site | Go |
|---|---|---|---|---|
| R | GET `/quotations` | repeated `?status=` (`DRAFT,SENT` / `ACCEPTED,INVOICED` / none); `?customer_id=`; `?project_id=` | `QuotationsView.vue:47-60`, `CustomerDetailView.vue:122-124`, `ProjectDetailView.vue:92-94` | ported |
| R | GET `/quotations/{id}` | — | `QuotationDetailView.vue:70`, `QuotationFormView.vue:133` | ported |
| R | GET `/quotations/{id}/pdf` | `window.open` | `QuotationDetailView.vue:134`, `QuotationFormView.vue:267` | ported |
| W | POST / PATCH `/quotations[/{id}]` | `{customer_id,project_id,currency,quotation_number,po_so_number,issue_date,valid_until,payment_terms,notes,discount_type,discount_value,line_items}` | `QuotationFormView.vue:204-235` | ported |
| W | POST `/quotations/{id}/mark-sent`, `/decline`, `/void` | no body | `QuotationDetailView.vue:139,198,209` | ported |
| W | POST `/quotations/{id}/accept` | `{accepted_by:null}` | `QuotationDetailView.vue:187` | ported |
| W | POST `/quotations/{id}/convert-to-invoice` | `{}` | `QuotationDetailView.vue:229` | ported |
| W | DELETE `/quotations/{id}` | — | `QuotationDetailView.vue:219` | ported |
| W | POST `/quotations/{id}/send` | `{to_email,cc_email,subject,message}` (sends mail) | `QuotationDetailView.vue:150-157` | ported |

### Bills (ported)
| R | GET `/bills` | repeated `?status=` (`DRAFT` / ... / none) | `BillsView.vue:50-62` |
|---|---|---|---|
| R | GET `/bills/{id}` | — | `BillFormView.vue:138` |
| W | POST / PATCH `/bills[/{id}]` | `{vendor_id,project_id,bill_number,po_number,currency,issue_date,due_date,payment_terms,notes,line_items[{expense_account_id,description,quantity,unit_price,position}]}` | `BillFormView.vue:236-266` |
| W | POST `/bills/{id}/record-payment` | `{amount,payment_date,payer_reference,notes}` | `BillFormView.vue:195-205` |

### Payments, recon, stats (ported: `payments`, `stats`)
| R/W | Call | Shape | Site |
|---|---|---|---|
| R | GET `/payments/audit-log` | `?action=&payer=&from_date=&to_date=` (each optional) | `AuditLogView.vue:46-58` |
| R | GET `/payments/awaiting-review` | — | `ManualReviewView.vue:45` |
| R | GET `/payments/{id}/log` | — | `ManualReviewView.vue:51` |
| W | POST `/payments/{id}/manual-review` | `{invoice_id, adjustment_type:"NONE"}` | `ManualReviewView.vue:57-60` |
| W | POST `/payments/{id}/reverse` | `{reason}` | `ManualReviewView.vue:71-73` |
| R | GET `/stats/dashboard` | — | `PaymentIntakeWidget.vue:78` |
| R | GET `/stats/revenue-monthly` | — | `PaymentIntakeWidget.vue:96` |
| R | GET `/stats/invoices-summary` | — | `InvoicesView.vue:94` |

Not called by the frontend, all ported (compare needs its own cases):
- `POST /invoices/milestone`, `GET /invoices/recurring-templates`, `POST /invoices/recurring/{id}/trigger`, `POST /invoices/usage/{id}/lock`, `DELETE /invoices/{id}`;
- `GET /accounting/fx-rates/latest`, `POST /bills/{id}/void`, `DELETE /projects/{id}`, `GET /payments/{id}`, `POST /payments/intake/{webhook,email}`;
- the OAuth and discovery routes, and `/mcp` (`backend/ROUTES.md` "OAuth and discovery", "/mcp").

The read comparison takes the GETs among these, plus the two `/.well-known/oauth-*` documents, which print the same issuer when `MCP_PUBLIC_BASE_URL` is the same. `/mcp` is JSON-RPC over POST, so it stays out of the read comparison.

Writes that leave the machine are excluded from the local write comparison:
- forgot-password, invoice send, quotation send and send-receipt (Resend);
- `fx-rates/sync` (Frankfurter);
- a PAID transition on an invoice with hosting lines (the Cloudflare restore, `hosting.Restorer`).

They could not leave it anyway: `RESEND_API_KEY` and `CLOUDFLARE_API_TOKEN` are unset, and FX points at a stub.

## §3 Allowed comparison differences already recorded

Explicitly labelled "allowed difference for compare":
- Spec "The comparison":
  - decimal strings are equal by value;
  - timestamps are equal as instants;
  - the error body shape: Python's `{"detail"}` vs problem+json with the same `detail`;
  - plus the PORT-NOTES entries.
- PN 14: Go may accept email edge forms (quoted local parts, odd dots, a numeric TLD, domains over 253 characters).
- PN 24: the business-profile `updated_at` moves on a PUT that changes nothing.
- PN 50: `expected_cash` is `"0"` in Python and `"0.0000"` in Go. PN 63: the same for the project summary's empty sums.
- PN 76: quotation `last_sent_at` and `accepted_at` gain a `Z`. PN 108: recent-activity `created_at` gains a `Z`.
- PN 99: `0` and `0.0000` are equal, and so are `-0.0000` and `0.0000`.
- PN 104: `avg_days_to_pay` is `10` in one and `10.0` in the other.
- PN 86, 87: intake responses print amounts at 4 places and confidence at 2; an intake 400 for invalid JSON carries Go's parser text.

Deliberate Go/Python differences the comparison will also meet (the diff file needs each one, with its note):
- Status or sentence changes on bad input: PN 10, 11, 15, 16, 17, 19, 20, 21, 25, 30, 36, 37, 38, 55, 59, 60, 61, 75, 86.
- Concurrency and locking (a 409 instead of a 500, a single result): PN 27, 31, 34, 35, 56, 57, 58.
- Session-epoch sign-outs: PN 13, 22. The business profile GET writes nothing: PN 12.
- FX sync: the 502 text and what it covers: PN 28, 32, 33.
- Matching: Go holds a tie between equal top candidates, where Python auto-cleared it (PN 6, a PRD safe-stop fix).
- The balance sheet honours `as_of` (PN 97); one open-AR rule (PN 98).
- "Today" is UTC in Go (PN 5, and 34, 41, 45, 49, 64). It equals Python's only if the Python container's clock is UTC.
- Go's order is deterministic where Python's was undefined: PN 40, 41, 44, 47, 53, 78, 100. These compare as multisets on those routes only.
- Deleting a referenced invoice is a 409 (PN 36). A project delete counts quotations (PN 62).
- Bill void `reason` edge forms (PN 74); bill record-payment float printing (PN 73).
- **Invoice send refuses a DRAFT (400 `invoice_not_finalized`) and a template (400 `invoice_template`).** Python sent DRAFTs (PN 112, owner decision 2026-11-10). Sends are in neither comparison, but the MCP `send_invoice_email` tool description differs too.
- OAuth:
  - authorize honours the session's expiry and epoch (PN 148);
  - redirect URIs are validated, and every OAuth 400 has the OAuth shape (PN 150);
  - control characters in `client_name` are refused (PN 159);
  - authorization codes and confirm tokens are single use (PN 145, 147).
- Errors: Python's 500s become problem+json, and invoice and quotation send failures are 502 (spec "Email").
- PDFs come from a new renderer (Maroto), so the bytes never match: compare only status, `Content-Type` and `Content-Disposition`. SVG logos print the default logo (PN 124; the owner will upload a PNG or JPEG).

## §4 Switch-over prerequisites and their status on `main` (`9fb4981`)

| # | Prerequisite | Status | Source |
|---|---|---|---|
| 1 | Every `ROUTES.md` route ported; every `TESTS.md` row ported (with a mutation) or not ported (with a reason) | Routes done; **4 test rows `todo`** (plan 11 Task 1) | spec "Done"; `backend/ROUTES.md`; `backend/TESTS.md` |
| 2 | Hosting restore and enforcement ported | Done (plan 9) | `hosting.Restorer`; `jobs.HostingEnforcement` |
| 3 | Production `TimeZone` checked with `SHOW timezone` before the comparison. If it is not UTC, the Go pool sets `TimeZone=UTC` and existing rows are read from the server zone | Not done (OWNER YES, Task 6.1) | spec "Dates" |
| 4 | No default intake secrets in production: Go refuses an empty or `dev-…-change-me` value unless `ENVIRONMENT` is `development` or `test` | Code done (`config.go:181-186`); Coolify names checked in Task 7.1 | PN 90 |
| 5 | Way back rehearsed locally before the switch. Python cannot read argon2id; reset with `scripts.seed_admin` | Not done (Task 5) | spec "Switch-over" 4 |
| 6 | The owner runs `api set-password` after the switch (a bcrypt hash gets 401 `invalid credentials` in Go) | Not done (OWNER YES, Task 9.2) | spec "Auth" |
| 7 | `api adopt-baseline` on production before any Go serve there | Not done (OWNER YES, Task 8.1) | `backend/migrations/adopt.go:16-20`, `:85` |
| 8 | Scheduler: Go off until the switch, then on; Python off after; the same lock keys; never both on (Python writes no slot rows) | Code done. Distinct Coolify variables come in Task 5, which also proves the first-start catch-up | spec "Background work"; PN 119, 120 |
| 9 | The read comparison on production is clean or every difference is allowed; the local read and write comparison runs on a restored dump | Not done (Tasks 2–4, 6, 8.3) | spec "The comparison" |
| 10 | Go idle memory measured on the server against ~30 MB | Not done (Task 8.2, 8.4) | spec "Switch-over" 1 |
| 11 | Browser check against Go locally before the switch, and on production after it | Not done (Task 5, 9.3) | spec "Switch-over" 3 |
| 12 | MCP connector working against Go | Code done (plan 10). One reconnect is expected after `set-password` (PN 157); Task 9.3 | spec "MCP connector" |
| 13 | The one frontend edit (`return_to` under `/oauth/`) | Done (plan 10; `scripts/check.sh` "frontend return_to check") | PN 9 |
| 14 | Goose migrations are additive, so Python runs beside them | Done for 00002–00007 (new tables, one column with a default, one index) | spec "Database" |
| 15 | Client address: `CF-Connecting-IP`, else the left-most `X-Forwarded-For` (nginx appends, `frontend/nginx.conf.template:28`) | Accepted by the spec | spec "Auth" |
| 16 | The cookie is Secure unless `ENVIRONMENT` is `development` or `test`; Go defaults to `ENVIRONMENT=production` | Done (`principal.go:98`, `config.go:70`, `:130`) | spec "Auth" |
| 17 | Schema drift check in adopt | Done (`adopt.go:85`) | spec "Database" |
| 18 | One week after the switch with no way back: delete `backend-python/` and the Python service, drop `alembic_version`, update CLAUDE.md and AGENTS.md | Not done (OWNER YES, Task 10) | spec "Switch-over" 5 |
| 19 | Every step that deploys or changes production waits for the owner's yes; merging to `main` deploys | Standing rule | spec "Switch-over" |
| 20 | Post-switch fixes from the PORT-NOTES "After the switch" lines | Collected in the plan's "After the switch" table | `backend/PORT-NOTES.md` |
| 21 | PN 82: real receipt renderer and Resend sender before the switch | Done (plan 9: `payments.PDFReceiptRenderer`, `pickSender`) | PN 82 |
| 22 | PN 35: duplicate recurring children already in production data | Found on the dump in Task 6.5 (`scripts.fix_recurring_cycle_keys` dry run); voided after the switch (OWNER YES) | PN 35 |
| 23 | Only finalized invoices can be sent | Landing on `feat/go-send-finalized-only`; treated as landed | PN 112 |

## §5 Production configuration surface

### Environment variables, name by name

- **Python:** `backend-python/app/config.py`. pydantic-settings maps each field to its upper-case name (`:8`).
- **Go:** env tags at `backend/internal/platform/config.go:70-103`, checks at `:137-210`. The only other env read is `PORT` in `healthcheck` (`backend/cmd/api/main.go:379`).
- **Coolify** sets them at `docker-compose.coolify.yml:26-55`.

Every Go name below exists on `main`. The Go service in Coolify takes the Python service's value for each.

| Variable | Python | Go (`config.go`) | Coolify (Python service) | Note for the Go service |
|---|---|---|---|---|
| `DATABASE_URL` | `:10-25` | `:71` required; `pgxURL` (`:269`) rewrites `postgresql+psycopg://` | `:29` | same value |
| `SCHEDULER_ENABLED` | `:28` default true | `:103` default **false** | not set (true) | `${GO_SCHEDULER_ENABLED:-false}`; Python gets `${PY_SCHEDULER_ENABLED:-true}` |
| `SESSION_SECRET` | `:30` | `:72` required, at least 32 bytes (`:137`) | `:31` `SERVICE_BASE64_64_SESSION` | same value; the cookie formats still differ |
| `SESSION_MAX_AGE_SECONDS`, `SESSION_REMEMBER_MAX_AGE_SECONDS` | `:33`, `:39` | `:73-74`, same defaults | — | none |
| `WEBHOOK_HMAC_SECRET`, `EMAIL_WEBHOOK_TOKEN` | `:41-42` dev defaults | `:85-86`; empty or default refused outside dev and test (`:181-186`) | `:32-33` | same values (PN 90) |
| `CORS_ORIGINS` | `:44` JSON list | `:75` JSON or comma list | `:36` | same value |
| `APP_BASE_URL` | `:63` | `:76` | `:37` | same value |
| `BASE_CURRENCY` | `:48` IDR | `:81`, default IDR, blank refused (`:154`) | not set | none |
| `SMTP_REPLY_TO` | `:59` default `wenhao.kho@gmail.com` | `:98`, same default when unset (`:149-152`) | not set | none |
| `SMTP_*` (other), `PDF_ENGINE` | `:52-58`, `:83` | dropped (PN 131) | not set | none |
| `MCP_ENABLED` | `:70` | `:87` default true | `:38` | same value |
| `MCP_CONFIRM_SECRET`, `MCP_TOKEN_SECRET` | `:72`, `:74`, at least 16 | `:88-89`; under 16 bytes refused everywhere, defaults refused outside dev and test (`:190-196`) | `:39-40` | **same values**, so tokens verify on both backends |
| `MCP_ACCESS_TOKEN_TTL_SECONDS`, `MCP_REFRESH_TOKEN_TTL_SECONDS` | `:75-76` | `:90-91`, must be >0 (`:205`) | `:42-43` | same values |
| `MCP_PUBLIC_BASE_URL` | `:78` | `:92` default `http://localhost:8000` (not refused) | `:41` | **must** be the public origin, or the Host/Origin guard and the OAuth metadata are wrong |
| `RESEND_API_KEY`, `RESEND_FROM_EMAIL`, `RESEND_FROM_NAME` | `:96-98` | `:93-95`; an empty key means the logging sender | `:47-49` | same values |
| `RESEND_BASE_URL`, `RESEND_TIMEOUT_SECONDS` | `:99-100` | `:96-97` | not set | none |
| `CLOUDFLARE_API_TOKEN` | `:65` | `:99`; empty means Cloudflare is skipped | `:51` | same value |
| `CLOUDFLARE_API_BASE_URL`, `CLOUDFLARE_TIMEOUT_SECONDS` | `:66-67` | `:100-101` | not set | none |
| `FX_PROVIDER_BASE_URL`, `FX_PROVIDER_TIMEOUT_SECONDS`, `FX_SYNC_CURRENCIES` | `:86-91` | `:82-84` | not set | none |
| `SENTRY_DSN`, `SENTRY_ENVIRONMENT`, `SENTRY_TRACES_SAMPLE_RATE` | `:105-108` | `:77-79` | `:53-55` | same values |
| `ENVIRONMENT` | — | `:70` default `production` | not set | leave unset |
| `PORT` | — (uvicorn `--port 8000`) | `:80` default 8000 | not set | none |
| `BACKEND_UPSTREAM` (web) | — | — | literal `:91` | `${BACKEND_UPSTREAM:-wenhao-billing-api:8000}` |

### Coolify compose today

- `wenhao-billing-api` (Python):
  - builds `./backend-python` from `Dockerfile.prod` (`docker-compose.coolify.yml:21-25`);
  - has no public domain;
  - its healthcheck GETs `http://localhost:8000/docs` (`:56-67`);
  - its command is `alembic upgrade head && uvicorn … --port 8000` (`:69-74`);
  - runs the scheduler in-process, on by default.
- `wenhao-billing-web` (nginx):
  - builds `./frontend` from `Dockerfile.prod` (`:76-84`);
  - `SERVICE_FQDN_WEB_80` (`:89`);
  - `BACKEND_UPSTREAM: wenhao-billing-api:8000` (`:91`);
  - `depends_on: wenhao-billing-api: service_healthy` (`:92-94`).
- The shared Postgres sits outside this stack, on a shared Docker network.
- nginx proxies these to the one upstream:
  - `^/(api|oauth|\.well-known)/` (`frontend/nginx.conf.template:34`);
  - `^/(docs|redoc|openapi\.json)$` (`:37`);
  - `^/mcp(/|$)`, unbuffered, with a 1 h read timeout (`:43`).

  **`/mcp`, `/oauth` and `/.well-known` need no nginx change:** switching the upstream moves them along with `/api`. Go answers `/docs` and `/openapi.json` (Huma defaults, `platform/server.go:84`). `/redoc` is Python-only and returns 404 after the switch.

### How nginx points at Go (plan 11 Task 5)

1. Add `wenhao-billing-api-go`: build `./backend`, no `SERVICE_FQDN_*`, and an exec-form healthcheck `["CMD","/api","healthcheck"]` (the image is distroless).
2. Interpolate `BACKEND_UPSTREAM`, `PY_SCHEDULER_ENABLED` and `GO_SCHEDULER_ENABLED`.
3. Switch: `BACKEND_UPSTREAM=wenhao-billing-api-go:8000` plus the two flags, in one redeploy. Go listens on 8000, as Python does.
4. `depends_on` stays on Python while Python runs (the week after the switch). Task 10 moves it to Go.

## §6 Risks and rollback points

Spec risks and rollback points:
- The side-by-side deploy is the first production contact. Python keeps serving, and Go's scheduler is off.
- Rollback point A: before the switch, revert the compose PR. Nothing user-facing has changed, and Go's tables stay, harmless.
- The switch is one move for every route, not route by route.
- Rollback point B, the way back: `BACKEND_UPSTREAM` back to Python, the scheduler flags swapped, a redeploy. It is rehearsed locally first (Task 5).
- After the switch, the owner's hash is argon2id, which Python cannot read.
  - Python's login uses passlib's bcrypt-only `verify` (`backend-python/app/api/v1/routers/auth.py:38`, `:46`), so a login before the reset is likely a 500 (inference).
  - Reset with `scripts.seed_admin`. It takes the password in argv (`backend-python/scripts/seed_admin.py:35-38`), so the runbook reads it with `read -rs` inside the container.
- No session carries over. Both backends use the `billing_session` cookie with different formats, so everyone signs in again at the switch and at the way back.
- Python ignores `users.session_epoch`.
- MCP connectors:
  - tokens work on both backends, in both directions (same claims, secret and tables);
  - but `set-password` at the switch revokes the owner's refresh tokens (PN 157), so the connector reconnects once, at most about an hour after the switch;
  - clients with a plain `http://` or custom-scheme redirect must register again (PN 150);
  - on the way back nothing is revoked.
- The way back is ruled out once `backend-python/` and `alembic_version` are gone.
- The PDF design changes; the owner reviews the first versions (PN 126, 127).
- Sends stay synchronous. Go's Resend idempotency key stops a send retried inside 10 minutes from going out twice. Python sends no key, so a send retried across the switch is not deduplicated (negligible).

From the repository:
- **The side-by-side deploy writes to production.** Serve refuses an un-adopted database, so `api adopt-baseline` runs on production first (it writes `goose_db_version`). Serve then applies 00002–00007 at that deploy, not at the switch.
- Adopt refuses drift. Any hand change on production, or an Alembic revision past `0028_business_npwp`, blocks it (`backend/migrations/adopt.go:16`; `backend-python/FROZEN.md`). Task 6 adopts the dump copy first, so drift shows up locally.
- Python still runs `alembic upgrade head` at every start (`docker-compose.coolify.yml:73`). That is a no-op at 0028; dropping `alembic_version` waits until Python is removed.
- **Scheduler overlap.**
  - Equal lock keys stop concurrent runs only. Python writes no slot rows, so with both schedulers on, a slot runs twice in sequence (PN 119).
  - Go's first start with the scheduler on runs each job's latest slot from the past 24 hours, even if Python ran it.
  - Mitigations:
    - distinct flag variables;
    - one flag per backend in every state;
    - the switch outside 02:00–05:59 UTC;
    - the Task 5 rehearsal, which proves the second run changes nothing.
  - Fallback: pre-insert today's `scheduled_job_runs` rows (its own OWNER YES).
- Usage-lock: Go's job uses `StubAccrual`, which errors as Python's `NotImplementedError` does (PN 1, 48) when a usage draft reaches its cut-off day. That is expected noise in Sentry, not a switch fault.
- `issue-session` and `set-password` need `docker exec -i` into a distroless container. Coolify's web terminal needs a shell (inference).
- Until Task 5 lands, the literal `BACKEND_UPSTREAM` makes the switch and the way back a git change plus a deploy.
- nginx fails to start if the upstream host does not resolve, so the web `depends_on` stays on the API that is certain to run.
- The production read comparison reaches Go only inside the network, so `/compare` ships in the Go image.
- Local write comparison: ids, numbers and timestamps differ per backend, so compare maps the created ids.
- The dev compose's Python `SESSION_SECRET` is 28 bytes (`docker-compose.yml:24`), and Go needs at least 32 (`config.go:137`). Production's generated 64-character value is fine.
