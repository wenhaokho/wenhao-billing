package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
)

type billPaymentSchema struct {
	Amount         any     `json:"amount"`
	PaymentDate    string  `json:"payment_date" format:"date"`
	PayerName      *string `json:"payer_name,omitempty"`
	PayerReference *string `json:"payer_reference,omitempty"`
	Notes          *string `json:"notes,omitempty"`
}

func registerBillPaymentVoid(api huma.API, s sessionHandler) {
	response := func(desc string, body any) *huma.Response {
		return &huma.Response{Description: desc, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(body), true, "")}}}
	}
	responses := map[string]*huma.Response{"400": response("Invalid state", authErrorBody{}), "401": response("Not authenticated", authErrorBody{}), "403": response("Admin required", authErrorBody{}), "404": response("Bill not found", authErrorBody{}), "413": response("Body too large", authErrorBody{}), "422": response("Validation error", vendorUpdateValidationErrorBody{}), "500": {Description: "Internal server error", Content: map[string]*huma.MediaType{"text/plain": {Schema: &huma.Schema{Type: "string"}}}}}

	for _, op := range []struct {
		path, id string
		void     bool
	}{{"record-payment", "record-bill-payment", false}, {"void", "void-bill", true}} {
		operation := huma.Operation{Method: "POST", Path: "/api/v1/bills/{bill_id}/" + op.path, OperationID: op.id, SkipValidateBody: true, Responses: responses, Security: []map[string][]string{{"sessionCookie": {}}}, Middlewares: huma.Middlewares{sessionMiddleware(s.billPaymentVoid(op.void))}}
		if op.void {
			huma.Register(api, operation, func(context.Context, *struct {
				ID   string `path:"bill_id" format:"uuid"`
				Body map[string]any
			}) (*struct{ Body billSchema }, error) {
				panic("middleware")
			})
		} else {
			huma.Register(api, operation, func(context.Context, *struct {
				ID   string `path:"bill_id" format:"uuid"`
				Body billPaymentSchema
			}) (*struct{ Body billSchema }, error) {
				panic("middleware")
			})
		}
	}
	schema := api.OpenAPI().Components.Schemas.Map()["BillPaymentSchema"]
	schema.AdditionalProperties = true
	zero := float64(0)
	*schema.Properties["amount"] = huma.Schema{AnyOf: []*huma.Schema{{Type: "number", ExclusiveMinimum: &zero}, {Type: "string"}}}
}
func (s sessionHandler) billPaymentVoid(void bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") == "" {
			r = r.Clone(r.Context())
			r.Header.Set("Content-Type", "application/json")
		}
		body, ok := readBodyWithSyntax(w, r)
		if !ok {
			return
		}
		current, ok := s.requireAdmin(w, r)
		if !ok {
			return
		}
		if len(body) == 0 {
			validationResponse(w, []validationError{{"missing", []any{"body"}, "Field required", nil, nil}})
			return
		}
		if !requestJSONMediaType(r) {
			typ, msg := "model_attributes_type", "Input should be a valid dictionary or object to extract fields from"
			if void {
				typ, msg = "dict_type", "Input should be a valid dictionary"
			}
			vendorUpdateValidationResponse(w, []validationError{{typ, []any{"body"}, msg, nil, nil}})
			return
		}
		dec := json.NewDecoder(bytes.NewReader(body))
		dec.UseNumber()
		var raw any
		if e := dec.Decode(&raw); e != nil {
			validationResponse(w, itemUpdateJSONError(body, e, false))
			return
		}
		var extra any
		if e := dec.Decode(&extra); e != io.EOF {
			validationResponse(w, itemUpdateJSONError(body, e, true))
			return
		}
		obj, ok := raw.(map[string]any)
		if !ok {
			typ, msg := "model_attributes_type", "Input should be a valid dictionary or object to extract fields from"
			if void {
				typ, msg = "dict_type", "Input should be a valid dictionary"
			}
			validationResponse(w, []validationError{{typ, []any{"body"}, msg, raw, nil}})
			return
		}
		var amount decimal.Decimal
		if !void {
			var errs []validationError
			for _, field := range []string{"amount", "payment_date"} {
				if _, exists := obj[field]; !exists {
					errs = append(errs, validationError{"missing", []any{"body", field}, "Field required", obj, nil})
				}
			}
			if v, exists := obj["amount"]; exists {
				b, _ := json.Marshal(v)
				var valid bool
				amount, valid, _ = invoiceDecimal(b, true)
				if !valid {
					er := itemMoneyError("amount", v)
					if er.Type == "greater_than_equal" {
						if text, ok := v.(string); ok {
							positive := strings.TrimPrefix(strings.TrimSpace(text), "-")
							pe := itemMoneyError("amount", positive)
							if pe.Type == "decimal_max_digits" || pe.Type == "decimal_max_places" || pe.Type == "decimal_whole_digits" {
								er = pe
								er.Input = v
							}
						}
					}
					if er.Type == "greater_than_equal" {
						er.Type = "greater_than"
						er.Msg = "Input should be greater than 0"
						er.Ctx = map[string]int{"gt": 0}
					}
					errs = append(errs, er)
				} else if amount.Sign() <= 0 {
					errs = append(errs, validationError{"greater_than", []any{"body", "amount"}, "Input should be greater than 0", v, map[string]int{"gt": 0}})
				}
			}
			if v, exists := obj["payment_date"]; exists {
				switch v.(type) {
				case string, json.Number:
					_, e := invoiceDate(v)
					if e != nil {
						e.Loc = []any{"body", "payment_date"}
						e.Input = v
						errs = append(errs, *e)
					}
				default:
					errs = append(errs, validationError{"date_type", []any{"body", "payment_date"}, "Input should be a valid date", v, nil})
				}
			}
			for _, name := range []string{"payer_name", "payer_reference", "notes"} {
				if v, exists := obj[name]; exists && v != nil {
					if _, ok := v.(string); !ok {
						errs = append(errs, validationError{"string_type", []any{"body", name}, "Input should be a valid string", v, nil})
					}
				}
			}
			if len(errs) > 0 {
				order := map[string]int{"amount": 0, "payment_date": 1, "payer_name": 2, "payer_reference": 3, "notes": 4}
				sort.SliceStable(errs, func(i, j int) bool { return order[errs[i].Loc[1].(string)] < order[errs[j].Loc[1].(string)] })
				validationResponse(w, errs)
				return
			}
		}
		id, ok := parsePathUUID(chi.URLParam(r, "bill_id"))
		if !ok {
			raw := chi.URLParam(r, "bill_id")
			d := uuidErrorDetail(raw)
			validationResponse(w, []validationError{{"uuid_parsing", []any{"path", "bill_id"}, "Input should be a valid UUID, " + d, raw, map[string]string{"error": d}}})
			return
		}
		internal := func() {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(500)
			_, _ = w.Write([]byte("Internal Server Error"))
		}
		ctx := r.Context()
		tx, e := s.db.Begin(ctx)
		if e != nil {
			internal()
			return
		}
		defer tx.Rollback(ctx)
		q := dbquery.New(tx)
		user, e := q.LockSessionUserByID(ctx, current.UserID)
		if errors.Is(e, pgx.ErrNoRows) || e == nil && user.Role != "admin" {
			authError(w, 403, "admin required")
			return
		}
		if e != nil {
			internal()
			return
		}
		b, e := q.LockBill(ctx, id)
		if errors.Is(e, pgx.ErrNoRows) {
			authError(w, 404, "bill not found")
			return
		}
		if e != nil {
			internal()
			return
		}
		if void {
			if b.Status == "PAID" || b.Status == "VOID" {
				authError(w, 400, "bill is "+b.Status+" and cannot be voided")
				return
			}
			reason := "no reason given"
			ordered, _ := billOrderedJSON(body)
			if root, ok := ordered.(billObject); ok {
				if v, exists := root.get("reason"); exists && billReasonTruthy(v) {
					reason = billPythonString(v, false)
				}
			}
			b.Status = "VOID"
			b.BalanceDue = invoiceNumeric(decimal.Zero)
			b.Notes = pgtype.Text{String: b.Notes.String + "\n[voided] " + reason, Valid: true}
		} else {
			if b.Status == "DRAFT" || b.Status == "VOID" || b.Status == "PAID" {
				authError(w, 400, "cannot record payment on "+b.Status+" bill")
				return
			}
			balance, e := updateNumeric(b.BalanceDue)
			if e != nil {
				internal()
				return
			}
			if amount.GreaterThan(balance) {
				authError(w, 400, "amount "+billPaymentAmountString(obj["amount"], amount)+" exceeds balance due "+balance.StringFixed(4))
				return
			}
			balance = balance.Sub(amount)
			b.BalanceDue = invoiceNumeric(balance)
			if balance.Sign() <= 0 {
				b.Status = "PAID"
			} else {
				b.Status = "PARTIAL"
			}
		}
		b, e = q.UpdateBill(ctx, dbquery.UpdateBillParams{BillID: id, VendorID: b.VendorID, ProjectID: b.ProjectID, BillNumber: b.BillNumber, PoNumber: b.PoNumber, Currency: b.Currency, Subtotal: b.Subtotal, DiscountType: b.DiscountType, DiscountValue: b.DiscountValue, Amount: b.Amount, BalanceDue: b.BalanceDue, Status: b.Status, IssueDate: b.IssueDate, DueDate: b.DueDate, PaymentTerms: b.PaymentTerms, Notes: b.Notes})
		if e != nil {
			internal()
			return
		}
		lines, e := q.BillLines(ctx, []pgtype.UUID{id})
		if e != nil {
			internal()
			return
		}
		out, e := billJSON(b, lines)
		if e != nil {
			internal()
			return
		}
		encoded, e := json.Marshal(out)
		if e != nil {
			internal()
			return
		}
		if tx.Commit(ctx) != nil {
			internal()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(append(encoded, '\n'))
	}
}

type billMember struct {
	key   string
	value any
}
type billObject []billMember

func (o billObject) get(key string) (any, bool) {
	for _, m := range o {
		if m.key == key {
			return m.value, true
		}
	}
	return nil, false
}
func billOrderedJSON(body []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	return billReadValue(d)
}
func billReadValue(d *json.Decoder) (any, error) {
	tok, err := d.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return tok, nil
	}
	switch delim {
	case '{':
		var obj billObject
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return nil, err
			}
			v, err := billReadValue(d)
			if err != nil {
				return nil, err
			}
			k := key.(string)
			found := false
			for i := range obj {
				if obj[i].key == k {
					obj[i].value = v
					found = true
					break
				}
			}
			if !found {
				obj = append(obj, billMember{k, v})
			}
		}
		_, err = d.Token()
		return obj, err
	case '[':
		var arr []any
		for d.More() {
			v, err := billReadValue(d)
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
		}
		_, err = d.Token()
		return arr, err
	}
	return nil, fmt.Errorf("invalid JSON delimiter")
}
func billReasonTruthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case json.Number:
		if strings.ContainsAny(string(x), ".eE") {
			f, _ := strconv.ParseFloat(string(x), 64)
			return f != 0
		}
		n, ok := new(big.Int).SetString(string(x), 10)
		return ok && n.Sign() != 0
	case []any:
		return len(x) != 0
	case billObject:
		return len(x) != 0
	}
	return true
}
func billPythonString(v any, nested bool) string {
	switch x := v.(type) {
	case nil:
		return "None"
	case bool:
		if x {
			return "True"
		}
		return "False"
	case string:
		if !nested {
			return x
		}
		return billPythonRepr(x)
	case json.Number:
		text := string(x)
		if !strings.ContainsAny(text, ".eE") {
			n, ok := new(big.Int).SetString(text, 10)
			if ok {
				return n.String()
			}
			return text
		}
		f, _ := strconv.ParseFloat(text, 64)
		if math.IsInf(f, 1) {
			return "inf"
		}
		if math.IsInf(f, -1) {
			return "-inf"
		}
		a := math.Abs(f)
		format := byte('g')
		if a == 0 || (a >= 1e-4 && a < 1e16) {
			format = 'f'
		}
		out := strconv.FormatFloat(f, format, -1, 64)
		if !strings.ContainsAny(out, ".e") {
			out += ".0"
		}
		return out
	case []any:
		parts := make([]string, 0, len(x))
		for _, v := range x {
			parts = append(parts, billPythonString(v, true))
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case billObject:
		parts := make([]string, 0, len(x))
		for _, m := range x {
			parts = append(parts, billPythonString(m.key, true)+": "+billPythonString(m.value, true))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	return fmt.Sprint(v)
}
func billPythonRepr(s string) string {
	quote := rune(39)
	if strings.ContainsRune(s, 39) && !strings.ContainsRune(s, 34) {
		quote = 34
	}
	var b strings.Builder
	b.WriteRune(quote)
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString("\\\\")
		case '\n':
			b.WriteString("\\n")
		case '\r':
			b.WriteString("\\r")
		case '\t':
			b.WriteString("\\t")
		default:
			if r == quote {
				b.WriteByte('\\')
				b.WriteRune(r)
			} else if !strconv.IsPrint(r) {
				if r <= 0xff {
					fmt.Fprintf(&b, "\\x%02x", r)
				} else if r <= 0xffff {
					fmt.Fprintf(&b, "\\u%04x", r)
				} else {
					fmt.Fprintf(&b, "\\U%08x", r)
				}
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteRune(quote)
	return b.String()
}

func billPaymentAmountString(v any, amount decimal.Decimal) string {
	text := fmt.Sprint(v)
	if strings.ContainsAny(text, "eE") {
		if d, e := decimal.NewFromString(strings.TrimSpace(text)); e == nil {
			digits := d.Coefficient().String()
			sign := ""
			if strings.HasPrefix(digits, "-") {
				sign = "-"
				digits = digits[1:]
			}
			adjusted := int(d.Exponent()) + len(digits) - 1
			if d.Exponent() > 0 || adjusted < -6 {
				mantissa := digits[:1]
				if len(digits) > 1 {
					mantissa += "." + digits[1:]
				}
				if adjusted >= 0 {
					return sign + mantissa + "E+" + fmt.Sprint(adjusted)
				}
				return sign + mantissa + "E" + fmt.Sprint(adjusted)
			}
			return d.StringFixed(max(0, -d.Exponent()))
		}
	}
	return amount.StringFixed(max(0, -amount.Exponent()))
}
