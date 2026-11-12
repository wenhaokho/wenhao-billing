package payments

import (
	"encoding/csv"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/payments/paymentsdb"
)

const wantAuditCSVHeader = "created_at,action,reason_codes,reasons,payer_name,amount,currency,payment_date,invoice_number,customer_name,actor_name,intake_source,confidence_score"

func (a *api) auditCSV(t *testing.T, query string) (http.Header, [][]string) {
	t.Helper()
	rec := a.app.Do(t, http.MethodGet, reconPath+"/audit-log.csv"+query, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	// A UTF-8 BOM leads the file so Excel reads it as UTF-8.
	body, ok := strings.CutPrefix(rec.Body.String(), "\xEF\xBB\xBF")
	if !ok {
		t.Fatalf("no UTF-8 BOM: %q", rec.Body.String()[:min(rec.Body.Len(), 16)])
	}
	rows, err := csv.NewReader(strings.NewReader(body)).ReadAll()
	if err != nil {
		t.Fatalf("csv: %v\n%s", err, rec.Body)
	}
	return rec.Header(), rows
}

func TestAuditLogCSV(t *testing.T) {
	t.Parallel()
	a := newAPI(t, NoHosting{})
	odd := a.seedPayment(t, `Smith, "Bob" & Co`, "CLEARED")
	a.exec(t, `UPDATE payments SET amount = 1234.5, confidence_score = 97.5 WHERE payment_id = $1`, odd)
	evil := a.seedPayment(t, "=HYPERLINK(\"http://x\")", "CLEARED")
	other := a.seedPayment(t, "Beta Ltd", "PENDING_MANUAL_REVIEW")
	admin := a.admin
	a.logAt(t, other, "HELD", at(2026, 4, 1, 9, 0, 0), nil)
	a.logAt(t, odd, "OVERRIDDEN", at(2026, 4, 2, 10, 30, 0), &admin)
	a.logAt(t, evil, "CLEARED", at(2026, 4, 3, 0, 0, 0), nil)

	res, rows := a.auditCSV(t, "")
	if ct := res.Get("Content-Type"); ct != "text/csv; charset=utf-8" {
		t.Errorf("content type %q", ct)
	}
	if cd := res.Get("Content-Disposition"); !regexp.MustCompile(`^attachment; filename="audit-log-\d{4}-\d{2}-\d{2}\.csv"$`).MatchString(cd) {
		t.Errorf("content disposition %q", cd)
	}
	if got := strings.Join(rows[0], ","); got != wantAuditCSVHeader {
		t.Fatalf("header %q", got)
	}
	if len(rows) != 4 {
		t.Fatalf("%d rows, want header + 3", len(rows))
	}
	// Newest first: evil, odd, other.
	if rows[1][4] != `'=HYPERLINK("http://x")` {
		t.Errorf("formula not guarded: %q", rows[1][4])
	}
	r := rows[2]
	want := []string{"2026-04-02T10:30:00Z", "OVERRIDDEN", "OTHER", "r", `Smith, "Bob" & Co`, "1234.5000", "USD", "2026-04-15", "", "", r[10], "WEBHOOK", "97.50"}
	if !slices.Equal(r, want) {
		t.Errorf("row %q\nwant %q", r, want)
	}
	if r[10] == "" {
		t.Errorf("actor_name empty for a user action")
	}
	if rows[3][10] != "" || rows[3][8] != "" {
		t.Errorf("system row should have empty actor and invoice: %q", rows[3])
	}

	_, rows = a.auditCSV(t, "?payer=smith&action=OVERRIDDEN&from_date=2026-04-02&to_date=2026-04-02")
	if len(rows) != 2 || rows[1][1] != "OVERRIDDEN" {
		t.Errorf("filters: %q", rows)
	}
	_, rows = a.auditCSV(t, "?payer=nobody")
	if len(rows) != 1 {
		t.Errorf("no match should be header only: %q", rows)
	}
}

func TestCSVTextGuardsFormulas(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"=1+1": "'=1+1", "+1": "'+1", "-1": "'-1", "@x": "'@x", "\tx": "'\tx", "\rx": "'\rx", "\nx": "'\nx",
		"plain": "plain", "": "", "a=b": "a=b",
	}
	for in, want := range cases {
		if got := csvText(in); got != want {
			t.Errorf("csvText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAuditCSVRecordKeepsNegativeAmount(t *testing.T) {
	t.Parallel()
	row := paymentsdb.AuditLogRow{Action: "CLEARED"}
	row.Amount = decimal.NullDecimal{Decimal: decimal.RequireFromString("-12"), Valid: true}
	e := auditCSVRecord(row)
	if len(e) != 13 || e[5] != "-12.0000" {
		t.Fatalf("record %q", e)
	}
}
