# Plan 5 contract inventory: invoices (frozen Python -> Go)

Read-only inventory. All paths are relative to `backend-python/` unless prefixed. Abbreviations:

| Abbr | File |
|---|---|
| R | app/api/v1/routers/invoices.py |
| C | app/api/v1/routers/customers.py |
| S | app/services/invoicing.py |
| RS | app/services/recurring_schedule.py |
| SCH | app/schemas/invoice.py |
| M / LI | app/models/invoice.py / app/models/invoice_line_item.py |
| W | app/services/withholding.py (Go: backend/internal/withholding) |
| H | app/services/hosting.py |
| DN | app/services/draft_notifications.py |
| WR / WU | app/workers/tasks/recurring.py / app/workers/tasks/usage_lock.py |
| ST | app/services/stats.py |
| DDL | backend/migrations/00001_baseline.sql |
| FE | frontend/src/views/ |

Global facts:
- Every route is admin-only through `Depends(current_admin)` (app/api/deps.py:18), mounted under `/api/v1` (app/main.py:89, :91). Unauthenticated -> 401 `"not authenticated"` (deps.py:21).
- Validation errors are FastAPI's default 422 (no custom handler in app/main.py). No schema in SCH has a custom validator. All constraints come from `Field(...)`/`Literal`, so 422 messages are Pydantic defaults, e.g. `"String should have at least 3 characters"`, `"Input should be greater than 0"`, `"Input should be greater than or equal to 0"`, `"Decimal input should have no more than 4 decimal places"`, `"Decimal input should have no more than 19 digits in total"`, `"Input should be 'PERCENT' or 'AMOUNT'"`, `"Input should be 'NONE', 'PPH21' or 'PPH23'"`, `"Input should be 'DAILY', 'WEEKLY', 'MONTHLY' or 'YEARLY'"`, `"Input should be 'NEVER', 'ON_DATE' or 'AFTER_N'"`, `"Input should be less than or equal to 500"`.
- Every `invoicing.InvoicingError` becomes **400** with `detail=str(e)`. That includes "not found" (only the GET routes return 404). Withholding `normalize` errors therefore surface as **400** on invoices (S:76-82). The Go `withholding.Normalize` comment says they surface as 422. That is true for customers, not for invoices.
- Decimals are serialized as JSON strings. Values that come from the DB carry the column scale (`numeric(19,4)` -> `"100.0000"`, `wht_rate numeric(7,4)` -> `"2.5000"`). Every mutating route commits before returning. `expire_on_commit` reloads the row, so responses show DB scale even without `db.refresh`.
- Request decimals: the frontend sends `quantity`, `unit_price` and `discount_value` as JSON **numbers** (`Number(...)`, FE/InvoiceFormView.vue:657-658, 675-676). `wht_rate` may be a string. The Go decoder must accept a number or a string for every decimal.
- No ledger use anywhere in scope. S imports only Customer/Invoice/InvoiceLineItem, schemas, recurring_schedule and withholding (S:16-33). R imports no ledger module. Confirmed: invoice create, finalize, void and delete post nothing.
- No `business_id` column on invoices (DDL:228-260), so there is no business scoping.

---

## 0. Storage (DDL:212-260, constraints/indexes)

`invoices` (DDL:228-260): invoice_id uuid NOT NULL (PK `invoices_pkey`, DDL:603); customer_id uuid NULL (FK customers, DDL:970); invoice_type varchar(20) NOT NULL; currency varchar(3) NOT NULL; amount numeric(19,4) NOT NULL; balance_due numeric(19,4) NOT NULL; status varchar(20) NOT NULL; billing_cycle_ref jsonb NULL; issue_date date; due_date date; created_at timestamp (no tz) DEFAULT CURRENT_TIMESTAMP, **nullable**; invoice_number varchar(50) (UNIQUE `uq_invoices_invoice_number`, DDL:715-716); po_so_number varchar(100); subtotal numeric(19,4); discount_type varchar(10); discount_value numeric(19,4); notes text; footer text; payment_terms varchar(30); is_template boolean DEFAULT false NOT NULL; project_id uuid (FK projects, DDL:977); source_quote_id uuid (FK quotations, DDL:984); coverage_start date; coverage_end date; internal_note text; wht_type varchar(10) DEFAULT 'NONE' NOT NULL; wht_rate numeric(7,4); wht_gross_up boolean DEFAULT false NOT NULL; wht_base numeric(19,4); wht_amount numeric(19,4) DEFAULT 0 NOT NULL; wht_booked numeric(19,4) DEFAULT 0 NOT NULL.
- There are no CHECK constraints. invoice_id has no DB default: Python generates `uuid4` (M:19-21).
- Indexes: ix_invoices_customer_id (DDL:793), ix_invoices_project_id (:799), ix_invoices_source_quote_id (:805), ix_invoices_status (:811).
- FKs **into** invoices with no ON DELETE: `payments.invoice_id` (DDL:1034) and `quotations.converted_invoice_id` (DDL:1062).

`invoice_line_items` (DDL:212-222): line_item_id uuid NOT NULL (PK, DDL:597, Python uuid4 LI:19-21); invoice_id uuid NOT NULL FK ON DELETE CASCADE (DDL:957); item_id uuid NULL FK items (DDL:964); "position" integer NOT NULL; description text NOT NULL; quantity/unit_price/amount numeric(19,4) NOT NULL; created_at timestamp DEFAULT CURRENT_TIMESTAMP NOT NULL. Index ix_invoice_line_items_invoice_id (DDL:787).
- ORM relationship `line_items` is ordered by `InvoiceLineItem.position` only (M:75-80), with cascade delete-orphan. Ties have no defined order.

### InvoiceOut (SCH:137-176): response shape shared by most routes, fields in this order
1. invoice_id UUID
2. customer_id UUID|null
3. project_id UUID|null
4. source_quote_id UUID|null (default None)
5. invoice_type str
6. invoice_number str|null
7. po_so_number str|null
8. currency str
9. subtotal Decimal|null (scale 4)
10. discount_type str|null
11. discount_value Decimal|null (scale 4)
12. wht_type str
13. wht_rate Decimal|null (scale 4, numeric(7,4))
14. wht_gross_up bool
15. wht_base Decimal|null (4)
16. wht_amount Decimal (4)
17. wht_booked Decimal (4)
18. amount Decimal (4)
19. balance_due Decimal (4)
20. status str
21. billing_cycle_ref object|null (JSONB passthrough, see section 3)
22. issue_date date|null
23. due_date date|null
24. coverage_start date|null
25. coverage_end date|null
26. payment_terms str|null
27. notes str|null
28. footer str|null
29. internal_note str|null
30. is_template bool
31. created_at datetime: naive ISO with no offset, e.g. `"2026-11-09T12:34:56.123456"`
32. line_items list[InvoiceLineItemOut] (default []). Each item (SCH:45-54), in order: line_item_id UUID, item_id UUID|null, description str, quantity Decimal(4), unit_price Decimal(4), amount Decimal(4), position int. There is no created_at.
33. expected_cash Decimal: a `@computed_field`, serialized **last** (SCH:173-176). Value = `max(Decimal(0), balance_due - (wht_amount - wht_booked))` (W:53-56). When the value is exactly zero, Python's `max` returns the first argument `Decimal(0)`, so the JSON is `"0"`, not `"0.0000"`. Go `withholding.ExpectedCash` returns `v` (scale 4) instead, so the outputs differ.

---

## 1. Routes

Route registration order matters in R: the static paths (`/milestone`, `/recurring-template`, `/recurring-templates`, `/recurring-templates/rows`, `/awaiting-finalization`) are registered before `/{invoice_id}`.

### (a) Core CRUD / list / read

#### POST /api/v1/invoices -> 201 InvoiceOut (R:54-66)
Request `InvoiceCreate` (SCH:57-75):
| field | type | default | constraints |
|---|---|---|---|
| customer_id | UUID\|null | None | not checked to exist (FK -> 500) |
| project_id | UUID\|null | None | not checked (FK -> 500) |
| invoice_type | str | "MILESTONE" | free string; `or "MILESTONE"` maps "" to MILESTONE (S:254); >20 chars -> DB 500 |
| currency | str | required | min_length=3, max_length=3. No case or known-currency check |
| invoice_number | str\|null | None | falsy -> generated (S:245); duplicate -> unhandled IntegrityError 500 |
| po_so_number | str\|null | None | no max_length (column 100) |
| issue_date | date\|null | None | None -> `date.today()` (S:246) |
| due_date | date\|null | None | None -> issue + terms days (S:247-249) |
| payment_terms | str\|null | None | no max_length (column 30) |
| notes, footer, internal_note | str\|null | None | |
| discount_type | "PERCENT"\|"AMOUNT"\|null | None | Literal |
| discount_value | Decimal\|null | None | ge=0, max_digits=19, decimal_places=4. No upper bound for PERCENT |
| wht_type | "NONE"\|"PPH21"\|"PPH23"\|null | None | Literal; null -> customer default |
| wht_rate | Decimal\|null | None | max_digits=7, decimal_places=4. No ge, so a negative rate gets a 400 from normalize |
| wht_gross_up | bool\|null | None | |
| line_items | list[InvoiceLineItemIn] | [] | an empty list -> 400 |

`InvoiceLineItemIn` (SCH:37-42): item_id UUID|null=None; description str (required); quantity Decimal ge=0 max_digits=19 decimal_places=4 (required); unit_price same (required); position int=0.

Errors (400, from S:228-280):
- `"invoice must have at least one line item"` (S:229-230)
- `"Unknown withholding tax type: {t}"` (W:63). This is only reachable through a customer default, because the payload type is a Literal.
- `"Withholding tax rate must be greater than 0 and less than 100"` (W:69). Covered by test_create_invoice_rate_100_returns_400.

Frontend: FE/InvoiceFormView.vue:728 `buildPayload()` (:651-680). It sends customer_id, project_id, `invoice_type:"MILESTONE"` (create only, :666), currency, invoice_number (`||null`), po_so_number, issue_date, due_date (`YYYY-MM-DD` or null), payment_terms, notes, internal_note, discount_type, discount_value, wht_type, wht_rate (null when NONE), wht_gross_up (false when NONE), and line_items `{item_id, description, quantity:Number, unit_price:Number, position: idx}`. It does **not** send `footer`.

#### POST /api/v1/invoices/milestone -> 201 InvoiceOut (R:69-86)
Request `MilestoneInvoiceCreate` (SCH:12-19): customer_id UUID (required); project_id UUID|null=None; currency str min3 max3; amount Decimal gt=0 max_digits=19 decimal_places=4; milestone_ref str (required); issue_date date|null=None; due_in_days int=14 (unbounded, negatives allowed).
- There is no try/except. A missing customer raises an FK IntegrityError -> 500.
- No `db.refresh`. Fields still reload after the commit.
Frontend: **not called**.

#### GET /api/v1/invoices -> 200 list[InvoiceOut] (R:426-450)
Query: `status` list[str]|None (repeatable, `status=SENT&status=PARTIAL`, R:428-431); `customer_id` UUID|None; `invoice_type` str|None; `limit` int default 200, `le=500`, **no lower bound**. A negative limit is a Postgres error -> 500.
SQL: `WHERE is_template = false [AND status IN (...)] [AND customer_id = ?] [AND invoice_type = ?] ORDER BY created_at DESC LIMIT :limit`.
Frontend: FE/InvoicesView.vue:72-83. Tabs send `status=OPEN&status=SENT&status=PARTIAL` (Unpaid), `status=DRAFT`, or no filter. It reads invoice_id, customer_id, invoice_type, invoice_number, currency, amount, balance_due, wht_amount, wht_booked, status, issue_date, due_date and created_at (:25-39).

#### GET /api/v1/invoices/{invoice_id} -> 200 InvoiceOut (R:482-491)
- 404 `"invoice not found"` when the row is missing.
- Templates are returned too (there is no is_template filter).
Frontend: FE/InvoiceFormView.vue:261. It reads the full InvoiceOut (interface :106-145), including expected_cash and line_items sorted by position.

#### PATCH /api/v1/invoices/{invoice_id} -> 200 InvoiceOut (R:494-507)
Request `InvoiceUpdate` (SCH:78-96): every field is optional with default None. The fields and constraints match InvoiceCreate, except invoice_type is `str|None` and `line_items` is `list|None`. Semantics use `model_dump(exclude_unset=True)`: only keys that are present apply, and an explicit `null` is applied.
Errors (400):
- `"invoice {invoice_id} not found"` (S:285-286)
- `"only DRAFT invoices can be edited (got {status})"` (S:287-288)
- the withholding normalize messages listed under POST
- An explicit null for a NOT NULL column (currency, invoice_type) gives a 500 at flush.
Frontend: FE/InvoiceFormView.vue:726 sends the same `buildPayload()` **without invoice_type** on edit (:664-666). Covered by test_patch_without_invoice_type_keeps_it.

#### GET /api/v1/invoices/awaiting-finalization -> 200 list[InvoiceOut] (R:411-423)
SQL: `WHERE status='DRAFT' AND is_template=false ORDER BY created_at DESC`, with no limit.
Frontend: FE/AwaitingFinalizationView.vue:52. It reads invoice_id, customer_id, invoice_type, invoice_number, currency, amount, balance_due, status, issue_date, due_date and created_at (:17-29).

#### GET /api/v1/customers/{customer_id}/billing-summary -> 200 BillingSummary (C:147-201)
Response `BillingSummary` (C:29-37), in order:
- milestone list[InvoiceOut]
- recurring list[InvoiceOut]
- usage list[InvoiceOut]
- unpaid_invoices list[InvoiceOut]
- total_unpaid list[CurrencyAmount]
- paid_last_12_months list[CurrencyAmount]
- last_invoice_issue_date date|null
- last_invoice_id UUID|null

`CurrencyAmount` (C:24-26) = {currency str, amount Decimal}.

Behaviour:
- 404 `"customer not found"` (C:154-155).
- `open_invoices`: `WHERE customer_id=? AND status IN ('DRAFT','OPEN','SENT','PARTIAL') ORDER BY due_date ASC`. There is **no is_template filter** (C:157-164).
- milestone/recurring/usage are filtered in Python by invoice_type (C:183-191). unpaid_invoices is all of open_invoices.
- `total_unpaid` = the sum of balance_due per currency, sorted by currency code (C:137-144, :193-195).
- `latest_invoice`: `WHERE customer_id=? ORDER BY issue_date DESC, created_at DESC LIMIT 1` (C:165-170). Postgres puts NULLs first on DESC.
- `paid_last_12_months` = the sum of `Payment.amount` (cash only, no wht) for payments with status `CLEARED` and `payment_date >= date.today() - 365 days` (C:172-180, :196-198).
Frontend: FE/CustomerDetailView.vue:99. It reads total_unpaid, paid_last_12_months, last_invoice_issue_date and the lists (InvoiceRow: invoice_id, invoice_type, currency, amount, balance_due, status, issue_date, due_date; :63-87).

#### GET /api/v1/customers/{customer_id}/invoices -> 200 list[InvoiceOut] (C:204-219)
- 404 `"customer not found"`.
- `WHERE customer_id=? ORDER BY issue_date DESC, created_at DESC`, with no limit and **no is_template filter**.
Frontend: FE/CustomerDetailView.vue:105.

#### GET /api/v1/customers/balances -> 200 list[CustomerBalanceRow] (C:52-74)
Registered before `/{customer_id}`. Row fields, in order: customer_id UUID, currency str, balance Decimal, overdue Decimal. Data comes from `compute_customer_balances(db, today=date.today())` (ST:401-434):
```sql
SELECT customer_id, currency, SUM(balance_due) AS balance,
       SUM(CASE WHEN due_date < :today THEN balance_due ELSE 0 END) AS overdue
WHERE status IN ('OPEN','SENT','PARTIAL')   -- _OPEN_STATUSES ST:66
  AND is_template = false AND balance_due > 0 AND customer_id IS NOT NULL
GROUP BY customer_id, currency            -- no ORDER BY
```
`bal or 0`, `od or 0` (ST:430-431). A null due_date is never overdue.
Frontend: FE/CustomersView.vue:59 reads customer_id, currency, balance and overdue.

### (b) Lifecycle

#### POST /api/v1/invoices/{invoice_id}/finalize -> 200 InvoiceOut (R:453-464; S:643-663)
No body. Errors (400), checked in this order:
- `"invoice {id} not found"`
- `"only DRAFT invoices can be finalized (got {status})"`
- `"cannot finalize a recurring template"` (reachable, because templates are DRAFT)

Effect: `balance_due <= 0` -> balance_due=0 and status **PAID**. Otherwise status **OPEN**. It does not assign a number, change dates, or check customer or lines.
Frontend: FE/AwaitingFinalizationView.vue:56.

#### POST /api/v1/invoices/{invoice_id}/void -> 200 InvoiceOut (R:467-479; S:691-700)
Body `VoidRequest` (SCH:33-34): `reason: str` is **required but never stored or used**.
Errors (400):
- `"invoice {id} not found"`
- `"cannot void a PAID invoice — reverse payments first"` (em dash U+2014)

Effect: status=VOID, balance_due=0, for **any** non-PAID status (DRAFT, OPEN, SENT, PARTIAL, VOID, and templates). wht_booked and payments are untouched.
Frontend: FE/AwaitingFinalizationView.vue:62 sends `{reason:"voided from finalization queue"}`.

#### DELETE /api/v1/invoices/{invoice_id} -> 204, no body (R:781-791; S:467-474)
Errors (400):
- `"invoice {id} not found"`
- `"only DRAFT invoices can be deleted (got {status})"`

Line items cascade (ORM and DB). Templates (DRAFT) can be deleted, which leaves the children's `template_invoice_id` dangling. If a payment or a quotation's `converted_invoice_id` references the invoice, the delete is an unhandled FK error -> 500.
Frontend: **not called** (MCP gated_delete uses the service).

#### POST /api/v1/invoices/{invoice_id}/mark-paid -> 200 InvoiceOut (R:616-633; S:666-688)
No body. Errors (400):
- `"invoice {id} not found"`
- `"only OPEN, SENT or PARTIAL invoices can be settled this way (got {status})"`
- `"invoice has an outstanding balance of {balance_due} — record a payment instead"` (balance printed with DB scale, e.g. `100.0000`; em dash)

Effect: balance_due=0, status=PAID.
Frontend: FE/InvoiceFormView.vue:445.

### (c) Recurring templates and trigger

`RecurringTemplateCreate` (SCH:108-123) is used by both POST and PUT:
| field | type | default | constraints |
|---|---|---|---|
| customer_id | UUID\|null | None | |
| project_id | UUID\|null | None | |
| currency | str | required | min/max 3 |
| po_so_number | str\|null | None | |
| payment_terms | **str** | **required** | may be "" |
| notes, footer, internal_note | str\|null | None | |
| discount_type / discount_value | as InvoiceCreate | | |
| wht_type / wht_rate / wht_gross_up | as InvoiceCreate | | |
| line_items | list[InvoiceLineItemIn] | [] | empty -> 400 |
| schedule | RecurringSchedule | **required** | |

`RecurringSchedule` (SCH:99-105): frequency Literal DAILY|WEEKLY|MONTHLY|YEARLY (required); interval int ge=1 default 1; start_date date (required); end_mode Literal NEVER|ON_DATE|AFTER_N default NEVER; end_date date|null=None; end_after_cycles int|null=None ge=1.
There is no `invoice_type`, `invoice_number`, `issue_date` or `due_date` on templates.

#### POST /api/v1/invoices/recurring-template -> 201 InvoiceOut (R:89-101; S:364-412)
Errors (400), checked in this order:
1. `"recurring template must have at least one line item"` (S:365-366)
2. `"invalid schedule: end_mode=ON_DATE requires end_date"` or `"invalid schedule: end_mode=AFTER_N requires positive integer end_after_cycles"` (S:357-360 wrapping RS:84, :89-91). The other ScheduleErrors cannot happen here because Pydantic already enforces the Literals.
3. withholding normalize messages

Frontend: FE/InvoiceFormView.vue:723 `buildTemplatePayload()` (:682-714). It sends customer_id, project_id, currency, po_so_number, payment_terms (raw), notes, footer, internal_note, discount_*, wht_*, line_items (position=idx), and `schedule:{frequency, interval (??1), start_date (the "issue date" picker), end_mode, end_date (null unless ON_DATE), end_after_cycles (null unless AFTER_N)}`.

#### GET /api/v1/invoices/recurring-templates -> 200 list[InvoiceOut] (R:104-115)
`WHERE is_template = true ORDER BY created_at DESC`. Frontend: **not called**.

#### GET /api/v1/invoices/recurring-templates/rows -> 200 list[RecurringTemplateRow] (R:215-284)
Row (R:118-132), fields in order:
- template_id UUID
- customer_id UUID|null
- customer_name str|null
- currency str
- amount Decimal (4)
- payment_terms str|null
- schedule_description str
- frequency str|null
- start_date date|null
- end_mode str|null
- next_run_date date|null
- previous_issue_date date|null
- generated_count int
- status str

Algorithm:
- Templates: `WHERE is_template=true ORDER BY created_at DESC` (R:222-228).
- For each template, one customer lookup and one children query (N+1).
- `_generated_children` (R:135-153): `WHERE is_template=false AND invoice_type='RECURRING' AND status <> 'VOID' AND billing_cycle_ref->>'template_invoice_id' = :tid ORDER BY issue_date DESC NULLS LAST`.
- `generated_count = len(children)`.
- `previous_issue_date = _previous_cycle_date(children)` (R:156-172): `date.fromisoformat(children[0].billing_cycle_ref["cycle_key"])`. On a ValueError or a missing key it falls back to `children[0].issue_date`. With no children it is None.
- `next_run_date = next_cycle_after(schedule, previous or start_date - 1 day)` (R:175-182).
- Status (R:249-254): `next_run is None` -> `"ENDED"`; else `is_paused(cfg)` -> `"PAUSED"`; else `"ACTIVE"`.
- If parsing raises ScheduleError (R:258-264): `schedule_description="Schedule not configured"`, `status="MISCONFIGURED"`, and next_run, frequency, start_date and end_mode are all null.
- `schedule_description = describe(schedule)` (RS:242-262).
Frontend: FE/RecurringInvoicesView.vue:91 (tabs filter on the client: ACTIVE | PAUSED+MISCONFIGURED | ENDED | all) and FE/InvoiceFormView.vue:361 (reads `template_id`, `status==="ENDED"` -> read-only form).

#### GET /api/v1/invoices/recurring-templates/{template_id} -> 200 InvoiceOut (R:287-296)
404 `"recurring template not found"` when the row is missing or `is_template` is false.
Frontend: FE/InvoiceFormView.vue:301. It reads `billing_cycle_ref.{frequency, interval, start_date, end_mode, end_date, end_after_cycles}` and the template fields (:266-295, :308-335).

#### PUT /api/v1/invoices/recurring-templates/{template_id} -> 200 InvoiceOut (R:299-317; S:415-464)
Body `RecurringTemplateCreate`.
- First: if the template exists, is a template, and `_template_has_ended` (R:185-192: parses the schedule, where a ScheduleError counts as not ended; `next_run is None`) -> **409** `"recurring template has ended and is view-only"`.
- Then the service errors (400):
  - `"recurring template {id} not found"` (missing or not a template; note this is 400 here, while GET/PATCH give 404)
  - `"recurring template must have at least one line item"`
  - `"invalid schedule: ..."`
  - normalize messages
Frontend: FE/InvoiceFormView.vue:721.

#### PATCH /api/v1/invoices/recurring-templates/{template_id} -> 200 InvoiceOut (R:320-362)
Body `RecurringTemplateAction` (R:320-321): `action: str`, upper-cased (R:336).
- 404 `"recurring template not found"` (R:332-333).
- PAUSE or RESUME on an ended template -> 409 `"recurring template has ended and is view-only"` (R:337-340).
- PAUSE: `cfg["paused"]=True`.
- RESUME: `cfg.pop("paused")`.
- END_NOW: if the template has not ended, set `end_mode="ON_DATE"`, `end_date=_end_now_date().isoformat()` and pop `end_after_cycles`. Always pop `paused` (R:345-355). END_NOW is allowed on an ended template and is idempotent there.
- Anything else -> 400 `f"unknown action: {payload.action}"` (original casing, R:356-357).
- `_end_now_date` (R:195-212) = `min(date.today() - 1 day, next_run - 1 day)`. If the schedule is invalid it returns yesterday.
Frontend: FE/RecurringInvoicesView.vue:61 sends `{action:"PAUSE"|"RESUME"|"END_NOW"}`.

#### POST /api/v1/invoices/recurring/{template_id}/trigger -> 200 InvoiceOut (R:365-388; S:477-601)
Body `RecurringTrigger` (SCH:22-25): `cycle_key: str` (required).
Flow:
1. `existing = find_cycle_invoice(...)`
2. `trigger_recurring_cycle(...)`
3. If `existing is None`, build `draft_summary` (DN:25-35)
4. `db.commit()`
5. `notify_users_of_new_drafts` (DN:55-58): emails every User best-effort and logs failures (DN:42-52)

The status is 200 for both a new invoice and an idempotent replay.
Errors (400, in order, S:508-533):
- `"template invoice {id} not found"`
- `"invoice {id} is not RECURRING"`
- `"template {id} has an invalid schedule: {ScheduleError}"`. A generated child (RECURRING, not a template) lands here with `invalid frequency: None`, because the child's cfg has no frequency.
- `"cycle_key must be an ISO date (YYYY-MM-DD), got {cycle_key!r}"` (Python repr, normally single-quoted)
- `"cycle_key {cycle_key!r} is not a valid cycle start for this schedule"`
Frontend: **not called** (MCP `trigger_recurring` uses the service).

### (d) Usage lock

#### POST /api/v1/invoices/usage/{invoice_id}/lock -> 200 InvoiceOut (R:391-408; S:604-640)
Body `UsageLockRequest` (SCH:28-30): accrued_amount Decimal ge=0 max_digits=19 decimal_places=4; cutoff_date date. Both required.
Errors (400):
- `"invoice {id} not found"`
- `"invoice {id} is not USAGE"`
- `"usage invoice already locked (status={status})"` (fires for any non-DRAFT status)
Frontend: **not called**.

Job `cutoff_scan` (WU:27-52) runs on cron 03:00 UTC (app/workers/scheduler.py:33):
- `yesterday = date.today() - 1`.
- Selects `invoice_type='USAGE' AND status='DRAFT'` with no order.
- Skips rows where `cfg.locked` is set, `cut_off_day` is missing, or `int(cut_off_day) != yesterday.day`.
- Calls `compute_accrued(invoice_id)`. The default is `_stub_accrued`, which **raises NotImplementedError** (WU:23-24).
- Then calls `lock_usage_invoice(..., cutoff_date=yesterday)`. There is one commit at the end, and the job returns the count.

### (e) Out of scope (later plans): list only
- POST `/invoices/{id}/record-payment` -> 201 PaymentOut (R:510-594): manual cash+wht payment. Sets PARTIAL/PAID, restores hosting, writes ReconciliationLog. FE InvoiceFormView:424, InvoicesView:179.
- GET `/invoices/{id}/payments` -> list[PaymentOut] (R:597-613): CLEARED payments, newest first. FE InvoiceFormView:517.
- POST `/invoices/{id}/payments/{payment_id}/send-receipt` -> `{"sent_to":...}` (R:636-696): emails a receipt PDF. FE InvoiceFormView:396/525, InvoicesView:150.
- GET `/invoices/{id}/pdf` (R:699-717): inline invoice PDF. FE InvoiceFormView:467 (window.open).
- POST `/invoices/{id}/send` -> InvoiceOut (R:720-778): emails the invoice PDF and promotes DRAFT/OPEN -> SENT. FE InvoiceFormView:478.

---

## 2. Service rules

### Totals: `_compute_totals` (S:47-73)
```python
Q4 = Decimal("0.0001")                       # S:40
def _round4(v): return v.quantize(Q4, rounding=ROUND_HALF_UP)   # S:43-44
subtotal = Decimal("0")
for ln in lines: subtotal += Decimal(ln.quantity) * Decimal(ln.unit_price)
subtotal = _round4(subtotal)                 # rounded once, on the raw sum
if discount_type == "PERCENT" and discount_value is not None:
    pct = Decimal(discount_value) / Decimal("100")
    amount = _round4(subtotal * (Decimal("1") - pct))
elif discount_type == "AMOUNT" and discount_value is not None:
    amount = _round4(subtotal - Decimal(discount_value))
else:
    amount = subtotal
if amount < 0: amount = Decimal("0")
wht = compute_withholding(amount, currency, wht_type, wht_rate, wht_gross_up)
return subtotal, wht.total, wht              # invoice.amount = wht.total
```
- Line amount (S:174-189): `amount=_round4(qty * price)`, and `position = ln.position if ln.position is not None else idx`. Because position defaults to 0 and is never None, the idx fallback never fires.
- Withholding (W:27-50, ported to Go as `withholding.Compute`):
  - NONE, a null rate, or base==0 -> total=base, wht=0.
  - Gross-up: `total = round_ccy(base / (1 - rate/100))` and `wht = total - base`.
  - Otherwise: `wht = round_ccy(base*rate/100)` and total=base.
  - `round_ccy`: IDR quantizes to `1`, every other currency to `0.01`, ROUND_HALF_UP.
  - `wht_base = base` (the after-discount amount).
- Only the WHT path rounds to currency precision. A non-WHT total keeps 4 dp (for example a USD 10.3333 total is possible).
- Persisted values: `subtotal`, `amount = balance_due = wht.total`, `wht_base`, `wht_amount`. `wht_booked` stays at its default 0.

### Withholding resolution
- `_resolve_wht` (S:85-103), used by create_invoice, create_recurring_template and create_milestone_invoice (which passes the payload as None):
  - If the payload `wht_type` is not None (even "NONE"), use `normalize(payload_type, payload_rate, bool(payload_gross_up))`.
  - Otherwise, if the customer exists, use `normalize(customer.default_wht_type, default_wht_rate, default_wht_gross_up)`.
  - Otherwise `("NONE", None, False)`.
- `_merge_wht` (S:106-119), used by update_invoice and update_recurring_template:
  - If none of `wht_type`, `wht_rate`, `wht_gross_up` appears in the set keys, keep the stored values **without re-normalizing**.
  - Otherwise: `wht_type = data.get("wht_type") or current.wht_type`; `rate = data.get("wht_rate")`; if `rate is None and wht_type == current.wht_type` use the current rate; `gross_up = data.get or current`; then normalize.
  - As a result, switching type with a null rate picks up the new type's default rate.
- `normalize` (W:59-70):
  - Unknown type -> `"Unknown withholding tax type: {t}"`.
  - NONE -> `("NONE", None, False)`.
  - A null rate takes the default: PPH21 2.5, PPH23 2.
  - Requires `0 < rate < 100`, else `"Withholding tax rate must be greater than 0 and less than 100"`.

### create_invoice (S:228-280)
Steps, in order:
1. Check that line items are not empty.
2. Resolve wht.
3. Compute totals.
4. `invoice_number = payload.invoice_number or generate_invoice_number(db)`.
5. `issue_date = payload.issue_date or date.today()`.
6. `due = payload.due_date or issue + _parse_payment_terms_days(payload.payment_terms)`.
7. Insert the row: `invoice_type = payload.invoice_type or "MILESTONE"`, status DRAFT, is_template False, balance_due = amount, and every payload text field copied as given.

Nothing is copied from the customer except the wht defaults (and only when wht_type is null). Nothing is copied from the business profile (that happens only in PDF and quotation code). Nothing is copied from items: item_id is just stored, and description and price come from the payload.

### `_parse_payment_terms_days` (S:145-154)
- None or "" -> 14.
- `strip().lower()` in `("on receipt","on-receipt","due on receipt")` -> 0.
- `re.match(r"^\s*net\s+(\d+)\s*$", t, re.I)` -> N.
- Anything else -> 14.
- The quoting service imports this function, together with `_resolve_wht` and `generate_invoice_number` (app/services/quoting.py:32-34, :287-301), so the Go helpers need to be exported for the later quotations plan.

### update_invoice (S:283-341)
Steps, in order:
1. not found / DRAFT-only checks.
2. `data = model_dump(exclude_unset=True)`, then pop `line_items`.
3. setattr for each present key among customer_id, project_id, invoice_type, currency, invoice_number, po_so_number, issue_date, due_date, payment_terms, notes, footer, internal_note, discount_type, discount_value (S:293-310).
4. If line_items is not None: remove all existing lines, flush, append the rebuilt lines.
5. Merge wht.
6. Recompute totals from the **current** lines (`_lines_as_inputs`, S:132-142) and the invoice's discount and currency.
7. Apply wht, subtotal, amount, and `balance_due = amount`.
8. If `due_date is None and issue_date is not None`, set `due_date = issue_date + terms days` (S:335-338). A changed issue_date does not move an existing due_date.

No is_template guard: templates (DRAFT) are editable through PATCH /invoices/{id}.

### create_milestone_invoice (S:192-225)
- `today = issue_date or date.today()`.
- wht comes from the customer default (`_resolve_wht(db, customer_id, None, None, None)`).
- `compute_withholding(amount, ...)` with no discount.
- Fields: `invoice_type="MILESTONE"`, **no invoice_number**, no subtotal, no line items, `amount=balance_due=wht.total`, status DRAFT, `billing_cycle_ref={"milestone_ref": milestone_ref}`, `due_date = today + due_in_days`, payment_terms null.

### Document numbering: `generate_invoice_number` (S:157-171)
```python
year = date.today().year            # server-local today, NOT issue_date
prefix = f"WH{year}"
rows = db.scalars(select(Invoice.invoice_number).where(Invoice.invoice_number.like(f"{prefix}%"))).all()
max_n = 0
for n in rows:
    if n is None: continue
    tail = n[len(prefix):]
    if tail.isdigit(): max_n = max(max_n, int(tail))
return f"{prefix}{max_n + 1:04d}"   # WH20260001; grows past 4 digits naturally
```
- Assigned **at creation**: create_invoice when the payload has no number (S:245), trigger_recurring_cycle always (S:556), and quotation conversion (quoting.py:301).
- **Never** assigned at finalize, nor for milestone-route invoices or templates (both stay null).
- Templates are excluded only because they have a null number. There is no lock and no retry.

### Status machine
Statuses: `DRAFT`, `OPEN`, `SENT`, `PARTIAL`, `PAID`, `VOID`. There is no OVERDUE (overdue is derived from due_date).

| From | Action | To | Where / refusal |
|---|---|---|---|
| (new) | create / milestone / template / trigger / quote convert | DRAFT | S:268, :218, :399, :569 |
| DRAFT | finalize, balance_due > 0 | OPEN | S:661 |
| DRAFT | finalize, balance_due <= 0 | PAID (balance 0) | S:654-656 |
| non-DRAFT | finalize | refused | `"only DRAFT invoices can be finalized (got {status})"` |
| DRAFT template | finalize | refused | `"cannot finalize a recurring template"` |
| DRAFT | PATCH edit, usage lock | DRAFT | non-DRAFT: `"only DRAFT invoices can be edited (got {status})"` / `"usage invoice already locked (status={status})"` |
| DRAFT | delete | (row gone) | non-DRAFT: `"only DRAFT invoices can be deleted (got {status})"` |
| any except PAID | void | VOID (balance 0) | PAID: `"cannot void a PAID invoice — reverse payments first"` |
| OPEN/SENT/PARTIAL with balance <= 0 | mark-paid | PAID | `"only OPEN, SENT or PARTIAL invoices can be settled this way (got {status})"`; `"invoice has an outstanding balance of {balance_due} — record a payment instead"` |
| DRAFT/OPEN | send email (out of scope) | SENT | R:774-775; VOID refused: `"cannot send a voided invoice"` (R:734-735) |
| OPEN/SENT/PARTIAL | record-payment / reconciliation (out of scope) | PARTIAL/PAID | R:570-577; reconciliation.py:160, :200-203; a reversal goes back to OPEN at reconciliation.py:248 |

### Recurring schedule (RS, pure; port verbatim)
- `parse_schedule(cfg)` (RS:65-101). Errors, in order:
  - empty/None -> `"schedule config is empty"`
  - frequency not in the 4 -> `f"invalid frequency: {frequency!r}"`
  - `interval = cfg.get("interval", 1)` that is not an int or is < 1 -> `f"interval must be a positive int, got {interval!r}"`. Note that Python `bool` is an int subclass.
  - start_date -> `_parse_date`: a non-ISO string gives `f"invalid ISO date for {field}: {value!r}"`; a missing value or a non-string gives `f"missing or invalid {field}"` (RS:54-62).
  - end_mode (default NEVER) not valid -> `f"invalid end_mode: {end_mode!r}"`
  - ON_DATE without end_date -> `"end_mode=ON_DATE requires end_date"`
  - AFTER_N with a missing, non-int or < 1 count -> `"end_mode=AFTER_N requires positive integer end_after_cycles"`
  - end_date and end_after_cycles are only read for their own mode. No check that end_date >= start_date.
- `_add_months(d, m)` (RS:109-114): adds months and clamps the day to the target month's length.
- `_cycle_start_for_index(s, i)` (RS:117-130), with `step = interval*i`:
  - DAILY: start + step days
  - WEEKLY: start + 7*step days
  - MONTHLY: `_add_months(start, step)`. This is always computed **from start_date**, so 31 Jan -> 28 Feb -> 31 Mar.
  - YEARLY: `_add_months(start, 12*step)`
- `_index_for_day(s, day)` (RS:133-156):
  - DAILY: `days // interval`.
  - WEEKLY: `days // (7*interval)`.
  - MONTHLY/YEARLY: `est = month_delta // (interval*unit)`, then walk down while `est>0 and start(est) > day`, then walk up while `start(est+1) <= day`.
- `_index_past_end` (RS:159-166): ON_DATE -> `cycle_start > end_date` (so end_date itself is inclusive). AFTER_N -> `index >= end_after_cycles`. NEVER -> False.
- `current_cycle(s, today)` (RS:169-177): None if `today < start`. Otherwise take the index and start for today, and return None if past the end.
- `is_cycle_start(s, day)` (RS:180-191): `day >= start and start(index_for_day(day)) == day`. It **ignores the end condition**, by design.
- `next_cycle_after(s, after)` (RS:194-204): the next index is 0 if `after < start`, otherwise `index_for_day(after)+1`. Returns None if that cycle is past the end.
- `cycle_key(c) = c.start.isoformat()` (RS:207-208).
- `is_paused(cfg) = bool(cfg.get("paused", False))`, and False for empty cfg (RS:211-215).
- `describe` (RS:242-262):
  - `"Daily"` / `"Every {i} days"`
  - `"Weekly on {Weekday}"` / `"Every {i} weeks on {Weekday}"` (weekday of start_date, Monday-first names)
  - `"Monthly on the {ordinal}"` / `"Every {i} months on the {ordinal}"` (ordinal: 11-13 take "th" because the rule is `10 <= n%100 <= 20`; otherwise 1st/2nd/3rd, else th)
  - `"Yearly on {Month} {day}"` / `"Every {i} years on {Month} {day}"`
  - fallback `"Custom"`
- `_schedule_to_json` (S:344-361): builds `{"frequency","interval","start_date" (iso),"end_mode"}`, adds `end_date` (iso) and `end_after_cycles` only when they are not None (even if end_mode doesn't use them), then validates with parse_schedule.

### create/update recurring template (S:364-464)
- Create:
  - wht via `_resolve_wht`; totals as for invoices.
  - `invoice_type="RECURRING"`, invoice_number None, status DRAFT, issue_date and due_date None, is_template True.
  - `billing_cycle_ref = schedule dict`, `amount = balance_due = total`.
- Update:
  - Preserves `paused: True` if the old cfg had it (S:427-428).
  - wht via `_merge_wht(template, payload.model_dump(exclude_unset=True))`.
  - Overwrites customer_id, project_id, currency, po_so_number, payment_terms, notes, footer, internal_note, discount_type, discount_value, subtotal, amount, balance_due and billing_cycle_ref. Other cfg keys, such as END_NOW's end config, are replaced by the payload.
  - Deletes the old lines, flushes, rebuilds them, flushes again.

### trigger_recurring_cycle (S:501-601), idempotency
1. Load the template and check it exists and has type RECURRING. It does **not** check is_template; a child fails later on the schedule.
2. `parse_schedule(template.billing_cycle_ref)`.
3. `cycle_start = date.fromisoformat(cycle_key)`.
4. Check `is_cycle_start`.
5. `find_cycle_invoice` (S:477-498): `invoice_type='RECURRING' AND is_template=false AND billing_cycle_ref->>'template_invoice_id' = :tid AND billing_cycle_ref->>'cycle_key' = :raw_key`, via `db.scalar` (first row, never raises). If a row exists, **return it unchanged**.
6. `today = date.today()`. Recompute totals from the template's lines, discount and the stored wht (not re-normalized).
7. Create the child:
   - customer, project, po_so, currency, discount, wht_*, payment_terms, notes, footer and internal_note are copied from the template.
   - `invoice_number = generate_invoice_number`.
   - status DRAFT; `billing_cycle_ref = {"template_invoice_id": str(tid), "cycle_key": cycle_key}`.
   - `issue_date = today` (**not** the cycle date); `due_date = today + terms days`; is_template False.
   - Lines are copied verbatim, including position and the stored amount.
8. Flush, then apply hosting fields.

Hosting fields (H:61-83), applied only if any line's item has `is_hosting`:
- `bundle_months = cfg.get("interval") or 1` (the interval, whatever the frequency).
- `coverage_start = cycle_start`.
- `coverage_end = _add_months(cycle_start, bundle_months) - 1 day` (H:42-44).
- Appends `"\nCoverage: {start} to {end}"` to **every** line description that doesn't already contain it.

### Scanner `scan_and_generate` (WR:47-72), cron 02:00 UTC (app/workers/scheduler.py:32)
- `today = date.today()`.
- Templates: `invoice_type='RECURRING' AND is_template=true`, no order, no status filter.
- `_cycle_key_for` (WR:29-44): paused -> skip; ScheduleError -> skip; `current_cycle(today)` None -> skip; otherwise the iso key.
- For each due template: `find_cycle_invoice` then `trigger_recurring_cycle`, and collect a draft_summary if it is new. `generated += 1` even for idempotent re-returns.
- One commit after the loop, then notify.
- Only the cycle containing today is generated. Cycles missed entirely are never backfilled.
- The job runs under a Postgres advisory lock `crc32(job_id)` (scheduler.py:40-52). Exceptions are logged and swallowed, so one failing template rolls back the whole run.

### lock_usage_invoice (S:604-640)
1. Run the checks (see 1d).
2. `wht = compute_withholding(accrued_amount, invoice.currency, invoice.wht_type, invoice.wht_rate, invoice.wht_gross_up)`.
3. Set wht_base, wht_amount, `amount = balance_due = wht.total`.
4. `cfg = dict(cfg or {})`, then set `cfg["cutoff_date"] = cutoff.isoformat()` and `cfg["locked"] = True`.

Not touched: subtotal, discount, line items, status (stays DRAFT). It does not refuse an already `locked` cfg.

### Date sources (server-local `date.today()`; the container TZ decides)
- S:158 (number year), S:203 (milestone issue default), S:246 (create issue default), S:541 (trigger issue/due)
- R:203 (`_end_now_date`)
- C:65 (balances overdue cutoff), C:172 (12-month window)
- WR:48, WU:28

There is no `datetime.utcnow` in scope; hosting uses `datetime.now(UTC)` (H:30) for enforcement only. The cron triggers are UTC (scheduler.py:30-36).

---

## 3. JSONB / special columns

`invoices.billing_cycle_ref` (jsonb, nullable). The response key is `billing_cycle_ref` (passthrough dict). On template create/update the request key is `schedule`. Shapes by writer:

| Writer | Shape |
|---|---|
| template create/update (S:344-361, S:427-428) | `{"frequency": "MONTHLY", "interval": 1, "start_date": "2026-05-01", "end_mode": "NEVER"\|"ON_DATE"\|"AFTER_N", ["end_date": "YYYY-MM-DD"], ["end_after_cycles": int], ["paused": true]}` |
| PATCH PAUSE/RESUME/END_NOW (R:335-359) | the same dict, with `paused` added or removed; END_NOW sets `end_mode:"ON_DATE"` and `end_date`, and removes `end_after_cycles` |
| generated child (S:570-573) | `{"template_invoice_id": "<uuid str>", "cycle_key": "<raw cycle_key as sent>"}` |
| milestone route (S:219) | `{"milestone_ref": "<str>"}` |
| usage lock (S:635-638) | existing keys + `"cutoff_date": "YYYY-MM-DD"`, `"locked": true` |
| usage scanner reads (WU:37-41) | `cut_off_day` (int or numeric string through `int()`), `locked`. **No API writes `cut_off_day`**. |
| POST /invoices, PATCH, quote conversion | null (not settable through the API) |

- JSONB normalizes key order (shorter keys first, then bytewise), so response key order follows Postgres, not insertion.
- Readers:
  - `RS.parse_schedule` and `is_paused`
  - `R._generated_children` / `find_cycle_invoice` (`->>` text compare)
  - `R._previous_cycle_date` (`cycle_key`)
  - `H:74` (`interval`)
  - `ST:96-99`, `ST:337` (stats; out of scope)
- There are no other JSON or array columns on invoices or line items.

---

## 4. Tests

Legend: **S** = service-level (DB, no HTTP); **H** = HTTP; **U** = pure unit. `[OUT]` = needs payments/email/PDF/quotes/hosting enforcement/scripts, so it belongs to another plan.

### tests/unit/test_recurring_schedule.py (33, U, all in scope -> RS port)
- `test_parse_minimal_monthly_schedule`: defaults interval 1, NEVER, null end fields
- `test_parse_end_on_date`: ON_DATE parses end_date
- `test_parse_end_after_n`: AFTER_N parses count 12
- `test_parse_rejects_bad_frequency`: ScheduleError
- `test_parse_rejects_non_positive_interval`: ScheduleError
- `test_parse_rejects_missing_start_date`: ScheduleError
- `test_parse_rejects_end_on_date_without_end_date`: ScheduleError
- `test_parse_rejects_end_after_n_without_count`: ScheduleError
- `test_parse_rejects_none_config`: ScheduleError
- `test_returns_none_before_start_date`: current_cycle None before start
- `test_start_date_itself_is_cycle_zero`: start day is index 0
- `test_daily_every_day_advances_index`: daily index 2 on day 3
- `test_daily_interval_three_buckets_by_three_days`: 3-day buckets
- `test_weekly_interval_one_advances_by_seven_days`: weekly boundaries
- `test_weekly_interval_two_advances_every_fortnight`: fortnight boundaries
- `test_monthly_interval_one_basic`: monthly starts, index 10 after 10 months
- `test_monthly_interval_three_quarterly`: quarterly from the 15th
- `test_monthly_end_of_month_start_clamps_short_months`: 31st clamps to 28 Feb and returns to 31 Mar
- `test_yearly_interval_one`: yearly boundary
- `test_end_on_date_inclusive_on_the_day`: end_date inclusive
- `test_end_after_n_caps_by_index`: AFTER_N=3 stops at index 3
- `test_cycle_key_is_iso_date_of_cycle_start`: "2026-07-01"
- `test_describe_monthly_basic`: "Monthly on the 1st"
- `test_describe_monthly_interval`: "Every 3 months on the 15th"
- `test_describe_weekly`: "Weekly on Monday"
- `test_describe_weekly_interval`: "Every 2 weeks on Monday"
- `test_describe_daily`: "Daily"
- `test_describe_yearly`: "Yearly on May 1"
- `test_next_cycle_after_before_start_returns_start`: next = start
- `test_next_cycle_after_on_current_returns_next`: next month
- `test_next_cycle_after_returns_none_past_end_date`: None
- `test_next_cycle_after_returns_none_past_count_cap`: None
- `test_paused_flag_roundtrips`: is_paused for True/False/{}/None

### tests/unit/test_recurring_scanner.py (8, U, `_cycle_key_for`)
- `test_none_config_returns_none`, `test_malformed_config_returns_none`, `test_before_start_returns_none`
- `test_monthly_returns_iso_cycle_start`: "2026-05-01"/"2026-06-01"
- `test_weekly_interval_two`
- `test_past_end_date_returns_none`
- `test_paused_template_returns_none_even_when_cycle_is_due`
- `test_past_after_n_returns_none`

### tests/unit/test_recurring_drafts_email.py (3, U) [OUT: email]
- `test_subject_counts_drafts`: digest subject `"1 draft invoice awaiting your approval"` / `"2 draft invoices ..."`
- `test_text_body_lists_draft_details`: digest body contents
- `test_html_body_links_invoice_number`: html links and "Awaiting Finalization"

### tests/integration/test_recurring_scheduling.py (27)
- `test_create_template_stores_schedule_in_billing_cycle_ref` (S): is_template, RECURRING, cfg keys/values
- `test_create_template_persists_end_condition` (S): ON_DATE + end_date stored
- `test_create_template_rejects_inconsistent_end_condition` (S): InvoicingError
- `test_cycle_key_none_before_start` (S)
- `test_cycle_key_is_iso_cycle_start_monthly` (S)
- `test_cycle_key_weekly_interval_two` (S)
- `test_cycle_key_none_past_end_date` (S)
- `test_cycle_key_none_past_after_n` (S)
- `test_cycle_key_returns_none_if_config_missing` (S)
- `test_rows_previous_is_cycle_date_not_generation_date` (H rows): previous = cycle_key, not issue date
- `test_rows_next_is_previous_cycle_plus_one_interval` (H rows): next = previous + 1 month
- `test_trigger_rejects_year_month_cycle_key` (S): match "ISO date"
- `test_trigger_rejects_off_cycle_date` (S): match "not a valid cycle start"
- `test_trigger_accepts_valid_cycle_start` (S): child cycle_key stored
- `test_manual_trigger_notifies_users_once` (H trigger): first call sends the digest (cycle_date, /edit link); the second call sends nothing. Email is monkeypatched, so the Go version needs a notifier seam.
- `test_find_cycle_invoice_none_before_trigger_then_found` (S)
- `test_fix_recurring_cycle_keys_plan_and_apply` (S) [OUT: scripts/fix_recurring_cycle_keys]
- `test_rows_excludes_void_children` (H rows): VOID child is neither counted nor used as previous
- `test_fix_script_ignores_void_and_fixes_live_key` (S) [OUT: script]
- `test_end_now_ends_template_with_overdue_cycle` (H PATCH+rows): ENDED, end_date = next-1 (2026-06-30)
- `test_end_now_ends_template_that_never_generated` (H): end_date = start-1 (2025-12-31)
- `test_end_now_with_future_next_cycle_ends_yesterday` (H): end_date = today-1
- `test_end_now_on_paused_template_shows_ended_not_paused` (H): PAUSE then END_NOW -> ENDED, paused removed
- `test_rows_ended_schedule_outranks_paused_flag` (H rows)
- `test_ended_template_rejects_edit` (H PUT): 409, unchanged
- `test_ended_template_rejects_pause_and_resume` (H PATCH): 409 for both, end_date unchanged, still ENDED
- `test_active_template_still_accepts_edit` (H PUT): 200, payment_terms "Net 30"

Several of these depend on real `date.today()` (overdue / yesterday). They need a clock seam or relative dates.

### tests/integration/test_invoice_withholding.py (12)
- `test_create_invoice_gross_up_pph23` (H POST): IDR 10,000,000 at PPH23 2% gross-up -> amount 10204082, wht 204082, base 10000000, rate 2, expected_cash 10000000
- `test_create_invoice_inherits_customer_default` (H): PPH21, 2.5, gross_up true
- `test_explicit_none_overrides_customer_default` (H): NONE, wht 0, amount 10000000
- `test_discount_then_withholding` (H): 10% discount -> 900000, wht 18000
- `test_update_draft_recomputes_withholding` (H PATCH): -> PPH23 gross-up totals
- `test_update_type_switch_resets_rate` (H PATCH): rate becomes 2.5
- `test_recurring_cycle_uses_template_snapshot` (S): child copies the wht snapshot and amount
- `test_quote_conversion_applies_customer_default` (S) [OUT: quotations]
- `test_usage_lock_recomputes_from_snapshot` (S): lock -> 10204082 / 204082 / 10000000
- `test_milestone_invoice_inherits_customer_default` (S): PPH21 2.5, wht 25000, amount 1000000
- `test_update_template_gross_up_only_keeps_type_and_rate` (S): rate 3 kept, amount 10309278, wht 309278
- `test_create_invoice_rate_100_returns_400` (H POST): 400

### tests/integration/test_invoice_internal_note.py (4)
- `test_internal_note_round_trips_through_api` (H POST/PATCH/GET)
- `test_internal_note_can_be_cleared` (S update with explicit None)
- `test_internal_note_never_reaches_the_pdf` (S) [OUT: PDF]
- `test_template_internal_note_copies_to_generated_child` (S)

### tests/integration/test_invoice_type_preserved.py (1, parametrized x2)
- `test_patch_without_invoice_type_keeps_it[RECURRING|USAGE]` (H POST then PATCH notes): type kept

### tests/integration/test_zero_value_invoice.py (8)
- `test_zero_value_draft_has_zero_totals` (S): amount 0, balance 0, DRAFT
- `test_finalize_zero_value_invoice_becomes_paid` (S)
- `test_finalize_nonzero_invoice_becomes_open` (S): OPEN, balance 100
- `test_settle_zero_value_invoice_service` (S): PAID
- `test_settle_rejects_nonzero_balance` (S)
- `test_settle_rejects_draft` (S)
- `test_mark_paid_endpoint_settles_zero_value` (H): 200 PAID
- `test_mark_paid_endpoint_rejects_nonzero_balance` (H): 400

### tests/integration/test_customer_balances.py (10, S on `compute_customer_balances`)
- `test_empty_db_returns_empty_list`
- `test_customer_with_no_open_invoices_excluded`
- `test_sent_invoice_not_yet_due_contributes_to_balance_only`
- `test_sent_invoice_past_due_contributes_to_both_balance_and_overdue`
- `test_partial_uses_balance_due_not_amount`
- `test_sums_across_multiple_open_invoices_same_currency`
- `test_draft_void_paid_template_all_excluded`
- `test_zero_balance_rows_excluded`
- `test_multiple_currencies_produce_separate_rows`
- `test_multiple_customers_produce_separate_rows`

### tests/integration/test_hosting.py (6)
- `test_trigger_recurring_cycle_stamps_coverage_on_hosting_invoice` (S): monthly interval 3, cycle 2026-06-01 -> coverage 2026-06-01..2026-08-31, label in line 0. **In scope** (it needs an `items` row with is_hosting and a Cloudflare target; items are already ported).
- [OUT: hosting enforcement/reconciliation; they use trigger only as setup]:
  - `test_hosting_item_creates_with_cloudflare_target`
  - `test_hosting_enforcement_suspends_and_reconciliation_restore_reactivates`
  - `test_enforcement_suspend_failure_marks_provider_error`
  - `test_enforcement_is_idempotent_for_already_suspended_hosting_item`
  - `test_enforcement_ignores_non_overdue_invoice`

### Other consumers (all [OUT])
- MCP: test_mcp_write_invoices.py (13), test_mcp_read_invoices.py (9), test_mcp_gated_delete.py (4 invoice tests: lines 32, 40, 49, 58). Their messages pin the same service strings, e.g. `{"error": "only DRAFT invoices can be finalized (got SENT)"}` at :101.
- Payments/email: test_record_payment_withholding.py (13), test_invoice_payments_list.py (4), test_send_receipt.py (6), test_customer_cc.py (4), test_reconciliation_withholding.py (5).
- Stats: test_invoices_summary.py (16), test_dashboard_stats.py (12), test_mrr_stats.py (9), test_upcoming_invoices.py (9), test_revenue_monthly_base.py (9).
- test_api_no_store.py (2): generic `Cache-Control: no-store`, motivated by the finalize queue.

### Coverage gaps (no Python test at all)
- HTTP: GET /invoices (list + filters), awaiting-finalization, finalize, void, delete, milestone, usage lock, GET/POST recurring template, GET recurring-templates, billing-summary, customer invoices, GET /customers/balances.
- Jobs: `cutoff_scan`, and `scan_and_generate` as a whole.

### In-scope count
| File | In scope |
|---|---|
| test_recurring_schedule | 33 |
| test_recurring_scanner | 8 |
| test_recurring_scheduling | 25 |
| test_invoice_withholding | 11 |
| test_invoice_internal_note | 3 |
| test_invoice_type_preserved | 1 (x2) |
| test_zero_value_invoice | 8 |
| test_customer_balances | 10 |
| test_hosting | 1 |
| **Total** | **100** |

---

## 5. Oddities (decide: replicate or fix, then note it in the plan)

1. **Templates leak into customer views.**
   - billing-summary and `/customers/{id}/invoices` have no `is_template` filter (C:157-164, C:213-218). Templates are DRAFT with `balance_due = amount`, so they show up in `recurring`/`unpaid_invoices` and inflate `total_unpaid`.
   - `ORDER BY issue_date DESC` puts NULL issue_dates (templates) **first** in Postgres. `last_invoice_id` / `last_invoice_issue_date` therefore point to a template (or null) whenever the customer has one (C:165-170).
   - The frontend InvoiceRow doesn't read `is_template` (FE/CustomerDetailView.vue:63-72).
2. **cycle_key is not canonicalized.** Python 3.11+ `date.fromisoformat` accepts `"20260301"` and `"2026-W09-7"` (verified: both parse to 2026-03-01). S:525 validates the date, but S:497/S:572 store and compare the **raw string**. Two spellings therefore produce two invoices for the same cycle. There is no unique index on (template_invoice_id, cycle_key), so concurrent trigger plus scanner can also duplicate, and `find_cycle_invoice` uses `db.scalar`, which silently takes the first row (S:489).
3. **Numbering race and gaps.**
   - `generate_invoice_number` (S:157-171) is an unlocked scan-and-max. Concurrent creates hit `uq_invoices_invoice_number` -> unhandled IntegrityError -> 500.
   - A user-supplied duplicate number also gives a 500 (S:245, S:309-310).
   - The year comes from `date.today()`, not issue_date.
   - Milestone-route invoices never get a number, and finalize doesn't assign one (S:192-225, :643-663).
   - `str.isdigit()` accepts Unicode digits (`'²'.isdigit()` is True), which would make `int()` raise -> 500.
4. **Not found is 400, not 404** for PATCH /invoices/{id}, finalize, void, delete, mark-paid, usage lock, trigger, and PUT template (InvoicingError -> 400). GET /invoices/{id}, GET/PATCH template and the customer routes do return 404.
5. **Unhandled 500s.**
   - Milestone with an unknown customer (no try, R:75-86). Unknown customer_id, project_id or item_id on any create (no existence check).
   - An explicit null for currency or invoice_type on PATCH (S:309-310).
   - Strings longer than their column (invoice_type 20, payment_terms 30, invoice_number 50, po_so_number 100; no `max_length` in SCH).
   - `limit<0` on the list (R:434).
   - Deleting a DRAFT referenced by `payments` or by a quotation's `converted_invoice_id`: both FKs have no ON DELETE (DDL:1034, :1062; S:473). A DRAFT made by quotation conversion therefore cannot be deleted.
6. **Void is permissive.**
   - Allowed from DRAFT, PARTIAL (payments and wht_booked untouched, balance zeroed), VOID (re-void), and templates (S:691-700).
   - `reason` is required but discarded (SCH:33-34, R:467-479).
   - Voiding a template does not stop generation: the scanner and the rows endpoint don't filter template status (WR:52-56, R:222-228).
7. **PATCH /invoices/{id} edits templates.** There is no is_template guard (S:283-288). Only the DRAFT status protects anything.
8. **Rounding inconsistencies.**
   - `subtotal = round4(Σ raw qty·price)` but each line amount is `round4(qty·price)`, so subtotal can differ from the sum of line amounts (S:57-60 vs S:186).
   - Non-WHT totals are not rounded to currency precision; only the WHT path is (W:27-30).
   - A PERCENT discount has no upper bound and clamps to 0 (SCH:71, S:62-71).
   - `discount_value` is stored even when `discount_type` is null.
9. **Line positions.** `position` defaults to 0, so the `else idx` fallback is dead code (S:182). Clients that omit position get all lines at 0, and `ORDER BY position` ties are nondeterministic (M:78). The frontend always sends idx.
10. **Generated child dates.** `issue_date = today`, not the cycle start (S:541, :574). A late or backfilled cycle is therefore dated today.
11. **Manual trigger ignores paused/ended.** By design (RS:185-186), a manual trigger can still generate after END_NOW or while paused.
12. **Hosting coverage uses `interval` as months whatever the frequency** (H:74). A WEEKLY interval-1 template gets one month of coverage. The label is appended to **every** line, not only hosting lines (H:81-83).
13. **Rows endpoint.**
    - N+1 queries (R:230-232).
    - "Previous" is the child with the latest **issue_date**, not the latest cycle_key (R:151, :165). Backfilling an old cycle today makes it "previous" and moves Next backwards.
    - next_run_date can be in the past and is not flagged.
14. **END_NOW** can write `end_date < start_date` (R:203-212). Neither parse_schedule nor template create checks `end_date >= start_date` (RS:82-85).
15. **PUT template on a missing id** returns 400 `"recurring template {id} not found"`, while GET/PATCH return 404 `"recurring template not found"` (R:306-314 vs R:293-295, R:331-333).
16. **Usage lock is effectively dead.**
    - No API writes `cut_off_day`, so `cutoff_scan` can never match an API-created invoice.
    - If one ever matches, `_stub_accrued` raises NotImplementedError and the whole job rolls back (WU:23-24, :43).
    - The lock route never checks `cfg.locked` (re-lock is allowed while DRAFT) and never checks is_template. Its refusal text says "already locked" for any non-DRAFT status, and it ignores discount, lines and subtotal (S:619-638).
17. **Scanner fragility.** One exception aborts the whole run with no commit (WR:57-70). `generated` counts idempotent replays (WR:69). Days come from server-local `date.today()` while cron runs in UTC (scheduler.py:30-33).
18. **expected_cash serialization.** It is `"0"` (not `"0.0000"`) when exactly zero (W:56, Python `max` returns the first arg). Go `ExpectedCash` keeps scale.
19. **Error status mismatch.** Withholding normalize errors are **400** on invoice routes (S:76-82), not 422 as the Go withholding doc comment implies.
20. **created_at nullable.** It is nullable in DDL (DDL:239) but required in InvoiceOut, so a null would give a ResponseValidationError (500).
21. **billing-summary totals.** `total_unpaid` includes DRAFT balances (C:161). The 12-month window is a flat 365 days and sums cash `amount` only (excludes withheld, C:172-198). The `/customers/balances` result has no ORDER BY (ST:411-425).
22. **update_invoice due date.** Changing issue_date does not move an existing due_date. due_date is only filled when it is null (S:335-338).
23. **Currency is never validated** (any 3 chars, any case), and PATCH can change it on a DRAFT. No check against the customer's default currency.
24. **awaiting-finalization has no limit** (R:416-423). `due_in_days` is unbounded and accepts negatives (SCH:19).

---

## 6. Size estimate and suggested split

Routes in scope:

| Group | Routes |
|---|---|
| (a) core | 9: POST /invoices, POST /milestone, GET /invoices, GET /{id}, PATCH /{id}, GET /awaiting-finalization, GET /customers/{id}/billing-summary, GET /customers/{id}/invoices, GET /customers/balances |
| (b) lifecycle | 4: finalize, void, delete, mark-paid |
| (c) recurring | 7: POST template, GET templates, GET rows, GET/PUT/PATCH template, POST trigger |
| (d) usage | 1: lock (+ cutoff job) |
| **Total** | **21 routes + 2 jobs** |

(e) has 5 routes, deferred. Python tests to port: **100** (41 pure unit, the rest DB/HTTP). Untested routes need new Go tests (see the gaps in section 4).

Suggested tasks, each one cohesive and reviewable with its tests:
1. **Schedule math (pure)**: port RS as `internal/invoices/schedule` (parse, cycle math, describe, is_paused) plus the 33 + 8 unit tests (`_cycle_key_for` as a pure function). No DB.
2. **Invoice store + totals**: sqlc queries for invoices/line items, `computeTotals`, `buildLines`, `paymentTermsDays`, `nextInvoiceNumber`, and wht resolve/merge (reusing `internal/withholding`). Export the helpers quoting will need. Add pure tests for rounding and terms, plus DB tests for numbering.
3. **Core routes (a, invoice side)**: POST, GET list (status repeat, limit), GET one, PATCH (exclude-unset semantics, which need presence-aware JSON decoding), awaiting-finalization, milestone. Tests: withholding H tests (6), internal note (3), type preserved (x2), plus new list/awaiting/milestone tests.
4. **Lifecycle (b)**: finalize, void, delete, mark-paid with the exact refusal strings. Tests: zero-value (8) plus new void/delete/finalize-template tests.
5. **Recurring templates (c minus trigger)**: POST/PUT/GET/GET list/rows/PATCH actions, the 409 ended rule, END_NOW date (clock seam). Tests: ~16 from test_recurring_scheduling (create, rows, end_now, 409) and test_update_template_gross_up_only_keeps_type_and_rate.
6. **Trigger + scanner job**: trigger (validation order, idempotency, copy rules, hosting coverage stamp), the draft-notification seam (no real email), `scan_and_generate` under the advisory lock. Tests: trigger tests (5), find_cycle (1), notify-once (1), template snapshot (1), internal-note copy (1), hosting coverage (1), plus a new scanner test.
7. **Usage lock (d) + cutoff job**: route and job with an injectable accrual source. Tests: test_usage_lock_recomputes_from_snapshot plus new route/job tests.
8. **Customer invoice views**: balances (10 service tests + 1 new HTTP test), billing-summary, customer invoices. The decision on oddity #1 (template leak, NULLS FIRST) must be made here.

Tasks 1 and 2 have no dependencies. 3-8 depend on 2. 5 and 6 depend on 1. 8 only needs the store from 2.
