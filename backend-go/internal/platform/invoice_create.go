package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
	"github.com/wenhaokho/wenhao-billing/backend/internal/recurringschedule"
)

type invoiceCreateLine struct {
	ItemID      *string         `json:"item_id"`
	Description *string         `json:"description"`
	Quantity    json.RawMessage `json:"quantity"`
	UnitPrice   json.RawMessage `json:"unit_price"`
	Position    *int32          `json:"position"`
}
type invoiceCreateInput struct {
	CustomerID    *string             `json:"customer_id"`
	ProjectID     *string             `json:"project_id"`
	InvoiceType   *string             `json:"invoice_type"`
	Currency      *string             `json:"currency"`
	InvoiceNumber *string             `json:"invoice_number"`
	PoSoNumber    *string             `json:"po_so_number"`
	IssueDate     *string             `json:"issue_date"`
	DueDate       *string             `json:"due_date"`
	PaymentTerms  *string             `json:"payment_terms"`
	Notes         *string             `json:"notes"`
	Footer        *string             `json:"footer"`
	InternalNote  *string             `json:"internal_note"`
	DiscountType  *string             `json:"discount_type"`
	DiscountValue json.RawMessage     `json:"discount_value"`
	WhtType       *string             `json:"wht_type"`
	WhtRate       json.RawMessage     `json:"wht_rate"`
	WhtGrossUp    *bool               `json:"wht_gross_up"`
	LineItems     []invoiceCreateLine `json:"line_items"`
}

type invoiceCreateLineSchema struct {
	ItemID      *string `json:"item_id,omitempty" format:"uuid"`
	Description string  `json:"description"`
	Quantity    any     `json:"quantity"`
	UnitPrice   any     `json:"unit_price"`
	Position    int     `json:"position,omitempty" default:"0"`
}
type invoiceCreateSchema struct {
	CustomerID    *string                   `json:"customer_id,omitempty" format:"uuid"`
	ProjectID     *string                   `json:"project_id,omitempty" format:"uuid"`
	InvoiceType   string                    `json:"invoice_type,omitempty" default:"MILESTONE"`
	Currency      string                    `json:"currency" minLength:"3" maxLength:"3"`
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

func registerInvoiceCreate(api huma.API, s sessionHandler) {
	response := func(desc string, body any) *huma.Response {
		return &huma.Response{Description: desc, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(body), true, "")}}}
	}
	responses := map[string]*huma.Response{"400": response("Invalid invoice", authErrorBody{}), "401": response("Not authenticated", authErrorBody{}), "403": response("Admin required", authErrorBody{}), "413": response("Body too large", authErrorBody{}), "422": response("Validation error", vendorUpdateValidationErrorBody{}), "500": response("Internal server error", authErrorBody{})}
	huma.Register(api, huma.Operation{Method: "POST", Path: "/api/v1/invoices", OperationID: "create-invoice", DefaultStatus: 201, SkipValidateBody: true, Responses: responses, Security: []map[string][]string{{"sessionCookie": {}}}, Middlewares: huma.Middlewares{sessionMiddleware(s.invoiceCreate)}}, func(context.Context, *struct{ Body invoiceCreateSchema }) (*struct{ Body invoiceOutputSchema }, error) {
		panic("middleware")
	})
	templateResponses := make(map[string]*huma.Response, len(responses))
	for k, v := range responses {
		templateResponses[k] = v
	}
	templateResponses["500"] = &huma.Response{Description: "Internal server error", Content: map[string]*huma.MediaType{"text/plain": {Schema: &huma.Schema{Type: "string"}}}}
	huma.Register(api, huma.Operation{Method: "POST", Path: "/api/v1/invoices/recurring-template", OperationID: "create-recurring-template", DefaultStatus: 201, SkipValidateBody: true, Responses: templateResponses, Security: []map[string][]string{{"sessionCookie": {}}}, Middlewares: huma.Middlewares{sessionMiddleware(s.invoiceCreate)}}, func(context.Context, *struct{ Body recurringTemplateSchema }) (*struct{ Body invoiceOutputSchema }, error) {
		panic("middleware")
	})
	for _, entry := range []struct {
		schema   string
		names    []string
		nullable bool
	}{{"InvoiceCreateSchema", []string{"discount_value", "wht_rate"}, true}, {"RecurringTemplateSchema", []string{"discount_value", "wht_rate"}, true}, {"InvoiceCreateLineSchema", []string{"quantity", "unit_price"}, false}} {
		schema := api.OpenAPI().Components.Schemas.Map()[entry.schema]
		schema.AdditionalProperties = true
		for _, name := range entry.names {
			variants := []*huma.Schema{{Type: "number"}, {Type: "string"}}
			if entry.nullable {
				variants = append(variants, &huma.Schema{Type: "null"})
			}
			*schema.Properties[name] = huma.Schema{AnyOf: variants}
		}
	}
}
func invoiceDecimal(raw json.RawMessage, required bool) (decimal.Decimal, bool, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return decimal.Zero, !required, false
	}
	n, ok := itemMoney(raw)
	if !ok {
		return decimal.Zero, false, false
	}
	s, err := n.Value()
	if err != nil {
		return decimal.Zero, false, false
	}
	d, err := decimal.NewFromString(s.(string))
	return d, err == nil, true
}

var invoiceNetTerms = regexp.MustCompile(`(?i)^\s*net\s+(\d+)\s*$`)

func invoiceDays(terms *string) int {
	if terms == nil {
		return 14
	}
	t := strings.TrimSpace(*terms)
	switch strings.ToLower(t) {
	case "on receipt", "on-receipt", "due on receipt":
		return 0
	}
	m := invoiceNetTerms.FindStringSubmatch(t)
	if m != nil {
		n, e := strconv.Atoi(m[1])
		if e == nil && n < 3000000 {
			return n
		}
	}
	return 14
}
func (s sessionHandler) invoiceCreate(w http.ResponseWriter, r *http.Request) {
	template := strings.HasSuffix(r.URL.Path, "/recurring-template")
	internalError := func() {
		if template {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("Internal Server Error"))
			return
		}
		authError(w, 500, "internal server error")
	}
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
	if len(body) == 0 && template {
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
		body, scheduleJSON, valid = validateRecurringTemplate(w, body)
		if !valid {
			return
		}
	}
	if !template {
		if errs := validateInvoiceCreate(body); len(errs) > 0 {
			vendorUpdateValidationResponse(w, errs)
			return
		}
	}
	var normalized map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	_ = decoder.Decode(&normalized)
	if v, ok := normalized["wht_gross_up"]; ok && v != nil {
		switch x := v.(type) {
		case string:
			switch strings.ToLower(x) {
			case "1", "true", "on", "yes", "y", "t":
				normalized["wht_gross_up"] = true
			case "0", "false", "off", "no", "n", "f":
				normalized["wht_gross_up"] = false
			}
		case json.Number:
			n, _ := x.Float64()
			normalized["wht_gross_up"] = n == 1
		}
	}
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
					if rat, ok := boundedItemRat(text); ok && rat.IsInt() && rat.Num().IsInt64() {
						obj["position"] = rat.Num().Int64()
					}
				}
			}
		}
	}
	body, _ = json.Marshal(normalized)
	dec := json.NewDecoder(bytes.NewReader(body))
	var b invoiceCreateInput
	if e := dec.Decode(&b); e != nil {
		invalid()
		return
	}
	var extra any
	if dec.Decode(&extra) != io.EOF {
		invalid()
		return
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(body, &fields) != nil || fields == nil || string(fields["invoice_type"]) == "null" || string(fields["line_items"]) == "null" {
		invalid()
		return
	}
	if b.Currency == nil || utf8.RuneCountInString(*b.Currency) != 3 {
		invalid()
		return
	}
	for _, id := range []*string{b.CustomerID, b.ProjectID} {
		if id != nil {
			if _, ok := parsePathUUID(*id); !ok {
				invalid()
				return
			}
		}
	}
	if b.DiscountType != nil && *b.DiscountType != "PERCENT" && *b.DiscountType != "AMOUNT" {
		invalid()
		return
	}
	if b.WhtType != nil && *b.WhtType != "NONE" && *b.WhtType != "PPH21" && *b.WhtType != "PPH23" {
		invalid()
		return
	}
	discount, ok, hasDiscount := invoiceDecimal(b.DiscountValue, false)
	if !ok {
		invalid()
		return
	}
	rateRaw := b.WhtRate
	negativeRate := false
	if len(rateRaw) > 0 {
		var text string
		if rateRaw[0] == '"' {
			_ = json.Unmarshal(rateRaw, &text)
		} else {
			text = string(rateRaw)
		}
		if strings.HasPrefix(text, "-") {
			negativeRate = true
			rateRaw, _ = json.Marshal(strings.TrimPrefix(text, "-"))
		}
	}
	rate, ok, hasRate := invoiceDecimal(rateRaw, false)
	if negativeRate {
		rate = rate.Neg()
	}
	if !ok || hasRate && rate.Abs().GreaterThanOrEqual(decimal.NewFromInt(1000)) {
		invalid()
		return
	}
	if len(b.LineItems) == 0 {
		message := "invoice must have at least one line item"
		if template {
			message = "recurring template must have at least one line item"
		}
		authError(w, 400, message)
		return
	}
	subtotal := decimal.Zero
	lineAmounts := make([]decimal.Decimal, len(b.LineItems))
	qtys := make([]decimal.Decimal, len(b.LineItems))
	prices := make([]decimal.Decimal, len(b.LineItems))
	for i, l := range b.LineItems {
		if l.Description == nil {
			invalid()
			return
		}
		if l.ItemID != nil {
			if _, ok := parsePathUUID(*l.ItemID); !ok {
				invalid()
				return
			}
		}
		q, ok, _ := invoiceDecimal(l.Quantity, true)
		if !ok {
			invalid()
			return
		}
		p, ok, _ := invoiceDecimal(l.UnitPrice, true)
		if !ok {
			invalid()
			return
		}
		qtys[i] = q
		prices[i] = p
		product := q.Mul(p)
		subtotal = subtotal.Add(product)
		lineAmounts[i] = product.Round(4)
	}
	subtotal = subtotal.Round(4)
	discountType := ""
	if b.DiscountType != nil {
		discountType = *b.DiscountType
	}
	base := invoiceDiscountedBase(subtotal, discountType, discount, hasDiscount)
	today := time.Now()
	issue := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, today.Location())
	if b.IssueDate != nil {
		var e error
		issue, e = time.Parse("2006-01-02", *b.IssueDate)
		if e != nil {
			invalid()
			return
		}
	}
	due := issue.AddDate(0, 0, invoiceDays(b.PaymentTerms))
	if b.DueDate != nil {
		var e error
		due, e = time.Parse("2006-01-02", *b.DueDate)
		if e != nil {
			invalid()
			return
		}
	}
	ctx := r.Context()
	tx, e := s.db.Begin(ctx)
	if e != nil {
		internalError()
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
		internalError()
		return
	}
	if template {
		if b.CustomerID != nil {
			id, _ := parsePathUUID(*b.CustomerID)
			_, e = q.LockCustomer(ctx, id)
			if e != nil && !errors.Is(e, pgx.ErrNoRows) {
				internalError()
				return
			}
		}
		if b.ProjectID != nil {
			id, _ := parsePathUUID(*b.ProjectID)
			_, e = q.LockProject(ctx, id)
			if e != nil && !errors.Is(e, pgx.ErrNoRows) {
				internalError()
				return
			}
		}
		ids := map[string]bool{}
		for _, line := range b.LineItems {
			if line.ItemID != nil {
				ids[*line.ItemID] = true
			}
		}
		ordered := make([]string, 0, len(ids))
		for id := range ids {
			ordered = append(ordered, id)
		}
		sort.Strings(ordered)
		for _, value := range ordered {
			id, _ := parsePathUUID(value)
			_, e = q.LockItem(ctx, id)
			if e != nil && !errors.Is(e, pgx.ErrNoRows) {
				internalError()
				return
			}
		}
	}
	typ := "NONE"
	gross := false
	if b.WhtType != nil {
		typ = *b.WhtType
		gross = b.WhtGrossUp != nil && *b.WhtGrossUp
	} else if b.CustomerID != nil {
		customerID, _ := parsePathUUID(*b.CustomerID)
		defaults, err := q.CustomerWithholdingDefaults(ctx, customerID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			internalError()
			return
		}
		if err == nil {
			typ = defaults.DefaultWhtType
			gross = defaults.DefaultWhtGrossUp
			if defaults.DefaultWhtRate.Valid {
				v, err := defaults.DefaultWhtRate.Value()
				if err != nil {
					internalError()
					return
				}
				rate, err = decimal.NewFromString(v.(string))
				if err != nil {
					internalError()
					return
				}
				hasRate = true
			} else {
				hasRate = false
			}
		}

	}
	if typ == "NONE" {
		hasRate = false
		gross = false
	} else {
		if !hasRate {
			if typ == "PPH21" {
				rate = decimal.RequireFromString("2.5")
			} else {
				rate = decimal.NewFromInt(2)
			}
			hasRate = true
		}
		if rate.LessThanOrEqual(decimal.Zero) || rate.GreaterThanOrEqual(decimal.NewFromInt(100)) {
			authError(w, 400, "Withholding tax rate must be greater than 0 and less than 100")
			return
		}
	}
	amount, wht := invoiceWithholdingTotals(base, *b.Currency, typ, rate, gross)
	var rateArg, discountArg pgtype.Numeric
	if hasRate {
		rateArg = invoiceNumeric(rate)
	}
	if hasDiscount {
		discountArg = invoiceNumeric(discount)
	}

	number := ""
	if b.InvoiceNumber != nil {
		number = *b.InvoiceNumber
	}
	if !template {
		e = q.LockInvoiceNumberAllocation(ctx)
		if e != nil {
			internalError()
			return
		}
		if number == "" {
			prefix := "WH" + strconv.Itoa(today.Year())
			rows, err := q.InvoiceNumbersForPrefix(ctx, pgtype.Text{String: prefix + "%", Valid: true})
			if err != nil {
				internalError()
				return
			}
			max := new(big.Int)
			for _, n := range rows {
				suffix := strings.TrimPrefix(n.String, prefix)
				v, digit, invalid := documentNumberSuffix(suffix)
				if invalid {
					internalError()
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

	}
	invType := "MILESTONE"
	if b.InvoiceType != nil && *b.InvoiceType != "" {
		invType = *b.InvoiceType
	}
	if template {
		invType = "RECURRING"
	}
	id, e := itemUUID()
	if e != nil {
		internalError()
		return
	}
	var customerID, projectID pgtype.UUID
	if b.CustomerID != nil {
		customerID, _ = parsePathUUID(*b.CustomerID)
	}
	if b.ProjectID != nil {
		projectID, _ = parsePathUUID(*b.ProjectID)
	}
	e = q.InsertDraftInvoice(ctx, dbquery.InsertDraftInvoiceParams{InvoiceID: id, CustomerID: customerID, ProjectID: projectID, InvoiceType: invType, InvoiceNumber: pgtype.Text{String: number, Valid: !template}, PoSoNumber: textValue(b.PoSoNumber), Currency: *b.Currency, Subtotal: invoiceNumeric(subtotal), DiscountType: textValue(b.DiscountType), DiscountValue: discountArg, WhtType: typ, WhtRate: rateArg, WhtGrossUp: gross, WhtBase: invoiceNumeric(base), WhtAmount: invoiceNumeric(wht), Amount: invoiceNumeric(amount), IssueDate: pgtype.Date{Time: issue, Valid: !template}, DueDate: pgtype.Date{Time: due, Valid: !template}, PaymentTerms: textValue(b.PaymentTerms), Notes: textValue(b.Notes), Footer: textValue(b.Footer), InternalNote: textValue(b.InternalNote)})
	if e != nil {
		internalError()
		return
	}
	if template {
		e = q.MarkInvoiceTemplate(ctx, dbquery.MarkInvoiceTemplateParams{InvoiceID: id, BillingCycleRef: scheduleJSON})
		if e != nil {
			internalError()
			return
		}
	}
	for i, l := range b.LineItems {
		lid, err := itemUUID()
		if err != nil {
			internalError()
			return
		}
		pos := int32(0)
		if l.Position != nil {
			pos = *l.Position
		}
		var itemID pgtype.UUID
		if l.ItemID != nil {
			itemID, _ = parsePathUUID(*l.ItemID)
		}
		e = q.InsertDraftInvoiceLine(ctx, dbquery.InsertDraftInvoiceLineParams{LineItemID: lid, InvoiceID: id, ItemID: itemID, Position: pos, Description: *l.Description, Quantity: invoiceNumeric(qtys[i]), UnitPrice: invoiceNumeric(prices[i]), Amount: invoiceNumeric(lineAmounts[i])})
		if e != nil {
			internalError()
			return
		}
	}
	row, e := q.GetInvoice(ctx, id)
	if e != nil {
		internalError()
		return
	}
	lines, e := q.InvoiceLinesByIDs(ctx, []pgtype.UUID{id})
	if e != nil {
		internalError()
		return
	}
	out, e := invoiceJSON(row, lines)
	if e != nil {
		internalError()
		return
	}
	encoded, e := json.Marshal(out)
	if e != nil {
		internalError()
		return
	}
	if e = tx.Commit(ctx); e != nil {
		internalError()
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(201)
	w.Write(append(encoded, '\n'))
}
func fmtNumber(n int) string {
	s := strconv.Itoa(n)
	if len(s) < 4 {
		return strings.Repeat("0", 4-len(s)) + s
	}
	return s
}

func invoiceNumeric(d decimal.Decimal) pgtype.Numeric {
	var n pgtype.Numeric
	_ = n.Scan(d.String())
	return n
}

// invoiceDiscountedBase and invoiceWithholdingTotals are shared by draft creation and updates.
func invoiceDiscountedBase(subtotal decimal.Decimal, typ string, discount decimal.Decimal, hasDiscount bool) decimal.Decimal {
	base := subtotal
	if hasDiscount {
		if typ == "PERCENT" {
			base = subtotal.Mul(decimal.NewFromInt(1).Sub(discount.Div(decimal.NewFromInt(100)))).Round(4)
		} else if typ == "AMOUNT" {
			base = subtotal.Sub(discount).Round(4)
		}
	}
	if base.IsNegative() {
		return decimal.Zero
	}
	return base
}
func invoiceWithholdingTotals(base decimal.Decimal, currency, typ string, rate decimal.Decimal, gross bool) (decimal.Decimal, decimal.Decimal) {
	amount, wht := base, decimal.Zero
	if typ != "NONE" && !base.IsZero() {
		scale := int32(2)
		if currency == "IDR" {
			scale = 0
		}
		if gross {
			amount = base.DivRound(decimal.NewFromInt(1).Sub(rate.Div(decimal.NewFromInt(100))), 28).Round(scale)
			wht = amount.Sub(base)
		} else {
			wht = base.Mul(rate).Div(decimal.NewFromInt(100)).Round(scale)
		}
	}
	return amount, wht
}

func validateRecurringTemplate(w http.ResponseWriter, body []byte, deferSchedule ...bool) ([]byte, []byte, bool) {
	var obj map[string]json.RawMessage
	if json.Unmarshal(body, &obj) != nil || obj == nil {
		errs := validateInvoiceCreate(body)
		if len(errs) > 0 {
			vendorUpdateValidationResponse(w, errs)
			return nil, nil, false
		}
		return body, nil, true
	}
	for _, name := range []string{"invoice_type", "invoice_number", "issue_date", "due_date"} {
		delete(obj, name)
	}
	base, _ := json.Marshal(obj)
	errs := validateInvoiceCreate(base)
	if _, ok := obj["payment_terms"]; !ok {
		errs = append(errs, validationError{"missing", []any{"body", "payment_terms"}, "Field required", nil, nil})
	} else if string(obj["payment_terms"]) == "null" {
		errs = append(errs, validationError{"string_type", []any{"body", "payment_terms"}, "Input should be a valid string", nil, nil})
	}
	sortErrors := func(es []validationError) {
		order := map[string]int{"customer_id": 0, "project_id": 1, "currency": 2, "po_so_number": 3, "payment_terms": 4, "notes": 5, "footer": 6, "internal_note": 7, "discount_type": 8, "discount_value": 9, "wht_type": 10, "wht_rate": 11, "wht_gross_up": 12, "line_items": 13, "schedule": 14}
		sort.SliceStable(es, func(i, j int) bool { return order[es[i].Loc[1].(string)] < order[es[j].Loc[1].(string)] })
	}
	if _, ok := obj["schedule"]; !ok {
		errs = append(errs, validationError{"missing", []any{"body", "schedule"}, "Field required", nil, nil})
		sortErrors(errs)
		vendorUpdateValidationResponse(w, errs)
		return nil, nil, false
	}
	fail := func() ([]byte, []byte, bool) {
		vendorUpdateValidationResponse(w, []validationError{{"value_error", []any{"body"}, "Invalid input", nil, nil}})
		return nil, nil, false
	}
	var rawSchedule any
	decoder := json.NewDecoder(bytes.NewReader(obj["schedule"]))
	decoder.UseNumber()
	_ = decoder.Decode(&rawSchedule)
	schedule, ok := rawSchedule.(map[string]any)
	if !ok {
		errs = append(errs, validationError{"model_attributes_type", []any{"body", "schedule"}, "Input should be a valid dictionary or object to extract fields from", rawSchedule, nil})
		sortErrors(errs)
		vendorUpdateValidationResponse(w, errs)
		return nil, nil, false
	}
	errors := errs
	add := func(typ, name, msg string, value, ctx any) {
		errors = append(errors, validationError{typ, []any{"body", "schedule", name}, msg, value, ctx})
	}
	normalized := map[string]any{}
	for _, spec := range []struct {
		name     string
		values   []string
		expected string
		required bool
		def      string
	}{{"frequency", []string{"DAILY", "WEEKLY", "MONTHLY", "YEARLY"}, "'DAILY', 'WEEKLY', 'MONTHLY' or 'YEARLY'", true, ""}, {"end_mode", []string{"NEVER", "ON_DATE", "AFTER_N"}, "'NEVER', 'ON_DATE' or 'AFTER_N'", false, "NEVER"}} {
		v, exists := schedule[spec.name]
		if !exists {
			if spec.required {
				add("missing", spec.name, "Field required", schedule, nil)
			} else {
				normalized[spec.name] = spec.def
			}
			continue
		}
		valid := false
		for _, x := range spec.values {
			if v == x {
				valid = true
				break
			}
		}
		if !valid {
			add("literal_error", spec.name, "Input should be "+spec.expected, v, map[string]string{"expected": spec.expected})
		} else {
			normalized[spec.name] = v
		}
	}
	for _, name := range []string{"interval", "end_after_cycles"} {
		v, exists := schedule[name]
		if !exists {
			if name == "interval" {
				normalized[name] = 1
			}
			continue
		}
		if v == nil && name == "end_after_cycles" {
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
		if !valid || !rat.IsInt() || !rat.Num().IsInt64() {
			typ, msg := "int_parsing", "Input should be a valid integer, unable to parse string as an integer"
			if v == nil {
				typ, msg = "int_type", "Input should be a valid integer"
			} else if _, ok := v.(json.Number); ok && valid && !rat.IsInt() {
				typ, msg = "int_from_float", "Input should be a valid integer, got a number with a fractional part"
			}
			add(typ, name, msg, v, nil)
			continue
		}
		n := rat.Num().Int64()
		if n < 1 {
			add("greater_than_equal", name, "Input should be greater than or equal to 1", v, map[string]int{"ge": 1})
		} else {
			normalized[name] = n
		}
	}
	for _, name := range []string{"start_date", "end_date"} {
		v, exists := schedule[name]
		if !exists {
			if name == "start_date" {
				add("missing", name, "Field required", schedule, nil)
			}
			continue
		}
		if v == nil && name == "end_date" {
			continue
		}
		switch v.(type) {
		case string, json.Number:
		default:
			add("date_type", name, "Input should be a valid date", v, nil)
			continue
		}
		d, problem := invoiceDate(v)
		if problem != nil {
			problem.Loc = []any{"body", "schedule", name}
			problem.Input = v
			errors = append(errors, *problem)
		} else {
			normalized[name] = d.Format("2006-01-02")
		}
	}
	order := map[string]int{"frequency": 0, "interval": 1, "start_date": 2, "end_mode": 3, "end_date": 4, "end_after_cycles": 5}
	sort.SliceStable(errors, func(i, j int) bool {
		if len(errors[i].Loc) < 3 || errors[i].Loc[1] != "schedule" {
			return false
		}
		if len(errors[j].Loc) < 3 || errors[j].Loc[1] != "schedule" {
			return false
		}
		return order[errors[i].Loc[2].(string)] < order[errors[j].Loc[2].(string)]
	})
	sortErrors(errors)
	if len(errors) > 0 {
		vendorUpdateValidationResponse(w, errors)
		return nil, nil, false
	}
	if len(deferSchedule) == 0 && normalized["end_mode"] == "ON_DATE" && normalized["end_date"] == nil {
		authError(w, 400, "invalid schedule: end_mode=ON_DATE requires end_date")
		return nil, nil, false
	}
	if len(deferSchedule) == 0 && normalized["end_mode"] == "AFTER_N" && normalized["end_after_cycles"] == nil {
		authError(w, 400, "invalid schedule: end_mode=AFTER_N requires positive integer end_after_cycles")
		return nil, nil, false
	}
	raw, _ := json.Marshal(normalized)
	_, err := recurringschedule.Parse(raw)
	if err != nil && len(deferSchedule) == 0 {
		return fail()
	}
	delete(obj, "invoice_type")
	delete(obj, "invoice_number")
	delete(obj, "issue_date")
	delete(obj, "due_date")
	delete(obj, "schedule")
	clean, _ := json.Marshal(obj)
	return clean, raw, true
}

type recurringScheduleSchema struct {
	Frequency      string  `json:"frequency" enum:"DAILY,WEEKLY,MONTHLY,YEARLY"`
	Interval       int     `json:"interval,omitempty" minimum:"1" default:"1"`
	StartDate      string  `json:"start_date" format:"date"`
	EndMode        string  `json:"end_mode,omitempty" enum:"NEVER,ON_DATE,AFTER_N" default:"NEVER"`
	EndDate        *string `json:"end_date,omitempty" format:"date"`
	EndAfterCycles *int    `json:"end_after_cycles,omitempty" minimum:"1"`
}
type recurringTemplateSchema struct {
	CustomerID    *string                   `json:"customer_id,omitempty" format:"uuid"`
	ProjectID     *string                   `json:"project_id,omitempty" format:"uuid"`
	Currency      string                    `json:"currency" minLength:"3" maxLength:"3"`
	PoSoNumber    *string                   `json:"po_so_number,omitempty"`
	PaymentTerms  string                    `json:"payment_terms"`
	Notes         *string                   `json:"notes,omitempty"`
	Footer        *string                   `json:"footer,omitempty"`
	InternalNote  *string                   `json:"internal_note,omitempty"`
	DiscountType  *string                   `json:"discount_type,omitempty" enum:"PERCENT,AMOUNT"`
	DiscountValue any                       `json:"discount_value,omitempty"`
	WhtType       *string                   `json:"wht_type,omitempty" enum:"NONE,PPH21,PPH23"`
	WhtRate       any                       `json:"wht_rate,omitempty"`
	WhtGrossUp    *bool                     `json:"wht_gross_up,omitempty"`
	LineItems     []invoiceCreateLineSchema `json:"line_items,omitempty"`
	Schedule      recurringScheduleSchema   `json:"schedule"`
}
