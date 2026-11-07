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
)

type milestoneSchema struct {
	CustomerID   string  `json:"customer_id" format:"uuid"`
	ProjectID    *string `json:"project_id,omitempty" format:"uuid"`
	Currency     string  `json:"currency" minLength:"3" maxLength:"3"`
	Amount       any     `json:"amount"`
	MilestoneRef string  `json:"milestone_ref"`
	IssueDate    *string `json:"issue_date,omitempty" format:"date"`
	DueInDays    int     `json:"due_in_days,omitempty" default:"14"`
}

func registerMilestoneCreate(api huma.API, s sessionHandler) {
	response := func(desc string, body any) *huma.Response {
		return &huma.Response{Description: desc, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(body), true, "")}}}
	}
	responses := map[string]*huma.Response{"401": response("Not authenticated", authErrorBody{}), "403": response("Admin required", authErrorBody{}), "413": response("Body too large", authErrorBody{}), "422": response("Validation error", vendorUpdateValidationErrorBody{}), "500": response("Internal server error", authErrorBody{})}
	huma.Register(api, huma.Operation{Method: "POST", Path: "/api/v1/invoices/milestone", OperationID: "create-milestone-invoice", DefaultStatus: 201, SkipValidateBody: true, Responses: responses, Security: []map[string][]string{{"sessionCookie": {}}}, Middlewares: huma.Middlewares{sessionMiddleware(s.invoiceWriter().milestoneCreate)}}, func(context.Context, *struct{ Body milestoneSchema }) (*struct{ Body invoiceOutputSchema }, error) {
		panic("middleware")
	})
	schema := api.OpenAPI().Components.Schemas.Map()["MilestoneSchema"]
	schema.AdditionalProperties = true
	zero := float64(0)
	*schema.Properties["amount"] = huma.Schema{AnyOf: []*huma.Schema{{Type: "number", ExclusiveMinimum: &zero}, {Type: "string"}}}
}
func (s invoiceWriter) milestoneCreate(w http.ResponseWriter, r *http.Request) {
	body, ok := readItemBody(w, r)
	if !ok {
		return
	}
	current, ok := s.admin(w, r)
	if !ok {
		return
	}
	invalid := func() {
		vendorUpdateValidationResponse(w, []validationError{{"value_error", []any{"body"}, "Invalid input", nil, nil}})
	}
	if !requestJSONMediaType(r) || len(body) == 0 {
		invalid()
		return
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var value any
	if e := dec.Decode(&value); e != nil {
		vendorUpdateValidationResponse(w, itemUpdateJSONError(body, e, false))
		return
	}
	var extra any
	if e := dec.Decode(&extra); e != io.EOF {
		vendorUpdateValidationResponse(w, itemUpdateJSONError(body, e, true))
		return
	}
	obj, ok := value.(map[string]any)
	if !ok {
		vendorUpdateValidationResponse(w, []validationError{{"model_attributes_type", []any{"body"}, "Input should be a valid dictionary or object to extract fields from", value, nil}})
		return
	}
	errs := []validationError{}
	add := func(typ, name, msg string, v, ctx any) {
		errs = append(errs, validationError{typ, []any{"body", name}, msg, v, ctx})
	}
	for _, name := range []string{"customer_id", "project_id"} {
		v, exists := obj[name]
		if !exists {
			if name == "customer_id" {
				add("missing", name, "Field required", obj, nil)
			}
			continue
		}
		if v == nil && name == "project_id" {
			continue
		}
		x, ok := v.(string)
		if !ok {
			add("uuid_type", name, "UUID input should be a string, bytes or UUID object", v, nil)
		} else if _, ok := parsePathUUID(x); !ok {
			d := uuidErrorDetail(x)
			add("uuid_parsing", name, "Input should be a valid UUID, "+d, v, map[string]string{"error": d})
		}
	}
	for _, name := range []string{"currency", "milestone_ref"} {
		v, exists := obj[name]
		if !exists {
			add("missing", name, "Field required", obj, nil)
			continue
		}
		x, ok := v.(string)
		if !ok {
			add("string_type", name, "Input should be a valid string", v, nil)
			continue
		}
		if name == "currency" {
			n := utf8.RuneCountInString(x)
			if n < 3 {
				add("string_too_short", name, "String should have at least 3 characters", v, map[string]int{"min_length": 3})
			} else if n > 3 {
				add("string_too_long", name, "String should have at most 3 characters", v, map[string]int{"max_length": 3})
			}
		}
	}
	var amount decimal.Decimal
	if v, exists := obj["amount"]; !exists {
		add("missing", "amount", "Field required", obj, nil)
	} else {
		raw, _ := json.Marshal(v)
		d, valid, _ := invoiceDecimal(raw, true)
		if !valid && strings.HasPrefix(fmt.Sprint(v), "-") {
			rawPositive, _ := json.Marshal(strings.TrimPrefix(fmt.Sprint(v), "-"))
			positive, good, _ := invoiceDecimal(rawPositive, true)
			if good {
				d = positive.Neg()
				valid = true
			}
		}
		if !valid {
			e := itemMoneyError("amount", v)
			e.Loc = []any{"body", "amount"}
			errs = append(errs, e)
		} else if d.LessThanOrEqual(decimal.Zero) {
			add("greater_than", "amount", "Input should be greater than 0", v, map[string]int{"gt": 0})
		} else {
			amount = d
		}
	}
	today := time.Now()
	issue := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, today.Location())
	if v, exists := obj["issue_date"]; exists && v != nil {
		switch v.(type) {
		case string, json.Number:
		default:
			add("date_type", "issue_date", "Input should be a valid date", v, nil)
			v = nil
		}
		if v != nil {
			d, e := invoiceDate(v)
			if e != nil {
				e.Loc = []any{"body", "issue_date"}
				e.Input = v
				errs = append(errs, *e)
			} else {
				issue = d
			}
		}
	}
	days := int64(14)
	if v, exists := obj["due_in_days"]; exists {
		switch x := v.(type) {
		case bool:
			if x {
				days = 1
			} else {
				days = 0
			}
		case json.Number:
			rat, ok := boundedItemRat(string(x))
			if ok && rat.IsInt() && rat.Num().IsInt64() {
				days = rat.Num().Int64()
			} else {
				add("int_from_float", "due_in_days", "Input should be a valid integer, got a number with a fractional part", v, nil)
			}
		case string:
			n, e := strconv.ParseInt(x, 10, 64)
			if e != nil {
				add("int_parsing", "due_in_days", "Input should be a valid integer, unable to parse string as an integer", v, nil)
			} else {
				days = n
			}
		default:
			add("int_type", "due_in_days", "Input should be a valid integer", v, nil)
		}
	}
	if len(errs) > 0 {
		order := map[string]int{"customer_id": 0, "project_id": 1, "currency": 2, "amount": 3, "milestone_ref": 4, "issue_date": 5, "due_in_days": 6}
		sort.SliceStable(errs, func(i, j int) bool { return order[errs[i].Loc[1].(string)] < order[errs[j].Loc[1].(string)] })
		vendorUpdateValidationResponse(w, errs)
		return
	}
	if days > 3652058 || days < -3652058 {
		authError(w, 500, "internal server error")
		return
	}
	due := issue.AddDate(0, 0, int(days))
	if due.Year() < 1 || due.Year() > 9999 {
		authError(w, 500, "internal server error")
		return
	}
	customer, _ := parsePathUUID(obj["customer_id"].(string))
	var project pgtype.UUID
	if v, ok := obj["project_id"].(string); ok {
		project, _ = parsePathUUID(v)
	}
	ctx := r.Context()
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		authError(w, 500, "internal server error")
		return
	}
	defer tx.Rollback(ctx)
	q := s.queries(tx)
	user, e := q.lockAdminRole(ctx, current)
	if errors.Is(e, pgx.ErrNoRows) || e == nil && user != "admin" {
		authError(w, 403, "admin required")
		return
	}
	if e != nil {
		authError(w, 500, "internal server error")
		return
	}
	internalError := func() { authError(w, 500, "internal server error") }
	typ := "NONE"
	gross := false
	rate := decimal.Zero
	hasRate := false
	defaults, e := q.LockMilestoneCustomer(ctx, customer)
	if !s.acceptReference(w, internalError, e) {
		return
	}
	if e == nil {
		typ = defaults.DefaultWhtType
		gross = defaults.DefaultWhtGrossUp
		if defaults.DefaultWhtRate.Valid {
			v, e := defaults.DefaultWhtRate.Value()
			if e != nil {
				authError(w, 500, "internal server error")
				return
			}
			rate, e = decimal.NewFromString(v.(string))
			if e != nil {
				authError(w, 500, "internal server error")
				return
			}
			hasRate = true
		}
	}
	if project.Valid {
		_, e = q.LockMilestoneProject(ctx, project)
		if !s.acceptReference(w, internalError, e) {
			return
		}
	}
	if typ == "NONE" {
		gross = false
		hasRate = false
	} else {
		if !hasRate {
			if typ == "PPH21" {
				rate = decimal.RequireFromString("2.5")
			} else if typ == "PPH23" {
				rate = decimal.NewFromInt(2)
			} else {
				authError(w, 500, "internal server error")
				return
			}
			hasRate = true
		}
		if rate.LessThanOrEqual(decimal.Zero) || rate.GreaterThanOrEqual(decimal.NewFromInt(100)) {
			authError(w, 500, "internal server error")
			return
		}
	}
	total, wht := invoiceWithholdingTotals(amount, obj["currency"].(string), typ, rate, gross)
	var rateArg pgtype.Numeric
	if hasRate {
		rateArg = invoiceNumeric(rate)
	}
	id, e := itemUUID()
	if e != nil {
		authError(w, 500, "internal server error")
		return
	}
	ref, _ := json.Marshal(map[string]string{"milestone_ref": obj["milestone_ref"].(string)})
	e = q.InsertMilestoneInvoice(ctx, dbquery.InsertMilestoneInvoiceParams{InvoiceID: id, CustomerID: customer, ProjectID: project, Currency: obj["currency"].(string), WhtType: typ, WhtRate: rateArg, WhtGrossUp: gross, WhtBase: invoiceNumeric(amount), WhtAmount: invoiceNumeric(wht), Amount: invoiceNumeric(total), BillingCycleRef: ref, IssueDate: pgtype.Date{Time: issue, Valid: true}, DueDate: pgtype.Date{Time: due, Valid: true}})
	if e != nil {
		authError(w, 500, "internal server error")
		return
	}
	row, e := q.GetInvoice(ctx, id)
	if e != nil {
		authError(w, 500, "internal server error")
		return
	}
	lines, e := q.InvoiceLinesByIDs(ctx, []pgtype.UUID{id})
	if e != nil {
		authError(w, 500, "internal server error")
		return
	}
	out, e := invoiceJSON(row, lines)
	if e != nil {
		authError(w, 500, "internal server error")
		return
	}
	encoded, e := json.Marshal(out)
	if e != nil {
		authError(w, 500, "internal server error")
		return
	}
	if e = tx.Commit(ctx); e != nil {
		authError(w, 500, "internal server error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(201)
	fmt.Fprintln(w, string(encoded))
}
