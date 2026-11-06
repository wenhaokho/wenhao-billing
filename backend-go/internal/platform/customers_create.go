package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
)

type customerCreateSchema struct {
	Name                         string   `json:"name" minLength:"1" maxLength:"255"`
	MatchingAliases              []string `json:"matching_aliases,omitempty"`
	Active                       *bool    `json:"active,omitempty"`
	ContactFirstName             *string  `json:"contact_first_name,omitempty"`
	ContactLastName              *string  `json:"contact_last_name,omitempty"`
	ContactEmail                 *string  `json:"contact_email,omitempty" format:"email"`
	ContactPhone                 *string  `json:"contact_phone,omitempty"`
	ContactPhone2                *string  `json:"contact_phone_2,omitempty"`
	AccountNumber                *string  `json:"account_number,omitempty"`
	Website                      *string  `json:"website,omitempty"`
	Notes                        *string  `json:"notes,omitempty"`
	DefaultCurrency              *string  `json:"default_currency,omitempty"`
	BillingAddress               *string  `json:"billing_address,omitempty"`
	BillingAddress1              *string  `json:"billing_address1,omitempty"`
	BillingAddress2              *string  `json:"billing_address2,omitempty"`
	BillingCountry               *string  `json:"billing_country,omitempty"`
	BillingState                 *string  `json:"billing_state,omitempty"`
	BillingCity                  *string  `json:"billing_city,omitempty"`
	BillingPostalCode            *string  `json:"billing_postal_code,omitempty"`
	ShipToName                   *string  `json:"ship_to_name,omitempty"`
	ShippingAddress1             *string  `json:"shipping_address1,omitempty"`
	ShippingAddress2             *string  `json:"shipping_address2,omitempty"`
	ShippingCountry              *string  `json:"shipping_country,omitempty"`
	ShippingState                *string  `json:"shipping_state,omitempty"`
	ShippingCity                 *string  `json:"shipping_city,omitempty"`
	ShippingPostalCode           *string  `json:"shipping_postal_code,omitempty"`
	ShippingPhone                *string  `json:"shipping_phone,omitempty"`
	ShippingDeliveryInstructions *string  `json:"shipping_delivery_instructions,omitempty"`
	QuotationCcEmails            []string `json:"quotation_cc_emails,omitempty"`
	BillingCcEmails              []string `json:"billing_cc_emails,omitempty"`
	DefaultWhtType               *string  `json:"default_wht_type,omitempty"`
	DefaultWhtRate               any      `json:"default_wht_rate,omitempty"`
	DefaultWhtGrossUp            *bool    `json:"default_wht_gross_up,omitempty"`
}

func (s sessionHandler) customerCreate(w http.ResponseWriter, r *http.Request) {
	body, ok := readItemBody(w, r)
	if !ok {
		return
	}
	// FastAPI parses JSON syntax before resolving authentication dependencies.
	if len(body) > 0 {
		d := json.NewDecoder(bytes.NewReader(body))
		d.UseNumber()
		var v any
		if err := d.Decode(&v); err != nil {
			validationResponse(w, itemUpdateJSONError(body, err, false))
			return
		}
		if err := d.Decode(&v); err != io.EOF {
			validationResponse(w, itemUpdateJSONError(body, err, true))
			return
		}
	}
	current, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	input, errs, domain := parseCustomerCreate(body)
	if len(errs) > 0 {
		validationResponse(w, errs)
		return
	}
	if domain != "" {
		authError(w, 422, domain)
		return
	}
	// Python stores explicit null CC lists as JSON null, which cannot satisfy
	// CustomerOut. Reject before insertion so no invalid row is committed.
	if input.QuotationCcEmails.Null || input.BillingCcEmails.Null {
		authError(w, 500, "internal server error")
		return
	}
	data, err := json.Marshal(input)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	ctx := r.Context()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	defer tx.Rollback(ctx)
	q := dbquery.New(tx)
	user, err := q.LockSessionUserByID(ctx, current.UserID)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && user.Role != "admin" {
		authError(w, 403, "admin required")
		return
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	c, err := q.CreateCustomer(ctx, input.Name.Value)
	if err == nil {
		c, err = q.ApplyCustomerCreate(ctx, dbquery.ApplyCustomerCreateParams{Data: data, CustomerID: c.CustomerID})
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	out, err := customerJSON(c)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if err = tx.Commit(ctx); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(201)
	w.Write(append(encoded, '\n'))
}

func registerCustomerCreate(api huma.API, s sessionHandler) {
	response := func(desc string, body any) *huma.Response {
		return &huma.Response{Description: desc, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(body), true, "")}}}
	}
	responses := map[string]*huma.Response{"401": response("Not authenticated", authErrorBody{}), "403": response("Admin required", authErrorBody{}), "413": response("Request body too large", authErrorBody{}), "422": response("Validation error", validationErrorBody{}), "500": response("Internal server error", authErrorBody{})}
	huma.Register(api, huma.Operation{Method: "POST", Path: "/api/v1/customers", OperationID: "create-customer", DefaultStatus: 201, SkipValidateBody: true, Security: []map[string][]string{{"sessionCookie": {}}}, Middlewares: huma.Middlewares{sessionMiddleware(s.customerCreate)}, Responses: responses}, func(context.Context, *struct{ Body customerCreateSchema }) (*struct{ Body customerOutputSchema }, error) {
		panic("middleware")
	})
	schema := api.OpenAPI().Components.Schemas.Map()["CustomerCreateSchema"]
	schema.AdditionalProperties = true
	limits := map[string]int{
		"contact_first_name": 120, "contact_last_name": 120,
		"contact_phone": 64, "contact_phone_2": 64, "shipping_phone": 64,
		"account_number": 255, "website": 255, "billing_address1": 255, "billing_address2": 255,
		"billing_state": 255, "billing_city": 255, "ship_to_name": 255, "shipping_address1": 255,
		"shipping_address2": 255, "shipping_state": 255, "shipping_city": 255,
		"billing_country": 100, "shipping_country": 100, "billing_postal_code": 32, "shipping_postal_code": 32,
		"default_currency": 3, "default_wht_type": 16,
	}
	for name, prop := range schema.Properties {
		if name == "name" || name == "matching_aliases" || name == "$schema" {
			continue
		}
		base := &huma.Schema{Type: "string"}
		switch name {
		case "active", "default_wht_gross_up":
			base.Type = "boolean"
		case "quotation_cc_emails", "billing_cc_emails":
			base.Type = "array"
			base.Items = &huma.Schema{Type: "string"}
		case "contact_email":
			base.Format = "email"
		}
		if max, ok := limits[name]; ok {
			base.MaxLength = &max
		}
		if name == "default_currency" {
			min := 3
			base.MinLength = &min
		}
		choices := []*huma.Schema{base, {Type: "null"}}
		if name == "default_wht_rate" {
			choices = []*huma.Schema{{Type: "number"}, {Type: "string"}, {Type: "null"}}
		}
		*prop = huma.Schema{AnyOf: choices, Default: json.RawMessage("null")}
	}
	*schema.Properties["matching_aliases"] = huma.Schema{Type: "array", Items: &huma.Schema{Type: "string"}}
}
