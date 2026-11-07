package platform

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"

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

func (h scopedProfileRouter) billCreate(w http.ResponseWriter, r *http.Request) {
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
	vendor, _ := parsePathUUID(b.VendorID)
	_, e = scopedvendor.Lock(ctx, tx, h.scope, vendor)
	if errors.Is(e, pgx.ErrNoRows) {
		authError(w, 400, "referenced record not found")
		return
	}
	if e != nil {
		internal()
		return
	}
	var project pgtype.UUID
	if b.ProjectID != nil {
		project, _ = parsePathUUID(*b.ProjectID)
		_, e = scopedproject.LockProject(ctx, tx, h.scope, dbqueryv8.LockProjectParams{ProjectID: project})
		if errors.Is(e, pgx.ErrNoRows) {
			authError(w, 400, "referenced record not found")
			return
		}
		if e != nil {
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
	for _, id := range orderedItemIDs(ids) {
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
	for id := range accounts {
		ac = append(ac, int(id))
	}
	sort.Ints(ac)
	for _, id := range ac {
		_, e = scopedaccount.Lock(ctx, tx, h.scope, int32(id))
		if errors.Is(e, pgx.ErrNoRows) {
			authError(w, 400, "referenced record not found")
			return
		}
		if e != nil {
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
	row, e := scopedbills.InsertBill(ctx, tx, h.scope, dbqueryv8.InsertBillParams{BillID: id, VendorID: vendor, ProjectID: project, BillNumber: textValue(b.BillNumber), PoNumber: textValue(b.PoNumber), Currency: strings.ToUpper(b.Currency), Subtotal: invoiceNumeric(subtotal), DiscountType: textValue(b.DiscountType), DiscountValue: dv, Amount: invoiceNumeric(total), IssueDate: date(b.IssueDate), DueDate: date(b.DueDate), PaymentTerms: textValue(b.PaymentTerms), Notes: textValue(b.Notes)})
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
		e = scopedbills.InsertBillLine(ctx, tx, h.scope, dbqueryv8.InsertBillLineParams{LineItemID: lid, BillID: id, ItemID: item, ExpenseAccountID: account, Position: pos, Description: l.Description, Quantity: invoiceNumeric(quantities[i]), UnitPrice: invoiceNumeric(prices[i]), Amount: invoiceNumeric(amounts[i])})
		if e != nil {
			internal()
			return
		}
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
	out, e := billJSON(legacyBill(row), legacyLines)
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
