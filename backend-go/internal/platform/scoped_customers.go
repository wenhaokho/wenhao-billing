package platform

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedcustomer"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopeduser"
	"io"
	"net/http"
)

func legacyCustomer(c dbqueryv8.Customer) dbquery.Customer {
	return dbquery.Customer{CustomerID: c.CustomerID, Name: c.Name, MatchingAliases: c.MatchingAliases, Active: c.Active, ContactEmail: c.ContactEmail, ContactPhone: c.ContactPhone, BillingAddress: c.BillingAddress, ContactFirstName: c.ContactFirstName, ContactLastName: c.ContactLastName, ContactPhone2: c.ContactPhone2, AccountNumber: c.AccountNumber, Website: c.Website, Notes: c.Notes, DefaultCurrency: c.DefaultCurrency, BillingAddress1: c.BillingAddress1, BillingAddress2: c.BillingAddress2, BillingCountry: c.BillingCountry, BillingState: c.BillingState, BillingCity: c.BillingCity, BillingPostalCode: c.BillingPostalCode, ShipToName: c.ShipToName, ShippingAddress1: c.ShippingAddress1, ShippingAddress2: c.ShippingAddress2, ShippingCountry: c.ShippingCountry, ShippingState: c.ShippingState, ShippingCity: c.ShippingCity, ShippingPostalCode: c.ShippingPostalCode, ShippingPhone: c.ShippingPhone, ShippingDeliveryInstructions: c.ShippingDeliveryInstructions, QuotationCcEmails: c.QuotationCcEmails, BillingCcEmails: c.BillingCcEmails, DefaultWhtType: c.DefaultWhtType, DefaultWhtRate: c.DefaultWhtRate, DefaultWhtGrossUp: c.DefaultWhtGrossUp}
}
func (h scopedProfileRouter) customerRead(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.admin(w, r); !ok {
		return
	}
	ctx := r.Context()
	raw := chi.URLParam(r, "customer_id")
	var rows []dbqueryv8.Customer
	if raw != "" {
		id, valid := parsePathUUID(raw)
		if !valid {
			d := uuidErrorDetail(raw)
			validationResponse(w, []validationError{{"uuid_parsing", []any{"path", "customer_id"}, "Input should be a valid UUID, " + d, raw, map[string]string{"error": d}}})
			return
		}
		c, err := scopedcustomer.GetCustomer(ctx, h.pool, h.scope, dbqueryv8.GetCustomerParams{CustomerID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			authError(w, 404, "customer not found")
			return
		}
		if err != nil {
			authError(w, 500, "internal server error")
			return
		}
		rows = []dbqueryv8.Customer{c}
	} else {
		a, ok := customerBoolFilter(w, r)
		if !ok {
			return
		}
		var err error
		rows, err = scopedcustomer.ListCustomers(ctx, h.pool, h.scope, dbqueryv8.ListCustomersParams{Active: a})
		if err != nil {
			authError(w, 500, "internal server error")
			return
		}
	}
	out := make([]customerOutput, 0, len(rows))
	for _, c := range rows {
		v, err := customerJSON(legacyCustomer(c))
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

func (h scopedProfileRouter) customerDeactivate(w http.ResponseWriter, r *http.Request) {
	current, ok := h.admin(w, r)
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
	tx, err := (candidateTransactions{db: h.pool, scope: h.scope}).Begin(ctx)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	defer tx.Rollback(ctx)
	user, err := scopeduser.LockSessionUserByID(ctx, tx, h.scope, dbqueryv8.LockSessionUserByIDParams{UserID: current.UserID})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && user.Role != "admin" {
		authError(w, 403, "admin required")
		return
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	c, err := scopedcustomer.DeactivateCustomer(ctx, tx, h.scope, dbqueryv8.DeactivateCustomerParams{CustomerID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		authError(w, 404, "customer not found")
		return
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	out, err := customerJSON(legacyCustomer(c))
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

func (h scopedProfileRouter) customerCreate(w http.ResponseWriter, r *http.Request) {
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
	current, ok := h.admin(w, r)
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
	tx, err := (candidateTransactions{db: h.pool, scope: h.scope}).Begin(ctx)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	defer tx.Rollback(ctx)
	user, err := scopeduser.LockSessionUserByID(ctx, tx, h.scope, dbqueryv8.LockSessionUserByIDParams{UserID: current.UserID})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && user.Role != "admin" {
		authError(w, 403, "admin required")
		return
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	c, err := scopedcustomer.CreateCustomer(ctx, tx, h.scope, dbqueryv8.CreateCustomerParams{Name: input.Name.Value})
	if err == nil {
		c, err = scopedcustomer.ApplyCustomerCreate(ctx, tx, h.scope, dbqueryv8.ApplyCustomerCreateParams{Data: data, CustomerID: c.CustomerID})
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	out, err := customerJSON(legacyCustomer(c))
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

func (h scopedProfileRouter) customerUpdate(w http.ResponseWriter, r *http.Request) {
	body, ok := readItemBody(w, r)
	if !ok {
		return
	}
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
	current, ok := h.admin(w, r)
	if !ok {
		return
	}
	_, errs, _ := parseCustomerUpdate(body, customerWithholding{"NONE", "", false})
	raw := chi.URLParam(r, "customer_id")
	id, valid := parsePathUUID(raw)
	if !valid {
		d := uuidErrorDetail(raw)
		errs = append([]validationError{{"uuid_parsing", []any{"path", "customer_id"}, "Input should be a valid UUID, " + d, raw, map[string]string{"error": d}}}, errs...)
	}
	if len(errs) > 0 {
		validationResponse(w, errs)
		return
	}
	ctx := r.Context()
	tx, err := (candidateTransactions{db: h.pool, scope: h.scope}).Begin(ctx)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	defer tx.Rollback(ctx)
	user, err := scopeduser.LockSessionUserByID(ctx, tx, h.scope, dbqueryv8.LockSessionUserByIDParams{UserID: current.UserID})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && user.Role != "admin" {
		authError(w, 403, "admin required")
		return
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	c, err := scopedcustomer.LockCustomer(ctx, tx, h.scope, dbqueryv8.LockCustomerParams{CustomerID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		authError(w, 404, "customer not found")
		return
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	rate := ""
	if p := numericString(c.DefaultWhtRate); p != nil {
		rate = *p
	}
	input, errs, domain := parseCustomerUpdate(body, customerWithholding{c.DefaultWhtType, rate, c.DefaultWhtGrossUp})
	if len(errs) > 0 {
		validationResponse(w, errs)
		return
	}
	if domain != "" {
		authError(w, 422, domain)
		return
	}
	// JSON null arrays cannot satisfy CustomerOut; avoid committing an invalid response.
	if input.MatchingAliases.Null || input.QuotationCcEmails.Null || input.BillingCcEmails.Null {
		authError(w, 500, "internal server error")
		return
	}
	data, err := json.Marshal(input)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	c, err = scopedcustomer.ApplyCustomerUpdate(ctx, tx, h.scope, dbqueryv8.ApplyCustomerUpdateParams{Data: data, CustomerID: id})
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	out, err := customerJSON(legacyCustomer(c))
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
	w.Write(append(encoded, '\n'))
}
