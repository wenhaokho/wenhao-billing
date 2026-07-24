# Per-Currency Payment Accounts — Design

**Date:** 2026-07-24
**Status:** Approved for planning

## Problem

Money is received in three currencies (SGD, IDR, USD), each with **different bank
payment details**. Today `BusinessProfile` is a singleton with a single
`payment_instructions` text field, and the invoice/quotation PDF prints it
verbatim regardless of the document's currency. An SGD invoice and an IDR
invoice therefore show the *same* account, which is wrong.

Invoices already carry a fixed `currency` (`String(3)`, set at creation and never
auto-converted). The PDF should select the payment account that matches the
document's currency, falling back to a designated default account.

## Goals

- Store bank details per currency.
- Invoice/quotation PDF renders the account matching the document currency.
- Fall back to a default account when no currency-specific match exists.
- Admin can manage accounts from Account settings.

## Non-goals

- Exposing payment accounts via MCP (`get_business_profile` already deliberately
  excludes `payment_instructions`; unchanged here).
- Multi-tenant support (single-tenant; `business_profile.id` is always 1).
- Currency conversion of any kind.

## Data model

New table `payment_account`:

| column | type | notes |
|---|---|---|
| `id` | int PK | autoincrement |
| `business_profile_id` | int FK → `business_profile.id` | always 1; enables `BusinessProfile.payment_accounts` relationship |
| `currency` | `String(3)` not null | e.g. `SGD` / `IDR` / `USD` |
| `instructions` | Text not null | free-text bank details block (same content shape as today's `payment_instructions`) |
| `is_default` | Boolean not null default false | fallback account |
| `updated_at` | timestamp | server default + onupdate, matching `BusinessProfile` |

Constraints / indexes:

- Unique `(business_profile_id, currency)` — one account per currency.
- **Partial unique index** on `business_profile_id` where `is_default IS TRUE` —
  at most one default account.

SQLAlchemy: add `BusinessProfile.payment_accounts` relationship
(`cascade="all, delete-orphan"`, `lazy="selectin"`) so the PDF context can read
accounts without a separate session.

## PDF selection logic

In `app/services/pdf/context.py::_common`, replace the current
`"payment_instructions": getattr(business, "payment_instructions", None)` with a
resolver over `business.payment_accounts`:

1. Row where `currency == doc.currency` → use its `instructions`.
2. Else the row where `is_default is True` → use its `instructions`.
3. Else `None`.

The template (`document.html.j2`) already wraps the block in
`{% if payment_instructions %}` and renders `<div></div>` otherwise — **no
template change required**. The context key stays named `payment_instructions`
so the template and downstream stay stable.

Add a small pure helper (e.g. `resolve_payment_instructions(accounts, currency)`)
so the selection is unit-testable in isolation.

## API

Extend the existing business-profile router (`/business-profile`):

- `GET  /business-profile/payment-accounts` → `list[PaymentAccountOut]`
- `PUT  /business-profile/payment-accounts` → **full replace** of the account set

`PUT` accepts a list of `{currency, instructions, is_default}` and:

- validates currencies are unique within the payload,
- validates **at most one** `is_default = true`,
- replaces the stored set for `business_profile_id = 1` (delete-then-insert is
  acceptable for a singleton settings table),
- returns the new list.

Rationale for bulk-replace over per-row CRUD: the Account settings page uses a
single Save action for the whole company form; a replace endpoint matches that
UX and keeps validation (uniqueness, single default) in one atomic request.

Schemas (`app/schemas/business_profile.py`):

```python
class PaymentAccountIn(BaseModel):
    currency: str            # 3-letter, validated against allowed set
    instructions: str
    is_default: bool = False

class PaymentAccountOut(BaseModel):
    model_config = ConfigDict(from_attributes=True)
    id: int
    currency: str
    instructions: str
    is_default: bool
    updated_at: datetime
```

Both endpoints require `current_admin`, consistent with the existing profile
routes.

## Account settings UI (`frontend/src/views/AccountView.vue`)

Replace the single "Payment instructions" textarea with a **Payment accounts**
editor:

- One row per account: currency dropdown + instructions textarea + "Default"
  radio (mutually exclusive across rows) + remove button.
- Currency dropdown options: **SGD, IDR, USD** (constrained — PDF matching is an
  exact string compare against `invoice.currency`, so a free-text typo would
  silently fall back to the default).
- "Add account" button (disabled once all allowed currencies are used).
- Loaded via `GET /business-profile/payment-accounts` on mount.
- Persisted by the existing Save button, which issues `PUT /business-profile`
  (unchanged fields) **and** `PUT /business-profile/payment-accounts`.

Client-side guard mirrors the API: block save if two rows share a currency or if
more than one is marked default.

## Migration — `0022_payment_accounts`

(revision id < 32 chars per Alembic constraint)

1. Create `payment_account` table with constraints/indexes above.
2. Data migration: if `business_profile.payment_instructions` is non-empty,
   insert one row for `business_profile_id = 1` with `currency = 'IDR'`,
   `is_default = true`, `instructions = <existing value>`. (The seeded account is
   OCBC NISP / SWIFT `NISPIDJA`, an Indonesian bank → IDR.)
3. Drop `business_profile.payment_instructions` column.

Downgrade: re-add the column, copy the default (or IDR) account's `instructions`
back, drop the table.

## Seed (`backend/scripts/seed_business_profile.py`)

- Remove `payment_instructions` from the `PROFILE` dict.
- After seeding the profile, seed a single **IDR default** `payment_account` from
  the current OCBC value (idempotent — skip if any account already exists).
- SGD and USD accounts are left for the user to fill via the settings UI.

## Model/consumer cleanup

- Remove `payment_instructions` from `BusinessProfile` model,
  `BusinessProfileUpdate`, and `BusinessProfileOut`.
- Remove `payment_instructions` from the `AccountView.vue` company form type and
  save payload.
- No change to `read_misc.py` (`_PROFILE_FIELDS` never included it).

## Tests

- `test_pdf_context`: currency match returns that account; no match returns the
  default; empty set returns `None` (block omitted).
- New API test: `PUT /business-profile/payment-accounts` happy path; rejects
  duplicate currency; rejects >1 default; `GET` reflects the replace.

## Risks / assumptions

- **Assumption:** the existing OCBC account is IDR (confirmed by SWIFT
  `NISPIDJA`). If it were meant for another currency, the data migration value
  would need adjusting.
- Dropping `payment_instructions` is destructive; the data migration preserves
  the value into the IDR account first. Downgrade path restores it.
