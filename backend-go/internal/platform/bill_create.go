package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
)

type billCreateLine struct {
	ItemID           *string         `json:"item_id"`
	ExpenseAccountID *int32          `json:"expense_account_id"`
	Description      string          `json:"description"`
	Quantity         json.RawMessage `json:"quantity"`
	UnitPrice        json.RawMessage `json:"unit_price"`
	Position         int32           `json:"position"`
}
type billCreateInput struct {
	VendorID      string           `json:"vendor_id"`
	ProjectID     *string          `json:"project_id"`
	BillNumber    *string          `json:"bill_number"`
	PoNumber      *string          `json:"po_number"`
	Currency      string           `json:"currency"`
	IssueDate     *string          `json:"issue_date"`
	DueDate       *string          `json:"due_date"`
	PaymentTerms  *string          `json:"payment_terms"`
	Notes         *string          `json:"notes"`
	DiscountType  *string          `json:"discount_type"`
	DiscountValue json.RawMessage  `json:"discount_value"`
	LineItems     []billCreateLine `json:"line_items"`
}
type billCreateLineSchema struct {
	ItemID           *string `json:"item_id,omitempty" format:"uuid"`
	ExpenseAccountID *int32  `json:"expense_account_id,omitempty"`
	Description      string  `json:"description"`
	Quantity         any     `json:"quantity"`
	UnitPrice        any     `json:"unit_price"`
	Position         int32   `json:"position,omitempty"`
}
type billCreateSchema struct {
	VendorID      string                 `json:"vendor_id" format:"uuid"`
	ProjectID     *string                `json:"project_id,omitempty" format:"uuid"`
	BillNumber    *string                `json:"bill_number,omitempty"`
	PoNumber      *string                `json:"po_number,omitempty"`
	Currency      string                 `json:"currency" minLength:"3" maxLength:"3"`
	IssueDate     *string                `json:"issue_date,omitempty" format:"date"`
	DueDate       *string                `json:"due_date,omitempty" format:"date"`
	PaymentTerms  *string                `json:"payment_terms,omitempty"`
	Notes         *string                `json:"notes,omitempty"`
	DiscountType  *string                `json:"discount_type,omitempty" enum:"PERCENT,AMOUNT"`
	DiscountValue any                    `json:"discount_value,omitempty"`
	LineItems     []billCreateLineSchema `json:"line_items,omitempty"`
}

func registerBillCreate(api huma.API, s sessionHandler) {
	response := func(desc string, body any) *huma.Response {
		return &huma.Response{Description: desc, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(body), true, "")}}}
	}
	responses := map[string]*huma.Response{"401": response("Not authenticated", authErrorBody{}), "403": response("Admin required", authErrorBody{}), "413": response("Body too large", authErrorBody{}), "422": response("Validation error", vendorUpdateValidationErrorBody{}), "500": response("Internal server error", authErrorBody{})}
	responses["500"] = &huma.Response{Description: "Internal server error", Content: map[string]*huma.MediaType{"text/plain": {Schema: &huma.Schema{Type: "string"}}}}
	huma.Register(api, huma.Operation{Method: "POST", Path: "/api/v1/bills", OperationID: "create-bill", DefaultStatus: 201, SkipValidateBody: true, Responses: responses, Security: []map[string][]string{{"sessionCookie": {}}}, Middlewares: huma.Middlewares{sessionMiddleware(s.billCreate)}}, func(context.Context, *struct{ Body billCreateSchema }) (*struct{ Body billSchema }, error) {
		panic("middleware")
	})
	for _, entry := range []struct {
		name     string
		fields   []string
		nullable bool
	}{{"BillCreateSchema", []string{"discount_value"}, true}, {"BillCreateLineSchema", []string{"quantity", "unit_price"}, false}} {
		schema := api.OpenAPI().Components.Schemas.Map()[entry.name]
		schema.AdditionalProperties = true
		for _, name := range entry.fields {
			variants := []*huma.Schema{{Type: "number"}, {Type: "string"}}
			if entry.nullable {
				variants = append(variants, &huma.Schema{Type: "null"})
			}
			*schema.Properties[name] = huma.Schema{AnyOf: variants}
		}
	}
}
func (s sessionHandler) billCreate(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Content-Type") == "" {
		r = r.Clone(r.Context())
		r.Header.Set("Content-Type", "application/json")
	}
	body, ok := readItemBody(w, r)
	if !ok {
		return
	}
	current, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	if !requestJSONMediaType(r) {
		vendorUpdateValidationResponse(w, []validationError{{"value_error", []any{"body"}, "Invalid input", nil, nil}})
		return
	}
	if errs := validateBillCreate(body); len(errs) > 0 {
		vendorUpdateValidationResponse(w, errs)
		return
	}
	var obj map[string]any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	_ = dec.Decode(&obj)
	for _, name := range []string{"issue_date", "due_date"} {
		if v := obj[name]; v != nil {
			d, _ := invoiceDate(v)
			obj[name] = d.Format("2006-01-02")
		}
	}
	if lines, ok := obj["line_items"].([]any); ok {
		for _, lv := range lines {
			l := lv.(map[string]any)
			for _, name := range []string{"position", "expense_account_id"} {
				if v := l[name]; v != nil {
					t := strings.ReplaceAll(strings.ReplaceAll(strings.ToLower(strings.TrimSpace(strings.TrimSpace(stringifyBill(v)))), "true", "1"), "false", "0")
					if n, ok := boundedItemRat(t); ok && n.IsInt() && n.Num().IsInt64() {
						l[name] = n.Num().Int64()
					}
				}
			}
		}
	}
	body, _ = json.Marshal(obj)
	var b billCreateInput
	if json.Unmarshal(body, &b) != nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Internal Server Error"))
		return
	}
	discount, _, hasDiscount := invoiceDecimal(b.DiscountValue, false)
	subtotal := decimal.Zero
	amounts := make([]decimal.Decimal, len(b.LineItems))
	quantities := make([]decimal.Decimal, len(b.LineItems))
	prices := make([]decimal.Decimal, len(b.LineItems))
	for i, l := range b.LineItems {
		quantities[i], _, _ = invoiceDecimal(l.Quantity, true)
		prices[i], _, _ = invoiceDecimal(l.UnitPrice, true)
		amounts[i] = quantities[i].Mul(prices[i]).RoundBank(4)
		subtotal = subtotal.Add(amounts[i])
	}
	total := subtotal
	if b.DiscountType != nil && hasDiscount {
		if *b.DiscountType == "PERCENT" {
			total = subtotal.Mul(decimal.NewFromInt(1).Sub(discount.Shift(-2)))
		} else {
			total = subtotal.Sub(discount)
		}
		if total.IsNegative() {
			total = decimal.Zero
		}
	}
	total = total.RoundBank(4)
	internal := func() {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
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
	vendor, _ := parsePathUUID(b.VendorID)
	_, e = q.LockVendor(ctx, vendor)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		internal()
		return
	}
	var project pgtype.UUID
	if b.ProjectID != nil {
		project, _ = parsePathUUID(*b.ProjectID)
		_, e = q.LockProject(ctx, project)
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			internal()
			return
		}
	}
	ids := map[[16]byte]pgtype.UUID{}
	accounts := map[int32]bool{}
	for _, l := range b.LineItems {
		if l.ItemID != nil {
			id, _ := parsePathUUID(*l.ItemID)
			ids[id.Bytes] = id
		}
		if l.ExpenseAccountID != nil {
			accounts[*l.ExpenseAccountID] = true
		}
	}
	for _, id := range orderedBillItemIDs(ids) {
		_, e = q.LockItem(ctx, id)
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			internal()
			return
		}
	}
	ac := make([]int, 0, len(accounts))
	for id := range accounts {
		ac = append(ac, int(id))
	}
	sort.Ints(ac)
	for _, id := range ac {
		_, e = q.LockAccount(ctx, int32(id))
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			internal()
			return
		}
	}
	id, e := itemUUID()
	if e != nil {
		internal()
		return
	}
	var dv pgtype.Numeric
	if hasDiscount {
		dv = invoiceNumeric(discount)
	}
	date := func(v *string) pgtype.Date {
		if v == nil {
			return pgtype.Date{}
		}
		d, _ := invoiceDate(*v)
		return pgtype.Date{Time: d, Valid: true}
	}
	row, e := q.InsertBill(ctx, dbquery.InsertBillParams{BillID: id, VendorID: vendor, ProjectID: project, BillNumber: textValue(b.BillNumber), PoNumber: textValue(b.PoNumber), Currency: strings.ToUpper(b.Currency), Subtotal: invoiceNumeric(subtotal), DiscountType: textValue(b.DiscountType), DiscountValue: dv, Amount: invoiceNumeric(total), IssueDate: date(b.IssueDate), DueDate: date(b.DueDate), PaymentTerms: textValue(b.PaymentTerms), Notes: textValue(b.Notes)})
	if e != nil {
		internal()
		return
	}
	for i, l := range b.LineItems {
		lid, err := itemUUID()
		if err != nil {
			internal()
			return
		}
		var item pgtype.UUID
		if l.ItemID != nil {
			item, _ = parsePathUUID(*l.ItemID)
		}
		var account pgtype.Int4
		if l.ExpenseAccountID != nil {
			account = pgtype.Int4{Int32: *l.ExpenseAccountID, Valid: true}
		}
		pos := l.Position
		if pos == 0 {
			pos = int32(i)
		}
		e = q.InsertBillLine(ctx, dbquery.InsertBillLineParams{LineItemID: lid, BillID: id, ItemID: item, ExpenseAccountID: account, Position: pos, Description: l.Description, Quantity: invoiceNumeric(quantities[i]), UnitPrice: invoiceNumeric(prices[i]), Amount: invoiceNumeric(amounts[i])})
		if e != nil {
			internal()
			return
		}
	}
	lines, e := q.BillLines(ctx, []pgtype.UUID{id})
	if e != nil {
		internal()
		return
	}
	out, e := billJSON(row, lines)
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
	w.WriteHeader(201)
	_, _ = w.Write(append(encoded, '\n'))
}
func stringifyBill(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case json.Number:
		return x.String()
	case bool:
		return strconv.FormatBool(x)
	default:
		return ""
	}
}
