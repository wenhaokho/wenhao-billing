// Package businessscope records the v7 ownership inventory for migration and coverage tests.
package businessscope

type Table struct {
	Name              string
	PrimaryKey        string
	ForeignKeys       []string
	NaturalUniqueKeys []string
}

// OwnedTables describes existing v7 constraints, not generated runtime SQL.
var OwnedTables = []Table{
	{"bill_line_items", "line_item_id", []string{"bill_id -> bills(bill_id) ON DELETE CASCADE", "expense_account_id -> chart_of_accounts(account_id)", "item_id -> items(item_id)"}, []string{}},
	{"bills", "bill_id", []string{"project_id -> projects(project_id)", "vendor_id -> vendors(vendor_id)"}, []string{}},
	{"business_profile", "id", []string{}, []string{}},
	{"chart_of_accounts", "account_id", []string{}, []string{"code"}},
	{"cloudflare_targets", "target_id", []string{"item_id -> items(item_id) ON DELETE CASCADE"}, []string{"item_id"}},
	{"customers", "customer_id", []string{}, []string{}},
	{"fx_rates", "rate_id", []string{}, []string{"from_currency, to_currency, as_of_date"}},
	{"invoice_line_items", "line_item_id", []string{"invoice_id -> invoices(invoice_id) ON DELETE CASCADE", "item_id -> items(item_id)"}, []string{}},
	{"invoices", "invoice_id", []string{"customer_id -> customers(customer_id)", "project_id -> projects(project_id)", "source_quote_id -> quotations(quotation_id)"}, []string{"invoice_number"}},
	{"items", "item_id", []string{"expense_account_id -> chart_of_accounts(account_id)", "revenue_account_id -> chart_of_accounts(account_id)"}, []string{"sku"}},
	{"journal_entries", "entry_id", []string{"reversed_by_entry_id -> journal_entries(entry_id)"}, []string{}},
	{"journal_lines", "line_id", []string{"account_id -> chart_of_accounts(account_id)", "entry_id -> journal_entries(entry_id)"}, []string{}},
	{"oauth_clients", "client_id", []string{}, []string{}},
	{"oauth_refresh_tokens", "jti", []string{}, []string{}},
	{"payment_account", "id", []string{"business_profile_id -> business_profile(id) ON DELETE CASCADE"}, []string{"business_profile_id, currency", "business_profile_id WHERE is_default"}},
	{"payments", "payment_id", []string{"customer_id -> customers(customer_id)", "invoice_id -> invoices(invoice_id)"}, []string{"intake_source, external_ref"}},
	{"projects", "project_id", []string{"customer_id -> customers(customer_id)"}, []string{"code", "code (unique index ix_projects_code)"}},
	{"quotation_line_items", "line_item_id", []string{"item_id -> items(item_id)", "quotation_id -> quotations(quotation_id) ON DELETE CASCADE"}, []string{}},
	{"quotations", "quotation_id", []string{"converted_invoice_id -> invoices(invoice_id)", "customer_id -> customers(customer_id)", "project_id -> projects(project_id)"}, []string{"quotation_number"}},
	{"reconciliation_log", "log_id", []string{"actor_user_id -> users(user_id)", "payment_id -> payments(payment_id)"}, []string{}},
	{"users", "user_id", []string{}, []string{"email", "password_reset_token (unique index)"}},
	{"vendors", "vendor_id", []string{}, []string{}},
	{"recurring_draft_events", "invoice_id", []string{}, []string{}},
	{"recurring_draft_deliveries", "invoice_id, user_id", []string{"invoice_id -> recurring_draft_events(invoice_id)"}, []string{}},
	{"hosting_restore_intents", "invoice_id, item_id", []string{"invoice_id -> invoices(invoice_id)", "item_id -> items(item_id)"}, []string{"item_id WHERE lease_token IS NOT NULL"}},
	{"hosting_operation_leases", "item_id", []string{"item_id -> items(item_id) ON DELETE CASCADE"}, []string{}},
}

// GlobalStatements are the only categories exempt from application ownership.
var GlobalStatements = []string{"schema and migration metadata", "catalog inspection", "PostgreSQL health and time", "migration-level locks"}
