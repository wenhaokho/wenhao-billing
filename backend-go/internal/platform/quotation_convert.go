package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
	"github.com/wenhaokho/wenhao-billing/backend/internal/withholding"
)

type quotationConvertSchema struct {
	IssueDate *string `json:"issue_date,omitempty" format:"date"`
	DueInDays int     `json:"due_in_days,omitempty" default:"14"`
}

func registerQuotationConvert(api huma.API, s sessionHandler) {
	response := func(desc string, body any) *huma.Response {
		return &huma.Response{Description: desc, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(body), true, "")}}}
	}
	responses := map[string]*huma.Response{"400": response("Invalid quotation", authErrorBody{}), "401": response("Not authenticated", authErrorBody{}), "403": response("Admin required", authErrorBody{}), "413": response("Body too large", authErrorBody{}), "422": response("Validation error", validationErrorBody{}), "500": response("Internal server error", authErrorBody{})}
	huma.Register(api, huma.Operation{Method: "POST", Path: "/api/v1/quotations/{quotation_id}/convert-to-invoice", OperationID: "convert-quotation-to-invoice", DefaultStatus: 201, SkipValidateBody: true, Responses: responses, Security: []map[string][]string{{"sessionCookie": {}}}, Middlewares: huma.Middlewares{sessionMiddleware(s.quotationConvert)}}, func(context.Context, *struct {
		ID   string `path:"quotation_id" format:"uuid"`
		Body quotationConvertSchema
	}) (*struct{ Body invoiceOutputSchema }, error) {
		panic("middleware")
	})
	api.OpenAPI().Components.Schemas.Map()["QuotationConvertSchema"].AdditionalProperties = true
}

type quotationConverter struct {
	pool    transactionBeginner
	admin   func(http.ResponseWriter, *http.Request) (pgtype.UUID, bool)
	queries func(pgx.Tx) quotationConversionQueries
	scoped  bool
}

type quotationConversionQueries interface {
	lockAdminRole(context.Context, pgtype.UUID) (string, error)
	lockConversionQuote(context.Context, pgtype.UUID) (dbquery.Quotation, error)
	QuotationLines(context.Context, []pgtype.UUID) ([]dbquery.QuotationLineItem, error)
	LockMilestoneCustomer(context.Context, pgtype.UUID) (dbquery.LockMilestoneCustomerRow, error)
	lockProjectReference(context.Context, pgtype.UUID) error
	lockItemReference(context.Context, pgtype.UUID) error
	LockInvoiceNumberAllocation(context.Context) error
	InvoiceNumbersForPrefix(context.Context, pgtype.Text) ([]pgtype.Text, error)
	InsertDraftInvoice(context.Context, dbquery.InsertDraftInvoiceParams) error
	InsertDraftInvoiceLine(context.Context, dbquery.InsertDraftInvoiceLineParams) error
	setInvoiceSource(context.Context, pgtype.UUID, pgtype.UUID) error
	markQuoteConverted(context.Context, pgtype.UUID, pgtype.UUID) error
	GetInvoice(context.Context, pgtype.UUID) (dbquery.Invoice, error)
	InvoiceLinesByIDs(context.Context, []pgtype.UUID) ([]dbquery.InvoiceLineItem, error)
}

type legacyQuotationConversionQueries struct {
	legacyQuotationWriteQueries
}

func (q legacyQuotationConversionQueries) lockConversionQuote(ctx context.Context, id pgtype.UUID) (dbquery.Quotation, error) {
	if _, err := q.tx.Exec(ctx, `SELECT quotation_id FROM quotations WHERE quotation_id=$1 FOR NO KEY UPDATE`, id); err != nil {
		return dbquery.Quotation{}, err
	}
	return q.GetQuotation(ctx, id)
}
func (q legacyQuotationConversionQueries) lockProjectReference(ctx context.Context, id pgtype.UUID) error {
	_, err := q.tx.Exec(ctx, `SELECT project_id FROM projects WHERE project_id=$1 FOR KEY SHARE`, id)
	return err
}
func (q legacyQuotationConversionQueries) lockItemReference(ctx context.Context, id pgtype.UUID) error {
	_, err := q.tx.Exec(ctx, `SELECT item_id FROM items WHERE item_id=$1 FOR KEY SHARE`, id)
	return err
}
func (q legacyQuotationConversionQueries) setInvoiceSource(ctx context.Context, invoice, quote pgtype.UUID) error {
	_, err := q.tx.Exec(ctx, `UPDATE invoices SET source_quote_id=$2 WHERE invoice_id=$1`, invoice, quote)
	return err
}
func (q legacyQuotationConversionQueries) markQuoteConverted(ctx context.Context, quote, invoice pgtype.UUID) error {
	_, err := q.tx.Exec(ctx, `UPDATE quotations SET status='INVOICED',converted_invoice_id=$2 WHERE quotation_id=$1`, quote, invoice)
	return err
}

func (s sessionHandler) quotationConvert(w http.ResponseWriter, r *http.Request) {
	converter := quotationConverter{
		pool:  s.db,
		admin: s.quotationWriter().admin,
		queries: func(tx pgx.Tx) quotationConversionQueries {
			return legacyQuotationConversionQueries{legacyQuotationWriteQueries{Queries: dbquery.New(tx), tx: tx}}
		},
	}
	converter.convert(w, r)
}

func (s quotationConverter) convert(w http.ResponseWriter, r *http.Request) {
	body, ok := readItemBody(w, r)
	if !ok {
		return
	}
	if len(body) > 0 {
		r.Body = io.NopCloser(bytes.NewReader(body))
		body, ok = readBodyWithSyntax(w, r)
		if !ok {
			return
		}
	}
	current, ok := s.admin(w, r)
	if !ok {
		return
	}
	raw := chi.URLParam(r, "quotation_id")
	id, valid := parsePathUUID(raw)
	var errs []validationError
	if !valid {
		d := uuidErrorDetail(raw)
		errs = append(errs, validationError{"uuid_parsing", []any{"path", "quotation_id"}, "Input should be a valid UUID, " + d, raw, map[string]string{"error": d}})
	}
	if len(body) == 0 {
		errs = append(errs, validationError{"missing", []any{"body"}, "Field required", nil, nil})
		validationResponse(w, errs)
		return
	}
	if !requestJSONMediaType(r) {
		errs = append(errs, validationError{"model_attributes_type", []any{"body"}, "Input should be a valid dictionary or object to extract fields from", "[REDACTED]", nil})
		validationResponse(w, errs)
		return
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var v any
	if e := dec.Decode(&v); e != nil {
		validationResponse(w, append(errs, itemUpdateJSONError(body, e, true)...))
		return
	}
	var extra any
	if e := dec.Decode(&extra); e != io.EOF {
		validationResponse(w, append(errs, itemUpdateJSONError(body, e, true)...))
		return
	}
	obj, ok := v.(map[string]any)
	if !ok {
		errs = append(errs, validationError{"model_attributes_type", []any{"body"}, "Input should be a valid dictionary or object to extract fields from", v, nil})
		validationResponse(w, errs)
		return
	}
	add := func(t, n, m string, v any) { errs = append(errs, validationError{t, []any{"body", n}, m, v, nil}) }
	now := time.Now()
	issue := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	if v, exists := obj["issue_date"]; exists && v != nil {
		switch v.(type) {
		case string, json.Number:
			d, e := invoiceDate(v)
			if e != nil {
				e.Loc = []any{"body", "issue_date"}
				e.Input = v
				errs = append(errs, *e)
			} else {
				issue = d
			}
		default:
			add("date_type", "issue_date", "Input should be a valid date", v)
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
			text := string(x)
			if strings.ContainsAny(text, ".eE") {
				f, err := strconv.ParseFloat(text, 64)
				if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
					add("finite_number", "due_in_days", "Input should be a finite number", v)
				} else if f >= 9223372036854775808.0 || f <= -9223372036854775808.0 {
					add("int_parsing_size", "due_in_days", "Unable to parse input string as an integer, exceeded maximum size", v)
				} else if math.Trunc(f) != f {
					add("int_from_float", "due_in_days", "Input should be a valid integer, got a number with a fractional part", v)
				} else {
					days = int64(f)
				}
			} else if n, ok := new(big.Int).SetString(text, 10); ok {
				days = quotationDaysInt(n)
			} else {
				add("int_parsing", "due_in_days", "Input should be a valid integer, unable to parse string as an integer", v)
			}

		case string:
			text := strings.TrimSpace(x)
			if !quotationIntegerString.MatchString(text) {
				add("int_parsing", "due_in_days", "Input should be a valid integer, unable to parse string as an integer", v)
			} else if rat, ok := boundedItemRat(strings.ReplaceAll(text, "_", "")); ok && rat.IsInt() {
				days = quotationDaysInt(rat.Num())
			} else {
				add("int_parsing", "due_in_days", "Input should be a valid integer, unable to parse string as an integer", v)
			}

		default:
			add("int_type", "due_in_days", "Input should be a valid integer", v)
		}
	}
	if len(errs) > 0 {
		validationResponse(w, errs)
		return
	}
	fail := func() { authError(w, 500, "internal server error") }
	ctx := r.Context()
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		fail()
		return
	}
	defer tx.Rollback(ctx)
	q := s.queries(tx)
	role, e := q.lockAdminRole(ctx, current)
	if errors.Is(e, pgx.ErrNoRows) || e == nil && role != "admin" {
		authError(w, 403, "admin required")
		return
	}
	if e != nil {
		fail()
		return
	}
	// A no-key update lock serializes quote mutations while remaining compatible
	// with foreign-key checks in concurrent project deletion.
	quote, e := q.lockConversionQuote(ctx, id)
	if errors.Is(e, pgx.ErrNoRows) {
		detail := fmt.Sprintf("quotation %s not found", id.String())
		if s.scoped {
			detail = "quotation not found"
		}
		authError(w, 400, detail)
		return
	}
	if e != nil {
		fail()
		return
	}
	if quote.Status == "INVOICED" || quote.Status == "VOID" || quote.Status == "DECLINED" {
		authError(w, 400, fmt.Sprintf("cannot convert a %s quotation to invoice", quote.Status))
		return
	}
	lines, e := q.QuotationLines(ctx, []pgtype.UUID{id})
	if e != nil {
		fail()
		return
	}
	if len(lines) == 0 {
		authError(w, 400, "quotation has no line items to convert")
		return
	}
	// Stabilize withholding defaults and referenced rows without blocking foreign-key key-share locks held by other invoice writers.
	customer, e := q.LockMilestoneCustomer(ctx, quote.CustomerID)
	if e != nil {
		fail()
		return
	}
	if quote.ProjectID.Valid {
		if e = q.lockProjectReference(ctx, quote.ProjectID); e != nil {
			fail()
			return
		}
	}
	ids := map[[16]byte]pgtype.UUID{}
	for _, l := range lines {
		if l.ItemID.Valid {
			ids[l.ItemID.Bytes] = l.ItemID
		}
	}
	for _, item := range orderedItemIDs(ids) {
		if e = q.lockItemReference(ctx, item); e != nil {
			fail()
			return
		}
	}
	var rate *decimal.Decimal
	if customer.DefaultWhtRate.Valid {
		d, err := updateNumeric(customer.DefaultWhtRate)
		if err != nil {
			fail()
			return
		}
		rate = &d
	}
	kind, rate, gross, e := withholding.Normalize(withholding.Type(customer.DefaultWhtType), rate, customer.DefaultWhtGrossUp)
	if e != nil {
		authError(w, 400, e.Error())
		return
	}
	base, e := updateNumeric(quote.Amount)
	if e != nil {
		fail()
		return
	}
	wht := withholding.Compute(base, quote.Currency, kind, rate, gross)
	if quote.PaymentTerms.Valid && quote.PaymentTerms.String != "" {
		terms := quotationTermsASCII(quote.PaymentTerms.String)
		if match := invoiceNetTerms.FindStringSubmatch(terms); match != nil {
			n, ok := new(big.Int).SetString(match[1], 10)
			if !ok || !n.IsInt64() || n.Int64() > 3652058 {
				fail()
				return
			}
			days = n.Int64()
		} else {
			original := strings.TrimFunc(quote.PaymentTerms.String, quotationPythonSpace)
			days = int64(invoiceDays(&original))
		}
	}
	if days > 3652058 || days < -3652058 {
		fail()
		return
	}
	due := issue.AddDate(0, 0, int(days))
	if due.Year() < 1 || due.Year() > 9999 {
		fail()
		return
	}
	if e = q.LockInvoiceNumberAllocation(ctx); e != nil {
		fail()
		return
	}
	prefix := "WH" + strconv.Itoa(now.Year())
	numbers, e := q.InvoiceNumbersForPrefix(ctx, pgtype.Text{String: prefix + "%", Valid: true})
	if e != nil {
		fail()
		return
	}
	max := new(big.Int)
	for _, n := range numbers {
		v, digit, invalid := documentNumberSuffix(strings.TrimPrefix(n.String, prefix))
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
	invoiceID, e := itemUUID()
	if e != nil {
		fail()
		return
	}
	var rateArg pgtype.Numeric
	if rate != nil {
		rateArg = invoiceNumeric(*rate)
	}
	e = q.InsertDraftInvoice(ctx, dbquery.InsertDraftInvoiceParams{InvoiceID: invoiceID, CustomerID: quote.CustomerID, ProjectID: quote.ProjectID, InvoiceType: "MILESTONE", InvoiceNumber: pgtype.Text{String: prefix + suffix, Valid: true}, PoSoNumber: quote.PoSoNumber, Currency: quote.Currency, Subtotal: quote.Subtotal, DiscountType: quote.DiscountType, DiscountValue: quote.DiscountValue, WhtType: string(kind), WhtRate: rateArg, WhtGrossUp: gross, WhtBase: invoiceNumeric(wht.Base), WhtAmount: invoiceNumeric(wht.Withholding), Amount: invoiceNumeric(wht.Total), IssueDate: pgtype.Date{Time: issue, Valid: true}, DueDate: pgtype.Date{Time: due, Valid: true}, PaymentTerms: quote.PaymentTerms, Notes: quote.Notes, Footer: quote.Footer})
	if e != nil {
		fail()
		return
	}
	if e = q.setInvoiceSource(ctx, invoiceID, id); e != nil {
		fail()
		return
	}
	for _, l := range lines {
		lid, err := itemUUID()
		if err != nil {
			fail()
			return
		}
		e = q.InsertDraftInvoiceLine(ctx, dbquery.InsertDraftInvoiceLineParams{LineItemID: lid, InvoiceID: invoiceID, ItemID: l.ItemID, Position: l.Position, Description: l.Description, Quantity: l.Quantity, UnitPrice: l.UnitPrice, Amount: l.Amount})
		if e != nil {
			fail()
			return
		}
	}
	if e = q.markQuoteConverted(ctx, id, invoiceID); e != nil {
		fail()
		return
	}
	row, e := q.GetInvoice(ctx, invoiceID)
	if e != nil {
		fail()
		return
	}
	invoiceLines, e := q.InvoiceLinesByIDs(ctx, []pgtype.UUID{invoiceID})
	if e != nil {
		fail()
		return
	}
	out, e := invoiceJSON(row, invoiceLines)
	if e != nil {
		fail()
		return
	}
	encoded, e := json.Marshal(out)
	if e != nil {
		fail()
		return
	}
	if e = tx.Commit(ctx); e != nil {
		fail()
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(201)
	_, _ = w.Write(append(encoded, '\n'))
}

// Python's regular expressions accept Unicode decimal digits and whitespace.
func quotationTermsASCII(s string) string {
	var b strings.Builder
	for _, r := range s {
		if quotationPythonSpace(r) {
			b.WriteByte(' ')
			continue
		}
		mapped := false
		for _, table := range unicode.Digit.R16 {
			if uint32(r) >= uint32(table.Lo) && uint32(r) <= uint32(table.Hi) && (uint32(r)-uint32(table.Lo))%uint32(table.Stride) == 0 {
				b.WriteByte('0' + byte(((uint32(r)-uint32(table.Lo))/uint32(table.Stride))%10))
				mapped = true
				break
			}
		}
		if !mapped {
			for _, table := range unicode.Digit.R32 {
				if uint32(r) >= table.Lo && uint32(r) <= table.Hi && (uint32(r)-table.Lo)%table.Stride == 0 {
					b.WriteByte('0' + byte(((uint32(r)-table.Lo)/table.Stride)%10))
					mapped = true
					break
				}
			}
		}
		if !mapped {
			b.WriteRune(r)
		}
	}
	return b.String()
}

var quotationIntegerString = regexp.MustCompile(`^[+-]?[0-9](?:_?[0-9])*(?:\.[0-9]+)?$`)

func quotationDaysInt(n *big.Int) int64 {
	if n.IsInt64() {
		return n.Int64()
	}
	if n.Sign() < 0 {
		return -3652059
	}
	return 3652059
}

func quotationPythonSpace(r rune) bool { return unicode.IsSpace(r) || r >= '\u001c' && r <= '\u001f' }
