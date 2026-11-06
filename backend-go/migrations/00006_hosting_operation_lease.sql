-- +goose Up
CREATE TABLE hosting_operation_leases (
    item_id uuid PRIMARY KEY REFERENCES items(item_id) ON DELETE CASCADE,
    operation text NOT NULL CHECK (operation IN ('SUSPEND', 'RESTORE')),
    lease_token uuid NOT NULL,
    lease_until timestamptz NOT NULL,
    repair_required boolean NOT NULL DEFAULT false,
    manual_review_required boolean NOT NULL DEFAULT false,
    target_fingerprint text
);
-- +goose Down
DROP TABLE hosting_operation_leases;
