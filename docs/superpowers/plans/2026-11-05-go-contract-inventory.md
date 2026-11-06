# Go replacement contract inventory

Historical read-only inventories from the previous Claude session. These are leads, not verified contracts: reconcile against the current working tree and tests before implementation.

<task-notification>
<task-id>a987a10ec227f3d29</task-id>
<tool-use-id>toolu_01EUotX12XB7QspRyGNhxqri</tool-use-id>
<output-file>C:\Users\wenha\AppData\Local\Temp\claude\D--Development-wenhao-billing\487ee98e-d4ae-4704-ac1b-22b7889a6a4d\tasks\a987a10ec227f3d29.output</output-file>
<status>completed</status>
<summary>Agent "Inventory frontend API calls" finished</summary>
<note>A task-notification fires each time this agent stops with no live background children of its own. The user can send it another message and resume it, so the same task-id may notify more than once.</note>
<result>I mapped every HTTP call in `frontend/src` by reading each call site, the error handlers and the templates that render the responses. The frontend calls **96 distinct method + path endpoints**. All of them go through the axios `api` instance except two PDF URLs opened with `window.open`.

# Frontend HTTP inventory (D:\Development\wenhao-billing\frontend\src)

## 0. Overview and transport

**There is no API module layer.** `D:\Development\wenhao-billing\frontend\src\api\` contains only `client.ts`. There are no `api/types*` files, no openapi-typescript and no generated types. Every view declares its own response interfaces inline, and they differ from view to view. The only shared types are:
- `User` and `ForgotPasswordResponse` in `D:\Development\wenhao-billing\frontend\src\stores\auth.ts:5-17`
- `ReceiptPayment` in `D:\Development\wenhao-billing\frontend\src\components\SendReceiptDialog.vue:12`
- `CustomerFormData` and `buildCustomerPayload` in `D:\Development\wenhao-billing\frontend\src\components\customerForm.ts`
- `LineItemRow` in `D:\Development\wenhao-billing\frontend\src\composables\useInvoiceForm.ts`

Calls live in `stores/auth.ts` and in the views. No component or composable makes HTTP calls; the components only emit payloads.

**`D:\Development\wenhao-billing\frontend\src\api\client.ts`**
- `axios.create({ baseURL: "/api/v1", withCredentials: true })`.
- No request interceptor, no Authorization header, no CSRF header. Every non-GET body is JSON.
- **There are no multipart uploads anywhere.** The logo is sent as a base64 data URI inside the JSON `PUT /business-profile` (`AccountView.vue:190-224`). The file limit is 1 MB, so the body is about 1.4 MB.
- Response interceptor: if `status === 401` and the URL does not contain `/auth/me`, it sets `useAuthStore().user = null`. Then, if the current route is not `meta.public`, it calls `router.push({name:"login", query:{next: current.fullPath}})` and still rejects the error.
- **401 is the only status code branched on anywhere.** Nothing checks 404, 409 or 422.

**Auth model**
- The session is a cookie only. The router guard (`router/index.ts:264-279`) calls `GET /auth/me` when no user is loaded. If that fails for any reason, the store sets `user = null` and the guard redirects to `/login?next=…`.
- Sentry (`main.ts:30`): when `VITE_SENTRY_DSN` is set, `tracePropagationTargets: [/^\/api\//]` adds `sentry-trace` and `baggage` headers to API calls. The Go server must accept and ignore them.
- Dev proxy (`vite.config.ts`): `/api` goes to `http://localhost:8000`.
- Production (`nginx.conf.template`): `/api/`, `/oauth/`, `/.well-known/`, `/docs`, `/redoc`, `/openapi.json` and `/mcp` go to the backend, with Host kept.
- The frontend never calls `/mcp` or `/oauth`. `McpIntegrationView.vue:8-9` only shows `https://billing.wenhao.id/mcp` and `http://localhost:8000/mcp` as text.

**Wire-format conventions the frontend relies on everywhere**
- **Money and decimals are JSON strings** (`amount: string` and so on). Most are passed through `Number()` or `formatAmount()`, but some are **printed raw** (listed in section 13).
- In request bodies, money is sent as **numbers** (`quantity`, `unit_price`, `amount`, `rate`, `wht_rate`, `discount_value`, `contract_value`, `default_unit_price` and others).
- **Date-only fields are `"YYYY-MM-DD"` strings**: `issue_date`, `due_date`, `valid_until`, `payment_date`, `as_of_date`, `next_run_date`, `start_date`, `next_date`. Code concatenates `+ "T00:00:00"` (`CustomerDetailView.vue:144,185`, `InvoiceFormView.vue:325,327`) and splits on `-` (`PaymentIntakeWidget.vue:282`). A full timestamp in these fields would break those spots.
- **Timestamps** (`created_at`, `last_sent_at`, `accepted_at`) go through `new Date()`. The dashboard's `timeAgo` (`PaymentIntakeWidget.vue:309-314`) assumes naive UTC and appends `Z` when there is no timezone. RFC3339 with `Z` also works.
- IDs: UUID strings, except **`account_id` and `rate_id`, which are JSON numbers**.
- **No pagination anywhere.** Every list endpoint returns a bare JSON array.
- Repeated status filters are built with `URLSearchParams.append`, so the query string is `?status=OPEN&amp;status=SENT&amp;status=PARTIAL` (repeated key, not comma-separated). Other params use axios defaults: `{active:true}` becomes `?active=true`.

---

## 1. Auth (`stores/auth.ts`)

`User` = `{ user_id: string, email: string, role: string, display_name: string|null, created_at: string }`

| Method + path | file:line | Request | Response fields read | Errors |
|---|---|---|---|---|
| POST `/auth/login` | auth.ts:29 | JSON `{email, password, remember: boolean}` | Whole body stored as `User` | `LoginView.vue:56`: `friendlyError(detail)`. A string matching `/invalid\|incorrect\|unauthor\|credentials/i` shows a fixed "incorrect" message; `/disabled\|locked\|inactive/i` shows "not active"; any other string is shown raw; non-string falls back to "Login failed". The backend currently returns 401 with `"invalid credentials"`. The interceptor fires on that 401 but does not navigate, because `/login` is public. |
| GET `/auth/me` | auth.ts:35 | none | Whole body is `User` (`user_id`, `email`, `role`, `display_name`, `created_at` are shown in `AccountView.vue:291,523-529` and `App.vue:222`) | Any error means signed out. Excluded from the 401 interceptor. |
| POST `/auth/logout` | auth.ts:43 | no body | Not read (backend currently returns 204) | Unhandled |
| POST `/auth/change-password` | auth.ts:54 | `{current_password, new_password}` | Body **replaces** `auth.user`, so it must return a `User` | `AccountView.vue:257` shows `detail`. **This must not return 401** for a wrong current password, or the interceptor logs the user out. The backend currently uses 400. |
| POST `/auth/forgot-password` | auth.ts:63 | `{email}` | Only `message: string` (`ForgotPasswordView.vue:23`). `reset_token` and `reset_link` are typed but unused. | `detail`, fallback "Request failed" |
| POST `/auth/reset-password` | auth.ts:70 | `{token, new_password}` | Not read; the view ignores the returned value | `ResetPasswordView.vue:46` shows `detail` |

## 2. Users

| Method + path | file:line | Request | Response read | Errors |
|---|---|---|---|---|
| PATCH `/users/me` | auth.ts:48 (called from `AccountView.vue:62`) | `{email, display_name: string\|null}` | Replaces `auth.user`, so it must be a `User` | `AccountView.vue:68` shows `detail` |
| GET `/users` | `UsersView.vue:23`, `UserResetPasswordView.vue:24` | none | Array of `{user_id, email, role, created_at}` | none |
| POST `/users` | `UserInviteView.vue:18` | `{email, password, role}` (role is free text, default `"admin"`) | Not read | `detail`, fallback "Create failed" |
| POST `/users/{userId}/reset-password` | `UserResetPasswordView.vue:33` | `{password}` | Not read | `detail`, fallback "Reset failed" |

## 3. Business profile (`AccountView.vue`, `InvoiceFormView.vue`)

| Method + path | file:line | Request | Response read | Errors |
|---|---|---|---|---|
| GET `/business-profile` | `AccountView.vue:125`, `InvoiceFormView.vue:346` | none | `{name, address, contact_email, contact_phone, invoice_title, invoice_summary, logo_url, default_notes, npwp, npwp_name}`, all `string\|null`. `logo_url` may be a data URI and is used as `&lt;img :src&gt;` (`AccountView.vue:380`). `InvoiceFormView` reads only `default_notes`. | `detail`, fallback "Failed to load company profile" |
| PUT `/business-profile` | `AccountView.vue:177` | The same 10 keys; blank strings become `null` | Not read | `detail ?? e.message` |
| GET `/business-profile/payment-accounts` | `AccountView.vue:138` | none | Array of `{currency, instructions: string, is_default: boolean}`, matched by `currency` against IDR, SGD and USD | (same catch as above) |
| PUT `/business-profile/payment-accounts` | `AccountView.vue:178` | **A bare JSON array** `[{currency, instructions, is_default}]` containing only non-blank rows; the first row is forced to `is_default` if none is set | Not read | (same catch) |

## 4. Customers

Customer shape as read by `CustomerDetailView.vue:26-61` (the superset). Other views read subsets.
- `customer_id`, `name`, `matching_aliases: string[]` (**must be an array**: `CustomersView` reads `.length`), `active: boolean`
- `contact_first_name`, `contact_last_name`, `contact_email`, `contact_phone`, `contact_phone_2`
- `quotation_cc_emails?: string[]`, `billing_cc_emails?: string[]`
- `account_number`, `website` (used as `&lt;a :href&gt;`), `notes`, `default_currency`
- `billing_address`, `billing_address1`, `billing_address2`, `billing_country`, `billing_state`, `billing_city`, `billing_postal_code`
- `ship_to_name`, `shipping_address1`, `shipping_address2`, `shipping_country`, `shipping_state`, `shipping_city`, `shipping_postal_code`, `shipping_phone`, `shipping_delivery_instructions`
- `default_wht_type?: "NONE"|"PPH21"|"PPH23"`, `default_wht_rate?: string|null` (put through `Number()`), `default_wht_gross_up?: boolean`

| Method + path | file:line | Request | Response read | Errors |
|---|---|---|---|---|
| GET `/customers` | `CustomersView.vue:52` with params `{active:true}` (none when "show inactive" is on); unfiltered from `AwaitingFinalizationView:37`, `InvoicesView:88`, `InvoiceFormView:235`, `ProjectFormView:82`, `ProjectsView:69`, `QuotationsView:66`, `QuotationFormView:107`, `QuotationDetailView:75` | query `active` | Array of customers. List views read `customer_id` and `name`. The invoice form reads the address fields, `contact_email`, `contact_phone`, `billing_cc_emails`, `default_currency` and `default_wht_*`. The quotation detail reads `quotation_cc_emails`. | none |
| GET `/customers/balances` | `CustomersView.vue:59` | none | **Flat** array `{customer_id, currency, balance: string, overdue: string}` | none |
| GET `/customers/{customerId}` | `CustomerDetailView.vue:93`, `ProjectDetailView.vue:117` (reads only `name`) | none | Full customer | none |
| POST `/customers` | `CustomerNewView.vue:28` | `buildCustomerPayload` (`customerForm.ts:84-124`): every field above except IDs; blank strings become `null`; `quotation_cc_emails` and `billing_cc_emails` are `string[]`; `default_currency` defaults to `"IDR"`; `default_wht_rate` is a number or null (null and gross-up false when type is NONE) | Only `res.data.customer_id` | `detail`, fallback "Failed to create customer" |
| PATCH `/customers/{id}` | `CustomerDetailView.vue:268` sends the full payload; `CustomersView.vue:77` sends only **`{active:true}`** | So **PATCH must be a partial update** | Not read | Detail view shows `detail`, fallback "Save failed" |
| DELETE `/customers/{id}` | `CustomersView.vue:71` | none (this is a deactivate) | Not read | none |
| GET `/customers/{id}/billing-summary` | `CustomerDetailView.vue:99` | none | `{unpaid_invoices: InvoiceRow[], total_unpaid: {currency, amount}[], paid_last_12_months: {currency, amount}[], last_invoice_issue_date: "YYYY-MM-DD"\|null}`. Also typed but **unused**: `milestone`, `recurring`, `usage`, `last_invoice_id`. | none |
| GET `/customers/{id}/invoices` | `CustomerDetailView.vue:105` | none | Array `InvoiceRow` = `{invoice_id, invoice_type, currency, amount, balance_due, status, issue_date, due_date}`. `due_date + "T00:00:00"` is computed for OPEN, SENT and PARTIAL rows. `invoice_type` must be a non-null string (`.charAt`). | none |

## 5. Invoices

Invoice list row (`InvoicesView.vue:25-39`, `AwaitingFinalizationView.vue:17-29`): `{invoice_id, customer_id, invoice_type, invoice_number|null, currency, amount, balance_due, wht_amount?, wht_booked?, status, issue_date, due_date, created_at}`. The fallback `customerNameById[...] ?? row.customer_id.slice(0,8)` needs `customer_id` to be **a non-null string**.

Invoice detail `InvoiceOut` (`InvoiceFormView.vue:106-145`):
- `invoice_id`, `customer_id`, `project_id`, `invoice_type`, `invoice_number`, `po_so_number`, `currency`
- `subtotal`, `discount_type`, `discount_value`
- `wht_type`, `wht_rate`, `wht_gross_up`, `wht_base`, `wht_amount`, `wht_booked`, `expected_cash`
- `amount`, `balance_due`, `status`
- `issue_date`, `due_date`, `coverage_start`, `coverage_end`, `payment_terms`, `notes`, `footer`, `internal_note`, `is_template`
- `line_items: [{line_item_id, item_id, description, quantity: string, unit_price: string, amount, position: number}]`

Status values used: `DRAFT`, `OPEN`, `SENT`, `PARTIAL`, `PAID`, `VOID`.

| Method + path | file:line | Request | Response read | Errors |
|---|---|---|---|---|
| GET `/invoices` | `InvoicesView.vue:82` | Repeated `status` param (OPEN, SENT, PARTIAL / DRAFT / none) | Array of list rows | none |
| GET `/invoices/awaiting-finalization` | `AwaitingFinalizationView.vue:52` | none | Array of list rows | none |
| GET `/invoices/{id}` | `InvoiceFormView.vue:261` | none | `InvoiceOut`. `line_items` is sorted by `position`. `issue_date` and `due_date` go through `new Date()`. `balance_due` goes through `Number()`. `wht_amount` and `wht_booked` are used for the outstanding-PPh calculation. | none |
| POST `/invoices` | `InvoiceFormView.vue:728` | `{customer_id, project_id, invoice_type:"MILESTONE", currency, invoice_number\|null, po_so_number\|null, issue_date, due_date, payment_terms\|null, notes\|null, internal_note\|null, discount_type:"PERCENT"\|"AMOUNT"\|null, discount_value:number\|null, wht_type, wht_rate:number\|null, wht_gross_up:boolean, line_items:[{item_id\|null, description, quantity:number, unit_price:number, position}]}` | Not read (navigates back) | Template line 1250 shows `detail`, fallback "Save failed" |
| PATCH `/invoices/{id}` | `InvoiceFormView.vue:726` | Same as POST but **without `invoice_type`** | Not read | same |
| POST `/invoices/{id}/finalize` | `AwaitingFinalizationView.vue:56` | no body | Not read | none |
| POST `/invoices/{id}/void` | `AwaitingFinalizationView.vue:62` | `{reason: "voided from finalization queue"}` | Not read | none |
| POST `/invoices/{id}/record-payment` | `InvoiceFormView.vue:424`, `InvoicesView.vue:179` | `{amount:number, wht_amount:number, payment_date:"YYYY-MM-DD", payer_name\|null, payer_reference\|null, notes\|null}`. The client-side `receipt` field is removed before sending. | **`payment_id`** is read and used to chain to send-receipt | `detail`, fallback "Failed to record payment" |
| POST `/invoices/{id}/mark-paid` | `InvoiceFormView.vue:445` | no body | Not read | `detail`, fallback "Failed to mark as paid" |
| POST `/invoices/{id}/send` | `InvoiceFormView.vue:478` | `{to_email, cc_email: string\|null (one comma+space joined string, not an array), subject, message}` | Not read | `detail`, fallback "Failed to send invoice" |
| GET `/invoices/{id}/payments` | `InvoiceFormView.vue:517` (only when status is PAID or PARTIAL) | none | Array `{payment_id, amount, currency, payment_date, payer_reference\|null}` | none |
| POST `/invoices/{id}/payments/{paymentId}/send-receipt` | `InvoiceFormView.vue:396,525`, `InvoicesView.vue:150` | `{to_email, cc_email\|null}` | Not read | `detail` goes into a "Retry?" confirm dialog, or the dialog error message |
| **GET `/api/v1/invoices/{id}/pdf`** | `InvoiceFormView.vue:467`, opened with `window.open(…, "_blank")` | Cookie only, **outside axios** (no interceptor) | The browser renders it, so it needs an inline PDF content type | Any error shows as a raw page |

**Recurring templates**

| Method + path | file:line | Request | Response read | Errors |
|---|---|---|---|---|
| POST **`/invoices/recurring-template`** (singular) | `InvoiceFormView.vue:723` | `{customer_id, project_id, currency, po_so_number, payment_terms, notes, footer, internal_note, discount_type, discount_value, wht_type, wht_rate, wht_gross_up, line_items[…same as invoice…], schedule:{frequency:"DAILY"\|"WEEKLY"\|"MONTHLY"\|"YEARLY", interval:number, start_date, end_mode:"NEVER"\|"ON_DATE"\|"AFTER_N", end_date\|null, end_after_cycles\|null}}` | Not read | Template `detail` |
| PUT `/invoices/recurring-templates/{id}` | `InvoiceFormView.vue:721` | Same body | Not read | same |
| GET `/invoices/recurring-templates/{id}` | `InvoiceFormView.vue:301` | none | `{customer_id, project_id, currency, po_so_number, payment_terms, notes, footer, internal_note, discount_type, discount_value, wht_type, wht_rate, wht_gross_up, billing_cycle_ref:{frequency, interval, start_date, end_mode, end_date, end_after_cycles}\|null, line_items:[{line_item_id, item_id, description, quantity, unit_price, position}]}`. **The response key is `billing_cycle_ref`, but the request key is `schedule`.** `line_items` must be an array (`.slice()`). | none |
| GET `/invoices/recurring-templates/rows` | `RecurringInvoicesView.vue:91`, `InvoiceFormView.vue:361` | none | Array `{template_id, customer_id, customer_name, currency, amount, payment_terms, schedule_description: string, frequency, start_date, end_mode, next_run_date, previous_issue_date, generated_count: number, status: "ACTIVE"\|"PAUSED"\|"MISCONFIGURED"\|"ENDED"}`. Filtering is client-side. | none |
| PATCH `/invoices/recurring-templates/{id}` | `RecurringInvoicesView.vue:61` | `{action: "PAUSE"\|"RESUME"\|"END_NOW"}` | Not read | none |

## 6. Quotations

`QuotationOut` (`QuotationDetailView.vue:43-65`):
- `quotation_id`, `customer_id`, `project_id`, `quotation_number`, `po_so_number`, `currency`
- `subtotal`, `discount_type`, `discount_value`, `amount`, `status`
- `issue_date`, `valid_until`, `payment_terms`, `notes`, `footer`
- `last_sent_at`, `accepted_at`, `accepted_by` (timestamps go through `new Date()`), `converted_invoice_id`
- `line_items[{line_item_id, item_id, description, quantity, unit_price, amount, position}]`

Status values: `DRAFT`, `SENT`, `ACCEPTED`, `INVOICED`, `DECLINED`, `EXPIRED`, `VOID`.

| Method + path | file:line | Request | Response read | Errors |
|---|---|---|---|---|
| GET `/quotations` | `QuotationsView.vue:60` (repeated `status`: DRAFT, SENT / ACCEPTED, INVOICED / none); `CustomerDetailView.vue:122` with `{customer_id}`; `ProjectDetailView.vue:92` with `{project_id}` | query | Array `{quotation_id, customer_id, quotation_number, currency, amount, status, issue_date, valid_until, created_at}` | none |
| GET `/quotations/{id}` | `QuotationDetailView.vue:70`, `QuotationFormView.vue:133` | none | `QuotationOut` | none |
| POST `/quotations` | `QuotationFormView.vue:235` | `{customer_id, project_id, currency, quotation_number\|null, po_so_number\|null, issue_date, valid_until, payment_terms\|null, notes\|null, discount_type, discount_value, line_items[{item_id, description, quantity, unit_price, position}]}` (no WHT, no footer) | **`quotation_id`** (navigates to it) | Template line 435 shows `detail` |
| PATCH `/quotations/{id}` | `QuotationFormView.vue:233` | Same body | `quotation_id` | same |
| DELETE `/quotations/{id}` | `QuotationDetailView.vue:219` | none | Not read | `onError` puts `detail` in `errorMsg` |
| POST `/quotations/{id}/mark-sent` | :139 | no body | Not read | `detail` |
| POST `/quotations/{id}/send` | :157 | `{to_email, cc_email\|null, subject, message}` | Not read | `detail`, fallback "Failed to send quotation" |
| POST `/quotations/{id}/accept` | :187 | `{accepted_by: null}` | Not read | `detail` |
| POST `/quotations/{id}/decline` | :198 | no body | Not read | `detail` |
| POST `/quotations/{id}/void` | :209 | no body | Not read | `detail` |
| POST `/quotations/{id}/convert-to-invoice` | :229 | `{}` | **`invoice_id`** (navigates to `/invoices/{id}/edit`) | `detail` |
| **GET `/api/v1/quotations/{id}/pdf`** | `QuotationDetailView.vue:134`, `QuotationFormView.vue:267` (`window.open`, `"noopener"` in the form) | Cookie only, outside axios | Browser renders it | Raw page |

## 7. Projects

`Project` = `{project_id, customer_id, code, name, currency, contract_value: string|null, status: "ACTIVE"|"ON_HOLD"|"COMPLETED"|"CANCELLED", start_date, end_date, notes, active, created_at}`

| Method + path | file:line | Request | Response read | Errors |
|---|---|---|---|---|
| GET `/projects` | `ProjectsView.vue:63` (repeated `status`: ACTIVE / ON_HOLD / none); `BillFormView.vue:119` with `{status:"ACTIVE"}`; `InvoiceFormView.vue:243` and `QuotationFormView.vue:120` with `{customer_id, status:"ACTIVE"}` | query | Array of `Project` (the forms read `project_id, customer_id, code, name, currency, status`) | none |
| GET `/projects/{id}` | `ProjectDetailView.vue:63`, `ProjectFormView.vue:87` | none | `Project` | none |
| POST `/projects` | `ProjectFormView.vue:156` | `{customer_id, code (trimmed, uppercased), name, currency, status, contract_value:number\|null, start_date, end_date, notes\|null}` | Not read | `detail`, fallback "Save failed" |
| PATCH `/projects/{id}` | `ProjectFormView.vue:154` | Same plus `active` | Not read | same |
| GET `/projects/{id}/summary` | `ProjectDetailView.vue:69` | none | `{invoiced_total, paid_total, outstanding_total, contract_value\|null, invoice_count: number}` (`contract_remaining` and `bill_total` are typed but unused) | none |
| GET `/projects/{id}/invoices` | `ProjectDetailView.vue:75` | none | Array `{invoice_id, invoice_number, invoice_type, amount, balance_due, status, currency, issue_date, due_date}` | none |

## 8. Vendors

`Vendor` = `{vendor_id, name, contact_email, contact_name, default_currency, payment_terms_days: number, tax_id, notes, active, created_at}`

| Method + path | file:line | Request | Response read | Errors |
|---|---|---|---|---|
| GET `/vendors` | `VendorsView.vue:36` with `{active:true}` or none; `BillFormView.vue:113` with `{active:true}`; `BillsView.vue:68` with none | query | Array of `Vendor` | none |
| GET `/vendors/{id}` | `VendorDetailView.vue:26`, `VendorFormView.vue:71` | none | `Vendor` | Detail view shows a generic "Failed to load vendor." |
| POST `/vendors` | `VendorFormView.vue:107` | `{name, contact_name, contact_email, default_currency, payment_terms_days:number, tax_id, notes, active}`. **Keys whose value is `""` are deleted**, so on edit a cleared field is omitted rather than sent as null. | Not read | `detail`, fallback "Save failed" |
| PATCH `/vendors/{id}` | `VendorFormView.vue:105`; `VendorsView.vue:47` sends `{active:true}` | Partial | Not read | same |
| DELETE `/vendors/{id}` | `VendorsView.vue:41` | none | Not read | none |

## 9. Bills

`BillOut` = `{bill_id, vendor_id, project_id, bill_number, po_number, currency, amount, balance_due, status (DRAFT|OPEN|PARTIAL|PAID|VOID), issue_date, due_date, payment_terms, notes, discount_type, discount_value, line_items[{line_item_id, expense_account_id: number|null, description, quantity, unit_price, amount, position}]}`

The list row adds `created_at` and drops `line_items`.

| Method + path | file:line | Request | Response read | Errors |
|---|---|---|---|---|
| GET `/bills` | `BillsView.vue:62` | Repeated `status` (OPEN, PARTIAL / DRAFT / none) | Array of list rows | none |
| GET `/bills/{id}` | `BillFormView.vue:138` | none | `BillOut` | none |
| POST `/bills` | `BillFormView.vue:266` | `{vendor_id, project_id, bill_number\|null, po_number\|null, currency, issue_date, due_date, payment_terms\|null, notes\|null, line_items[{expense_account_id, description, quantity:number, unit_price:number, position}]}` | Not read | Template line 462 shows `detail` |
| PATCH `/bills/{id}` | `BillFormView.vue:264` | Same | Not read | same |
| POST `/bills/{id}/record-payment` | `BillFormView.vue:205` | The dialog's `$event` is passed **unstripped**: `{amount, wht_amount:0, payment_date, payer_name\|null, payer_reference\|null, notes\|null, receipt:null}`. The server must accept or ignore the extra keys. | Not read | `detail`, fallback "Failed to record payment" |

## 10. Items

`Item` (`ItemFormView.vue:30-52`):
- `item_id`, `sku`, `name`, `item_type: "SERVICE"|"USAGE"|"FIXED_FEE"`, `description`, `default_currency`
- `default_unit_price: string|null`, `default_purchase_price: string|null`
- `revenue_account_id: number|null`, `expense_account_id: number|null`
- `is_sold`, `is_purchased`, `active`, `created_at`
- `is_hosting`, `hosting_domain`, `hosting_grace_days`, `hosting_suspension_enabled`, `hosting_status`, `hosting_last_action_at`, `hosting_last_error`
- `cloudflare_target: {target_id, item_id, zone_id, record_id, record_name, record_type, live_content, maintenance_content, proxied, provider_status, last_action_at, last_error}|null`

| Method + path | file:line | Request | Response read | Errors |
|---|---|---|---|---|
| GET `/items` | `ItemsView.vue:43`, `InvoiceFormView.vue:253`, `QuotationFormView.vue:112` | none | Array (the forms read `item_id, name, description, default_unit_price, default_currency`) | none |
| GET `/items/{id}` | `ItemDetailView.vue:35`, `ItemFormView.vue:135` | none | `Item`; the form shows `hosting_status`, `cloudflare_target.provider_status` and `hosting_last_error` | Detail view shows a generic error |
| POST `/items` | `ItemFormView.vue:236` | Always `{name, item_type, default_currency, active, is_sold, is_purchased, is_hosting}`. Optional: `sku`, `description`; when sold, `default_unit_price` and `revenue_account_id`; when purchased, `default_purchase_price` and `expense_account_id`; when hosting, `hosting_domain`, `hosting_grace_days`, `hosting_suspension_enabled` and `cloudflare_target{zone_id, record_id, record_name, record_type, live_content, maintenance_content, proxied}` | Not read | `detail`, fallback "Save failed" |
| PATCH `/items/{id}` | `ItemFormView.vue:234`; `ItemsView.vue:63` sends `{active:true}` | Partial | Not read | same |
| DELETE `/items/{id}` | `ItemsView.vue:58` | none | Not read | none |

## 11. Accounting

`Account` = `{account_id: number, code, name, type: ASSET|LIABILITY|EQUITY|INCOME|COGS|EXPENSE, active}`

| Method + path | file:line | Request | Response read | Errors |
|---|---|---|---|---|
| GET `/accounting/chart-of-accounts` | `ChartOfAccountsView:26`, `BillFormView:124`, `ItemFormView:174`, `ItemsView:48`, `ItemDetailView:40` | none | Array of `Account` | none |
| GET `/accounting/chart-of-accounts/{accountId}` | `AccountFormView.vue:44` | none | `Account` | none |
| POST `/accounting/chart-of-accounts` | `AccountFormView.vue:65` | `{code, name, type, active}` | Not read | `detail`, fallback "Save failed" |
| PATCH `/accounting/chart-of-accounts/{id}` | `AccountFormView.vue:60` sends the full body; `ChartOfAccountsView.vue:50` sends `{active:true}` | Partial | Not read | same |
| DELETE `/accounting/chart-of-accounts/{id}` | `ChartOfAccountsView.vue:44` | none (deactivate) | Not read | none |
| GET `/accounting/base-currency` | `FxRatesView.vue:37`, `FxRateNewView.vue:21` | none | `{base_currency: string}` | Errors are swallowed |
| GET `/accounting/fx-rates` | `FxRatesView.vue:46` | none | Array `{rate_id: number, from_currency, to_currency, rate: string, as_of_date, source, created_at}` | none |
| POST `/accounting/fx-rates` | `FxRateNewView.vue:48` | `{from_currency, to_currency, rate:number, as_of_date:"YYYY-MM-DD", source (default "MANUAL")}` | Not read | `detail`, fallback "Save failed" |
| DELETE `/accounting/fx-rates/{rate_id}` | `FxRatesView.vue:50` | none | Not read | none |
| POST `/accounting/fx-rates/sync` | `FxRatesView.vue:58` | no body | `{created: number, updated: number, fetched: number, source: string}` (`pairs` is typed but unused) | `detail`, fallback "Sync failed" |
| GET `/accounting/reports/trial-balance` | `ReportsView.vue:107` | `?as_of=YYYY-MM-DD` | `{base_currency, total_debit_base, total_credit_base, rows[{account_id, code, name, type, currency, debit, credit, net, base_debit, base_credit, base_net}]}` | none |
| GET `/accounting/reports/balance-sheet` | :115 | `as_of` | `{base_currency, groups[{type, total_base, lines[{account_id, code, name, base_balance}]}], assets_total_base, liabilities_total_base, equity_total_base, income_total_base, expense_total_base}` | none |
| GET `/accounting/reports/fx-revaluation` | :123 | `as_of` | `{base_currency, lines[{account_id, code, name, type, currency, face_balance, original_base, revalued_base\|null, rate\|null, rate_as_of\|null, unrealised_gain_loss\|null, rate_missing: boolean}], total_unrealised_gain, total_unrealised_loss, missing_rates: string[]}`. `formatRate` and `gainLossSeverity` check `=== null` strictly. | none |
| GET `/accounting/reports/ar-aging` | :131 | `as_of` | `{as_of, kind: "AR", rows[{party_id, party_name, currency, buckets{current, d_1_30, d_31_60, d_61_90, d_90_plus, total}}], totals_by_currency: {[ccy]: buckets}}` | none |
| GET `/accounting/reports/ap-aging` | :139 | `as_of` | Same shape with `kind: "AP"` | none |

## 12. Payments, reconciliation and stats

| Method + path | file:line | Request | Response read | Errors |
|---|---|---|---|---|
| GET `/payments/awaiting-review` | `ManualReviewView.vue:45` | none | Array `{payment_id, invoice_id, customer_id, amount, currency, payer_name, payer_reference, payment_date, status (CLEARED/FLAGGED/…), adjustment_type, confidence_score: string\|null, created_at}` | none |
| GET `/payments/{paymentId}/log` | :51 | none | Array `{log_id, action, reasons: string[], created_at}`. **Only `log[0].reasons` is shown**, so the order is assumed newest-first (or most relevant first). | none |
| POST `/payments/{paymentId}/manual-review` | :57 | `{invoice_id, adjustment_type: "NONE"}` | Not read | Template line 170 shows `detail`, fallback "Approval failed" |
| POST `/payments/{paymentId}/reverse` | :71 | `{reason}` | Not read | Not shown |
| GET `/payments/audit-log` | `AuditLogView.vue:58` | Optional `action` (CLEARED, MATCHED, HELD, OVERRIDDEN, REVERSED), `payer`, `from_date`, `to_date` (YYYY-MM-DD) | Array `{log_id, payment_id, action, reasons: string[], actor_user_id, actor_email, created_at, payer_name, amount\|null, currency\|null, invoice_id\|null, intake_source\|null}` | none |
| GET `/stats/dashboard` | `PaymentIntakeWidget.vue:78`, polled every 30 s | none | `{awaiting_review_count, draft_count, sent_count, customer_count, auto_cleared_last_30d, open_quotation_count` (all numbers)`, open_ar_by_currency[{currency, amount}], open_quotation_pipeline[{currency, amount}]` (**the server's order is used: the first entry is shown as the top one**)`, mrr_by_currency[{currency, amount}], base_currency, open_ar_base_by_currency[{currency, base_amount}], open_ar_unconverted: string[], upcoming_invoices[{invoice_id, customer_id, customer_name, mode: "RECURRING"\|"USAGE", next_date: "YYYY-MM-DD", amount, currency, amount_is_estimate: boolean}], recent_activity[{log_id, payment_id, action, reasons: string[], actor_user_id, created_at, payer_name, amount, currency, payment_date, payer_reference, wht_amount, confidence_score, intake_source, invoice_id, invoice_number, customer_name, actor_name}]}` | Generic error state with Retry |
| GET `/stats/revenue-monthly` | :96, polled every 60 s | none | `{base_currency, currencies: string[], unconverted: string[], months[{month: string, totals, native_totals, unpaid_totals, native_unpaid_totals}]}`. The four month fields are each `Record&lt;ccy, string&gt;`. | none |
| GET `/stats/invoices-summary` | `InvoicesView.vue:94` | none | `{overdue[{currency, amount}], due_30d[{currency, amount}], avg_days_to_pay: number\|null}` (`.toFixed(1)` is called on it) | none |

---

## 13. Response-shape hazards for a Go rewrite

1. **Arrays must never be `null`.** The code calls `.length`, `.slice()` or `.join` on these without a guard, and Go's nil slice encodes as `null`:
   - dashboard: `mrr_by_currency`, `open_ar_by_currency`, `open_ar_base_by_currency`, `open_ar_unconverted`, `upcoming_invoices`, `recent_activity`, `recent_activity[].reasons`
   - revenue: `currencies`, `months`
   - `matching_aliases` (CustomersView)
   - `quote.line_items` and recurring-template `line_items`
   - `fxReval.missing_rates.join`
   - every list endpoint (DataTable takes `data ?? []`, so a top-level null is survivable, but return `[]`)
2. **Optional fields must be present as `null`, not omitted.** `a.confidence_score !== null` (`PaymentIntakeWidget.vue:520`) and the report helpers in `ReportsView.vue:145,153` check strict `null`. A missing key renders "NaN%" or "NaN".
3. **Decimal strings shown verbatim**, so their scale and format must match the current backend exactly:
   - `ln.quantity` (`QuotationDetailView.vue:554`)
   - `quote.discount_value` followed by `%` (:566)
   - `${q.amount} ${q.currency}` in the default send-email text (:179)
   - `confidence_score` (ManualReview column)
   - the `formatRate` fallback
   - Everything else goes through `Number()` and is forgiving.
4. **Path collisions to order carefully in the Go router:**
   - `/customers/balances` vs `/customers/{id}`
   - `/invoices/awaiting-finalization` and `/invoices/recurring-templates/rows` vs `/invoices/{id}` and `/invoices/recurring-templates/{id}`
   - the singular `/invoices/recurring-template` (POST)
   - `/payments/audit-log` and `/payments/awaiting-review` vs `/payments/{id}/…`
   - `/accounting/fx-rates/sync` vs `/accounting/fx-rates/{id}`
   - `/users/me` vs `/users/{id}/…`
5. **PATCH must be a partial update:** omitted key means unchanged, explicit `null` means clear. Reactivation sends `{active:true}` alone, and vendor edit drops empty strings.
6. Request bodies can contain **extra or unused keys** (bill record-payment sends `receipt: null`, `wht_amount` and `payer_name`). The current FastAPI backend ignores extras, so Go must not reject unknown fields.
7. `customer_id` in invoice and quotation list rows must be a non-null string (`.slice(0,8)` fallback). `ManualReview` calls `selected.payment_id.slice`.

---

## Totals

**96 distinct endpoints:**
- 94 via axios
- 2 browser `window.open` GETs: `/api/v1/invoices/{id}/pdf` and `/api/v1/quotations/{id}/pdf`

| Group | Count |
|---|---|
| Auth | 6 |
| Users | 4 |
| Business profile | 4 |
| Customers | 8 |
| Invoices | 13 |
| Recurring templates | 5 |
| Quotations | 12 |
| Projects | 6 |
| Vendors | 5 |
| Bills | 5 |
| Items | 5 |
| Accounting | 15 |
| Payments | 5 |
| Stats | 3 |

`/mcp`, `/oauth/*` and `/.well-known/*` are never called by the SPA. They are only proxied by nginx for Claude's MCP connector.

## Error-shape assumptions

1. **`err.response.data.detail` is assumed to be a string** at every error site. No code handles the FastAPI 422 array shape `[{loc, msg, type}]`, and the current backend has no custom `RequestValidationError` handler. A 422 currently renders as a JSON dump in `&lt;Message&gt;` blocks. The exceptions are LoginView, which falls back to "Login failed…", and the `saveError` refs, which show "[object Object]"-style text. A Go backend that always returns `{"detail": "&lt;string&gt;"}` would be safe, and would actually look better than today.
2. **The only status branch is 401**, in the interceptor. It applies to every URL except those containing `/auth/me`: it clears the user and redirects to `/login?next=` unless the current route is public. So:
   - Wrong-password style errors on authenticated pages, such as change-password and admin user reset, **must not be 401**. The current backend uses 400.
   - Login failure is 401 with `detail: "invalid credentials"`, and the wording must match `/invalid|incorrect|unauthor|credentials/i` for the friendly message.
   - Inactive accounts should match `/disabled|locked|inactive/i`.
3. No 403, 404, 409 or 422 handling. Detail pages (vendor, item) show a generic message on any error. Most queries have no error UI.
4. `GET /auth/me`: any non-2xx counts as "signed out".
5. The send-receipt failure path reads `detail`, falling back to "the email service returned an error", and offers a Retry.
6. PDF endpoints are opened outside axios, so a 401 or 404 there is never intercepted and is shown raw in a new tab.
7. No CSRF token or header is sent or expected. The cookie session uses `withCredentials: true`. Optional `sentry-trace` and `baggage` request headers may arrive.</result>
<usage><subagent_tokens>243914</subagent_tokens><tool_uses>77</tool_uses><duration_ms>326116</duration_ms></usage>
</task-notification>

---

<task-notification>
<task-id>ab2e9c965e8dbdf95</task-id>
<tool-use-id>toolu_012rHwNepHXXYnDCW4fi9ez9</tool-use-id>
<output-file>C:\Users\wenha\AppData\Local\Temp\claude\D--Development-wenhao-billing\487ee98e-d4ae-4704-ac1b-22b7889a6a4d\tasks\ab2e9c965e8dbdf95.output</output-file>
<status>completed</status>
<summary>Agent "Inventory backend behaviour" finished</summary>
<note>A task-notification fires each time this agent stops with no live background children of its own. The user can send it another message and resume it, so the same task-id may notify more than once.</note>
<result>The FastAPI backend has 115 routes, a `/mcp` sub-app with 42 MCP tools, 22 database tables and 28 Alembic migrations (head `0028_business_npwp`). I read all of it without modifying anything. Line numbers point to the `def` line of each handler; the decorator is on the line above.

**Behaviours the Go port must copy, because the frontend sees them:**
- **Decimals are JSON strings, not numbers.** They keep the database's 4 decimal places, e.g. `"1000.0000"`. FX rates have 8 decimal places.
- **Datetimes have no timezone.** Most columns are `timestamp without time zone`, so values serialize like `2026-01-01T10:00:00.123456`. The two OAuth tables are the exception.
- **There is no global exception handler.** Errors are FastAPI's default `{"detail": "&lt;string&gt;"}`. Anything not caught returns a plain-text 500 `Internal Server Error`.
- **Some 422s carry a string `detail`, not the usual list.** These come from `business_profile.py:79,82,84` and `customers.py:86,116`.
- **Login is a signed session cookie, not JWT.** The cookie is `billing_session`. Its format is described in section 3.
- **Single-tenant.** There is no `business_id` anywhere. The business profile is always row id 1.
- **The `/mcp` and OAuth routes use their own error shapes.** OAuth returns `{"error", "error_description"}`.

---

## 1. Routes

All files are under `backend/app/`. Routers are mounted with prefix `/api/v1` in `main.py:87-101`. The OAuth router is mounted at the root, and only when `mcp_enabled` is true (`main.py:105-119`).

Auth key: **A** = `Depends(current_admin)` (`api/deps.py:18`). The default status is 200 unless a different one is listed.

### auth.py — prefix `/auth` (6 routes)
| Method | Path | Line | Auth | Status | Request | Response |
|---|---|---|---|---|---|---|
| POST | /api/v1/auth/login | 45 | none | 200 | LoginRequest | UserOut |
| POST | /api/v1/auth/logout | 64 | none | 204 | – | – |
| GET | /api/v1/auth/me | 69 | A | 200 | – | UserOut |
| POST | /api/v1/auth/change-password | 74 | A | 200 | ChangePasswordRequest | UserOut |
| POST | /api/v1/auth/forgot-password | 99 | none | 200 | ForgotPasswordRequest | ForgotPasswordResponse |
| POST | /api/v1/auth/reset-password | 137 | none | 200 | ResetPasswordRequest | UserOut |

### users.py — `/users` (4 routes)
| Method | Path | Line | Auth | Status | Request | Response |
|---|---|---|---|---|---|---|
| GET | /api/v1/users | 19 | A | 200 | – | list[UserOut] |
| PATCH | /api/v1/users/me | 27 | A | 200 | ProfileUpdate | UserOut |
| POST | /api/v1/users | 50 | A | 201 | UserCreate | UserOut |
| POST | /api/v1/users/{user_id}/reset-password | 70 | A | 200 | UserPasswordReset | UserOut |

### invoices.py — `/invoices` (23 routes, all A)
| Method | Path | Line | Status | Request | Response |
|---|---|---|---|---|---|
| POST | /invoices | 55 | 201 | InvoiceCreate | InvoiceOut |
| POST | /invoices/milestone | 70 | 201 | MilestoneInvoiceCreate | InvoiceOut |
| POST | /invoices/recurring-template | 90 | 201 | RecurringTemplateCreate | InvoiceOut |
| GET | /invoices/recurring-templates | 105 | 200 | – | list[InvoiceOut] |
| GET | /invoices/recurring-templates/rows | 216 | 200 | – | list[RecurringTemplateRow] (inline, line 118) |
| GET | /invoices/recurring-templates/{template_id} | 288 | 200 | – | InvoiceOut |
| PUT | /invoices/recurring-templates/{template_id} | 300 | 200 | RecurringTemplateCreate | InvoiceOut |
| PATCH | /invoices/recurring-templates/{template_id} | 325 | 200 | RecurringTemplateAction `{action: PAUSE\|RESUME\|END_NOW}` (inline, line 320) | InvoiceOut |
| POST | /invoices/recurring/{template_id}/trigger | 366 | 200 | RecurringTrigger | InvoiceOut |
| POST | /invoices/usage/{invoice_id}/lock | 392 | 200 | UsageLockRequest | InvoiceOut |
| GET | /invoices/awaiting-finalization | 412 | 200 | – | list[InvoiceOut] |
| GET | /invoices | 427 | 200 | query: `status` (repeatable), `customer_id`, `invoice_type`, `limit` (default 200, max 500) | list[InvoiceOut] |
| POST | /invoices/{id}/finalize | 454 | 200 | – | InvoiceOut |
| POST | /invoices/{id}/void | 468 | 200 | VoidRequest | InvoiceOut |
| GET | /invoices/{id} | 483 | 200 | – | InvoiceOut |
| PATCH | /invoices/{id} | 495 | 200 | InvoiceUpdate | InvoiceOut |
| POST | /invoices/{id}/record-payment | 511 | 201 | InvoiceRecordPaymentRequest | PaymentOut |
| GET | /invoices/{id}/payments | 598 | 200 | – | list[PaymentOut] (CLEARED only) |
| POST | /invoices/{id}/mark-paid | 617 | 200 | – | InvoiceOut |
| POST | /invoices/{id}/payments/{payment_id}/send-receipt | 637 | 200 | SendReceiptRequest | raw `{"sent_to": str}` |
| GET | /invoices/{id}/pdf | 700 | 200 | – | `application/pdf`, `Content-Disposition: inline; filename="invoice-&lt;number or 8-char id&gt;.pdf"` |
| POST | /invoices/{id}/send | 721 | 200 | SendInvoiceRequest | InvoiceOut |
| DELETE | /invoices/{id} | 782 | 204 | – | – |

Route order matters: the literal paths are registered before `/{invoice_id}`.

### bills.py — `/bills` (6 routes, all A)
| Method | Path | Line | Status | Request | Response |
|---|---|---|---|---|---|
| POST | /bills | 20 | 201 | BillCreate | BillOut |
| GET | /bills | 35 | 200 | query: `status` (repeatable), `vendor_id` | list[BillOut] |
| GET | /bills/{bill_id} | 53 | 200 | – | BillOut |
| PATCH | /bills/{bill_id} | 65 | 200 | BillUpdate | BillOut |
| POST | /bills/{bill_id}/record-payment | 84 | 200 | RecordPaymentRequest | BillOut |
| POST | /bills/{bill_id}/void | 120 | 200 | raw `dict`, reads optional `reason` | BillOut |

### customers.py — `/customers` (8 routes, all A)
| Method | Path | Line | Status | Request | Response |
|---|---|---|---|---|---|
| GET | /customers | 41 | 200 | query: `active` | list[CustomerOut] |
| GET | /customers/balances | 60 | 200 | – | list[CustomerBalanceRow] (inline, line 52) |
| POST | /customers | 78 | 201 | CustomerCreate | CustomerOut |
| GET | /customers/{id} | 93 | 200 | – | CustomerOut |
| PATCH | /customers/{id} | 105 | 200 | CustomerUpdate | CustomerOut |
| DELETE | /customers/{id} | 123 | 200 | – | CustomerOut (soft delete: `active=false`) |
| GET | /customers/{id}/billing-summary | 148 | 200 | – | BillingSummary (inline, line 29) |
| GET | /customers/{id}/invoices | 205 | 200 | – | list[InvoiceOut] |

### payments.py — `/payments` (2 routes, intake webhooks)
| Method | Path | Line | Auth | Status | Request | Response |
|---|---|---|---|---|---|---|
| POST | /api/v1/payments/intake/webhook | 19 | HMAC: `X-Signature` = hex HMAC-SHA256 of the raw body, keyed by `webhook_hmac_secret` (`deps.py:37`) | 201 | raw JSON; required fields `amount, currency, payer_name, payment_date, external_ref` (`services/intake/webhook.py`) | PaymentOut |
| POST | /api/v1/payments/intake/email | 43 | `X-Email-Token` header compared constant-time against `email_webhook_token` (`deps.py:52`) | 201 | raw `dict` (SendGrid/Postmark shape) | PaymentOut |

### recon.py — also prefix `/payments`, tag "recon" (6 routes, all A)
| Method | Path | Line | Status | Request | Response |
|---|---|---|---|---|---|
| GET | /payments/audit-log | 21 | 200 | query: `action`, `payer`, `from_date`, `to_date`, `limit` (default 200, max 500) | raw list[dict]: ids as strings, `amount` as string, `created_at` naive ISO |
| GET | /payments/awaiting-review | 80 | 200 | – | list[PaymentOut] |
| GET | /payments/{payment_id} | 94 | 200 | – | PaymentOut |
| GET | /payments/{payment_id}/log | 106 | 200 | – | raw list[dict] `{log_id, action, reasons, actor_user_id, created_at}`; returns `[]` if the payment does not exist, never 404 |
| POST | /payments/{payment_id}/manual-review | 129 | 200 | ManualApproveRequest | PaymentOut |
| POST | /payments/{payment_id}/reverse | 151 | 200 | ReverseRequest | PaymentOut |

### items.py — `/items` (5 routes, all A)
| Method | Path | Line | Status | Request | Response |
|---|---|---|---|---|---|
| GET | /items | 18 | 200 | query: `active`, `item_type`, `is_hosting` | list[ItemOut] |
| POST | /items | 36 | 201 | ItemCreate | ItemOut |
| GET | /items/{id} | 48 | 200 | – | ItemOut |
| PATCH | /items/{id} | 60 | 200 | ItemUpdate | ItemOut |
| DELETE | /items/{id} | 76 | 200 | – | ItemOut (soft delete) |

### vendors.py — `/vendors` (5 routes, all A)
| Method | Path | Line | Status | Request | Response |
|---|---|---|---|---|---|
| GET | /vendors | 18 | 200 | query: `active` | list[VendorOut] |
| POST | /vendors | 30 | 201 | VendorCreate | VendorOut |
| GET | /vendors/{id} | 42 | 200 | – | VendorOut |
| PATCH | /vendors/{id} | 54 | 200 | VendorUpdate | VendorOut |
| DELETE | /vendors/{id} | 70 | 200 | – | VendorOut (soft delete) |

### accounting.py — `/accounting` (16 routes, all A)
| Method | Path | Line | Status | Request | Response |
|---|---|---|---|---|---|
| GET | /accounting/chart-of-accounts | 46 | 200 | query: `active` | list[AccountOut] |
| POST | /accounting/chart-of-accounts | 60 | 201 | AccountCreate | AccountOut |
| GET | /accounting/chart-of-accounts/{account_id:int} | 77 | 200 | – | AccountOut |
| PATCH | /accounting/chart-of-accounts/{account_id} | 89 | 200 | AccountUpdate | AccountOut |
| DELETE | /accounting/chart-of-accounts/{account_id} | 110 | 200 | – | AccountOut (soft delete) |
| GET | /accounting/base-currency | 130 | 200 | – | raw `{"base_currency": "IDR"}` |
| GET | /accounting/fx-rates | 135 | 200 | query: `from_currency`, `to_currency` (each 3 chars) | list[FxRateOut] |
| POST | /accounting/fx-rates | 150 | 201 | FxRateCreate | FxRateOut |
| POST | /accounting/fx-rates/sync | 176 | 200 | – | FxSyncResult |
| GET | /accounting/fx-rates/latest | 195 | 200 | query: `from_currency` (required), `to_currency`, `on_date` | FxRateOut |
| DELETE | /accounting/fx-rates/{rate_id:int} | 219 | 204 | – | – |
| GET | /accounting/reports/trial-balance | 242 | 200 | query: `as_of` (default today) | TrialBalanceReport |
| GET | /accounting/reports/balance-sheet | 313 | 200 | query: `as_of` | BalanceSheetReport |
| GET | /accounting/reports/fx-revaluation | 388 | 200 | query: `as_of` | FxRevaluationReport |
| GET | /accounting/reports/ar-aging | 557 | 200 | query: `as_of` | AgingReport |
| GET | /accounting/reports/ap-aging | 606 | 200 | query: `as_of` | AgingReport |

### business_profile.py — `/business-profile` (4 routes, all A)
| Method | Path | Line | Status | Request | Response |
|---|---|---|---|---|---|
| GET | /business-profile | 31 | 200 | – | BusinessProfileOut |
| PUT | /business-profile | 39 | 200 | BusinessProfileUpdate (only fields that are sent get changed) | BusinessProfileOut |
| GET | /business-profile/payment-accounts | 63 | 200 | – | list[PaymentAccountOut] |
| PUT | /business-profile/payment-accounts | 72 | 200 | list[PaymentAccountIn] (replaces the whole set) | list[PaymentAccountOut] |

### projects.py — `/projects` (7 routes, all A)
| Method | Path | Line | Status | Request | Response |
|---|---|---|---|---|---|
| POST | /projects | 34 | 201 | ProjectCreate | ProjectOut |
| GET | /projects | 49 | 200 | query: `customer_id`, `status` (repeatable), `active` | list[ProjectOut] |
| GET | /projects/{id} | 70 | 200 | – | ProjectOut |
| PATCH | /projects/{id} | 79 | 200 | ProjectUpdate | ProjectOut |
| DELETE | /projects/{id} | 96 | 204 | – | – (hard delete; 400 if any invoice or bill references it) |
| GET | /projects/{id}/summary | 119 | 200 | – | ProjectSummary |
| GET | /projects/{id}/invoices | 165 | 200 | – | list[InvoiceOut] |

### quotations.py — `/quotations` (12 routes, all A)
| Method | Path | Line | Status | Request | Response |
|---|---|---|---|---|---|
| POST | /quotations | 32 | 201 | QuotationCreate | QuotationOut |
| GET | /quotations | 47 | 200 | query: `status` (repeatable), `customer_id`, `project_id`, `limit` (default 200, max 500) | list[QuotationOut] |
| GET | /quotations/{id} | 69 | 200 | – | QuotationOut |
| PATCH | /quotations/{id} | 81 | 200 | QuotationUpdate | QuotationOut |
| DELETE | /quotations/{id} | 97 | 204 | – | – |
| POST | /quotations/{id}/mark-sent | 110 | 200 | – | QuotationOut |
| POST | /quotations/{id}/accept | 124 | 200 | AcceptQuotationRequest | QuotationOut |
| POST | /quotations/{id}/decline | 139 | 200 | – | QuotationOut |
| POST | /quotations/{id}/void | 153 | 200 | – | QuotationOut |
| GET | /quotations/{id}/pdf | 167 | 200 | – | PDF, `quotation-&lt;number&gt;.pdf` |
| POST | /quotations/{id}/send | 187 | 200 | SendQuotationRequest | QuotationOut |
| POST | /quotations/{id}/convert-to-invoice | 247 | 201 | ConvertToInvoiceRequest | InvoiceOut |

### stats.py — `/stats` (3 routes, all A)
| Method | Path | Line | Response |
|---|---|---|---|
| GET | /stats/dashboard | 119 | DashboardStats (inline, line 101) |
| GET | /stats/revenue-monthly | 236 | MonthlyRevenueResponse (line 86) |
| GET | /stats/invoices-summary | 276 | InvoicesSummaryResponse (line 95) |

### mcp/oauth.py — mounted at root (7 routes)
| Method | Path | Line | Auth | Status | Body | Response |
|---|---|---|---|---|---|---|
| GET | /.well-known/oauth-authorization-server | 283 | none | 200 | – | metadata dict (S256, `token_endpoint_auth_methods: ["none"]`) |
| GET | /.well-known/oauth-protected-resource | 298 | none | 200 | – | `{resource, authorization_servers}` |
| POST | /oauth/register | 306 | none | 201 | JSON RegisterRequest `{redirect_uris[], client_name?}` | client JSON |
| GET | /oauth/authorize | 326 | session cookie | 200 HTML consent page / 302 to `{app_base_url}/login?return_to=…` / 4xx OAuth error | query params | HTML |
| POST | /oauth/authorize/consent | 383 | session + CSRF (`session["oauth_authorize_csrf"]`) | 302 to `redirect_uri?code=…&amp;state=…` | form | – |
| POST | /oauth/token | 442 | public client + PKCE | 200 | form: `grant_type` = `authorization_code` or `refresh_token` | `{access_token, token_type: "Bearer", expires_in, refresh_token}` |
| POST | /oauth/revoke | 501 | none | always 200 `{}` | form `token` | – |

### main.py
- `GET|POST|DELETE /mcp` → 307 redirect to `/mcp/` (`main.py:113-117`).
- `app.mount("/mcp", mcp_http_app)` (`main.py:119`). This is the FastMCP 3.4.3 streamable-HTTP app (`mcp/server.py:222`), protected by a Bearer `BillingTokenVerifier` (`mcp/verifier.py`). Its Host/Origin allowlist comes from `mcp_public_base_url` (`server.py:191-227`); a wrong Host gets 421.
- There are 42 MCP tools: `ping` (`server.py:183`) plus 41 tools in `mcp/tools/*`:
  - **Reads:** `list_bills`, `get_bill`, `list_items`, `list_vendors`, `resolve_vendor`, `list_customers`, `get_customer`, `resolve_customer`, `list_invoices`, `get_invoice`, `get_business_profile`, `get_stats`, `list_projects`, `get_project`, `list_quotations`, `get_quotation`, `get_reconciliation_queue`.
  - **Writes:** `create_bill`, `update_bill`, `create_item`, `update_item`, `create_vendor`, `update_vendor`, `create_customer`, `update_customer`, `create_invoice`, `update_invoice`, `finalize_invoice`, `void_invoice`, `create_recurring_template`, `trigger_recurring`, `create_project`, `update_project`, `create_quotation`, `update_quotation`, `resolve_reconciliation_match`.
  - **Gated (two-step `confirm_token`):** `delete_invoice`, `delete_bill`, `delete_customer`, `send_invoice_email`, `send_payment_reminder`.
- `_NoStoreMiddleware` (`main.py:144`) sets `Cache-Control: no-store` on every path starting with `/api/`, `/oauth/`, `/mcp` or `/.well-known/`.

### Totals
| Router | Routes |
|---|---|
| accounting | 16 |
| auth | 6 |
| bills | 6 |
| business_profile | 4 |
| customers | 8 |
| invoices | 23 |
| items | 5 |
| payments | 2 |
| projects | 7 |
| quotations | 12 |
| recon | 6 |
| stats | 3 |
| users | 4 |
| vendors | 5 |
| **/api/v1 total** | **107** |
| OAuth | 7 |
| `/mcp` redirect | 1 |
| **Total** | **115**, plus the `/mcp` mount |

---

## 2. Error handling

**There is no global exception handler.** No `exception_handler` or `add_exception_handler` exists anywhere in `app/`. That means:
- `HTTPException` returns `{"detail": "&lt;string&gt;"}`.
- Validation errors return 422 `{"detail": [{"type", "loc", "msg", "input", "ctx"?}]}`. This is FastAPI with pydantic v2; FastAPI omits `url`.
- Anything uncaught returns a plain-text 500 `Internal Server Error`. Because CORS is not the outermost middleware, these 500s have no CORS headers.

**`HTTPException` raised with a string `detail` on 422** (not the list shape):
- `business_profile.py:79` "Duplicate currency in payment accounts"
- `business_profile.py:82` "Unsupported currency: …" (allowed: SGD, IDR, USD)
- `business_profile.py:84` "At most one account can be the default"
- `customers.py:86,116` — the `ValueError` message from the customers service

**Custom exception classes and how routers map them:**
| Exception | Defined | Mapped to |
|---|---|---|
| `InvoicingError` | `services/invoicing.py:36` | 400, `str(e)` |
| `QuotingError` | `services/quoting.py:39` | 400 |
| `BillError` | `services/billing_ap.py:19` | 400 |
| `ProjectError` | `services/projects.py:22` | 400 |
| `CustomerError` | `services/customers.py:20` | 404 "customer not found" (update only) |
| `ItemError` | `services/items.py:19` | 404 (update only) |
| `VendorError` | `services/vendors.py:15` | 404 (update only) |
| `FxProviderError` | `services/fx_provider.py:24` | 502 (`accounting.py:189`) |
| `ScheduleError` (ValueError) | `services/recurring_schedule.py:29` | caught internally |
| `LedgerError` | `services/ledger.py:35` | not mapped → 500 |
| `FxRateMissing` | `services/fx.py:26` | not mapped → 500 |
| `HostingError` | `services/hosting.py:26` | not mapped → 500 |
| `CloudflareError` | `services/cloudflare.py:11` | not mapped → 500 |
| `TokenError` | `mcp/tokens.py:21` | OAuth error JSON |
| `ConfirmError` | `mcp/confirm.py:18` | MCP tool error dict |
| `ToolAuthError` | `mcp/context.py:19` | MCP 401 |
| `_BillDeleteError` | `mcp/tools/gated_delete.py:50` | MCP tool error dict |

`IntegrityError` maps to 409 at `accounting.py:69,102,165` and `payments.py:36,56`. Plain `ValueError` maps to 400 in `recon.py:137,144,160` and `payments.py:30,50`.

**Unhandled paths that currently return 500:**
- An email failure in `POST /invoices/{id}/send` (`invoices.py:766`) and `POST /quotations/{id}/send`. `send-receipt` is different: it wraps the failure as 502 (`invoices.py:694`).
- `LedgerError` or `FxRateMissing` during webhook/email intake, manual-review or reverse. Only `ValueError` is caught there.
- `POST /invoices/milestone` has no try/except at all.
- `POST /items` and `POST /vendors` have no try/except at all.

**Every `HTTPException` (file:line, status, detail):**
- **deps.py**
  - 21: 401 "not authenticated"
  - 28: 401 "session expired"
  - 33: 403 "admin required"
  - 43: 400 "missing X-Signature header"
  - 48: 401 "invalid webhook signature"
  - 59: 401 "invalid email webhook token"
- **auth.py**
  - 48: 401 "invalid credentials"
  - 80: 400 "current password is incorrect"
  - 85: 400 "new password must differ from current password"
  - 145, 154: 400 "invalid or expired reset token"
- **users.py**
  - 37: 409 "email already in use"
  - 56: 409 "email already exists"
  - 78: 404 "user not found"
- **accounting.py**
  - 84, 97, 117: 404 "account not found"
  - 71, 104: 409 "account code already exists"
  - 167: 409 "fx rate for this currency pair and date already exists"
  - 189: 502 `str(exc)`
  - 214: 404 "no rate found"
  - 226: 404 "rate not found"
- **bills.py**
  - 60, 73, 97, 128: 404 "bill not found"
  - 99: 400 "cannot record payment on {status} bill"
  - 104: 400 "amount {x} exceeds balance due {y}"
  - 28, 77, 133: 400 `str(e)`
- **customers.py**
  - 100, 114, 130, 155, 212: 404 "customer not found"
  - 86, 116: 422 `str(exc)`
- **invoices.py**
  - 295, 333: 404 "recurring template not found"
  - 308, 338: 409 "recurring template has ended and is view-only"
  - 357: 400 "unknown action: …"
  - 490, 524, 605, 651, 708, 733: 404 "invoice not found"
  - 526: 400 "cannot record payment on {status} invoice"
  - 534: 400 "amount and withheld cannot both be zero"
  - 539: 400 "amount + withheld {settled} exceeds balance due {y}" (or "amount {settled} exceeds balance due {y}" when nothing is withheld)
  - 549: 400 "withheld {x} exceeds outstanding withholding {y}"
  - 654: 404 "payment not found on this invoice"
  - 659, 739: 400 "no recipient: provide to_email or set a contact_email on the customer"
  - 695: 502 "failed to send receipt email"
  - 735: 400 "cannot send a voided invoice"
  - 63, 98, 314, 380, 406, 462, 477, 504, 630, 790: 400 `str(e)`
- **items.py**: 55, 69, 83: 404 "item not found"
- **vendors.py**: 49, 63, 77: 404 "vendor not found"
- **payments.py**
  - 26: 400 "invalid JSON: …"
  - 31, 51: 400 `str(e)`
  - 38: 409 "duplicate (intake_source, external_ref)"
  - 58: 409 "duplicate email payment"
- **projects.py**
  - 29: 404 "project not found"
  - 110: 400 "cannot delete: project is referenced by N invoice(s)/bill(s)"
  - 42, 89: 400 `str(e)`
- **quotations.py**
  - 76, 174, 195: 404 "quotation not found"
  - 197: 400 "cannot send a {status} quotation"
  - 204: 400 "no recipient: …"
  - 40, 90, 105, 118, 133, 147, 161, 261: 400 `str(e)`
- **recon.py**
  - 101: 404 "payment not found"
  - 138: 400 "unknown adjustment_type '…'"
  - 146, 162: 400 `str(e)`

**OAuth errors** use `{"error": …, "error_description": …}` (`oauth.py:70`). Statuses: 400 by default, 401 for `invalid_client`, 403 for `access_denied` when the session is not an admin. Codes used: `unsupported_response_type`, `invalid_request`, `invalid_client`, `access_denied`, `invalid_grant`, `unsupported_grant_type`, `invalid_redirect_uri`.

**MCP tools** return `{"error": "..."}` dicts (47 occurrences). Unknown-field rejection is in `mcp/validate.py:6`.

**Distinct status codes:**
- Success: 200, 201, 204, 302, 307
- Errors: 400, 401, 403, 404, 409, 422, 502, plus unhandled 500 and MCP 421

---

## 3. Auth and sessions

**Session middleware.** Starlette `SessionMiddleware` (`main.py:64-76`):
- Cookie `billing_session` (`config.py:31`), signed with `session_secret`.
- The cookie value is base64 of the session JSON, signed with an itsdangerous `TimestampSigner` (Starlette's default).
- `max_age` is always 30 days (`session_remember_max_age_seconds`). Starlette also rejects a cookie older than that.
- `same_site="lax"`, `https_only=False`, `path=/`, HttpOnly.

Middleware order, outermost first: NoStore → CORS → Session. CORS uses `allow_origins=settings.cors_origins` with credentials allowed.

**Session keys:**
- `user_id`: UUID as a string
- `exp`: integer epoch seconds
- `oauth_authorize_csrf`: set during the OAuth authorize flow

**Login** (`auth.py:45`):
- Looks the user up by email and verifies with bcrypt. On failure: 401 "invalid credentials".
- Sets `exp = now + (30d if remember else 8h)`. The 8h comes from `session_max_age_seconds` (`config.py:33`).
- Returns UserOut. There is no role check at login.

**`current_admin`** (`deps.py:18`): 401 if `user_id` is missing. 401 "session expired" if `exp` is missing or past, and it clears the session. 403 if the user no longer exists or `role != "admin"`.

**Logout** clears the session and returns 204.

**Password hashing:** passlib `CryptContext(schemes=["bcrypt"], deprecated="auto")` at `auth.py:38` and `users.py:14`. Default rounds are 12 (passlib's default). Tests lower them to 4 (`tests/conftest.py:44-63`). bcrypt is pinned to `&gt;=4.0,&lt;4.1` (`pyproject.toml:17`).

**Forgot/reset password** (`auth.py:99`, `auth.py:137`):
- The token is opaque, not signed: `secrets.token_urlsafe(32)`, stored in plaintext in `users.password_reset_token` (String(64), unique and indexed).
- Expiry is stored in `password_reset_expires` (naive UTC) and set to 1 hour (`auth.py:41`).
- The email link is `{app_base_url}/reset-password?token=…`.
- The response is always the same generic message, with `reset_token` and `reset_link` both null. Email errors are swallowed.
- Reset clears the token. Change-password and admin reset also clear it.

**Invite / roles:**
- There is no invite email. An admin calls `POST /users` with a password (`users.py:50`).
- `role` is a free string defaulting to "admin" (`schemas/user.py`). Only `role == "admin"` is ever checked; there is no other permission system.

**MCP OAuth** (`mcp/oauth.py`, `mcp/tokens.py`):
- **Tables:**
  - `oauth_clients`: `client_id` (uuid4 hex string PK), `client_name`, `redirect_uris` (space-separated), `created_at` timestamptz.
  - `oauth_refresh_tokens`: `jti` (PK), `user_id` (string), `revoked` (bool), `created_at` timestamptz.
- **Tokens:** all are HS256 JWTs signed with `mcp_token_secret`.
  - Access token: `{sub, typ: "access", iat, exp}`, 3600 s, stateless.
  - Refresh token: `{sub, typ: "refresh", iat, exp, jti: uuid4 hex}`, 30 days, `jti` persisted in `oauth_refresh_tokens`.
  - Authorization code: also a JWT, `{typ: "code", sub, client_id, redirect_uri, code_challenge, iat, exp}`, 120 s. Codes are not stored, so they are not single-use beyond the TTL.
- **Flow rules:** PKCE must be S256. Refresh tokens rotate on use. Replaying a revoked refresh token revokes every live refresh token for that user (`oauth.py:239`).
- **Verifier:** on every MCP request it checks the JWT and that the user still exists and is an admin (`verifier.py:139`).
- **Confirm tokens** (`mcp/confirm.py`): `"{expiry}.{hex HMAC-SHA256}"` over canonical JSON `{action, payload, expiry}` (sorted keys, compact separators), keyed by `mcp_confirm_secret`, valid for 300 s. Nothing is stored in the database.

---

## 4. Serialization conventions

- **Decimals:** pydantic v2 serializes `Decimal` as a JSON **string**. It keeps the database scale: `numeric(19,4)` gives `"150.0000"`, FX rates `numeric(19,8)` give 8 places, and computed report values keep whatever scale the arithmetic produced. Schemas have no custom serializers (checked with grep). The MCP layer also uses `str(Decimal)` (`mcp/serialize.py:13`).
- **Computed field:** `InvoiceOut.expected_cash` (`schemas/invoice.py:173`) is serialized too.
- **Datetimes:**
  - Most columns are `DateTime(timezone=False)`, so values are naive ISO strings with no `Z`, e.g. `2026-01-01T10:00:00.123456`. Microseconds are dropped when they are zero.
  - `oauth_*.created_at` is timestamptz.
  - Dates are `YYYY-MM-DD`.
  - `as_of` query parameters default to `date.today()` in server-local time. The scheduler runs in UTC.
- **Enums:** plain strings. There are no Postgres ENUM types. Pydantic `Literal`s:
  - `discount_type`: PERCENT, AMOUNT
  - `wht_type`: NONE, PPH21, PPH23
  - `frequency`: DAILY, WEEKLY, MONTHLY, YEARLY
  - `end_mode`: NEVER, ON_DATE, AFTER_N
  - Bill status: DRAFT, OPEN, PARTIAL, PAID, VOID
  - `ItemType`: SERVICE, USAGE, FIXED_FEE
  - `HostingStatus`: ACTIVE, SUSPEND_PENDING, SUSPENDED, CANCELLED
  - `AccountType`: ASSET, LIABILITY, EQUITY, INCOME, EXPENSE, COGS
  - `ProjectStatus`: ACTIVE, ON_HOLD, COMPLETED, CANCELLED
  - `QuotationStatus`: `schemas/quotation.py:11`
- **Pagination:** none. List endpoints return bare JSON arrays. Some take a `limit` (default 200, maximum 500); there is no offset or envelope.
- **Field naming:** snake_case. The only alias is `EmailWebhookPayload.from_` ↔ `"from"` (`schemas/payment.py:83`).
- **CC addresses:** CC fields accept comma/semicolon/space-separated lists, normalized to `"a@x, b@y"` (`schemas/email.py`).
- **Document numbers:**
  - Invoices: `WH{YYYY}{NNNN}`, computed by scanning for the current maximum, with no sequence (`invoicing.py:157`). `invoice_number` has a unique constraint.
  - Quotations: `Q{YYYY}{NNNN}` (`quoting.py:73`).
  - Receipts: `RCT-{first 8 hex of payment_id, uppercase}` (`receipt_pdf.py:31`).

---

## 5. Background and scheduled work

The APScheduler `BackgroundScheduler` runs inside the API process (`workers/scheduler.py`), started from the lifespan when `scheduler_enabled` is true (`main.py:47`).
- Times are UTC. Job defaults: `coalesce=True`, `max_instances=1`, `misfire_grace_time=3600`.
- Each run is wrapped in `pg_try_advisory_lock(crc32(job_id))` and unlocked afterwards. Failures are logged with `log.exception` and go to Sentry.

| Job id | Schedule (UTC) | Function | What it does | Advisory lock key |
|---|---|---|---|---|
| recurring-scan-and-generate | daily 02:00 | `tasks/recurring.py:scan_and_generate` | For each RECURRING template due today and not paused, creates an idempotent DRAFT child invoice, then emails a digest of new drafts to **all** users | 3587987709 |
| usage-lock-cutoff-scan | daily 03:00 | `tasks/usage_lock.py:cutoff_scan` | Locks USAGE drafts whose cut-off day was yesterday. The accrued-amount hook is a stub that raises `NotImplementedError`, so any matching invoice makes the job fail | 2413487788 |
| hosting-overdue-enforcement | daily 04:00 | `tasks/hosting_enforcement.py:daily_hosting_enforcement` | Suspends or restores hosting items through Cloudflare DNS | 1601569364 |
| fx-rates-weekly-sync | Monday 05:00 | `tasks/fx_sync.py:sync_fx_rates` | Upserts Frankfurter rates into `fx_rates` | 1988665425 |

**Outbound integrations:**
- **Email** (`services/email.py`):
  - Uses the Resend HTTP API (`POST {resend_base_url}/emails`) when `resend_api_key` is set. Attachments are sent base64-encoded.
  - Otherwise uses SMTP with STARTTLS if `smtp_host` is set.
  - Otherwise only logs the message.
  - `Reply-To` is `smtp_reply_to` on both paths.
  - Senders: invoice send, quotation send, receipt send, password reset, the recurring-draft digest (`draft_notifications.py`), and the MCP `send_invoice_email` / `send_payment_reminder` tools.
- **Cloudflare** (`services/cloudflare.py`): `PATCH {base}/zones/{zone_id}/dns_records/{record_id}` with a Bearer `cloudflare_api_token`. It switches the record content between `maintenance_content` and `live_content`. Restoring also happens when an invoice becomes PAID (`ensure_hosting_restored`, called from record-payment at `invoices.py:575` and from reconciliation).
- **FX** (`services/fx_provider.py`): Frankfurter (ECB rates), `GET {fx_provider_base_url}/latest?base=SRC&amp;symbols=IDR`, source tag `FRANKFURTER`. Also exposed as `POST /accounting/fx-rates/sync`.
- **Logo fetch** (`pdf/logo.py`): if `logo_url` is an http(s) URL, it is fetched with httpx (5 s timeout).
- **Inbound webhooks:**
  - The generic HMAC webhook and the email intake both feed `reconciliation.process_incoming_payment`, which runs the matching engine and posts to the ledger.
  - Email "OCR" is a regex over subject, text and html (`intake/email_ocr.py`). If a body has attachments, the OCR hook raises `NotImplementedError`, which becomes a 400.
- **Outbound webhooks:** none.
- **Sentry:** `main.py:124`, with `send_default_pii=False`.

**Ledger posting** happens only in `reconciliation.py:98` (payment cleared) and `:233` (reversal). Manual `record-payment` and `finalize` do **not** create journal entries.

---

## 6. Files, logos and PDFs

- **Logo:** there is no upload endpoint. The frontend sends a base64 `data:` URI in `BusinessProfile.logo_url`, a `TEXT` column (`models/business_profile.py`), through `PUT /business-profile`.
  - The 1 MB limit exists only in the frontend (`frontend/src/views/AccountView.vue:193`).
  - Nothing goes to the filesystem or S3. There are no attachment tables.
- **PDF façade** (`services/pdf/__init__.py`):
  - WeasyPrint HTML→PDF by default (`pdf_engine="html"`), falling back to reportlab on any exception (`services/invoice_pdf.py`, `quotation_pdf.py`, `receipt_pdf.py`).
  - Three documents: **invoice, quotation, receipt**.
  - One Jinja2 template, `services/pdf/templates/document.html.j2` (233 lines, autoescaped), with contexts built in `services/pdf/context.py`.
  - The WeasyPrint URL fetcher allows only `data:` URIs (`pdf/engine.py`).
- **Fonts** (embedded as base64 by `pdf/assets.py`):
  - `services/pdf/assets/fonts/inter.woff2`
  - `services/pdf/assets/fonts/manrope.woff2`
  - `services/pdf/assets/fonts/jetbrains-mono.woff2`
- **Default logo:** `services/pdf/assets/default-logo.png`.

---

## 7. Business-id scoping

There is no `business_id` or tenant concept anywhere (checked with grep). It is a single-tenant install:
- `business_profile` is a singleton with `id=1`, enforced by `CHECK (id = 1)` and seeded in migration 0010.
- Code reads `db.get(BusinessProfile, 1)` directly (`invoices.py:664,710,744`, `quotations.py:176,201`, `mcp/tools/gated_email.py:86`). Payment accounts use `business_profile_id == 1` (`business_profile.py:22,56,87`).
- `GET /business-profile` creates the row if it is missing but does not commit (`business_profile.py:22`).
- All data is global, and any admin sees everything.

---

## 8. Database

**Tables (22, plus `alembic_version`):**
| Table | Notes |
|---|---|
| users | |
| customers | JSONB columns: matching aliases, cc lists |
| vendors | |
| items | |
| cloudflare_targets | |
| chart_of_accounts | serial PK |
| fx_rates | bigserial PK; unique `(from, to, as_of_date)` |
| invoices | `billing_cycle_ref` JSONB |
| invoice_line_items | |
| payments | unique `(intake_source, external_ref)` |
| reconciliation_log | `reasons` is `text[]` |
| journal_entries | |
| journal_lines | |
| business_profile | singleton |
| payment_account | serial PK |
| bills | |
| bill_line_items | |
| projects | `code` unique index |
| quotations | |
| quotation_line_items | ON DELETE CASCADE |
| oauth_clients | |
| oauth_refresh_tokens | |

The `hosting_subscriptions` table from migration 0017 is dropped again in 0018.

**Alembic:** 28 migrations. **Head: `0028_business_npwp`.** Revision ids are not all equal to the filenames: `0020_mcp_oauth.py` has revision `mcp_oauth`, and `0021_business_pay_instr.py` has revision `business_pay_instr`. The actual chain is 0019 → business_pay_instr → mcp_oauth → 0022 → … → 0028.

**Postgres-specific features:**
- No ENUM types, triggers or explicit sequences. Serial/bigserial PKs are used for `chart_of_accounts`, `fx_rates` and `payment_account`; every other PK is a UUID generated in the app (`uuid4`).
- **Numeric types:**
  - `numeric(19,4)` for all money.
  - `numeric(19,8)` for `fx_rates.rate` and `journal_lines.fx_rate`.
  - `numeric(7,4)` for withholding rates.
  - `numeric(5,2)` for `confidence_score`.
- **Check constraints:**
  - `ck_journal_lines_one_sided` (`0001:153`)
  - `ck_items_type` (`0003:65`)
  - `ck_business_profile_singleton` (`0010:36`)
- **Partial indexes:**
  - `ix_items_is_hosting WHERE is_hosting = true` (`0019:63`)
  - `uq_payment_account_one_default` unique `WHERE is_default` (`0022:44`)
- **Other types:** JSONB (customers, invoices) and `text[]` (`reconciliation_log`). JSONB path queries are used, e.g. `billing_cycle_ref['template_invoice_id'].astext` (`invoices.py:148`).
- **Advisory locks:** only the scheduler's `pg_try_advisory_lock` / `pg_advisory_unlock` with the crc32 keys above.
- **Seed data:** chart of accounts in 0002, FX gain/loss accounts in 0008, account 1300 deleted in 0027.

---

## 9. Tests

Layout of `backend/tests/` (there is no `api/` folder):
- `unit/`: 23 test files, plus `__init__.py`
- `integration/`: 38 test files (including 13 MCP and 2 OAuth/session tests), plus `__init__.py`
- Root: `conftest.py` and `__init__.py`

**Notable fixtures and setup in `conftest.py`:**
- **Environment at import:** sets `SCHEDULER_ENABLED=false` and `SENTRY_DSN=""` at import time (lines 31, 35).
- **Real Postgres only:** tests run against `TEST_DATABASE_URL` and skip when it is unset or unreachable. `alembic upgrade head` runs once per session (`migrated_engine`, line 89).
- **`db`** (line 123): one transaction per test, rolled back at the end, using `join_transaction_mode="create_savepoint"`. It also seeds FX rates USD→IDR 16000 and SGD→IDR 12000, dated 2026-01-01.
- **`client`** (line 150): a `TestClient` with `get_db` overridden to the test session.
- **`admin_session`** (line 166): logs in through the real `/api/v1/auth/login` endpoint.
- **`_cheap_bcrypt_for_app`** (line 49): lowers bcrypt rounds to 4.
- **MCP isolation:** `_tool_session_poison_guard` (line 460) and `mcp_tool_db` (line 478) bind the MCP tool session and verifier session to the test connection.
- **`seed_*` fixtures:** admin, business_profile, customer, two_acme, invoice, sent_invoice, draft_invoice, vendor, two_acme_vendors, bill, draft_bill, quotation, project, item, payment_pending_review.

---

## 10. Settings (`config.py`)

`pydantic-settings` reads `.env` with `extra="ignore"`. Environment variable names are the field names in upper case. List fields are JSON, e.g. `CORS_ORIGINS='["…"]'`.

| Field (line) | Default | Purpose |
|---|---|---|
| `database_url` (10) | `postgresql+psycopg://billing:billing@localhost:5432/billing` | DB connection; `postgres://` and `postgresql://` are rewritten to psycopg3 (line 14) |
| `scheduler_enabled` (28) | True | Run the in-process jobs |
| `session_secret` (30) | `dev-session-secret-change-me` | Cookie signing |
| `session_cookie_name` (31) | `billing_session` | Cookie name |
| `session_max_age_seconds` (33) | 28800 | Non-remember session lifetime (8h) |
| `session_remember_max_age_seconds` (39) | 2592000 | Remember-me lifetime and cookie max-age (30d) |
| `webhook_hmac_secret` (41) | `dev-webhook-secret-change-me` | Payment webhook HMAC |
| `email_webhook_token` (42) | `dev-email-token-change-me` | Email intake token |
| `cors_origins` (44) | `["http://localhost:5173"]` | CORS |
| `base_currency` (48) | IDR | Functional currency |
| `smtp_host` (52) | None | SMTP; logging only if unset |
| `smtp_port` (53) | 587 | |
| `smtp_username` (54) | None | |
| `smtp_password` (55) | None | |
| `smtp_use_tls` (56) | True | |
| `smtp_from_email` (57) | noreply@wenhao.id | |
| `smtp_from_name` (58) | wenhao-billing | |
| `smtp_reply_to` (59) | wenhao.kho@gmail.com | Reply-To for both SMTP and Resend |
| `app_base_url` (63) | http://localhost:5173 | Reset links, draft digest links, OAuth login redirect |
| `cloudflare_api_token` (65) | None | Cloudflare DNS |
| `cloudflare_api_base_url` (66) | https://api.cloudflare.com/client/v4 | |
| `cloudflare_timeout_seconds` (67) | 15.0 | |
| `mcp_enabled` (70) | True | Mount OAuth routes and `/mcp` |
| `mcp_confirm_secret` (72) | dev-… (minimum 16 chars) | Confirm-token HMAC |
| `mcp_token_secret` (74) | dev-… (minimum 16 chars) | JWT HS256 key |
| `mcp_access_token_ttl_seconds` (75) | 3600 | |
| `mcp_refresh_token_ttl_seconds` (76) | 2592000 | |
| `mcp_public_base_url` (78) | http://localhost:8000 | OAuth issuer and MCP host allowlist |
| `pdf_engine` (83) | html | `html` or `reportlab` |
| `fx_provider_base_url` (86) | https://api.frankfurter.dev/v1 | Frankfurter |
| `fx_provider_timeout_seconds` (87) | 15.0 | |
| `fx_sync_currencies` (89) | `["USD","SGD"]` | Currencies to sync |
| `resend_api_key` (96) | None | Takes priority over SMTP when set |
| `resend_from_email` (97) | noreply@wenhao.id | |
| `resend_from_name` (98) | Wenhao Dev Billing | |
| `resend_base_url` (99) | https://api.resend.com | |
| `resend_timeout_seconds` (100) | 15.0 | |
| `sentry_dsn` (105) | None | Sentry |
| `sentry_environment` (106) | development | |
| `sentry_traces_sample_rate` (108) | 0.0 | |

**Main libraries** (`backend/pyproject.toml`): fastapi ≥0.115, SQLAlchemy 2.0, pydantic 2 with email, APScheduler 3, passlib with bcrypt 4.0, itsdangerous, httpx, reportlab, weasyprint 68–69, fastmcp 3.4.3, pyjwt 2, sentry-sdk 2.</result>
<usage><subagent_tokens>236789</subagent_tokens><tool_uses>38</tool_uses><duration_ms>323853</duration_ms></usage>
</task-notification>