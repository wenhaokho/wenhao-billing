-- name: ListCustomers :many
SELECT * FROM customers WHERE (sqlc.narg(active)::boolean IS NULL OR active = sqlc.narg(active)) ORDER BY name ASC;

-- name: GetCustomer :one
SELECT * FROM customers WHERE customer_id = $1;

-- name: DeactivateCustomer :one
UPDATE customers SET active = false WHERE customer_id = $1 RETURNING *;

-- name: CreateCustomer :one
INSERT INTO customers (customer_id, name) VALUES (gen_random_uuid(), sqlc.arg(name)) RETURNING *;

-- name: ApplyCustomerCreate :one
UPDATE customers AS c SET matching_aliases = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).matching_aliases, active = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).active, contact_email = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).contact_email, contact_phone = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).contact_phone, billing_address = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).billing_address, contact_first_name = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).contact_first_name, contact_last_name = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).contact_last_name, contact_phone_2 = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).contact_phone_2, account_number = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).account_number, website = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).website, notes = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).notes, default_currency = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).default_currency, billing_address1 = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).billing_address1, billing_address2 = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).billing_address2, billing_country = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).billing_country, billing_state = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).billing_state, billing_city = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).billing_city, billing_postal_code = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).billing_postal_code, ship_to_name = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).ship_to_name, shipping_address1 = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).shipping_address1, shipping_address2 = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).shipping_address2, shipping_country = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).shipping_country, shipping_state = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).shipping_state, shipping_city = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).shipping_city, shipping_postal_code = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).shipping_postal_code, shipping_phone = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).shipping_phone, shipping_delivery_instructions = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).shipping_delivery_instructions, quotation_cc_emails = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).quotation_cc_emails, billing_cc_emails = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).billing_cc_emails, default_wht_type = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).default_wht_type, default_wht_rate = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).default_wht_rate, default_wht_gross_up = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).default_wht_gross_up WHERE c.customer_id = sqlc.arg(customer_id) RETURNING *;


-- name: LockCustomer :one
SELECT * FROM customers WHERE customer_id = $1 FOR UPDATE;

-- name: ApplyCustomerUpdate :one
UPDATE customers AS c SET name = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).name, matching_aliases = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).matching_aliases, active = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).active, contact_email = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).contact_email, contact_phone = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).contact_phone, billing_address = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).billing_address, contact_first_name = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).contact_first_name, contact_last_name = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).contact_last_name, contact_phone_2 = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).contact_phone_2, account_number = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).account_number, website = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).website, notes = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).notes, default_currency = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).default_currency, billing_address1 = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).billing_address1, billing_address2 = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).billing_address2, billing_country = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).billing_country, billing_state = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).billing_state, billing_city = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).billing_city, billing_postal_code = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).billing_postal_code, ship_to_name = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).ship_to_name, shipping_address1 = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).shipping_address1, shipping_address2 = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).shipping_address2, shipping_country = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).shipping_country, shipping_state = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).shipping_state, shipping_city = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).shipping_city, shipping_postal_code = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).shipping_postal_code, shipping_phone = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).shipping_phone, shipping_delivery_instructions = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).shipping_delivery_instructions, quotation_cc_emails = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).quotation_cc_emails, billing_cc_emails = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).billing_cc_emails, default_wht_type = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).default_wht_type, default_wht_rate = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).default_wht_rate, default_wht_gross_up = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).default_wht_gross_up WHERE c.customer_id = sqlc.arg(customer_id) RETURNING *;

-- name: CustomerBalances :many
SELECT customer_id, currency, sum(balance_due)::numeric AS balance,
       sum(CASE WHEN due_date < sqlc.arg(today)::date THEN balance_due ELSE 0 END)::numeric AS overdue
FROM invoices
WHERE status IN ('OPEN', 'SENT', 'PARTIAL') AND is_template = false
  AND balance_due > 0 AND customer_id IS NOT NULL
GROUP BY customer_id, currency;

-- name: CustomerInvoices :many
SELECT * FROM invoices WHERE customer_id = $1 ORDER BY issue_date DESC, created_at DESC;

-- name: CustomerInvoiceLines :many
SELECT l.* FROM invoice_line_items l JOIN invoices i ON i.invoice_id = l.invoice_id WHERE i.customer_id = $1 ORDER BY l.position;

-- name: CustomerRecentPayments :many
SELECT currency, sum(amount)::numeric AS amount FROM payments WHERE customer_id = $1 AND status = 'CLEARED' AND payment_date >= $2 GROUP BY currency ORDER BY currency;

-- name: CustomerOpenInvoices :many
SELECT * FROM invoices WHERE customer_id = $1 AND status IN ('DRAFT', 'OPEN', 'SENT', 'PARTIAL') ORDER BY due_date ASC;
