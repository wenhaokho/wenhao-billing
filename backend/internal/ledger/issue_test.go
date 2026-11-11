package ledger

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/wenhaokho/wenhao-billing/backend/internal/fx"
)

// issueDay is after apitest's seed rates (USD 16000, SGD 12000 on 2026-01-01).
var issueDay = time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)

// shares are Shares on the accounts with the codes, in pairs: code, amount.
func shares(t *testing.T, pool *pgxpool.Pool, pairs ...string) []Share {
	t.Helper()
	ids := accountIDs(t, pool)
	out := make([]Share, 0, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		id, ok := ids[pairs[i]]
		if !ok {
			t.Fatalf("no account %s", pairs[i])
		}
		out = append(out, Share{AccountID: id, Amount: dec(pairs[i+1])})
	}
	return out
}

func document(sourceType, currency, amount string, ss []Share) Document {
	return Document{
		SourceType: sourceType, ID: uuid.New(), Currency: currency, Amount: dec(amount),
		RateDate: issueDay, IssueDate: issueDay, Memo: sourceType + " issued", Shares: ss,
	}
}

// postIssued posts d and commits.
func postIssued(t *testing.T, pool *pgxpool.Pool, d Document) Entry {
	t.Helper()
	tx := begin(t, pool)
	e, err := PostIssued(context.Background(), tx, base, d)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	return e
}

func postedAt(t *testing.T, pool *pgxpool.Pool, entryID uuid.UUID) time.Time {
	t.Helper()
	var at time.Time
	if err := pool.QueryRow(context.Background(), `SELECT posted_at FROM journal_entries WHERE entry_id = $1`, entryID).Scan(&at); err != nil {
		t.Fatal(err)
	}
	return at
}

// addRate stores a from->IDR rate as of the date.
func addRate(t *testing.T, pool *pgxpool.Pool, from, rate string, on time.Time) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO fx_rates (from_currency, to_currency, rate, as_of_date, source) VALUES ($1, 'IDR', $2::numeric, $3::date, 'TEST')`,
		from, rate, on.Format(time.DateOnly)); err != nil {
		t.Fatal(err)
	}
}

func TestPostIssuedInvoiceIDR(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	d := document(SourceInvoice, "IDR", "1000.0000", shares(t, pool, FixedFeeCode, "600", SupportCode, "400"))
	e := postIssued(t, pool, d)
	if e.SourceType != SourceInvoice || e.SourceID != d.ID || e.Memo == nil || *e.Memo != d.Memo {
		t.Errorf("entry = %s %s %v", e.SourceType, e.SourceID, e.Memo)
	}
	checkLines(t, pool, e, []want{
		{ReceivableCode, "1000", "0", "IDR", "1000", "0", "1"},
		{FixedFeeCode, "0", "600", "IDR", "0", "600", "1"},
		{SupportCode, "0", "400", "IDR", "0", "400", "1"},
	})
}

func TestPostIssuedInvoiceUSDBaseRounding(t *testing.T) {
	t.Parallel()
	const rate = "15123.45678901"
	rateDay := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name  string
		pairs []string
		want  []want
	}{
		{
			// Each share converts to its own rounded base and the bases sum
			// to Convert(100.0001) = 1512347.1912.
			name:  "brief",
			pairs: []string{FixedFeeCode, "33.3334", SupportCode, "33.3334", UsageCode, "33.3333"},
			want: []want{
				{ReceivableCode, "100.0001", "0", "USD", "1512347.1912", "0", rate},
				{FixedFeeCode, "0", "33.3334", "USD", "0", "504116.2345", rate},
				{SupportCode, "0", "33.3334", "USD", "0", "504116.2345", rate},
				{UsageCode, "0", "33.3333", "USD", "0", "504114.7222", rate},
			},
		},
		{
			// Separately each converts to 504114.7222, 504114.7222 and
			// 504117.7469, 0.0001 over the control; the largest absorbs it.
			name:  "largest absorbs",
			pairs: []string{FixedFeeCode, "33.3333", SupportCode, "33.3335", UsageCode, "33.3333"},
			want: []want{
				{ReceivableCode, "100.0001", "0", "USD", "1512347.1912", "0", rate},
				{FixedFeeCode, "0", "33.3333", "USD", "0", "504114.7222", rate},
				{SupportCode, "0", "33.3335", "USD", "0", "504117.7468", rate},
				{UsageCode, "0", "33.3333", "USD", "0", "504114.7222", rate},
			},
		},
		{
			// Each converts to 15124.9691, 0.0001 under the control 45374.9074;
			// on a tie the lowest code absorbs it, wherever it is listed.
			name:  "tie goes to the first code",
			pairs: []string{UsageCode, "1.0001", SupportCode, "1.0001", FixedFeeCode, "1.0001"},
			want: []want{
				{ReceivableCode, "3.0003", "0", "USD", "45374.9074", "0", rate},
				{UsageCode, "0", "1.0001", "USD", "0", "15124.9691", rate},
				{SupportCode, "0", "1.0001", "USD", "0", "15124.9691", rate},
				{FixedFeeCode, "0", "1.0001", "USD", "0", "15124.9692", rate},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			pool := newPool(t)
			addRate(t, pool, "USD", rate, rateDay)
			d := document(SourceInvoice, "USD", c.want[0].debit, shares(t, pool, c.pairs...))
			d.RateDate = rateDay.AddDate(0, 0, 9)
			e := postIssued(t, pool, d)
			checkLines(t, pool, e, c.want)
			var net string
			if err := pool.QueryRow(context.Background(),
				`SELECT (sum(base_amount_debit) - sum(base_amount_credit))::text FROM journal_lines WHERE entry_id = $1`, e.ID).Scan(&net); err != nil {
				t.Fatal(err)
			}
			if net != "0.0000" {
				t.Errorf("base net = %s, want 0.0000", net)
			}
		})
	}
}

func TestPostIssuedBillDebitsSharesCreditsAP(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	addRate(t, pool, "USD", "15123.45678901", issueDay)
	d := document(SourceBill, "USD", "100.0001", shares(t, pool, "5000", "33.3333", "5100", "33.3335", "5200", "33.3333"))
	e := postIssued(t, pool, d)
	if e.SourceType != SourceBill || e.SourceID != d.ID {
		t.Errorf("entry = %s %s", e.SourceType, e.SourceID)
	}
	const rate = "15123.45678901"
	checkLines(t, pool, e, []want{
		{"5000", "33.3333", "0", "USD", "504114.7222", "0", rate},
		{"5100", "33.3335", "0", "USD", "504117.7468", "0", rate},
		{"5200", "33.3333", "0", "USD", "504114.7222", "0", rate},
		{PayableCode, "0", "100.0001", "USD", "0", "1512347.1912", rate},
	})
}

func TestPostIssuedPostedAtIsIssueDateOrNow(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	ss := shares(t, pool, FixedFeeCode, "10")

	past := document(SourceInvoice, "IDR", "10", ss)
	past.IssueDate = time.Date(2026, 1, 5, 17, 30, 0, 0, time.UTC) // the time of day is dropped
	e := postIssued(t, pool, past)
	if got := postedAt(t, pool, e.ID); !got.Equal(issueDay) {
		t.Errorf("posted_at = %s, want %s", got, issueDay)
	}

	future := document(SourceInvoice, "IDR", "10", ss)
	future.IssueDate = time.Now().AddDate(0, 1, 0)
	e = postIssued(t, pool, future)
	var off float64
	if err := pool.QueryRow(context.Background(),
		`SELECT abs(extract(epoch FROM posted_at - LOCALTIMESTAMP))::float8 FROM journal_entries WHERE entry_id = $1`, e.ID).Scan(&off); err != nil {
		t.Fatal(err)
	}
	if off > 1 {
		t.Errorf("posted_at is %.3fs from now, want within 1s", off)
	}
}

func TestPostIssuedTwiceRefused(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	d := document(SourceInvoice, "IDR", "10", shares(t, pool, FixedFeeCode, "10"))
	postIssued(t, pool, d)
	tx := begin(t, pool)
	_, err := PostIssued(context.Background(), tx, base, d)
	if !errors.Is(err, ErrLedger) {
		t.Fatalf("err = %v, want ErrLedger", err)
	}
	if want := "INVOICE " + d.ID.String() + " already has an active issue entry"; err.Error() != want {
		t.Errorf("text = %q, want %q", err.Error(), want)
	}
	if e, l := countRows(t, tx); e != 1 || l != 2 {
		t.Errorf("%d entries and %d lines, want the first post's 1 and 2", e, l)
	}
}

func TestPostIssuedSharesMustSumToAmount(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	cases := []struct {
		name, amount string
		pairs        []string
		msg          string
	}{
		{"short", "1000.0000", []string{FixedFeeCode, "600", SupportCode, "399.9999"}, "shares sum to 999.9999, not the amount 1000.0000"},
		{"over", "1000.0000", []string{FixedFeeCode, "600", SupportCode, "400.0001"}, "shares sum to 1000.0001, not the amount 1000.0000"},
		{"zero share", "1000", []string{FixedFeeCode, "1000", SupportCode, "0"}, "share on account %d must be > 0 (got 0)"},
		{"negative share", "1000", []string{FixedFeeCode, "1001", SupportCode, "-1"}, "share on account %d must be > 0 (got -1)"},
		{"no shares", "0", nil, "amount must be > 0 (got 0)"},
		{"unknown account", "10", nil, "chart_of_accounts missing account 999999"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ss := shares(t, pool, c.pairs...)
			if c.name == "unknown account" {
				ss = []Share{{AccountID: 999999, Amount: dec("10")}}
			}
			d := document(SourceInvoice, "IDR", c.amount, ss)
			msg := c.msg
			if strings.Contains(msg, "%d") {
				msg = fmt.Sprintf(msg, ss[1].AccountID)
			}
			tx := begin(t, pool)
			_, err := PostIssued(context.Background(), tx, base, d)
			refusedFor(t, tx, err, d, msg)
		})
	}
}

// refusedFor is refused for a refusal about document d: msg prefixed with
// "<source> <id> ".
func refusedFor(t *testing.T, tx pgx.Tx, err error, d Document, msg string) {
	t.Helper()
	refused(t, tx, err, d.SourceType+" "+d.ID.String()+" "+msg)
}

// At a rate of 0.0003 each 0.5 share converts to 0.00015 → 0.0002 (half
// even) and the 1.5 control to 0.00045 → 0.0004: the absorbing share (the
// lowest code on the tie) would be left a base of 0, which is refused.
func TestPostIssuedAbsorbedBaseNotPositiveRefused(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	addRate(t, pool, "USD", "0.0003", issueDay)
	d := document(SourceInvoice, "USD", "1.5", shares(t, pool, UsageCode, "0.5", SupportCode, "0.5", FixedFeeCode, "0.5"))
	tx := begin(t, pool)
	_, err := PostIssued(context.Background(), tx, base, d)
	refusedFor(t, tx, err, d, "share on account "+FixedFeeCode+" converts to a base of 0.0000 after rounding; must be > 0")
}

func TestPostIssuedRateMissing(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	d := document(SourceInvoice, "EUR", "10", shares(t, pool, FixedFeeCode, "10"))
	tx := begin(t, pool)
	_, err := PostIssued(context.Background(), tx, base, d)
	if !errors.Is(err, fx.ErrRateMissing) {
		t.Fatalf("err = %v, want ErrRateMissing", err)
	}
	if e, l := countRows(t, tx); e != 0 || l != 0 {
		t.Errorf("%d entries and %d lines written, want none", e, l)
	}
}

func TestPostIssuedUnknownSourceType(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	d := document("PAYMENT", "IDR", "10", shares(t, pool, FixedFeeCode, "10"))
	tx := begin(t, pool)
	_, err := PostIssued(context.Background(), tx, base, d)
	if err == nil || errors.Is(err, ErrLedger) {
		t.Fatalf("err = %v, want a plain error", err)
	}
	if e, l := countRows(t, tx); e != 0 || l != 0 {
		t.Errorf("%d entries and %d lines written, want none", e, l)
	}
}

func reverseIssued(t *testing.T, pool *pgxpool.Pool, sourceType string, id uuid.UUID, memo string) bool {
	t.Helper()
	tx := begin(t, pool)
	ok, err := ReverseIssued(context.Background(), tx, sourceType, id, memo)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	return ok
}

func TestReverseIssuedNoneIsFalse(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	if reverseIssued(t, pool, SourceBill, uuid.New(), "void") {
		t.Error("reversed = true for a document with no entry")
	}
	if e, l := countRows(t, pool); e != 0 || l != 0 {
		t.Errorf("%d entries and %d lines written, want none", e, l)
	}
}

func TestReverseIssuedThenPostAgain(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	d := document(SourceBill, "IDR", "10", shares(t, pool, "5000", "10"))
	first := postIssued(t, pool, d)
	if !reverseIssued(t, pool, SourceBill, d.ID, "Bill back to draft") {
		t.Fatal("reversed = false after a post")
	}
	var memo string
	var reversedBy uuid.UUID
	if err := pool.QueryRow(context.Background(),
		`SELECT r.memo, o.reversed_by_entry_id FROM journal_entries o JOIN journal_entries r ON r.entry_id = o.reversed_by_entry_id WHERE o.entry_id = $1`,
		first.ID).Scan(&memo, &reversedBy); err != nil {
		t.Fatal(err)
	}
	if memo != "Bill back to draft" {
		t.Errorf("reversal memo = %q", memo)
	}
	if reverseIssued(t, pool, SourceBill, d.ID, "again") {
		t.Error("reversed = true with no active entry left")
	}
	second := postIssued(t, pool, d)
	if id, ok := active(t, pool, SourceBill, d.ID); !ok || id != second.ID {
		t.Errorf("active = %s %v, want the repost %s", id, ok, second.ID)
	}
}

func TestPostReversalAtKeepsDate(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	d := document(SourceInvoice, "IDR", "10", shares(t, pool, FixedFeeCode, "10"))
	orig := postIssued(t, pool, d)
	at := postedAt(t, pool, orig.ID)
	tx := begin(t, pool)
	memo := "restated"
	rev, err := PostReversalAt(context.Background(), tx, orig.ID, &memo, at)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := postedAt(t, pool, rev.ID); !got.Equal(issueDay) {
		t.Errorf("reversal posted_at = %s, want %s", got, issueDay)
	}
	checkLines(t, pool, rev, []want{
		{ReceivableCode, "0", "10", "IDR", "0", "10", "1"},
		{FixedFeeCode, "10", "0", "IDR", "10", "0", "1"},
	})
	var reversedBy *uuid.UUID
	if err := pool.QueryRow(context.Background(), `SELECT reversed_by_entry_id FROM journal_entries WHERE entry_id = $1`, orig.ID).
		Scan(&reversedBy); err != nil {
		t.Fatal(err)
	}
	if reversedBy == nil || *reversedBy != rev.ID {
		t.Errorf("reversed_by_entry_id = %v, want %s", reversedBy, rev.ID)
	}
}

func TestAccountsByIDAndCode(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	ids := accountIDs(t, pool)
	tx := begin(t, pool)
	byCode, err := AccountsByCode(context.Background(), tx, []string{PayableCode, FixedFeeCode, "nope"})
	if err != nil {
		t.Fatal(err)
	}
	if len(byCode) != 2 {
		t.Fatalf("by code = %+v, want 2 accounts", byCode)
	}
	if a := byCode[PayableCode]; a.ID != ids[PayableCode] || a.Code != PayableCode || a.Type != "LIABILITY" || !a.Active {
		t.Errorf("2000 = %+v", a)
	}
	byID, err := AccountsByID(context.Background(), tx, []int32{ids[FixedFeeCode], 999999})
	if err != nil {
		t.Fatal(err)
	}
	if len(byID) != 1 {
		t.Fatalf("by id = %+v, want 1 account", byID)
	}
	if a := byID[ids[FixedFeeCode]]; a.Code != FixedFeeCode || a.Type != "INCOME" || !a.Active {
		t.Errorf("4000 = %+v", a)
	}
}
