package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/jackc/pgx/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wenhaokho/wenhao-billing/backend/internal/bills"
	"github.com/wenhaokho/wenhao-billing/backend/internal/bills/billsdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/business/businessdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/customers"
	"github.com/wenhaokho/wenhao-billing/backend/internal/customers/customersdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices"
	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices/invoicesdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/items"
	"github.com/wenhaokho/wenhao-billing/backend/internal/items/itemsdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/payments"
	"github.com/wenhaokho/wenhao-billing/backend/internal/payments/paymentsdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/internal/projects"
	"github.com/wenhaokho/wenhao-billing/backend/internal/projects/projectsdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/quotations"
	"github.com/wenhaokho/wenhao-billing/backend/internal/quotations/quotationsdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/stats"
	"github.com/wenhaokho/wenhao-billing/backend/internal/vendors"
	"github.com/wenhaokho/wenhao-billing/backend/internal/vendors/vendorsdb"
)

// The 17 read tools (inventory §2.6 rows 2-18). Every one is read-only, so
// it never needs the principal. The field lists are Python's (inventory
// §0.5), in Python's order; the values print as the HTTP routes' response
// types print them, which is Python's coerce_value for every column type.

var (
	billFields = []string{
		"bill_id", "vendor_id", "project_id", "bill_number", "po_number", "currency", "amount", "balance_due",
		"status", "issue_date", "due_date",
	}
	billDetailFields = concat(billFields, "subtotal", "discount_type", "discount_value", "payment_terms", "notes")

	itemFields = []string{
		"item_id", "sku", "name", "item_type", "default_currency", "default_unit_price", "default_purchase_price",
		"active", "is_hosting",
	}

	vendorFields = []string{
		"vendor_id", "name", "contact_email", "contact_name", "default_currency", "payment_terms_days", "active",
	}

	customerFields       = []string{"customer_id", "name", "contact_email"}
	customerDetailFields = []string{
		"customer_id", "name", "active", "matching_aliases",
		"contact_first_name", "contact_last_name", "contact_email", "contact_phone", "contact_phone_2",
		"account_number", "website", "notes",
		"default_currency", "default_wht_type", "default_wht_rate", "default_wht_gross_up",
		"billing_address", "billing_address1", "billing_address2", "billing_city", "billing_state",
		"billing_postal_code", "billing_country",
		"ship_to_name", "shipping_address1", "shipping_address2", "shipping_city", "shipping_state",
		"shipping_postal_code", "shipping_country", "shipping_phone", "shipping_delivery_instructions",
	}

	// Every invoice result ends with expected_cash (Python's invoice_to_dict).
	invoiceFields = []string{
		"invoice_id", "invoice_number", "customer_id", "invoice_type", "currency", "amount", "balance_due", "status",
		"issue_date", "due_date", "wht_type", "wht_rate", "wht_gross_up", "wht_amount",
	}
	invoiceDetailFields = concat(invoiceFields,
		"project_id", "po_so_number", "subtotal", "discount_type", "discount_value", "payment_terms", "notes", "footer", "is_template")

	projectFields = []string{
		"project_id", "customer_id", "code", "name", "currency", "contract_value", "status", "start_date", "end_date",
		"active",
	}
	projectDetailFields = concat(projectFields, "notes")

	quotationFields = []string{
		"quotation_id", "quotation_number", "customer_id", "project_id", "currency", "amount", "status", "issue_date",
		"valid_until",
	}
	quotationDetailFields = concat(quotationFields,
		"po_so_number", "subtotal", "discount_type", "discount_value", "payment_terms", "notes", "footer")

	paymentFields = []string{
		"payment_id", "invoice_id", "customer_id", "amount", "currency", "payer_name", "payer_reference",
		"payment_date", "intake_source", "status", "confidence_score",
	}

	statsFields = []string{
		"awaiting_review_count", "draft_count", "sent_count", "customer_count", "auto_cleared_last_30d",
		"open_ar_by_currency", "open_quotation_count", "open_quotation_pipeline", "mrr_by_currency", "base_currency",
		"open_ar_base_by_currency", "open_ar_unconverted",
	}
)

// concat is a new slice of base followed by more.
func concat(base []string, more ...string) []string {
	return append(append(make([]string, 0, len(base)+len(more)), base...), more...)
}

// Descriptions are Python's whole docstrings (FastMCP sends the cleaned
// docstring), which carry the usage rules a client reads.
const (
	descListBills       = "List bills with optional status/vendor filters."
	descGetBill         = "Fetch one bill by id, including terms/notes/discount. Returns {} if not\nfound."
	descListItems       = "List items, optionally filtered by a name substring."
	descListVendors     = "List vendors, optionally filtered by a name substring."
	descResolveVendor   = "Resolve a vendor name substring to a single vendor id.\n\nAmbiguity-stop primitive: on exactly one match, returns\n{\"vendor_id\": \"<uuid>\"}. On zero or more than one match, returns\n{\"candidates\": [{\"vendor_id\", \"name\"}, ...]} and takes NO other\naction — later write tools depend on this exact shape to decide\nwhether they may proceed automatically or must ask for disambiguation."
	descListCustomers   = "List customers, optionally filtered by a name substring."
	descGetCustomer     = "Fetch one customer by id, with its full contact/billing/shipping\nprofile. Returns {} if not found."
	descResolveCustomer = "Resolve a customer name substring to a single customer id.\n\nAmbiguity-stop primitive: on exactly one match, returns\n{\"customer_id\": \"<uuid>\"}. On zero or more than one match, returns\n{\"candidates\": [{\"customer_id\", \"name\"}, ...]} and takes NO other\naction — later write tools depend on this exact shape to decide\nwhether they may proceed automatically or must ask for disambiguation."
	descListInvoices    = "List non-template invoices with optional status/customer/type filters."
	descGetInvoice      = "Fetch one invoice by id, including terms/notes/footer/discount.\nReturns {} if not found."
	descBusinessProfile = "Return the agency's business profile (name, contact, address, etc.).\n\nAn uploaded logo is stored as a base64 data URI (often hundreds of KB), so\n`logo_url` is replaced by a short placeholder in that case; a regular URL\nis returned as-is. `has_logo` says whether any logo is set."
	descGetStats        = "Return dashboard/summary figures (counts + open AR/quotation totals\nper currency), mirroring GET /api/v1/stats/dashboard (minus\n`recent_activity`, which is a reconciliation-log feed, not a summary\nfigure).\n\nDelegates all query logic to\n`app.services.stats.compute_dashboard_stats`, the same function the\nrouter calls — no duplicated SQL here."
	descListProjects    = "List projects, optionally filtered by customer_id."
	descGetProject      = "Fetch one project by id, including notes. Returns {} if not found."
	descListQuotations  = "List quotations with optional status/customer filters."
	descGetQuotation    = "Fetch one quotation by id, including terms/notes/footer/discount.\nReturns {} if not found."
	descRecon           = "List payments awaiting manual review (status == PENDING_MANUAL_REVIEW)."
)

// Input schemas, as FastMCP derives them from the Python signatures (the
// recorded tools/list): additionalProperties false, an optional argument is
// anyOf [T, null] with default null, limit defaults to 200.
const (
	inNoArgs     = `{"additionalProperties":false,"properties":{},"type":"object"}`
	inQuery      = `{"additionalProperties":false,"properties":{"query":{"type":"string"}},"required":["query"],"type":"object"}`
	inQueryLimit = `{"additionalProperties":false,"properties":{"query":{"anyOf":[{"type":"string"},{"type":"null"}],"default":null},"limit":{"default":200,"type":"integer"}},"type":"object"}`
	inLimit      = `{"additionalProperties":false,"properties":{"limit":{"default":200,"type":"integer"}},"type":"object"}`
	inBills      = `{"additionalProperties":false,"properties":{"status":{"anyOf":[{"items":{"type":"string"},"type":"array"},{"type":"null"}],"default":null},"vendor_id":{"anyOf":[{"type":"string"},{"type":"null"}],"default":null},"limit":{"default":200,"type":"integer"}},"type":"object"}`
	inInvoices   = `{"additionalProperties":false,"properties":{"status":{"anyOf":[{"items":{"type":"string"},"type":"array"},{"type":"null"}],"default":null},"customer_id":{"anyOf":[{"type":"string"},{"type":"null"}],"default":null},"invoice_type":{"anyOf":[{"type":"string"},{"type":"null"}],"default":null},"limit":{"default":200,"type":"integer"}},"type":"object"}`
	inProjects   = `{"additionalProperties":false,"properties":{"customer_id":{"anyOf":[{"type":"string"},{"type":"null"}],"default":null},"limit":{"default":200,"type":"integer"}},"type":"object"}`
	inQuotations = `{"additionalProperties":false,"properties":{"status":{"anyOf":[{"items":{"type":"string"},"type":"array"},{"type":"null"}],"default":null},"customer_id":{"anyOf":[{"type":"string"},{"type":"null"}],"default":null},"limit":{"default":200,"type":"integer"}},"type":"object"}`
)

// idInput is the input schema of a tool taking one required id argument.
func idInput(name string) string {
	return `{"additionalProperties":false,"properties":{"` + name + `":{"type":"string"}},"required":["` + name + `"],"type":"object"}`
}

// inputSchema parses one of the schema literals above.
func inputSchema(text string) *jsonschema.Schema {
	var s jsonschema.Schema
	if err := json.Unmarshal([]byte(text), &s); err != nil {
		panic(fmt.Sprintf("mcp: input schema %s: %v", text, err))
	}
	return &s
}

// readTool is the Tool of a read tool: name, Python's description and input
// schema.
func readTool(name, desc, input string) *mcp.Tool {
	return &mcp.Tool{Name: name, Description: desc, InputSchema: inputSchema(input)}
}

// listOut is the result of the list tools: Python wraps a list[dict] as
// {"result": [...]}.
type listOut struct {
	Result []Object `json:"result"`
}

// The argument types of the read tools. A list tool's limit is Python's int
// with its default of 200, which the input schema applies when it is
// absent.
type (
	noIn    struct{}
	queryIn struct {
		Query string `json:"query"`
	}
	queryLimitIn struct {
		Query *string `json:"query"`
		Limit int     `json:"limit"`
	}
	limitIn struct {
		Limit int `json:"limit"`
	}
	listBillsIn struct {
		Status   []string `json:"status"`
		VendorID *string  `json:"vendor_id"`
		Limit    int      `json:"limit"`
	}
	listInvoicesIn struct {
		Status      []string `json:"status"`
		CustomerID  *string  `json:"customer_id"`
		InvoiceType *string  `json:"invoice_type"`
		Limit       int      `json:"limit"`
	}
	listProjectsIn struct {
		CustomerID *string `json:"customer_id"`
		Limit      int     `json:"limit"`
	}
	listQuotationsIn struct {
		Status     []string `json:"status"`
		CustomerID *string  `json:"customer_id"`
		Limit      int      `json:"limit"`
	}
	billIDIn struct {
		BillID string `json:"bill_id"`
	}
	customerIDIn struct {
		CustomerID string `json:"customer_id"`
	}
	invoiceIDIn struct {
		InvoiceID string `json:"invoice_id"`
	}
	projectIDIn struct {
		ProjectID string `json:"project_id"`
	}
	quotationIDIn struct {
		QuotationID string `json:"quotation_id"`
	}
)

// listArg is a bad argument of a list tool (see BadArg); refuseArg of one
// that answers {"error": ...}.
func listArg(err error) error   { return BadArg("%s", err.Error()) }
func refuseArg(err error) error { return Refuse("%s", err.Error()) }

// registerReadTools adds the 17 read tools, in Python's tools/list order.
func registerReadTools(s *mcp.Server, d Deps) {
	addTool(s, d, readTool("list_bills", descListBills, inBills), func(ctx context.Context, tx pgx.Tx, _ principalRef, in listBillsIn) (listOut, error) {
		vendor, err := optID("vendor_id", in.VendorID)
		if err != nil {
			return listOut{}, listArg(err)
		}
		rows, err := bills.List(ctx, billsdb.New(tx), bills.ListFilter{Statuses: in.Status, VendorID: vendor, Limit: clampLimit(in.Limit)})
		if err != nil {
			return listOut{}, err
		}
		res, err := pickAll(rows, billFields)
		return listOut{Result: res}, err
	}, withOutSchema())

	addTool(s, d, readTool("get_bill", descGetBill, idInput("bill_id")), func(ctx context.Context, tx pgx.Tx, _ principalRef, in billIDIn) (Object, error) {
		id, err := parseID("bill_id", in.BillID)
		if err != nil {
			return Object{}, refuseArg(err)
		}
		b, err := bills.Get(ctx, billsdb.New(tx), id)
		if noRows(err) {
			return Object{}, nil
		}
		if err != nil {
			return Object{}, err
		}
		return pick(b, billDetailFields)
	})

	addTool(s, d, readTool("list_items", descListItems, inQueryLimit), func(ctx context.Context, tx pgx.Tx, _ principalRef, in queryLimitIn) (listOut, error) {
		rows, err := items.List(ctx, itemsdb.New(tx), itemsdb.ListItemsParams{NameLike: nameLike(in.Query), RowLimit: clampLimit(in.Limit)})
		if err != nil {
			return listOut{}, err
		}
		res, err := pickAll(rows, itemFields)
		return listOut{Result: res}, err
	}, withOutSchema())

	addTool(s, d, readTool("list_vendors", descListVendors, inQueryLimit), func(ctx context.Context, tx pgx.Tx, _ principalRef, in queryLimitIn) (listOut, error) {
		rows, err := vendors.List(ctx, vendorsdb.New(tx), vendors.ListFilter{NameLike: nameLike(in.Query), Limit: clampLimit(in.Limit)})
		if err != nil {
			return listOut{}, err
		}
		res, err := pickAll(rows, vendorFields)
		return listOut{Result: res}, err
	}, withOutSchema())

	addTool(s, d, readTool("resolve_vendor", descResolveVendor, inQuery), func(ctx context.Context, tx pgx.Tx, _ principalRef, in queryIn) (Object, error) {
		// "" matches everything, as Python's ILIKE '%%' does; at most 10, by name.
		like := platform.LikeEscape(in.Query)
		limit := int32(10)
		rows, err := vendors.List(ctx, vendorsdb.New(tx), vendors.ListFilter{NameLike: &like, Limit: &limit})
		if err != nil {
			return Object{}, err
		}
		ids := make([]string, len(rows))
		names := make([]string, len(rows))
		for i, v := range rows {
			ids[i], names[i] = v.VendorID.String(), v.Name
		}
		return resolved("vendor_id", ids, names)
	})

	addTool(s, d, readTool("list_customers", descListCustomers, inQueryLimit), func(ctx context.Context, tx pgx.Tx, _ principalRef, in queryLimitIn) (listOut, error) {
		rows, err := customers.List(ctx, customersdb.New(tx), customers.ListFilter{NameLike: nameLike(in.Query), Limit: clampLimit(in.Limit)})
		if err != nil {
			return listOut{}, err
		}
		res, err := pickAll(rows, customerFields)
		return listOut{Result: res}, err
	}, withOutSchema())

	addTool(s, d, readTool("get_customer", descGetCustomer, idInput("customer_id")), func(ctx context.Context, tx pgx.Tx, _ principalRef, in customerIDIn) (Object, error) {
		id, err := parseID("customer_id", in.CustomerID)
		if err != nil {
			return Object{}, refuseArg(err)
		}
		c, err := customers.Get(ctx, customersdb.New(tx), id)
		if noRows(err) {
			return Object{}, nil
		}
		if err != nil {
			return Object{}, err
		}
		return pick(c, customerDetailFields)
	})

	addTool(s, d, readTool("resolve_customer", descResolveCustomer, inQuery), func(ctx context.Context, tx pgx.Tx, _ principalRef, in queryIn) (Object, error) {
		like := platform.LikeEscape(in.Query)
		limit := int32(10)
		rows, err := customers.List(ctx, customersdb.New(tx), customers.ListFilter{NameLike: &like, Limit: &limit})
		if err != nil {
			return Object{}, err
		}
		ids := make([]string, len(rows))
		names := make([]string, len(rows))
		for i, c := range rows {
			ids[i], names[i] = c.CustomerID.String(), c.Name
		}
		return resolved("customer_id", ids, names)
	})

	addTool(s, d, readTool("list_invoices", descListInvoices, inInvoices), func(ctx context.Context, tx pgx.Tx, _ principalRef, in listInvoicesIn) (listOut, error) {
		customer, err := optID("customer_id", in.CustomerID)
		if err != nil {
			return listOut{}, listArg(err)
		}
		rows, err := invoices.ListBy(ctx, invoicesdb.New(tx), invoices.ListFilter{
			ExcludeTemplates: true, Statuses: in.Status, CustomerID: customer,
			InvoiceType: optText(in.InvoiceType), Limit: clampLimit(in.Limit),
		})
		if err != nil {
			return listOut{}, err
		}
		res, err := pickAll(rows, concat(invoiceFields, "expected_cash"))
		return listOut{Result: res}, err
	}, withOutSchema())

	addTool(s, d, readTool("get_invoice", descGetInvoice, idInput("invoice_id")), func(ctx context.Context, tx pgx.Tx, _ principalRef, in invoiceIDIn) (Object, error) {
		id, err := parseID("invoice_id", in.InvoiceID)
		if err != nil {
			return Object{}, refuseArg(err)
		}
		inv, err := invoices.Get(ctx, invoicesdb.New(tx), id)
		if noRows(err) {
			return Object{}, nil
		}
		if err != nil {
			return Object{}, err
		}
		return pick(inv, concat(invoiceDetailFields, "expected_cash"))
	})

	addTool(s, d, readTool("get_business_profile", descBusinessProfile, inNoArgs), func(ctx context.Context, tx pgx.Tx, _ principalRef, _ noIn) (Object, error) {
		// Row id 1, the singleton; no row is {} (Go change 13).
		p, err := businessdb.New(tx).GetProfile(ctx)
		if noRows(err) {
			return Object{}, nil
		}
		if err != nil {
			return Object{}, err
		}
		return profileObject(p)
	})

	addTool(s, d, readTool("get_stats", descGetStats, inNoArgs), func(ctx context.Context, tx pgx.Tx, _ principalRef, _ noIn) (Object, error) {
		st, err := stats.Dashboard(ctx, tx, d.Cfg.BaseCurrency, platform.TodayUTC(d.Clock), d.Clock.Now())
		if err != nil {
			return Object{}, err
		}
		return pick(st, statsFields)
	})

	addTool(s, d, readTool("list_projects", descListProjects, inProjects), func(ctx context.Context, tx pgx.Tx, _ principalRef, in listProjectsIn) (listOut, error) {
		customer, err := optID("customer_id", in.CustomerID)
		if err != nil {
			return listOut{}, listArg(err)
		}
		rows, err := projects.List(ctx, projectsdb.New(tx), projects.ListFilter{CustomerID: customer, Limit: clampLimit(in.Limit)})
		if err != nil {
			return listOut{}, err
		}
		res, err := pickAll(rows, projectFields)
		return listOut{Result: res}, err
	}, withOutSchema())

	addTool(s, d, readTool("get_project", descGetProject, idInput("project_id")), func(ctx context.Context, tx pgx.Tx, _ principalRef, in projectIDIn) (Object, error) {
		id, err := parseID("project_id", in.ProjectID)
		if err != nil {
			return Object{}, refuseArg(err)
		}
		p, err := projects.Get(ctx, projectsdb.New(tx), id)
		if noRows(err) {
			return Object{}, nil
		}
		if err != nil {
			return Object{}, err
		}
		return pick(p, projectDetailFields)
	})

	addTool(s, d, readTool("list_quotations", descListQuotations, inQuotations), func(ctx context.Context, tx pgx.Tx, _ principalRef, in listQuotationsIn) (listOut, error) {
		customer, err := optID("customer_id", in.CustomerID)
		if err != nil {
			return listOut{}, listArg(err)
		}
		rows, err := quotations.List(ctx, quotationsdb.New(tx), quotations.ListFilter{Statuses: in.Status, CustomerID: customer, Limit: clampLimit(in.Limit)})
		if err != nil {
			return listOut{}, err
		}
		res, err := pickAll(rows, quotationFields)
		return listOut{Result: res}, err
	}, withOutSchema())

	addTool(s, d, readTool("get_quotation", descGetQuotation, idInput("quotation_id")), func(ctx context.Context, tx pgx.Tx, _ principalRef, in quotationIDIn) (Object, error) {
		id, err := parseID("quotation_id", in.QuotationID)
		if err != nil {
			return Object{}, refuseArg(err)
		}
		q, err := quotations.Get(ctx, quotationsdb.New(tx), id)
		if noRows(err) {
			return Object{}, nil
		}
		if err != nil {
			return Object{}, err
		}
		return pick(q, quotationDetailFields)
	})

	addTool(s, d, readTool("get_reconciliation_queue", descRecon, inLimit), func(ctx context.Context, tx pgx.Tx, _ principalRef, in limitIn) (listOut, error) {
		rows, err := payments.AwaitingReview(ctx, paymentsdb.New(tx), clampLimit(in.Limit))
		if err != nil {
			return listOut{}, err
		}
		res, err := pickAll(rows, paymentFields)
		return listOut{Result: res}, err
	}, withOutSchema())
}

// resolved is the answer of resolve_vendor / resolve_customer: exactly one
// match is {key: id}; zero or several are {"candidates": [{key, "name"}]}
// and nothing else, so a later tool cannot act on an ambiguous name.
func resolved(key string, ids, names []string) (Object, error) {
	var o Object
	if len(ids) == 1 {
		return o, o.set(key, ids[0])
	}
	cands := make([]Object, len(ids))
	for i := range ids {
		if err := cands[i].set(key, ids[i]); err != nil {
			return Object{}, err
		}
		if err := cands[i].set("name", names[i]); err != nil {
			return Object{}, err
		}
	}
	return o, o.set("candidates", cands)
}

// profileObject is _PROFILE_FIELDS of the business profile plus has_logo. An
// uploaded logo is a base64 data URI of hundreds of KB, so it is replaced by
// a short placeholder (the same text as Python); a regular URL is returned
// as is.
func profileObject(p businessdb.BusinessProfile) (Object, error) {
	var o Object
	logo := p.LogoUrl
	hasLogo := logo != nil && *logo != ""
	var logoOut any = logo
	if hasLogo && strings.HasPrefix(*logo, "data:") {
		mime, _, _ := strings.Cut(strings.TrimPrefix(*logo, "data:"), ";")
		mime, _, _ = strings.Cut(mime, ",")
		if mime == "" {
			mime = "unknown"
		}
		logoOut = fmt.Sprintf("<inline %s image, %d KB omitted>", mime, utf8.RuneCountInString(*logo)/1024)
	}
	for _, kv := range []struct {
		key string
		val any
	}{
		{"id", p.ID},
		{"name", p.Name},
		{"address", p.Address},
		{"contact_email", p.ContactEmail},
		{"contact_phone", p.ContactPhone},
		{"invoice_title", p.InvoiceTitle},
		{"invoice_summary", p.InvoiceSummary},
		{"logo_url", logoOut},
		{"default_notes", p.DefaultNotes},
		{"npwp", p.Npwp},
		{"npwp_name", p.NpwpName},
		{"updated_at", isoNaive(p.UpdatedAt)},
		{"has_logo", hasLogo},
	} {
		if err := o.set(kv.key, kv.val); err != nil {
			return Object{}, err
		}
	}
	return o, nil
}
