package businessscope

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// This is a staging completeness check, not proof of SQL isolation.
func TestSQLCStagingNamesAndArguments(t *testing.T) {
	names := func(dir string) map[string]string {
		t.Helper()
		files, err := filepath.Glob(filepath.Join("../..", dir, "*.sql"))
		if err != nil || len(files) == 0 {
			t.Fatalf("queries %s: %v", dir, err)
		}
		name := regexp.MustCompile(`(?m)^-- name: ([A-Za-z0-9_]+) :[a-z]+\s*$`)
		result := make(map[string]string)
		for _, path := range files {
			text, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			matches := name.FindAllStringSubmatchIndex(string(text), -1)
			for i, m := range matches {
				n := string(text[m[2]:m[3]])
				if _, exists := result[n]; exists {
					t.Fatalf("duplicate query %s", n)
				}
				end := len(text)
				if i+1 < len(matches) {
					end = matches[i+1][0]
				}
				result[n] = string(text[m[1]:end])
			}
		}
		return result
	}
	old, scoped := names("queries"), names("queries_v8")
	for name := range old {
		if _, ok := scoped[name]; !ok {
			t.Errorf("missing v8 query %s", name)
		}
	}
	// These staged queries replace inline SQL in the v7 handlers or combine
	// supporting reads into a single statement. They also include new scoped
	// operations for held intake and ledger integrity.
	stagedOnly := map[string]bool{
		"AcceptQuotation": true, "DeclineQuotation": true, "DeleteQuotation": true,
		"MarkQuotationSent": true, "QuotationSendDetails": true, "RecordInvoiceSend": true, "LockInvoiceReceiptPayment": true,
		"RecordQuotationSend": true, "VoidQuotation": true, "CustomerReportHasDamagedReferences": true,
		"CurrentAgingDocuments": true, "TrialBalanceLines": true, "ProjectReportHasDamagedReferences": true,
		"GetInvoiceSummaryBuckets": true, "GetInvoiceSummaryPaymentDays": true,
		"GetMonthlyRevenue":  true,
		"GetDashboardCounts": true, "GetDashboardOpenAR": true, "GetDashboardOpenQuotations": true,
		"GetDashboardActivity": true, "GetDashboardUpcomingSources": true,
		// Record held intake and its audit log within the configured business.
		"CreateHeldIntakePayment": true, "LogHeldIntakePayment": true,
		// Lock the invoice's payment and check for an existing posting.
		"LedgerLockPayment": true, "LedgerPaymentPosted": true,
		// Validate journal accounts and any existing reversal before reuse.
		"LedgerHasInvalidLineAccount": true, "LedgerReversalMatches": true,
	}
	for name := range stagedOnly {
		if _, ok := scoped[name]; !ok {
			t.Errorf("missing staged-only query %s", name)
		}
	}
	for name, sql := range scoped {
		if _, ok := old[name]; !ok && !stagedOnly[name] {
			t.Errorf("unexpected v8 query %s", name)
		}
		if name != "GetBusinessProfileDefaultTime" && !strings.Contains(sql, "sqlc.arg(business_id)::uuid") {
			t.Errorf("missing scoped argument in %s", name)
		}
	}
}
