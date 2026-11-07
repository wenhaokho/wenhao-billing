package platform

import (
	"bytes"
	"compress/gzip"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	"image/png"
	"math"
	"mime"
	"sync"

	"io"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	maroto "github.com/johnfercher/maroto/v2"
	pdfimage "github.com/johnfercher/maroto/v2/pkg/components/image"
	"github.com/johnfercher/maroto/v2/pkg/components/page"
	"github.com/johnfercher/maroto/v2/pkg/components/row"
	"github.com/johnfercher/maroto/v2/pkg/components/text"
	"github.com/johnfercher/maroto/v2/pkg/config"
	"github.com/johnfercher/maroto/v2/pkg/consts/align"
	"github.com/johnfercher/maroto/v2/pkg/consts/extension"
	"github.com/johnfercher/maroto/v2/pkg/consts/fontstyle"
	"github.com/johnfercher/maroto/v2/pkg/core"
	"github.com/johnfercher/maroto/v2/pkg/fontrepository"
	"github.com/johnfercher/maroto/v2/pkg/props"
	"github.com/shopspring/decimal"
	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
	"golang.org/x/image/font"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

//go:embed pdfassets/noto-sans-sc.ttf.gz
var quotationCompressedFont []byte
var quotationFontOnce sync.Once
var quotationFont []byte
var quotationFontErr error
var quotationMetrics *sfnt.Font

func loadQuotationFont() error {
	quotationFontOnce.Do(func() {
		var r *gzip.Reader
		r, quotationFontErr = gzip.NewReader(bytes.NewReader(quotationCompressedFont))
		if quotationFontErr != nil {
			return
		}
		quotationFont, quotationFontErr = io.ReadAll(r)
		r.Close()
		if quotationFontErr == nil {
			quotationMetrics, quotationFontErr = sfnt.Parse(quotationFont)
		}
	})
	return quotationFontErr
}

//go:embed pdfassets/default-logo.png
var quotationDefaultLogo []byte

const quotationLogoLimit = 5 << 20

func quotationSVG(data []byte) (result []byte) {
	// Reject external references and XML directives before handing bytes to the
	// SVG parser. The renderer receives no network or filesystem access.
	defer func() {
		if recover() != nil {
			result = nil
		}
	}()
	decoder := xml.NewDecoder(bytes.NewReader(data))
	nodes := 0
	root := false
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil
		}
		switch t := token.(type) {
		case xml.Directive:
			return nil
		case xml.StartElement:
			// oksvg expands <use> recursively; even a tiny cyclic definition
			// can exhaust the stack. Reject it before parsing or rendering.
			if strings.EqualFold(t.Name.Local, "use") {
				return nil
			}
			nodes++
			if nodes > 10000 {
				return nil
			}
			if !root {
				if t.Name.Local != "svg" {
					return nil
				}
				root = true
			}
			for _, a := range t.Attr {
				name := strings.ToLower(a.Name.Local)
				if name == "stroke-dasharray" || name == "stroke-dashoffset" {
					return nil
				}
				v := strings.TrimSpace(a.Value)
				lower := strings.ToLower(v)
				if strings.Contains(lower, "stroke-dasharray") || strings.Contains(lower, "stroke-dashoffset") {
					return nil
				}
				if strings.EqualFold(a.Name.Local, "href") && !strings.HasPrefix(v, "#") {
					return nil
				}
				if strings.Contains(lower, "url(") && !strings.Contains(lower, "url(#") {
					return nil
				}
			}
		case xml.CharData:
			if strings.Contains(strings.ToLower(string(t)), "url(") || strings.Contains(strings.ToLower(string(t)), "stroke-dasharray") || strings.Contains(strings.ToLower(string(t)), "stroke-dashoffset") {
				return nil
			}
		}
	}
	if !root {
		return nil
	}
	icon, err := oksvg.ReadIconStream(bytes.NewReader(data), oksvg.StrictErrorMode)
	if err != nil {
		return nil
	}
	w, h := icon.ViewBox.W, icon.ViewBox.H
	if w <= 0 || h <= 0 || math.IsNaN(w) || math.IsNaN(h) || math.IsInf(w, 0) || math.IsInf(h, 0) {
		return nil
	}
	scale := math.Min(512/w, 512/h)
	width, height := max(1, int(math.Ceil(w*scale))), max(1, int(math.Ceil(h*scale)))
	dst := image.NewRGBA(image.Rect(0, 0, width, height))
	icon.SetTarget(0, 0, float64(width), float64(height))
	icon.Draw(rasterx.NewDasher(width, height, rasterx.NewScannerGV(width, height, dst, dst.Bounds())), 1)
	var out bytes.Buffer
	if png.Encode(&out, dst) != nil {
		return nil
	}
	return out.Bytes()
}
func quotationLogo(ctx context.Context, value string) ([]byte, extension.Type) {
	var data []byte
	if strings.HasPrefix(value, "data:") {
		if _, encoded, ok := strings.Cut(value, ","); ok && len(encoded) <= base64.StdEncoding.EncodedLen(quotationLogoLimit) {
			data, _ = base64.StdEncoding.DecodeString(encoded)
		}
	} else if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, value, nil)
		if err == nil {
			client := http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			if resp, err := client.Do(req); err == nil {
				defer resp.Body.Close()
				if resp.StatusCode >= 200 && resp.StatusCode < 300 {
					data, _ = io.ReadAll(io.LimitReader(resp.Body, quotationLogoLimit+1))
				}
			}
		}
	}
	if len(data) == 0 || len(data) > quotationLogoLimit {
		return quotationDefaultLogo, extension.Png
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err == nil && (format == "png" || format == "jpeg") && cfg.Width > 0 && cfg.Height > 0 && int64(cfg.Width)*int64(cfg.Height) <= 16_000_000 {
		if _, _, err = image.Decode(bytes.NewReader(data)); err == nil {
			if format == "jpeg" {
				return data, extension.Jpeg
			}
			return data, extension.Png
		}
	}
	if b := quotationSVG(data); len(b) > 0 {
		return b, extension.Png
	}
	return quotationDefaultLogo, extension.Png
}

func registerQuotationPDF(api huma.API, s sessionHandler) {
	responses := map[string]*huma.Response{}
	for _, code := range []string{"401", "403", "404", "422", "500"} {
		responses[code] = &huma.Response{Description: "Request failed", Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(authErrorBody{}), true, "")}}}
	}
	responses["422"].Content["application/json"].Schema = api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(validationErrorBody{}), true, "")
	responses["200"] = &huma.Response{Description: "Quotation PDF", Content: map[string]*huma.MediaType{"application/pdf": {Schema: &huma.Schema{Type: "string", Format: "binary"}}}}
	huma.Register(api, huma.Operation{Method: "GET", Path: "/api/v1/quotations/{quotation_id}/pdf", OperationID: "quotation-pdf", Security: []map[string][]string{{"sessionCookie": {}}}, Middlewares: huma.Middlewares{sessionMiddleware(s.quotationPDF)}, Responses: responses}, func(context.Context, *quotationGetInput) (*struct{}, error) { panic("middleware") })
}

func (s sessionHandler) quotationPDF(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	raw := chi.URLParam(r, "quotation_id")
	id, ok := parsePathUUID(raw)
	if !ok {
		d := uuidErrorDetail(raw)
		validationResponse(w, []validationError{{"uuid_parsing", []any{"path", "quotation_id"}, "Input should be a valid UUID, " + d, raw, map[string]string{"error": d}}})
		return
	}
	tx, e := s.db.BeginTx(r.Context(), pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		authError(w, 500, "internal server error")
		return
	}
	defer tx.Rollback(r.Context())
	q := dbquery.New(tx)
	quote, e := q.GetQuotation(r.Context(), id)
	if errors.Is(e, pgx.ErrNoRows) {
		authError(w, 404, "quotation not found")
		return
	}
	if e != nil {
		authError(w, 500, "internal server error")
		return
	}
	customer, e := q.GetCustomer(r.Context(), quote.CustomerID)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		authError(w, 500, "internal server error")
		return
	}
	business, e := q.GetBusinessProfile(r.Context())
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		authError(w, 500, "internal server error")
		return
	}
	lines, e := q.QuotationLines(r.Context(), []pgtype.UUID{id})
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
	b, e := renderQuotationPDF(r.Context(), quote, customer, business, lines, accounts)
	if e != nil {
		authError(w, 500, "internal server error")
		return
	}
	filename := quote.QuotationNumber.String
	if filename == "" {
		filename = fmt.Sprintf("%x", quote.QuotationID.Bytes[:4])
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": "quotation-" + filename + ".pdf"}))
	w.Write(b)
}

func quotationNumber(n pgtype.Numeric, currency string) string {
	if !n.Valid {
		return "—"
	}
	d, e := recurringDecimal(n)
	if e != nil {
		return "—"
	}
	dp := int32(2)
	switch strings.ToUpper(currency) {
	case "IDR", "JPY", "KRW", "VND", "CLP", "ISK", "HUF", "PYG", "RWF", "UGX", "XAF", "XOF", "XPF":
		dp = 0
	}
	s := d.RoundBank(dp).StringFixed(dp)
	parts := strings.SplitN(s, ".", 2)
	x := parts[0]
	start := 0
	if strings.HasPrefix(x, "-") {
		start = 1
	}
	for i := len(x) - 3; i > start; i -= 3 {
		x = x[:i] + "," + x[i:]
	}
	if len(parts) > 1 {
		x += "." + parts[1]
	}
	return x
}
func quotationDate(d pgtype.Date) string {
	if !d.Valid {
		return "—"
	}
	return d.Time.Format("02 Jan 2006")
}
func quotationText(t pgtype.Text) string {
	if !t.Valid {
		return ""
	}
	return t.String
}

// Each row is measured against the embedded font before it reaches Maroto.
// Explicit line breaks and overlong words are split without adding characters.
func quotationFitNumber(value string, widthMM float64, base props.Text) props.Text {
	var b sfnt.Buffer
	width := 0.0
	for _, r := range value {
		g, _ := quotationMetrics.GlyphIndex(&b, r)
		advance, _ := quotationMetrics.GlyphAdvance(&b, g, fixed.I(1000), font.HintingNone)
		width += float64(advance) / 64 / 1000 * base.Size * 25.4 / 72
	}
	if width > widthMM*0.9 {
		base.Size *= widthMM * 0.9 / width
	}
	return base
}
func quotationWrap(value string, widthMM, size float64) []string {
	var result []string
	var buf sfnt.Buffer
	for _, paragraph := range strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n") {
		runes := []rune(paragraph)
		if len(runes) == 0 {
			result = append(result, "")
			continue
		}
		for len(runes) > 0 {
			used := 0.0
			cut := 0
			lastSpace := -1
			for i, r := range runes {
				g, _ := quotationMetrics.GlyphIndex(&buf, r)
				advance, _ := quotationMetrics.GlyphAdvance(&buf, g, fixed.I(1000), font.HintingNone)
				w := float64(advance) / 64 / 1000 * size * 25.4 / 72
				if used+w > widthMM*0.90 && i > 0 {
					break
				}
				used += w
				cut = i + 1
				if r == ' ' {
					lastSpace = i
				}
			}
			if cut < len(runes) && lastSpace >= cut/2 {
				cut = lastSpace + 1
			}
			if cut == 0 {
				cut = 1
			}
			result = append(result, string(runes[:cut]))
			runes = runes[cut:]
		}
	}
	return result
}

// documentMasthead measures each line before placing it, so subsequent rows
// begin below even unusually long titles and references on every page.
func documentMasthead(logo []byte, logoType extension.Type, heading, ref string, style props.Text) []core.Row {
	// Treat embedded line breaks and other whitespace as spacing, so a
	// newline-heavy title cannot consume an entire page.
	parts := quotationWrap(strings.Join(strings.Fields(heading+" "+ref), " "), 135, style.Size)
	rows := []core.Row{row.New(22).Add(pdfimage.NewFromBytesCol(3, logo, logoType, props.Rect{Percent: 80}), text.NewCol(9, parts[0], style))}
	for _, part := range parts[1:] {
		rows = append(rows, row.New(9).Add(text.NewCol(3, "", style), text.NewCol(9, part, style)))
	}
	return rows
}

func renderQuotationPDF(ctx context.Context, q dbquery.Quotation, c dbquery.Customer, b dbquery.BusinessProfile, lines []dbquery.QuotationLineItem, accounts []dbquery.PaymentAccount) ([]byte, error) {
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
	ref := quotationText(q.QuotationNumber)
	if ref == "" {
		ref = "—"
	}
	logo, logoType := quotationLogo(ctx, quotationText(b.LogoUrl))
	brand := func() []core.Row { return documentMasthead(logo, logoType, "QUOTATION", ref, title) }
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
	add("Quote No.  " + ref + "    Issue Date  " + quotationDate(q.IssueDate) + "    Valid Until  " + quotationDate(q.ValidUntil))
	statusParts := strings.Split(strings.ToLower(strings.ReplaceAll(q.Status, "_", " ")), " ")
	for i, v := range statusParts {
		if len(v) > 0 {
			statusParts[i] = strings.ToUpper(v[:1]) + v[1:]
		}
	}
	add("Status: " + strings.Join(statusParts, " "))
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
	add("Total    " + quotationNumber(q.Amount, q.Currency) + " " + q.Currency)
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
	notes := quotationText(q.Notes)
	if notes == "" {
		notes = quotationText(b.DefaultNotes)
	}
	if notes != "" {
		add("Notes")
		add(notes)
	}
	if q.PaymentTerms.Valid && q.PaymentTerms.String != "" {
		add("Terms")
		add(q.PaymentTerms.String)
	}
	doc, e := m.Generate()
	if e != nil {
		return nil, e
	}
	return doc.GetBytes(), nil
}
