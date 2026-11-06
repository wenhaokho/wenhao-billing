package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
	"github.com/wenhaokho/wenhao-billing/backend/internal/recurringschedule"
)

type recurringTriggerInput struct {
	ID   string `path:"template_id" format:"uuid"`
	Body struct {
		CycleKey string `json:"cycle_key"`
	}
}

func registerRecurringTrigger(api huma.API, s sessionHandler) {
	responses := map[string]*huma.Response{}
	for _, code := range []string{"400", "401", "403", "409", "413", "422", "500"} {
		responses[code] = &huma.Response{Description: "Request failed", Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(authErrorBody{}), true, "")}}}
	}
	huma.Register(api, huma.Operation{Method: "POST", Path: "/api/v1/invoices/recurring/{template_id}/trigger", OperationID: "trigger-recurring-cycle", SkipValidateBody: true, Responses: responses, Security: []map[string][]string{{"sessionCookie": {}}}, Middlewares: huma.Middlewares{sessionMiddleware(s.recurringTrigger)}}, func(context.Context, *recurringTriggerInput) (*struct{ Body invoiceOutputSchema }, error) {
		panic("middleware")
	})
}
func recurringDecimal(n pgtype.Numeric) (decimal.Decimal, error) {
	if !n.Valid {
		return decimal.Zero, nil
	}
	v, e := n.Value()
	if e != nil {
		return decimal.Zero, e
	}
	return decimal.NewFromString(v.(string))
}

// scheduleErrorText mirrors the service's schedule validation messages.
func scheduleErrorText(data []byte, reason error) string {
	var m map[string]json.RawMessage
	if json.Unmarshal(data, &m) != nil || len(m) == 0 {
		return "schedule config is empty"
	}
	repr := func(v json.RawMessage) string {
		if len(v) == 0 || string(v) == "null" {
			return "None"
		}
		var str string
		if json.Unmarshal(v, &str) == nil {
			return pythonRepr(str)
		}
		switch string(v) {
		case "true":
			return "True"
		case "false":
			return "False"
		}
		return string(v)
	}
	switch reason.Error() {
	case "invalid frequency":
		return "invalid frequency: " + repr(m["frequency"])
	case "invalid interval":
		return "interval must be a positive int, got " + repr(m["interval"])
	case "invalid end mode":
		return "invalid end_mode: " + repr(m["end_mode"])
	case "invalid end count":
		return "end_mode=AFTER_N requires positive integer end_after_cycles"
	case "invalid date":
		field := "start_date"
		var start string
		if json.Unmarshal(m[field], &start) == nil {
			if _, err := recurringschedule.ParseDate(start); err == nil {
				field = "end_date"
			}
		}
		v := m[field]
		if field == "end_date" && (len(v) == 0 || string(v) == "null") {
			return "end_mode=ON_DATE requires end_date"
		}
		var str string
		if json.Unmarshal(v, &str) != nil {
			return "missing or invalid " + field
		}
		return "invalid ISO date for " + field + ": " + pythonRepr(str)
	}
	return reason.Error()
}
func (s sessionHandler) recurringTrigger(w http.ResponseWriter, r *http.Request) {
	body, ok := readBodyWithSyntax(w, r)
	if !ok {
		return
	}
	current, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	raw := chi.URLParam(r, "template_id")
	id, ok := parsePathUUID(raw)
	if !ok {
		validationResponse(w, []validationError{{"uuid_parsing", []any{"path", "template_id"}, "Input should be a valid UUID", nil, nil}})
		return
	}
	canonicalID, _ := id.Value()
	raw = canonicalID.(string)
	var data map[string]json.RawMessage
	if len(body) == 0 {
		validationResponse(w, []validationError{{"missing", []any{"body"}, "Field required", nil, nil}})
		return
	}
	if json.Unmarshal(body, &data) != nil || data == nil {
		validationResponse(w, []validationError{{"model_attributes_type", []any{"body"}, "Input should be a valid dictionary or object to extract fields from", nil, nil}})
		return
	}
	keyraw, exists := data["cycle_key"]
	if !exists {
		validationResponse(w, []validationError{{"missing", []any{"body", "cycle_key"}, "Field required", nil, nil}})
		return
	}
	var key string
	if json.Unmarshal(keyraw, &key) != nil || string(keyraw) == "null" {
		validationResponse(w, []validationError{{"string_type", []any{"body", "cycle_key"}, "Input should be a valid string", nil, nil}})
		return
	}
	recurringTriggerCore(w, r.Context(), s.db, id, raw, key, &current.UserID, nil, pgtype.UUID{}, nil)
}

// recurringTriggerCore serializes template actions and generation on the template row.
// scanTime selects the current cycle after the row lock, preventing stale pause/end decisions.
func recurringTriggerCore(w http.ResponseWriter, ctx context.Context, db interface {
	Begin(context.Context) (pgx.Tx, error)
}, id pgtype.UUID, raw, key string, userID *pgtype.UUID, scanTime *time.Time, batch pgtype.UUID, recipients *recurringRecipients) {
	tx, e := db.Begin(ctx)
	if e != nil {
		authError(w, 500, "internal server error")
		return
	}
	defer tx.Rollback(ctx)
	q := dbquery.New(tx)
	fail := func() { authError(w, 500, "internal server error") }
	if userID != nil {
		user, err := q.LockSessionUserByID(ctx, *userID)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && user.Role != "admin" {
			authError(w, 403, "admin required")
			return
		}
		if err != nil {
			fail()
			return
		}
	}
	template, e := q.LockInvoice(ctx, id)
	if errors.Is(e, pgx.ErrNoRows) {
		authError(w, 400, fmt.Sprintf("template invoice %s not found", raw))
		return
	}
	if e != nil {
		fail()
		return
	}
	if template.InvoiceType != "RECURRING" {
		authError(w, 400, fmt.Sprintf("invoice %s is not RECURRING", raw))
		return
	}
	if scanTime != nil && (!template.IsTemplate || recurringschedule.IsPaused(template.BillingCycleRef)) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	schedule, e := recurringschedule.Parse(template.BillingCycleRef)
	if e != nil {
		authError(w, 400, fmt.Sprintf("template %s has an invalid schedule: %s", raw, scheduleErrorText(template.BillingCycleRef, e)))
		return
	}
	if scanTime != nil {
		today := time.Date(scanTime.Year(), scanTime.Month(), scanTime.Day(), 0, 0, 0, 0, time.UTC)
		current, err := recurringschedule.CurrentCycle(schedule, today)
		if err != nil {
			fail()
			return
		}
		if current == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		key = recurringschedule.CycleKey(*current)
	}
	cycle, e := recurringschedule.ParseDate(key)
	if e != nil {
		authError(w, 400, fmt.Sprintf("cycle_key must be an ISO date (YYYY-MM-DD), got %s", pythonRepr(key)))
		return
	}
	valid, e := recurringschedule.IsCycleStart(schedule, cycle)
	if e != nil {
		fail()
		return
	}
	if !valid {
		authError(w, 400, fmt.Sprintf("cycle_key %s is not a valid cycle start for this schedule", pythonRepr(key)))
		return
	}
	created := false
	row, e := q.FindRecurringCycle(ctx, dbquery.FindRecurringCycleParams{TemplateID: id, CycleKey: key})
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		fail()
		return
	}

	if errors.Is(e, pgx.ErrNoRows) {
		candidates, err := q.ListRecurringCycleCandidates(ctx, id)
		if err != nil {
			fail()
			return
		}
		ambiguous := false
		for _, candidate := range candidates {
			var ref struct {
				CycleKey string `json:"cycle_key"`
			}
			if json.Unmarshal(candidate.BillingCycleRef, &ref) != nil {
				ambiguous = true
				continue
			}
			day, err := recurringschedule.ParseDate(ref.CycleKey)
			if err != nil {
				ambiguous = true
				continue
			}
			isStart, err := recurringschedule.IsCycleStart(schedule, day)
			if err != nil || !isStart {
				ambiguous = true
				continue
			}
			if day.Equal(cycle) {
				row = candidate
				e = nil
				break
			}
		}
		if errors.Is(e, pgx.ErrNoRows) && ambiguous {
			authError(w, 409, "Existing recurring invoice has an ambiguous cycle key; manual review required")
			return
		}

	}
	if errors.Is(e, pgx.ErrNoRows) {
		created = true
		lines, err := q.LockInvoiceLines(ctx, id)
		if err != nil {
			fail()
			return
		}
		subtotal := decimal.Zero
		hosting := false
		itemIDs := map[string]pgtype.UUID{}
		for _, ln := range lines {
			if ln.ItemID.Valid {
				value, err := ln.ItemID.Value()
				if err != nil {
					fail()
					return
				}
				itemIDs[value.(string)] = ln.ItemID
			}
		}
		ordered := make([]string, 0, len(itemIDs))
		for key := range itemIDs {
			ordered = append(ordered, key)
		}
		sort.Strings(ordered)
		for _, key := range ordered {
			h, err := q.ItemIsHosting(ctx, itemIDs[key])
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				fail()
				return
			}
			hosting = hosting || h
		}
		for _, ln := range lines {
			qty, err := recurringDecimal(ln.Quantity)
			if err != nil {
				fail()
				return
			}
			price, err := recurringDecimal(ln.UnitPrice)
			if err != nil {
				fail()
				return
			}
			subtotal = subtotal.Add(qty.Mul(price))
		}
		subtotal = subtotal.Round(4)
		discount, err := recurringDecimal(template.DiscountValue)
		if err != nil {
			fail()
			return
		}
		rate, err := recurringDecimal(template.WhtRate)
		if err != nil {
			fail()
			return
		}
		base := invoiceDiscountedBase(subtotal, template.DiscountType.String, discount, template.DiscountValue.Valid)
		amount, wht := base, decimal.Zero
		if template.WhtRate.Valid {
			amount, wht = invoiceWithholdingTotals(base, template.Currency, template.WhtType, rate, template.WhtGrossUp)
		}
		if err = q.LockInvoiceNumberAllocation(ctx); err != nil {
			fail()
			return
		}
		now := time.Now()
		if scanTime != nil {
			now = *scanTime
		}
		prefix := "WH" + strconv.Itoa(now.Year())
		numbers, err := q.InvoiceNumbersForPrefix(ctx, pgtype.Text{String: prefix + "%", Valid: true})
		if err != nil {
			fail()
			return
		}
		max := new(big.Int)
		for _, n := range numbers {
			suffix := strings.TrimPrefix(n.String, prefix)
			v, digit, invalid := documentNumberSuffix(suffix)
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
		number := prefix + suffix
		child, err := itemUUID()
		if err != nil {
			fail()
			return
		}
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		var terms *string
		if template.PaymentTerms.Valid {
			terms = &template.PaymentTerms.String
		}
		due := today.AddDate(0, 0, invoiceDays(terms))
		err = q.InsertDraftInvoice(ctx, dbquery.InsertDraftInvoiceParams{InvoiceID: child, CustomerID: template.CustomerID, ProjectID: template.ProjectID, InvoiceType: "RECURRING", InvoiceNumber: pgtype.Text{String: number, Valid: true}, PoSoNumber: template.PoSoNumber, Currency: template.Currency, Subtotal: invoiceNumeric(subtotal), DiscountType: template.DiscountType, DiscountValue: template.DiscountValue, WhtType: template.WhtType, WhtRate: template.WhtRate, WhtGrossUp: template.WhtGrossUp, WhtBase: invoiceNumeric(base), WhtAmount: invoiceNumeric(wht), Amount: invoiceNumeric(amount), IssueDate: pgtype.Date{Time: today, Valid: true}, DueDate: pgtype.Date{Time: due, Valid: true}, PaymentTerms: template.PaymentTerms, Notes: template.Notes, Footer: template.Footer, InternalNote: template.InternalNote})
		if err != nil {
			fail()
			return
		}
		var start, end pgtype.Date
		label := ""
		if hosting {
			month := cycle.AddDate(0, schedule.Interval, 0) // Clamp month-end as Python does.
			target := time.Date(cycle.Year(), cycle.Month()+time.Month(schedule.Interval)+1, 0, 0, 0, 0, 0, time.UTC)
			day := cycle.Day()
			if day > target.Day() {
				day = target.Day()
			}
			month = time.Date(target.Year(), target.Month(), day, 0, 0, 0, 0, time.UTC)
			endDate := month.AddDate(0, 0, -1)
			start = pgtype.Date{Time: cycle, Valid: true}
			end = pgtype.Date{Time: endDate, Valid: true}
			label = fmt.Sprintf("Coverage: %s to %s", cycle.Format("2006-01-02"), endDate.Format("2006-01-02"))
		}
		ref, _ := json.Marshal(map[string]string{"template_invoice_id": raw, "cycle_key": key})
		if err = q.SetRecurringChildFields(ctx, dbquery.SetRecurringChildFieldsParams{InvoiceID: child, BillingCycleRef: ref, CoverageStart: start, CoverageEnd: end}); err != nil {
			fail()
			return
		}
		for _, ln := range lines {
			lid, err := itemUUID()
			if err != nil {
				fail()
				return
			}
			desc := ln.Description
			if label != "" && !strings.Contains(desc, label) {
				desc += "\n" + label
			}
			err = q.InsertDraftInvoiceLine(ctx, dbquery.InsertDraftInvoiceLineParams{LineItemID: lid, InvoiceID: child, ItemID: ln.ItemID, Position: ln.Position, Description: desc, Quantity: ln.Quantity, UnitPrice: ln.UnitPrice, Amount: ln.Amount})
			if err != nil {
				fail()
				return
			}
		}
		row, e = q.GetInvoice(ctx, child)
		if e != nil {
			fail()
			return
		}
	}
	lines, e := q.InvoiceLinesByIDs(ctx, []pgtype.UUID{row.InvoiceID})
	if e != nil {
		fail()
		return
	}
	out, e := invoiceJSON(row, lines)
	if e != nil {
		fail()
		return
	}
	encoded, e := json.Marshal(out)
	if e != nil {
		fail()
		return
	}
	if created {
		if _, err := tx.Exec(ctx, `INSERT INTO recurring_draft_events(invoice_id, cycle_key, invoice_number, customer_name, amount, currency, scan_batch_id) SELECT i.invoice_id, $2, coalesce(i.invoice_number,''), coalesce(c.name,'(unknown customer)'), i.amount, i.currency, $3 FROM invoices i LEFT JOIN customers c ON c.customer_id=i.customer_id WHERE i.invoice_id=$1`, row.InvoiceID, key, batch); err != nil {
			fail()
			return
		}
		if recipients == nil {
			_, e = tx.Exec(ctx, `INSERT INTO recurring_draft_deliveries(invoice_id, user_id, email) SELECT $1, user_id, email FROM users`, row.InvoiceID)
		} else {
			_, e = tx.Exec(ctx, `INSERT INTO recurring_draft_deliveries(invoice_id, user_id, email) SELECT $1, id, email FROM unnest($2::uuid[], $3::text[]) AS r(id,email)`, row.InvoiceID, recipients.ids, recipients.emails)
		}
		if e != nil {
			fail()
			return
		}
	}
	if tx.Commit(ctx) != nil {
		fail()
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(append(encoded, '\n'))
}
