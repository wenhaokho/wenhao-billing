-- +goose Up
-- Go's own (UI revamp part 2, "Send reminder"): every payment reminder sent,
-- by the REST route or the MCP tool, is a row. The rows drive the 24 h
-- per-customer repeat guard, the invoice's reminder history and the
-- dashboard's last_reminder_at. amount is the cash the reminder asked for
-- (balance_due less the unbooked withholding), in the invoice's currency.
CREATE TABLE invoice_reminders (
    reminder_id uuid PRIMARY KEY,
    invoice_id uuid NOT NULL REFERENCES invoices (invoice_id),
    sent_at timestamptz NOT NULL,
    sent_by_user_id uuid REFERENCES users (user_id),
    channel text NOT NULL CHECK (channel IN ('REST', 'MCP')),
    recipient text NOT NULL,
    amount numeric(19,4) NOT NULL,
    currency varchar(3) NOT NULL
);
CREATE INDEX ix_invoice_reminders_invoice_id ON invoice_reminders (invoice_id);
CREATE INDEX ix_invoice_reminders_sent_at ON invoice_reminders (sent_at);

-- +goose Down
DROP TABLE invoice_reminders;
