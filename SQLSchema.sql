-- Chart of Accounts (Standalone)
CREATE TABLE chart_of_accounts (
    account_id SERIAL PRIMARY KEY,
    code VARCHAR(10) NOT NULL,
    name VARCHAR(100) NOT NULL,
    type VARCHAR(50) NOT NULL -- Asset, Liability, Equity, Income, Expense, COGS
);

-- Customer Entity
CREATE TABLE customers (
    customer_id UUID PRIMARY KEY,
    name VARCHAR(255) NOT NULL
);

-- Invoicing (Milestone, Recurring, Usage-Based)
CREATE TABLE invoices (
    invoice_id UUID PRIMARY KEY,
    customer_id UUID REFERENCES customers(customer_id),
    invoice_type VARCHAR(20) NOT NULL, -- MILESTONE, RECURRING, USAGE
    currency VARCHAR(3) NOT NULL,
    amount DECIMAL(19, 4) NOT NULL,
    balance_due DECIMAL(19, 4) NOT NULL,
    status VARCHAR(20) NOT NULL, -- DRAFT, SENT, PAID, PARTIAL
    billing_cycle_ref JSONB, -- Stores cut-off dates or cycle triggers
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Payment Intake (Webhook/Email Proof)
CREATE TABLE payments (
    payment_id UUID PRIMARY KEY,
    invoice_id UUID REFERENCES invoices(invoice_id),
    amount DECIMAL(19, 4) NOT NULL,
    currency VARCHAR(3) NOT NULL,
    intake_source VARCHAR(50), -- WEBHOOK, EMAIL_PARSER, MANUAL
    status VARCHAR(20) NOT NULL, -- CLEARED, PENDING_MANUAL_REVIEW, FLAGGED
    adjustment_type VARCHAR(20), -- NONE, PARTIAL_PAY, CREDIT_ON_ACCOUNT, BANK_FEE
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);