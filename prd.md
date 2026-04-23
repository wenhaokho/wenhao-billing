PRD: Software Agency Accounting System (Phase 1)
1. Core Architecture
Data Integrity: All financial fields must use DECIMAL(19, 4).

Multi-Tenancy: Every record must be keyed by business_id to ensure absolute isolation from other ventures.

Audit Trail: Every transaction (creation, edit, deletion, AI-action) must be logged with user_id and timestamp.

2. Customer & Invoicing Module
Customer Entity: Stores BillingConfig for each customer.

Invoice Hierarchy: Invoices are independent of the Customer level, allowing for granular billing.

Milestone Invoices: Manual generation for project deliverables.

Recurring Invoices: Automated generation based on cycle_trigger_date.

Usage-Based Invoices: Triggered via "Usage Lock" at a client-specific cut_off_day.

Currency Constraints: Currency is set at the time of invoice creation. The system rejects any incoming payment proof or webhook that does not match the invoice's set currency.

3. Financial Intake & Reconciliation (AI-Assisted)
Ingestion Layer:

Webhook Listener: Receives real-time payment data from gateways.

Email Parser: OCR-based scanning of payment proof attachments (PDF/JPG).

AI Matching Engine:

Logic: Matches incoming payments to invoices using Amount + Currency + Payer + Date.

Threshold: Only Confidence Score >= 95% triggers auto-reconciliation.

Strict "Safe-Stop": Any amount mismatch or ambiguity must trigger a PENDING_MANUAL_REVIEW status.

Resolution Interface: Dashboard for manual intervention to handle Partial Payments, Credits, or Bank Fees.

4. Chart of Accounts (Agency Baseline)
Assets (1xxx): Operating Bank (multi-currency capable), Accounts Receivable.

Liabilities (2xxx): Accounts Payable, Sales Tax Payable, Unbilled AR (Accrued Revenue for usage-based).

Income (4xxx): Fixed-Fee, Support/Maintenance, Usage-Based (AI Tokens).

COGS (5xxx): External Contractor Fees, Cloud Infrastructure, AI Inference Costs.

5. Dashboard Requirements
"Awaiting Finalization" Queue: Centralized hub for manual approval of all generated invoices.

Payment Intake Widget: Consolidated view of auto-reconciled transactions vs. those requiring manual attention.

Customer Billing Summary: Consolidated view of all active agreements (Milestone/Recurring/Usage) per customer.

6. Development Rules for Claude
Strict Isolation: Code must enforce business_id filters on all SELECT/INSERT/UPDATE queries.

No Automatic "Fixes": In the event of an amount/currency mismatch, the system must not attempt to guess. It must set status to PENDING_MANUAL_REVIEW and alert the admin.

Manual Override: Ensure every automated action (e.g., AI marking an invoice as paid) can be manually reversed or edited by the admin.

No Banking API: All reconciliation for this phase must be driven by CSV/OFX/QBO uploads and Webhook/Email ingestion.

7. Quotation Module (Phase 1)
Quotation Entity: Independent document that precedes an invoice. Fields mirror Invoice (customer, project, currency, line items, amount, notes) plus quotation-specific fields: quotation_number (unique), issue_date, valid_until, status.

Status Machine: DRAFT -> SENT -> ACCEPTED | DECLINED | EXPIRED -> INVOICED | VOID. Admin-only transitions. Convert-to-invoice is admin-only.

Send by Email: Dedicated SendQuotationDialog generates the quotation PDF and attaches it, distinct from invoice email flow.

Audit: Every status transition and conversion must be logged (payment-centric ReconciliationLog is not reused; a generic audit log is pending decision).

Dashboard surface: Open quotations count and pipeline by currency (SENT + ACCEPTED) are computed server-side in /stats/dashboard and rendered as a tile on the Payment Intake widget.

8. UX Parity with Wave Accounting (Phase 1.1)
Reference: Wave's Estimate / Invoice / Customer / Products & Services screens are the baseline for information architecture. We match features and workflow, not pixels. PrimeVue styling is retained.

Planning artifact: C:\Users\wenha\.claude\plans\frolicking-juggling-treasure.md captures per-screenshot observations, in-scope items, out-of-scope items, and execution order.

In-scope changes (this pass):
- Always-visible Preview button on Quotation and Invoice forms (disabled when unsaved).
- "On Receipt" helper text under invoice Payment-due field where applicable.
- Invoice list summary tiles: Overdue, Due within 30 days, Average days to get paid (new /stats/invoices-summary endpoint).
- Invoice list tabs: Unpaid / Draft / All.
- Inline "Record payment" action on unpaid invoice rows.
- Customers list: Balance / Overdue column with red highlight when overdue.
- Customer form: structured Primary-contact block (first/last/email/phone), separate Billing vs Shipping address blocks with delivery instructions, account_number and website fields. Requires schema migration.

Out of scope (future work):
- Editable per-document business-identity block, edit-columns on line-item tables, collapsible Footer on forms, split "Save and continue" button.
- Recurring Invoices module (new module, separate planning round).
- CSV import for customers.
- Saved payment cards column (no card tokenization in this product — permanent out of scope).
- Products & Services catalog (new module with sell/buy dual-role items, line-item picker integration).
- Sales-tax support (requires a tax-rate module not in Phase 1).
- Multi-contact ("Add another contact") — requires a customer_contacts child table.