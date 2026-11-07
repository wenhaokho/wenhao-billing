package platform

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/LOQATO/gokit/auth"
	"github.com/go-chi/chi/v5"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wenhaokho/wenhao-billing/backend/internal/business"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedprofile"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopeduser"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// NewScopedProfileRouter is a development-only v8 router. It intentionally
// registers only the explicitly converted routes; it is not used at startup.
func NewScopedProfileRouter(pool *pgxpool.Pool, cfg SessionConfig, scope business.ID) (http.Handler, error) {
	if err := scope.Validate(); err != nil {
		return nil, err
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if pool == nil {
		return nil, errors.New("database required")
	}
	h := scopedProfileRouter{pool: pool, cfg: cfg, scope: scope}
	r := chi.NewRouter()
	r.Post("/api/v1/auth/login", h.login)
	r.Post("/api/v1/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		sessionHandler{config: cfg}.cookie(w, "", -1)
		w.WriteHeader(http.StatusNoContent)
	})
	r.Get("/api/v1/auth/me", h.me)
	r.Get("/api/v1/bills", h.billRead)
	r.Post("/api/v1/bills", h.billCreate)
	r.Get("/api/v1/bills/{bill_id}", h.billRead)
	r.Patch("/api/v1/bills/{bill_id}", h.billUpdate)
	r.Post("/api/v1/bills/{bill_id}/record-payment", h.billPaymentVoid(false))
	r.Post("/api/v1/bills/{bill_id}/void", h.billPaymentVoid(true))
	quotationWriter := h.quotationWriter()
	r.Get("/api/v1/quotations", h.quotationRead)
	r.Post("/api/v1/quotations", quotationWriter.create)
	r.Get("/api/v1/quotations/{quotation_id}", h.quotationRead)
	r.Get("/api/v1/quotations/{quotation_id}/pdf", h.documentPDF("quotation", h.loadQuotationPDF))
	r.Patch("/api/v1/quotations/{quotation_id}", quotationWriter.update)
	r.Delete("/api/v1/quotations/{quotation_id}", quotationWriter.delete)
	r.Post("/api/v1/quotations/{quotation_id}/convert-to-invoice", h.quotationConvert)
	r.Post("/api/v1/invoices/{invoice_id}/send", h.invoiceSender().send)
	r.Post("/api/v1/invoices/{invoice_id}/record-payment", h.invoicePaymentWriter().record)
	r.Get("/api/v1/invoices/{invoice_id}/payments", h.invoicePayments)
	r.Post("/api/v1/invoices/{invoice_id}/payments/{payment_id}/send-receipt", h.receiptSender().send)
	r.Post("/api/v1/quotations/{quotation_id}/send", func(w http.ResponseWriter, r *http.Request) {
		quotationWriter.send(cfg.Sender, w, r)
	})
	for _, action := range []string{"mark-sent", "accept", "decline", "void"} {
		r.Post("/api/v1/quotations/{quotation_id}/"+action, quotationWriter.status(action))
	}
	r.Get("/api/v1/invoices", h.invoiceRead)
	invoiceWriter := h.invoiceWriter()
	r.Post("/api/v1/invoices", invoiceWriter.invoiceCreate)
	r.Post("/api/v1/invoices/milestone", invoiceWriter.milestoneCreate)
	r.Patch("/api/v1/invoices/{invoice_id}", invoiceWriter.invoiceUpdate)
	r.With(NoStore).Delete("/api/v1/invoices/{invoice_id}", invoiceWriter.invoiceDelete)
	for _, action := range []string{"finalize", "mark-paid", "void"} {
		r.With(NoStore).Post("/api/v1/invoices/{invoice_id}/"+action, invoiceWriter.invoiceTransition)
	}
	r.Post("/api/v1/invoices/recurring-template", invoiceWriter.invoiceCreate)
	r.Post("/api/v1/invoices/recurring/{template_id}/trigger", h.recurringTrigger)
	r.Post("/api/v1/invoices/usage/{invoice_id}/lock", h.usageLock)
	r.Put("/api/v1/invoices/recurring-templates/{template_id}", invoiceWriter.invoiceUpdate)
	r.Patch("/api/v1/invoices/recurring-templates/{template_id}", invoiceWriter.recurringAction)
	r.Get("/api/v1/invoices/awaiting-finalization", h.invoiceRead)
	r.Get("/api/v1/invoices/recurring-templates", h.invoiceRead)
	r.Get("/api/v1/invoices/recurring-templates/rows", h.recurringRows)
	r.Get("/api/v1/invoices/recurring-templates/{template_id}", h.invoiceRead)
	r.Get("/api/v1/invoices/{invoice_id}", h.invoiceRead)
	r.Get("/api/v1/invoices/{invoice_id}/pdf", h.documentPDF("invoice", h.loadInvoicePDF))
	r.Get("/api/v1/projects", h.projectRead)
	r.Post("/api/v1/projects", h.projectCreate)
	r.Patch("/api/v1/projects/{project_id}", h.projectUpdate)
	r.Delete("/api/v1/projects/{project_id}", h.projectDelete)
	r.Get("/api/v1/projects/{project_id}", h.projectRead)
	r.Get("/api/v1/projects/{project_id}/invoices", h.projectReport)
	r.Get("/api/v1/projects/{project_id}/summary", h.projectReport)
	r.Get("/api/v1/items", h.itemRead)
	r.Post("/api/v1/items", h.itemCreate)
	r.Get("/api/v1/items/{item_id}", h.itemRead)
	r.Patch("/api/v1/items/{item_id}", h.itemUpdate)
	r.Delete("/api/v1/items/{item_id}", h.itemDeactivate)
	r.Get("/api/v1/customers", h.customerRead)
	r.Post("/api/v1/customers", h.customerCreate)
	r.Patch("/api/v1/customers/{customer_id}", h.customerUpdate)
	r.Get("/api/v1/customers/balances", h.customerBalances)
	r.Get("/api/v1/customers/{customer_id}/billing-summary", h.customerSummary)
	r.Get("/api/v1/customers/{customer_id}/invoices", h.customerSummary)
	r.Get("/api/v1/customers/{customer_id}", h.customerRead)
	r.Delete("/api/v1/customers/{customer_id}", h.customerDeactivate)
	r.Get("/api/v1/vendors", h.vendorRead)
	r.Post("/api/v1/vendors", h.vendorCreate)
	r.Patch("/api/v1/vendors/{vendor_id}", h.vendorUpdate)
	r.Get("/api/v1/vendors/{vendor_id}", h.vendorRead)
	r.Delete("/api/v1/vendors/{vendor_id}", h.vendorDelete)
	r.Get("/api/v1/payments/audit-log", h.paymentAudit)
	r.Get("/api/v1/payments/{payment_id}/log", h.paymentLog)
	r.Get("/api/v1/payments/awaiting-review", h.paymentRead)
	r.Get("/api/v1/payments/{payment_id}", h.paymentRead)
	r.Post("/api/v1/payments/intake/webhook", h.paymentIntake(false))
	r.Post("/api/v1/payments/intake/email", h.paymentIntake(true))
	r.Post("/api/v1/payments/{payment_id}/manual-review", h.reconcilePayment(false))
	r.Post("/api/v1/payments/{payment_id}/reverse", h.reconcilePayment(true))
	r.Get("/api/v1/users", h.listUsers)
	r.Post("/api/v1/users", h.createUser)
	r.Post("/api/v1/users/{user_id}/reset-password", h.adminResetPassword)
	r.Patch("/api/v1/users/me", h.updateUserProfile)
	r.Post("/api/v1/auth/change-password", h.changePassword)
	r.Post("/api/v1/auth/reset-password", h.resetPassword)
	r.Post("/api/v1/auth/forgot-password", h.forgotPassword)
	r.Get("/api/v1/accounting/base-currency", h.fxRead)
	r.Get("/api/v1/accounting/reports/trial-balance", h.trialBalance)
	r.Get("/api/v1/accounting/reports/balance-sheet", h.balanceSheet)
	r.Get("/api/v1/stats/invoices-summary", h.invoiceSummary)
	r.Get("/api/v1/stats/revenue-monthly", h.monthlyRevenue)
	r.Get("/api/v1/stats/dashboard", h.dashboard)
	r.Get("/api/v1/accounting/reports/ar-aging", h.currentAging("AR"))
	r.Get("/api/v1/accounting/reports/ap-aging", h.currentAging("AP"))
	r.Get("/api/v1/accounting/reports/fx-revaluation", h.fxRevaluation)
	r.Get("/api/v1/accounting/fx-rates", h.fxRead)
	r.Get("/api/v1/accounting/fx-rates/latest", h.fxRead)
	r.Post("/api/v1/accounting/fx-rates", h.fxCreate)
	r.Post("/api/v1/accounting/fx-rates/sync", h.fxSync)
	r.Delete("/api/v1/accounting/fx-rates/{rate_id}", h.fxDelete)
	r.Get("/api/v1/accounting/chart-of-accounts", h.accountRead)
	r.Get("/api/v1/accounting/chart-of-accounts/{account_id}", h.accountRead)
	r.Post("/api/v1/accounting/chart-of-accounts", h.accountWrite)
	r.Patch("/api/v1/accounting/chart-of-accounts/{account_id}", h.accountWrite)
	r.Delete("/api/v1/accounting/chart-of-accounts/{account_id}", h.accountWrite)
	r.Get("/api/v1/business-profile", h.profile)
	r.Put("/api/v1/business-profile", h.update)
	r.Get("/api/v1/business-profile/payment-accounts", h.accounts)
	r.Put("/api/v1/business-profile/payment-accounts", h.replaceAccounts)
	return r, nil
}

type scopedProfileRouter struct {
	pool  *pgxpool.Pool
	cfg   SessionConfig
	scope business.ID
}

func (h scopedProfileRouter) login(w http.ResponseWriter, r *http.Request) {
	in, ok := loginValidation(w, r)
	if !ok {
		return
	}
	u, err := scopeduser.GetSessionUserByEmail(r.Context(), h.pool, h.scope, dbqueryv8.GetSessionUserByEmailParams{Email: in.Email})
	if errors.Is(err, pgx.ErrNoRows) {
		authError(w, 401, "invalid credentials")
		return
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	valid, err := auth.Verify(in.Password, u.PasswordHash)
	if err != nil || !valid {
		authError(w, 401, "invalid credentials")
		return
	}
	ttl := 8 * time.Hour
	if in.Remember {
		ttl = 30 * 24 * time.Hour
	}
	token, err := (auth.Tokens{Secret: h.cfg.Secret, AccessTTL: ttl}).Issue(jwt.MapClaims{"sub": u.UserID.String(), "business_id": h.scope.String()})
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	sessionHandler{config: h.cfg}.cookie(w, token, int(ttl.Seconds()))
	userJSON(w, u.UserID, u.Email, u.Role, u.DisplayName, u.CreatedAt)
}
func (h scopedProfileRouter) admin(w http.ResponseWriter, r *http.Request) (dbqueryv8.GetSessionUserByIDRow, bool) {
	c, err := r.Cookie("session")
	if err != nil {
		authError(w, 401, "not authenticated")
		return dbqueryv8.GetSessionUserByIDRow{}, false
	}
	claims, err := (auth.Tokens{Secret: h.cfg.Secret}).Parse(c.Value)
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			authError(w, 401, "session expired")
		} else {
			authError(w, 401, "not authenticated")
		}
		return dbqueryv8.GetSessionUserByIDRow{}, false
	}
	bound, ok := claims["business_id"].(string)
	if !ok || bound != h.scope.String() {
		authError(w, 401, "not authenticated")
		return dbqueryv8.GetSessionUserByIDRow{}, false
	}
	sub, err := claims.GetSubject()
	var id pgtype.UUID
	if err != nil || id.Scan(sub) != nil || !id.Valid {
		authError(w, 401, "not authenticated")
		return dbqueryv8.GetSessionUserByIDRow{}, false
	}
	u, err := scopeduser.GetSessionUserByID(r.Context(), h.pool, h.scope, dbqueryv8.GetSessionUserByIDParams{UserID: id})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && u.Role != "admin" {
		authError(w, 403, "admin required")
		return u, false
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return u, false
	}
	return u, true
}
func (h scopedProfileRouter) me(w http.ResponseWriter, r *http.Request) {
	u, ok := h.admin(w, r)
	if ok {
		userJSON(w, u.UserID, u.Email, u.Role, u.DisplayName, u.CreatedAt)
	}
}

func (h scopedProfileRouter) changePassword(w http.ResponseWriter, r *http.Request) {
	body, ok := parseChangePasswordBody(w, r)
	if !ok {
		return
	}
	initial, ok := h.admin(w, r)
	if !ok {
		return
	}
	current, next, ok := validateChangePasswordBody(w, body)
	if !ok {
		return
	}
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	defer tx.Rollback(r.Context())
	user, err := scopeduser.LockSessionUserByID(r.Context(), tx, h.scope, dbqueryv8.LockSessionUserByIDParams{UserID: initial.UserID})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && user.Role != "admin" {
		authError(w, 403, "admin required")
		return
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	valid, err := auth.Verify(current, user.PasswordHash)
	if err != nil || !valid {
		authError(w, 400, "current password is incorrect")
		return
	}
	if current == next {
		authError(w, 400, "new password must differ from current password")
		return
	}
	params := auth.DefaultHashParams
	if h.cfg.hashParams != nil {
		params = *h.cfg.hashParams
	}
	hash, err := auth.Hash(next, params)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if _, err = scopeduser.SetUserPassword(r.Context(), tx, h.scope, dbqueryv8.SetUserPasswordParams{UserID: initial.UserID, PasswordHash: hash}); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	userJSON(w, user.UserID, user.Email, user.Role, user.DisplayName, user.CreatedAt)
}
func scopedProfileOutput(p dbqueryv8.BusinessProfile) businessProfileOutput {
	return businessProfileOutput{p.Name, p.Address, p.ContactEmail, p.ContactPhone, p.InvoiceTitle, p.InvoiceSummary, p.LogoUrl, p.DefaultNotes, p.Npwp, p.NpwpName, pythonTimestamp(p.UpdatedAt)}
}
func (h scopedProfileRouter) profile(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.admin(w, r); !ok {
		return
	}
	p, err := scopedprofile.Get(r.Context(), h.pool, h.scope)
	if errors.Is(err, pgx.ErrNoRows) {
		p.UpdatedAt, err = dbqueryv8.New(h.pool).GetBusinessProfileDefaultTime(r.Context())
	}
	if err != nil || !p.UpdatedAt.Valid {
		authError(w, 500, "internal server error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	writeSafeJSON(w, scopedProfileOutput(p))
}
func (h scopedProfileRouter) update(w http.ResponseWriter, r *http.Request) {
	body, ok := readBodyWithSyntax(w, r)
	if !ok {
		return
	}
	u, ok := h.admin(w, r)
	if !ok {
		return
	}
	if len(body) == 0 {
		validationResponse(w, []validationError{{"missing", []any{"body"}, "Field required", nil, nil}})
		return
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var raw any
	_ = dec.Decode(&raw)
	obj, ok := raw.(map[string]any)
	if !ok {
		validationResponse(w, []validationError{{"model_attributes_type", []any{"body"}, "Input should be a valid dictionary or object to extract fields from", raw, nil}})
		return
	}
	data := map[string]any{}
	var errs []validationError
	for _, f := range profileFields {
		v, exists := obj[f]
		if !exists {
			continue
		}
		if v != nil {
			if _, ok := v.(string); !ok {
				errs = append(errs, validationError{"string_type", []any{"body", f}, "Input should be a valid string", v, nil})
				continue
			}
		}
		data[f] = v
	}
	if len(errs) > 0 {
		validationResponse(w, errs)
		return
	}
	encoded, _ := json.Marshal(data)
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	defer tx.Rollback(r.Context())
	locked, err := scopeduser.LockSessionUserByID(r.Context(), tx, h.scope, dbqueryv8.LockSessionUserByIDParams{UserID: u.UserID})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && locked.Role != "admin" {
		authError(w, 403, "admin required")
		return
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	p, err := scopedprofile.Update(r.Context(), tx, h.scope, encoded)
	if err != nil || tx.Commit(r.Context()) != nil {
		authError(w, 500, "internal server error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	writeSafeJSON(w, scopedProfileOutput(p))
}
func (h scopedProfileRouter) accounts(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.admin(w, r); !ok {
		return
	}
	rows, err := scopedprofile.ListAccounts(r.Context(), h.pool, h.scope)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	out := make([]paymentAccountOutput, 0, len(rows))
	for _, p := range rows {
		out = append(out, paymentAccountOutput{p.ID, p.Currency, p.Instructions, p.IsDefault, pythonTimestamp(p.UpdatedAt)})
	}
	w.Header().Set("Content-Type", "application/json")
	writeSafeJSON(w, out)
}
func (h scopedProfileRouter) replaceAccounts(w http.ResponseWriter, r *http.Request) {
	body, ok := readBodyWithSyntax(w, r)
	if !ok {
		return
	}
	u, ok := h.admin(w, r)
	if !ok {
		return
	}
	if len(body) == 0 {
		validationResponse(w, []validationError{{"missing", []any{"body"}, "Field required", nil, nil}})
		return
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	var raw any
	_ = d.Decode(&raw)
	list, ok := raw.([]any)
	if !ok {
		validationResponse(w, []validationError{{"list_type", []any{"body"}, "Input should be a valid list", "[REDACTED]", nil}})
		return
	}
	inputs := make([]paymentAccountInputSchema, 0, len(list))
	var errs []validationError
	for i, v := range list {
		obj, ok := v.(map[string]any)
		if !ok {
			errs = append(errs, validationError{"model_attributes_type", []any{"body", i}, "Input should be a valid dictionary or object to extract fields from", "[REDACTED]", nil})
			continue
		}
		var a paymentAccountInputSchema
		for _, f := range []string{"currency", "instructions"} {
			val, exists := obj[f]
			loc := []any{"body", i, f}
			if !exists {
				errs = append(errs, validationError{"missing", loc, "Field required", nil, nil})
				continue
			}
			x, ok := val.(string)
			if !ok {
				errs = append(errs, validationError{"string_type", loc, "Input should be a valid string", "[REDACTED]", nil})
				continue
			}
			if f == "currency" {
				a.Currency = x
			} else {
				a.Instructions = x
			}
		}
		if v, exists := obj["is_default"]; exists {
			loc := []any{"body", i, "is_default"}
			if e := itemBooleanError(v, loc); e != nil {
				e.Input = "[REDACTED]"
				errs = append(errs, *e)
			} else {
				switch x := v.(type) {
				case bool:
					a.IsDefault = x
				case string:
					switch strings.ToLower(x) {
					case "true", "1", "on", "yes", "y", "t":
						a.IsDefault = true
					}
				case json.Number:
					n, _ := x.Float64()
					a.IsDefault = n == 1
				}
			}
		}
		inputs = append(inputs, a)
	}
	if len(errs) > 0 {
		validationResponse(w, errs)
		return
	}
	seen := map[string]bool{}
	unsupported := map[string]bool{}
	defaults := 0
	for i := range inputs {
		c := cases.Upper(language.Und).String(inputs[i].Currency)
		inputs[i].Currency = c
		if seen[c] {
			authError(w, 422, "Duplicate currency in payment accounts")
			return
		}
		seen[c] = true
		if c != "USD" && c != "SGD" && c != "IDR" {
			unsupported[c] = true
		}
		if inputs[i].IsDefault {
			defaults++
		}
	}
	if len(unsupported) > 0 {
		authError(w, 422, "Unsupported currency")
		return
	}
	if defaults > 1 {
		authError(w, 422, "At most one account can be the default")
		return
	}
	accounts := make([]scopedprofile.Account, 0, len(inputs))
	for _, a := range inputs {
		accounts = append(accounts, scopedprofile.Account{Currency: a.Currency, Instructions: a.Instructions, IsDefault: a.IsDefault})
	}
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	defer tx.Rollback(r.Context())
	locked, err := scopeduser.LockSessionUserByID(r.Context(), tx, h.scope, dbqueryv8.LockSessionUserByIDParams{UserID: u.UserID})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && locked.Role != "admin" {
		authError(w, 403, "admin required")
		return
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if err = scopedprofile.ReplaceAccounts(r.Context(), tx, h.scope, accounts); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	rows, err := scopedprofile.ListAccounts(r.Context(), tx, h.scope)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	out := make([]paymentAccountOutput, 0, len(rows))
	for _, p := range rows {
		out = append(out, paymentAccountOutput{p.ID, p.Currency, p.Instructions, p.IsDefault, pythonTimestamp(p.UpdatedAt)})
	}
	if err = tx.Commit(r.Context()); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	writeSafeJSON(w, out)
}
