-- +goose Up
-- The expense account of a bill line with neither its own nor its item's
-- expense account (plan 12, PORT-NOTES 164). The accounts bills and invoices
-- post to at issue are in every Alembic-built database already; inserting
-- them here only guards a database that lost one.
INSERT INTO chart_of_accounts (code, name, type) VALUES ('6000', 'Uncategorised Expense', 'EXPENSE') ON CONFLICT (code) DO NOTHING;
INSERT INTO chart_of_accounts (code, name, type) VALUES ('2000', 'Accounts Payable', 'LIABILITY') ON CONFLICT (code) DO NOTHING;
INSERT INTO chart_of_accounts (code, name, type) VALUES ('4000', 'Fixed-Fee Revenue', 'INCOME') ON CONFLICT (code) DO NOTHING;
INSERT INTO chart_of_accounts (code, name, type) VALUES ('4100', 'Support/Maintenance Revenue', 'INCOME') ON CONFLICT (code) DO NOTHING;
INSERT INTO chart_of_accounts (code, name, type) VALUES ('4200', 'Usage-Based Revenue (AI Tokens)', 'INCOME') ON CONFLICT (code) DO NOTHING;

-- +goose Down
-- 6000 goes only while nothing references it; the guarded accounts stay.
DELETE FROM chart_of_accounts a
 WHERE a.code = '6000'
   AND NOT EXISTS (SELECT 1 FROM journal_lines l WHERE l.account_id = a.account_id)
   AND NOT EXISTS (SELECT 1 FROM bill_line_items b WHERE b.expense_account_id = a.account_id)
   AND NOT EXISTS (SELECT 1 FROM items i WHERE i.expense_account_id = a.account_id OR i.revenue_account_id = a.account_id);
