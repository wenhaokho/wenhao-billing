package platform

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
	"net/http"
	"reflect"
	"strings"
)

type customerOutput struct {
	CustomerID                   pgtype.UUID `json:"customer_id"`
	Name                         string      `json:"name"`
	MatchingAliases              []string    `json:"matching_aliases" nullable:"false"`
	Active                       bool        `json:"active"`
	ContactEmail                 pgtype.Text `json:"contact_email"`
	ContactPhone                 pgtype.Text `json:"contact_phone"`
	BillingAddress               pgtype.Text `json:"billing_address"`
	ContactFirstName             pgtype.Text `json:"contact_first_name"`
	ContactLastName              pgtype.Text `json:"contact_last_name"`
	ContactPhone2                pgtype.Text `json:"contact_phone_2"`
	AccountNumber                pgtype.Text `json:"account_number"`
	Website                      pgtype.Text `json:"website"`
	Notes                        pgtype.Text `json:"notes"`
	DefaultCurrency              string      `json:"default_currency"`
	BillingAddress1              pgtype.Text `json:"billing_address1"`
	BillingAddress2              pgtype.Text `json:"billing_address2"`
	BillingCountry               pgtype.Text `json:"billing_country"`
	BillingState                 pgtype.Text `json:"billing_state"`
	BillingCity                  pgtype.Text `json:"billing_city"`
	BillingPostalCode            pgtype.Text `json:"billing_postal_code"`
	ShipToName                   pgtype.Text `json:"ship_to_name"`
	ShippingAddress1             pgtype.Text `json:"shipping_address1"`
	ShippingAddress2             pgtype.Text `json:"shipping_address2"`
	ShippingCountry              pgtype.Text `json:"shipping_country"`
	ShippingState                pgtype.Text `json:"shipping_state"`
	ShippingCity                 pgtype.Text `json:"shipping_city"`
	ShippingPostalCode           pgtype.Text `json:"shipping_postal_code"`
	ShippingPhone                pgtype.Text `json:"shipping_phone"`
	ShippingDeliveryInstructions pgtype.Text `json:"shipping_delivery_instructions"`
	QuotationCcEmails            []string    `json:"quotation_cc_emails" nullable:"false"`
	BillingCcEmails              []string    `json:"billing_cc_emails" nullable:"false"`
	DefaultWhtType               string      `json:"default_wht_type"`
	DefaultWhtRate               *string     `json:"default_wht_rate"`
	DefaultWhtGrossUp            bool        `json:"default_wht_gross_up"`
}
type customerOutputSchema struct {
	CustomerID                   string   `json:"customer_id" format:"uuid"`
	Name                         string   `json:"name"`
	MatchingAliases              []string `json:"matching_aliases" nullable:"false"`
	Active                       bool     `json:"active"`
	ContactEmail                 *string  `json:"contact_email"`
	ContactPhone                 *string  `json:"contact_phone"`
	BillingAddress               *string  `json:"billing_address"`
	ContactFirstName             *string  `json:"contact_first_name"`
	ContactLastName              *string  `json:"contact_last_name"`
	ContactPhone2                *string  `json:"contact_phone_2"`
	AccountNumber                *string  `json:"account_number"`
	Website                      *string  `json:"website"`
	Notes                        *string  `json:"notes"`
	DefaultCurrency              string   `json:"default_currency"`
	BillingAddress1              *string  `json:"billing_address1"`
	BillingAddress2              *string  `json:"billing_address2"`
	BillingCountry               *string  `json:"billing_country"`
	BillingState                 *string  `json:"billing_state"`
	BillingCity                  *string  `json:"billing_city"`
	BillingPostalCode            *string  `json:"billing_postal_code"`
	ShipToName                   *string  `json:"ship_to_name"`
	ShippingAddress1             *string  `json:"shipping_address1"`
	ShippingAddress2             *string  `json:"shipping_address2"`
	ShippingCountry              *string  `json:"shipping_country"`
	ShippingState                *string  `json:"shipping_state"`
	ShippingCity                 *string  `json:"shipping_city"`
	ShippingPostalCode           *string  `json:"shipping_postal_code"`
	ShippingPhone                *string  `json:"shipping_phone"`
	ShippingDeliveryInstructions *string  `json:"shipping_delivery_instructions"`
	QuotationCcEmails            []string `json:"quotation_cc_emails" nullable:"false"`
	BillingCcEmails              []string `json:"billing_cc_emails" nullable:"false"`
	DefaultWhtType               string   `json:"default_wht_type"`
	DefaultWhtRate               *string  `json:"default_wht_rate"`
	DefaultWhtGrossUp            bool     `json:"default_wht_gross_up"`
}

func customerJSON(c dbquery.Customer) (customerOutput, error) {
	o := customerOutput{
		CustomerID:                   c.CustomerID,
		Name:                         c.Name,
		Active:                       c.Active,
		ContactEmail:                 c.ContactEmail,
		ContactPhone:                 c.ContactPhone,
		BillingAddress:               c.BillingAddress,
		ContactFirstName:             c.ContactFirstName,
		ContactLastName:              c.ContactLastName,
		ContactPhone2:                c.ContactPhone2,
		AccountNumber:                c.AccountNumber,
		Website:                      c.Website,
		Notes:                        c.Notes,
		DefaultCurrency:              c.DefaultCurrency,
		BillingAddress1:              c.BillingAddress1,
		BillingAddress2:              c.BillingAddress2,
		BillingCountry:               c.BillingCountry,
		BillingState:                 c.BillingState,
		BillingCity:                  c.BillingCity,
		BillingPostalCode:            c.BillingPostalCode,
		ShipToName:                   c.ShipToName,
		ShippingAddress1:             c.ShippingAddress1,
		ShippingAddress2:             c.ShippingAddress2,
		ShippingCountry:              c.ShippingCountry,
		ShippingState:                c.ShippingState,
		ShippingCity:                 c.ShippingCity,
		ShippingPostalCode:           c.ShippingPostalCode,
		ShippingPhone:                c.ShippingPhone,
		ShippingDeliveryInstructions: c.ShippingDeliveryInstructions,
		DefaultWhtType:               c.DefaultWhtType,
		DefaultWhtRate:               numericString(c.DefaultWhtRate),
		DefaultWhtGrossUp:            c.DefaultWhtGrossUp,
	}
	o.MatchingAliases = []string{}
	if err := json.Unmarshal(c.MatchingAliases, &o.MatchingAliases); err != nil {
		return o, err
	}
	if o.MatchingAliases == nil {
		o.MatchingAliases = []string{}
	}
	o.QuotationCcEmails = []string{}
	if err := json.Unmarshal(c.QuotationCcEmails, &o.QuotationCcEmails); err != nil {
		return o, err
	}
	if o.QuotationCcEmails == nil {
		o.QuotationCcEmails = []string{}
	}
	o.BillingCcEmails = []string{}
	if err := json.Unmarshal(c.BillingCcEmails, &o.BillingCcEmails); err != nil {
		return o, err
	}
	if o.BillingCcEmails == nil {
		o.BillingCcEmails = []string{}
	}
	return o, nil
}
func (s sessionHandler) customerRead(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	q := dbquery.New(s.db)
	ctx := r.Context()
	raw := chi.URLParam(r, "customer_id")
	var rows []dbquery.Customer
	if raw != "" {
		id, valid := parsePathUUID(raw)
		if !valid {
			d := uuidErrorDetail(raw)
			validationResponse(w, []validationError{{"uuid_parsing", []any{"path", "customer_id"}, "Input should be a valid UUID, " + d, raw, map[string]string{"error": d}}})
			return
		}
		c, err := q.GetCustomer(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			authError(w, 404, "customer not found")
			return
		}
		if err != nil {
			authError(w, 500, "internal server error")
			return
		}
		rows = []dbquery.Customer{c}
	} else {
		a, ok := customerBoolFilter(w, r)
		if !ok {
			return
		}
		var err error
		rows, err = q.ListCustomers(ctx, a)
		if err != nil {
			authError(w, 500, "internal server error")
			return
		}
	}
	out := make([]customerOutput, 0, len(rows))
	for _, c := range rows {
		v, err := customerJSON(c)
		if err != nil {
			authError(w, 500, "internal server error")
			return
		}
		out = append(out, v)
	}
	w.Header().Set("Content-Type", "application/json")
	if raw != "" {
		json.NewEncoder(w).Encode(out[0])
	} else {
		json.NewEncoder(w).Encode(out)
	}
}

func customerBoolFilter(w http.ResponseWriter, r *http.Request) (pgtype.Bool, bool) {
	values, exists := r.URL.Query()["active"]
	if !exists {
		return pgtype.Bool{}, true
	}
	raw := values[len(values)-1]
	switch strings.ToLower(raw) {
	case "1", "true", "on", "yes", "y", "t":
		return pgtype.Bool{Bool: true, Valid: true}, true
	case "0", "false", "off", "no", "n", "f":
		return pgtype.Bool{Bool: false, Valid: true}, true
	}
	validationResponse(w, []validationError{{"bool_parsing", []any{"query", "active"}, "Input should be a valid boolean, unable to interpret input", raw, nil}})
	return pgtype.Bool{}, false
}

type customerListInput struct {
	Active bool `query:"active"`
}
type customerGetInput struct {
	ID string `path:"customer_id" format:"uuid"`
}

func registerCustomerReads(api huma.API, s sessionHandler) {
	security := []map[string][]string{{"sessionCookie": {}}}
	m := huma.Middlewares{sessionMiddleware(s.customerRead)}
	response := func(desc string, body any) *huma.Response {
		return &huma.Response{Description: desc, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(body), true, "")}}}
	}
	responses := func(detail bool) map[string]*huma.Response {
		r := map[string]*huma.Response{"401": response("Not authenticated", authErrorBody{}), "403": response("Admin required", authErrorBody{}), "422": response("Validation error", validationErrorBody{}), "500": response("Internal server error", authErrorBody{})}
		if detail {
			r["404"] = response("Customer not found", authErrorBody{})
		}
		return r
	}
	huma.Register(api, huma.Operation{Method: "GET", Path: "/api/v1/customers", OperationID: "list-customers", Security: security, Middlewares: m, Responses: responses(false)}, func(context.Context, *customerListInput) (*struct{ Body []customerOutputSchema }, error) {
		panic("middleware")
	})
	huma.Register(api, huma.Operation{Method: "GET", Path: "/api/v1/customers/{customer_id}", OperationID: "get-customer", Security: security, Middlewares: m, Responses: responses(true)}, func(context.Context, *customerGetInput) (*struct{ Body customerOutputSchema }, error) {
		panic("middleware")
	})
	huma.Register(api, huma.Operation{Method: "DELETE", Path: "/api/v1/customers/{customer_id}", OperationID: "deactivate-customer", Security: security, Middlewares: huma.Middlewares{sessionMiddleware(s.customerDeactivate)}, Responses: responses(true)}, func(context.Context, *customerGetInput) (*struct{ Body customerOutputSchema }, error) {
		panic("middleware")
	})
}

func (s sessionHandler) customerDeactivate(w http.ResponseWriter, r *http.Request) {
	current, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	raw := chi.URLParam(r, "customer_id")
	id, valid := parsePathUUID(raw)
	if !valid {
		d := uuidErrorDetail(raw)
		validationResponse(w, []validationError{{"uuid_parsing", []any{"path", "customer_id"}, "Input should be a valid UUID, " + d, raw, map[string]string{"error": d}}})
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
	c, err := q.DeactivateCustomer(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		authError(w, 404, "customer not found")
		return
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
	if err = tx.Commit(ctx); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}
