-- +goose Up
CREATE TABLE hosting_restore_intents (
    invoice_id uuid NOT NULL REFERENCES invoices(invoice_id),
    item_id uuid NOT NULL REFERENCES items(item_id),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (invoice_id, item_id)
);

-- +goose Down
DROP TABLE hosting_restore_intents;
