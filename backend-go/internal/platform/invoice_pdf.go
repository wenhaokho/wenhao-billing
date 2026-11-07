package platform

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"reflect"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	maroto "github.com/johnfercher/maroto/v2"
	"github.com/johnfercher/maroto/v2/pkg/components/page"
	"github.com/johnfercher/maroto/v2/pkg/components/row"
	"github.com/johnfercher/maroto/v2/pkg/components/text"
	"github.com/johnfercher/maroto/v2/pkg/config"
	"github.com/johnfercher/maroto/v2/pkg/consts/align"
	"github.com/johnfercher/maroto/v2/pkg/consts/fontstyle"
	"github.com/johnfercher/maroto/v2/pkg/core"
	"github.com/johnfercher/maroto/v2/pkg/fontrepository"
	"github.com/johnfercher/maroto/v2/pkg/props"
	"github.com/shopspring/decimal"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
)

func registerInvoicePDF(api huma.API, s sessionHandler) {
	responses := map[string]*huma.Response{}
	for _, code := range []string{"401", "403", "404", "422", "500"} {
		responses[code] = &huma.Response{Description: "Request failed", Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(authErrorBody{}), true, "")}}}
	}
	responses["422"].Content["application/json"].Schema = api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(validationErrorBody{}), true, "")
	responses["200"] = &huma.Response{Description: "Invoice PDF", Content: map[string]*huma.MediaType{"application/pdf": {Schema: &huma.Schema{Type: "string", Format: "binary"}}}}
	huma.Register(api, huma.Operation{Method: "GET", Path: "/api/v1/invoices/{invoice_id}/pdf", OperationID: "invoice-pdf", Security: []map[string][]string{{"sessionCookie": {}}}, Middlewares: huma.Middlewares{sessionMiddleware(s.invoicePDF)}, Responses: responses}, func(context.Context, *invoiceGetInput) (*struct{}, error) { panic("middleware") })
}

func (s sessionHandler) invoicePDF(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	raw := chi.URLParam(r, "invoice_id")
	id, ok := parsePathUUID(raw)
	if !ok {
		d := uuidErrorDetail(raw)
		validationResponse(w, []validationError{{"uuid_parsing", []any{"path", "invoice_id"}, "Input should be a valid UUID, " + d, raw, map[string]string{"error": d}}})
		return
	}
	tx, e := s.db.BeginTx(r.Context(), pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		authError(w, 500, "internal server error")
		return
	}
	defer tx.Rollback(r.Context())
	q := dbquery.New(tx)
	inv, e := q.GetInvoice(r.Context(), id)
	if errors.Is(e, pgx.ErrNoRows) {
		authError(w, 404, "invoice not found")
		return
	}
	if e != nil {
		authError(w, 500, "internal server error")
		return
	}
	customer, e := q.GetCustomer(r.Context(), inv.CustomerID)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		authError(w, 500, "internal server error")
		return
	}
	business, e := q.GetBusinessProfile(r.Context())
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		authError(w, 500, "internal server error")
		return
	}
	lines, e := q.InvoiceLinesByIDs(r.Context(), []pgtype.UUID{id})
	if e != nil {
		authError(w, 500, "internal server error")
		return
	}
	accounts, e := q.ListBusinessPaymentAccounts(r.Context())
	if e != nil {
		authError(w, 500, "internal server error")
		return
	}
	if e = tx.Commit(r.Context()); e != nil {
		authError(w, 500, "internal server error")
		return
	}
	b, e := renderInvoicePDF(r.Context(), inv, customer, business, lines, accounts)
	if e != nil {
		authError(w, 500, "internal server error")
		return
	}
	filename := inv.InvoiceNumber.String
	if filename == "" {
		filename = fmt.Sprintf("%x", inv.InvoiceID.Bytes[:4])
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": "invoice-" + filename + ".pdf"}))
	w.Write(b)
}

func renderInvoicePDF(ctx context.Context, q dbquery.Invoice, c dbquery.Customer, b dbquery.BusinessProfile, lines []dbquery.InvoiceLineItem, accounts []dbquery.PaymentAccount) ([]byte, error) {
	if e := loadQuotationFont(); e != nil {
		return nil, e
	}
	fonts, e := fontrepository.New().AddUTF8FontFromBytes("Noto", fontstyle.Normal, quotationFont).Load()
	if e != nil {
		return nil, e
	}
	m := maroto.New(config.NewBuilder().WithCustomFonts(fonts).WithDefaultFont(&props.Font{Family: "Noto", Size: 10}).WithLeftMargin(15).WithRightMargin(15).WithTopMargin(15).WithBottomMargin(15).Build())
	normal := props.Text{Size: 10, Bottom: 2}
	small := props.Text{Size: 9, Bottom: 2}
	title := props.Text{Size: 18, Bottom: 4}
	right := props.Text{Size: 9, Align: align.Right, Bottom: 2}
	ref := quotationText(q.InvoiceNumber)
	if ref == "" {
		ref = "—"
	}
	logo, logoType := quotationLogo(ctx, quotationText(b.LogoUrl))
	brand := func() []core.Row {
		heading := strings.ToUpper(quotationText(b.InvoiceTitle))
		if heading == "" {
			heading = "INVOICE"
		}
		return documentMasthead(logo, logoType, heading, ref, title)
	}
	tableHeader := func() core.Row {
		return row.New(6).Add(text.NewCol(1, "#", small), text.NewCol(5, "Description", small), text.NewCol(2, "Qty", right), text.NewCol(2, "Rate", right), text.NewCol(2, "Amount "+q.Currency, right))
	}
	if e = m.RegisterFooter(text.NewAutoRow(strings.Join([]string{quotationText(b.Name), quotationText(b.ContactEmail), quotationText(b.ContactPhone)}, "  "), small)); e != nil {
		return nil, e
	}
	m.AddRows(brand()...)
	ensure := func(height float64, table bool) {
		if !m.FitlnCurrentPage(height) {
			m.AddPages(page.New())
			m.AddRows(brand()...)
			if table {
				m.AddRows(tableHeader())
			}
		}
	}
	add := func(s string) {
		if s != "" {
			for _, chunk := range quotationWrap(s, 180, 10) {
				ensure(7, false)
				m.AddRows(row.New(7).Add(text.NewCol(12, chunk, normal)))
			}
		}
	}
	add("Invoice No.  " + ref + "    Issue Date  " + quotationDate(q.IssueDate) + "    Due Date  " + quotationDate(q.DueDate))
	statusParts := strings.Split(strings.ToLower(strings.ReplaceAll(q.Status, "_", " ")), " ")
	for i, v := range statusParts {
		if len(v) > 0 {
			statusParts[i] = strings.ToUpper(v[:1]) + v[1:]
		}
	}
	add("Status: " + strings.Join(statusParts, " "))
	add(quotationText(b.InvoiceSummary))
	if v := quotationText(q.PoSoNumber); v != "" {
		add("PO / SO  " + v)
	}
	name := quotationText(b.Name)
	if name == "" {
		name = "—"
	}
	add("Bill From: " + name)
	add(quotationText(b.Address))
	add(strings.TrimSpace(strings.Join([]string{quotationText(b.ContactEmail), quotationText(b.ContactPhone)}, "  ")))
	customer := c.Name
	if customer == "" {
		customer = "—"
	}
	add("Bill To: " + customer)
	addr := []string{quotationText(c.BillingAddress1), quotationText(c.BillingAddress2), strings.Trim(strings.Join([]string{quotationText(c.BillingState), quotationText(c.BillingPostalCode)}, ", "), ", "), strings.Trim(strings.Join([]string{quotationText(c.BillingCity), quotationText(c.BillingCountry)}, ", "), ", ")}
	has := false
	for _, v := range addr {
		if v != "" {
			has = true
			add(v)
		}
	}
	if !has {
		add(quotationText(c.BillingAddress))
	}
	add(strings.TrimSpace(strings.Join([]string{quotationText(c.ContactEmail), quotationText(c.ContactPhone)}, "  ")))

	ensure(12, false)
	m.AddRows(tableHeader())
	for i, l := range lines {
		qty, _ := recurringDecimal(l.Quantity)
		cols := [][]string{quotationWrap(fmt.Sprintf("%02d", i+1), 15, 9), quotationWrap(l.Description, 75, 9), []string{qty.String()}, []string{quotationNumber(l.UnitPrice, q.Currency)}, []string{quotationNumber(l.Amount, q.Currency)}}
		count := 0
		for _, col := range cols {
			if len(col) > count {
				count = len(col)
			}
		}
		for n := 0; n < count; n++ {
			part := func(c int) string {
				if n < len(cols[c]) {
					return cols[c][n]
				}
				return ""
			}
			ensure(6, true)
			m.AddRows(row.New(6).Add(text.NewCol(1, part(0), small), text.NewCol(5, part(1), small), text.NewCol(2, part(2), quotationFitNumber(part(2), 30, right)), text.NewCol(2, part(3), quotationFitNumber(part(3), 30, right)), text.NewCol(2, part(4), quotationFitNumber(part(4), 30, right))))
		}
	}

	add("Subtotal    " + quotationNumber(q.Subtotal, q.Currency))
	if q.DiscountType.Valid && q.DiscountValue.Valid {
		v, _ := recurringDecimal(q.DiscountValue)
		if !v.IsZero() {
			label := "Discount"
			if q.DiscountType.String == "PERCENT" {
				label += " (" + v.String() + "%)"
				sub, _ := recurringDecimal(q.Subtotal)
				v = sub.Mul(v).Div(decimal.NewFromInt(100))
			}
			add(label + "    -" + quotationNumber(invoiceNumeric(v.RoundBank(2)), q.Currency))
		}
	}
	amount, _ := recurringDecimal(q.Amount)
	balance, _ := recurringDecimal(q.BalanceDue)
	booked, _ := recurringDecimal(q.WhtBooked)
	withholding, _ := recurringDecimal(q.WhtAmount)
	paid := amount.Sub(balance).Sub(booked)
	wht := q.WhtType != "NONE" && q.WhtType != ""
	if wht {
		label := map[string]string{"PPH21": "PPh 21", "PPH23": "PPh 23"}[q.WhtType]
		rate, _ := recurringDecimal(q.WhtRate)
		if q.WhtGrossUp {
			base, _ := recurringDecimal(q.WhtBase)
			add(label + " (" + rate.String() + "%)    " + quotationNumber(invoiceNumeric(amount.Sub(base)), q.Currency))
		}
		add("Total    " + quotationNumber(q.Amount, q.Currency))
		add(label + " withholding (" + rate.String() + "%)    −" + quotationNumber(q.WhtAmount, q.Currency))
	} else if paid.GreaterThan(decimal.Zero) {
		add("Total    " + quotationNumber(q.Amount, q.Currency))
	}
	if paid.GreaterThan(decimal.Zero) {
		add("Paid    −" + quotationNumber(invoiceNumeric(paid.RoundBank(2)), q.Currency))
	}
	if wht {
		cash := balance.Sub(withholding.Sub(booked))
		if cash.IsNegative() {
			cash = decimal.Zero
		}
		add("Amount to Transfer    " + quotationNumber(invoiceNumeric(cash), q.Currency) + " " + q.Currency)
	} else {
		add("Total Due    " + quotationNumber(q.BalanceDue, q.Currency) + " " + q.Currency)
	}
	instructions := ""
	for _, a := range accounts {
		if a.Currency == q.Currency {
			instructions = a.Instructions
			break
		}
	}
	matched := false
	for _, a := range accounts {
		if a.Currency == q.Currency {
			matched = true
			break
		}
	}
	if !matched {
		for _, a := range accounts {
			if a.IsDefault {
				instructions = a.Instructions
				break
			}
		}
	}
	if instructions != "" {
		add("Payment Instructions")
		add(instructions)
	}
	if wht && strings.TrimSpace(quotationText(b.Npwp)) != "" {
		npwp := "NPWP " + strings.TrimSpace(quotationText(b.Npwp))
		if name := strings.TrimSpace(quotationText(b.NpwpName)); name != "" {
			npwp += " a.n. " + name
		}
		add(npwp)
	}
	notes := quotationText(q.Notes)
	if notes == "" {
		notes = quotationText(b.DefaultNotes)
	}
	if notes != "" {
		add("Notes")
		add(notes)
	}

	// The default HTML invoice omits payment terms (including Net N); the
	// legacy ReportLab renderer displays them. Follow the default here.
	if v := quotationText(q.Footer); v != "" {
		add(v)
	}
	doc, e := m.Generate()
	if e != nil {
		return nil, e
	}
	return doc.GetBytes(), nil
}
