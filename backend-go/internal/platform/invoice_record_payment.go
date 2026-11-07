package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
)

type invoiceRecordPaymentSchema struct {
	Amount         any     `json:"amount"`
	WhtAmount      any     `json:"wht_amount,omitempty"`
	PaymentDate    string  `json:"payment_date" format:"date"`
	PayerName      *string `json:"payer_name,omitempty"`
	PayerReference *string `json:"payer_reference,omitempty"`
	Notes          *string `json:"notes,omitempty"`
}

func registerInvoiceRecordPayment(api huma.API, s sessionHandler) {
	responses := map[string]*huma.Response{}
	for code, desc := range map[string]string{"400": "Invalid payment", "401": "Not authenticated", "403": "Admin required", "404": "Invoice not found", "413": "Body too large", "422": "Validation error", "500": "Internal server error"} {
		typ := reflect.TypeOf(authErrorBody{})
		if code == "422" {
			typ = reflect.TypeOf(validationErrorBody{})
		}
		responses[code] = &huma.Response{Description: desc, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(typ, true, "")}}}
	}
	huma.Register(api, huma.Operation{Method: "POST", Path: "/api/v1/invoices/{invoice_id}/record-payment", OperationID: "record-invoice-payment", DefaultStatus: 201, SkipValidateBody: true, Security: []map[string][]string{{"sessionCookie": {}}}, Middlewares: huma.Middlewares{sessionMiddleware(s.invoiceRecordPayment)}, Responses: responses}, func(context.Context, *struct {
		ID   string `path:"invoice_id" format:"uuid"`
		Body invoiceRecordPaymentSchema
	}) (*struct{ Body paymentSchema }, error) {
		panic("middleware")
	})
	schema := api.OpenAPI().Components.Schemas.Map()["InvoiceRecordPaymentSchema"]
	schema.AdditionalProperties = true
	for _, f := range []string{"amount", "wht_amount"} {
		*schema.Properties[f] = huma.Schema{AnyOf: []*huma.Schema{{Type: "number"}, {Type: "string"}}}
	}
}

type invoicePaymentWriter struct {
	pool    transactionBeginner
	admin   func(http.ResponseWriter, *http.Request) (pgtype.UUID, bool)
	queries func(pgx.Tx) invoicePaymentQueries
}

type invoicePaymentQueries interface {
	lockAdminRole(context.Context, pgtype.UUID) (string, error)
	LockInvoice(context.Context, pgtype.UUID) (dbquery.Invoice, error)
	queueHostingRestore(context.Context, pgtype.UUID) error
	InsertInvoicePayment(context.Context, dbquery.InsertInvoicePaymentParams) (dbquery.Payment, error)
	ApplyInvoicePayment(context.Context, dbquery.ApplyInvoicePaymentParams) error
	InsertInvoicePaymentLog(context.Context, dbquery.InsertInvoicePaymentLogParams) error
}

func (s sessionHandler) invoiceRecordPayment(w http.ResponseWriter, r *http.Request) {
	writer := invoicePaymentWriter{
		pool:  s.db,
		admin: s.invoiceWriter().admin,
		queries: func(tx pgx.Tx) invoicePaymentQueries {
			return legacyInvoiceWriteQueries{Queries: dbquery.New(tx), tx: tx}
		},
	}
	writer.record(w, r)
}

func (s invoicePaymentWriter) record(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Content-Type") == "" {
		r = r.Clone(r.Context())
		r.Header.Set("Content-Type", "application/json")
	}
	body, ok := readBodyWithSyntax(w, r)
	if !ok {
		return
	}
	current, ok := s.admin(w, r)
	if !ok {
		return
	}
	if len(body) == 0 {
		paymentValidationResponse(w, []validationError{{"missing", []any{"body"}, "Field required", nil, nil}})
		return
	}
	if !requestJSONMediaType(r) {
		paymentValidationResponse(w, []validationError{{"model_attributes_type", []any{"body"}, "Input should be a valid dictionary or object to extract fields from", nil, nil}})
		return
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var raw any
	if e := dec.Decode(&raw); e != nil {
		paymentValidationResponse(w, itemUpdateJSONError(body, e, false))
		return
	}
	var extra any
	if e := dec.Decode(&extra); e != io.EOF {
		paymentValidationResponse(w, itemUpdateJSONError(body, e, true))
		return
	}
	obj, ok := raw.(map[string]any)
	if !ok {
		paymentValidationResponse(w, []validationError{{"model_attributes_type", []any{"body"}, "Input should be a valid dictionary or object to extract fields from", raw, nil}})
		return
	}
	var errs []validationError
	amounts := map[string]decimal.Decimal{"wht_amount": decimal.Zero}
	for _, f := range []string{"amount", "wht_amount"} {
		v, exists := obj[f]
		if !exists {
			if f == "amount" {
				errs = append(errs, validationError{"missing", []any{"body", f}, "Field required", obj, nil})
			}
			continue
		}
		b, _ := json.Marshal(v)
		d, valid, _ := invoiceDecimal(b, true)
		if !valid {
			e := itemMoneyError(f, v)
			if e.Type == "greater_than_equal" {
				e.Ctx = map[string]int{"ge": 0}
			}
			errs = append(errs, e)
		} else {
			amounts[f] = d
		}
	}
	var paydate pgtype.Date
	if v, exists := obj["payment_date"]; !exists {
		errs = append(errs, validationError{"missing", []any{"body", "payment_date"}, "Field required", obj, nil})
	} else {
		switch v.(type) {
		case string, json.Number:
			d, e := invoiceDate(v)
			if e != nil {
				e.Loc = []any{"body", "payment_date"}
				e.Input = v
				errs = append(errs, *e)
			} else {
				paydate = pgtype.Date{Time: d, Valid: true}
			}
		default:
			errs = append(errs, validationError{"date_type", []any{"body", "payment_date"}, "Input should be a valid date", v, nil})
		}
	}
	for _, f := range []string{"payer_name", "payer_reference", "notes"} {
		if v, exists := obj[f]; exists && v != nil {
			if str, ok := v.(string); !ok {
				errs = append(errs, validationError{"string_type", []any{"body", f}, "Input should be a valid string", nil, nil})
			} else {
				limit := 255
				if f == "notes" {
					limit = 4096
				}
				if utf8.RuneCountInString(str) > limit {
					errs = append(errs, validationError{"string_too_long", []any{"body", f}, fmt.Sprintf("String should have at most %d characters", limit), nil, map[string]int{"max_length": limit}})
				}
			}
		}
	}
	if len(errs) > 0 {
		paymentValidationResponse(w, errs)
		return
	}
	rawID := chi.URLParam(r, "invoice_id")
	id, ok := parsePathUUID(rawID)
	if !ok {
		d := uuidErrorDetail(rawID)
		paymentValidationResponse(w, []validationError{{"uuid_parsing", []any{"path", "invoice_id"}, "Input should be a valid UUID, " + d, rawID, map[string]string{"error": d}}})
		return
	}
	fail := func() { authError(w, 500, "internal server error") }
	ctx := r.Context()
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		fail()
		return
	}
	defer tx.Rollback(ctx)
	q := s.queries(tx)
	role, e := q.lockAdminRole(ctx, current)
	if errors.Is(e, pgx.ErrNoRows) || e == nil && role != "admin" {
		authError(w, 403, "admin required")
		return
	}
	if e != nil {
		fail()
		return
	}
	inv, e := q.LockInvoice(ctx, id)
	if errors.Is(e, pgx.ErrNoRows) {
		authError(w, 404, "invoice not found")
		return
	}
	if e != nil {
		fail()
		return
	}
	if inv.Status == "DRAFT" || inv.Status == "VOID" {
		authError(w, 400, "cannot record payment on "+inv.Status+" invoice")
		return
	}
	balance, e := updateNumeric(inv.BalanceDue)
	if e != nil {
		fail()
		return
	}
	wht, e := updateNumeric(inv.WhtAmount)
	if e != nil {
		fail()
		return
	}
	booked, e := updateNumeric(inv.WhtBooked)
	if e != nil {
		fail()
		return
	}
	amount, withheld := amounts["amount"], amounts["wht_amount"]
	settled := amount.Add(withheld)
	if settled.IsZero() {
		authError(w, 400, "amount and withheld cannot both be zero")
		return
	}
	if settled.GreaterThan(balance) {
		authError(w, 400, "Payment exceeds balance due")
		return
	}
	outstanding := wht.Sub(booked)
	if withheld.GreaterThan(outstanding) {
		authError(w, 400, "Withholding exceeds outstanding withholding")
		return
	}
	balance = balance.Sub(settled)
	status := "PARTIAL"
	if balance.IsZero() {
		status = "PAID"
	}
	// Record restoration intent with the payment. A future provider adapter must
	// recheck eligibility before acting; no provider calls occur in this request.
	if status == "PAID" {
		if err := q.queueHostingRestore(ctx, id); err != nil {
			fail()
			return
		}
	}
	paymentID, e := itemUUID()
	if e != nil {
		fail()
		return
	}
	logID, e := itemUUID()
	if e != nil {
		fail()
		return
	}
	payer := "(manual entry)"
	if v, ok := obj["payer_name"].(string); ok && v != "" {
		payer = v
	}
	var ref pgtype.Text
	if v, ok := obj["payer_reference"].(string); ok {
		ref = pgtype.Text{String: v, Valid: true}
	}
	external := fmt.Sprintf("manual-%x", paymentID.Bytes[:6])
	p, e := q.InsertInvoicePayment(ctx, dbquery.InsertInvoicePaymentParams{PaymentID: paymentID, InvoiceID: id, CustomerID: inv.CustomerID, Amount: invoiceNumeric(amount), WhtAmount: invoiceNumeric(withheld), Currency: inv.Currency, PayerName: payer, PayerReference: ref, PaymentDate: paydate, ExternalRef: pgtype.Text{String: external, Valid: true}})
	if e != nil {
		fail()
		return
	}
	e = q.ApplyInvoicePayment(ctx, dbquery.ApplyInvoicePaymentParams{InvoiceID: id, BalanceDue: invoiceNumeric(balance), WhtBooked: invoiceNumeric(booked.Add(withheld)), Status: status})
	if e != nil {
		fail()
		return
	}
	reasons := []string{"manual-entry"}
	if v, ok := obj["notes"].(string); ok && v != "" {
		runes := []rune(v)
		if len(runes) > 200 {
			runes = runes[:200]
		}
		reasons = append(reasons, string(runes))
	}
	if e = q.InsertInvoicePaymentLog(ctx, dbquery.InsertInvoicePaymentLogParams{LogID: logID, PaymentID: paymentID, Reasons: reasons, ActorUserID: current}); e != nil {
		fail()
		return
	}
	out, e := paymentJSON(p)
	if e != nil {
		fail()
		return
	}
	encoded, e := json.Marshal(out)
	if e != nil {
		fail()
		return
	}
	if e = tx.Commit(ctx); e != nil {
		fail()
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(201)
	_, _ = w.Write(append(encoded, '\n'))
}

// Do not echo payment details back in validation errors.
func paymentValidationResponse(w http.ResponseWriter, errs []validationError) {
	for i := range errs {
		errs[i].Input = nil
	}
	validationResponse(w, errs)
}

func (q legacyInvoiceWriteQueries) queueHostingRestore(ctx context.Context, id pgtype.UUID) error {
	rows, err := q.tx.Query(ctx, `SELECT i.item_id FROM items i WHERE i.is_hosting AND EXISTS (SELECT 1 FROM invoice_line_items l WHERE l.invoice_id=$1 AND l.item_id=i.item_id) ORDER BY i.item_id FOR UPDATE`, id)
	if err != nil {
		return err
	}
	var itemIDs []pgtype.UUID
	for rows.Next() {
		var itemID pgtype.UUID
		if err = rows.Scan(&itemID); err != nil {
			break
		}
		itemIDs = append(itemIDs, itemID)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, itemID := range itemIDs {
		if _, err = q.tx.Exec(ctx, `SELECT target_id FROM cloudflare_targets WHERE item_id=$1 FOR UPDATE`, itemID); err != nil {
			return err
		}
		if _, err = q.tx.Exec(ctx, `UPDATE items SET hosting_last_paid_at=date_trunc('second', timezone('UTC', now())) WHERE item_id=$1`, itemID); err != nil {
			return err
		}
		if _, err = q.tx.Exec(ctx, `INSERT INTO hosting_restore_intents(invoice_id,item_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, id, itemID); err != nil {
			return err
		}
	}
	return nil
}
