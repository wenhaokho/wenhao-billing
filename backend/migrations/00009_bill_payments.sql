-- +goose Up
-- Go's own (plan 12, PORT-NOTES 165): a bill payment is a row, posted Dr
-- 2000 / Cr 1000 (with realised FX) and reversible. Python kept no row: a
-- payment only lowered the bill's balance_due.
CREATE TABLE bill_payments (
    bill_payment_id uuid PRIMARY KEY,
    bill_id uuid NOT NULL REFERENCES bills (bill_id),
    amount numeric(19,4) NOT NULL CHECK (amount > 0),
    currency varchar(3) NOT NULL,
    payment_date date NOT NULL,
    payer_name varchar(255),
    reference varchar(255),
    notes text,
    status varchar(20) NOT NULL DEFAULT 'CLEARED' CHECK (status IN ('CLEARED', 'REVERSED')),
    source varchar(20) NOT NULL DEFAULT 'MANUAL' CHECK (source IN ('MANUAL', 'STATUS_UPDATE', 'LEGACY_BACKPOST')),
    created_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
    reversed_at timestamp,
    reversal_reason text
);
CREATE INDEX ix_bill_payments_bill_id ON bill_payments (bill_id);

-- +goose Down
DROP TABLE bill_payments;
