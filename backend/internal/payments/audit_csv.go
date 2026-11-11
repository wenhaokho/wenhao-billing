package payments

import (
	"bytes"
	"context"
	"encoding/csv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/payments/paymentsdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// auditCSVMaxRows caps one export.
const auditCSVMaxRows = 50000

var auditCSVHeader = []string{
	"created_at", "action", "reason_codes", "reasons", "payer_name", "amount", "currency",
	"payment_date", "invoice_number", "customer_name", "actor_name", "intake_source", "confidence_score",
}

type auditCSVIn struct {
	Action   string                            `query:"action" doc:"Exact action; empty is no filter."`
	Payer    string                            `query:"payer" doc:"Substring of the payer name; empty is no filter."`
	FromDate platform.OptionalParam[time.Time] `query:"from_date" timeFormat:"2006-01-02"`
	ToDate   platform.OptionalParam[time.Time] `query:"to_date" timeFormat:"2006-01-02"`
}

// csvOut is a CSV download.
type csvOut struct {
	ContentType        string `header:"Content-Type"`
	ContentDisposition string `header:"Content-Disposition"`
	Body               []byte
}

func (r routes) auditLogCSV(ctx context.Context, in *auditCSVIn) (*csvOut, error) {
	rows, err := r.q.AuditLog(ctx, auditParams(in.Action, in.Payer, in.FromDate, in.ToDate, auditCSVMaxRows))
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	// A UTF-8 BOM first, so Excel (how the owner opens it) reads the file
	// as UTF-8 instead of the system code page.
	buf.WriteString("\xEF\xBB\xBF")
	w := csv.NewWriter(&buf)
	if err := w.Write(auditCSVHeader); err != nil {
		return nil, err
	}
	for _, l := range rows {
		if err := w.Write(auditCSVRecord(l)); err != nil {
			return nil, err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, err
	}
	return &csvOut{
		ContentType:        "text/csv; charset=utf-8",
		ContentDisposition: `attachment; filename="audit-log-` + platform.TodayUTC(r.clock).Format("2006-01-02") + `.csv"`,
		Body:               buf.Bytes(),
	}, nil
}

// auditCSVRecord is one row in the order of auditCSVHeader. Free text goes
// through csvText; amounts, dates and codes are produced here and never need it.
func auditCSVRecord(l paymentsdb.AuditLogRow) []string {
	rec := make([]string, 0, len(auditCSVHeader))
	created := ""
	if l.CreatedAt != nil {
		created = l.CreatedAt.UTC().Format(time.RFC3339)
	}
	amount := ""
	if l.Amount.Valid {
		amount = l.Amount.Decimal.StringFixed(4)
	}
	payDate := ""
	if l.PaymentDate.Valid {
		payDate = l.PaymentDate.Time.Format("2006-01-02")
	}
	actor := ""
	switch {
	case l.ActorDisplayName != nil && *l.ActorDisplayName != "":
		actor = *l.ActorDisplayName
	case l.ActorEmail != nil:
		actor = *l.ActorEmail
	}
	rec = append(rec,
		created, l.Action,
		csvText(strings.Join(l.ReasonCodes, "; ")), csvText(strings.Join(l.Reasons, "; ")),
		csvText(deref(l.PayerName)), amount, deref(l.Currency), payDate,
		csvText(deref(l.InvoiceNumber)), csvText(deref(l.CustomerName)), csvText(actor),
		deref(l.IntakeSource), scoreText(l.ConfidenceScore),
	)
	return rec
}

func scoreText(d decimal.NullDecimal) string {
	if !d.Valid {
		return ""
	}
	return d.Decimal.StringFixed(2)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// csvText guards a free-text cell against spreadsheet formula injection (OWASP):
// a cell starting with =, +, -, @, tab, CR or LF gets a single quote in front.
func csvText(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r\n", rune(s[0])) {
		return "'" + s
	}
	return s
}
