# Plan 6 contract inventory: quotations, projects, bills (frozen Python -> Go)

Read-only inventory. All paths are relative to `backend-python/` unless prefixed. Abbreviations:

| Abbr | File |
|---|---|
| RQ | app/api/v1/routers/quotations.py |
| RP | app/api/v1/routers/projects.py |
| RB | app/api/v1/routers/bills.py |
| SQ | app/services/quoting.py |
| SP | app/services/projects.py |
| SB | app/services/billing_ap.py |
| CQ / CP / CB | app/schemas/quotation.py / project.py / bill.py |
| PAY | app/schemas/payment.py |
| MQ / MQL | app/models/quotation.py / quotation_line_item.py |
| MP | app/models/project.py |
| MB / MBL | app/models/bill.py / bill_line_item.py |
| INV | app/services/invoicing.py |
| W | app/services/withholding.py (Go: backend/internal/withholding) |
| GI | backend/internal/invoices (Go, already ported) |
| PN | backend/PORT-NOTES.md |
| DDL | backend/migrations/00001_baseline.sql |
| FE | frontend/src/views/ |

Global facts:
- Every route is admin-only through `Depends(current_admin)` and mounted under `/api/v1` (app/main.py:90 bills, :100 projects, :101 quotations). Unauthenticated -> 401 `"not authenticated"`.
- Validation errors are FastAPI's default 422. No schema in CQ/CP/CB has a custom validator; constraints are `Field(...)`/`Literal` only. New Literal messages: `"Input should be 'ACTIVE', 'ON_HOLD', 'COMPLETED' or 'CANCELLED'"` (CP:9), `"Input should be 'DRAFT', 'OPEN', 'PARTIAL', 'PAID' or 'VOID'"` (CB:63), `"Input should be 'PERCENT' or 'AMOUNT'"`.
- Domain errors: `QuotingError` (SQ:39-40), `ProjectError` (SP:22-23) and `BillError` (SB:19-20) all become **400** `detail=str(e)` in their routers. GET routes return 404. Bills/projects routers do their own 404 lookup before calling the service, so the service "not found" strings are unreachable over REST for projects (SP:50) and bills (SB has none); quotations write routes give **400** `"quotation {id} not found"` (same pattern as PN 39).
- Decimals serialize as JSON strings with DB scale (`"100.0000"`), because every mutating route commits and the row reloads. Exception: computed values in ProjectSummary (see 1b).
- Frontend sends `quantity`, `unit_price`, `discount_value`, `contract_value` and bill payment `amount` as JSON **numbers**.
- **No ledger use anywhere in scope** (grep `ledger|journal` in SQ, SP, SB, RQ, RP, RB: only the docstring SB:4 "journal posting are handled by their own subsystems later"). Bill record-payment posts nothing (PN 8). Quote conversion posts nothing (the invoice is DRAFT).
- No `business_id` column on any of the five tables (DDL:10-45, :401-458).
- No JSONB / array columns on the five tables (section 3).

---

## 0. Storage

### quotations (DDL:436-458)
quotation_id uuid NOT NULL (PK `quotations_pkey`, DDL:687-688; Python uuid4, MQ:26-28, no DB default); customer_id uuid **NOT NULL** (FK customers, DDL:1068-1069); project_id uuid NULL (FK projects, DDL:1075-1076); quotation_number varchar(50) NULL (UNIQUE `quotations_quotation_number_key`, DDL:694-695); po_so_number varchar(100); currency varchar(3) NOT NULL; subtotal numeric(19,4) NULL; discount_type varchar(10); discount_value numeric(19,4); amount numeric(19,4) NOT NULL; status varchar(20) DEFAULT 'DRAFT' NOT NULL; issue_date date; valid_until date; payment_terms varchar(30); notes text; footer text; last_sent_at timestamp (no tz) NULL; accepted_at timestamp NULL; accepted_by varchar(255); converted_invoice_id uuid NULL (FK invoices, **no ON DELETE**, DDL:1061-1062); created_at timestamp DEFAULT CURRENT_TIMESTAMP **NOT NULL**.
- No CHECK constraints. No wht columns, no internal_note.
- Indexes: ix_quotations_converted_invoice_id (DDL:865), ix_quotations_customer_id (:871), ix_quotations_project_id (:877). No status index.
- FK into quotations: `invoices.source_quote_id` (DDL:984-985, no ON DELETE).

### quotation_line_items (DDL:420-430)
line_item_id uuid NOT NULL (PK DDL:680-681, Python uuid4 MQL:15-17); quotation_id uuid NOT NULL FK **ON DELETE CASCADE** (DDL:1054-1055); item_id uuid NULL FK items (DDL:1047-1048); "position" integer NOT NULL; description text NOT NULL; quantity / unit_price / amount numeric(19,4) NOT NULL; created_at timestamp DEFAULT CURRENT_TIMESTAMP NOT NULL. Index ix_quotation_line_items_quotation_id (DDL:859).
- ORM `line_items` ordered by `position` only, cascade delete-orphan, `lazy="selectin"` (MQ:58-63). Ties unordered.

### projects (DDL:401-414)
project_id uuid NOT NULL (PK DDL:673-674, Python uuid4 MP:22-24); customer_id uuid NOT NULL (FK DDL:1040-1041); code varchar(40) NOT NULL (UNIQUE **twice**: constraint `projects_code_key` DDL:666-667 **and** unique index `ix_projects_code` DDL:847 — the Go error mapper must recognise both names); name varchar(255) NOT NULL; currency varchar(3) NOT NULL; contract_value numeric(19,4) NULL; status varchar(20) DEFAULT 'ACTIVE' NOT NULL; start_date date; end_date date; notes text; active boolean DEFAULT true NOT NULL; created_at timestamp DEFAULT CURRENT_TIMESTAMP NOT NULL.
- Index ix_projects_customer_id (DDL:853). No CHECK.
- FKs into projects, all **no ON DELETE**: invoices.project_id (DDL:977-978), bills.project_id (:928-929), quotations.project_id (:1075-1076).
- There is no "budget" column; `contract_value` is the budget (MP:36-38).

### bills (DDL:27-45)
bill_id uuid NOT NULL (PK DDL:540-541, Python uuid4 MB:26-28); vendor_id uuid NOT NULL (FK vendors DDL:935-936); bill_number varchar(100) (**not unique**); po_number varchar(100); currency varchar(3) NOT NULL; subtotal numeric(19,4) NULL; discount_type varchar(10); discount_value numeric(19,4); amount numeric(19,4) NOT NULL; balance_due numeric(19,4) NOT NULL; status varchar(20) DEFAULT 'DRAFT' NOT NULL; issue_date date; due_date date; payment_terms varchar(30); notes text; created_at timestamp DEFAULT CURRENT_TIMESTAMP NOT NULL; project_id uuid NULL (FK projects DDL:928-929; column is last in the table).
- Indexes: ix_bills_project_id (DDL:763), ix_bills_status (:769), ix_bills_vendor_id (:775). No CHECK.
- Vendors are never deleted because bills reference them (Go: backend/internal/vendors/queries.sql:30).

### bill_line_items (DDL:10-21)
line_item_id uuid NOT NULL (PK DDL:533-534); bill_id uuid NOT NULL FK **ON DELETE CASCADE** (DDL:907-908); item_id uuid NULL FK items (:921-922); expense_account_id **integer** NULL FK chart_of_accounts (:914-915); "position" integer NOT NULL; description text NOT NULL; quantity / unit_price / amount numeric(19,4) NOT NULL; created_at timestamp DEFAULT CURRENT_TIMESTAMP NOT NULL. Index ix_bill_line_items_bill_id (DDL:757).
- ORM `line_items` ordered by `position` only, cascade delete-orphan, selectin (MB:54-59). The comment says expense account is "Required at the line level" (MBL:28-30) but the column and schema are nullable.

### QuotationOut (CQ:90-114), fields in order
1. quotation_id UUID
2. customer_id UUID (non-null)
3. project_id UUID|null
4. quotation_number str|null
5. po_so_number str|null
6. currency str
7. subtotal Decimal|null (4)
8. discount_type str|null
9. discount_value Decimal|null (4)
10. amount Decimal (4)
11. status str
12. issue_date date|null
13. valid_until date|null
14. payment_terms str|null
15. notes str|null
16. footer str|null
17. last_sent_at datetime|null (naive, no offset; written as `datetime.utcnow()`)
18. accepted_at datetime|null (naive UTC)
19. accepted_by str|null
20. converted_invoice_id UUID|null
21. created_at datetime (naive; DB `CURRENT_TIMESTAMP` in the session time zone)
22. line_items list[QuotationLineItemOut] (default []). Item (CQ:24-33): line_item_id UUID, item_id UUID|null, description str, quantity Decimal(4), unit_price Decimal(4), amount Decimal(4), position int. No created_at.

### ProjectOut (CP:37-51)
1. project_id UUID
2. customer_id UUID
3. code str
4. name str
5. currency str
6. contract_value Decimal|null (4)
7. status str
8. start_date date|null
9. end_date date|null
10. notes str|null
11. active bool
12. created_at datetime (naive)

### ProjectSummary (CP:54-64), not from_attributes, built in RP:152-161
1. project_id UUID
2. invoiced_total Decimal
3. paid_total Decimal
4. outstanding_total Decimal
5. contract_value Decimal|null
6. contract_remaining Decimal|null
7. invoice_count int
8. bill_total Decimal

Scale: `COALESCE(SUM(numeric(19,4)), 0)` returns `Decimal('0')` when no rows match, so the JSON is `"0"`, not `"0.0000"`; with rows it is `"1234.0000"`. paid_total = invoiced - outstanding inherits that (`"0"` when empty). contract_value is DB scale 4. contract_remaining = cv - invoiced is scale 4. Go `platform.Dec4` always marshals 4 places (backend/internal/platform/types.go:248) -> allowed difference like PN 50.

### BillOut (CB:66-86)
1. bill_id UUID
2. vendor_id UUID
3. project_id UUID|null
4. bill_number str|null
5. po_number str|null
6. currency str
7. subtotal Decimal|null (4)
8. discount_type str|null
9. discount_value Decimal|null (4)
10. amount Decimal (4)
11. balance_due Decimal (4)
12. status str
13. issue_date date|null
14. due_date date|null
15. payment_terms str|null
16. notes str|null
17. created_at datetime (naive)
18. line_items list[BillLineItemOut] (default []). Item (CB:18-28): line_item_id UUID, item_id UUID|null, expense_account_id int|null, description str, quantity Decimal(4), unit_price Decimal(4), amount Decimal(4), position int.

### InvoiceOut
As in the plan 5 inventory (GI `InvoiceOut`, api.go:70). Used by convert-to-invoice and `GET /projects/{id}/invoices`.

---

## 1. Routes

### (a) Quotations (RQ)

Route registration: `/{quotation_id}` routes only; no static sub-paths collide.

#### POST /api/v1/quotations -> 201 QuotationOut (RQ:31-43)
Request `QuotationCreate` (CQ:36-51):
| field | type | default | constraints |
|---|---|---|---|
| customer_id | UUID | **required** | not checked to exist: FK error at flush inside the service, not caught -> 500 |
| project_id | UUID\|null | None | not checked (FK -> 500); not checked to belong to the customer |
| currency | str | required | min_length=3, max_length=3; stored as sent (no upper, unlike projects/bills) |
| quotation_number | str\|null | None | falsy (None or "") -> generated (SQ:118); duplicate -> unique violation 500; no max_length (column 50) |
| po_so_number | str\|null | None | no max_length (column 100) |
| issue_date | date\|null | None | None -> `date.today()` (SQ:119) |
| valid_until | date\|null | None | None -> issue_date + 30 days (SQ:120); no check that it is >= issue_date |
| payment_terms | str\|null | None | no max_length (column 30) |
| notes, footer | str\|null | None | |
| discount_type | "PERCENT"\|"AMOUNT"\|null | None | Literal |
| discount_value | Decimal\|null | None | ge=0, max_digits=19, decimal_places=4; no PERCENT upper bound |
| line_items | list[QuotationLineItemIn] | [] | empty -> 400; explicit null -> 422 |

`QuotationLineItemIn` (CQ:16-21): item_id UUID|null=None; description str (required, may be ""); quantity Decimal ge=0 max_digits=19 decimal_places=4 (required); unit_price same; position int=0.

Errors: 400 `"quotation must have at least one line item"` (SQ:111-112). Everything else is 422 or an unhandled 500.
The router does `db.commit(); db.refresh(q)` (RQ:41-42).

Frontend: FE/QuotationFormView.vue:235, `buildPayload()` (:204-227). Sends customer_id, project_id, currency, quotation_number (`||null`), po_so_number, issue_date, valid_until (`YYYY-MM-DD` or null), payment_terms, notes, discount_type / discount_value (null unless the discount toggle is on; value is a number), line_items `{item_id, description, quantity:Number, unit_price:Number, position: idx}` with blank rows dropped (:206). It does **not** send `footer`. On success it navigates to `/quotations/{quotation_id}` (:240).

#### GET /api/v1/quotations -> 200 list[QuotationOut] (RQ:46-65)
Query: `status` list[str]|None (repeatable, any string, no Literal); `customer_id` UUID|None; `project_id` UUID|None; `limit` int default 200, `le=500`, **no lower bound** (negative -> Postgres error 500).
SQL: `[WHERE status IN (...)] [AND customer_id=?] [AND project_id=?] ORDER BY created_at DESC LIMIT :limit`. No tie-break.
Frontend:
- FE/QuotationsView.vue:51-62: tabs send `status=DRAFT&status=SENT` (Open), `status=ACCEPTED&status=INVOICED` (Accepted), or nothing (All) (:45-49). Reads quotation_id, customer_id, quotation_number, currency, amount, status, issue_date, valid_until, created_at (:19-29).
- FE/CustomerDetailView.vue:118-127 sends `customer_id`.
- FE/ProjectDetailView.vue:88-96 sends `project_id`; reads quotation_id, quotation_number, currency, amount, status, issue_date, valid_until (:78-86).

#### GET /api/v1/quotations/{quotation_id} -> 200 QuotationOut (RQ:68-77)
404 `"quotation not found"`.
Frontend: FE/QuotationDetailView.vue:67-70 (full QuotationOut, interface :43-65) and FE/QuotationFormView.vue:128-135 (edit).

#### PATCH /api/v1/quotations/{quotation_id} -> 200 QuotationOut (RQ:80-93; SQ:145-202)
Request `QuotationUpdate` (CQ:54-69): every field optional default None, same constraints as create (customer_id `UUID|None`, currency min/max 3, discount Literal, discount_value ge=0, `line_items: list|None`). `model_dump(exclude_unset=True)`: only present keys apply, explicit null is applied.
Errors (400):
- `"quotation {quotation_id} not found"` (SQ:149-150)
- `"only DRAFT or SENT quotations can be edited (got {status})"` (SQ:151-154)
- explicit null on `customer_id` or `currency` -> NOT NULL violation 500 (PN 10 pattern).
No empty-lines check: `line_items: []` is accepted and gives subtotal = amount = 0 with no lines.
Frontend: FE/QuotationFormView.vue:233 sends the same `buildPayload()` (all keys, including `quotation_number: null` when the box is blank, which clears the stored number).

#### DELETE /api/v1/quotations/{quotation_id} -> 204 (RQ:96-106; SQ:254-263)
Errors (400): `"quotation {id} not found"`; `"only DRAFT quotations can be deleted (got {status})"`. Lines cascade. A DRAFT quote is never referenced by `invoices.source_quote_id` (conversion sets INVOICED), so no FK failure in practice.
Frontend: FE/QuotationDetailView.vue:218-225 (button only when status DRAFT, :125).

#### POST /api/v1/quotations/{quotation_id}/mark-sent -> 200 QuotationOut (RQ:109-120; SQ:205-214)
No body. Errors (400): `"quotation {id} not found"`; `"cannot mark-sent from status {status}"` (any status but DRAFT/SENT).
Effect: status=SENT, `last_sent_at = datetime.utcnow()` (re-marking a SENT quote refreshes last_sent_at). No db.refresh (reload after commit).
Frontend: FE/QuotationDetailView.vue:137-146 (button only for DRAFT, :104).

#### POST /api/v1/quotations/{quotation_id}/accept -> 200 QuotationOut (RQ:123-135; SQ:217-229)
Body `AcceptQuotationRequest` (CQ:81-82): `accepted_by: str|None = None`, no max_length (column 255 -> 500 when longer). The body itself is **required** (FastAPI model param without default: missing body -> 422).
Errors (400): `"quotation {id} not found"`; `"cannot accept from status {status}"` (any status but DRAFT/SENT).
Effect: status=ACCEPTED, `accepted_at = datetime.utcnow()`, `accepted_by = payload.accepted_by` (null overwrites).
Frontend: FE/QuotationDetailView.vue:185-194 sends `{accepted_by: null}`.

#### POST /api/v1/quotations/{quotation_id}/decline -> 200 QuotationOut (RQ:138-149; SQ:232-240)
No body. Errors (400): not found; `"cannot decline from status {status}"` (any status but DRAFT/SENT). Effect: status=DECLINED only.
Frontend: FE/QuotationDetailView.vue:196-205.

#### POST /api/v1/quotations/{quotation_id}/void -> 200 QuotationOut (RQ:152-163; SQ:243-251)
No body. Errors (400): not found; `"cannot void an already-invoiced quotation"` (INVOICED only). Every other status, including VOID, DECLINED, ACCEPTED and EXPIRED, becomes VOID. No reason.
Frontend: FE/QuotationDetailView.vue:207-216, sends no body (button hidden for VOID/INVOICED, :121-124).

#### POST /api/v1/quotations/{quotation_id}/convert-to-invoice -> **201 InvoiceOut** (RQ:242-264; SQ:266-339)
Body `ConvertToInvoiceRequest` (CQ:85-87): `issue_date: date|None = None`; `due_in_days: int = 14` (unbounded, negatives allowed). Body required (missing -> 422).
Errors (400), in order:
- `"quotation {quotation_id} not found"` (SQ:275-276)
- `"cannot convert a {status} quotation to invoice"` for INVOICED, VOID, DECLINED (SQ:277-280); note the grammar `"cannot convert a INVOICED quotation to invoice"`
- `"quotation has no line items to convert"` (SQ:281-282)
- withholding normalize sentences from the customer default, re-raised as QuotingError (SQ:289-294): `"Unknown withholding tax type: {t}"`, `"Withholding tax rate must be greater than 0 and less than 100"`
- unhandled: due-date overflow (`today + timedelta(days)`) -> 500; duplicate generated invoice number under a race -> 500.
Router: `db.commit(); db.refresh(invoice)` (RQ:262-263).
Frontend: FE/QuotationDetailView.vue:227-238 sends `{}`, reads `invoice_id` and navigates to `/invoices/{invoice_id}/edit`. Button shown for DRAFT/SENT/ACCEPTED with no converted_invoice_id (:117-120).

#### Out of scope (documents plan)
- GET `/quotations/{id}/pdf` (RQ:166-183): inline quotation PDF, 404 `"quotation not found"`. FE QuotationDetailView:133 (window.open).
- POST `/quotations/{id}/send` (RQ:186-239): emails the PDF; 404 `"quotation not found"`, 400 `"cannot send a {status} quotation"` (VOID/INVOICED), 400 `"no recipient: provide to_email or set a contact_email on the customer"`; DRAFT -> SENT, `last_sent_at = datetime.utcnow()`. FE QuotationDetailView:150-170.

### (b) Projects (RP)

`_load` (RP:26-30): `db.get`, 404 `"project not found"`.

#### POST /api/v1/projects -> 201 ProjectOut (RP:33-45; SP:26-44)
Request `ProjectCreate` (CP:12-21):
| field | type | default | constraints |
|---|---|---|---|
| customer_id | UUID | required | unknown -> FK IntegrityError, caught as **"project code ... already exists"** (see oddities) |
| code | str | required | min_length=1, max_length=40, checked **before** `strip().upper()` (SP:29) |
| name | str | required | min 1, max 255, checked before `strip()` (SP:30) |
| currency | str | required | min/max 3, then `.upper()` (SP:31) |
| contract_value | Decimal\|null | None | ge=0, max_digits=19, decimal_places=4 |
| status | Literal ACTIVE\|ON_HOLD\|COMPLETED\|CANCELLED | "ACTIVE" | |
| start_date, end_date | date\|null | None | no ordering check |
| notes | str\|null | None | |
`active` is not on create (DB default true).
Errors (400): `f"project code {project.code!r} already exists"` (SP:39-43), i.e. `project code 'DUP-1' already exists` (Python repr, normalised code). Raised for **any** IntegrityError at flush.
Frontend: FE/ProjectFormView.vue:156 `buildBody()` (:135-149): customer_id, code (`trim().toUpperCase()` client side too), name (trimmed), currency, status, contract_value (number or null), start_date, end_date, notes (`||null`). Error shown from `detail` (:163-165).

#### GET /api/v1/projects -> 200 list[ProjectOut] (RP:48-66)
Query: `customer_id` UUID|None; `status` list[str]|None (repeatable, any string); `active` bool|None (`WHERE active IS true/false`). **No limit.**
SQL: `ORDER BY created_at DESC`, no tie-break.
Frontend:
- FE/ProjectsView.vue:55-65: tabs `status=ACTIVE` / `status=ON_HOLD` / none (:49-53). Reads the Project interface (:14-27).
- FE/BillFormView.vue:115-120 `params: {status: "ACTIVE"}`.
- FE/InvoiceFormView.vue:238-249 and FE/QuotationFormView.vue:115-126 `params: {customer_id, status: "ACTIVE"}` (ProjectLite: project_id, customer_id, code, name, currency, status).

#### GET /api/v1/projects/{project_id} -> 200 ProjectOut (RP:69-75)
404 `"project not found"`. Frontend: FE/ProjectDetailView.vue:61-64, FE/ProjectFormView.vue:85-88.

#### PATCH /api/v1/projects/{project_id} -> 200 ProjectOut (RP:78-92; SP:47-63)
Request `ProjectUpdate` (CP:24-34): all optional default None; same constraints as create plus `active: bool|None`. `exclude_unset=True`; code (if not None) `strip().upper()`, currency (if not None) `upper()`; then **setattr every present key**, nulls included.
Errors:
- 404 `"project not found"` (router `_load` first, RP:85). The service's `"project {project_id} not found"` (SP:49-50) is unreachable over REST.
- 400 `"project code already exists"` (SP:58-62, no repr) for **any** IntegrityError: duplicate code, unknown customer_id, **and explicit null** for code/name/currency/status/active/customer_id (NotNullViolation is an IntegrityError subclass).
No status machine: any status to any status.
Frontend: FE/ProjectFormView.vue:154 sends `buildBody()` plus `active` on edit (:146).

#### DELETE /api/v1/projects/{project_id} -> 204 (RP:95-115)
- 404 `"project not found"`.
- 400 `f"cannot delete: project is referenced by {ref_count} invoice(s)/bill(s)"` where ref_count = count(invoices with project_id, **any status, templates included**) + count(bills with project_id, any status) (RP:103-113).
- **Quotations are not counted**: a project referenced only by a quotation fails at commit on `quotations_project_id_fkey` -> 500.
- Count then delete with no lock (a concurrent insert gives an FK 500).
Frontend: **not called**.

#### GET /api/v1/projects/{project_id}/summary -> 200 ProjectSummary (RP:118-161)
- 404 `"project not found"`.
- Invoices: `SELECT COALESCE(SUM(amount),0), COALESCE(SUM(balance_due),0), COUNT(invoice_id) FROM invoices WHERE project_id=? AND status IN ('OPEN','SENT','PARTIAL','PAID')` (RP:127-136). Templates are DRAFT so excluded implicitly; no is_template filter.
- `invoiced_total = Decimal(sum amount)`; `outstanding_total = Decimal(sum balance_due)`; `paid_total = invoiced_total - outstanding_total`; `invoice_count = int(count)` (RP:137-140).
- Bills: `SELECT COALESCE(SUM(amount),0) FROM bills WHERE project_id=? AND status IN ('OPEN','PARTIAL','PAID')` (RP:142-147).
- `contract_value = Decimal(project.contract_value)` or None; `contract_remaining = contract_value - invoiced_total` or None (RP:149-150). Can be negative.
- No currency filter or grouping: invoices and bills in any currency are added together. No rounding.
Frontend: FE/ProjectDetailView.vue:66-70 reads every field (:28-37); `contract_value` truthiness and `Number(...)` progress math (:121-127).

#### GET /api/v1/projects/{project_id}/invoices -> 200 list[InvoiceOut] (RP:164-178)
404 `"project not found"`. `WHERE project_id=? AND is_template=false ORDER BY created_at DESC`, no limit, every status (DRAFT and VOID included).
Frontend: FE/ProjectDetailView.vue:72-76 reads invoice_id, invoice_number, invoice_type, amount, balance_due, status, currency, issue_date, due_date (:39-49).

### (c) Bills (RB)

#### POST /api/v1/bills -> 201 BillOut (RB:19-31; SB:45-81)
Request `BillCreate` (CB:31-45):
| field | type | default | constraints |
|---|---|---|---|
| vendor_id | UUID | required | not checked (FK at the service's flush, not caught -> 500) |
| project_id | UUID\|null | None | not checked (FK -> 500) |
| bill_number | str\|null | None | not unique, not generated; no max_length (column 100) |
| po_number | str\|null | None | no max_length (100) |
| currency | str | required | min/max 3, then `.upper()` (SB:51) |
| issue_date, due_date | date\|null | None | **no defaults** (no today, no terms-based due date) |
| payment_terms | str\|null | None | no max_length (30); never parsed |
| notes | str\|null | None | |
| discount_type | PERCENT\|AMOUNT\|null | None | |
| discount_value | Decimal\|null | None | ge=0, 19/4 |
| line_items | list[BillLineItemIn] | [] | **empty allowed** (bill of 0) |

`BillLineItemIn` (CB:9-15): item_id UUID|null=None; expense_account_id int|null=None (not checked to exist or be EXPENSE/COGS; unknown -> FK 500); description str required; quantity / unit_price Decimal ge=0 19/4 required; position int=0.
No 400s exist on create. Router commits and refreshes (RB:29-30).
Frontend: FE/BillFormView.vue:266, `buildPayload()` (:236-259): vendor_id, project_id, bill_number (`||null`), po_number, currency, issue_date, due_date, payment_terms, notes, line_items `{expense_account_id, description, quantity:Number, unit_price:Number, position: idx}` (rows with neither description nor account dropped). It sends **no** item_id, discount_type, discount_value or status. Vendor defaults: currency and `payment_terms = "Net {payment_terms_days}"` on new bills (:146-153).

#### GET /api/v1/bills -> 200 list[BillOut] (RB:34-49)
Query: `status` list[str]|None (repeatable); `vendor_id` UUID|None. **No limit, no project_id filter.** `ORDER BY created_at DESC`, no tie-break.
Frontend: FE/BillsView.vue:54-64: tabs `status=OPEN&status=PARTIAL` (Unpaid), `status=DRAFT`, none (:48-52). Reads bill_id, vendor_id, bill_number, po_number, currency, amount, balance_due, status, issue_date, due_date, created_at (:19-31).

#### GET /api/v1/bills/{bill_id} -> 200 BillOut (RB:52-61)
404 `"bill not found"`. Frontend: FE/BillFormView.vue:132-139 (BillOut interface :52-75; lines sorted by position).

#### PATCH /api/v1/bills/{bill_id} -> 200 BillOut (RB:64-80; SB:84-115)
Request `BillUpdate` (CB:48-63): all optional; same fields as create plus **`status: Literal["DRAFT","OPEN","PARTIAL","PAID","VOID"]|None`**.
Errors:
- 404 `"bill not found"` (RB:71-73).
- 400 `f"bill is {bill.status} and cannot be edited"` when the **current** status is not DRAFT/OPEN (SB:85-86).
- explicit null on vendor_id or currency -> NOT NULL 500 (currency null skips `.upper()` then is set).
Effect:
- setattr every present key except line_items, **including `status`** (SB:88-92). This is the only way a bill becomes OPEN.
- If `line_items` is not None: clear lines, flush, insert rebuilt lines, `_apply_totals` (SB:94-112).
- Totals are **not** recomputed when only the discount changes (no line_items key).
- `_apply_totals` re-baselines balance_due only when the (possibly just-PATCHed) status is DRAFT (SB:41-42).
Frontend: FE/BillFormView.vue:264 sends the full `buildPayload()` (no status). Form is read-only unless status is DRAFT or OPEN (:187-189).

#### POST /api/v1/bills/{bill_id}/record-payment -> **200** BillOut (RB:83-116)
Request `RecordPaymentRequest` (PAY:50-56): amount Decimal gt=0 max_digits=19 decimal_places=4 (required); payment_date date (required, **unused**); payer_name, payer_reference, notes str|null (**unused**).
Errors, in order:
- 404 `"bill not found"` (RB:96-97)
- 400 `f"cannot record payment on {bill.status} bill"` when status is DRAFT, VOID or PAID (RB:98-101)
- 400 `f"amount {amount} exceeds balance due {bill.balance_due}"` (RB:103-107). `amount` prints as the parsed Decimal (`600`, `600.5`, `600.00` as sent); balance prints DB scale (`500.0000`).
Effect: `balance_due -= amount`; `<= 0` -> balance 0, status PAID; else PARTIAL (RB:108-113). No Payment row, no ledger (PN 8), no currency, no row lock.
Frontend: FE/BillFormView.vue:196-213 sends `{amount: number, payment_date, payer_reference, notes}`; button only for OPEN/PARTIAL (:192-195); shows `detail` on error.

#### POST /api/v1/bills/{bill_id}/void -> 200 BillOut (RB:119-136; SB:118-125)
Body `payload: dict` (**required**, any JSON object; missing -> 422). `reason = payload.get("reason") or "no reason given"` (any JSON type, formatted with f-string).
Errors: 404 `"bill not found"`; 400 `f"bill is {bill.status} and cannot be voided"` for PAID or VOID.
Effect: status VOID, balance_due 0, `notes = (notes or "") + f"\n[voided] {reason}"` (leading newline when notes was null).
Frontend: **not called**.

#### Not a REST route
Bill delete exists only as MCP `gated_delete.delete_bill` (DRAFT only, app/mcp/tools/gated_delete.py:60, :102). [OUT: MCP plan]

---

## 2. Service rules

### Quote totals (SQ:43-70): **own copy, not invoicing's `_compute_totals`**
```python
Q4 = Decimal("0.0001")                                  # SQ:43
def _round4(v): return v.quantize(Q4, rounding=ROUND_HALF_UP)   # SQ:46-47
subtotal = round4(sum(Decimal(qty) * Decimal(price)))   # raw sum, rounded once (SQ:55-58)
PERCENT and value is not None -> amount = round4(subtotal * (1 - value/100))
AMOUNT  and value is not None -> amount = round4(subtotal - value)
else amount = subtotal
if amount < 0: amount = 0
return subtotal, amount                                 # no withholding on quotes
```
- Identical to INV `_compute_totals` minus withholding. Go: `invoices.ComputeTotals(lines, dt, dv, currency, "NONE", nil, false)` returns `Amount = WHT.Total = base` (W: NONE -> total=base), so it is reusable as is; `invoices.LineAmount` for each line.
- Line build (SQ:92-107): `amount = round4(qty*price)`, `position = ln.position if not None else idx` (dead fallback; position defaults 0).
- Imports from invoicing (SQ:30-35): `InvoicingError`, `_parse_payment_terms_days`, `_resolve_wht`, `generate_invoice_number`. All three are used **only** in convert_to_invoice.
- update recomputes from the **current** lines and the quote's discount after applying the patch, whether or not lines changed (SQ:185-199), so a discount-only PATCH reprices.

### Quote numbering (SQ:73-89)
```python
year = date.today().year            # server-local today, not issue_date
prefix = f"Q{year}"
rows = SELECT quotation_number WHERE quotation_number LIKE 'Q{year}%'
max_n = max(int(tail) for tail = n[len(prefix):] if tail.isdigit())
return f"{prefix}{max_n + 1:04d}"   # Q20260001
```
- Assigned **at create** only, when `payload.quotation_number` is falsy (SQ:118). Never on mark-sent/send.
- No lock, no retry (unique `quotations_quotation_number_key` -> 500 on a race or a duplicate supplied number).
- Separate sequence from invoices (`WH{YYYY}`). The test fixture uses `"QUO-0001"` (tests/conftest.py:383), which the scan ignores.
- Go: mirror `invoices.NextInvoiceNumber` (advisory lock `quotation-number:<year>`, ASCII tails, UTC year; PN 34 and PN 5) and map the unique violation to 409.

### Quote status machine
Statuses (CQ:11-13): DRAFT, SENT, ACCEPTED, DECLINED, EXPIRED, INVOICED, VOID. **Nothing ever sets EXPIRED** (grep: only the Literal, docstrings, PDF badge app/services/pdf/context.py:26 and FE badges). No scheduler job checks valid_until.

| From | Action | To | Refusal |
|---|---|---|---|
| (new) | create | DRAFT (SQ:132) | |
| DRAFT, SENT | PATCH | unchanged | else `"only DRAFT or SENT quotations can be edited (got {status})"` |
| DRAFT, SENT | mark-sent | SENT, last_sent_at=utcnow | else `"cannot mark-sent from status {status}"` |
| DRAFT, SENT | accept | ACCEPTED, accepted_at=utcnow, accepted_by | else `"cannot accept from status {status}"` |
| DRAFT, SENT | decline | DECLINED | else `"cannot decline from status {status}"` |
| any but INVOICED | void | VOID | `"cannot void an already-invoiced quotation"` |
| DRAFT | delete | (gone) | else `"only DRAFT quotations can be deleted (got {status})"` |
| DRAFT, SENT, ACCEPTED, EXPIRED | convert | INVOICED | INVOICED/VOID/DECLINED: `"cannot convert a {status} quotation to invoice"` |
| DRAFT | send (out of scope) | SENT | VOID/INVOICED: `"cannot send a {status} quotation"` (RQ:196-199) |

No transition out of ACCEPTED except void/convert; no un-decline; accept/decline skip SENT.

### Expiry / valid_until
- Default `issue_date + 30 days` at create (SQ:119-120), where issue_date is the payload's or today.
- PATCH sets it as sent (null clears it; no re-default). No validation vs issue_date. Never enforced (accept/convert after valid_until are allowed).

### convert_to_invoice (SQ:266-339)
Steps, in order:
1. `db.get` quote (no lock); status check; empty-lines check.
2. `today = issue_date or date.today()` (SQ:284).
3. `days = due_in_days`; **if `q.payment_terms` is truthy**, `days = _parse_payment_terms_days(q.payment_terms)` (SQ:285-287) — so unparseable terms give 14 and override `due_in_days`. Go cannot use `DueAfterTerms` directly (nil terms there give 14, not due_in_days): use `PaymentTermsDays` only for non-empty terms (refuse ok=false as `due_date_out_of_range`), else `due_in_days`, then `invoices.AddDays`.
4. `_resolve_wht(db, q.customer_id, None, None, None)`: **customer default only** (quotes have no wht fields). InvoicingError -> QuotingError -> 400. Go: `invoices.ResolveWHT(ctx, q, &customerID, nil, nil, nil)`.
5. `wht = compute_withholding(q.amount, q.currency, wht_type, wht_rate, wht_gross_up)` on the **stored** quote amount (after discount).
6. New Invoice field mapping (SQ:296-321):

| Invoice field | Source |
|---|---|
| customer_id | q.customer_id |
| project_id | q.project_id |
| source_quote_id | q.quotation_id |
| invoice_type | `"MILESTONE"` |
| invoice_number | `generate_invoice_number(db)` (always; WH{YYYY}{NNNN}; INV:157-171) |
| po_so_number | q.po_so_number |
| currency | q.currency |
| subtotal | q.subtotal (stored) |
| discount_type / discount_value | q's |
| wht_type / wht_rate / wht_gross_up | customer default (step 4) |
| wht_base | wht.base (= q.amount) |
| wht_amount | wht.wht_amount |
| amount, balance_due | wht.total (gross-up raises it above q.amount) |
| wht_booked | DB default 0 |
| status | `"DRAFT"` |
| issue_date | today (step 2) |
| due_date | `today + timedelta(days=days)` |
| payment_terms, notes, footer | q's |
| is_template | False |
| internal_note, coverage_*, billing_cycle_ref | null (not set) |
| line_items | copied verbatim: item_id, position, description, quantity, unit_price, **stored amount** (SQ:322-332) |

   Nothing comes from the business profile (it is read only by pdf/send, RQ:176, :201).
7. flush, then `q.status = "INVOICED"`, `q.converted_invoice_id = invoice.invoice_id` (SQ:336-338). accepted_at is not set when converting from DRAFT/SENT.

Idempotency: only the status check. A second call is 400 `"cannot convert a INVOICED quotation to invoice"`. Two concurrent calls both pass (no lock) and create two invoices; the later `converted_invoice_id` wins. Go: read the quote `FOR UPDATE`, then take the invoice number lock last (GI lock order, PN 35), insert through `invoices.MapWriteError`, return via `invoices.Get`.
Afterwards the invoice cannot be deleted (Go 409 `invoice is referenced by a payment or quotation`, GI lifecycle.go:96; PN 36) and voiding it leaves the quote INVOICED.

### Projects
- Normalisation (SP:26-37, :51-57): create `code.strip().upper()`, `name.strip()`, `currency.upper()`; update only `code.strip().upper()` and `currency.upper()` (**name is not stripped on PATCH**).
- Status: 4 Literal values, no transitions enforced; `active` is an independent boolean (create can't set it; PATCH can).
- Budget: `contract_value` only (nullable, ge=0). No check against invoiced totals.
- Linking: invoices and bills carry `project_id` (set on create/PATCH; quote conversion copies it; recurring children copy the template's). Nothing checks that the project's customer or currency matches the invoice/quote, or that a bill's project is related to anything.
- Summary math: see 1b. Quoted:
  ```python
  invoiced_total = Decimal(invoice_stats[0])            # SUM(amount) for OPEN/SENT/PARTIAL/PAID
  outstanding_total = Decimal(invoice_stats[1])         # SUM(balance_due)
  paid_total = invoiced_total - outstanding_total
  bill_total = Decimal(bill_stats[0])                   # SUM(bills.amount) for OPEN/PARTIAL/PAID
  contract_remaining = (cv - invoiced_total) if cv is not None else None
  ```
  No rounding. `amount` is the grossed-up total for WHT invoices, so invoiced_total includes gross-up; "paid" includes withheld tax and mark-paid write-offs (balance zeroed without cash).

### Bills (SB)
- Line amount (SB:23-24): `(quantity * unit_price).quantize(Decimal("0.0001"))` with **no rounding argument -> context default ROUND_HALF_EVEN** (unlike invoices/quotes HALF_UP).
- `_apply_totals` (SB:27-42):
  ```python
  subtotal = sum(rounded line amounts)          # sum of ROUNDED lines (invoices/quotes round the raw sum)
  total = subtotal
  if bill.discount_type and bill.discount_value is not None:
      PERCENT: total = subtotal * (1 - value/100)   # unrounded
      AMOUNT:  total = subtotal - value
      if total < 0: total = 0
  bill.subtotal = subtotal.quantize(0.0001)       # HALF_EVEN
  bill.amount   = total.quantize(0.0001)          # HALF_EVEN
  if bill.status in (None, "DRAFT"): bill.balance_due = bill.amount
  ```
  Go cannot reuse `invoices.ComputeTotals` (different rounding mode and subtotal basis). Use `decimal.RoundBank(4)` (shopspring) for HALF_EVEN.
- Position (SB:71, :105): `ln.position or idx` — position 0 becomes the list index (unlike quotes/invoices, where 0 stays 0).
- Create: inserts the bill with amount=balance_due=0 and status DRAFT, flushes, adds lines, applies totals (SB:46-81).
- Statuses (MB:19-21 docstring): DRAFT -> OPEN -> PARTIAL -> PAID, -> VOID. Enforced transitions:

| From | Action | To | Refusal |
|---|---|---|---|
| (new) | create | DRAFT | |
| DRAFT, OPEN | PATCH | **any of the 5 via `status`**, else unchanged | `"bill is {status} and cannot be edited"` |
| OPEN, PARTIAL (and any non DRAFT/VOID/PAID) | record-payment | PARTIAL or PAID | `"cannot record payment on {status} bill"`; `"amount {amount} exceeds balance due {balance_due}"` |
| DRAFT, OPEN, PARTIAL | void | VOID (balance 0, note appended) | `"bill is {status} and cannot be voided"` |

- Record-payment math (RB:102-113): overpayment refused (strict `>`); exact payment -> PAID with balance 0; no currency on the request; no FX; no row lock.
- Vendor link: vendor_id required, never validated; vendor defaults are applied by the frontend only.
- Expense accounts: per line `expense_account_id`, nullable, no type check (FE filters EXPENSE/COGS, BillFormView.vue:127-130). Nothing posts to them.

### Rounding per call site
| Site | Mode |
|---|---|
| SQ:46-47 `_round4` (quote subtotal, discounted amount, line amount) | HALF_UP to 4 |
| SQ:295 `compute_withholding` on convert | round_ccy HALF_UP (IDR 1, else 0.01), W:27-30 |
| SB:24, :37-38 (bill lines, subtotal, amount) | quantize default context = **HALF_EVEN** to 4 |
| RB:108 record-payment subtraction | none (both scale <= 4) |
| RP:137-150 summary | none |
Also: Python's default decimal context has prec=28, so `qty * price` with more than 28 significant digits is rounded before quantize, and a quantize whose result needs more than 28 digits raises InvalidOperation (500). Reachable only with absurd inputs (19-digit qty x 19-digit price).

### date.today / clock use
- SQ:74 (quote number year), SQ:119 (create issue default), SQ:284 (convert issue default), INV:158 via generate_invoice_number (invoice number year).
- `datetime.utcnow()`: SQ:212 (mark-sent), SQ:226 (accept), RQ:236 (send, out of scope).
- Projects and bills: none. No `datetime.now`.
- Go: one clock seam (`platform.Clock`), UTC date per PN 5; last_sent_at/accepted_at as naive UTC timestamps.

### Ledger
None (see Global facts). No caller in scope imports app/services/ledger.py.

---

## 3. JSON / special columns
- None of quotations, quotation_line_items, projects, bills, bill_line_items has a JSON or array column (DDL:10-45, :401-458).
- Adjacent: `customers.quotation_cc_emails` jsonb (DDL:170) is read only by the out-of-scope send route; already ported with customers.
- `bill_line_items.expense_account_id` is an **integer** FK (chart_of_accounts.account_id is int), the only non-uuid id in scope.
- Timestamps without tz: `last_sent_at`, `accepted_at` (written in UTC) next to `created_at` (DB clock).

---

## 4. Tests

Legend: **S** = service-level (DB, no HTTP); **H** = HTTP; **U** = pure unit. `[OUT]` names the plan.

### In scope
tests/unit/test_projects_service.py (4, S; fixtures `seed_customer`, `seed_project` tests/conftest.py:223, :394-406):
- `test_create_project_normalizes_code_and_currency`: "atlas-26" -> "ATLAS-26", "usd" -> "USD", " Atlas Project " -> "Atlas Project", status ACTIVE
- `test_create_project_duplicate_code_raises_and_leaves_one_row`: second "DUP-1" raises ProjectError; one row remains
- `test_update_project_edits_and_normalizes`: code "atlas-27" -> "ATLAS-27", currency "sgd" -> "SGD", status ON_HOLD
- `test_update_project_not_found_raises`: ProjectError for a random id (Go: route-level 404 instead, the service sentence is unreachable over REST)

tests/integration/test_invoice_withholding.py (1 of 13, S):
- `test_quote_conversion_applies_customer_default` (:146-167): IDR customer default PPH23 gross-up; quote 1 x 10,000,000 -> invoice wht_type PPH23, amount = balance_due = 10204082, wht_amount 204082, wht_base 10000000.

### [OUT]
- MCP plan:
  - test_mcp_writes_all.py: projects (5: `test_create_project_normalizes_code_and_currency`, `test_create_project_duplicate_code_returns_error_and_persists_only_one`, `test_update_project_edits_fields`, `test_update_project_rejects_unknown_field_names`, `test_create_project_echoes_notes`, :137-189); bills (6: `test_create_bill_makes_draft` asserts amount `"200.0000"`, `test_update_bill_edits_open_bill`, `test_update_bill_not_found_returns_error` pins `{"error": "bill {id} not found"}` (an MCP-only sentence), `test_update_bill_rejects_unknown_field_names`, `test_update_bill_unknown_field_check_precedes_not_found`, `test_update_bill_echoes_notes`, :359-414); quotations (5: `test_create_quotation_makes_draft` amount `"1000.0000"`, `test_update_quotation_edits_draft`, `test_create_quotation_without_line_items_returns_error` pins `"quotation must have at least one line item"`, `test_update_quotation_rejects_unknown_field_names`, `test_update_quotation_echoes_notes`, :421-465).
  - test_mcp_reads_all.py: bills (5, :24-91), quotations (5, :97-162), projects (4, :168-215).
  - test_mcp_gated_delete.py: bills (4, :75-107; DRAFT-only delete).
- Documents plan: unit/test_quotation_pdf.py (4), unit/test_pdf_context.py (`test_quotation_context_total_hero`, `test_both_builders_share_key_set`, `test_quotation_notes_fall_back_to_business_default`), unit/test_pdf_render_html.py::`test_quotation_html_renders_total`, unit/test_pdf_wiring.py::`test_routers_use_facade` (asserts RQ uses the pdf facade).
- Stats plan: test_dashboard_stats.py::`test_open_quotation_pipeline_sums_sent_and_accepted_only_sorted_desc` (and the pipeline fields in `test_empty_db_returns_zero_counts_and_no_currency_rows`, `test_dashboard_endpoint_matches_service_function`); test_mcp_read_misc.py get_stats tests reading `open_quotation_pipeline`.

### Coverage gaps (no Python test at all)
Every one of the 23 in-scope HTTP routes. Service-level: quote create/update/totals/numbering, every quote transition, convert field mapping and refusals (beyond the one wht test), project delete/summary/invoices, every bill service rule (HALF_EVEN, position rule, PATCH status, totals), record-payment, void.

### In-scope count
| File | In scope |
|---|---|
| test_projects_service | 4 |
| test_invoice_withholding (quote conversion) | 1 |
| **Total** | **5** |
New Go tests carry the plan; estimate ~70 (about 3 per route plus the oddities decided).

---

## 5. Oddities (decide: replicate or fix, then add a PORT-NOTES entry)

1. **Projects: every IntegrityError is reported as a duplicate code.** SP:39-43 and :58-62 catch any IntegrityError. An unknown `customer_id` on create answers 400 `"project code 'X' already exists"`; on PATCH, an unknown customer_id **or an explicit null** for code/name/currency/status/active/customer_id answers 400 `"project code already exists"` (NotNullViolation subclasses IntegrityError). Go: map `projects_code_key`/`ix_projects_code` to the code sentence, the customer FK to 422, nulls to 422 (PN 10).
2. **Project delete ignores quotations** (RP:103-108). A project referenced only by a quote fails at commit on `quotations_project_id_fkey` -> 500. It counts VOID invoices, templates and VOID bills as references. Count-then-delete has no lock.
3. **Project summary mixes currencies** (RP:128-147): SUM over invoices and bills in any currency, and `contract_remaining` subtracts a mixed sum from a project-currency value. `"0"` vs `"0.0000"` scale when empty (section 0). paid_total counts withheld tax and mark-paid write-offs as paid.
4. **Project code/name normalisation after validation** (CP:14-15, SP:29-30): `"   "` passes min_length=1 and is stored as `""`; `.upper()` on non-ASCII can lengthen (`"ß"` -> `"SS"`), so a 40-char code or a 3-char currency can overflow its column after validation (DataError, not IntegrityError -> 500). PATCH does not strip `name` (SP:51-57).
5. **Bill status is freely writable through PATCH** (CB:63, SB:88-92): DRAFT/OPEN -> PAID with no payment, -> VOID without the void note, OPEN -> DRAFT (which re-baselines balance_due on the next line edit). It is also the **only** way to open a bill, and the frontend never sends `status` (FE/BillFormView.vue:236-259) and has no "open" action, so a bill created in the UI stays DRAFT and the record-payment button (OPEN/PARTIAL only, :192-195) never appears. Only MCP can open a bill.
6. **Bill PATCH on an OPEN bill desyncs balance_due**: new lines recompute `amount` but balance_due is re-baselined only for DRAFT (SB:41-42), so balance_due can exceed or fall below the new amount. A discount-only PATCH does not recompute totals at all (SB:94-112).
7. **Bill rounding differs from invoices/quotes**: HALF_EVEN quantize (SB:24, :37-38), subtotal = sum of rounded lines, unrounded PERCENT total before clamp. `position or idx` (SB:71, :105) differs from quotes/invoices.
8. **Bill record-payment** (RB:83-116): no Payment row, no ledger (PN 8), `payment_date`/`payer_*`/`notes` discarded, no lock (two concurrent payments lose an update), status 200 not 201 (invoice record-payment is 201). Error text echoes the amount as sent (`600` vs `600.00`).
9. **Bill void** (RB:119-136, SB:118-125): body is an untyped `dict` (required); reason may be any JSON type; note appended with a leading newline when notes is null; PARTIAL bills are voidable with balance zeroed. Not called by the frontend. There is no REST bill delete (MCP only).
10. **Bill create accepts no lines** (SB:65-81): a 0-amount DRAFT bill; no issue/due defaults; `payment_terms` is never parsed; `bill_number` not unique.
11. **Unhandled 500s on quotes/bills**: unknown customer_id/project_id/item_id (quotes, SQ:140-141), unknown vendor_id/project_id/item_id/expense_account_id (bills, SB:62-63, :79-80); duplicate or racing `quotation_number` (unique, SQ:118); explicit null for NOT NULL columns on PATCH (quote customer_id/currency, bill vendor_id/currency); over-long strings with no max_length (quote quotation_number 50, po_so_number 100, payment_terms 30, accepted_by 255; bill bill_number/po_number 100, payment_terms 30); `limit < 0` on GET /quotations (RQ:54-58); convert due-date overflow (SQ:316).
12. **Quote numbering race**: unlocked scan-and-max (SQ:73-89), year from server-local `date.today()`; `isdigit` accepts Unicode digits (`int()` then raises). Same as PN 34.
13. **EXPIRED is dead**: no code sets it; `valid_until` is never enforced (accept/convert after expiry allowed). An EXPIRED quote (only via direct SQL) is convertible and voidable but not editable.
14. **Convert-to-invoice race and grammar**: no lock (SQ:274), two concurrent converts make two invoices; refusal reads `"cannot convert a INVOICED quotation to invoice"`; `payment_terms` (even unparseable) overrides `due_in_days` (SQ:285-287); invoice wht is the customer default only, computed on the stored quote amount; invoice number year is today's, not issue_date's.
15. **Convert from DRAFT/SENT skips acceptance**: accepted_at/accepted_by stay null. Accept and decline also skip SENT.
16. **Void quote is permissive** (SQ:243-251): re-void allowed; DECLINED/ACCEPTED voidable; no reason.
17. **Quote PATCH clears the number**: `quotation_number: null` (the form sends null when blank, FE/QuotationFormView.vue:218) sets it to null; `""` is stored as `""` on PATCH (create would generate one). A second `""` collides on the unique constraint -> 500. PATCH does not re-default `valid_until` or `issue_date` when nulled.
18. **Quote PATCH `line_items: []`** is accepted (SQ:176-183), leaving a 0-amount quote with no lines that convert later refuses with `"quotation has no line items to convert"`.
19. **Not found is 400 on quote writes** (SQ:150, :208, :222, :235, :246, :256, :276) but 404 on GET (RQ:76) and on every project/bill route. Same split as PN 39.
20. **Currency case**: projects and bills upper-case currency (SP:31, :55; SB:51, :91), quotes and invoices store it as sent (SQ:127). A lower-case quote converts into a lower-case invoice.
21. **Lists have no limit**: GET /projects, GET /bills, GET /projects/{id}/invoices; ORDER BY created_at DESC with no tie-break everywhere (RQ:58, RP:59, :176, RB:44). Line ties by position unordered (MQ:58-63, MB:54-59).
22. **Required-but-empty bodies**: accept (`AcceptQuotationRequest`), convert (`ConvertToInvoiceRequest`) and bill void (`dict`) need a JSON body even though every field is optional (missing body -> 422). The frontend sends `{accepted_by:null}` and `{}`.
23. **Mixed clocks**: last_sent_at/accepted_at are naive UTC (`datetime.utcnow()`, SQ:212, :226) while created_at is the DB session's CURRENT_TIMESTAMP; all serialized without offset.
24. **No project/customer consistency**: a quote, invoice or bill may point at another customer's project or a project in another currency (SQ:122-138, SB:46-61); only the frontend filters projects by customer.

---

## 6. Size estimate and suggested split

Routes in scope:

| Feature | Routes |
|---|---|
| Quotations | 10: POST, GET list, GET one, PATCH, DELETE, mark-sent, accept, decline, void, convert-to-invoice (pdf, send -> documents plan) |
| Projects | 7: POST, GET list, GET one, PATCH, DELETE, summary, invoices |
| Bills | 6: POST, GET list, GET one, PATCH, record-payment, void |
| **Total** | **23 routes, no jobs** |

Python tests to port: **5** (4 project service + 1 quote conversion); 34 MCP, 9 PDF and ~4 stats tests are [OUT]. Every route needs new Go tests.

Dependencies on GI (all exported): `ComputeTotals` + `LineAmount` (quote totals with whtType "NONE"), `PaymentTermsDays` + `AddDays` (convert due date; not `DueAfterTerms`, see section 2), `ResolveWHT` (customer default), `NextInvoiceNumber` (same tx, lock last), `MapWriteError` (invoice insert), `Get` + `InvoiceOut` (convert response), `ErrInvoicing` (map wht refusals to 400). **Gap**: GI exports only single-invoice `Get`; `GET /projects/{id}/invoices` needs an exported list loader (e.g. `invoices.ListByProject` or a batch `LoadMany`) so lines and expected_cash render identically.

Suggested tasks, each cohesive with its tests:
1. **Projects CRUD**: sqlc store, POST/GET/list/PATCH/DELETE, normalisation, error mapping (both unique names, customer FK, nulls), delete reference count (decide oddity 2: count quotations or 409 on the FK). Tests: the 4 service tests + list filters (status repeat, active, customer) + delete refusal. No GI dependency.
2. **Project summary + project invoices**: summary SQL (decide oddity 3: keep mixed-currency sums, `"0"` scale is an allowed difference), invoices list via the new GI list loader. Depends on GI and on the bills table existing (it does; SQL only).
3. **Quotations store + CRUD**: POST/GET/list/PATCH/DELETE, totals via `invoices.ComputeTotals`, `NextQuotationNumber` with advisory lock, exclude-unset PATCH (presence-aware decoding as in GI), 400/404 split. Depends on GI totals.
4. **Quotation lifecycle**: mark-sent, accept, decline, void with exact refusal sentences and the clock seam for last_sent_at/accepted_at.
5. **Convert-to-invoice**: one transaction (quote `FOR UPDATE` -> ResolveWHT -> withholding.Compute -> NextInvoiceNumber -> insert invoice + lines -> mark quote INVOICED), 201 InvoiceOut via `invoices.Get`. Tests: `test_quote_conversion_applies_customer_default` + field mapping, terms-vs-due_in_days, refusals, replay 400, concurrent convert makes one invoice. Depends on 3 and GI.
6. **Bills CRUD**: POST/GET/list/PATCH with bill-specific totals (HALF_EVEN, rounded-line subtotal, `position or idx`), PATCH status semantics (decide oddity 5/6), currency upper. No GI dependency.
7. **Bill record-payment + void**: exact sentences, `FOR UPDATE` on the bill (fixes the lost update), no ledger (PN 8), void note format. Depends on 6.

Tasks 1, 3 and 6 are independent. 2 needs 1 (+ GI list loader). 4 and 5 need 3. 7 needs 6.
