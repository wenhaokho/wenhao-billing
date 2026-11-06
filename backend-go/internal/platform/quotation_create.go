package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
)

type quotationCreateSchema struct {
	CustomerID      string                    `json:"customer_id" format:"uuid"`
	ProjectID       *string                   `json:"project_id,omitempty" format:"uuid"`
	Currency        string                    `json:"currency" minLength:"3" maxLength:"3"`
	QuotationNumber *string                   `json:"quotation_number,omitempty"`
	PoSoNumber      *string                   `json:"po_so_number,omitempty"`
	IssueDate       *string                   `json:"issue_date,omitempty" format:"date"`
	ValidUntil      *string                   `json:"valid_until,omitempty" format:"date"`
	PaymentTerms    *string                   `json:"payment_terms,omitempty"`
	Notes           *string                   `json:"notes,omitempty"`
	Footer          *string                   `json:"footer,omitempty"`
	DiscountType    *string                   `json:"discount_type,omitempty" enum:"PERCENT,AMOUNT"`
	DiscountValue   any                       `json:"discount_value,omitempty"`
	LineItems       []invoiceCreateLineSchema `json:"line_items,omitempty"`
}

func registerQuotationCreate(api huma.API, s sessionHandler) {
	response := func(desc string, body any) *huma.Response {
		return &huma.Response{Description: desc, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(body), true, "")}}}
	}
	responses := map[string]*huma.Response{"400": response("Invalid quotation", authErrorBody{}), "401": response("Not authenticated", authErrorBody{}), "403": response("Admin required", authErrorBody{}), "413": response("Body too large", authErrorBody{}), "422": response("Validation error", vendorUpdateValidationErrorBody{}), "500": response("Internal server error", authErrorBody{})}
	responses["500"].Content["text/plain"] = &huma.MediaType{Schema: &huma.Schema{Type: "string"}}
	huma.Register(api, huma.Operation{Method: "POST", Path: "/api/v1/quotations", OperationID: "create-quotation", DefaultStatus: 201, SkipValidateBody: true, Responses: responses, Security: []map[string][]string{{"sessionCookie": {}}}, Middlewares: huma.Middlewares{sessionMiddleware(s.quotationCreate)}}, func(context.Context, *struct{ Body quotationCreateSchema }) (*struct{ Body quotationSchema }, error) {
		panic("middleware")
	})
	schema := api.OpenAPI().Components.Schemas.Map()["QuotationCreateSchema"]
	schema.AdditionalProperties = true
	*schema.Properties["discount_value"] = huma.Schema{AnyOf: []*huma.Schema{{Type: "number"}, {Type: "string"}, {Type: "null"}}}
}
func (s sessionHandler) quotationCreate(w http.ResponseWriter, r *http.Request) {
	body, ok := readItemBody(w, r)
	if !ok {
		return
	}
	current, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	invalid := func() {
		vendorUpdateValidationResponse(w, []validationError{{"value_error", []any{"body"}, "Invalid input", nil, nil}})
	}
	if len(body) == 0 {
		vendorUpdateValidationResponse(w, validateInvoiceCreate(body))
		return
	}
	if !requestJSONMediaType(r) {
		invalid()
		return
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(body, &obj) != nil || obj == nil {
		if errs := validateInvoiceCreate(body); len(errs) > 0 {
			vendorUpdateValidationResponse(w, errs)
		} else {
			invalid()
		}
		return
	}
	// Validate only fields accepted by the Python quotation model.
	allowed := map[string]bool{"customer_id": true, "project_id": true, "currency": true, "quotation_number": true, "po_so_number": true, "issue_date": true, "valid_until": true, "payment_terms": true, "notes": true, "footer": true, "discount_type": true, "discount_value": true, "line_items": true}
	clean := map[string]json.RawMessage{}
	for k, v := range obj {
		if allowed[k] {
			clean[k] = v
		}
	}
	clean["invoice_number"] = clean["quotation_number"]
	delete(clean, "quotation_number")
	clean["due_date"] = clean["valid_until"]
	delete(clean, "valid_until")
	transformed, _ := json.Marshal(clean)
	errs := validateInvoiceCreate(transformed)
	if v, exists := obj["customer_id"]; !exists {
		errs = append([]validationError{{"missing", []any{"body", "customer_id"}, "Field required", nil, nil}}, errs...)
	} else if string(v) == "null" {
		errs = append([]validationError{{"uuid_type", []any{"body", "customer_id"}, "UUID input should be a string, bytes or UUID object", nil, nil}}, errs...)
	}
	for i := range errs {
		if len(errs[i].Loc) > 1 {
			switch errs[i].Loc[1] {
			case "invoice_number":
				errs[i].Loc[1] = "quotation_number"
			case "due_date":
				errs[i].Loc[1] = "valid_until"
			}
		}
	}

	var rawLines []map[string]json.RawMessage
	if json.Unmarshal(obj["line_items"], &rawLines) == nil {
		for i, line := range rawLines {
			raw, exists := line["position"]
			if !exists || string(raw) == "null" {
				continue
			}
			var v any
			d := json.NewDecoder(bytes.NewReader(raw))
			d.UseNumber()
			if d.Decode(&v) != nil {
				continue
			}
			text := fmt.Sprint(v)
			if text == "true" {
				text = "1"
			}
			if text == "false" {
				text = "0"
			}
			rat, valid := boundedItemRat(text)
			if !valid || !rat.IsInt() {
				typ, msg := "int_parsing", "Input should be a valid integer, unable to parse string as an integer"
				if _, ok := v.(json.Number); ok {
					typ, msg = "int_from_float", "Input should be a valid integer, got a number with a fractional part"
				}
				if _, ok := v.(bool); !ok {
					if _, ok := v.(string); !ok {
						if _, ok := v.(json.Number); !ok {
							typ, msg = "int_type", "Input should be a valid integer"
						}
					}
				}
				errs = append(errs, validationError{typ, []any{"body", "line_items", i, "position"}, msg, v, nil})
			}
		}
	}
	if len(errs) > 0 {
		vendorUpdateValidationResponse(w, errs)
		return
	}
	var normalized map[string]any
	decoder := json.NewDecoder(bytes.NewReader(transformed))
	decoder.UseNumber()
	_ = decoder.Decode(&normalized)
	for _, name := range []string{"issue_date", "due_date"} {
		if v, ok := normalized[name]; ok && v != nil {
			d, problem := invoiceDate(v)
			if problem != nil {
				invalid()
				return
			}
			normalized[name] = d.Format("2006-01-02")
		}
	}

	positionOverflow := false
	if lines, ok := normalized["line_items"].([]any); ok {
		for _, line := range lines {
			if obj, ok := line.(map[string]any); ok {
				if v, ok := obj["position"]; ok && v != nil {
					text := fmt.Sprint(v)
					if text == "true" {
						text = "1"
					}
					if text == "false" {
						text = "0"
					}
					if rat, ok := boundedItemRat(text); ok && rat.IsInt() {
						n := rat.Num()
						if !n.IsInt64() || n.Int64() < -2147483648 || n.Int64() > 2147483647 {
							positionOverflow = true
							obj["position"] = 0
						} else {
							obj["position"] = n.Int64()
						}
					}
				}
			}
		}
	}
	normalizedBody, _ := json.Marshal(normalized)
	var b invoiceCreateInput
	if json.Unmarshal(normalizedBody, &b) != nil || b.CustomerID == nil || b.Currency == nil {
		invalid()
		return
	}
	if len(b.LineItems) == 0 {
		authError(w, 400, "quotation must have at least one line item")
		return
	}
	discount, ok, hasDiscount := invoiceDecimal(b.DiscountValue, false)
	if !ok {
		invalid()
		return
	}
	subtotal := decimal.Zero
	qtys := make([]decimal.Decimal, len(b.LineItems))
	prices := make([]decimal.Decimal, len(b.LineItems))
	amounts := make([]decimal.Decimal, len(b.LineItems))
	for i, l := range b.LineItems {
		qtys[i], ok, _ = invoiceDecimal(l.Quantity, true)
		if !ok {
			invalid()
			return
		}
		prices[i], ok, _ = invoiceDecimal(l.UnitPrice, true)
		if !ok {
			invalid()
			return
		}
		p := qtys[i].Mul(prices[i])
		subtotal = subtotal.Add(p)
		amounts[i] = p.Round(4)
	}
	subtotal = subtotal.Round(4)
	typ := ""
	if b.DiscountType != nil {
		typ = *b.DiscountType
	}
	amount := invoiceDiscountedBase(subtotal, typ, discount, hasDiscount)
	now := time.Now()
	issue := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	if b.IssueDate != nil {
		issue, _ = time.Parse("2006-01-02", *b.IssueDate)
	}
	valid := issue.AddDate(0, 0, 30)
	if b.DueDate != nil {
		valid, _ = time.Parse("2006-01-02", *b.DueDate)
	}
	if positionOverflow {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Internal Server Error"))
		return
	}
	ctx := r.Context()
	tx, e := s.db.Begin(ctx)
	fail := func() { authError(w, 500, "internal server error") }
	if e != nil {
		fail()
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
		fail()
		return
	}
	customer, _ := parsePathUUID(*b.CustomerID)
	_, e = q.LockCustomer(ctx, customer)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		fail()
		return
	}
	var project pgtype.UUID
	if b.ProjectID != nil {
		project, _ = parsePathUUID(*b.ProjectID)
		_, e = q.LockProject(ctx, project)
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			fail()
			return
		}
	}
	ids := map[[16]byte]pgtype.UUID{}
	for _, l := range b.LineItems {
		if l.ItemID != nil {
			id, _ := parsePathUUID(*l.ItemID)
			ids[id.Bytes] = id
		}
	}
	for _, id := range orderedBillItemIDs(ids) {
		_, e = q.LockItem(ctx, id)
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			fail()
			return
		}
	}
	if e = q.LockQuotationNumberAllocation(ctx); e != nil {
		fail()
		return
	}
	number := ""
	if b.InvoiceNumber != nil {
		number = *b.InvoiceNumber
	}
	if number == "" {
		prefix := "Q" + strconv.Itoa(now.Year())
		rows, err := q.QuotationNumbersForPrefix(ctx, pgtype.Text{String: prefix + "%", Valid: true})
		if err != nil {
			fail()
			return
		}
		max := new(big.Int)
		for _, n := range rows {
			tail := strings.TrimPrefix(n.String, prefix)
			v, digit, invalid := documentNumberSuffix(tail)
			if invalid {
				fail()
				return
			}
			if digit && v.Cmp(max) > 0 {
				max = v
			}
		}
		max.Add(max, big.NewInt(1))
		suffix := max.String()
		if len(suffix) < 4 {
			suffix = strings.Repeat("0", 4-len(suffix)) + suffix
		}
		number = prefix + suffix
	}
	id, e := itemUUID()
	if e != nil {
		fail()
		return
	}
	var discountArg pgtype.Numeric
	if hasDiscount {
		discountArg = invoiceNumeric(discount)
	}
	e = q.InsertQuotation(ctx, dbquery.InsertQuotationParams{QuotationID: id, CustomerID: customer, ProjectID: project, QuotationNumber: pgtype.Text{String: number, Valid: true}, PoSoNumber: textValue(b.PoSoNumber), Currency: *b.Currency, Subtotal: invoiceNumeric(subtotal), DiscountType: textValue(b.DiscountType), DiscountValue: discountArg, Amount: invoiceNumeric(amount), IssueDate: pgtype.Date{Time: issue, Valid: true}, ValidUntil: pgtype.Date{Time: valid, Valid: true}, PaymentTerms: textValue(b.PaymentTerms), Notes: textValue(b.Notes), Footer: textValue(b.Footer)})
	if e != nil {
		fail()
		return
	}
	for i, l := range b.LineItems {
		lid, err := itemUUID()
		if err != nil {
			fail()
			return
		}
		var item pgtype.UUID
		if l.ItemID != nil {
			item, _ = parsePathUUID(*l.ItemID)
		}
		pos := int32(0)
		if l.Position != nil {
			pos = *l.Position
		}
		e = q.InsertQuotationLine(ctx, dbquery.InsertQuotationLineParams{LineItemID: lid, QuotationID: id, ItemID: item, Position: pos, Description: *l.Description, Quantity: invoiceNumeric(qtys[i]), UnitPrice: invoiceNumeric(prices[i]), Amount: invoiceNumeric(amounts[i])})
		if e != nil {
			fail()
			return
		}
	}
	row, e := q.GetQuotation(ctx, id)
	if e != nil {
		fail()
		return
	}
	lines, e := q.QuotationLines(ctx, []pgtype.UUID{id})
	if e != nil {
		fail()
		return
	}
	out, e := quotationJSON(row, lines)
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
