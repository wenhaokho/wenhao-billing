package customers

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/customers/customersdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/internal/withholding"
)

// Get is the customer, read through q (a pool or a transaction). An unknown
// id is pgx.ErrNoRows, unwrapped: test it with errors.Is(err, pgx.ErrNoRows).
func Get(ctx context.Context, q *customersdb.Queries, id uuid.UUID) (CustomerOut, error) {
	c, err := q.GetCustomer(ctx, id)
	if err != nil {
		return CustomerOut{}, err
	}
	return customerOut(c), nil
}

// ListFilter narrows List; the zero value lists every customer. Active
// filters when set (nil is every customer, active and inactive); NameLike
// is a name substring, already escaped by the caller for LIKE (nil is no
// filter); Limit caps the page when set (nil is unlimited, 0 lists
// nothing).
type ListFilter struct {
	Active   *bool
	NameLike *string
	Limit    *int32
}

// List is the customers matching f, rendered as the routes render them,
// never nil. Exported for the MCP list_customers and resolve_customer
// tools (inventory §4).
func List(ctx context.Context, q *customersdb.Queries, f ListFilter) ([]CustomerOut, error) {
	rows, err := q.ListCustomers(ctx, customersdb.ListCustomersParams{
		Active: f.Active, NameLike: f.NameLike, RowLimit: f.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]CustomerOut, 0, len(rows))
	for _, c := range rows {
		out = append(out, customerOut(c))
	}
	return out, nil
}

// ccList is Python's EmailList: blank entries dropped, each address trimmed
// and checked, a repeat (compared case-insensitively) dropped so the first
// stays. An address is stored as sent after trimming: Python validated it
// but kept the original string, domain case included. field locates the 422.
func ccList(field string, in []string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for i, addr := range in {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			continue
		}
		if _, ok := platform.NormalizeEmail(addr); !ok {
			return nil, platform.FieldError(field+"."+strconv.Itoa(i), "invalid email address: "+addr)
		}
		if k := strings.ToLower(addr); !seen[k] {
			seen[k] = true
			out = append(out, addr)
		}
	}
	return out, nil
}

// cleanCC replaces each CC list sent with its cleaned form, or is the 422 for
// the first refused address (quotation_cc_emails first, as Python's fields).
func cleanCC(f *CustomerFields) error {
	for _, l := range []struct {
		field string
		list  *platform.NotNull[[]string]
	}{{"quotation_cc_emails", &f.QuotationCcEmails}, {"billing_cc_emails", &f.BillingCcEmails}} {
		if !l.list.Set {
			continue
		}
		clean, err := ccList(l.field, l.list.Value)
		if err != nil {
			return err
		}
		l.list.Value = clean
	}
	return nil
}

// normalizeWHT is Python's _normalize_wht: when any of the three withholding
// fields was sent, the stored values overlaid with the ones sent go through
// withholding.Normalize. A type change without a rate takes the new type's
// default rate; a rate sent as null does too. A type or gross-up sent as
// null counts as sent (Python tests `f in data`) but keeps the stored value
// (Python's `data.get(f)` is None). A rate with more than four decimal
// places never gets here: Dec4 refuses it at the field.
func normalizeWHT(f CustomerFields, c *customersdb.Customer) error {
	if !f.DefaultWhtType.Set && !f.DefaultWhtRate.Set && !f.DefaultWhtGrossUp.Set {
		return nil
	}
	typ, grossUp := c.DefaultWhtType, c.DefaultWhtGrossUp
	if f.DefaultWhtType.Set && !f.DefaultWhtType.Null {
		typ = f.DefaultWhtType.Value
	}
	if f.DefaultWhtGrossUp.Set && !f.DefaultWhtGrossUp.Null {
		grossUp = f.DefaultWhtGrossUp.Value
	}
	var rate *decimal.Decimal
	switch {
	case f.DefaultWhtRate.Set:
		if !f.DefaultWhtRate.Null {
			v := f.DefaultWhtRate.Value.Decimal()
			rate = &v
		}
	case typ != c.DefaultWhtType:
		rate = nil
	case c.DefaultWhtRate.Valid:
		rate = &c.DefaultWhtRate.Decimal
	}
	typ, rate, grossUp, err := withholding.Normalize(typ, rate, grossUp)
	if err != nil {
		return platform.Err(http.StatusUnprocessableEntity, "invalid_withholding", err.Error())
	}
	c.DefaultWhtType, c.DefaultWhtRate, c.DefaultWhtGrossUp = typ, nullDec(rate), grossUp
	return nil
}

// overlay is c with the fields sent written over it and the withholding
// normalised. The CC lists must have been through cleanCC.
func overlay(c customersdb.Customer, f CustomerFields) (customersdb.Customer, error) {
	c.MatchingAliases = nonNil(f.MatchingAliases.Or(c.MatchingAliases))
	c.Active = f.Active.Or(c.Active)
	c.ContactFirstName = f.ContactFirstName.Merge(c.ContactFirstName)
	c.ContactLastName = f.ContactLastName.Merge(c.ContactLastName)
	c.ContactEmail = emailPtr(f.ContactEmail, c.ContactEmail)
	c.QuotationCcEmails = nonNil(f.QuotationCcEmails.Or(c.QuotationCcEmails))
	c.BillingCcEmails = nonNil(f.BillingCcEmails.Or(c.BillingCcEmails))
	c.ContactPhone = f.ContactPhone.Merge(c.ContactPhone)
	c.ContactPhone2 = f.ContactPhone2.Merge(c.ContactPhone2)
	c.AccountNumber = f.AccountNumber.Merge(c.AccountNumber)
	c.Website = f.Website.Merge(c.Website)
	c.Notes = f.Notes.Merge(c.Notes)
	c.DefaultCurrency = f.DefaultCurrency.Or(c.DefaultCurrency)
	c.BillingAddress = f.BillingAddress.Merge(c.BillingAddress)
	c.BillingAddress1 = f.BillingAddress1.Merge(c.BillingAddress1)
	c.BillingAddress2 = f.BillingAddress2.Merge(c.BillingAddress2)
	c.BillingCountry = f.BillingCountry.Merge(c.BillingCountry)
	c.BillingState = f.BillingState.Merge(c.BillingState)
	c.BillingCity = f.BillingCity.Merge(c.BillingCity)
	c.BillingPostalCode = f.BillingPostalCode.Merge(c.BillingPostalCode)
	c.ShipToName = f.ShipToName.Merge(c.ShipToName)
	c.ShippingAddress1 = f.ShippingAddress1.Merge(c.ShippingAddress1)
	c.ShippingAddress2 = f.ShippingAddress2.Merge(c.ShippingAddress2)
	c.ShippingCountry = f.ShippingCountry.Merge(c.ShippingCountry)
	c.ShippingState = f.ShippingState.Merge(c.ShippingState)
	c.ShippingCity = f.ShippingCity.Merge(c.ShippingCity)
	c.ShippingPostalCode = f.ShippingPostalCode.Merge(c.ShippingPostalCode)
	c.ShippingPhone = f.ShippingPhone.Merge(c.ShippingPhone)
	c.ShippingDeliveryInstructions = f.ShippingDeliveryInstructions.Merge(c.ShippingDeliveryInstructions)
	err := normalizeWHT(f, &c)
	return c, err
}

// Create writes a new customer: the fields sent over Python's defaults.
// Exported for the MCP create_customer tool (inventory §4). It checks the
// CC lists, then normalises the withholding and writes the customer.
func Create(ctx context.Context, q *customersdb.Queries, b CustomerCreate) (CustomerOut, error) {
	if err := cleanCC(&b.CustomerFields); err != nil {
		return CustomerOut{}, err
	}
	return create(ctx, q, b)
}

// create writes a new customer: the fields sent over Python's defaults.
func create(ctx context.Context, q *customersdb.Queries, b CustomerCreate) (CustomerOut, error) {
	row, err := overlay(customersdb.Customer{
		CustomerID: uuid.New(), Name: b.Name, Active: true, DefaultCurrency: "IDR", DefaultWhtType: "NONE",
	}, b.CustomerFields)
	if err != nil {
		return CustomerOut{}, err
	}
	c, err := q.CreateCustomer(ctx, customersdb.CreateCustomerParams(row))
	if err != nil {
		return CustomerOut{}, err
	}
	return customerOut(c), nil
}

// Update is the update route's customer: it checks the CC lists before
// looking the customer up, then updates on q, a transaction. Exported for
// the MCP update_customer tool (inventory §4).
func Update(ctx context.Context, q *customersdb.Queries, id uuid.UUID, b CustomerUpdate) (CustomerOut, error) {
	if err := cleanCC(&b.CustomerFields); err != nil {
		return CustomerOut{}, err
	}
	return update(ctx, q, id, b)
}

// update is the PATCH pattern on q, a transaction: read the row locked,
// overlay the fields sent, write every column back. An unknown id is
// pgx.ErrNoRows.
func update(ctx context.Context, q *customersdb.Queries, id uuid.UUID, b CustomerUpdate) (CustomerOut, error) {
	cur, err := q.GetCustomerForUpdate(ctx, id)
	if err != nil {
		return CustomerOut{}, err
	}
	cur.Name = b.Name.Or(cur.Name)
	r, err := overlay(cur, b.CustomerFields)
	if err != nil {
		return CustomerOut{}, err
	}
	c, err := q.UpdateCustomer(ctx, customersdb.UpdateCustomerParams{
		CustomerID: r.CustomerID, Name: r.Name, MatchingAliases: r.MatchingAliases, Active: r.Active,
		ContactEmail: r.ContactEmail, ContactPhone: r.ContactPhone, BillingAddress: r.BillingAddress,
		ContactFirstName: r.ContactFirstName, ContactLastName: r.ContactLastName, ContactPhone2: r.ContactPhone2,
		AccountNumber: r.AccountNumber, Website: r.Website, Notes: r.Notes, DefaultCurrency: r.DefaultCurrency,
		BillingAddress1: r.BillingAddress1, BillingAddress2: r.BillingAddress2, BillingCountry: r.BillingCountry,
		BillingState: r.BillingState, BillingCity: r.BillingCity, BillingPostalCode: r.BillingPostalCode,
		ShipToName: r.ShipToName, ShippingAddress1: r.ShippingAddress1, ShippingAddress2: r.ShippingAddress2,
		ShippingCountry: r.ShippingCountry, ShippingState: r.ShippingState, ShippingCity: r.ShippingCity,
		ShippingPostalCode: r.ShippingPostalCode, ShippingPhone: r.ShippingPhone,
		ShippingDeliveryInstructions: r.ShippingDeliveryInstructions, QuotationCcEmails: r.QuotationCcEmails,
		BillingCcEmails: r.BillingCcEmails, DefaultWhtType: r.DefaultWhtType, DefaultWhtRate: r.DefaultWhtRate,
		DefaultWhtGrossUp: r.DefaultWhtGrossUp,
	})
	if err != nil {
		return CustomerOut{}, err
	}
	return customerOut(c), nil
}
