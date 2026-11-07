package platform

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedaccount"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedbills"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopeditem"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedproject"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopeduser"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedvendor"
)

func (h scopedProfileRouter) billUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Content-Type") == "" {
		r = r.Clone(r.Context())
		r.Header.Set("Content-Type", "application/json")
	}
	body, ok := readItemBody(w, r)
	if !ok {
		return
	}
	current, ok := h.admin(w, r)
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
	tx, e := h.pool.Begin(ctx)
	if e != nil {
		internal()
		return
	}
	defer tx.Rollback(ctx)
	user, e := scopeduser.LockSessionUserByID(ctx, tx, h.scope, dbqueryv8.LockSessionUserByIDParams{UserID: current.UserID})
	if errors.Is(e, pgx.ErrNoRows) || e == nil && user.Role != "admin" {
		authError(w, 403, "admin required")
		return
	}
	if e != nil {
		internal()
		return
	}
	b, e := scopedbills.LockBill(ctx, tx, h.scope, dbqueryv8.LockBillParams{BillID: id})
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
		_, e = scopedvendor.Lock(ctx, tx, h.scope, b.VendorID)
		if errors.Is(e, pgx.ErrNoRows) {
			authError(w, 400, "referenced record not found")
			return
		}
		if e != nil {
			internal()
			return
		}
	}
	if b.ProjectID.Valid {
		_, e = scopedproject.LockProject(ctx, tx, h.scope, dbqueryv8.LockProjectParams{ProjectID: b.ProjectID})
		if errors.Is(e, pgx.ErrNoRows) {
			authError(w, 400, "referenced record not found")
			return
		}
		if e != nil {
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
		for _, id := range orderedItemIDs(items) {
			_, e = scopeditem.LockItem(ctx, tx, h.scope, dbqueryv8.LockItemParams{ItemID: id})
			if errors.Is(e, pgx.ErrNoRows) {
				authError(w, 400, "referenced record not found")
				return
			}
			if e != nil {
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
			_, e = scopedaccount.Lock(ctx, tx, h.scope, int32(x))
			if errors.Is(e, pgx.ErrNoRows) {
				authError(w, 400, "referenced record not found")
				return
			}
			if e != nil {
				internal()
				return
			}
		}
		if scopedbills.DeleteBillLines(ctx, tx, h.scope, dbqueryv8.DeleteBillLinesParams{BillID: id}) != nil {
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
			if scopedbills.InsertBillLine(ctx, tx, h.scope, dbqueryv8.InsertBillLineParams{LineItemID: lid, BillID: id, ItemID: item, ExpenseAccountID: account, Position: pos, Description: l["description"].(string), Quantity: invoiceNumeric(qty), UnitPrice: invoiceNumeric(price), Amount: invoiceNumeric(amount)}) != nil {
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
	b, e = scopedbills.UpdateBill(ctx, tx, h.scope, dbqueryv8.UpdateBillParams{BillID: id, VendorID: b.VendorID, ProjectID: b.ProjectID, BillNumber: b.BillNumber, PoNumber: b.PoNumber, Currency: b.Currency, Subtotal: b.Subtotal, DiscountType: b.DiscountType, DiscountValue: b.DiscountValue, Amount: b.Amount, BalanceDue: b.BalanceDue, Status: b.Status, IssueDate: b.IssueDate, DueDate: b.DueDate, PaymentTerms: b.PaymentTerms, Notes: b.Notes})
	if e != nil {
		internal()
		return
	}
	lines, e := scopedbills.BillLines(ctx, tx, h.scope, dbqueryv8.BillLinesParams{BillIds: []pgtype.UUID{id}})
	if e != nil {
		internal()
		return
	}
	legacyLines := make([]dbquery.BillLineItem, 0, len(lines))
	for _, line := range lines {
		legacyLines = append(legacyLines, legacyBillLine(line))
	}
	out, e := billJSON(legacyBill(b), legacyLines)
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
