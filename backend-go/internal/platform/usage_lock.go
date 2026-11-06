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
	"sort"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
)

type usageLockSchema struct {
	AccruedAmount any    `json:"accrued_amount"`
	CutoffDate    string `json:"cutoff_date" format:"date"`
}
type usageLockInput struct {
	ID   string `path:"invoice_id" format:"uuid"`
	Body usageLockSchema
}

func registerUsageLock(api huma.API, s sessionHandler) {
	responses := map[string]*huma.Response{}
	for code, desc := range map[string]string{"400": "Invalid invoice", "401": "Not authenticated", "403": "Admin required", "413": "Body too large", "422": "Validation error", "500": "Internal server error"} {
		typ := reflect.TypeOf(authErrorBody{})
		if code == "422" {
			typ = reflect.TypeOf(validationErrorBody{})
		}
		responses[code] = &huma.Response{Description: desc, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(typ, true, "")}}}
	}
	huma.Register(api, huma.Operation{Method: "POST", Path: "/api/v1/invoices/usage/{invoice_id}/lock", OperationID: "lock-usage-invoice", SkipValidateBody: true, Security: []map[string][]string{{"sessionCookie": {}}}, Middlewares: huma.Middlewares{sessionMiddleware(s.usageLock)}, Responses: responses}, func(context.Context, *usageLockInput) (*struct{ Body invoiceOutputSchema }, error) {
		panic("middleware")
	})
	zero := float64(0)
	schema := api.OpenAPI().Components.Schemas.Map()["UsageLockSchema"]
	schema.AdditionalProperties = true
	*schema.Properties["accrued_amount"] = huma.Schema{AnyOf: []*huma.Schema{{Type: "number", Minimum: &zero}, {Type: "string"}}}
}
func (s sessionHandler) usageLock(w http.ResponseWriter, r *http.Request) {
	body, ok := readBodyWithSyntax(w, r)
	if !ok {
		return
	}
	current, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	raw := chi.URLParam(r, "invoice_id")
	id, valid := parsePathUUID(raw)
	if !valid {
		d := uuidErrorDetail(raw)
		validationResponse(w, []validationError{{"uuid_parsing", []any{"path", "invoice_id"}, "Input should be a valid UUID, " + d, raw, map[string]string{"error": d}}})
		return
	}
	if len(body) == 0 {
		validationResponse(w, []validationError{{"missing", []any{"body"}, "Field required", nil, nil}})
		return
	}
	if !requestJSONMediaType(r) {
		validationResponse(w, []validationError{{"model_attributes_type", []any{"body"}, "Input should be a valid dictionary or object to extract fields from", "[REDACTED]", nil}})
		return
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		validationResponse(w, invoiceVoidJSONError(body, err, false))
		return
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		validationResponse(w, invoiceVoidJSONError(body, err, true))
		return
	}
	obj, ok := v.(map[string]any)
	if !ok {
		validationResponse(w, []validationError{{"model_attributes_type", []any{"body"}, "Input should be a valid dictionary or object to extract fields from", v, nil}})
		return
	}
	var errs []validationError
	var amount decimal.Decimal
	if x, exists := obj["accrued_amount"]; !exists {
		errs = append(errs, validationError{"missing", []any{"body", "accrued_amount"}, "Field required", obj, nil})
	} else {
		raw, _ := json.Marshal(x)
		n, valid := itemMoney(raw)
		if !valid || x == nil {
			e := itemMoneyError("accrued_amount", x)
			if e.Type == "greater_than_equal" {
				e.Ctx = map[string]int{"ge": 0}
			}
			if x == nil {
				e.Type = "decimal_type"
				e.Msg = "Decimal input should be an integer, float, string or Decimal object"
			}
			errs = append(errs, e)
		} else {
			value, _ := n.Value()
			amount, _ = decimal.NewFromString(value.(string))
		}
	}
	var cutoff string
	if x, exists := obj["cutoff_date"]; !exists {
		errs = append(errs, validationError{"missing", []any{"body", "cutoff_date"}, "Field required", obj, nil})
	} else {
		switch x.(type) {
		case string, json.Number:
			d, e := invoiceDate(x)
			if e != nil {
				e.Loc = []any{"body", "cutoff_date"}
				e.Input = x
				errs = append(errs, *e)
			} else {
				cutoff = d.Format("2006-01-02")
			}
		default:
			errs = append(errs, validationError{"date_type", []any{"body", "cutoff_date"}, "Input should be a valid date", x, nil})
		}
	}
	if len(errs) > 0 {
		validationResponse(w, errs)
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
	row, err := q.LockInvoice(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		authError(w, 400, fmt.Sprintf("invoice %x-%x-%x-%x-%x not found", id.Bytes[:4], id.Bytes[4:6], id.Bytes[6:8], id.Bytes[8:10], id.Bytes[10:]))
		return
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if row.InvoiceType != "USAGE" {
		authError(w, 400, fmt.Sprintf("invoice %x-%x-%x-%x-%x is not USAGE", id.Bytes[:4], id.Bytes[4:6], id.Bytes[6:8], id.Bytes[8:10], id.Bytes[10:]))
		return
	}
	if row.Status != "DRAFT" {
		authError(w, 400, fmt.Sprintf("usage invoice already locked (status=%s)", row.Status))
		return
	}
	if err = applyUsageLock(ctx, q, row, amount, cutoff); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	row, err = q.GetInvoice(ctx, id)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	lines, err := q.InvoiceLinesByIDs(ctx, []pgtype.UUID{id})
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	out, err := invoiceJSON(row, lines)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if tx.Commit(ctx) != nil {
		authError(w, 500, "internal server error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(append(encoded, '\n'))
}

// applyUsageLock requires the invoice row to be locked by the caller's transaction.
func applyUsageLock(ctx context.Context, q *dbquery.Queries, row dbquery.Invoice, amount decimal.Decimal, cutoff string) error {
	total, tax := amount, decimal.Zero
	if row.WhtRate.Valid {
		val, err := row.WhtRate.Value()
		if err != nil {
			return err
		}
		rate, err := decimal.NewFromString(val.(string))
		if err != nil {
			return err
		}
		if row.WhtType != "NONE" && !amount.IsZero() && row.WhtGrossUp && rate.Equal(decimal.NewFromInt(100)) {
			return errors.New("invalid withholding rate")
		}
		total, tax = invoiceWithholdingTotals(amount, row.Currency, row.WhtType, rate, row.WhtGrossUp)
	}
	ref, err := usageBillingRef(row.BillingCycleRef, cutoff)
	if err != nil {
		return err
	}
	return q.LockUsageInvoice(ctx, dbquery.LockUsageInvoiceParams{InvoiceID: row.InvoiceID, WhtBase: invoiceNumeric(amount), WhtAmount: invoiceNumeric(tax), Amount: invoiceNumeric(total), BillingCycleRef: ref})
}

// usageBillingRef mirrors dict(reference or {}) while keeping nested JSON numbers exact.
func usageBillingRef(raw []byte, cutoff string) ([]byte, error) {
	ref := make(map[string]json.RawMessage)
	var value any
	if len(raw) > 0 {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if err := dec.Decode(&value); err != nil {
			return nil, err
		}
	}
	switch v := value.(type) {
	case nil:
	case bool:
		if v {
			return nil, errors.New("invalid billing reference")
		}
	case json.Number:
		d, err := decimal.NewFromString(string(v))
		if err != nil || !d.IsZero() {
			return nil, errors.New("invalid billing reference")
		}
	case string:
		if v != "" {
			return nil, errors.New("invalid billing reference")
		}
	case map[string]any:
		for k, x := range v {
			b, err := json.Marshal(x)
			if err != nil {
				return nil, err
			}
			ref[k] = b
		}
	case []any:
		for _, entry := range v {
			var key string
			var val any
			switch pair := entry.(type) {
			case []any:
				if len(pair) != 2 {
					return nil, errors.New("invalid billing reference pair")
				}
				var ok bool
				key, ok = pair[0].(string)
				if !ok {
					return nil, errors.New("invalid billing reference key")
				}
				val = pair[1]
			case map[string]any:
				if len(pair) != 2 {
					return nil, errors.New("invalid billing reference pair")
				}
				keys := make([]string, 0, 2)
				for k := range pair {
					keys = append(keys, k)
				}
				// PostgreSQL jsonb emits object keys ordered by byte length, then bytes.
				sort.Slice(keys, func(i, j int) bool {
					if len(keys[i]) != len(keys[j]) {
						return len(keys[i]) < len(keys[j])
					}
					return keys[i] < keys[j]
				})
				key = keys[0]
				val = keys[1]
			case string:
				chars := []rune(pair)
				if len(chars) != 2 {
					return nil, errors.New("invalid billing reference pair")
				}
				key = string(chars[0])
				val = string(chars[1])
			default:
				return nil, errors.New("invalid billing reference pair")
			}
			b, err := json.Marshal(val)
			if err != nil {
				return nil, err
			}
			ref[key] = b
		}
	default:
		return nil, errors.New("invalid billing reference")
	}
	date, err := json.Marshal(cutoff)
	if err != nil {
		return nil, err
	}
	ref["cutoff_date"] = date
	ref["locked"] = json.RawMessage("true")
	return json.Marshal(ref)
}
