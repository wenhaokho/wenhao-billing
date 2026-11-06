package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"sort"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
)

type billUpdateSchema struct {
	VendorID      *string                `json:"vendor_id,omitempty" format:"uuid"`
	ProjectID     *string                `json:"project_id,omitempty" format:"uuid"`
	BillNumber    *string                `json:"bill_number,omitempty"`
	PoNumber      *string                `json:"po_number,omitempty"`
	Currency      *string                `json:"currency,omitempty" minLength:"3" maxLength:"3"`
	IssueDate     *string                `json:"issue_date,omitempty" format:"date"`
	DueDate       *string                `json:"due_date,omitempty" format:"date"`
	PaymentTerms  *string                `json:"payment_terms,omitempty"`
	Notes         *string                `json:"notes,omitempty"`
	DiscountType  *string                `json:"discount_type,omitempty" enum:"PERCENT,AMOUNT"`
	DiscountValue any                    `json:"discount_value,omitempty"`
	LineItems     []billCreateLineSchema `json:"line_items,omitempty"`
	Status        *string                `json:"status,omitempty" enum:"DRAFT,OPEN,PARTIAL,PAID,VOID"`
}

func registerBillUpdate(api huma.API, s sessionHandler) {
	response := func(desc string, body any) *huma.Response {
		return &huma.Response{Description: desc, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(body), true, "")}}}
	}
	responses := map[string]*huma.Response{"400": response("Invalid state", authErrorBody{}), "401": response("Not authenticated", authErrorBody{}), "403": response("Admin required", authErrorBody{}), "404": response("Bill not found", authErrorBody{}), "413": response("Body too large", authErrorBody{}), "422": response("Validation error", vendorUpdateValidationErrorBody{}), "500": {Description: "Internal server error", Content: map[string]*huma.MediaType{"text/plain": {Schema: &huma.Schema{Type: "string"}}}}}
	huma.Register(api, huma.Operation{Method: "PATCH", Path: "/api/v1/bills/{bill_id}", OperationID: "update-bill", SkipValidateBody: true, Responses: responses, Security: []map[string][]string{{"sessionCookie": {}}}, Middlewares: huma.Middlewares{sessionMiddleware(s.billUpdate)}}, func(context.Context, *struct {
		ID   string `path:"bill_id" format:"uuid"`
		Body billUpdateSchema
	}) (*struct{ Body billSchema }, error) {
		panic("middleware")
	})
	schema := api.OpenAPI().Components.Schemas.Map()["BillUpdateSchema"]
	schema.AdditionalProperties = true
	*schema.Properties["discount_value"] = huma.Schema{AnyOf: []*huma.Schema{{Type: "number"}, {Type: "string"}, {Type: "null"}}}
}
func (s sessionHandler) billUpdate(w http.ResponseWriter, r *http.Request) {
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
	if errs := validateBill(body, true); len(errs) > 0 {
		vendorUpdateValidationResponse(w, errs)
		return
	}
	raw := chi.URLParam(r, "bill_id")
	id, ok := parsePathUUID(raw)
	if !ok {
		d := uuidErrorDetail(raw)
		validationResponse(w, []validationError{{"uuid_parsing", []any{"path", "bill_id"}, "Input should be a valid UUID, " + d, raw, map[string]string{"error": d}}})
		return
	}
	var obj map[string]any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	_ = dec.Decode(&obj)
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
	if b.Status != "DRAFT" && b.Status != "OPEN" {
		authError(w, 400, "bill is "+b.Status+" and cannot be edited")
		return
	}
	// Fields omitted from the request retain their value; explicit null is applied.
	for k, v := range obj {
		switch k {
		case "vendor_id", "project_id":
			var x pgtype.UUID
			if v != nil {
				x, _ = parsePathUUID(v.(string))
			}
			if k == "vendor_id" {
				b.VendorID = x
			} else {
				b.ProjectID = x
			}
		case "bill_number", "po_number", "payment_terms", "notes", "discount_type":
			var x pgtype.Text
			if v != nil {
				x = pgtype.Text{String: v.(string), Valid: true}
			}
			switch k {
			case "bill_number":
				b.BillNumber = x
			case "po_number":
				b.PoNumber = x
			case "payment_terms":
				b.PaymentTerms = x
			case "notes":
				b.Notes = x
			case "discount_type":
				b.DiscountType = x
			}
		case "currency":
			if v == nil {
				internal()
				return
			}
			b.Currency = strings.ToUpper(v.(string))
		case "status":
			if v == nil {
				internal()
				return
			}
			b.Status = v.(string)
		case "issue_date", "due_date":
			var x pgtype.Date
			if v != nil {
				d, _ := invoiceDate(v)
				x = pgtype.Date{Time: d, Valid: true}
			}
			if k == "issue_date" {
				b.IssueDate = x
			} else {
				b.DueDate = x
			}
		case "discount_value":
			var x pgtype.Numeric
			if v != nil {
				raw, _ := json.Marshal(v)
				d, _, _ := invoiceDecimal(raw, false)
				x = invoiceNumeric(d)
			}
			b.DiscountValue = x
		}
	}
	if b.VendorID.Valid {
		_, e = q.LockVendor(ctx, b.VendorID)
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			internal()
			return
		}
	}
	if b.ProjectID.Valid {
		_, e = q.LockProject(ctx, b.ProjectID)
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			internal()
			return
		}
	}
	if lv, exists := obj["line_items"]; exists && lv != nil {
		lines := lv.([]any)
		items := map[[16]byte]pgtype.UUID{}
		accounts := map[int32]bool{}
		for _, v := range lines {
			l := v.(map[string]any)
			if x, ok := l["item_id"].(string); ok {
				id, _ := parsePathUUID(x)
				items[id.Bytes] = id
			}
			if x := l["expense_account_id"]; x != nil {
				n, ok := billUpdateInt(x)
				if !ok {
					internal()
					return
				}
				accounts[n] = true
			}
		}
		for _, id := range orderedBillItemIDs(items) {
			_, e = q.LockItem(ctx, id)
			if e != nil && !errors.Is(e, pgx.ErrNoRows) {
				internal()
				return
			}
		}
		ac := make([]int, 0, len(accounts))
		for x := range accounts {
			ac = append(ac, int(x))
		}
		sort.Ints(ac)
		for _, x := range ac {
			_, e = q.LockAccount(ctx, int32(x))
			if e != nil && !errors.Is(e, pgx.ErrNoRows) {
				internal()
				return
			}
		}
		if q.DeleteBillLines(ctx, id) != nil {
			internal()
			return
		}
		subtotal := decimal.Zero
		for i, v := range lines {
			l := v.(map[string]any)
			var item pgtype.UUID
			if x, ok := l["item_id"].(string); ok {
				item, _ = parsePathUUID(x)
			}
			var account pgtype.Int4
			if x := l["expense_account_id"]; x != nil {
				n, ok := billUpdateInt(x)
				if !ok {
					internal()
					return
				}
				account = pgtype.Int4{Int32: n, Valid: true}
			}
			pos := int32(i)
			if x := l["position"]; x != nil {
				n, ok := billUpdateInt(x)
				if !ok {
					internal()
					return
				}
				if n != 0 {
					pos = n
				}
			}
			qr, _ := json.Marshal(l["quantity"])
			pr, _ := json.Marshal(l["unit_price"])
			qty, _, _ := invoiceDecimal(qr, true)
			price, _, _ := invoiceDecimal(pr, true)
			amount := qty.Mul(price).RoundBank(4)
			subtotal = subtotal.Add(amount)
			lid, err := itemUUID()
			if err != nil {
				internal()
				return
			}
			if q.InsertBillLine(ctx, dbquery.InsertBillLineParams{LineItemID: lid, BillID: id, ItemID: item, ExpenseAccountID: account, Position: pos, Description: l["description"].(string), Quantity: invoiceNumeric(qty), UnitPrice: invoiceNumeric(price), Amount: invoiceNumeric(amount)}) != nil {
				internal()
				return
			}
		}
		total := subtotal
		if b.DiscountType.Valid && b.DiscountValue.Valid {
			d := decimal.NewFromBigInt(b.DiscountValue.Int, b.DiscountValue.Exp)
			if b.DiscountType.String == "PERCENT" {
				total = subtotal.Mul(decimal.NewFromInt(1).Sub(d.Shift(-2)))
			} else {
				total = subtotal.Sub(d)
			}
			if total.IsNegative() {
				total = decimal.Zero
			}
		}
		b.Subtotal = invoiceNumeric(subtotal)
		b.Amount = invoiceNumeric(total.RoundBank(4))
		if b.Status == "DRAFT" {
			b.BalanceDue = b.Amount
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

func billUpdateInt(v any) (int32, bool) {
	text := strings.ToLower(strings.TrimSpace(stringifyBill(v)))
	if text == "true" {
		text = "1"
	}
	if text == "false" {
		text = "0"
	}
	n, ok := boundedItemRat(text)
	if !ok || !n.IsInt() || !n.Num().IsInt64() {
		return 0, false
	}
	i := n.Num().Int64()
	if i < -2147483648 || i > 2147483647 {
		return 0, false
	}
	return int32(i), true
}
