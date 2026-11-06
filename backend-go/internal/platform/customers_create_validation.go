package platform

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

var customerDecimalPattern = regexp.MustCompile(`^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)

// customerCreate preserves omitted, null, and concrete field values separately.
type customerCreate struct {
	Name                         updateField[string]
	ContactFirstName             updateField[string]
	ContactLastName              updateField[string]
	ContactEmail                 updateField[string]
	ContactPhone                 updateField[string]
	ContactPhone2                updateField[string]
	AccountNumber                updateField[string]
	Website                      updateField[string]
	Notes                        updateField[string]
	DefaultCurrency              updateField[string]
	BillingAddress               updateField[string]
	BillingAddress1              updateField[string]
	BillingAddress2              updateField[string]
	BillingCountry               updateField[string]
	BillingState                 updateField[string]
	BillingCity                  updateField[string]
	BillingPostalCode            updateField[string]
	ShipToName                   updateField[string]
	ShippingAddress1             updateField[string]
	ShippingAddress2             updateField[string]
	ShippingCountry              updateField[string]
	ShippingState                updateField[string]
	ShippingCity                 updateField[string]
	ShippingPostalCode           updateField[string]
	ShippingPhone                updateField[string]
	ShippingDeliveryInstructions updateField[string]
	DefaultWhtType               updateField[string]
	DefaultWhtRate               updateField[string]
	MatchingAliases              updateField[[]string]
	QuotationCcEmails            updateField[[]string]
	BillingCcEmails              updateField[[]string]
	Active                       updateField[bool]
	DefaultWhtGrossUp            updateField[bool]
}

func (c customerCreate) MarshalJSON() ([]byte, error) {
	m := map[string]any{}
	add := func(k string, p, n bool, v any) {
		if p {
			if n {
				m[k] = nil
			} else {
				m[k] = v
			}
		}
	}
	add("name", c.Name.Present, c.Name.Null, c.Name.Value)
	add("contact_first_name", c.ContactFirstName.Present, c.ContactFirstName.Null, c.ContactFirstName.Value)
	add("contact_last_name", c.ContactLastName.Present, c.ContactLastName.Null, c.ContactLastName.Value)
	add("contact_email", c.ContactEmail.Present, c.ContactEmail.Null, c.ContactEmail.Value)
	add("contact_phone", c.ContactPhone.Present, c.ContactPhone.Null, c.ContactPhone.Value)
	add("contact_phone_2", c.ContactPhone2.Present, c.ContactPhone2.Null, c.ContactPhone2.Value)
	add("account_number", c.AccountNumber.Present, c.AccountNumber.Null, c.AccountNumber.Value)
	add("website", c.Website.Present, c.Website.Null, c.Website.Value)
	add("notes", c.Notes.Present, c.Notes.Null, c.Notes.Value)
	add("default_currency", c.DefaultCurrency.Present, c.DefaultCurrency.Null, c.DefaultCurrency.Value)
	add("billing_address", c.BillingAddress.Present, c.BillingAddress.Null, c.BillingAddress.Value)
	add("billing_address1", c.BillingAddress1.Present, c.BillingAddress1.Null, c.BillingAddress1.Value)
	add("billing_address2", c.BillingAddress2.Present, c.BillingAddress2.Null, c.BillingAddress2.Value)
	add("billing_country", c.BillingCountry.Present, c.BillingCountry.Null, c.BillingCountry.Value)
	add("billing_state", c.BillingState.Present, c.BillingState.Null, c.BillingState.Value)
	add("billing_city", c.BillingCity.Present, c.BillingCity.Null, c.BillingCity.Value)
	add("billing_postal_code", c.BillingPostalCode.Present, c.BillingPostalCode.Null, c.BillingPostalCode.Value)
	add("ship_to_name", c.ShipToName.Present, c.ShipToName.Null, c.ShipToName.Value)
	add("shipping_address1", c.ShippingAddress1.Present, c.ShippingAddress1.Null, c.ShippingAddress1.Value)
	add("shipping_address2", c.ShippingAddress2.Present, c.ShippingAddress2.Null, c.ShippingAddress2.Value)
	add("shipping_country", c.ShippingCountry.Present, c.ShippingCountry.Null, c.ShippingCountry.Value)
	add("shipping_state", c.ShippingState.Present, c.ShippingState.Null, c.ShippingState.Value)
	add("shipping_city", c.ShippingCity.Present, c.ShippingCity.Null, c.ShippingCity.Value)
	add("shipping_postal_code", c.ShippingPostalCode.Present, c.ShippingPostalCode.Null, c.ShippingPostalCode.Value)
	add("shipping_phone", c.ShippingPhone.Present, c.ShippingPhone.Null, c.ShippingPhone.Value)
	add("shipping_delivery_instructions", c.ShippingDeliveryInstructions.Present, c.ShippingDeliveryInstructions.Null, c.ShippingDeliveryInstructions.Value)
	add("default_wht_type", c.DefaultWhtType.Present, c.DefaultWhtType.Null, c.DefaultWhtType.Value)
	add("default_wht_rate", c.DefaultWhtRate.Present, c.DefaultWhtRate.Null, c.DefaultWhtRate.Value)
	add("matching_aliases", c.MatchingAliases.Present, c.MatchingAliases.Null, c.MatchingAliases.Value)
	add("quotation_cc_emails", c.QuotationCcEmails.Present, c.QuotationCcEmails.Null, c.QuotationCcEmails.Value)
	add("billing_cc_emails", c.BillingCcEmails.Present, c.BillingCcEmails.Null, c.BillingCcEmails.Value)
	add("active", c.Active.Present, c.Active.Null, c.Active.Value)
	add("default_wht_gross_up", c.DefaultWhtGrossUp.Present, c.DefaultWhtGrossUp.Null, c.DefaultWhtGrossUp.Value)
	return json.Marshal(m)
}

type customerCreateBuilder struct{ Fields map[string]any }

func (b customerCreateBuilder) typed() customerCreate {
	var c customerCreate
	for k, v := range b.Fields {
		switch k {
		case "name":
			c.Name.Present = true
			c.Name.Null = v == nil
			c.Name.Value, _ = v.(string)
		case "contact_first_name":
			c.ContactFirstName.Present = true
			c.ContactFirstName.Null = v == nil
			c.ContactFirstName.Value, _ = v.(string)
		case "contact_last_name":
			c.ContactLastName.Present = true
			c.ContactLastName.Null = v == nil
			c.ContactLastName.Value, _ = v.(string)
		case "contact_email":
			c.ContactEmail.Present = true
			c.ContactEmail.Null = v == nil
			c.ContactEmail.Value, _ = v.(string)
		case "contact_phone":
			c.ContactPhone.Present = true
			c.ContactPhone.Null = v == nil
			c.ContactPhone.Value, _ = v.(string)
		case "contact_phone_2":
			c.ContactPhone2.Present = true
			c.ContactPhone2.Null = v == nil
			c.ContactPhone2.Value, _ = v.(string)
		case "account_number":
			c.AccountNumber.Present = true
			c.AccountNumber.Null = v == nil
			c.AccountNumber.Value, _ = v.(string)
		case "website":
			c.Website.Present = true
			c.Website.Null = v == nil
			c.Website.Value, _ = v.(string)
		case "notes":
			c.Notes.Present = true
			c.Notes.Null = v == nil
			c.Notes.Value, _ = v.(string)
		case "default_currency":
			c.DefaultCurrency.Present = true
			c.DefaultCurrency.Null = v == nil
			c.DefaultCurrency.Value, _ = v.(string)
		case "billing_address":
			c.BillingAddress.Present = true
			c.BillingAddress.Null = v == nil
			c.BillingAddress.Value, _ = v.(string)
		case "billing_address1":
			c.BillingAddress1.Present = true
			c.BillingAddress1.Null = v == nil
			c.BillingAddress1.Value, _ = v.(string)
		case "billing_address2":
			c.BillingAddress2.Present = true
			c.BillingAddress2.Null = v == nil
			c.BillingAddress2.Value, _ = v.(string)
		case "billing_country":
			c.BillingCountry.Present = true
			c.BillingCountry.Null = v == nil
			c.BillingCountry.Value, _ = v.(string)
		case "billing_state":
			c.BillingState.Present = true
			c.BillingState.Null = v == nil
			c.BillingState.Value, _ = v.(string)
		case "billing_city":
			c.BillingCity.Present = true
			c.BillingCity.Null = v == nil
			c.BillingCity.Value, _ = v.(string)
		case "billing_postal_code":
			c.BillingPostalCode.Present = true
			c.BillingPostalCode.Null = v == nil
			c.BillingPostalCode.Value, _ = v.(string)
		case "ship_to_name":
			c.ShipToName.Present = true
			c.ShipToName.Null = v == nil
			c.ShipToName.Value, _ = v.(string)
		case "shipping_address1":
			c.ShippingAddress1.Present = true
			c.ShippingAddress1.Null = v == nil
			c.ShippingAddress1.Value, _ = v.(string)
		case "shipping_address2":
			c.ShippingAddress2.Present = true
			c.ShippingAddress2.Null = v == nil
			c.ShippingAddress2.Value, _ = v.(string)
		case "shipping_country":
			c.ShippingCountry.Present = true
			c.ShippingCountry.Null = v == nil
			c.ShippingCountry.Value, _ = v.(string)
		case "shipping_state":
			c.ShippingState.Present = true
			c.ShippingState.Null = v == nil
			c.ShippingState.Value, _ = v.(string)
		case "shipping_city":
			c.ShippingCity.Present = true
			c.ShippingCity.Null = v == nil
			c.ShippingCity.Value, _ = v.(string)
		case "shipping_postal_code":
			c.ShippingPostalCode.Present = true
			c.ShippingPostalCode.Null = v == nil
			c.ShippingPostalCode.Value, _ = v.(string)
		case "shipping_phone":
			c.ShippingPhone.Present = true
			c.ShippingPhone.Null = v == nil
			c.ShippingPhone.Value, _ = v.(string)
		case "shipping_delivery_instructions":
			c.ShippingDeliveryInstructions.Present = true
			c.ShippingDeliveryInstructions.Null = v == nil
			c.ShippingDeliveryInstructions.Value, _ = v.(string)
		case "default_wht_type":
			c.DefaultWhtType.Present = true
			c.DefaultWhtType.Null = v == nil
			c.DefaultWhtType.Value, _ = v.(string)
		case "default_wht_rate":
			c.DefaultWhtRate.Present = true
			c.DefaultWhtRate.Null = v == nil
			c.DefaultWhtRate.Value, _ = v.(string)
		case "matching_aliases":
			c.MatchingAliases.Present = true
			c.MatchingAliases.Null = v == nil
			c.MatchingAliases.Value, _ = v.([]string)
		case "quotation_cc_emails":
			c.QuotationCcEmails.Present = true
			c.QuotationCcEmails.Null = v == nil
			c.QuotationCcEmails.Value, _ = v.([]string)
		case "billing_cc_emails":
			c.BillingCcEmails.Present = true
			c.BillingCcEmails.Null = v == nil
			c.BillingCcEmails.Value, _ = v.([]string)
		case "active":
			c.Active.Present = true
			c.Active.Null = v == nil
			c.Active.Value, _ = v.(bool)
		case "default_wht_gross_up":
			c.DefaultWhtGrossUp.Present = true
			c.DefaultWhtGrossUp.Null = v == nil
			c.DefaultWhtGrossUp.Value, _ = v.(bool)
		}
	}
	return c
}

type customerUpdate = customerCreate

type customerWithholding struct {
	Type    string
	Rate    string // empty denotes SQL NULL
	GrossUp bool
}

func parseCustomerCreate(body []byte) (customerCreate, []validationError, string) {
	return parseCustomerFields(body, true, customerWithholding{"NONE", "", false})
}

func parseCustomerUpdate(body []byte, stored customerWithholding) (customerUpdate, []validationError, string) {
	return parseCustomerFields(body, false, stored)
}

func parseCustomerFields(body []byte, create bool, stored customerWithholding) (customerCreate, []validationError, string) {
	c := customerCreateBuilder{Fields: map[string]any{}}
	if len(body) > 1<<20 {
		return c.typed(), []validationError{{"body_too_large", []any{"body"}, "Request body too large", nil, nil}}, ""
	}
	if len(body) == 0 {
		return c.typed(), []validationError{{"missing", []any{"body"}, "Field required", nil, nil}}, ""
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	var input any
	if err := d.Decode(&input); err != nil {
		return c.typed(), itemUpdateJSONError(body, err, false), ""
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return c.typed(), itemUpdateJSONError(body, err, true), ""
	}
	obj, ok := input.(map[string]any)
	if !ok {
		return c.typed(), []validationError{{"model_attributes_type", []any{"body"}, "Input should be a valid dictionary or object to extract fields from", input, nil}}, ""
	}
	var errs []validationError
	fields := []struct {
		name, kind string
		min, max   int
	}{
		{"name", "string", 1, 255}, {"matching_aliases", "list", 0, 0}, {"active", "bool", 0, 0},
		{"contact_first_name", "string", 0, 120}, {"contact_last_name", "string", 0, 120}, {"contact_email", "email", 0, 0}, {"quotation_cc_emails", "cc", 0, 0}, {"billing_cc_emails", "cc", 0, 0}, {"contact_phone", "string", 0, 64}, {"contact_phone_2", "string", 0, 64},
		{"account_number", "string", 0, 255}, {"website", "string", 0, 255}, {"notes", "string", 0, 0}, {"default_currency", "string", 3, 3}, {"billing_address", "string", 0, 0}, {"billing_address1", "string", 0, 255}, {"billing_address2", "string", 0, 255}, {"billing_country", "string", 0, 100}, {"billing_state", "string", 0, 255}, {"billing_city", "string", 0, 255}, {"billing_postal_code", "string", 0, 32}, {"ship_to_name", "string", 0, 255}, {"shipping_address1", "string", 0, 255}, {"shipping_address2", "string", 0, 255}, {"shipping_country", "string", 0, 100}, {"shipping_state", "string", 0, 255}, {"shipping_city", "string", 0, 255}, {"shipping_postal_code", "string", 0, 32}, {"shipping_phone", "string", 0, 64}, {"shipping_delivery_instructions", "string", 0, 0}, {"default_wht_type", "string", 0, 16}, {"default_wht_rate", "decimal", 0, 0}, {"default_wht_gross_up", "bool", 0, 0}}
	for _, f := range fields {
		v, exists := obj[f.name]
		loc := []any{"body", f.name}
		if !exists {
			if create && f.name == "name" {
				errs = append(errs, validationError{"missing", loc, "Field required", obj, nil})
			}
			continue
		}
		c.Fields[f.name] = v
		if v == nil && (!create || (f.name != "name" && f.name != "matching_aliases")) {
			continue
		}
		switch f.kind {
		case "string":
			s, ok := v.(string)
			if !ok {
				errs = append(errs, validationError{"string_type", loc, "Input should be a valid string", v, nil})
				continue
			}
			n := utf8.RuneCountInString(s)
			if n < f.min {
				errs = append(errs, validationError{"string_too_short", loc, fmt.Sprintf("String should have at least %d character%s", f.min, map[bool]string{true: "s", false: ""}[f.min != 1]), v, map[string]int{"min_length": f.min}})
			} else if f.max > 0 && n > f.max {
				errs = append(errs, validationError{"string_too_long", loc, fmt.Sprintf("String should have at most %d characters", f.max), v, map[string]int{"max_length": f.max}})
			}
		case "bool":
			if e := updateBooleanError(v, loc); e != nil {
				errs = append(errs, *e)
			} else {
				switch x := v.(type) {
				case string:
					c.Fields[f.name] = strings.Contains("|true|1|on|yes|y|t|", "|"+strings.ToLower(x)+"|")
				case json.Number:
					r, _ := boundedItemRat(string(x))
					c.Fields[f.name] = r.Sign() != 0
				}
			}
		case "email":
			s, ok := v.(string)
			if !ok {
				errs = append(errs, validationError{"string_type", loc, "Input should be a valid string", v, nil})
				continue
			}
			normalized, reason := normalizeEmail(s)
			if reason != "" {
				errs = append(errs, validationError{"value_error", loc, "value is not a valid email address: " + reason, v, map[string]string{"reason": reason}})
			} else {
				c.Fields[f.name] = normalized
			}
		case "list", "cc":
			list, ok := v.([]any)
			if !ok {
				errs = append(errs, validationError{"list_type", loc, "Input should be a valid list", v, nil})
				continue
			}
			out := []string{}
			valid := true
			for i, x := range list {
				if _, ok := x.(string); !ok {
					errs = append(errs, validationError{"string_type", append(append([]any{}, loc...), i), "Input should be a valid string", x, nil})
					valid = false
				}
			}
			if !valid {
				continue
			}
			seen := make(map[string]struct{}, len(list))
			for _, x := range list {
				s := x.(string)
				if f.kind == "cc" {
					s = strings.TrimSpace(s)
					if s == "" {
						continue
					}
					_, reason := normalizeEmail(s)
					if reason != "" {
						if valid {
							errs = append(errs, validationError{"value_error", loc, "Value error, invalid email address: " + s, v, map[string]any{"error": map[string]any{}}})
						}
						valid = false
						break
					}
					key := cases.Lower(language.Und).String(s)
					if _, exists := seen[key]; exists {
						continue
					}
					seen[key] = struct{}{}

				}
				out = append(out, s)
			}
			c.Fields[f.name] = out
		case "decimal":
			var s string
			switch x := v.(type) {
			case string:
				s = x
			case json.Number:
				s = string(x)
			default:
				errs = append(errs, validationError{"decimal_type", loc, "Decimal input should be an integer, float, string or Decimal object", v, nil})
				continue
			}
			s = strings.TrimSpace(s)
			// Python Decimal removes underscores anywhere in a string before parsing.
			// Enforce the raw limit before removing them.
			rawLen := len(s)
			if _, isString := v.(string); isString {
				s = strings.ReplaceAll(s, "_", "")
			}
			_, valid := boundedItemRat(s)
			valid = valid && rawLen <= 4096
			valid = valid && customerDecimalPattern.MatchString(s)

			if !valid {
				errs = append(errs, validationError{"decimal_parsing", loc, "Input should be a valid decimal", v, nil})
			} else {
				if i := strings.IndexAny(s, "eE"); i >= 0 {
					if n, err := strconv.Atoi(s[i+1:]); err == nil && n == 0 {
						s = s[:i]
					}
				}
				c.Fields[f.name] = s
			}
		}
	}
	if len(errs) > 0 {
		return c.typed(), errs, ""
	}
	normalized, domain := normalizeCustomerWithholding(c.Fields, stored.Type, stored.Rate, stored.GrossUp)
	c.Fields = normalized
	if domain == "" {
		if rate, ok := normalized["default_wht_rate"].(string); ok {
			rat, _ := boundedItemRat(rate)
			if numeric, ok := obj["default_wht_rate"].(json.Number); ok && strings.ContainsAny(string(numeric), ".eE") {
				f, err := strconv.ParseFloat(string(numeric), 64)
				rounded, valid := boundedItemRat(strconv.FormatFloat(f, 'g', -1, 64))
				if err != nil || !valid || rounded.Cmp(rat) != 0 {
					return c.typed(), []validationError{{"decimal_inexact", []any{"body", "default_wht_rate"}, "Numeric value changes when decoded as a float; send an exact decimal string", numeric, nil}}, ""
				}
			}
			scaled := new(big.Rat).Mul(rat, big.NewRat(10000, 1))
			if !scaled.IsInt() || new(big.Int).Abs(scaled.Num()).Cmp(big.NewInt(9999999)) > 0 {
				return c.typed(), []validationError{{"decimal_precision", []any{"body", "default_wht_rate"}, "Value must be exactly representable as NUMERIC(7,4)", obj["default_wht_rate"], nil}}, ""
			}
		}
	}
	return c.typed(), nil, domain
}

// normalizeCustomerWithholding overlays supplied values on stored defaults.
func normalizeCustomerWithholding(fields map[string]any, storedType, storedRate string, storedGross bool) (map[string]any, string) {
	rawType, a := fields["default_wht_type"]
	rawRate, b := fields["default_wht_rate"]
	rawGross, g := fields["default_wht_gross_up"]
	if !a && !b && !g {
		return fields, ""
	}
	typ := storedType
	if rawType != nil {
		typ = rawType.(string)
	}
	rate := storedRate
	if b {
		rate, _ = rawRate.(string)
	} else if rawType != nil && typ != storedType {
		rate = ""
	}
	gross := storedGross
	if rawGross != nil {
		gross = rawGross.(bool)
	}
	if typ != "NONE" && typ != "PPH21" && typ != "PPH23" {
		return fields, "Unknown withholding tax type: " + typ
	}
	if typ == "NONE" {
		fields["default_wht_type"] = "NONE"
		fields["default_wht_rate"] = nil
		fields["default_wht_gross_up"] = false
		return fields, ""
	}
	if rate == "" {
		if typ == "PPH21" {
			rate = "2.5"
		} else {
			rate = "2"
		}
	}
	r, valid := boundedItemRat(rate)
	if !valid || r.Sign() <= 0 || r.Cmp(big.NewRat(100, 1)) >= 0 {
		return fields, "Withholding tax rate must be greater than 0 and less than 100"
	}
	fields["default_wht_type"] = typ
	fields["default_wht_rate"] = rate
	fields["default_wht_gross_up"] = gross
	return fields, ""
}
