-- name: ListCustomers :many
-- A null active lists every customer.
SELECT * FROM customers
WHERE sqlc.narg(active)::boolean IS NULL OR active = sqlc.narg(active)::boolean
ORDER BY name, customer_id;

-- name: GetCustomer :one
SELECT * FROM customers WHERE customer_id = $1;

-- name: GetCustomerForUpdate :one
-- A PATCH reads the row locked, merges the fields sent, and writes every
-- column back with UpdateCustomer.
SELECT * FROM customers WHERE customer_id = $1 FOR UPDATE;

-- name: CreateCustomer :one
INSERT INTO customers (customer_id, name, matching_aliases, active, contact_email, contact_phone, billing_address,
                       contact_first_name, contact_last_name, contact_phone_2, account_number, website, notes,
                       default_currency, billing_address1, billing_address2, billing_country, billing_state,
                       billing_city, billing_postal_code, ship_to_name, shipping_address1, shipping_address2,
                       shipping_country, shipping_state, shipping_city, shipping_postal_code, shipping_phone,
                       shipping_delivery_instructions, quotation_cc_emails, billing_cc_emails, default_wht_type,
                       default_wht_rate, default_wht_gross_up)
VALUES (sqlc.arg(customer_id), sqlc.arg(name), sqlc.arg(matching_aliases), sqlc.arg(active),
        sqlc.narg(contact_email), sqlc.narg(contact_phone), sqlc.narg(billing_address),
        sqlc.narg(contact_first_name), sqlc.narg(contact_last_name), sqlc.narg(contact_phone_2),
        sqlc.narg(account_number), sqlc.narg(website), sqlc.narg(notes), sqlc.arg(default_currency),
        sqlc.narg(billing_address1), sqlc.narg(billing_address2), sqlc.narg(billing_country),
        sqlc.narg(billing_state), sqlc.narg(billing_city), sqlc.narg(billing_postal_code), sqlc.narg(ship_to_name),
        sqlc.narg(shipping_address1), sqlc.narg(shipping_address2), sqlc.narg(shipping_country),
        sqlc.narg(shipping_state), sqlc.narg(shipping_city), sqlc.narg(shipping_postal_code),
        sqlc.narg(shipping_phone), sqlc.narg(shipping_delivery_instructions), sqlc.arg(quotation_cc_emails),
        sqlc.arg(billing_cc_emails), sqlc.arg(default_wht_type), sqlc.narg(default_wht_rate),
        sqlc.arg(default_wht_gross_up))
RETURNING *;

-- name: UpdateCustomer :one
UPDATE customers
SET name = sqlc.arg(name), matching_aliases = sqlc.arg(matching_aliases), active = sqlc.arg(active),
    contact_email = sqlc.narg(contact_email), contact_phone = sqlc.narg(contact_phone),
    billing_address = sqlc.narg(billing_address), contact_first_name = sqlc.narg(contact_first_name),
    contact_last_name = sqlc.narg(contact_last_name), contact_phone_2 = sqlc.narg(contact_phone_2),
    account_number = sqlc.narg(account_number), website = sqlc.narg(website), notes = sqlc.narg(notes),
    default_currency = sqlc.arg(default_currency), billing_address1 = sqlc.narg(billing_address1),
    billing_address2 = sqlc.narg(billing_address2), billing_country = sqlc.narg(billing_country),
    billing_state = sqlc.narg(billing_state), billing_city = sqlc.narg(billing_city),
    billing_postal_code = sqlc.narg(billing_postal_code), ship_to_name = sqlc.narg(ship_to_name),
    shipping_address1 = sqlc.narg(shipping_address1), shipping_address2 = sqlc.narg(shipping_address2),
    shipping_country = sqlc.narg(shipping_country), shipping_state = sqlc.narg(shipping_state),
    shipping_city = sqlc.narg(shipping_city), shipping_postal_code = sqlc.narg(shipping_postal_code),
    shipping_phone = sqlc.narg(shipping_phone),
    shipping_delivery_instructions = sqlc.narg(shipping_delivery_instructions),
    quotation_cc_emails = sqlc.arg(quotation_cc_emails), billing_cc_emails = sqlc.arg(billing_cc_emails),
    default_wht_type = sqlc.arg(default_wht_type), default_wht_rate = sqlc.narg(default_wht_rate),
    default_wht_gross_up = sqlc.arg(default_wht_gross_up)
WHERE customer_id = sqlc.arg(customer_id)
RETURNING *;

-- name: DeactivateCustomer :one
-- Customers are never deleted: invoices reference them.
UPDATE customers SET active = false WHERE customer_id = $1 RETURNING *;
