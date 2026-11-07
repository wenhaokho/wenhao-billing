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
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
	"github.com/wenhaokho/wenhao-billing/backend/internal/recurringschedule"
)

type invoiceUpdateSchema struct {
	CustomerID    *string                   `json:"customer_id,omitempty" format:"uuid"`
	ProjectID     *string                   `json:"project_id,omitempty" format:"uuid"`
	InvoiceType   *string                   `json:"invoice_type,omitempty"`
	Currency      *string                   `json:"currency,omitempty" minLength:"3" maxLength:"3"`
	InvoiceNumber *string                   `json:"invoice_number,omitempty"`
	PoSoNumber    *string                   `json:"po_so_number,omitempty"`
	IssueDate     *string                   `json:"issue_date,omitempty" format:"date"`
	DueDate       *string                   `json:"due_date,omitempty" format:"date"`
	PaymentTerms  *string                   `json:"payment_terms,omitempty"`
	Notes         *string                   `json:"notes,omitempty"`
	Footer        *string                   `json:"footer,omitempty"`
	InternalNote  *string                   `json:"internal_note,omitempty"`
	DiscountType  *string                   `json:"discount_type,omitempty" enum:"PERCENT,AMOUNT"`
	DiscountValue any                       `json:"discount_value,omitempty"`
	WhtType       *string                   `json:"wht_type,omitempty" enum:"NONE,PPH21,PPH23"`
	WhtRate       any                       `json:"wht_rate,omitempty"`
	WhtGrossUp    *bool                     `json:"wht_gross_up,omitempty"`
	LineItems     []invoiceCreateLineSchema `json:"line_items,omitempty"`
}
type invoiceUpdateRequest struct {
	ID   string `path:"invoice_id" format:"uuid"`
	Body invoiceUpdateSchema
}

func registerInvoiceUpdate(api huma.API, s sessionHandler) {
	response := func(desc string, body any) *huma.Response {
		return &huma.Response{Description: desc, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(body), true, "")}}}
	}
	responses := map[string]*huma.Response{"400": response("Invalid invoice", authErrorBody{}), "401": response("Not authenticated", authErrorBody{}), "403": response("Admin required", authErrorBody{}), "413": response("Body too large", authErrorBody{}), "422": response("Validation error", vendorUpdateValidationErrorBody{}), "500": response("Internal server error", authErrorBody{})}
	huma.Register(api, huma.Operation{Method: "PATCH", Path: "/api/v1/invoices/{invoice_id}", OperationID: "update-invoice", SkipValidateBody: true, Responses: responses, Security: []map[string][]string{{"sessionCookie": {}}}, Middlewares: huma.Middlewares{sessionMiddleware(s.invoiceUpdate)}}, func(context.Context, *invoiceUpdateRequest) (*struct{ Body invoiceOutputSchema }, error) {
		panic("middleware")
	})
	schema := api.OpenAPI().Components.Schemas.Map()["InvoiceUpdateSchema"]
	schema.AdditionalProperties = true
	for _, name := range []string{"discount_value", "wht_rate"} {
		*schema.Properties[name] = huma.Schema{AnyOf: []*huma.Schema{{Type: "number"}, {Type: "string"}, {Type: "null"}}}
	}
	lines := schema.Properties["line_items"]
	schema.Properties["line_items"] = &huma.Schema{AnyOf: []*huma.Schema{lines, {Type: "null"}}}
}
func updateNumeric(n pgtype.Numeric) (decimal.Decimal, error) {
	v, e := n.Value()
	if e != nil || v == nil {
		return decimal.Zero, fmt.Errorf("invalid numeric")
	}
	return decimal.NewFromString(v.(string))
}

type recurringTemplateUpdateRequest struct {
	ID   string `path:"template_id" format:"uuid"`
	Body recurringTemplateSchema
}

func registerRecurringTemplateUpdate(api huma.API, s sessionHandler) {
	responses := map[string]*huma.Response{}
	for _, code := range []string{"400", "401", "403", "409", "413", "422", "500"} {
		responses[code] = &huma.Response{Description: "Request failed", Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(authErrorBody{}), true, "")}}}
	}
	responses["422"] = &huma.Response{Description: "Validation error", Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(vendorUpdateValidationErrorBody{}), true, "")}}}
	responses["500"] = &huma.Response{Description: "Internal server error", Content: map[string]*huma.MediaType{"text/plain": {Schema: &huma.Schema{Type: "string"}}}}
	huma.Register(api, huma.Operation{Method: "PUT", Path: "/api/v1/invoices/recurring-templates/{template_id}", OperationID: "update-recurring-template", SkipValidateBody: true, Responses: responses, Security: []map[string][]string{{"sessionCookie": {}}}, Middlewares: huma.Middlewares{sessionMiddleware(s.invoiceUpdate)}}, func(context.Context, *recurringTemplateUpdateRequest) (*struct{ Body invoiceOutputSchema }, error) {
		panic("middleware")
	})
}

type recurringUpdateWriter struct {
	http.ResponseWriter
	internal bool
}

func (w *recurringUpdateWriter) WriteHeader(code int) {
	if code == 500 {
		w.internal = true
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.ResponseWriter.WriteHeader(code)
		_, _ = w.ResponseWriter.Write([]byte("Internal Server Error"))
		return
	}
	w.ResponseWriter.WriteHeader(code)
}
func (w *recurringUpdateWriter) Write(p []byte) (int, error) {
	if w.internal {
		return len(p), nil
	}
	return w.ResponseWriter.Write(p)
}
func (s sessionHandler) invoiceUpdate(w http.ResponseWriter, r *http.Request) {
	s.invoiceWriter().invoiceUpdate(w, r)
}

func (s invoiceWriter) invoiceUpdate(w http.ResponseWriter, r *http.Request) {
	template := r.Method == "PUT"
	if template {
		w = &recurringUpdateWriter{ResponseWriter: w}
	}

	body, ok := readItemBody(w, r)
	if !ok {
		return
	}
	current, ok := s.admin(w, r)
	if !ok {
		return
	}
	idName := "invoice_id"
	if template {
		idName = "template_id"
	}
	rawID := chi.URLParam(r, idName)
	id, ok := parsePathUUID(rawID)
	if !ok {
		d := uuidErrorDetail(rawID)
		validationResponse(w, []validationError{{"uuid_parsing", []any{"path", idName}, "Input should be a valid UUID, " + d, rawID, map[string]string{"error": d}}})
		return
	}
	internalError := func() { authError(w, 500, "internal server error") }
	invalid := func() {
		vendorUpdateValidationResponse(w, []validationError{{"value_error", []any{"body"}, "Invalid input", nil, nil}})
	}
	if template && len(body) == 0 {
		vendorUpdateValidationResponse(w, []validationError{{"missing", []any{"body"}, "Field required", nil, nil}})
		return
	}
	if len(body) == 0 || !requestJSONMediaType(r) {
		invalid()
		return
	}
	var scheduleJSON []byte
	if template {
		var valid bool
		body, scheduleJSON, valid = validateRecurringTemplate(w, body, true)
		if !valid {
			return
		}
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var fields map[string]any
	if dec.Decode(&fields) != nil || fields == nil {
		invalid()
		return
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		invalid()
		return
	}
	if template {
		for _, name := range []string{"customer_id", "project_id", "po_so_number", "notes", "footer", "internal_note", "discount_type", "discount_value"} {
			if _, ok := fields[name]; !ok {
				fields[name] = nil
			}
		}
		if _, ok := fields["line_items"]; !ok {
			fields["line_items"] = []any{}
		}
	}
	// The create validator shares the same constraints on individual values. Update makes every field optional.
	check := make(map[string]any, len(fields)+1)
	for k, v := range fields {
		check[k] = v
	}
	if check["currency"] == nil {
		check["currency"] = "USD"
	}
	if check["invoice_type"] == nil {
		delete(check, "invoice_type")
	}
	if check["line_items"] == nil {
		delete(check, "line_items")
	}
	validationBody, _ := json.Marshal(check)
	if errs := validateInvoiceCreate(validationBody); !template && len(errs) > 0 {
		vendorUpdateValidationResponse(w, errs)
		return
	}
	positionOverflow := false
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
			if _, ok := value.(bool); ok {
				continue
			}
			rat, valid := boundedItemRat(fmt.Sprint(value))
			if !valid || !rat.IsInt() {
				typ, msg := "int_type", "Input should be a valid integer"
				switch value.(type) {
				case string:
					typ, msg = "int_parsing", "Input should be a valid integer, unable to parse string as an integer"
				case json.Number:
					typ, msg = "int_from_float", "Input should be a valid integer, got a number with a fractional part"
				}
				vendorUpdateValidationResponse(w, []validationError{{typ, []any{"body", "line_items", i, "position"}, msg, nil, nil}})
				return
			}
			if !rat.Num().IsInt64() || rat.Num().Int64() < -2147483648 || rat.Num().Int64() > 2147483647 {
				positionOverflow = true
			}
		}
	}
	ctx := r.Context()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	defer tx.Rollback(ctx)
	q := s.queries(tx)
	user, err := q.lockAdminRole(ctx, current)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && user != "admin" {
		authError(w, 403, "admin required")
		return
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	inv, err := q.LockInvoice(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && s.scoped && template != inv.IsTemplate) {
		if s.scoped {
			message := "invoice not found"
			if template {
				message = "recurring template not found"
			}
			authError(w, 400, message)
			return
		}
		if template {
			authError(w, 400, fmt.Sprintf("recurring template %s not found", rawID))
		} else {
			authError(w, 400, fmt.Sprintf("invoice %x-%x-%x-%x-%x not found", id.Bytes[:4], id.Bytes[4:6], id.Bytes[6:8], id.Bytes[8:10], id.Bytes[10:]))
		}
		return
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if template {
		if !inv.IsTemplate {
			authError(w, 400, fmt.Sprintf("recurring template %s not found", rawID))
			return
		}
		schedule, parseErr := recurringschedule.Parse(inv.BillingCycleRef)
		if parseErr == nil {
			next, e := nextTemplateCycle(ctx, q, id, schedule)
			if e != nil {
				authError(w, 500, "internal server error")
				return
			}
			if next == nil {
				authError(w, 409, "recurring template has ended and is view-only")
				return
			}
		}
		if len(fields["line_items"].([]any)) == 0 {
			authError(w, 400, "recurring template must have at least one line item")
			return
		}
		var scheduleMap map[string]json.RawMessage
		_ = json.Unmarshal(scheduleJSON, &scheduleMap)
		if string(scheduleMap["end_mode"]) == `"ON_DATE"` && len(scheduleMap["end_date"]) == 0 {
			authError(w, 400, "invalid schedule: end_mode=ON_DATE requires end_date")
			return
		}
		if string(scheduleMap["end_mode"]) == `"AFTER_N"` && len(scheduleMap["end_after_cycles"]) == 0 {
			authError(w, 400, "invalid schedule: end_mode=AFTER_N requires positive integer end_after_cycles")
			return
		}
		if _, e := recurringschedule.Parse(scheduleJSON); e != nil {
			invalid()
			return
		}
		for _, name := range []string{"customer_id", "project_id"} {
			if value := fields[name]; !s.scoped && value != nil {
				rid, _ := parsePathUUID(value.(string))
				var e error
				if name == "customer_id" {
					e = q.lockCustomer(ctx, rid)
				} else {
					e = q.lockProject(ctx, rid)
				}
				if e != nil && !errors.Is(e, pgx.ErrNoRows) {
					authError(w, 500, "internal server error")
					return
				}
			}
		}

		var old, newSchedule map[string]json.RawMessage
		_ = json.Unmarshal(inv.BillingCycleRef, &old)
		_ = json.Unmarshal(scheduleJSON, &newSchedule)
		if string(old["paused"]) == "true" {
			newSchedule["paused"] = json.RawMessage("true")
		}
		scheduleJSON, _ = json.Marshal(newSchedule)
	}
	if !template && inv.Status != "DRAFT" {
		authError(w, 400, fmt.Sprintf("only DRAFT invoices can be edited (got %s)", inv.Status))
		return
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
	setUUID("customer_id", &inv.CustomerID)
	setUUID("project_id", &inv.ProjectID)
	if s.scoped {
		if inv.CustomerID.Valid && !s.acceptReference(w, internalError, q.lockCustomer(ctx, inv.CustomerID)) {
			return
		}
		if inv.ProjectID.Valid && !s.acceptReference(w, internalError, q.lockProject(ctx, inv.ProjectID)) {
			return
		}
	}
	if list, ok := fields["line_items"].([]any); ok {
		itemIDs := map[[16]byte]pgtype.UUID{}
		for _, line := range list {
			if value := line.(map[string]any)["item_id"]; value != nil {
				rid, _ := parsePathUUID(value.(string))
				itemIDs[rid.Bytes] = rid
			}
		}
		for _, rid := range orderedItemIDs(itemIDs) {
			e := q.lockItem(ctx, rid, template)
			if !s.acceptReference(w, internalError, e) {
				return
			}
		}
	}
	if positionOverflow {
		authError(w, 500, "internal server error")
		return
	}
	lines, err := q.LockInvoiceLines(ctx, id)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	for _, name := range []string{"invoice_type", "currency"} {
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
	if _, changed := fields["invoice_number"]; changed && s.scoped {
		if err = q.LockInvoiceNumberAllocation(ctx); err != nil {
			authError(w, 500, "internal server error")
			return
		}
	}
	if v, exists := fields["invoice_type"]; exists {
		inv.InvoiceType = v.(string)
	}
	if v, exists := fields["currency"]; exists {
		inv.Currency = v.(string)
	}
	setText("invoice_number", &inv.InvoiceNumber)
	setText("po_so_number", &inv.PoSoNumber)
	setText("payment_terms", &inv.PaymentTerms)
	setText("notes", &inv.Notes)
	setText("footer", &inv.Footer)
	setText("internal_note", &inv.InternalNote)
	setText("discount_type", &inv.DiscountType)
	setDate("issue_date", &inv.IssueDate)
	setDate("due_date", &inv.DueDate)
	if v, exists := fields["discount_value"]; exists {
		inv.DiscountValue = pgtype.Numeric{}
		if v != nil {
			b, _ := json.Marshal(v)
			d, _, _ := invoiceDecimal(b, false)
			inv.DiscountValue = invoiceNumeric(d)
		}
	}
	if v, exists := fields["line_items"]; exists && v != nil {
		if err = q.DeleteInvoiceLines(ctx, id); err != nil {
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
			if m["position"] != nil {
				switch x := m["position"].(type) {
				case bool:
					if x {
						pos = 1
					}
				default:
					if rat, ok := boundedItemRat(fmt.Sprint(x)); ok && rat.IsInt() && rat.Num().IsInt64() {
						pos = int32(rat.Num().Int64())
					}
				}
			}
			e = q.InsertDraftInvoiceLine(ctx, dbquery.InsertDraftInvoiceLineParams{LineItemID: lid, InvoiceID: id, ItemID: item, Position: pos, Description: m["description"].(string), Quantity: invoiceNumeric(qty), UnitPrice: invoiceNumeric(price), Amount: invoiceNumeric(qty.Mul(price).Round(4))})
			if e != nil {
				authError(w, 500, "internal server error")
				return
			}
			lines = append(lines, dbquery.InvoiceLineItem{Quantity: invoiceNumeric(qty), UnitPrice: invoiceNumeric(price)})
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
	_, typeSet := fields["wht_type"]
	_, rateSet := fields["wht_rate"]
	_, grossSet := fields["wht_gross_up"]
	if typeSet || rateSet || grossSet {
		old := inv.WhtType
		if v := fields["wht_type"]; v != nil {
			inv.WhtType = v.(string)
		}
		if v := fields["wht_rate"]; v != nil {
			b, _ := json.Marshal(v)
			text := strings.Trim(string(b), "\"")
			d, e := decimal.NewFromString(text)
			if e != nil {
				invalid()
				return
			}
			inv.WhtRate = invoiceNumeric(d)
		} else if inv.WhtType != old {
			inv.WhtRate = pgtype.Numeric{}
		}
		if v := fields["wht_gross_up"]; v != nil {
			switch x := v.(type) {
			case bool:
				inv.WhtGrossUp = x
			case string:
				inv.WhtGrossUp = strings.Contains("|1|true|on|yes|y|t|", "|"+strings.ToLower(x)+"|")
			case json.Number:
				n, _ := x.Float64()
				inv.WhtGrossUp = n == 1
			}
		}
		if inv.WhtType == "NONE" {
			inv.WhtRate = pgtype.Numeric{}
			inv.WhtGrossUp = false
		} else {
			if !inv.WhtRate.Valid {
				d := decimal.NewFromInt(2)
				if inv.WhtType == "PPH21" {
					d = decimal.RequireFromString("2.5")
				}
				inv.WhtRate = invoiceNumeric(d)
			}
			d, e := updateNumeric(inv.WhtRate)
			if e != nil {
				authError(w, 500, "internal server error")
				return
			}
			if d.LessThanOrEqual(decimal.Zero) || d.GreaterThanOrEqual(decimal.NewFromInt(100)) {
				authError(w, 400, "Withholding tax rate must be greater than 0 and less than 100")
				return
			}
		}
	}
	rate := decimal.Zero
	if inv.WhtType != "NONE" && !base.IsZero() {
		var e error
		rate, e = updateNumeric(inv.WhtRate)
		if e != nil {
			authError(w, 500, "internal server error")
			return
		}
	}
	amount, wht := invoiceWithholdingTotals(base, inv.Currency, inv.WhtType, rate, inv.WhtGrossUp)
	if !template && !inv.DueDate.Valid && inv.IssueDate.Valid {
		var terms *string
		if inv.PaymentTerms.Valid {
			terms = &inv.PaymentTerms.String
		}
		inv.DueDate = pgtype.Date{Time: inv.IssueDate.Time.AddDate(0, 0, invoiceDays(terms)), Valid: true}
	}
	err = q.UpdateDraftInvoice(ctx, dbquery.UpdateDraftInvoiceParams{InvoiceID: id, CustomerID: inv.CustomerID, ProjectID: inv.ProjectID, InvoiceType: inv.InvoiceType, Currency: inv.Currency, InvoiceNumber: inv.InvoiceNumber, PoSoNumber: inv.PoSoNumber, IssueDate: inv.IssueDate, DueDate: inv.DueDate, PaymentTerms: inv.PaymentTerms, Notes: inv.Notes, Footer: inv.Footer, InternalNote: inv.InternalNote, DiscountType: inv.DiscountType, DiscountValue: inv.DiscountValue, WhtType: inv.WhtType, WhtRate: inv.WhtRate, WhtGrossUp: inv.WhtGrossUp, Subtotal: invoiceNumeric(subtotal), WhtBase: invoiceNumeric(base), WhtAmount: invoiceNumeric(wht), Amount: invoiceNumeric(amount)})
	if err != nil {
		s.invoiceWriteError(w, err, internalError)
		return
	}
	if template {
		err = q.UpdateTemplateSchedule(ctx, dbquery.UpdateTemplateScheduleParams{InvoiceID: id, BillingCycleRef: scheduleJSON})
		if err != nil {
			authError(w, 500, "internal server error")
			return
		}
	}
	inv, err = q.GetInvoice(ctx, id)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	lines, err = q.InvoiceLinesByIDs(ctx, []pgtype.UUID{id})
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	out, err := invoiceJSON(inv, lines)
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
