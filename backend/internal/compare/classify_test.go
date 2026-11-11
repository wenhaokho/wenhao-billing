package compare

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// TestBillPaymentsIsGoOnly: bill_payments is Go's own table (PN 165). A
// scenario cannot list it, and the row comparison skips it.
func TestBillPaymentsIsGoOnly(t *testing.T) {
	t.Parallel()
	_, err := ParseWrites([]byte("scenarios:\n  - id: x\n    tables: [bill_payments]\n    steps:\n      - id: s\n        method: POST\n        path: /x\n"))
	if err == nil || !strings.Contains(err.Error(), "Go-only") {
		t.Fatalf("error %v, want the Go-only refusal for bill_payments", err)
	}

	f := newRowsFixture(t, "bill_payments")
	f.snapshot(t)
	goBill(t, f.gov, goVend, goBillID, "PARTIAL", "1000")
	mustExec(t, f.gov, `INSERT INTO bill_payments (bill_payment_id, bill_id, amount, currency, payment_date)
		VALUES (gen_random_uuid(), $1, 400, 'IDR', '2026-01-02')`, goBillID)
	if diffs := f.compare(t, IDMaps{}); len(diffs) != 0 {
		t.Fatalf("diffs %+v, want none: bill_payments is not compared", diffs)
	}
}

// TestWritesReportCountsUnclassified: a classified row difference is still
// counted and listed with its note; Unclassified counts the rest, step answer
// differences included, and the exit code is unchanged.
func TestWritesReportCountsUnclassified(t *testing.T) {
	t.Parallel()
	pyDB, goDB := openTestDB(t), openTestDB(t)
	goPool := openTestPool(t, goDB)
	py := writeBackend(t, true, func(w http.ResponseWriter, _ *http.Request) {
		jsonBody(w, http.StatusOK, `{"ok":true}`)
	})
	gov := writeBackend(t, false, func(w http.ResponseWriter, _ *http.Request) {
		// Go posts an issue entry (PN 162) and a payment entry for a payment
		// Python does not have (unclassified).
		insertGoInvoice(t, goPool, goSource, "OPEN", "IDR", "1000", "2026-01-01", false, "")
		insertEntry(t, goPool, goEntry, "INVOICE", goSource, "Invoice issued",
			idr(acctAR, "1000", "0"), idr(acctRevenue, "0", "1000"))
		insertEntry(t, goPool, goEntry2, "PAYMENT", goSource, "Payment cleared",
			idr(acctBank, "1000", "0"), idr(acctAR, "0", "1000"))
		jsonBody(w, http.StatusOK, `{"ok":false}`)
	})
	scenarios := WritesScenarios{Scenarios: []WritesScenario{{
		ID: "accrual", Tables: []string{"journal_entries", "journal_lines"},
		Steps: []WriteStep{{ID: "s", Method: http.MethodPost, Path: "/api/v1/things"}},
	}}}
	report, err := RunWrites(context.Background(), writeOptions(t, py, gov, pyDB, goDB, scenarios))
	if err != nil {
		t.Fatalf("RunWrites: %v", err)
	}
	if report.Differences != 7 || report.Unclassified != 4 {
		t.Fatalf("differences %d unclassified %d, want 7 (6 rows + $.ok) and 4: rows %+v", report.Differences, report.Unclassified, report.RowDiffs)
	}
	notes, checks := 0, 0
	for _, d := range report.RowDiffs {
		if d.Classify == "PN 162" {
			notes++
		}
		if strings.HasPrefix(d.Check, "PN 161 check failed") {
			checks++
		}
	}
	if notes != 3 || checks != 3 || len(report.RowDiffs) != 6 {
		t.Fatalf("row diffs %+v, want all 6 listed, 3 of them PN 162 and 3 naming the PN 161 check", report.RowDiffs)
	}
	if got := WritesSummary(report); got != "7 differences, 4 unclassified" {
		t.Fatalf("summary %q", got)
	}
	if got := WritesExitCode(report, nil); got != 1 {
		t.Fatalf("exit code %d, want 1: classified differences still count", got)
	}
	if got := WritesExitCode(WritesReport{Differences: 3}, nil); got != 1 {
		t.Fatalf("exit code %d for only classified differences, want 1", got)
	}
}

// TestWritesClassifiesAnswerPaths: classify_answers names the note for the
// listed JSON paths only; the differences are still counted, and a
// difference at another path stays unclassified.
func TestWritesClassifiesAnswerPaths(t *testing.T) {
	t.Parallel()
	pyDB, goDB := openTestDB(t), openTestDB(t)
	py := writeBackend(t, true, func(w http.ResponseWriter, _ *http.Request) {
		jsonBody(w, http.StatusOK, `{"balance_due":"1000.0000","notes":null,"name":"a"}`)
	})
	gov := writeBackend(t, false, func(w http.ResponseWriter, _ *http.Request) {
		jsonBody(w, http.StatusOK, `{"balance_due":"0.0000","notes":"\n[voided] x","name":"b"}`)
	})
	scenarios := WritesScenarios{Scenarios: []WritesScenario{{
		ID: "bill",
		Steps: []WriteStep{{
			ID: "patch", Method: http.MethodPatch, Path: "/api/v1/bills/x",
			ClassifyAnswers: &ClassifyAnswers{Note: "PN 164", Paths: []string{"$.balance_due", "$.notes"}},
		}},
	}}}
	report, err := RunWrites(context.Background(), writeOptions(t, py, gov, pyDB, goDB, scenarios))
	if err != nil {
		t.Fatalf("RunWrites: %v", err)
	}
	got := map[string]string{}
	for _, d := range report.Scenarios[0].Steps[0].Differences {
		got[d.Path] = d.Classify
	}
	if len(got) != 3 || got["$.balance_due"] != "PN 164" || got["$.notes"] != "PN 164" || got["$.name"] != "" {
		t.Fatalf("differences %+v, want balance_due and notes PN 164 and name unclassified", report.Scenarios[0].Steps[0].Differences)
	}
	if report.Differences != 3 || report.Unclassified != 1 {
		t.Fatalf("differences %d unclassified %d, want 3 and 1", report.Differences, report.Unclassified)
	}
}

// TestWritesParseNotes: every step note is exactly one PN, and
// classify_answers needs a PN, at least one $.path, and a compared answer.
func TestWritesParseNotes(t *testing.T) {
	t.Parallel()
	doc := func(step string) []byte {
		return []byte("scenarios:\n  - id: x\n    steps:\n      - {id: s, method: POST, path: /x" + step + "}\n")
	}
	cases := []struct{ name, step, wantErr string }{
		{"expect_status PN", `, expect_status: {python: 400, go: 200}, note: "PN 161"`, ""},
		{"expect_status free text", `, expect_status: {python: 400, go: 200}, note: "PN 161 reversal"`, "not one PN"},
		{"go_only no space", `, go_only: true, note: "PN165"`, "not one PN"},
		{"go_only words", `, go_only: true, note: "see 165"`, "not one PN"},
		{"classify_answers ok", `, classify_answers: {note: "PN 164", paths: ["$.notes"]}`, ""},
		{"classify_answers bad note", `, classify_answers: {note: "164", paths: ["$.notes"]}`, "not one PN"},
		{"classify_answers no paths", `, classify_answers: {note: "PN 164"}`, "names no paths"},
		{"classify_answers wildcard", `, classify_answers: {note: "PN 164", paths: ["$.*"]}`, "not one JSON path"},
		{"classify_answers root", `, classify_answers: {note: "PN 164", paths: ["$"]}`, "not one JSON path"},
		{"classify_answers on go_only", `, go_only: true, note: "PN 165", classify_answers: {note: "PN 165", paths: ["$.a"]}`, "does not apply"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseWrites(doc(tc.step))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("ParseWrites: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %v, want %q", err, tc.wantErr)
			}
		})
	}
}
