package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
	"io"
	"net/http"
	"reflect"
	"unicode/utf8"
)

type quotationUpdateSchema struct {
	CustomerID      *string                   `json:"customer_id,omitempty" format:"uuid"`
	ProjectID       *string                   `json:"project_id,omitempty" format:"uuid"`
	Currency        *string                   `json:"currency,omitempty" minLength:"3" maxLength:"3"`
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
type quotationUpdateRequest struct {
	ID   string `path:"quotation_id" format:"uuid"`
	Body quotationUpdateSchema
}

func registerQuotationUpdate(api huma.API, s sessionHandler) {
	response := func(desc string, body any) *huma.Response {
		return &huma.Response{Description: desc, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(body), true, "")}}}
	}
	responses := map[string]*huma.Response{"400": response("Invalid quotation", authErrorBody{}), "401": response("Not authenticated", authErrorBody{}), "403": response("Admin required", authErrorBody{}), "413": response("Body too large", authErrorBody{}), "422": response("Validation error", vendorUpdateValidationErrorBody{}), "500": response("Internal server error", authErrorBody{})}
	responses["500"].Content["text/plain"] = &huma.MediaType{Schema: &huma.Schema{Type: "string"}}
	huma.Register(api, huma.Operation{Method: "PATCH", Path: "/api/v1/quotations/{quotation_id}", OperationID: "update-quotation", SkipValidateBody: true, Responses: responses, Security: []map[string][]string{{"sessionCookie": {}}}, Middlewares: huma.Middlewares{sessionMiddleware(s.quotationWriter().update)}}, func(context.Context, *quotationUpdateRequest) (*struct{ Body quotationSchema }, error) {
		panic("middleware")
	})
	schema := api.OpenAPI().Components.Schemas.Map()["QuotationUpdateSchema"]
	schema.AdditionalProperties = true
	*schema.Properties["discount_value"] = huma.Schema{AnyOf: []*huma.Schema{{Type: "number"}, {Type: "string"}, {Type: "null"}}}
	lines := schema.Properties["line_items"]
	schema.Properties["line_items"] = &huma.Schema{AnyOf: []*huma.Schema{lines, {Type: "null"}}}
}
func (s quotationWriter) update(w http.ResponseWriter, r *http.Request) {

	body, ok := readItemBody(w, r)
	if !ok {
		return
	}
	userID, ok := s.admin(w, r)
	if !ok {
		return
	}
	idName := "quotation_id"
	rawID := chi.URLParam(r, idName)
	id, ok := parsePathUUID(rawID)
	if !ok {
		d := uuidErrorDetail(rawID)
		validationResponse(w, []validationError{{"uuid_parsing", []any{"path", idName}, "Input should be a valid UUID, " + d, rawID, map[string]string{"error": d}}})
		return
	}
	invalid := func() {
		vendorUpdateValidationResponse(w, []validationError{{"value_error", []any{"body"}, "Invalid input", nil, nil}})
	}
	if len(body) == 0 {
		vendorUpdateValidationResponse(w, []validationError{{"missing", []any{"body"}, "Field required", nil, nil}})
		return
	}
	if !requestJSONMediaType(r) {
		invalid()
		return
	}

	if !utf8.Valid(body) {
		authError(w, http.StatusBadRequest, "There was an error parsing the body")
		return
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var fields map[string]any
	if dec.Decode(&fields) != nil || fields == nil {
		if errs := validateInvoiceCreate(body); len(errs) > 0 {
			vendorUpdateValidationResponse(w, errs)
		} else {
			invalid()
		}
		return
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		vendorUpdateValidationResponse(w, itemUpdateJSONError(body, err, true))
		return
	}
	// The create validator shares the same constraints on individual values. Update makes every field optional.
	check := make(map[string]any, len(fields)+1)
	allowed := map[string]bool{"customer_id": true, "project_id": true, "currency": true, "quotation_number": true, "po_so_number": true, "issue_date": true, "valid_until": true, "payment_terms": true, "notes": true, "footer": true, "discount_type": true, "discount_value": true, "line_items": true}
	for k, v := range fields {
		if allowed[k] {
			check[k] = v
		}
	}
	if v, ok := check["quotation_number"]; ok {
		check["invoice_number"] = v
		delete(check, "quotation_number")
	}
	if v, ok := check["valid_until"]; ok {
		check["due_date"] = v
		delete(check, "valid_until")
	}
	if check["currency"] == nil {
		check["currency"] = "USD"
	}
	if check["line_items"] == nil {
		delete(check, "line_items")
	}
	validationBody, _ := json.Marshal(check)
	errs := validateInvoiceCreate(validationBody)
	if len(errs) > 0 {
		for i := range errs {
			if len(errs[i].Loc) > 1 {
				if errs[i].Loc[1] == "invoice_number" {
					errs[i].Loc[1] = "quotation_number"
				}
				if errs[i].Loc[1] == "due_date" {
					errs[i].Loc[1] = "valid_until"
				}
			}
		}
	}
	positionOverflow := false
	var positionErrs []validationError
	if list, ok := fields["line_items"].([]any); ok {
		for i, value := range list {
			line, ok := value.(map[string]any)
			if !ok {
				continue
			}
			value, exists := line["position"]
			if !exists || value == nil {
				continue
			}
			position, valid := quotationPosition(value)
			if !valid {
				typ, msg := "int_type", "Input should be a valid integer"
				switch value.(type) {
				case string:
					typ, msg = "int_parsing", "Input should be a valid integer, unable to parse string as an integer"
				case json.Number:
					typ, msg = "int_from_float", "Input should be a valid integer, got a number with a fractional part"
				}
				positionErrs = append(positionErrs, validationError{typ, []any{"body", "line_items", i, "position"}, msg, nil, nil})
				continue
			}
			if !position.IsInt64() || position.Int64() < -2147483648 || position.Int64() > 2147483647 {
				positionOverflow = true
			}
		}
	}
	// Both lists are in line order. Position is the final field on each line,
	// so merge it before the next line's first error without shifting slices.
	if len(positionErrs) > 0 {
		merged := make([]validationError, 0, len(errs)+len(positionErrs))
		next := 0
		for _, existing := range errs {
			if len(existing.Loc) > 2 && existing.Loc[1] == "line_items" {
				if index, ok := existing.Loc[2].(int); ok {
					for next < len(positionErrs) && positionErrs[next].Loc[2].(int) < index {
						merged = append(merged, positionErrs[next])
						next++
					}
				}
			}
			merged = append(merged, existing)
		}
		errs = append(merged, positionErrs[next:]...)
	}
	if len(errs) > 0 {
		vendorUpdateValidationResponse(w, errs)
		return
	}
	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	defer tx.Rollback(ctx)
	q := s.queries(tx)
	role, err := q.lockAdminRole(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && role != "admin" {
		authError(w, 403, "admin required")
		return
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	inv, err := q.LockQuotation(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		if s.scoped {
			authError(w, 400, "quotation not found")
			return
		}
		authError(w, 400, fmt.Sprintf("quotation %x-%x-%x-%x-%x not found", id.Bytes[:4], id.Bytes[4:6], id.Bytes[6:8], id.Bytes[8:10], id.Bytes[10:]))
		return
	}

	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if inv.Status != "DRAFT" && inv.Status != "SENT" {
		authError(w, 400, fmt.Sprintf("only DRAFT or SENT quotations can be edited (got %s)", inv.Status))
		return
	}
	if positionOverflow {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Internal Server Error"))
		return
	}
	for _, name := range []string{"customer_id", "project_id"} {
		if v := fields[name]; v != nil {
			rid, _ := parsePathUUID(v.(string))
			var e error
			if name == "customer_id" {
				e = q.lockCustomer(ctx, rid)
			} else {
				e = q.lockProject(ctx, rid)
			}
			if !s.acceptReference(w, e) {
				return
			}
		}
	}
	ids := map[[16]byte]pgtype.UUID{}
	if values, ok := fields["line_items"].([]any); ok {
		for _, value := range values {
			line := value.(map[string]any)
			if v := line["item_id"]; v != nil {
				rid, _ := parsePathUUID(v.(string))
				ids[rid.Bytes] = rid
			}
		}
	}
	for _, rid := range orderedItemIDs(ids) {
		if !s.acceptReference(w, q.lockItem(ctx, rid)) {
			return
		}
	}
	lines, err := q.QuotationLines(ctx, []pgtype.UUID{id})
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	for _, name := range []string{"currency"} {
		if v, exists := fields[name]; exists && v == nil {
			authError(w, 500, "internal server error")
			return
		}
	}
	setText := func(name string, dst *pgtype.Text) {
		if v, exists := fields[name]; exists {
			if v == nil {
				*dst = pgtype.Text{}
			} else {
				*dst = pgtype.Text{String: v.(string), Valid: true}
			}
		}
	}
	setUUID := func(name string, dst *pgtype.UUID) {
		if v, exists := fields[name]; exists {
			if v == nil {
				*dst = pgtype.UUID{}
			} else {
				*dst, _ = parsePathUUID(v.(string))
			}
		}
	}
	setDate := func(name string, dst *pgtype.Date) {
		if v, exists := fields[name]; exists {
			if v == nil {
				*dst = pgtype.Date{}
			} else {
				d, _ := invoiceDate(v)
				*dst = pgtype.Date{Time: d, Valid: true}
			}
		}
	}
	setUUID("customer_id", &inv.CustomerID)
	setUUID("project_id", &inv.ProjectID)
	if v, exists := fields["currency"]; exists {
		inv.Currency = v.(string)
	}
	previousNumber := inv.QuotationNumber
	setText("quotation_number", &inv.QuotationNumber)
	if s.scoped && inv.QuotationNumber != previousNumber {
		if err := q.LockQuotationNumberAllocation(ctx); err != nil {
			authError(w, 500, "internal server error")
			return
		}
	}
	setText("po_so_number", &inv.PoSoNumber)
	setText("payment_terms", &inv.PaymentTerms)
	setText("notes", &inv.Notes)
	setText("footer", &inv.Footer)

	setText("discount_type", &inv.DiscountType)
	setDate("issue_date", &inv.IssueDate)
	setDate("valid_until", &inv.ValidUntil)
	if v, exists := fields["discount_value"]; exists {
		inv.DiscountValue = pgtype.Numeric{}
		if v != nil {
			b, _ := json.Marshal(v)
			d, _, _ := invoiceDecimal(b, false)
			inv.DiscountValue = invoiceNumeric(d)
		}
	}
	if v, exists := fields["line_items"]; exists && v != nil {
		if err = q.DeleteQuotationLines(ctx, id); err != nil {
			authError(w, 500, "internal server error")
			return
		}
		lines = nil
		for _, x := range v.([]any) {
			m := x.(map[string]any)
			qb, _ := json.Marshal(m["quantity"])
			pb, _ := json.Marshal(m["unit_price"])
			qty, _, _ := invoiceDecimal(qb, true)
			price, _, _ := invoiceDecimal(pb, true)
			lid, e := itemUUID()
			if e != nil {
				authError(w, 500, "internal server error")
				return
			}
			var item pgtype.UUID
			if m["item_id"] != nil {
				item, _ = parsePathUUID(m["item_id"].(string))
			}
			pos := int32(0)
			if n, ok := quotationPosition(m["position"]); ok {
				pos = int32(n.Int64())
			}
			e = q.InsertQuotationLine(ctx, dbquery.InsertQuotationLineParams{LineItemID: lid, QuotationID: id, ItemID: item, Position: pos, Description: m["description"].(string), Quantity: invoiceNumeric(qty), UnitPrice: invoiceNumeric(price), Amount: invoiceNumeric(qty.Mul(price).Round(4))})
			if e != nil {
				authError(w, 500, "internal server error")
				return
			}
			lines = append(lines, dbquery.QuotationLineItem{Quantity: invoiceNumeric(qty), UnitPrice: invoiceNumeric(price)})
		}
	}
	subtotal := decimal.Zero
	for _, l := range lines {
		qty, e := updateNumeric(l.Quantity)
		if e != nil {
			authError(w, 500, "internal server error")
			return
		}
		price, e := updateNumeric(l.UnitPrice)
		if e != nil {
			authError(w, 500, "internal server error")
			return
		}
		subtotal = subtotal.Add(qty.Mul(price))
	}
	subtotal = subtotal.Round(4)
	discount := decimal.Zero
	if inv.DiscountValue.Valid {
		var e error
		discount, e = updateNumeric(inv.DiscountValue)
		if e != nil {
			authError(w, 500, "internal server error")
			return
		}
	}
	base := invoiceDiscountedBase(subtotal, inv.DiscountType.String, discount, inv.DiscountType.Valid && inv.DiscountValue.Valid)
	inv.Subtotal = invoiceNumeric(subtotal)
	inv.Amount = invoiceNumeric(base)
	err = q.UpdateQuotation(ctx, dbquery.UpdateQuotationParams{QuotationID: id, CustomerID: inv.CustomerID, ProjectID: inv.ProjectID, Currency: inv.Currency, QuotationNumber: inv.QuotationNumber, PoSoNumber: inv.PoSoNumber, IssueDate: inv.IssueDate, ValidUntil: inv.ValidUntil, PaymentTerms: inv.PaymentTerms, Notes: inv.Notes, Footer: inv.Footer, DiscountType: inv.DiscountType, DiscountValue: inv.DiscountValue, Subtotal: inv.Subtotal, Amount: inv.Amount})
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	inv, err = q.GetQuotation(ctx, id)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	lines, err = q.QuotationLines(ctx, []pgtype.UUID{id})
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	out, err := quotationJSON(inv, lines)
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
