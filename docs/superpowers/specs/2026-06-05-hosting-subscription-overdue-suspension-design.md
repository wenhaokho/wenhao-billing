# Hosting Subscription Overdue Suspension Design

## Goal

Add prepaid hosting subscriptions that:

- invoice every `x` months using the existing recurring invoice engine
- treat each generated invoice as prepaid coverage for the next `x`-month period
- suspend a hosting target in Cloudflare when the invoice is overdue past a grace period
- restore service automatically when the overdue invoice is fully paid

This design intentionally uses a hosting-specific operational model on top of the existing accounting entities rather than trying to infer hosting state from generic invoices alone.

## Chosen Approach

Use an explicit `hosting_subscriptions` table as the operational source of truth.

Why this approach:

- one subscription maps exactly to one billable hosting line item
- one subscription maps exactly to one Cloudflare target
- invoices remain accounting documents
- Cloudflare actions are driven by an explicit subscription record instead of fragile inference from notes, item names, or invoice type

Rejected alternatives:

- storing hosting metadata in invoice-line-item JSON: simpler schema, but weak querying, weak validation, and poor lifecycle tracking
- attaching hosting behavior to projects: too indirect for the approved rule of one subscription per invoice line item

## Domain Rules

### Subscription identity

- one hosting subscription equals one billable invoice line item
- one hosting subscription controls exactly one Cloudflare target

### Billing cadence

- subscriptions are prepaid bundles
- `bundle_months` is the billing cadence and coverage length
- recurring schedule always uses:
  - `frequency = MONTHLY`
  - `interval = bundle_months`

Example:

- `bundle_months = 3`
- anchor date `2026-06-01`
- generated invoice covers `2026-06-01` through `2026-08-31`

### Due-date behavior

- generated invoices use existing payment terms logic
- suspension trigger is based on `due_date + grace_days`
- default recommendation is `grace_days = 3`, but the field is stored per subscription

### Restore behavior

- restoration is automatic
- restore only when the exact overdue invoice linked to the current service period is fully paid

## Data Model

### New table: `hosting_subscriptions`

- `subscription_id UUID PRIMARY KEY`
- `customer_id UUID NOT NULL REFERENCES customers(customer_id)`
- `project_id UUID NULL REFERENCES projects(project_id)`
- `template_invoice_id UUID NULL REFERENCES invoices(invoice_id)`
- `item_id UUID NULL REFERENCES items(item_id)`
- `service_name VARCHAR(255) NOT NULL`
- `domain_name VARCHAR(255) NOT NULL`
- `currency VARCHAR(3) NOT NULL`
- `bundle_months INTEGER NOT NULL`
- `payment_terms VARCHAR(30) NOT NULL`
- `billing_anchor_date DATE NOT NULL`
- `grace_days INTEGER NOT NULL DEFAULT 3`
- `suspension_enabled BOOLEAN NOT NULL DEFAULT true`
- `status VARCHAR(20) NOT NULL`
- `last_invoice_id UUID NULL REFERENCES invoices(invoice_id)`
- `last_paid_at TIMESTAMP NULL`
- `created_at TIMESTAMP NOT NULL`
- `updated_at TIMESTAMP NOT NULL`

`status` values:

- `ACTIVE`
- `SUSPEND_PENDING`
- `SUSPENDED`
- `CANCELLED`

Notes:

- `template_invoice_id` links the subscription to the recurring template that drives invoice generation
- `item_id` keeps the subscription tied to the billable catalog item
- `service_name` is denormalized display text so operational screens do not depend on item renames

### New table: `cloudflare_targets`

- `target_id UUID PRIMARY KEY`
- `subscription_id UUID NOT NULL UNIQUE REFERENCES hosting_subscriptions(subscription_id)`
- `zone_id VARCHAR(100) NOT NULL`
- `record_id VARCHAR(100) NOT NULL`
- `record_name VARCHAR(255) NOT NULL`
- `record_type VARCHAR(20) NOT NULL`
- `live_content TEXT NOT NULL`
- `maintenance_content TEXT NOT NULL`
- `proxied BOOLEAN NOT NULL DEFAULT true`
- `provider_status VARCHAR(20) NOT NULL DEFAULT 'ACTIVE'`
- `last_action_at TIMESTAMP NULL`
- `last_error TEXT NULL`
- `created_at TIMESTAMP NOT NULL`
- `updated_at TIMESTAMP NOT NULL`

`provider_status` values:

- `ACTIVE`
- `SUSPENDED`
- `ERROR`

This table is intentionally one-to-one with `hosting_subscriptions` because MVP requires exactly one Cloudflare target per subscription.

### Extend `invoices`

Add:

- `subscription_id UUID NULL REFERENCES hosting_subscriptions(subscription_id)`
- `coverage_start DATE NULL`
- `coverage_end DATE NULL`

Purpose:

- ties a generated prepaid invoice back to the operational subscription
- stores the exact service period covered by that invoice

## Billing Flow

### Subscription creation

Admin creates a hosting subscription with:

- customer
- optional project
- catalog item
- service name
- domain name
- currency
- bundle months
- payment terms
- billing anchor date
- grace days
- suspension enabled flag
- Cloudflare target details

The system also creates or links a recurring invoice template.

### Recurring generation

Continue using the existing recurring invoice engine, but enrich generated invoices with hosting metadata.

When a cycle is due:

1. compute the cycle start date from the recurring schedule
2. set `coverage_start` to the cycle start date
3. set `coverage_end` to the day before the next cycle start
4. create the invoice with:
   - `subscription_id`
   - `coverage_start`
   - `coverage_end`
   - normal `due_date` from payment terms
5. set `last_invoice_id` on the subscription

Generated invoice line description includes the covered period so the document is clear to the customer.

### Payment effect

When the generated hosting invoice is fully paid:

- accounting behavior stays unchanged
- `hosting_subscriptions.last_paid_at` updates
- if the subscription was suspended because of that invoice, restore it automatically

## Suspension and Restore Flow

### Enforcement worker

Add a new daily Celery beat task, separate from recurring generation.

The task scans subscriptions where:

- `suspension_enabled = true`
- `status != CANCELLED`

### Suspend condition

Suspend when all are true:

- the linked invoice is `SENT` or `PARTIAL`
- `balance_due > 0`
- `due_date + grace_days < today`
- subscription status is not already `SUSPENDED`

### Suspend sequence

1. set subscription status to `SUSPEND_PENDING`
2. call Cloudflare using the subscription's target
3. swap the target content from `live_content` to `maintenance_content`
4. if successful:
   - set subscription status to `SUSPENDED`
   - set target `provider_status` to `SUSPENDED`
   - update `last_action_at`
   - clear `last_error`
5. if failed:
   - leave subscription in `SUSPEND_PENDING`
   - set target `provider_status` to `ERROR`
   - store `last_error`

### Restore condition

Restore when all are true:

- subscription status is `SUSPENDED`
- the overdue linked invoice becomes fully paid

### Restore sequence

1. call Cloudflare
2. restore the target to `live_content`
3. set subscription status to `ACTIVE`
4. set target `provider_status` to `ACTIVE`
5. update `last_action_at`
6. clear `last_error`

## Backend Structure

### New modules

- `app/models/hosting_subscription.py`
- `app/models/cloudflare_target.py`
- `app/services/hosting_subscriptions.py`
- `app/services/cloudflare.py`
- `app/workers/tasks/hosting_enforcement.py`

### Existing modules to extend

- `app/models/invoice.py`
- `app/services/invoicing.py`
- `app/workers/beat_schedule.py`
- invoice schemas and routers for the new invoice fields

### Service responsibilities

`hosting_subscriptions.py`

- create/update/cancel subscription
- derive recurring template schedule from `bundle_months`
- compute coverage windows
- resolve current enforceable invoice for a subscription
- coordinate suspend and restore transitions

`cloudflare.py`

- `suspend_target(target)`
- `restore_target(target)`

This module hides HTTP details and exposes a narrow interface to the rest of the app.

## UI Scope

Add a hosting subscription management surface with fields for:

- customer
- project
- item
- service name
- domain name
- bundle months
- payment terms
- billing anchor date
- grace days
- suspension enabled
- Cloudflare target data

Invoice detail shows:

- coverage period
- linked hosting subscription

MVP does not require a rich suspension dashboard beyond basic visibility.

## Error Handling

- recurring generation must not create duplicate invoices for the same cycle
- suspension must be idempotent
- restore must be idempotent
- Cloudflare failures must never mark a subscription `ACTIVE` or `SUSPENDED` incorrectly
- failed provider calls must retain enough error detail for manual follow-up

## Testing

Unit tests:

- coverage window calculation for monthly bundles
- grace-period overdue detection
- suspend/restore idempotency

Integration tests:

- recurring template generates invoice with `subscription_id`, `coverage_start`, `coverage_end`
- overdue hosting invoice suspends the correct subscription
- fully paid overdue hosting invoice restores the correct subscription
- provider failure leaves safe intermediate state

## Out of Scope

- multiple Cloudflare targets per subscription
- manual review queues for restore
- customer warning notifications before suspension
- provider abstraction beyond Cloudflare
- suspension rules based on partial payments or non-hosting bundled invoices
- redesign of the app-wide audit system

## Implementation Notes

- do not use `invoice_type` to identify hosting behavior; it remains `MILESTONE`, `RECURRING`, or `USAGE`
- hosting is an operational concern attached to a subscription, not a new invoice type
- subscription linkage must be explicit to keep overdue enforcement deterministic
