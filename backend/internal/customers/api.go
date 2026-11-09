// Package customers serves the customer master data at /api/v1/customers
// with the Python backend's contract: list (optionally by active), create,
// read, partial update, and deactivate. A customer carries its default
// withholding tax (copied onto new invoices) and the CC addresses the send
// dialogs prefill. Customers are never deleted.
package customers

import (
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/customers/customersdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// CustomerOut is a customer, as every route answers, in the field order of
// Python's CustomerOut. The lists are [] when empty, never null.
type CustomerOut struct {
	CustomerID      uuid.UUID `json:"customer_id"`
	Name            string    `json:"name"`
	MatchingAliases []string  `json:"matching_aliases" nullable:"false"`
	Active          bool      `json:"active"`

	ContactFirstName  *string  `json:"contact_first_name"`
	ContactLastName   *string  `json:"contact_last_name"`
	ContactEmail      *string  `json:"contact_email"`
	QuotationCcEmails []string `json:"quotation_cc_emails" nullable:"false"`
	BillingCcEmails   []string `json:"billing_cc_emails" nullable:"false"`
	ContactPhone      *string  `json:"contact_phone"`
	ContactPhone2     *string  `json:"contact_phone_2"`

	AccountNumber *string `json:"account_number"`
	Website       *string `json:"website"`
	Notes         *string `json:"notes"`

	DefaultCurrency   string  `json:"default_currency"`
	BillingAddress    *string `json:"billing_address"`
	BillingAddress1   *string `json:"billing_address1"`
	BillingAddress2   *string `json:"billing_address2"`
	BillingCountry    *string `json:"billing_country"`
	BillingState      *string `json:"billing_state"`
	BillingCity       *string `json:"billing_city"`
	BillingPostalCode *string `json:"billing_postal_code"`

	ShipToName                   *string `json:"ship_to_name"`
	ShippingAddress1             *string `json:"shipping_address1"`
	ShippingAddress2             *string `json:"shipping_address2"`
	ShippingCountry              *string `json:"shipping_country"`
	ShippingState                *string `json:"shipping_state"`
	ShippingCity                 *string `json:"shipping_city"`
	ShippingPostalCode           *string `json:"shipping_postal_code"`
	ShippingPhone                *string `json:"shipping_phone"`
	ShippingDeliveryInstructions *string `json:"shipping_delivery_instructions"`

	DefaultWhtType    string         `json:"default_wht_type"`
	DefaultWhtRate    *platform.Dec4 `json:"default_wht_rate"`
	DefaultWhtGrossUp bool           `json:"default_wht_gross_up"`
}

func customerOut(c customersdb.Customer) CustomerOut {
	var rate *platform.Dec4
	if c.DefaultWhtRate.Valid {
		rate = &platform.Dec4{Decimal: c.DefaultWhtRate.Decimal}
	}
	return CustomerOut{
		CustomerID: c.CustomerID, Name: c.Name, MatchingAliases: nonNil(c.MatchingAliases), Active: c.Active,
		ContactFirstName: c.ContactFirstName, ContactLastName: c.ContactLastName, ContactEmail: c.ContactEmail,
		QuotationCcEmails: nonNil(c.QuotationCcEmails), BillingCcEmails: nonNil(c.BillingCcEmails),
		ContactPhone: c.ContactPhone, ContactPhone2: c.ContactPhone2,
		AccountNumber: c.AccountNumber, Website: c.Website, Notes: c.Notes,
		DefaultCurrency: c.DefaultCurrency, BillingAddress: c.BillingAddress, BillingAddress1: c.BillingAddress1,
		BillingAddress2: c.BillingAddress2, BillingCountry: c.BillingCountry, BillingState: c.BillingState,
		BillingCity: c.BillingCity, BillingPostalCode: c.BillingPostalCode,
		ShipToName: c.ShipToName, ShippingAddress1: c.ShippingAddress1, ShippingAddress2: c.ShippingAddress2,
		ShippingCountry: c.ShippingCountry, ShippingState: c.ShippingState, ShippingCity: c.ShippingCity,
		ShippingPostalCode: c.ShippingPostalCode, ShippingPhone: c.ShippingPhone,
		ShippingDeliveryInstructions: c.ShippingDeliveryInstructions,
		DefaultWhtType:               c.DefaultWhtType, DefaultWhtRate: rate, DefaultWhtGrossUp: c.DefaultWhtGrossUp,
	}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// CustomerFields are the fields a create and a PATCH share: each changes its
// column when sent and leaves it alone when left out. A null clears a
// nullable column and is a 422 for the others. On create, active defaults to
// true, default_currency to IDR, the lists to [] and the withholding to NONE.
type CustomerFields struct {
	MatchingAliases platform.NotNull[[]string] `json:"matching_aliases" nullable:"false" doc:"Defaults to []."`
	Active          platform.NotNull[bool]     `json:"active" doc:"Defaults to true."`

	ContactFirstName  platform.Optional[string]         `json:"contact_first_name" maxLength:"120"`
	ContactLastName   platform.Optional[string]         `json:"contact_last_name" maxLength:"120"`
	ContactEmail      platform.Optional[platform.Email] `json:"contact_email" maxLength:"255"`
	QuotationCcEmails platform.NotNull[[]string]        `json:"quotation_cc_emails" nullable:"false" doc:"CC addresses for quotations. Blanks are dropped, each is trimmed and must be an email address, and a repeat (any case) is dropped. Defaults to []."`
	BillingCcEmails   platform.NotNull[[]string]        `json:"billing_cc_emails" nullable:"false" doc:"CC addresses for invoices and payment receipts, kept as quotation_cc_emails is. Defaults to []."`
	ContactPhone      platform.Optional[string]         `json:"contact_phone" maxLength:"64"`
	ContactPhone2     platform.Optional[string]         `json:"contact_phone_2" maxLength:"64"`

	AccountNumber platform.Optional[string] `json:"account_number" maxLength:"255"`
	Website       platform.Optional[string] `json:"website" maxLength:"255"`
	Notes         platform.Optional[string] `json:"notes"`

	DefaultCurrency   platform.NotNull[string]  `json:"default_currency" minLength:"3" maxLength:"3" doc:"Defaults to IDR."`
	BillingAddress    platform.Optional[string] `json:"billing_address" doc:"Legacy unstructured address."`
	BillingAddress1   platform.Optional[string] `json:"billing_address1" maxLength:"255"`
	BillingAddress2   platform.Optional[string] `json:"billing_address2" maxLength:"255"`
	BillingCountry    platform.Optional[string] `json:"billing_country" maxLength:"100"`
	BillingState      platform.Optional[string] `json:"billing_state" maxLength:"255"`
	BillingCity       platform.Optional[string] `json:"billing_city" maxLength:"255"`
	BillingPostalCode platform.Optional[string] `json:"billing_postal_code" maxLength:"32"`

	ShipToName                   platform.Optional[string] `json:"ship_to_name" maxLength:"255"`
	ShippingAddress1             platform.Optional[string] `json:"shipping_address1" maxLength:"255"`
	ShippingAddress2             platform.Optional[string] `json:"shipping_address2" maxLength:"255"`
	ShippingCountry              platform.Optional[string] `json:"shipping_country" maxLength:"100"`
	ShippingState                platform.Optional[string] `json:"shipping_state" maxLength:"255"`
	ShippingCity                 platform.Optional[string] `json:"shipping_city" maxLength:"255"`
	ShippingPostalCode           platform.Optional[string] `json:"shipping_postal_code" maxLength:"32"`
	ShippingPhone                platform.Optional[string] `json:"shipping_phone" maxLength:"64"`
	ShippingDeliveryInstructions platform.Optional[string] `json:"shipping_delivery_instructions"`

	DefaultWhtType    platform.NotNull[string]         `json:"default_wht_type" maxLength:"16" doc:"NONE, PPH21 or PPH23. Defaults to NONE."`
	DefaultWhtRate    platform.Optional[platform.Dec4] `json:"default_wht_rate" doc:"Percent, above 0 and below 100. Null or left out with a new type: the type's default rate (PPH21 2.5, PPH23 2)."`
	DefaultWhtGrossUp platform.NotNull[bool]           `json:"default_wht_gross_up" doc:"Defaults to false."`
}

// CustomerCreate adds a customer.
type CustomerCreate struct {
	Name string `json:"name" minLength:"1" maxLength:"255"`
	CustomerFields
}

// CustomerUpdate changes the fields sent and leaves the rest.
type CustomerUpdate struct {
	Name platform.NotNull[string] `json:"name" minLength:"1" maxLength:"255"`
	CustomerFields
}

// emailPtr is Optional.Merge for the email field, whose request type is Email
// and whose column is text.
func emailPtr(f platform.Optional[platform.Email], cur *string) *string {
	if !f.Set {
		return cur
	}
	if f.Null {
		return nil
	}
	s := string(f.Value)
	return &s
}

// nullDec is a nullable rate as its column type.
func nullDec(d *decimal.Decimal) decimal.NullDecimal {
	if d == nil {
		return decimal.NullDecimal{}
	}
	return decimal.NullDecimal{Decimal: *d, Valid: true}
}
