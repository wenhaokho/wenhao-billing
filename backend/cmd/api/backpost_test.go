package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// TestBackpostCLIUsage: `api backpost-ledger [--apply]` exits 0 done or dry
// run, 1 refused, 2 on a usage error (PORT-NOTES 166).
func TestBackpostCLIUsage(t *testing.T) {
	t.Parallel()
	entries := func(t *testing.T, a cliApp) int {
		t.Helper()
		var n int
		if err := a.pool.QueryRow(context.Background(), `SELECT COUNT(*) FROM journal_entries`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	invoice := func(t *testing.T, a cliApp, currency string) {
		t.Helper()
		if _, err := a.pool.Exec(context.Background(), `INSERT INTO invoices (invoice_id, invoice_number, invoice_type, currency, amount, balance_due, status, issue_date)
VALUES ($1, 'INV-1', 'MILESTONE', $2, 100, 100, 'OPEN', '2026-02-01')`, uuid.New(), currency); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		name     string
		currency string
		args     []string
		code     int
		out      string
		written  int
	}{
		{"unknown flag", "IDR", []string{"--bogus"}, 2, "usage: api backpost-ledger [--apply]", 0},
		{"extra argument", "IDR", []string{"--apply", "now"}, 2, "usage: api backpost-ledger [--apply]", 0},
		{"dry run", "IDR", nil, 0, "dry run: nothing written", 0},
		{"apply", "IDR", []string{"--apply"}, 0, "applied: committed", 1},
		{"refused", "EUR", []string{"--apply"}, 1, "INVOICE INV-1 EUR 2026-02-01: no EUR→IDR rate on or before", 0},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			a := newCLIApp(t)
			invoice(t, a, c.currency)
			var out bytes.Buffer
			if code := backpostCmd(context.Background(), a.pool, "IDR", c.args, &out); code != c.code {
				t.Errorf("exit = %d, want %d\n%s", code, c.code, out.String())
			}
			if !strings.Contains(out.String(), c.out) {
				t.Errorf("output lacks %q:\n%s", c.out, out.String())
			}
			if n := entries(t, a); n != c.written {
				t.Errorf("entries = %d, want %d", n, c.written)
			}
		})
	}
}
