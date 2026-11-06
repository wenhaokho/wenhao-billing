-- +goose Up
CREATE TABLE recurring_draft_events (
    invoice_id uuid PRIMARY KEY,
    cycle_key text NOT NULL,
    invoice_number text NOT NULL,
    customer_name text NOT NULL,
    amount numeric(19,4) NOT NULL,
    currency text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE recurring_draft_deliveries (
    invoice_id uuid NOT NULL REFERENCES recurring_draft_events(invoice_id),
    user_id uuid NOT NULL,
    email text NOT NULL,
    attempts integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    lease_token uuid,
    lease_until timestamptz,
    delivered_at timestamptz,
    cancelled_at timestamptz,
    last_error text,
    PRIMARY KEY (invoice_id, user_id)
);
CREATE INDEX recurring_draft_delivery_ready ON recurring_draft_deliveries (next_attempt_at) WHERE delivered_at IS NULL AND cancelled_at IS NULL;

-- +goose Down
DROP TABLE recurring_draft_deliveries;
DROP TABLE recurring_draft_events;
