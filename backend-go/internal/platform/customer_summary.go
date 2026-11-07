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
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
)

type invoiceLineOutput struct {
	LineItemID  pgtype.UUID `json:"line_item_id"`
	ItemID      pgtype.UUID `json:"item_id"`
	Description string      `json:"description"`
	Quantity    string      `json:"quantity"`
	UnitPrice   string      `json:"unit_price"`
	Amount      string      `json:"amount"`
	Position    int32       `json:"position"`
}
type invoiceOutput struct {
	InvoiceID       pgtype.UUID         `json:"invoice_id"`
	CustomerID      pgtype.UUID         `json:"customer_id"`
	ProjectID       pgtype.UUID         `json:"project_id"`
	SourceQuoteID   pgtype.UUID         `json:"source_quote_id"`
	InvoiceType     string              `json:"invoice_type"`
	InvoiceNumber   pgtype.Text         `json:"invoice_number"`
	PoSoNumber      pgtype.Text         `json:"po_so_number"`
	Currency        string              `json:"currency"`
	Subtotal        *string             `json:"subtotal"`
	DiscountType    pgtype.Text         `json:"discount_type"`
	DiscountValue   *string             `json:"discount_value"`
	WhtType         string              `json:"wht_type"`
	WhtRate         *string             `json:"wht_rate"`
	WhtGrossUp      bool                `json:"wht_gross_up"`
	WhtBase         *string             `json:"wht_base"`
	WhtAmount       string              `json:"wht_amount"`
	WhtBooked       string              `json:"wht_booked"`
	Amount          string              `json:"amount"`
	BalanceDue      string              `json:"balance_due"`
	Status          string              `json:"status"`
	BillingCycleRef map[string]any      `json:"billing_cycle_ref"`
	IssueDate       pgtype.Date         `json:"issue_date"`
	DueDate         pgtype.Date         `json:"due_date"`
	CoverageStart   pgtype.Date         `json:"coverage_start"`
	CoverageEnd     pgtype.Date         `json:"coverage_end"`
	PaymentTerms    pgtype.Text         `json:"payment_terms"`
	Notes           pgtype.Text         `json:"notes"`
	Footer          pgtype.Text         `json:"footer"`
	InternalNote    pgtype.Text         `json:"internal_note"`
	IsTemplate      bool                `json:"is_template"`
	CreatedAt       pythonTimestamp     `json:"created_at"`
	LineItems       []invoiceLineOutput `json:"line_items" nullable:"false"`
	ExpectedCash    string              `json:"expected_cash"`
}
type currencyAmount struct {
	Currency string `json:"currency"`
	Amount   string `json:"amount"`
}
type billingSummaryOutput struct {
	Milestone            []invoiceOutput  `json:"milestone" nullable:"false"`
	Recurring            []invoiceOutput  `json:"recurring" nullable:"false"`
	Usage                []invoiceOutput  `json:"usage" nullable:"false"`
	UnpaidInvoices       []invoiceOutput  `json:"unpaid_invoices" nullable:"false"`
	TotalUnpaid          []currencyAmount `json:"total_unpaid" nullable:"false"`
	PaidLast12Months     []currencyAmount `json:"paid_last_12_months" nullable:"false"`
	LastInvoiceIssueDate pgtype.Date      `json:"last_invoice_issue_date"`
	LastInvoiceID        pgtype.UUID      `json:"last_invoice_id"`
}
type invoiceLineSchema struct {
	LineItemID  string  `json:"line_item_id" format:"uuid"`
	ItemID      *string `json:"item_id" format:"uuid"`
	Description string  `json:"description"`
	Quantity    string  `json:"quantity"`
	UnitPrice   string  `json:"unit_price"`
	Amount      string  `json:"amount"`
	Position    int32   `json:"position"`
}
type invoiceOutputSchema struct {
	InvoiceID       string              `json:"invoice_id" format:"uuid"`
	CustomerID      *string             `json:"customer_id" format:"uuid"`
	ProjectID       *string             `json:"project_id" format:"uuid"`
	SourceQuoteID   *string             `json:"source_quote_id" format:"uuid"`
	InvoiceType     string              `json:"invoice_type"`
	InvoiceNumber   *string             `json:"invoice_number"`
	PoSoNumber      *string             `json:"po_so_number"`
	Currency        string              `json:"currency"`
	Subtotal        *string             `json:"subtotal"`
	DiscountType    *string             `json:"discount_type"`
	DiscountValue   *string             `json:"discount_value"`
	WhtType         string              `json:"wht_type"`
	WhtRate         *string             `json:"wht_rate"`
	WhtGrossUp      bool                `json:"wht_gross_up"`
	WhtBase         *string             `json:"wht_base"`
	WhtAmount       string              `json:"wht_amount"`
	WhtBooked       string              `json:"wht_booked"`
	Amount          string              `json:"amount"`
	BalanceDue      string              `json:"balance_due"`
	Status          string              `json:"status"`
	BillingCycleRef map[string]any      `json:"billing_cycle_ref"`
	IssueDate       *string             `json:"issue_date" format:"date"`
	DueDate         *string             `json:"due_date" format:"date"`
	CoverageStart   *string             `json:"coverage_start" format:"date"`
	CoverageEnd     *string             `json:"coverage_end" format:"date"`
	PaymentTerms    *string             `json:"payment_terms"`
	Notes           *string             `json:"notes"`
	Footer          *string             `json:"footer"`
	InternalNote    *string             `json:"internal_note"`
	IsTemplate      bool                `json:"is_template"`
	CreatedAt       string              `json:"created_at" format:"date-time"`
	LineItems       []invoiceLineSchema `json:"line_items" nullable:"false"`
	ExpectedCash    string              `json:"expected_cash"`
}
type currencyAmountSchema struct {
	Currency string `json:"currency"`
	Amount   string `json:"amount"`
}
type billingSummarySchema struct {
	Milestone            []invoiceOutputSchema  `json:"milestone" nullable:"false"`
	Recurring            []invoiceOutputSchema  `json:"recurring" nullable:"false"`
	Usage                []invoiceOutputSchema  `json:"usage" nullable:"false"`
	UnpaidInvoices       []invoiceOutputSchema  `json:"unpaid_invoices" nullable:"false"`
	TotalUnpaid          []currencyAmountSchema `json:"total_unpaid" nullable:"false"`
	PaidLast12Months     []currencyAmountSchema `json:"paid_last_12_months" nullable:"false"`
	LastInvoiceIssueDate *string                `json:"last_invoice_issue_date" format:"date"`
	LastInvoiceID        *string                `json:"last_invoice_id" format:"uuid"`
}
type customerSummaryPath struct {
	ID string `path:"customer_id" format:"uuid"`
}

func registerCustomerSummary(api huma.API, s sessionHandler) {
	responses := map[string]*huma.Response{}
	for code, desc := range map[string]string{"401": "Not authenticated", "403": "Admin required", "404": "Customer not found", "422": "Invalid customer ID", "500": "Internal server error"} {
		typ := reflect.TypeOf(authErrorBody{})
		if code == "422" {
			typ = reflect.TypeOf(validationErrorBody{})
		}
		responses[code] = &huma.Response{Description: desc, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(typ, true, "")}}}
	}
	op := func(path, id string) huma.Operation {
		return huma.Operation{Method: "GET", Path: "/api/v1/customers/{customer_id}/" + path, OperationID: id, Security: []map[string][]string{{"sessionCookie": {}}}, Responses: responses, Middlewares: huma.Middlewares{sessionMiddleware(s.customerSummary)}}
	}
	huma.Register(api, op("invoices", "customer-invoices"), func(context.Context, *customerSummaryPath) (*struct {
		Body []invoiceOutputSchema `nullable:"false"`
	}, error) {
		panic("middleware")
	})
	huma.Register(api, op("billing-summary", "customer-billing-summary"), func(context.Context, *customerSummaryPath) (*struct{ Body billingSummarySchema }, error) {
		panic("middleware")
	})
}
func checkedNum(n pgtype.Numeric, required bool) (*string, error) {
	if !n.Valid {
		if required {
			return nil, errors.New("missing numeric")
		}
		return nil, nil
	}
	if n.NaN || n.InfinityModifier != pgtype.Finite || n.Int == nil {
		return nil, errors.New("invalid numeric")
	}
	v := decimal.NewFromBigInt(n.Int, n.Exp).StringFixed(4)
	return &v, nil
}
func invoiceJSON(i dbquery.Invoice, lines []dbquery.InvoiceLineItem) (invoiceOutput, error) {
	var o invoiceOutput
	var err error
	o.InvoiceID = i.InvoiceID
	o.CustomerID = i.CustomerID
	o.ProjectID = i.ProjectID
	o.SourceQuoteID = i.SourceQuoteID
	o.InvoiceType = i.InvoiceType
	o.InvoiceNumber = i.InvoiceNumber
	o.PoSoNumber = i.PoSoNumber
	o.Currency = i.Currency
	o.DiscountType = i.DiscountType
	o.WhtType = i.WhtType
	o.WhtGrossUp = i.WhtGrossUp
	o.Status = i.Status
	o.IssueDate = i.IssueDate
	o.DueDate = i.DueDate
	o.CoverageStart = i.CoverageStart
	o.CoverageEnd = i.CoverageEnd
	o.PaymentTerms = i.PaymentTerms
	o.Notes = i.Notes
	o.Footer = i.Footer
	o.InternalNote = i.InternalNote
	o.IsTemplate = i.IsTemplate
	o.CreatedAt = pythonTimestamp(i.CreatedAt)
	for _, d := range []pgtype.Date{i.IssueDate, i.DueDate, i.CoverageStart, i.CoverageEnd} {
		if d.Valid && (d.InfinityModifier != pgtype.Finite || d.Time.Year() < 1 || d.Time.Year() > 9999) {
			return o, errors.New("invalid date")
		}
	}
	if !i.CreatedAt.Valid || i.CreatedAt.InfinityModifier != pgtype.Finite || i.CreatedAt.Time.Year() < 1 || i.CreatedAt.Time.Year() > 9999 {
		return o, errors.New("invalid timestamp")
	}
	if len(i.BillingCycleRef) > 0 && string(i.BillingCycleRef) != "null" {
		decoder := json.NewDecoder(bytes.NewReader(i.BillingCycleRef))
		decoder.UseNumber()
		if err = decoder.Decode(&o.BillingCycleRef); err != nil {
			return o, err
		}
	}
	for _, pair := range []struct {
		n pgtype.Numeric
		p **string
	}{{i.Subtotal, &o.Subtotal}, {i.DiscountValue, &o.DiscountValue}, {i.WhtRate, &o.WhtRate}, {i.WhtBase, &o.WhtBase}} {
		if *pair.p, err = checkedNum(pair.n, false); err != nil {
			return o, err
		}
	}
	for _, pair := range []struct {
		n pgtype.Numeric
		p *string
	}{{i.WhtAmount, &o.WhtAmount}, {i.WhtBooked, &o.WhtBooked}, {i.Amount, &o.Amount}, {i.BalanceDue, &o.BalanceDue}} {
		var v *string
		if v, err = checkedNum(pair.n, true); err != nil {
			return o, err
		}
		*pair.p = *v
	}
	bal, _ := decimal.NewFromString(o.BalanceDue)
	wa, _ := decimal.NewFromString(o.WhtAmount)
	wb, _ := decimal.NewFromString(o.WhtBooked)
	cash := bal.Sub(wa.Sub(wb))
	if !cash.IsPositive() {
		o.ExpectedCash = "0"
	} else {
		o.ExpectedCash = cash.StringFixed(4)
	}
	o.LineItems = make([]invoiceLineOutput, 0, len(lines))
	for _, l := range lines {
		a, e := checkedNum(l.Quantity, true)
		if e != nil {
			return o, e
		}
		b, e := checkedNum(l.UnitPrice, true)
		if e != nil {
			return o, e
		}
		c, e := checkedNum(l.Amount, true)
		if e != nil {
			return o, e
		}
		o.LineItems = append(o.LineItems, invoiceLineOutput{l.LineItemID, l.ItemID, l.Description, *a, *b, *c, l.Position})
	}
	return o, nil
}
func (s sessionHandler) customerSummary(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	raw := chi.URLParam(r, "customer_id")
	id, ok := parsePathUUID(raw)
	if !ok {
		d := uuidErrorDetail(raw)
		validationResponse(w, []validationError{{"uuid_parsing", []any{"path", "customer_id"}, "Input should be a valid UUID, " + d, raw, map[string]string{"error": d}}})
		return
	}
	q := dbquery.New(s.db)
	ctx := r.Context()
	if _, err := q.GetCustomer(ctx, id); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			authError(w, 404, "customer not found")
		} else {
			authError(w, 500, "internal server error")
		}
		return
	}
	rows, err := q.CustomerInvoices(ctx, id)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	lines, err := q.CustomerInvoiceLines(ctx, id)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if strings.HasSuffix(r.URL.Path, "/invoices") {
		writeCustomerInvoices(w, rows, lines)
		return
	}
	openRows, err := q.CustomerOpenInvoices(ctx, id)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	payments, err := q.CustomerRecentPayments(ctx, dbquery.CustomerRecentPaymentsParams{CustomerID: id, PaymentDate: pgtype.Date{Time: time.Now().AddDate(0, 0, -365), Valid: true}})
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	writeCustomerSummary(w, rows, openRows, lines, payments)
}

func customerInvoiceLinesByID(lines []dbquery.InvoiceLineItem) map[pgtype.UUID][]dbquery.InvoiceLineItem {
	byID := map[pgtype.UUID][]dbquery.InvoiceLineItem{}
	for _, l := range lines {
		byID[l.InvoiceID] = append(byID[l.InvoiceID], l)
	}
	return byID
}

func writeCustomerInvoices(w http.ResponseWriter, rows []dbquery.Invoice, lines []dbquery.InvoiceLineItem) {
	byID := customerInvoiceLinesByID(lines)
	out := make([]invoiceOutput, 0, len(rows))
	for _, row := range rows {
		v, e := invoiceJSON(row, byID[row.InvoiceID])
		if e != nil {
			authError(w, 500, "internal server error")
			return
		}
		out = append(out, v)
	}
	w.Header().Set("Content-Type", "application/json")
	writeSafeJSON(w, out)
}

func writeCustomerSummary(w http.ResponseWriter, rows, openRows []dbquery.Invoice, lines []dbquery.InvoiceLineItem, payments []dbquery.CustomerRecentPaymentsRow) {
	byID := customerInvoiceLinesByID(lines)
	summary := billingSummaryOutput{Milestone: []invoiceOutput{}, Recurring: []invoiceOutput{}, Usage: []invoiceOutput{}, UnpaidInvoices: []invoiceOutput{}, TotalUnpaid: []currencyAmount{}, PaidLast12Months: []currencyAmount{}}
	if len(rows) > 0 {
		summary.LastInvoiceID = rows[0].InvoiceID
		summary.LastInvoiceIssueDate = rows[0].IssueDate
	}
	open := make([]invoiceOutput, 0, len(openRows))
	for _, row := range openRows {
		v, e := invoiceJSON(row, byID[row.InvoiceID])
		if e != nil {
			authError(w, 500, "internal server error")
			return
		}
		open = append(open, v)
	}
	totals := map[string]decimal.Decimal{}
	for _, v := range open {
		summary.UnpaidInvoices = append(summary.UnpaidInvoices, v)
		switch v.InvoiceType {
		case "MILESTONE":
			summary.Milestone = append(summary.Milestone, v)
		case "RECURRING":
			summary.Recurring = append(summary.Recurring, v)
		case "USAGE":
			summary.Usage = append(summary.Usage, v)
		}
		a, _ := decimal.NewFromString(v.BalanceDue)
		totals[v.Currency] = totals[v.Currency].Add(a)
	}
	keys := make([]string, 0, len(totals))
	for k := range totals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		summary.TotalUnpaid = append(summary.TotalUnpaid, currencyAmount{k, totals[k].StringFixed(4)})
	}
	for _, p := range payments {
		v, e := checkedNum(p.Amount, true)
		if e != nil {
			authError(w, 500, "internal server error")
			return
		}
		summary.PaidLast12Months = append(summary.PaidLast12Months, currencyAmount{p.Currency, *v})
	}
	w.Header().Set("Content-Type", "application/json")
	writeSafeJSON(w, summary)
}
