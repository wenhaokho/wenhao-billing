# Go backend 11: comparison and switch-over inventory

Read-only survey taken 2026-11-09 on branch `feat/go-payments-recon` (uncommitted work in progress on that
branch: `backend/internal/platform/config.go` and others may move). It supports the final plan: the comparison of
both backends, running side by side on Coolify, and the switch-over. Every fact carries file:line. "Spec" is
`docs/superpowers/specs/2026-11-08-go-backend-design.md`; "PN n" is note n of `backend/PORT-NOTES.md`.
Nothing here was run against Coolify, production or any remote database.

## §1 What exists for the comparison and the switch, and what is missing

### Exists

| Piece | Where | State |
|---|---|---|
| Go production image (distroless, port 8000, self-probing healthcheck) | `backend/Dockerfile:4-14` | Exists. `CGO_ENABLED=0 go build -mod=vendor -trimpath -ldflags="-s -w"` (`:7`), `EXPOSE 8000` (`:11`), `HEALTHCHECK ... CMD ["/api","healthcheck"]` every 30 s (`:13`). There is no separate `Dockerfile.prod` for Go; the spec names only `backend/Dockerfile` (spec:446-449). |
| `api healthcheck` | `backend/cmd/api/main.go:87-88`, `:210-225` | GETs `http://127.0.0.1:$PORT/health`; `/health` pings the pool (`backend/internal/platform/server.go:92-101`). |
| `api adopt-baseline` | `backend/cmd/api/main.go:91-97`, `:195-208`; `backend/migrations/adopt.go:13-20`, `:78-140` | Exists. Checks `alembic_version` = `0028_business_npwp` (`adopt.go:16`) and the schema snapshot, then records goose versions 0 and 1 (`adopt.go` Adopt, insert at about `:113`). Refuses if goose history already exists. |
| Serve refuses an un-adopted Alembic database | `backend/cmd/api/main.go:137-139`, `:180-191`; `backend/migrations/adopt.go:37-76` | Exists (`ErrNotAdopted`, `adopt.go:20`). Serve then runs goose migrations at startup (`main.go:190`). |
| `api set-password <email>` | `backend/cmd/api/main.go:9-12`, `:98-107` | Exists; password read from **stdin** one line (`main.go:10`; README `backend/README.md:62-63`), not a TTY prompt as spec:268 says. |
| `api issue-session <email>` | `backend/cmd/api/main.go:13-15`, `:98-107` | Exists: prints a session token for an admin, 8 hours, writes nothing (`main.go:14`; `backend/README.md:64-65`). |
| `api openapi` | `backend/cmd/api/main.go:89-90`, `:227-237` | Exists. |
| Goose migrations | `backend/migrations/00001_baseline.sql`, `00002_users_session_epoch.sql`, `00003_login_attempts.sql` | Three exist. Spec:403-405 also names a spent-OAuth-code table and `scheduled_job_runs`: **not yet written** (MCP and scheduler plans). |
| Baseline tooling | `backend/port/make-baseline.sh:1-30`, `backend/port/clean-dump.pl`, `backend/migrations/testdata/{schema-snapshot.sql,alembic-0028.schema.txt,rows-snapshot.sql,alembic-0028.rows.txt}` | Exists (local dev stack only, `make-baseline.sh:8-11`). |
| Route and test inventories | `backend/port/inventory.py` (frontend-call matcher `:135-169`), `backend/ROUTES.md`, `backend/TESTS.md` | Exist. ROUTES: 107 `/api/v1` + 7 OAuth/discovery + `/mcp` (`ROUTES.md:7`); 84 rows `ported`, 32 `todo`. TESTS: 520 functions / 527 collected (`TESTS.md:5`); 157 `ported`, 363 `todo`. |
| Local side-by-side service | `docker-compose.yml:133-155` (`api-go`, profile `tools`, `8001:8000`, `ENVIRONMENT: development`) | Exists, for a scratch database only (`docker-compose.yml:133-140`; recipe `backend/README.md:68-82`). |
| nginx upstream variable | `frontend/nginx.conf.template:9-12` (`server ${BACKEND_UPSTREAM};`), default `backend:8000` in `frontend/Dockerfile.prod:29` | Exists. In Coolify it is a **literal** `BACKEND_UPSTREAM: wenhao-billing-api:8000` (`docker-compose.coolify.yml:91`), not `${...}`-interpolated. |
| Checks | `scripts/check.sh:22-76` | Compose paths, frozen Python tests, frontend build, Go lint + generated + test; no comparison step. |

### Missing

1. **`backend/cmd/compare`** (spec:431): does not exist; `backend/cmd/` holds only `api/` (`backend/cmd/api/main.go`, tests).
2. **`port/allowed-diffs.yaml`** (spec:432-435): does not exist (`backend/port/` holds only `clean-dump.pl`, `inventory.py`, `make-baseline.sh`).
3. **A Go service in `docker-compose.coolify.yml`** (spec:450-452): only `wenhao-billing-api` (Python, `:21-74`) and `wenhao-billing-web` (`:76-100`).
4. **`BACKEND_UPSTREAM` as a Coolify variable**: hard-coded at `docker-compose.coolify.yml:91`, so switching (and the way back) is a repo edit plus redeploy, not an env change, unless it becomes `${BACKEND_UPSTREAM:-wenhao-billing-api:8000}`.
5. **`depends_on` for the web container** names only the Python API (`docker-compose.coolify.yml:92-94`); nginx resolves `server ${BACKEND_UPSTREAM}` at start-up (`frontend/nginx.conf.template:9-12`), so a Go service that is absent or not resolvable when web starts fails nginx.
6. **Go config for the not-yet-ported features**: no `SCHEDULER_ENABLED`, `MCP_*`, `RESEND_*`, `SMTP_REPLY_TO`, `CLOUDFLARE_*`, `WEBHOOK_HMAC_SECRET`, `EMAIL_WEBHOOK_TOKEN` in `backend/internal/platform/config.go:35-51` (see §5).
7. **Ported routes**: 32 `todo` rows: reports (`backend/ROUTES.md:22-26`), invoice record-payment/payments/send-receipt/pdf/send (`:67-68`, `:70-72`), intake (`:79-80`), quotation pdf/send (`:97-98`), payments/recon (`:100-105`), stats (`:106-108`), OAuth and discovery (`:123-129`), `/mcp` (`:135-136`). The frontend calls 22 of them (§2).
8. **Scheduler in Go**: no `gokit/sched` use and no `SCHEDULER_ENABLED` anywhere in `backend/internal` or `backend/cmd` (grep: none). `invoices.Scan` exists but is unscheduled (PN 48-49).
9. **Production `TimeZone` check** (spec:220-223) not done; the Go pool sets no `TimeZone` (`backend/cmd/api/main.go:140`; grep `TimeZone` in `backend/internal`, `backend/migrations`: none).
10. **Comparison sign-in for Python on production**: spec says Python "is signed in with its normal login" (spec:274); nothing records how compare gets the owner's password non-interactively.
11. **A rehearsal script for the way back** (spec:476): none.
12. **Memory measurement procedure** (spec:465-468): none written.

## §2 Frontend calls: the comparison corpus

Client: axios `baseURL: "/api/v1"`, `withCredentials: true` (`frontend/src/api/client.ts:3-6`); any 401 except
`/auth/me` signs the user out (`client.ts:15-29`). Decimals in bodies go as JS numbers (`Number(...)`, e.g.
`InvoiceFormView.vue:657-658`). Go status from `backend/ROUTES.md`. R = read (GET, safe for the production read
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

### Reports (todo: reports plan)
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

### Invoices (core ported: `invoices`; payment/document routes todo)
| R/W | Call | Shape | Site | Go |
|---|---|---|---|---|
| R | GET `/invoices` | repeated `?status=` (`OPEN,SENT,PARTIAL` / `DRAFT` / none) | `InvoicesView.vue:68-82` | ported |
| R | GET `/invoices/awaiting-finalization` | — | `AwaitingFinalizationView.vue:52` | ported |
| R | GET `/invoices/{id}` | — | `InvoiceFormView.vue:261` | ported |
| R | GET `/invoices/{id}/payments` | — | `InvoiceFormView.vue:517` | **todo** |
| R | GET `/invoices/{id}/pdf` | `window.open`, binary | `InvoiceFormView.vue:467` | **todo** |
| R | GET `/invoices/recurring-templates/rows` | — | `RecurringInvoicesView.vue:91`, `InvoiceFormView.vue:361` | ported |
| R | GET `/invoices/recurring-templates/{id}` | — | `InvoiceFormView.vue:301` | ported |
| W | POST `/invoices` | `buildPayload`: customer, project, `invoice_type:"MILESTONE"`, currency, number, dates, terms, notes, internal_note, discount, withholding, `line_items[{item_id,description,quantity,unit_price,position}]` | `InvoiceFormView.vue:651-680`, `:728` | ported |
| W | PATCH `/invoices/{id}` | same without `invoice_type` | `InvoiceFormView.vue:726` | ported |
| W | POST `/invoices/recurring-template` / PUT `/invoices/recurring-templates/{id}` | template payload + `schedule{frequency,interval,start_date,end_mode,end_date,end_after_cycles}` | `InvoiceFormView.vue:682-723` | ported |
| W | PATCH `/invoices/recurring-templates/{id}` | `{action}` (PAUSE/RESUME/END_NOW) | `RecurringInvoicesView.vue:58-64` | ported |
| W | POST `/invoices/{id}/finalize` | — | `AwaitingFinalizationView.vue:56` | ported |
| W | POST `/invoices/{id}/void` | `{reason}` | `AwaitingFinalizationView.vue:62` | ported |
| W | POST `/invoices/{id}/mark-paid` | — | `InvoiceFormView.vue:445` | ported |
| W | POST `/invoices/{id}/record-payment` | `{amount,wht_amount,payment_date,payer_name,payer_reference,notes}` | `InvoiceFormView.vue:412-424`, `InvoicesView.vue:168-179` | **todo** |
| W | POST `/invoices/{id}/payments/{pid}/send-receipt` | `{to_email,cc_email}` (sends mail) | `InvoiceFormView.vue:396,525`, `InvoicesView.vue:150` | **todo** |
| W | POST `/invoices/{id}/send` | `{to_email,cc_email,subject,message}` (sends mail) | `InvoiceFormView.vue:470-478` | **todo** |

### Quotations (core ported; pdf/send todo)
| R/W | Call | Shape | Site | Go |
|---|---|---|---|---|
| R | GET `/quotations` | repeated `?status=` (`DRAFT,SENT` / `ACCEPTED,INVOICED` / none); `?customer_id=`; `?project_id=` | `QuotationsView.vue:47-60`, `CustomerDetailView.vue:122-124`, `ProjectDetailView.vue:92-94` | ported |
| R | GET `/quotations/{id}` | — | `QuotationDetailView.vue:70`, `QuotationFormView.vue:133` | ported |
| R | GET `/quotations/{id}/pdf` | `window.open` | `QuotationDetailView.vue:134`, `QuotationFormView.vue:267` | **todo** |
| W | POST / PATCH `/quotations[/{id}]` | `{customer_id,project_id,currency,quotation_number,po_so_number,issue_date,valid_until,payment_terms,notes,discount_type,discount_value,line_items}` | `QuotationFormView.vue:204-235` | ported |
| W | POST `/quotations/{id}/mark-sent`, `/decline`, `/void` | no body | `QuotationDetailView.vue:139,198,209` | ported |
| W | POST `/quotations/{id}/accept` | `{accepted_by:null}` | `QuotationDetailView.vue:187` | ported |
| W | POST `/quotations/{id}/convert-to-invoice` | `{}` | `QuotationDetailView.vue:229` | ported |
| W | DELETE `/quotations/{id}` | — | `QuotationDetailView.vue:219` | ported |
| W | POST `/quotations/{id}/send` | `{to_email,cc_email,subject,message}` (sends mail) | `QuotationDetailView.vue:150-157` | **todo** |

### Bills (ported)
| R | GET `/bills` | repeated `?status=` (`DRAFT` / ... / none) | `BillsView.vue:50-62` |
|---|---|---|---|
| R | GET `/bills/{id}` | — | `BillFormView.vue:138` |
| W | POST / PATCH `/bills[/{id}]` | `{vendor_id,project_id,bill_number,po_number,currency,issue_date,due_date,payment_terms,notes,line_items[{expense_account_id,description,quantity,unit_price,position}]}` | `BillFormView.vue:236-266` |
| W | POST `/bills/{id}/record-payment` | `{amount,payment_date,payer_reference,notes}` | `BillFormView.vue:195-205` |

### Payments, recon, stats (all todo)
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

Not called by the frontend but ported/planned (outside the frontend corpus; compare needs its own cases):
`POST /invoices/milestone`, `GET /invoices/recurring-templates`, `POST /invoices/recurring/{id}/trigger`,
`POST /invoices/usage/{id}/lock`, `DELETE /invoices/{id}`, `GET /accounting/fx-rates/latest`, `POST /bills/{id}/void`,
`DELETE /projects/{id}`, `GET /payments/{id}`, intake webhook/email, OAuth and `/mcp` (`backend/ROUTES.md:20`, `:38`, `:52`, `:54`, `:59-60`, `:73`, `:79-80`, `:85`, `:102`, `:123-136`).

Writes that leave the machine (must be stubbed or skipped in a local write comparison): forgot-password, invoice send,
quotation send, send-receipt (Resend), fx-rates/sync (Frankfurter), and a PAID transition that triggers the hosting
restore (Cloudflare) (`backend/internal/...` not yet; plan 07 `docs/superpowers/plans/2026-11-09-go-backend-07-payments-recon.md:133`).

## §3 Allowed comparison differences already recorded

Explicitly labelled "allowed difference for compare":
- Spec:432-435: decimal strings equal in value; timestamps equal as instants; error body shape (Python `{"detail"}` vs problem+json with the same `detail`); plus the PORT-NOTES entries.
- PN 14 (`PORT-NOTES.md:75-79`): email validation edge forms (quoted local parts, odd dots, numeric TLD, >253 domains) may be accepted by Go.
- PN 24 (`:128-131`): business-profile `updated_at` moves on a PUT that changes nothing.
- PN 50 (`:263-266`): `expected_cash` `"0"` (Python) vs `"0.0000"` (Go).
- PN 63 (`:328-331`): project summary empty sums `"0"` vs `"0.0000"`.
- PN 76 (`:393-396`): quotation `last_sent_at`/`accepted_at` gain `Z`.
- Plan 07 item 9 (`docs/superpowers/plans/2026-11-09-go-backend-07-payments-recon.md:116`): intake responses print amounts at 4 places, confidence at 2.
- Plan 07 (`...07-payments-recon.md:408`): intake invalid JSON 400 carries Go's parser text.

Recorded Go/Python differences the comparison will also meet (not labelled "allowed", but deliberate, so the diff file needs them):
- Status/sentence changes on bad input: PN 10 (`:52-56`), 11 (`:58-62`), 15 (`:81-85`), 16 (`:87-90`), 17 (`:92-95`), 19 (`:103-106`), 20 (`:108-111`), 21 (`:113-116`), 25 (`:133-136`), 30 (`:158-161`), 36 (`:191-195`), 37 (`:197-200`), 38 (`:202-205`), 55 (`:288-291`), 59 (`:308-311`), 60 (`:313-316`), 61 (`:318-321`), 75 (`:388-391`).
- Concurrency/locking changes (409 instead of 500, single result): PN 27, 31, 34, 35, 56, 57, 58 (`:143-146`, `:163-166`, `:179-183`, `:185-189`, `:293-301`, `:303-306`).
- Session epoch sign-outs: PN 13, 22 (`:69-73`, `:118-121`).
- Business profile GET writes nothing: PN 12 (`:64-67`).
- FX sync 502 text and coverage: PN 28, 32, 33 (`:148-151`, `:168-177`).
- "Today" is UTC in Go (PN 5 `:27-30`, and PN 34, 41, 45, 49, 64 refer to it): equal to Python only if the Python container's clock zone is UTC.
- Go tie-breaks where Python order was undefined: PN 40, 41, 44, 47, 53, 78 (`:213-216`, `:218-221`, `:233-236`, `:248-251`, `:278-281`, `:403-406`). A list comparison must compare as multisets where Python's order was undefined, or accept Go's order.
- `delete invoice` referenced → 409 (PN 36); quotation project delete counts quotations (PN 62 `:323-326`).
- Bill void `reason` edge forms (PN 74 `:383-386`); bill record-payment float printing (PN 73 `:378-381`).
- Errors: 500s become problem+json (spec:190-191, 498-499); invoice/quotation send failures 502 (spec:360-361).
- PDFs: a new renderer (spec:338-348), so bytes never match; compare only status, `Content-Type`, `Content-Disposition` (spec:239-243).

## §4 Switch-over prerequisites recorded so far

| # | Prerequisite | Source |
|---|---|---|
| 1 | Every `ROUTES.md` route ported (32 todo today), every Python test accounted for in `TESTS.md` (363 todo), every guard's mutation recorded | spec:414-415, 509-515; `backend/ROUTES.md`; `backend/TESTS.md:5` |
| 2 | Hosting restore ported: it is a no-op until the integrations plan; "the switch-over must not happen before that plan lands" | plan 07 `...07-payments-recon.md:121`, `:133`; inventory `...07-payments-recon-inventory.md:368` |
| 3 | Production `TimeZone` checked with `SHOW timezone` (read-only, with the owner's yes) before the comparison; if not UTC the Go pool sets `TimeZone=UTC` and the plan says how existing rows are read | spec:216-223 |
| 4 | No default intake secrets in production: Go refuses `dev-webhook-secret-change-me` / `dev-email-token-change-me` or empty when `ENVIRONMENT=production` (`TestProductionRefusesDefaultIntakeSecrets`) | plan 07 `:115`, `:446`, `:450`; inventory 07 `:374`; Python defaults `backend-python/app/config.py:41-42` |
| 5 | Way back rehearsed locally before step 3; Python cannot read argon2id, reset with `scripts.seed_admin` | spec:476-480; `backend-python/scripts/seed_admin.py:20-31` |
| 6 | Owner runs `api set-password` after the switch (bcrypt hash will not verify in Go: 401 `invalid credentials`, never 500) | spec:266-269, 473; plan 02 `...02-foundation.md:30` |
| 7 | `api adopt-baseline` on the production database (refuses wrong head or drift) before any Go serve there; serve refuses an un-adopted Alembic database | spec:399-402; plan 02 `:31`, `:200`; `backend/migrations/adopt.go:16-20`; replacement plan `...2026-11-05-go-backend-replacement.md:23` |
| 8 | Scheduler: Go `SCHEDULER_ENABLED=false` until step 3, then true; Python false after; jobs keep Python's advisory-lock keys | spec:369-371, 389-390, 471-472 |
| 9 | Read comparison on production clean or every difference allowed; local read+write comparison on a restored dump (owner approves taking the dump; stays on this machine) | spec:437-442, 469-470, 516-517 |
| 10 | Go idle memory measured on the server (after start-up, and ten minutes after a sign-in and a comparison run) against ~30 MB | spec:465-468, 518-519 |
| 11 | Browser check against Go locally before the switch and on production after it (dashboard, invoice PDF, quotation, reports) | spec:473-474, 520-521 |
| 12 | MCP connector working against Go; reconnect if asked; tokens keep `MCP_TOKEN_SECRET` and claims | spec:321-323, 474-475, 522 |
| 13 | The one frontend edit (`return_to` under `/oauth/` followed with `window.location.assign`) shipped with the MCP plan | spec:324-332; PN 9 (`PORT-NOTES.md:47-50`) |
| 14 | Goose migrations stay additive so Python runs on the same database during the week after | spec:403-405, 477-478 |
| 15 | Client address: `CF-Connecting-IP`, else left-most `X-Forwarded-For`; a forged header only spreads the attacker's own guesses (argon2id bounds them). nginx appends with `$proxy_add_x_forwarded_for` (`frontend/nginx.conf.template:28`), so left-most XFF is client-supplied when Cloudflare is bypassed | spec:284-294; `backend/internal/auth/throttle.go:27-28`; `backend/internal/auth/routes.go:120` |
| 16 | Secure cookie: `ENVIRONMENT` other than `development`/`test` keeps it Secure; Go defaults `ENVIRONMENT=production` | spec:276; `backend/internal/platform/config.go:36`, `:77`; `backend/internal/platform/principal.go:95` |
| 17 | Schema drift check: adopt compares the live schema with the Alembic 0028 snapshot | `backend/migrations/adopt.go:78-84`; `backend/README.md:59-61`; plan 02 `:200` |
| 18 | One week after the switch with no way back: delete `backend-python/` and the Python service, drop `alembic_version` (goose migration), update CLAUDE.md and AGENTS.md | spec:406, 481-482, 552; `backend-python/FROZEN.md:12-14` |
| 19 | Every step that deploys or changes production waits for the owner's yes; merging to `main` deploys (Coolify builds from it) | spec:462; plan 01 `...01-kit-template-freeze.md:34`; replacement plan `:15`, `:86` |
| 20 | Post-switch fixes queued in PORT-NOTES "After the switch" (non-"none" ones): PN 1, 2, 3, 4, 5, 6, 7, 8, 9, 18, 19, 21, 23, 34 (milestone numbers), 35 (void duplicate children), 39, 40, 41, 42, 43, 44, 45, 46, 47, 48, 52, 53, 54, 62, 63, 64, 65, 66, 67, 68, 69, 70, 71, 72, 73, 74, 75, 77, 78, 79 | `backend/PORT-NOTES.md:10-411` |
| 21 | Of those, PN 6 (tie auto-clear) is a PRD safe-stop breach; plan 07 makes it a Go change (`TestTwoExactCandidatesHold`), so it becomes a comparison difference | PN 6 `:32-35`; plan 07 `:137-138` |
| 22 | PN 35: duplicate recurring children already in production data stay; find and void after the switch | `PORT-NOTES.md:189` |

## §5 Production configuration surface

### Environment variables, name by name

Python: `backend-python/app/config.py`; pydantic-settings maps each field to its upper-case name (`:8`).
Go: `backend/internal/platform/config.go:35-51`; only other env read is `PORT` in `healthcheck` (`backend/cmd/api/main.go:211`).
Coolify sets: `docker-compose.coolify.yml:26-55`.

| Variable | Python | Go | Coolify (Python service) | Gap |
|---|---|---|---|---|
| `DATABASE_URL` | `:10-25`, rewrites `postgres://`/`postgresql://` to `+psycopg` | `:37` required; `pgxURL` rewrites `postgresql+psycopg://`, `postgres://` (`:133-140`) | `:29` | none |
| `SCHEDULER_ENABLED` | `:28` default true | **absent** | not set (true) | scheduler plan; must be false on Go until step 3 |
| `SESSION_SECRET` | `:30` default dev value (28 bytes) | `:38` required, ≥32 bytes (`:84-86`); HS256 key | `:31` `SERVICE_BASE64_64_SESSION` (64 chars) | fine on Coolify; dev compose Python value `docker-compose.yml:24` is too short for Go (api-go uses its own, `:147`) |
| `SESSION_COOKIE_NAME` | `:31` `billing_session` | constant `billing_session` (`principal.go:23`) | — | same name, different token format (see §6) |
| `SESSION_MAX_AGE_SECONDS` | `:33` 28800 | `:39` 28800 | — | none |
| `SESSION_REMEMBER_MAX_AGE_SECONDS` | `:39` 2592000 | `:40` 2592000 | — | none |
| `WEBHOOK_HMAC_SECRET` | `:41` dev default | **absent** (plan 07 adds, `...07-payments-recon.md:115`) | `:32` | payments plan |
| `EMAIL_WEBHOOK_TOKEN` | `:42` dev default | **absent** (plan 07) | `:33` | payments plan |
| `CORS_ORIGINS` | `:44` JSON list | `:41`, JSON list or comma list (`:155-178`); blank = none | `:36` JSON list | none |
| `BASE_CURRENCY` | `:48` IDR | `:47`, default IDR, blank refused (`:94-100`) | not set | none |
| `SMTP_HOST/PORT/USERNAME/PASSWORD/USE_TLS/FROM_EMAIL/FROM_NAME` | `:52-58` | dropped (spec:355-356) | not set | none |
| `SMTP_REPLY_TO` | `:59` default `wenhao.kho@gmail.com` | **absent**; spec keeps the name (spec:353-354) | not set | email plan |
| `APP_BASE_URL` | `:63` | `:42` | `:37` | none |
| `CLOUDFLARE_API_TOKEN` | `:65` | **absent** | `:51` | integrations plan |
| `CLOUDFLARE_API_BASE_URL`, `CLOUDFLARE_TIMEOUT_SECONDS` | `:66-67` | **absent** | not set | integrations plan |
| `MCP_ENABLED` | `:70` | **absent** | `:38` | MCP plan |
| `MCP_CONFIRM_SECRET` | `:72` ≥16 | **absent** | `:39` | MCP plan |
| `MCP_TOKEN_SECRET` | `:74` ≥16 | **absent** | `:40` | MCP plan; must be the same value for connector carry-over (spec:321-323) |
| `MCP_ACCESS_TOKEN_TTL_SECONDS`, `MCP_REFRESH_TOKEN_TTL_SECONDS` | `:75-76` | **absent** | `:42-43` | MCP plan |
| `MCP_PUBLIC_BASE_URL` | `:78` | **absent** | `:41` | MCP plan; Host/Origin guard (spec:308-309) |
| `PDF_ENGINE` | `:83` | dropped (spec:348) | not set | none |
| `FX_PROVIDER_BASE_URL` | `:86` | `:48` same default | not set | none |
| `FX_PROVIDER_TIMEOUT_SECONDS` | `:87` 15 | `:49` 15, >0 (`:110-112`) | not set | none |
| `FX_SYNC_CURRENCIES` | `:89-91` JSON list | `:50` comma or JSON list | not set | none |
| `RESEND_API_KEY`, `RESEND_FROM_EMAIL`, `RESEND_FROM_NAME` | `:96-98` | **absent** (mail is `email.LogSender`, `main.go:116-120`) | `:47-49` | email plan |
| `RESEND_BASE_URL`, `RESEND_TIMEOUT_SECONDS` | `:99-100` | **absent** | not set | email plan |
| `SENTRY_DSN` | `:105` | `:43` | `:53` | none |
| `SENTRY_ENVIRONMENT` | `:106` default `development` | `:44` default `development` | `:54` `production` | none |
| `SENTRY_TRACES_SAMPLE_RATE` | `:108` 0..1 | `:45`, no range check | `:55` | minor |
| `ENVIRONMENT` | — | `:36` default `production` (Secure cookie; production-only checks) | not set | Go only; leaving it unset is correct in Coolify |
| `PORT` | — (uvicorn `--port 8000`, `docker-compose.coolify.yml:73`) | `:46` default 8000; `main.go:211` | not set | none |

### Coolify compose today

- `wenhao-billing-api` (Python): build `./backend-python` `Dockerfile.prod` (`docker-compose.coolify.yml:21-25`); no public domain (`:5`); healthcheck GET `http://localhost:8000/docs` every 10 s, 20 retries, 30 s start (`:56-67`; same baked into `backend-python/Dockerfile.prod:60-61`); command `alembic upgrade head && uvicorn ... --port 8000` (`:69-74`); scheduler in-process (`:7-8`).
- `wenhao-billing-web` (nginx): build `./frontend` `Dockerfile.prod` with Sentry build args (`:76-84`); `SERVICE_FQDN_WEB_80` makes Coolify route the public domain and TLS (`:87-89`); `BACKEND_UPSTREAM: wenhao-billing-api:8000` (`:90-91`); `depends_on: wenhao-billing-api: service_healthy` (`:92-94`); healthcheck `wget http://127.0.0.1/` (`:95-100`).
- Shared Postgres managed outside this stack, reached by hostname on a shared Docker network (`:6`, `:15-18`).
- nginx proxies `^/(api|oauth|\.well-known)/` (`frontend/nginx.conf.template:34-36`), `^/(docs|redoc|openapi\.json)$` (`:37-39`), `^/mcp(/|$)` unbuffered with 1 h read timeout (`:43-48`); keeps `Host`, sets `X-Real-IP`, appends `X-Forwarded-For`, passes `X-Forwarded-Proto`/`Host` (`:24-30`); 25 MB body limit (`:20`); `keepalive 16` upstream (`:11`).

### How nginx would point at Go

1. Add `wenhao-billing-api-go` (build `./backend`, `Dockerfile`; same env names as §5 table; `SCHEDULER_ENABLED=false`; no `SERVICE_FQDN_*`) beside the Python service (spec:450-452). Its healthcheck must be exec form `["CMD","/api","healthcheck"]` because the image is distroless with no shell (`backend/Dockerfile:9`, `:12-13`); the image's own HEALTHCHECK already does this.
2. Make `BACKEND_UPSTREAM` interpolated (`${BACKEND_UPSTREAM:-wenhao-billing-api:8000}`) so step 3 is a Coolify variable change; today it is a literal (`docker-compose.coolify.yml:91`).
3. Switch: `BACKEND_UPSTREAM=wenhao-billing-api-go:8000` (spec:471); Go listens on 8000 like Python so only the host changes (spec:448-449).
4. `depends_on` should name whichever API the upstream uses, or both, since nginx resolves the upstream at start (`nginx.conf.template:9-12`).
5. Go answers `/docs` and `/openapi.json` only if Huma's default docs paths stay on (`backend/internal/platform/server.go:80`, `huma.DefaultConfig`); the nginx `/docs|redoc|openapi.json` route (`nginx.conf.template:37-39`) and Python's `/docs` healthcheck do not carry over (`/redoc` is Python-only). Not verified by running.

## §6 Risks and rollback points (from the spec, plus what the repository shows)

Spec risks and rollback points:
- Step 1 deploy is the first production contact; Python keeps serving, Go scheduler off (spec:464-465).
- Rollback point A: before step 3, remove the Go service; nothing user-facing changed (spec:464-470).
- Step 3 is one switch for every route, not route by route (spec:537).
- Rollback point B, the way back: `BACKEND_UPSTREAM` back to Python, swap scheduler flags; rehearsed locally first (spec:476-478).
- After a switch the owner's hash is argon2id, which Python cannot read; reset from Python with `scripts.seed_admin` (spec:478-480). Python's login calls passlib bcrypt-only `verify` (`backend-python/app/api/v1/routers/auth.py:37`, `:46`), which raises on an unknown hash, so a Python login before the reset is likely a 500, not a 401 (inference; not run). `seed_admin` takes the password on the command line (`backend-python/scripts/seed_admin.py:4`, `:38`).
- No session carry-over: everyone signs in again at the switch and at the way back (spec:538). Both backends use cookie `billing_session` (`backend-python/app/config.py:31`; `backend/internal/platform/principal.go:23`) with different formats, so each rejects the other's cookie as signed out.
- Python ignores `users.session_epoch` (spec:281-283): a sign-out-everywhere done in Go does not end Python-era cookies on the way back (they are a different format anyway).
- Go migrations must stay additive or Python breaks on the shared database (spec:403-405, 477-478). `00002` adds `session_epoch NOT NULL DEFAULT 0`, so Python inserts still work (`backend/migrations/00002_users_session_epoch.sql`).
- Go catches up one missed job slot after a restart (Python skips); safe only because jobs are idempotent; slots older than 24 h are skipped (spec:372-380). At the switch, flipping both flags in one redeploy risks a gap or an overlap; the shared advisory-lock keys prevent concurrent runs (spec:369-371).
- Timestamps: columns are `timestamp without time zone`; `CURRENT_TIMESTAMP` writes session-local time, UTC only if the server's `TimeZone` is UTC (spec:216-223).
- MCP tokens may or may not survive; owner reconnects once (spec:321-323, 474-475).
- Way back after a week is ruled out once `backend-python/` and `alembic_version` are gone (spec:481-482).
- PDFs change design (owner reviews first versions) (spec:338-343).
- Sends stay synchronous; Resend idempotency key prevents a retry double-send (spec:357-363).

From the repository (not in the spec's switch list):
- **Step 1 writes to production.** Go serve refuses an un-adopted database (`backend/cmd/api/main.go:137-139`, `backend/migrations/adopt.go:66-68`), so the side-by-side deploy needs `api adopt-baseline` on production first (writes `goose_db_version`), and serve then applies `00002`/`00003` (and later `scheduled_job_runs`, OAuth codes) to production at step 1, not step 3 (`main.go:190`). The spec's step 1 (spec:464-468) does not mention either.
- Adopt refuses drift; any hand change on production (or an Alembic revision past `0028_business_npwp`) blocks step 1 (`backend/migrations/adopt.go:13-16`; `backend-python/FROZEN.md:8`).
- Python's container still runs `alembic upgrade head` at every start (`docker-compose.coolify.yml:73`); harmless while frozen at 0028, but the drop of `alembic_version` (spec:406) must wait until the Python service is gone.
- Running `issue-session`/`set-password` needs `docker exec` into a distroless container (no shell, `backend/Dockerfile:9`); `set-password` reads stdin (`backend/cmd/api/main.go:10`), so it needs `docker exec -i`; Coolify's web terminal needs a shell (inference).
- `BACKEND_UPSTREAM` literal (`docker-compose.coolify.yml:91`) makes both the switch and the way back a git change plus deploy from `main`.
- nginx fails to start if the upstream host does not resolve (`frontend/nginx.conf.template:9-12`), so a Go service that is down at web start takes the site down.
- The production read comparison must sign in to Python with a real login (spec:274), and it reaches Go only "through the internal network" (spec:441-442): the compare binary has to run inside that network, and the Go image has no shell or tools.
- Local write comparison on two dump copies: server-generated ids, numbers and timestamps differ per backend, so compare must map created ids between the two runs (inference from spec:439-440).
- Throttle trusts `CF-Connecting-IP`/left-most XFF (`backend/internal/auth/throttle.go:27-28`); accepted by the spec (spec:292-294).
- Dev compose Python `SESSION_SECRET` is 28 bytes (`docker-compose.yml:24`); reusing it for Go fails startup (`config.go:84-86`). Production's 64-char generated value is fine (`docker-compose.coolify.yml:31`).
