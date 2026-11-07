-- name: ListCustomers :many
SELECT * FROM customers WHERE business_id=sqlc.arg(business_id)::uuid AND (sqlc.narg(active)::boolean IS NULL OR active = sqlc.narg(active)) ORDER BY name ASC;

-- name: GetCustomer :one
SELECT * FROM customers WHERE business_id=sqlc.arg(business_id)::uuid AND customer_id = $1;

-- name: DeactivateCustomer :one
UPDATE customers SET active = false WHERE business_id=sqlc.arg(business_id)::uuid AND customer_id = $1 RETURNING *;

-- name: CreateCustomer :one
INSERT INTO customers (business_id, customer_id, name) VALUES (sqlc.arg(business_id)::uuid, gen_random_uuid(), sqlc.arg(name)) RETURNING *;

-- name: ApplyCustomerCreate :one
UPDATE customers AS c SET matching_aliases = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).matching_aliases, active = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).active, contact_email = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).contact_email, contact_phone = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).contact_phone, billing_address = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).billing_address, contact_first_name = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).contact_first_name, contact_last_name = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).contact_last_name, contact_phone_2 = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).contact_phone_2, account_number = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).account_number, website = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).website, notes = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).notes, default_currency = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).default_currency, billing_address1 = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).billing_address1, billing_address2 = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).billing_address2, billing_country = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).billing_country, billing_state = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).billing_state, billing_city = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).billing_city, billing_postal_code = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).billing_postal_code, ship_to_name = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).ship_to_name, shipping_address1 = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).shipping_address1, shipping_address2 = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).shipping_address2, shipping_country = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).shipping_country, shipping_state = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).shipping_state, shipping_city = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).shipping_city, shipping_postal_code = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).shipping_postal_code, shipping_phone = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).shipping_phone, shipping_delivery_instructions = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).shipping_delivery_instructions, quotation_cc_emails = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).quotation_cc_emails, billing_cc_emails = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).billing_cc_emails, default_wht_type = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).default_wht_type, default_wht_rate = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).default_wht_rate, default_wht_gross_up = (jsonb_populate_record(c, jsonb_strip_nulls(sqlc.arg(data)::jsonb))).default_wht_gross_up WHERE c.business_id=sqlc.arg(business_id)::uuid AND c.customer_id = sqlc.arg(customer_id) RETURNING *;


-- name: LockCustomer :one
SELECT * FROM customers WHERE business_id=sqlc.arg(business_id)::uuid AND customer_id = $1 FOR UPDATE;

-- name: ApplyCustomerUpdate :one
UPDATE customers AS c SET name = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).name, matching_aliases = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).matching_aliases, active = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).active, contact_email = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).contact_email, contact_phone = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).contact_phone, billing_address = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).billing_address, contact_first_name = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).contact_first_name, contact_last_name = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).contact_last_name, contact_phone_2 = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).contact_phone_2, account_number = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).account_number, website = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).website, notes = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).notes, default_currency = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).default_currency, billing_address1 = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).billing_address1, billing_address2 = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).billing_address2, billing_country = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).billing_country, billing_state = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).billing_state, billing_city = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).billing_city, billing_postal_code = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).billing_postal_code, ship_to_name = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).ship_to_name, shipping_address1 = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).shipping_address1, shipping_address2 = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).shipping_address2, shipping_country = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).shipping_country, shipping_state = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).shipping_state, shipping_city = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).shipping_city, shipping_postal_code = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).shipping_postal_code, shipping_phone = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).shipping_phone, shipping_delivery_instructions = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).shipping_delivery_instructions, quotation_cc_emails = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).quotation_cc_emails, billing_cc_emails = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).billing_cc_emails, default_wht_type = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).default_wht_type, default_wht_rate = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).default_wht_rate, default_wht_gross_up = (jsonb_populate_record(c, sqlc.arg(data)::jsonb)).default_wht_gross_up WHERE c.business_id=sqlc.arg(business_id)::uuid AND c.customer_id = sqlc.arg(customer_id) RETURNING *;

-- name: CustomerBalances :many
SELECT i.customer_id, i.currency, sum(balance_due)::numeric AS balance,
       sum(CASE WHEN due_date < sqlc.arg(today)::date THEN balance_due ELSE 0 END)::numeric AS overdue
FROM invoices i
JOIN customers c ON c.customer_id = i.customer_id AND c.business_id = i.business_id
WHERE i.business_id=sqlc.arg(business_id)::uuid AND status IN ('OPEN', 'SENT', 'PARTIAL') AND is_template = false
  AND balance_due > 0 AND i.customer_id IS NOT NULL
GROUP BY i.customer_id, i.currency;

-- name: CustomerInvoices :many
SELECT * FROM invoices WHERE business_id=sqlc.arg(business_id)::uuid AND customer_id = $1 ORDER BY issue_date DESC, created_at DESC;

-- name: CustomerInvoiceLines :many
SELECT l.* FROM invoice_line_items l JOIN invoices i ON i.invoice_id = l.invoice_id AND i.business_id=l.business_id WHERE i.business_id=sqlc.arg(business_id)::uuid AND i.customer_id = $1 ORDER BY l.position;

-- name: CustomerRecentPayments :many
SELECT p.currency, sum(p.amount)::numeric AS amount
FROM payments p
LEFT JOIN invoices i ON i.invoice_id = p.invoice_id AND i.business_id = p.business_id
WHERE p.business_id = sqlc.arg(business_id)::uuid AND p.customer_id = $1
  AND p.status = 'CLEARED' AND p.payment_date >= $2
  AND (p.invoice_id IS NULL OR i.invoice_id IS NOT NULL)
GROUP BY p.currency ORDER BY p.currency;

-- name: CustomerOpenInvoices :many
SELECT * FROM invoices WHERE business_id=sqlc.arg(business_id)::uuid AND customer_id = $1 AND status IN ('DRAFT', 'OPEN', 'SENT', 'PARTIAL') ORDER BY due_date ASC;

-- name: CustomerReportHasDamagedReferences :one
SELECT EXISTS (
    SELECT 1
    FROM invoices i
    LEFT JOIN projects p ON p.project_id = i.project_id AND p.business_id = i.business_id
    LEFT JOIN quotations q ON q.quotation_id = i.source_quote_id AND q.business_id = i.business_id
    LEFT JOIN invoice_line_items l ON l.invoice_id = i.invoice_id AND l.business_id = i.business_id
    LEFT JOIN items it ON it.item_id = l.item_id AND it.business_id = l.business_id
    WHERE i.business_id = sqlc.arg(business_id)::uuid AND i.customer_id = sqlc.arg(customer_id)::uuid
      AND (sqlc.arg(all_statuses)::boolean OR i.status IN ('DRAFT', 'OPEN', 'SENT', 'PARTIAL'))
      AND ((i.project_id IS NOT NULL AND p.project_id IS NULL)
        OR (i.source_quote_id IS NOT NULL AND q.quotation_id IS NULL)
        OR (l.item_id IS NOT NULL AND it.item_id IS NULL))
);
