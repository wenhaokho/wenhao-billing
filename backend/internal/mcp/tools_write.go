package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/shopspring/decimal"

	"github.com/LOQATO/gokit/apierr"

	"github.com/wenhaokho/wenhao-billing/backend/internal/bills"
	"github.com/wenhaokho/wenhao-billing/backend/internal/customers"
	"github.com/wenhaokho/wenhao-billing/backend/internal/customers/customersdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/fx"
	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices"
	"github.com/wenhaokho/wenhao-billing/backend/internal/items"
	"github.com/wenhaokho/wenhao-billing/backend/internal/items/itemsdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/ledger"
	"github.com/wenhaokho/wenhao-billing/backend/internal/matching"
	"github.com/wenhaokho/wenhao-billing/backend/internal/payments"
	"github.com/wenhaokho/wenhao-billing/backend/internal/payments/paymentsdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/internal/projects"
	"github.com/wenhaokho/wenhao-billing/backend/internal/projects/projectsdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/quotations"
	"github.com/wenhaokho/wenhao-billing/backend/internal/vendors"
	"github.com/wenhaokho/wenhao-billing/backend/internal/vendors/vendorsdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/withholding"
)

// The 19 write tools (inventory §2.6 rows 19-37). Each one runs the service
// the HTTP route runs, in the helper's transaction, and answers the same
// field lists as the read tools. Every refusal, whether a bad argument, a
// rule of the domain or a known constraint, is {"error": sentence} (Go change
// 11); only a failure nothing foresaw is the generic isError.

var (
	itemDetailFields = []string{
		"item_id", "sku", "name", "item_type", "description", "default_currency", "default_unit_price",
		"default_purchase_price", "revenue_account_id", "expense_account_id", "is_sold", "is_purchased", "active",
		"is_hosting", "hosting_domain", "hosting_grace_days", "hosting_suspension_enabled", "hosting_status",
	}
	vendorDetailFields = []string{
		"vendor_id", "name", "contact_email", "contact_name", "default_currency", "payment_terms_days", "tax_id",
		"notes", "active",
	}
	// Every invoice result ends with expected_cash.
	invoiceResultFields = concat(invoiceDetailFields, "expected_cash")
)

// Python's whole docstrings, as FastMCP sends them.
const (
	descCreateBill     = "Create a DRAFT bill. Autonomous."
	descUpdateBill     = "Edit a DRAFT/OPEN bill. `changes` keys are BillUpdate field names (e.g.\nnotes, payment_terms, discount_type/discount_value, line_items).\nAutonomous.\n\nUnknown keys are rejected rather than applied, so a near-miss like \"note\"\nor \"terms\" errors instead of being dropped by Pydantic's default\nextra=\"ignore\" while the tool still reports success."
	descCreateItem     = "Create a catalog item (non-hosting: item_type SERVICE/USAGE/FIXED_FEE).\nAutonomous.\n\nEvery writable non-hosting ItemCreate field is an explicit arg, including\nthe revenue/expense account overrides — without them a caller had no way\nto post an item to a specific chart-of-accounts line. Hosting fields\n(is_hosting, hosting_*, cloudflare_target) are deliberately absent; see\nthe module docstring."
	descUpdateItem     = "Edit an item. `changes` keys are ItemUpdate field names — the same names\n`create_item` takes (e.g. name, default_unit_price, active). Autonomous.\n\nUnknown keys are rejected rather than applied: `unit_price` is a near-miss\nfor `default_unit_price` that Pydantic's default extra=\"ignore\" would\notherwise drop while still returning success."
	descCreateVendor   = "Create a vendor. Autonomous.\n\nThese args are every writable Vendor column. Unlike Customer, Vendor has\nno address or phone column at all (see backend/app/models/vendor.py) —\nthere is nothing further to expose here, and a caller asking to record a\nvendor address should be told the field doesn't exist rather than have it\nfolded into `notes`."
	descUpdateVendor   = "Edit a vendor. `changes` keys are VendorUpdate field names — the same\nnames `create_vendor` takes (e.g. name, contact_email,\npayment_terms_days, tax_id, notes, active). Autonomous.\n\nUnknown keys are rejected rather than applied. This matters most for the\ncolumns Vendor doesn't have: a `changes={\"address\": ...}` used to return\nsuccess while storing nothing."
	descCreateCustomer = "Create a customer. Autonomous.\n\nEvery writable customer column is an explicit arg on purpose. When this\ntool only exposed name/email/contact-name/currency/notes, a caller asked\nfor a customer \"with phone X at address Y\" had nowhere to put either and\nended up folding both into `notes` — the data was in the record but not\nin the fields the UI, invoices, and matching read from.\n\n`default_wht_type` (NONE|PPH21|PPH23), `default_wht_rate` (percent) and\n`default_wht_gross_up` seed new invoices for this customer; gross-up raises\nthe invoice so that after the client withholds PPh you still receive the\nfull base amount.\n\nAddress is structured (address1/address2/city/state/postal_code/country),\nmatching the customer form. The legacy unstructured `billing_address`\ncolumn is deliberately not writable here — it is read-only back-compat.\n\nThe primary contact name is stored as first/last — the same fields the\ncustomer edit form writes. There is deliberately no combined\n`contact_name` arg: the legacy column was dropped in\n0024_customer_contact_single because writing it here left records whose\ncontact name could not be edited in the UI."
	descUpdateCustomer = "Edit a customer. `changes` keys are CustomerUpdate field names — the\nsame names `create_customer` takes (e.g. contact_phone, billing_address1,\nbilling_city, notes, default_currency, active,\ndefault_wht_type/default_wht_rate/default_wht_gross_up). Gross-up raises the\ninvoice so that after the client withholds PPh you still receive the full\nbase amount. Autonomous.\n\nUnknown keys are rejected rather than applied: CustomerUpdate's default\nextra=\"ignore\" would otherwise drop a near-miss key like \"phone\" or\n\"address\" and still return success, the edit silently doing nothing. See\napp/mcp/validate.py — every `update_*` tool shares that check."
	descCreateInvoice  = "Create a DRAFT invoice (lands in Awaiting Finalization). Autonomous.\n\nOptional withholding: `wht_type` NONE|PPH21|PPH23, `wht_rate` percent\n(defaults 2.5 for PPH21, 2 for PPH23), `wht_gross_up`; gross-up raises the\ninvoice so that after the client withholds PPh you still receive the full\nbase amount. Omitted values fall back to the customer's defaults."
	descUpdateInvoice  = "Edit a DRAFT invoice. `changes` keys are InvoiceUpdate field names (e.g.\nnotes, payment_terms, discount_type/discount_value, line_items,\nwht_type/wht_rate/wht_gross_up). Gross-up raises the invoice so that after\nthe client withholds PPh you still receive the full base amount. Autonomous.\n\nUnknown keys are rejected rather than applied, so a near-miss like \"note\"\nor \"terms\" errors instead of being dropped by Pydantic's default\nextra=\"ignore\" while the tool still reports success."
	descFinalize       = "Issue a DRAFT invoice (-> OPEN; becomes SENT only when emailed).\n\nAutonomous (internal, reversible via void). A zero-value invoice settles\nstraight to PAID."
	descVoid           = "Void an invoice (reversible state, not a delete). Autonomous."
	descCreateTemplate = "Create a recurring invoice template (is_template=True, DRAFT).\n`schedule` matches RecurringSchedule fields (frequency, interval,\nstart_date, end_mode, end_date?, end_after_cycles?). Optional withholding\nvia `wht_type` NONE|PPH21|PPH23, `wht_rate`, `wht_gross_up`; gross-up\nraises the invoice so that after the client withholds PPh you still\nreceive the full base amount. Autonomous."
	descTrigger        = "Manually trigger one recurring cycle for a template — idempotent per\n(template_id, cycle_key). Autonomous."
	descCreateProject  = "Create a project. Autonomous."
	descUpdateProject  = "Edit a project. `changes` keys are ProjectUpdate field names — the same\nnames `create_project` takes (e.g. code, name, currency, status,\ncontract_value, notes, active). Autonomous.\n\nUnknown keys are rejected rather than applied, so a near-miss like\n\"project_name\" errors instead of being dropped by Pydantic's default\nextra=\"ignore\" while the tool still reports success."
	descCreateQuote    = "Create a DRAFT quotation. Autonomous."
	descUpdateQuote    = "Edit a DRAFT/SENT quotation. `changes` keys are QuotationUpdate field\nnames (e.g. notes, payment_terms, discount_type/discount_value,\nline_items). Autonomous.\n\nUnknown keys are rejected rather than applied, so a near-miss like \"note\"\nor \"valid_till\" (real field: `valid_until`) errors instead of being dropped\nby Pydantic's default extra=\"ignore\" while the tool still reports success."
	descResolve        = "Force-resolve a PENDING_MANUAL_REVIEW payment against a chosen\ninvoice. Safe-stop: refuses — returns {\"error\": ...}, no side effect,\npayment status unchanged — on any currency or amount mismatch between\nthe payment and the invoice; a human must resolve those via the\nManualReviewView UI. Autonomous only for an exact currency+amount\nmatch (the payer/date ambiguity that put it in the queue)."
)

// Property schemas, as FastMCP derives them from the Python signatures: an
// optional argument is anyOf [T, null] with default null, and an argument
// without a "default" is required.
const (
	tStr   = `{"type":"string"}`
	tNStr  = `{"anyOf":[{"type":"string"},{"type":"null"}],"default":null}`
	tNBool = `{"anyOf":[{"type":"boolean"},{"type":"null"}],"default":null}`
	tNInt  = `{"anyOf":[{"type":"integer"},{"type":"null"}],"default":null}`
	tNStrs = `{"anyOf":[{"items":{"type":"string"},"type":"array"},{"type":"null"}],"default":null}`
	tDict  = `{"additionalProperties":true,"type":"object"}`
	tLines = `{"items":{"additionalProperties":true,"type":"object"},"type":"array"}`
)

// prop is one named property of an input schema.
type prop struct{ name, schema string }

func p(name, schema string) prop { return prop{name, schema} }

// pd is a property with a default value def (a JSON literal) of JSON type typ.
func pd(name, typ, def string) prop {
	return prop{name, `{"default":` + def + `,"type":"` + typ + `"}`}
}

// nstrs are the optional string properties named, in order.
func nstrs(names ...string) []prop {
	out := make([]prop, len(names))
	for i, n := range names {
		out[i] = p(n, tNStr)
	}
	return out
}

// toolInput is the input schema of a tool whose properties are props, in
// Python's parameter order: additionalProperties false, and every property
// without a default required.
func toolInput(props ...prop) string {
	var b strings.Builder
	var required []string
	b.WriteString(`{"additionalProperties":false,"properties":{`)
	for i, pr := range props {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, "%q:%s", pr.name, pr.schema)
		if !strings.Contains(pr.schema, `"default"`) {
			required = append(required, fmt.Sprintf("%q", pr.name))
		}
	}
	b.WriteString(`},"required":[` + strings.Join(required, ",") + `],"type":"object"}`)
	return b.String()
}

func join(groups ...[]prop) []prop {
	var out []prop
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

func writeTool(name, desc, input string) *mcp.Tool {
	return &mcp.Tool{Name: name, Description: desc, InputSchema: inputSchema(input)}
}

// The arguments of the write tools. An optional argument is a pointer (nil
// is Python's None), an argument with a default is a plain value, which the
// input schema's default fills in when the client leaves it out.
type (
	createBillIn struct {
		VendorID     string           `json:"vendor_id"`
		Currency     string           `json:"currency"`
		LineItems    []map[string]any `json:"line_items"`
		ProjectID    *string          `json:"project_id"`
		BillNumber   *string          `json:"bill_number"`
		PoNumber     *string          `json:"po_number"`
		IssueDate    *string          `json:"issue_date"`
		DueDate      *string          `json:"due_date"`
		PaymentTerms *string          `json:"payment_terms"`
		Notes        *string          `json:"notes"`
		DiscountType *string          `json:"discount_type"`
		DiscountVal  *string          `json:"discount_value"`
	}
	updateBillIn struct {
		BillID  string         `json:"bill_id"`
		Changes map[string]any `json:"changes"`
	}
	createItemIn struct {
		Name             string  `json:"name"`
		ItemType         string  `json:"item_type"`
		Sku              *string `json:"sku"`
		Description      *string `json:"description"`
		DefaultCurrency  string  `json:"default_currency"`
		DefaultUnitPrice *string `json:"default_unit_price"`
		DefaultPurchase  *string `json:"default_purchase_price"`
		RevenueAccountID *int    `json:"revenue_account_id"`
		ExpenseAccountID *int    `json:"expense_account_id"`
		IsSold           bool    `json:"is_sold"`
		IsPurchased      bool    `json:"is_purchased"`
		Active           bool    `json:"active"`
	}
	updateItemIn struct {
		ItemID  string         `json:"item_id"`
		Changes map[string]any `json:"changes"`
	}
	createVendorIn struct {
		Name             string  `json:"name"`
		ContactEmail     *string `json:"contact_email"`
		ContactName      *string `json:"contact_name"`
		DefaultCurrency  string  `json:"default_currency"`
		PaymentTermsDays int     `json:"payment_terms_days"`
		TaxID            *string `json:"tax_id"`
		Notes            *string `json:"notes"`
		Active           bool    `json:"active"`
	}
	updateVendorIn struct {
		VendorID string         `json:"vendor_id"`
		Changes  map[string]any `json:"changes"`
	}
	createCustomerIn struct {
		Name             string   `json:"name"`
		MatchingAliases  []string `json:"matching_aliases"`
		ContactFirstName *string  `json:"contact_first_name"`
		ContactLastName  *string  `json:"contact_last_name"`
		ContactEmail     *string  `json:"contact_email"`
		ContactPhone     *string  `json:"contact_phone"`
		ContactPhone2    *string  `json:"contact_phone_2"`
		AccountNumber    *string  `json:"account_number"`
		Website          *string  `json:"website"`
		Notes            *string  `json:"notes"`
		DefaultCurrency  *string  `json:"default_currency"`
		DefaultWhtType   *string  `json:"default_wht_type"`
		DefaultWhtRate   *string  `json:"default_wht_rate"`
		DefaultWhtGross  *bool    `json:"default_wht_gross_up"`
		BillingAddress1  *string  `json:"billing_address1"`
		BillingAddress2  *string  `json:"billing_address2"`
		BillingCity      *string  `json:"billing_city"`
		BillingState     *string  `json:"billing_state"`
		BillingPostal    *string  `json:"billing_postal_code"`
		BillingCountry   *string  `json:"billing_country"`
		ShipToName       *string  `json:"ship_to_name"`
		ShippingAddress1 *string  `json:"shipping_address1"`
		ShippingAddress2 *string  `json:"shipping_address2"`
		ShippingCity     *string  `json:"shipping_city"`
		ShippingState    *string  `json:"shipping_state"`
		ShippingPostal   *string  `json:"shipping_postal_code"`
		ShippingCountry  *string  `json:"shipping_country"`
		ShippingPhone    *string  `json:"shipping_phone"`
		ShippingDelivery *string  `json:"shipping_delivery_instructions"`
	}
	updateCustomerIn struct {
		CustomerID string         `json:"customer_id"`
		Changes    map[string]any `json:"changes"`
	}
	createInvoiceIn struct {
		CustomerID   string           `json:"customer_id"`
		Currency     string           `json:"currency"`
		LineItems    []map[string]any `json:"line_items"`
		InvoiceType  string           `json:"invoice_type"`
		DiscountType *string          `json:"discount_type"`
		DiscountVal  *string          `json:"discount_value"`
		PaymentTerms *string          `json:"payment_terms"`
		Notes        *string          `json:"notes"`
		WhtType      *string          `json:"wht_type"`
		WhtRate      *string          `json:"wht_rate"`
		WhtGrossUp   *bool            `json:"wht_gross_up"`
	}
	updateInvoiceIn struct {
		InvoiceID string         `json:"invoice_id"`
		Changes   map[string]any `json:"changes"`
	}
	createTemplateIn struct {
		CustomerID   string           `json:"customer_id"`
		Currency     string           `json:"currency"`
		LineItems    []map[string]any `json:"line_items"`
		Schedule     map[string]any   `json:"schedule"`
		PaymentTerms string           `json:"payment_terms"`
		ProjectID    *string          `json:"project_id"`
		PoSoNumber   *string          `json:"po_so_number"`
		Notes        *string          `json:"notes"`
		Footer       *string          `json:"footer"`
		DiscountType *string          `json:"discount_type"`
		DiscountVal  *string          `json:"discount_value"`
		WhtType      *string          `json:"wht_type"`
		WhtRate      *string          `json:"wht_rate"`
		WhtGrossUp   *bool            `json:"wht_gross_up"`
	}
	triggerIn struct {
		TemplateID string `json:"template_id"`
		CycleKey   string `json:"cycle_key"`
	}
	createProjectIn struct {
		CustomerID    string  `json:"customer_id"`
		Code          string  `json:"code"`
		Name          string  `json:"name"`
		Currency      string  `json:"currency"`
		ContractValue *string `json:"contract_value"`
		Status        string  `json:"status"`
		StartDate     *string `json:"start_date"`
		EndDate       *string `json:"end_date"`
		Notes         *string `json:"notes"`
	}
	updateProjectIn struct {
		ProjectID string         `json:"project_id"`
		Changes   map[string]any `json:"changes"`
	}
	createQuotationIn struct {
		CustomerID   string           `json:"customer_id"`
		Currency     string           `json:"currency"`
		LineItems    []map[string]any `json:"line_items"`
		ProjectID    *string          `json:"project_id"`
		PoSoNumber   *string          `json:"po_so_number"`
		IssueDate    *string          `json:"issue_date"`
		ValidUntil   *string          `json:"valid_until"`
		PaymentTerms *string          `json:"payment_terms"`
		Notes        *string          `json:"notes"`
		Footer       *string          `json:"footer"`
		DiscountType *string          `json:"discount_type"`
		DiscountVal  *string          `json:"discount_value"`
	}
	updateQuotationIn struct {
		QuotationID string         `json:"quotation_id"`
		Changes     map[string]any `json:"changes"`
	}
	resolveIn struct {
		PaymentID string `json:"payment_id"`
		InvoiceID string `json:"invoice_id"`
	}
)

// body is the JSON object a tool builds from its arguments to decode as a
// route's request body. A nil argument is left out, so the field is no value
// (on a create) as Python's None was.
type body map[string]any

// text sets key to the argument unless it is nil.
func (b body) text(key string, v *string) {
	if v != nil {
		b[key] = *v
	}
}

// number is text for a money or rate argument, where Python read
// `Decimal(x) if x else None`: an empty string is no value either.
func (b body) number(key string, v *string) {
	if v != nil && *v != "" {
		b[key] = *v
	}
}

func (b body) flag(key string, v *bool) {
	if v != nil {
		b[key] = *v
	}
}

func (b body) integer(key string, v *int) {
	if v != nil {
		b[key] = *v
	}
}

// id sets key to the normalised id of the argument; nil and "" are no value
// (Python: `UUID(x) if x else None`), and a bad id is a refusal.
func (b body) id(key string, v *string) error {
	if v == nil || *v == "" {
		return nil
	}
	id, err := parseID(key, *v)
	if err != nil {
		return refuseArg(err)
	}
	b[key] = id.String()
	return nil
}

// lineArgs are the call's line items as body lines: copies, with an empty or
// null item_id removed (Python: `if ln.get("item_id")`) and a given one
// normalised as Python's UUID() reads it. Never nil: no lines is [].
func lineArgs(raw []map[string]any) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(raw))
	for i, ln := range raw {
		c := maps.Clone(ln)
		if c == nil {
			c = map[string]any{}
		}
		switch v := c["item_id"].(type) {
		case nil:
			delete(c, "item_id")
		case string:
			if v == "" {
				delete(c, "item_id")
				break
			}
			id, err := parseID(fmt.Sprintf("line_items.%d.item_id", i), v)
			if err != nil {
				return nil, refuseArg(err)
			}
			c["item_id"] = id.String()
		}
		out = append(out, c)
	}
	return out, nil
}

// jsonNames are the sorted JSON names of the fields of struct type t,
// embedded structs flattened: the fields an update body understands.
func jsonNames(t reflect.Type) []string {
	var names []string
	var walk func(reflect.Type)
	walk = func(t reflect.Type) {
		for i := range t.NumField() {
			f := t.Field(i)
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if f.Anonymous && name == "" {
				ft := f.Type
				for ft.Kind() == reflect.Pointer {
					ft = ft.Elem()
				}
				if ft.Kind() == reflect.Struct {
					walk(ft)
				}
				continue
			}
			if !f.IsExported() || name == "-" {
				continue
			}
			if name == "" {
				name = f.Name
			}
			names = append(names, name)
		}
	}
	walk(t)
	slices.Sort(names)
	return names
}

// unknownKeys is the refusal for changes keys that valid does not hold, or
// nil: Python's reject_unknown_changes sentence. It runs before anything
// else, so a near-miss key is reported as one even when the id is also wrong.
func unknownKeys(changes map[string]any, valid []string) error {
	var unknown []string
	for k := range changes {
		if !slices.Contains(valid, k) {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	slices.Sort(unknown)
	return Refuse("unknown field(s): %s. Valid fields: %s", strings.Join(unknown, ", "), strings.Join(valid, ", "))
}

// updateBody is the common start of an update tool: the unknown-key check,
// then the id, then the changes decoded as the route's PATCH body.
func updateBody[B any](ctx context.Context, valid []string, idName, rawID string, changes map[string]any, decode func(context.Context, map[string]any) (B, error)) (uuid.UUID, B, error) {
	var zero B
	if err := unknownKeys(changes, valid); err != nil {
		return uuid.Nil, zero, err
	}
	id, err := parseID(idName, rawID)
	if err != nil {
		return uuid.Nil, zero, refuseArg(err)
	}
	b, err := decode(ctx, changes)
	if err != nil {
		return uuid.Nil, zero, err
	}
	return id, b, nil
}

// constraintSentence is the refusal sentence of a database error that a
// client's input causes, or "" for any other. The services map the
// constraints a request can reach to their own sentences (sku already
// exists, vendor not found); this is the net under the rest, so that no
// constraint or column name and no driver text ever reaches a client.
func constraintSentence(pg *pgconn.PgError) string {
	switch pg.Code {
	case "23505":
		return "a record with the same value already exists"
	case "23503":
		return "a referenced record does not exist"
	case "23502":
		return "a required value is missing"
	case "23514", "23P01":
		return "a value is not allowed"
	case "22001", "22003", "22P02":
		return "a value is too long, out of range or malformed"
	}
	return ""
}

// refuseDomain is err as the refusal a client reads, when it is one: an
// error of the HTTP layer with a client status (a field's 422, a 400, a
// 409), a refusal of a service (bills, invoices, quotations, payments,
// ledger, fx), or a violated constraint. Anything else comes back unchanged
// for the helper to log and answer generically.
func refuseDomain(err error) error {
	if err == nil {
		return nil
	}
	var ref *Refusal
	if errors.As(err, &ref) {
		return err
	}
	var api *apierr.Error
	if errors.As(err, &api) && api.Status < http.StatusInternalServerError {
		if len(api.Details) == 1 && api.Details[0].Field != "" {
			return Refuse("%s: %s", api.Details[0].Field, api.Details[0].Message.String())
		}
		return Refuse("%s", api.Error())
	}
	for _, sentinel := range []error{bills.ErrBilling, invoices.ErrInvoicing, quotations.ErrQuoting, payments.ErrPayment, ledger.ErrLedger, fx.ErrRateMissing} {
		if errors.Is(err, sentinel) {
			return Refuse("%s", err.Error())
		}
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		if s := constraintSentence(pg); s != "" {
			return Refuse("%s", s)
		}
	}
	return err
}

// writeErr is err of a write of the row id of kind: "no such row" is
// Python's "<kind> <id> not found", the rest as refuseDomain. The callers
// pass the id as Python printed it: the canonical lowercase UUID where the
// Python tool handed UUID(id) to its service (customer, item, vendor,
// invoice, project, quotation), the raw argument where it formatted the
// string (bill).
func writeErr(err error, kind, id string) error {
	if noRows(err) {
		return Refuse("%s %s not found", kind, id)
	}
	return refuseDomain(err)
}

// notifyDrafts hands committed drafts to n, as invoices does after a scan or
// the trigger route: with the call's values but not its cancellation, and a
// panicking notifier is logged, never the caller's failure.
func notifyDrafts(ctx context.Context, n invoices.DraftNotifier, drafts []invoices.DraftSummary) {
	if len(drafts) == 0 {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			slog.ErrorContext(ctx, "draft notifier failed", "drafts", len(drafts), "panic", r)
		}
	}()
	n.NotifyNewDrafts(ctx, drafts)
}

// registerWriteTools adds the 19 write tools, in Python's tools/list order.
func registerWriteTools(s *mcp.Server, d Deps) {
	dec := newDecoder(d.Cfg)
	notifier := d.Notifier
	if notifier == nil {
		notifier = invoices.LogNotifier{}
	}

	decBillCreate := decodeInto[bills.BillCreate](dec, "create_bill")
	decBillUpdate := decodeInto[bills.BillUpdate](dec, "update_bill")
	decItemCreate := decodeInto[items.ItemCreate](dec, "create_item")
	decItemUpdate := decodeInto[items.ItemUpdate](dec, "update_item")
	decVendorCreate := decodeInto[vendors.VendorCreate](dec, "create_vendor")
	decVendorUpdate := decodeInto[vendors.VendorUpdate](dec, "update_vendor")
	decCustomerCreate := decodeInto[customers.CustomerCreate](dec, "create_customer")
	decCustomerUpdate := decodeInto[customers.CustomerUpdate](dec, "update_customer")
	decInvoiceCreate := decodeInto[invoices.InvoiceCreate](dec, "create_invoice")
	decInvoiceUpdate := decodeInto[invoices.InvoiceUpdate](dec, "update_invoice")
	decTemplate := decodeInto[invoices.RecurringTemplateCreate](dec, "create_recurring_template")
	decProjectCreate := decodeInto[projects.ProjectCreate](dec, "create_project")
	decProjectUpdate := decodeInto[projects.ProjectUpdate](dec, "update_project")
	decQuotationCreate := decodeInto[quotations.QuotationCreate](dec, "create_quotation")
	decQuotationUpdate := decodeInto[quotations.QuotationUpdate](dec, "update_quotation")

	validBill := jsonNames(reflect.TypeFor[bills.BillUpdate]())
	validItem := jsonNames(reflect.TypeFor[items.ItemUpdate]())
	validVendor := jsonNames(reflect.TypeFor[vendors.VendorUpdate]())
	validCustomer := jsonNames(reflect.TypeFor[customers.CustomerUpdate]())
	validInvoice := jsonNames(reflect.TypeFor[invoices.InvoiceUpdate]())
	validProject := jsonNames(reflect.TypeFor[projects.ProjectUpdate]())
	validQuotation := jsonNames(reflect.TypeFor[quotations.QuotationUpdate]())

	// create_bill / update_bill.
	addTool(s, d, writeTool("create_bill", descCreateBill, toolInput(
		p("vendor_id", tStr), p("currency", tStr), p("line_items", tLines),
		p("project_id", tNStr), p("bill_number", tNStr), p("po_number", tNStr), p("issue_date", tNStr), p("due_date", tNStr),
		p("payment_terms", tNStr), p("notes", tNStr), p("discount_type", tNStr), p("discount_value", tNStr),
	)), func(ctx context.Context, tx pgx.Tx, _ principalRef, in createBillIn) (Object, error) {
		vendor, err := parseID("vendor_id", in.VendorID)
		if err != nil {
			return Object{}, refuseArg(err)
		}
		lines, err := lineArgs(in.LineItems)
		if err != nil {
			return Object{}, err
		}
		b := body{"vendor_id": vendor.String(), "currency": in.Currency, "line_items": lines}
		if err := b.id("project_id", in.ProjectID); err != nil {
			return Object{}, err
		}
		b.text("bill_number", in.BillNumber)
		b.text("po_number", in.PoNumber)
		b.text("issue_date", in.IssueDate)
		b.text("due_date", in.DueDate)
		b.text("payment_terms", in.PaymentTerms)
		b.text("notes", in.Notes)
		b.text("discount_type", in.DiscountType)
		b.number("discount_value", in.DiscountVal)
		create, err := decBillCreate(ctx, b)
		if err != nil {
			return Object{}, err
		}
		out, err := bills.CreateBill(ctx, tx, create)
		if err != nil {
			return Object{}, refuseDomain(err)
		}
		return pick(out, billDetailFields)
	})

	addTool(s, d, writeTool("update_bill", descUpdateBill, toolInput(p("bill_id", tStr), p("changes", tDict))),
		func(ctx context.Context, tx pgx.Tx, _ principalRef, in updateBillIn) (Object, error) {
			id, upd, err := updateBody(ctx, validBill, "bill_id", in.BillID, in.Changes, decBillUpdate)
			if err != nil {
				return Object{}, err
			}
			out, err := bills.UpdateBill(ctx, tx, id, upd)
			if err != nil {
				return Object{}, writeErr(err, "bill", in.BillID)
			}
			return pick(out, billDetailFields)
		})

	// create_item / update_item.
	addTool(s, d, writeTool("create_item", descCreateItem, toolInput(
		p("name", tStr), p("item_type", tStr), p("sku", tNStr), p("description", tNStr),
		pd("default_currency", "string", `"IDR"`), p("default_unit_price", tNStr), p("default_purchase_price", tNStr),
		p("revenue_account_id", tNInt), p("expense_account_id", tNInt),
		pd("is_sold", "boolean", "true"), pd("is_purchased", "boolean", "false"), pd("active", "boolean", "true"),
	)), func(ctx context.Context, tx pgx.Tx, _ principalRef, in createItemIn) (Object, error) {
		b := body{
			"name": in.Name, "item_type": in.ItemType, "default_currency": in.DefaultCurrency,
			"is_sold": in.IsSold, "is_purchased": in.IsPurchased, "active": in.Active,
		}
		b.text("sku", in.Sku)
		b.text("description", in.Description)
		b.number("default_unit_price", in.DefaultUnitPrice)
		b.number("default_purchase_price", in.DefaultPurchase)
		b.integer("revenue_account_id", in.RevenueAccountID)
		b.integer("expense_account_id", in.ExpenseAccountID)
		create, err := decItemCreate(ctx, b)
		if err != nil {
			return Object{}, err
		}
		out, err := items.Create(ctx, itemsdb.New(tx), create)
		if err != nil {
			return Object{}, refuseDomain(err)
		}
		return pick(out, itemDetailFields)
	})

	addTool(s, d, writeTool("update_item", descUpdateItem, toolInput(p("item_id", tStr), p("changes", tDict))),
		func(ctx context.Context, tx pgx.Tx, _ principalRef, in updateItemIn) (Object, error) {
			id, upd, err := updateBody(ctx, validItem, "item_id", in.ItemID, in.Changes, decItemUpdate)
			if err != nil {
				return Object{}, err
			}
			out, err := items.Update(ctx, itemsdb.New(tx), id, upd)
			if err != nil {
				return Object{}, writeErr(err, "item", id.String())
			}
			return pick(out, itemDetailFields)
		})

	// create_vendor / update_vendor.
	addTool(s, d, writeTool("create_vendor", descCreateVendor, toolInput(
		p("name", tStr), p("contact_email", tNStr), p("contact_name", tNStr), pd("default_currency", "string", `"IDR"`),
		pd("payment_terms_days", "integer", "30"), p("tax_id", tNStr), p("notes", tNStr), pd("active", "boolean", "true"),
	)), func(ctx context.Context, tx pgx.Tx, _ principalRef, in createVendorIn) (Object, error) {
		b := body{"name": in.Name, "default_currency": in.DefaultCurrency, "payment_terms_days": in.PaymentTermsDays, "active": in.Active}
		b.text("contact_email", in.ContactEmail)
		b.text("contact_name", in.ContactName)
		b.text("tax_id", in.TaxID)
		b.text("notes", in.Notes)
		create, err := decVendorCreate(ctx, b)
		if err != nil {
			return Object{}, err
		}
		out, err := vendors.Create(ctx, vendorsdb.New(tx), create)
		if err != nil {
			return Object{}, refuseDomain(err)
		}
		return pick(out, vendorDetailFields)
	})

	addTool(s, d, writeTool("update_vendor", descUpdateVendor, toolInput(p("vendor_id", tStr), p("changes", tDict))),
		func(ctx context.Context, tx pgx.Tx, _ principalRef, in updateVendorIn) (Object, error) {
			id, upd, err := updateBody(ctx, validVendor, "vendor_id", in.VendorID, in.Changes, decVendorUpdate)
			if err != nil {
				return Object{}, err
			}
			out, err := vendors.Update(ctx, vendorsdb.New(tx), id, upd)
			if err != nil {
				return Object{}, writeErr(err, "vendor", id.String())
			}
			return pick(out, vendorDetailFields)
		})

	// create_customer / update_customer.
	addTool(s, d, writeTool("create_customer", descCreateCustomer, toolInput(join(
		[]prop{p("name", tStr), p("matching_aliases", tNStrs)},
		nstrs("contact_first_name", "contact_last_name", "contact_email", "contact_phone", "contact_phone_2", "account_number",
			"website", "notes", "default_currency", "default_wht_type", "default_wht_rate"),
		[]prop{p("default_wht_gross_up", tNBool)},
		nstrs("billing_address1", "billing_address2", "billing_city", "billing_state", "billing_postal_code", "billing_country",
			"ship_to_name", "shipping_address1", "shipping_address2", "shipping_city", "shipping_state", "shipping_postal_code",
			"shipping_country", "shipping_phone", "shipping_delivery_instructions"),
	)...)), func(ctx context.Context, tx pgx.Tx, _ principalRef, in createCustomerIn) (Object, error) {
		// Python parsed the rate itself, ahead of the model, and answered its
		// own sentence for an unparseable one.
		if in.DefaultWhtRate != nil && *in.DefaultWhtRate != "" {
			if _, err := decimal.NewFromString(*in.DefaultWhtRate); err != nil {
				return Object{}, Refuse("invalid default_wht_rate: %s", platform.PyRepr(*in.DefaultWhtRate))
			}
		}
		aliases := in.MatchingAliases
		if aliases == nil {
			aliases = []string{}
		}
		b := body{"name": in.Name, "matching_aliases": aliases}
		for _, f := range []struct {
			key string
			v   *string
		}{
			{"contact_first_name", in.ContactFirstName},
			{"contact_last_name", in.ContactLastName},
			{"contact_email", in.ContactEmail},
			{"contact_phone", in.ContactPhone},
			{"contact_phone_2", in.ContactPhone2},
			{"account_number", in.AccountNumber},
			{"website", in.Website},
			{"notes", in.Notes},
			{"default_currency", in.DefaultCurrency},
			{"default_wht_type", in.DefaultWhtType},
			{"billing_address1", in.BillingAddress1},
			{"billing_address2", in.BillingAddress2},
			{"billing_city", in.BillingCity},
			{"billing_state", in.BillingState},
			{"billing_postal_code", in.BillingPostal},
			{"billing_country", in.BillingCountry},
			{"ship_to_name", in.ShipToName},
			{"shipping_address1", in.ShippingAddress1},
			{"shipping_address2", in.ShippingAddress2},
			{"shipping_city", in.ShippingCity},
			{"shipping_state", in.ShippingState},
			{"shipping_postal_code", in.ShippingPostal},
			{"shipping_country", in.ShippingCountry},
			{"shipping_phone", in.ShippingPhone},
			{"shipping_delivery_instructions", in.ShippingDelivery},
		} {
			b.text(f.key, f.v)
		}
		b.number("default_wht_rate", in.DefaultWhtRate)
		b.flag("default_wht_gross_up", in.DefaultWhtGross)
		create, err := decCustomerCreate(ctx, b)
		if err != nil {
			return Object{}, err
		}
		out, err := customers.Create(ctx, customersdb.New(tx), create)
		if err != nil {
			return Object{}, refuseDomain(err)
		}
		return pick(out, customerDetailFields)
	})

	addTool(s, d, writeTool("update_customer", descUpdateCustomer, toolInput(p("customer_id", tStr), p("changes", tDict))),
		func(ctx context.Context, tx pgx.Tx, _ principalRef, in updateCustomerIn) (Object, error) {
			id, upd, err := updateBody(ctx, validCustomer, "customer_id", in.CustomerID, in.Changes, decCustomerUpdate)
			if err != nil {
				return Object{}, err
			}
			out, err := customers.Update(ctx, customersdb.New(tx), id, upd)
			if err != nil {
				return Object{}, writeErr(err, "customer", id.String())
			}
			return pick(out, customerDetailFields)
		})

	// The invoice tools.
	addTool(s, d, writeTool("create_invoice", descCreateInvoice, toolInput(
		p("customer_id", tStr), p("currency", tStr), p("line_items", tLines), pd("invoice_type", "string", `"MILESTONE"`),
		p("discount_type", tNStr), p("discount_value", tNStr), p("payment_terms", tNStr), p("notes", tNStr),
		p("wht_type", tNStr), p("wht_rate", tNStr), p("wht_gross_up", tNBool),
	)), func(ctx context.Context, tx pgx.Tx, _ principalRef, in createInvoiceIn) (Object, error) {
		customer, err := parseID("customer_id", in.CustomerID)
		if err != nil {
			return Object{}, refuseArg(err)
		}
		lines, err := lineArgs(in.LineItems)
		if err != nil {
			return Object{}, err
		}
		b := body{"customer_id": customer.String(), "currency": in.Currency, "invoice_type": in.InvoiceType, "line_items": lines}
		b.text("discount_type", in.DiscountType)
		b.number("discount_value", in.DiscountVal)
		b.text("payment_terms", in.PaymentTerms)
		b.text("notes", in.Notes)
		b.text("wht_type", in.WhtType)
		b.number("wht_rate", in.WhtRate)
		b.flag("wht_gross_up", in.WhtGrossUp)
		create, err := decInvoiceCreate(ctx, b)
		if err != nil {
			return Object{}, err
		}
		out, err := invoices.CreateInvoice(ctx, tx, create, platform.TodayUTC(d.Clock))
		if err != nil {
			return Object{}, refuseDomain(err)
		}
		return pick(out, invoiceResultFields)
	})

	addTool(s, d, writeTool("update_invoice", descUpdateInvoice, toolInput(p("invoice_id", tStr), p("changes", tDict))),
		func(ctx context.Context, tx pgx.Tx, _ principalRef, in updateInvoiceIn) (Object, error) {
			id, upd, err := updateBody(ctx, validInvoice, "invoice_id", in.InvoiceID, in.Changes, decInvoiceUpdate)
			if err != nil {
				return Object{}, err
			}
			out, err := invoices.UpdateInvoice(ctx, tx, id, upd)
			if err != nil {
				return Object{}, writeErr(err, "invoice", id.String())
			}
			return pick(out, invoiceResultFields)
		})

	addTool(s, d, writeTool("finalize_invoice", descFinalize, idInput("invoice_id")),
		func(ctx context.Context, tx pgx.Tx, _ principalRef, in invoiceIDIn) (Object, error) {
			id, err := parseID("invoice_id", in.InvoiceID)
			if err != nil {
				return Object{}, refuseArg(err)
			}
			out, err := invoices.FinalizeInvoice(ctx, tx, id)
			if err != nil {
				return Object{}, writeErr(err, "invoice", id.String())
			}
			return pick(out, invoiceResultFields)
		})

	addTool(s, d, writeTool("void_invoice", descVoid, idInput("invoice_id")),
		func(ctx context.Context, tx pgx.Tx, _ principalRef, in invoiceIDIn) (Object, error) {
			id, err := parseID("invoice_id", in.InvoiceID)
			if err != nil {
				return Object{}, refuseArg(err)
			}
			out, err := invoices.VoidInvoice(ctx, tx, id)
			if err != nil {
				return Object{}, writeErr(err, "invoice", id.String())
			}
			return pick(out, invoiceResultFields)
		})

	addTool(s, d, writeTool("create_recurring_template", descCreateTemplate, toolInput(
		p("customer_id", tStr), p("currency", tStr), p("line_items", tLines), p("schedule", tDict), p("payment_terms", tStr),
		p("project_id", tNStr), p("po_so_number", tNStr), p("notes", tNStr), p("footer", tNStr), p("discount_type", tNStr),
		p("discount_value", tNStr), p("wht_type", tNStr), p("wht_rate", tNStr), p("wht_gross_up", tNBool),
	)), func(ctx context.Context, tx pgx.Tx, _ principalRef, in createTemplateIn) (Object, error) {
		lines, err := lineArgs(in.LineItems)
		if err != nil {
			return Object{}, err
		}
		schedule := in.Schedule
		if schedule == nil {
			schedule = map[string]any{}
		}
		b := body{"currency": in.Currency, "payment_terms": in.PaymentTerms, "line_items": lines, "schedule": schedule}
		// Python: `UUID(customer_id) if customer_id else None`.
		if err := b.id("customer_id", &in.CustomerID); err != nil {
			return Object{}, err
		}
		if err := b.id("project_id", in.ProjectID); err != nil {
			return Object{}, err
		}
		b.text("po_so_number", in.PoSoNumber)
		b.text("notes", in.Notes)
		b.text("footer", in.Footer)
		b.text("discount_type", in.DiscountType)
		b.number("discount_value", in.DiscountVal)
		b.text("wht_type", in.WhtType)
		b.number("wht_rate", in.WhtRate)
		b.flag("wht_gross_up", in.WhtGrossUp)
		create, err := decTemplate(ctx, b)
		if err != nil {
			return Object{}, err
		}
		out, err := invoices.CreateTemplate(ctx, tx, create)
		if err != nil {
			return Object{}, refuseDomain(err)
		}
		return pick(out, invoiceResultFields)
	})

	// trigger_recurring commits in its own transaction (TriggerAndSummarise),
	// so it is registered withoutTx; the notification waits for the commit
	// through afterCommit as the others' side effects do.
	addTool(s, d, writeTool("trigger_recurring", descTrigger, toolInput(p("template_id", tStr), p("cycle_key", tStr))),
		func(ctx context.Context, _ pgx.Tx, _ principalRef, in triggerIn) (Object, error) {
			id, err := parseID("template_id", in.TemplateID)
			if err != nil {
				return Object{}, refuseArg(err)
			}
			out, summary, err := invoices.TriggerAndSummarise(ctx, d.Pool, id, in.CycleKey, platform.TodayUTC(d.Clock))
			if err != nil {
				return Object{}, writeErr(err, "invoice", in.TemplateID)
			}
			if summary != nil {
				afterCommit(ctx, func(ctx context.Context) {
					notifyDrafts(ctx, notifier, []invoices.DraftSummary{*summary})
				})
			}
			return pick(out, invoiceResultFields)
		}, withoutTx())

	// The project tools.
	addTool(s, d, writeTool("create_project", descCreateProject, toolInput(
		p("customer_id", tStr), p("code", tStr), p("name", tStr), p("currency", tStr), p("contract_value", tNStr),
		pd("status", "string", `"ACTIVE"`), p("start_date", tNStr), p("end_date", tNStr), p("notes", tNStr),
	)), func(ctx context.Context, tx pgx.Tx, _ principalRef, in createProjectIn) (Object, error) {
		customer, err := parseID("customer_id", in.CustomerID)
		if err != nil {
			return Object{}, refuseArg(err)
		}
		b := body{"customer_id": customer.String(), "code": in.Code, "name": in.Name, "currency": in.Currency, "status": in.Status}
		b.number("contract_value", in.ContractValue)
		b.text("start_date", in.StartDate)
		b.text("end_date", in.EndDate)
		b.text("notes", in.Notes)
		create, err := decProjectCreate(ctx, b)
		if err != nil {
			return Object{}, err
		}
		out, err := projects.Create(ctx, projectsdb.New(tx), create)
		if err != nil {
			return Object{}, refuseDomain(err)
		}
		return pick(out, projectDetailFields)
	})

	addTool(s, d, writeTool("update_project", descUpdateProject, toolInput(p("project_id", tStr), p("changes", tDict))),
		func(ctx context.Context, tx pgx.Tx, _ principalRef, in updateProjectIn) (Object, error) {
			id, upd, err := updateBody(ctx, validProject, "project_id", in.ProjectID, in.Changes, decProjectUpdate)
			if err != nil {
				return Object{}, err
			}
			out, err := projects.Update(ctx, projectsdb.New(tx), id, upd)
			if err != nil {
				return Object{}, writeErr(err, "project", id.String())
			}
			return pick(out, projectDetailFields)
		})

	// The quotation tools.
	addTool(s, d, writeTool("create_quotation", descCreateQuote, toolInput(
		p("customer_id", tStr), p("currency", tStr), p("line_items", tLines), p("project_id", tNStr), p("po_so_number", tNStr),
		p("issue_date", tNStr), p("valid_until", tNStr), p("payment_terms", tNStr), p("notes", tNStr), p("footer", tNStr),
		p("discount_type", tNStr), p("discount_value", tNStr),
	)), func(ctx context.Context, tx pgx.Tx, _ principalRef, in createQuotationIn) (Object, error) {
		customer, err := parseID("customer_id", in.CustomerID)
		if err != nil {
			return Object{}, refuseArg(err)
		}
		lines, err := lineArgs(in.LineItems)
		if err != nil {
			return Object{}, err
		}
		b := body{"customer_id": customer.String(), "currency": in.Currency, "line_items": lines}
		if err := b.id("project_id", in.ProjectID); err != nil {
			return Object{}, err
		}
		b.text("po_so_number", in.PoSoNumber)
		b.text("issue_date", in.IssueDate)
		b.text("valid_until", in.ValidUntil)
		b.text("payment_terms", in.PaymentTerms)
		b.text("notes", in.Notes)
		b.text("footer", in.Footer)
		b.text("discount_type", in.DiscountType)
		b.number("discount_value", in.DiscountVal)
		create, err := decQuotationCreate(ctx, b)
		if err != nil {
			return Object{}, err
		}
		out, err := quotations.CreateQuotation(ctx, tx, create, platform.TodayUTC(d.Clock))
		if err != nil {
			return Object{}, refuseDomain(err)
		}
		return pick(out, quotationDetailFields)
	})

	addTool(s, d, writeTool("update_quotation", descUpdateQuote, toolInput(p("quotation_id", tStr), p("changes", tDict))),
		func(ctx context.Context, tx pgx.Tx, _ principalRef, in updateQuotationIn) (Object, error) {
			id, upd, err := updateBody(ctx, validQuotation, "quotation_id", in.QuotationID, in.Changes, decQuotationUpdate)
			if err != nil {
				return Object{}, err
			}
			out, err := quotations.UpdateQuotation(ctx, tx, id, upd)
			if err != nil {
				return Object{}, writeErr(err, "quotation", id.String())
			}
			return pick(out, quotationDetailFields)
		})

	// resolve_reconciliation_match: the safe-stop wrapper of the admin
	// override. The payment and the invoice are read under their locks, so
	// the checks and the approval see the same rows.
	addTool(s, d, writeTool("resolve_reconciliation_match", descResolve, toolInput(p("payment_id", tStr), p("invoice_id", tStr))),
		func(ctx context.Context, tx pgx.Tx, who principalRef, in resolveIn) (Object, error) {
			paymentID, err := parseID("payment_id", in.PaymentID)
			if err != nil {
				return Object{}, refuseArg(err)
			}
			invoiceID, err := parseID("invoice_id", in.InvoiceID)
			if err != nil {
				return Object{}, refuseArg(err)
			}
			q := paymentsdb.New(tx)
			pay, err := q.LockPayment(ctx, paymentID)
			if noRows(err) {
				return Object{}, Refuse("payment %s not found", in.PaymentID)
			}
			if err != nil {
				return Object{}, err
			}
			// Go change 14: only a payment in the review queue (Python
			// approved a FLAGGED one, though the tool says "PENDING").
			if pay.Status != matching.StatusPending {
				return Object{}, Refuse("payment %s is %s, not awaiting review", in.PaymentID, pay.Status)
			}
			inv, err := q.LockInvoice(ctx, invoiceID)
			if noRows(err) {
				return Object{}, Refuse("invoice %s not found", in.InvoiceID)
			}
			if err != nil {
				return Object{}, err
			}
			// Safe-stop: only an exact currency and amount match resolves
			// here; the predicate is the matching engine's own.
			exact := matching.IsExactMatch(
				matching.Incoming{Amount: pay.Amount, Currency: pay.Currency},
				matching.Candidate{Currency: inv.Currency, BalanceDue: inv.BalanceDue, WhtOutstanding: inv.WhtAmount.Sub(inv.WhtBooked)},
			)
			if !exact {
				if platform.PyUpper(pay.Currency) != platform.PyUpper(inv.Currency) {
					return Object{}, Refuse("currency mismatch: payment %s vs invoice %s — refusing autonomous resolution, route to manual review", pay.Currency, inv.Currency)
				}
				return Object{}, Refuse("amount mismatch: payment %s vs invoice expected cash %s — refusing autonomous resolution, route to manual review",
					platform.PyDecimalStr(pay.Amount), platform.PyDecimalStr(withholding.ExpectedCash(inv.BalanceDue, inv.WhtAmount, inv.WhtBooked)))
			}
			actor := who.UserID
			cleared, err := d.Payments.ApproveManual(ctx, tx, paymentID, invoiceID, &actor, matching.AdjNone)
			if err != nil {
				return Object{}, refuseDomain(err)
			}
			if d.AfterPayment != nil {
				afterCommit(ctx, func(ctx context.Context) { d.AfterPayment(ctx, cleared) })
			}
			return pick(payments.PaymentOutFrom(cleared), paymentFields)
		})
}
