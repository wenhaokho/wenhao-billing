package migrations

import (
	"context"
	"github.com/jackc/pgx/v5"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"
	"github.com/wenhaokho/wenhao-billing/backend/internal/business"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedledger"
)

func TestScopedLedgerPosting(t *testing.T) {
	db, url := freshDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	if err := MigrateFresh(ctx, url); err != nil {
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, fstest.MapFS{"00008_business_scope.sql": &fstest.MapFile{Data: scopeMigration}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var aText string
	if err = pool.QueryRow(ctx, "SELECT business_id::text FROM businesses").Scan(&aText); err != nil {
		t.Fatal(err)
	}
	a, err := business.Parse(aText)
	if err != nil {
		t.Fatal(err)
	}
	b, err := business.Parse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	if err != nil {
		t.Fatal(err)
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("INSERT INTO businesses(business_id) VALUES ($1)", b.UUID())
	exec("INSERT INTO chart_of_accounts(business_id,code,name,type) SELECT $1,code,name,type FROM chart_of_accounts WHERE business_id=$2", b.UUID(), a.UUID())
	exec("INSERT INTO fx_rates(business_id,from_currency,to_currency,rate,as_of_date,source) VALUES ($1,'USD','IDR',2,'2024-01-01','test'),($2,'USD','IDR',5,'2024-01-01','test'),($1,'SGD','IDR',3,'2024-01-01','test'),($2,'SGD','IDR',7,'2024-01-01','test')", a.UUID(), b.UUID())
	seed := func(scope business.ID, currency, invoiceCurrency, status string) (pgtype.UUID, pgtype.UUID) {
		t.Helper()
		var invoice, payment pgtype.UUID
		err := pool.QueryRow(ctx, "INSERT INTO invoices(business_id,invoice_id,invoice_type,currency,amount,balance_due,status,wht_amount) VALUES ($1,gen_random_uuid(),'MILESTONE',$2,10,10,'OPEN',2) RETURNING invoice_id", scope.UUID(), invoiceCurrency).Scan(&invoice)
		if err != nil {
			t.Fatal(err)
		}
		err = pool.QueryRow(ctx, "INSERT INTO payments(business_id,payment_id,invoice_id,amount,currency,payer_name,payment_date,intake_source,status,wht_amount) VALUES ($1,gen_random_uuid(),$2,8,$3,'payer','2024-01-01','WEBHOOK',$4,2) RETURNING payment_id", scope.UUID(), invoice, currency, status).Scan(&payment)
		if err != nil {
			t.Fatal(err)
		}
		return invoice, payment
	}
	exec("INSERT INTO fx_rates(business_id,from_currency,to_currency,rate,as_of_date,source) VALUES ($1,'EUR','IDR',11,'2024-01-01','test')", a.UUID())
	invoiceA, paymentA := seed(a, "USD", "USD", "CLEARED")
	invoiceB, paymentB := seed(b, "USD", "SGD", "CLEARED")
	heldInvoice, held := seed(a, "USD", "USD", "PENDING_MANUAL_REVIEW")
	wrongInvoice, _ := seed(a, "USD", "USD", "CLEARED")
	missingFXInvoice, missingFXPayment := seed(b, "EUR", "SGD", "CLEARED")
	badAmountInvoice, badAmountPayment := seed(a, "USD", "USD", "CLEARED")
	exec("UPDATE payments SET amount = -8 WHERE business_id=$1 AND payment_id=$2", a.UUID(), badAmountPayment)
	badWHTInvoice, badWHTPayment := seed(a, "USD", "USD", "CLEARED")
	exec("UPDATE payments SET wht_amount = 3 WHERE business_id=$1 AND payment_id=$2", a.UUID(), badWHTPayment)
	draftInvoice, draftPayment := seed(a, "USD", "USD", "CLEARED")
	exec("UPDATE invoices SET status='DRAFT' WHERE business_id=$1 AND invoice_id=$2", a.UUID(), draftInvoice)
	post := func(scope business.ID, payment, invoice pgtype.UUID) (pgtype.UUID, error) {
		tx, err := pool.Begin(ctx)
		if err != nil {
			return pgtype.UUID{}, err
		}
		defer tx.Rollback(ctx)
		id, err := scopedledger.PostPayment(ctx, tx, scope, payment, invoice, "IDR")
		if err != nil {
			return id, err
		}
		return id, tx.Commit(ctx)
	}
	for _, attempt := range []struct {
		scope            business.ID
		payment, invoice pgtype.UUID
	}{
		{business.ID{}, paymentA, invoiceA}, {a, paymentB, invoiceA}, {a, paymentA, invoiceB}, {a, held, heldInvoice}, {a, paymentA, wrongInvoice}, {a, pgtype.UUID{Valid: true}, invoiceA}, {a, paymentA, pgtype.UUID{Valid: true}},
		{b, missingFXPayment, missingFXInvoice}, {a, badAmountPayment, badAmountInvoice},
		{a, badWHTPayment, badWHTInvoice}, {a, draftPayment, draftInvoice},
	} {
		if _, err := post(attempt.scope, attempt.payment, attempt.invoice); err == nil {
			t.Fatal("invalid posting succeeded")
		}
	}
	assertCount := func(table string, want int) {
		t.Helper()
		var got int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&got); err != nil || got != want {
			t.Fatalf("%s count = %d, want %d (%v)", table, got, want, err)
		}
	}
	assertCount("journal_entries", 0)
	entryA, err := post(a, paymentA, invoiceA)
	if err != nil {
		t.Fatal(err)
	}
	entryB, err := post(b, paymentB, invoiceB)
	if err != nil {
		t.Fatal(err)
	}
	assertLines := func(entry pgtype.UUID, want []string) {
		t.Helper()
		var got []string
		err := pool.QueryRow(ctx, `SELECT array_agg(concat_ws(':',a.code,l.currency,l.debit,l.credit,l.base_amount_debit,l.base_amount_credit,l.fx_rate) ORDER BY a.code)
			FROM journal_lines l JOIN chart_of_accounts a USING (business_id,account_id) WHERE l.entry_id=$1`, entry).Scan(&got)
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != len(want) {
			t.Fatalf("lines = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("lines = %v, want %v", got, want)
			}
		}
	}
	assertLines(entryA, []string{"1000:USD:8.0000:0.0000:16.0000:0.0000:2.00000000", "1100:USD:0.0000:10.0000:0.0000:20.0000:2.00000000", "1300:USD:2.0000:0.0000:4.0000:0.0000:2.00000000"})
	assertLines(entryB, []string{"1000:USD:8.0000:0.0000:40.0000:0.0000:5.00000000", "1100:SGD:0.0000:10.0000:0.0000:70.0000:7.00000000", "1300:SGD:2.0000:0.0000:14.0000:0.0000:7.00000000", "5900:IDR:16.0000:0.0000:16.0000:0.0000:1.00000000"})
	if _, err := post(a, paymentA, invoiceA); err == nil {
		t.Fatal("duplicate posting succeeded")
	}
	assertCount("journal_entries", 2)
	reverse := func(scope business.ID, original pgtype.UUID) (pgtype.UUID, error) {
		tx, err := pool.Begin(ctx)
		if err != nil {
			return pgtype.UUID{}, err
		}
		defer tx.Rollback(ctx)
		id, err := scopedledger.Reverse(ctx, tx, scope, original, "")
		if err != nil {
			return pgtype.UUID{}, err
		}
		return id, tx.Commit(ctx)
	}
	for _, original := range []pgtype.UUID{entryB, {Valid: true}} {
		if _, err = reverse(a, original); err == nil {
			t.Fatal("foreign or missing reversal succeeded")
		}
	}
	assertCount("journal_entries", 2)
	type result struct {
		id  pgtype.UUID
		err error
	}
	runTwo := func(call func() (pgtype.UUID, error)) (result, result) {
		t.Helper()
		start := make(chan struct{})
		done := make(chan result, 2)
		for range 2 {
			go func() {
				<-start
				id, err := call()
				done <- result{id, err}
			}()
		}
		close(start)
		return <-done, <-done
	}
	one, two := runTwo(func() (pgtype.UUID, error) { return reverse(a, entryA) })
	if one.err != nil || two.err != nil || one.id != two.id {
		t.Fatalf("concurrent reversal = %+v, %+v", one, two)
	}
	assertCount("journal_entries", 3)
	reversalA := one.id
	assertLines(reversalA, []string{"1000:USD:0.0000:8.0000:0.0000:16.0000:2.00000000", "1100:USD:10.0000:0.0000:20.0000:0.0000:2.00000000", "1300:USD:0.0000:2.0000:0.0000:4.0000:2.00000000"})
	concurrentInvoice, concurrentPayment := seed(a, "USD", "USD", "CLEARED")
	one, two = runTwo(func() (pgtype.UUID, error) { return post(a, concurrentPayment, concurrentInvoice) })
	if (one.err == nil) == (two.err == nil) {
		t.Fatalf("expected one concurrent posting, got %+v, %+v", one, two)
	}
	assertCount("journal_entries", 4)
	assertCount("journal_lines", 13)

	// Fail on the second or later line, after the entry and its first line have
	// already been written inside the savepoint.
	exec(`CREATE FUNCTION fail_ledger_line() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN
		IF EXISTS (SELECT 1 FROM journal_lines WHERE business_id=NEW.business_id AND entry_id=NEW.entry_id) THEN
			RAISE EXCEPTION 'injected ledger line failure';
		END IF;
		RETURN NEW;
	END; $$`)
	exec(`CREATE TRIGGER fail_ledger_line BEFORE INSERT ON journal_lines FOR EACH ROW EXECUTE FUNCTION fail_ledger_line()`)
	failedInvoice, failedPayment := seed(a, "USD", "USD", "CLEARED")
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = scopedledger.PostPayment(ctx, tx, a, failedPayment, failedInvoice, "IDR"); err == nil || !strings.Contains(err.Error(), "injected ledger line failure") {
		t.Fatalf("posting failure = %v", err)
	}
	if _, err = scopedledger.Reverse(ctx, tx, b, entryB, ""); err == nil || !strings.Contains(err.Error(), "injected ledger line failure") {
		t.Fatalf("reversal failure = %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	assertCount("journal_entries", 4)
	assertCount("journal_lines", 13)
	var link pgtype.UUID
	if err := pool.QueryRow(ctx, "SELECT reversed_by_entry_id FROM journal_entries WHERE entry_id=$1", entryB).Scan(&link); err != nil || link.Valid {
		t.Fatalf("failed reversal link = %v, %v", link, err)
	}
	exec("DROP TRIGGER fail_ledger_line ON journal_lines")
	snapshot, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Rollback(ctx)
	if _, err := scopedledger.PostPayment(ctx, snapshot, a, failedPayment, failedInvoice, "IDR"); err == nil || !strings.Contains(err.Error(), "requires read committed") {
		t.Fatalf("repeatable-read posting failure = %v", err)
	}
	assertCount("journal_entries", 4)
	for _, source := range []struct {
		scope            business.ID
		invoice, payment pgtype.UUID
	}{
		{a, invoiceA, paymentA}, {b, invoiceB, paymentB}, {a, failedInvoice, failedPayment},
	} {
		var invoiceStatus, paymentStatus, balance, booked, amount, withheld string
		err := pool.QueryRow(ctx, `SELECT i.status,p.status,i.balance_due::text,i.wht_booked::text,p.amount::text,p.wht_amount::text
			FROM invoices i JOIN payments p USING (business_id,invoice_id)
			WHERE i.business_id=$1 AND i.invoice_id=$2 AND p.payment_id=$3`, source.scope.UUID(), source.invoice, source.payment).
			Scan(&invoiceStatus, &paymentStatus, &balance, &booked, &amount, &withheld)
		if err != nil || invoiceStatus != "OPEN" || paymentStatus != "CLEARED" || balance != "10.0000" || booked != "0.0000" || amount != "8.0000" || withheld != "2.0000" {
			t.Fatalf("source rows changed: %q %q %s %s %s %s (%v)", invoiceStatus, paymentStatus, balance, booked, amount, withheld, err)
		}
	}
	whtInvoice, whtPayment := seed(a, "USD", "USD", "CLEARED")
	exec("UPDATE payments SET amount=0 WHERE business_id=$1 AND payment_id=$2", a.UUID(), whtPayment)
	whtEntry, err := post(a, whtPayment, whtInvoice)
	if err != nil {
		t.Fatal(err)
	}
	assertLines(whtEntry, []string{"1100:USD:0.0000:2.0000:0.0000:4.0000:2.00000000", "1300:USD:2.0000:0.0000:4.0000:0.0000:2.00000000"})

	overpaidInvoice, overpaidPayment := seed(a, "USD", "USD", "CLEARED")
	exec("UPDATE payments SET amount=8.0001 WHERE business_id=$1 AND payment_id=$2", a.UUID(), overpaidPayment)
	if _, err := post(a, overpaidPayment, overpaidInvoice); err == nil {
		t.Fatal("overpayment succeeded")
	}
	crossWHTInvoice, crossWHTPayment := seed(a, "USD", "SGD", "CLEARED")
	exec("UPDATE invoices SET balance_due=1 WHERE business_id=$1 AND invoice_id=$2", a.UUID(), crossWHTInvoice)
	if _, err := post(a, crossWHTPayment, crossWHTInvoice); err == nil {
		t.Fatal("excess cross-currency withholding succeeded")
	}
	exec("ALTER TABLE invoices DROP CONSTRAINT invoices_customer_id_fkey")
	foreignCustomerInvoice, foreignCustomerPayment := seed(a, "USD", "USD", "CLEARED")
	var foreignCustomer pgtype.UUID
	if err := pool.QueryRow(ctx, "INSERT INTO customers(business_id,customer_id,name) VALUES ($1,gen_random_uuid(),'foreign') RETURNING customer_id", b.UUID()).Scan(&foreignCustomer); err != nil {
		t.Fatal(err)
	}
	for _, customer := range []pgtype.UUID{foreignCustomer, {Valid: true}} {
		exec("UPDATE invoices SET customer_id=$3 WHERE business_id=$1 AND invoice_id=$2", a.UUID(), foreignCustomerInvoice, customer)
		if _, err := post(a, foreignCustomerPayment, foreignCustomerInvoice); err == nil {
			t.Fatal("invalid source customer accepted")
		}
	}

	// Reversing a reversal is supported. Retrying the original must still
	// return its immediate reversal, even after that reversal has its own link.
	reversalB, err := reverse(b, entryB)
	if err != nil {
		t.Fatal(err)
	}
	secondReversalB, err := reverse(b, reversalB)
	if err != nil || !secondReversalB.Valid || secondReversalB == entryB {
		t.Fatalf("second reversal = %v, %v", secondReversalB, err)
	}
	retriedB, err := reverse(b, entryB)
	if err != nil || retriedB != reversalB {
		t.Fatalf("retry after chained reversal = %v, %v", retriedB, err)
	}

	// Bypass the constraints in this disposable database to test the guards
	// against malformed historical rows without broadening any service query.
	exec("ALTER TABLE journal_entries DROP CONSTRAINT journal_entries_reversed_by_entry_id_fkey")
	exec("ALTER TABLE journal_lines DROP CONSTRAINT journal_lines_account_id_fkey")
	exec("ALTER TABLE journal_lines DROP CONSTRAINT journal_lines_entry_id_fkey")
	exec("ALTER TABLE journal_lines DROP CONSTRAINT ck_journal_lines_one_sided")
	journalSnapshot := func() string {
		t.Helper()
		var snapshot string
		err := pool.QueryRow(ctx, `SELECT jsonb_build_array(
			(SELECT jsonb_agg(to_jsonb(e) ORDER BY entry_id) FROM journal_entries e),
			(SELECT jsonb_agg(to_jsonb(l) ORDER BY line_id) FROM journal_lines l))::text`).Scan(&snapshot)
		if err != nil {
			t.Fatal(err)
		}
		return snapshot
	}
	rejectReversal := func(entry pgtype.UUID, message string) {
		t.Helper()
		before := journalSnapshot()
		if _, err := reverse(a, entry); err == nil {
			t.Fatal(message)
		}
		if journalSnapshot() != before {
			t.Fatal("rejected reversal changed journal state")
		}
	}
	for _, linked := range []pgtype.UUID{entryB, entryA, whtEntry, {Valid: true}} {
		exec("UPDATE journal_entries SET reversed_by_entry_id=$3 WHERE business_id=$1 AND entry_id=$2", a.UUID(), entryA, linked)
		rejectReversal(entryA, "malformed reversal link accepted")
	}
	exec("UPDATE journal_entries SET reversed_by_entry_id=$3 WHERE business_id=$1 AND entry_id=$2", a.UUID(), entryA, reversalA)
	exec("UPDATE journal_entries SET reversed_by_entry_id=$3 WHERE business_id=$1 AND entry_id=$2", a.UUID(), reversalA, entryA)
	rejectReversal(entryA, "reciprocal reversal link accepted")
	exec("UPDATE journal_entries SET reversed_by_entry_id=NULL WHERE business_id=$1 AND entry_id=$2", a.UUID(), reversalA)
	exec("UPDATE journal_lines SET base_amount_credit=base_amount_credit+1 WHERE business_id=$1 AND entry_id=$2 AND credit>0", a.UUID(), reversalA)
	rejectReversal(entryA, "non-mirroring reversal accepted")
	exec("UPDATE journal_entries SET reversed_by_entry_id=NULL WHERE business_id=$1 AND entry_id=$2", a.UUID(), entryA)
	exec("UPDATE journal_lines SET fx_rate=0 WHERE business_id=$1 AND entry_id=$2", a.UUID(), entryA)
	rejectReversal(entryA, "invalid journal rate accepted")
	exec("UPDATE journal_lines SET fx_rate=2,currency='12' WHERE business_id=$1 AND entry_id=$2", a.UUID(), entryA)
	rejectReversal(entryA, "invalid journal currency accepted")
	exec("UPDATE journal_lines SET currency='USD' WHERE business_id=$1 AND entry_id=$2", a.UUID(), entryA)
	exec("UPDATE journal_lines SET debit=credit WHERE business_id=$1 AND entry_id=$2 AND credit>0", a.UUID(), entryA)
	rejectReversal(entryA, "invalid journal sides accepted")
	exec("UPDATE journal_lines SET debit=0,base_amount_debit=1 WHERE business_id=$1 AND entry_id=$2 AND credit>0", a.UUID(), entryA)
	rejectReversal(entryA, "invalid journal base sides accepted")

	exec("UPDATE journal_lines SET account_id=(SELECT account_id FROM chart_of_accounts WHERE business_id=$3 AND code='1300') WHERE business_id=$1 AND entry_id=$2 AND debit>0", a.UUID(), whtEntry, b.UUID())
	rejectReversal(whtEntry, "foreign journal account accepted")

	// An entry with only foreign lines cannot be reversed into an empty entry.
	exec("UPDATE journal_lines SET business_id=$3 WHERE business_id=$1 AND entry_id=$2", a.UUID(), whtEntry, b.UUID())
	rejectReversal(whtEntry, "entry without owned lines accepted")
	assertCount("journal_entries", 7)
	assertCount("journal_lines", 23)

}
