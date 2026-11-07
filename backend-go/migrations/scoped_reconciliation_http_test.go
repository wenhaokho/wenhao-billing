package migrations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wenhaokho/wenhao-billing/backend/internal/business"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

func testScopedReconciliation(t *testing.T, pool *pgxpool.Pool, a, b, aUser, bUser, good string,
	token func(string, string) string, request func(string, string, string, string) *httptest.ResponseRecorder,
) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	query := func(sql string, args ...any) string {
		t.Helper()
		var s string
		if err := pool.QueryRow(ctx, sql, args...).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	exec(`INSERT INTO fx_rates(business_id,from_currency,to_currency,rate,as_of_date,source)
		VALUES ($1,'USD','IDR',2,'2001-12-01','test'),($1,'SGD','IDR',3,'2001-12-01','test'),($2,'USD','IDR',5,'2001-12-01','test'),($2,'SGD','IDR',7,'2001-12-01','test')
		ON CONFLICT DO NOTHING`, a, b)
	exec(`INSERT INTO chart_of_accounts(business_id,code,name,type)
		SELECT $1,code,name,type FROM chart_of_accounts WHERE business_id=$2 ON CONFLICT DO NOTHING`, b, a)
	seed := func(scope, currency, amount string) (string, string) {
		t.Helper()
		invoice := query(`INSERT INTO invoices(business_id,invoice_id,invoice_type,currency,amount,balance_due,wht_amount,status)
			VALUES ($1,gen_random_uuid(),'MILESTONE','SGD',10,10,2,'OPEN') RETURNING invoice_id::text`, scope)
		payment := query(`INSERT INTO payments(business_id,payment_id,amount,currency,payer_name,payment_date,intake_source,status)
			VALUES ($1,gen_random_uuid(),$2::text::numeric,$3,'Payer','2001-12-03','WEBHOOK','PENDING_MANUAL_REVIEW') RETURNING payment_id::text`, scope, amount, currency)
		return invoice, payment
	}
	approve := func(p, i, session string) *httptest.ResponseRecorder {
		return request("POST", "/api/v1/payments/"+p+"/manual-review", session, fmt.Sprintf(`{"invoice_id":%q}`, i))
	}
	reverse := func(p, session string) *httptest.ResponseRecorder {
		return request("POST", "/api/v1/payments/"+p+"/reverse", session, `{"reason":"operator reversal"}`)
	}
	expect := func(w *httptest.ResponseRecorder, code int, body string) {
		t.Helper()
		if w.Code != code || !strings.Contains(w.Body.String(), body) {
			t.Fatalf("want %d %q, got %d %s", code, body, w.Code, w.Body)
		}
	}
	invoice, payment := seed(a, "USD", "7.1234")
	foreignInvoice, foreignPayment := seed(b, "USD", "8.2345")
	item := query(`INSERT INTO items(business_id,item_id,name,item_type,is_hosting)
		VALUES ($1,gen_random_uuid(),'Recon hosting','SERVICE',true) RETURNING item_id::text`, a)
	exec(`INSERT INTO invoice_line_items(business_id,line_item_id,invoice_id,item_id,position,description,quantity,unit_price,amount)
		VALUES ($1,gen_random_uuid(),$2,$3,0,'hosting',1,10,10)`, a, invoice, item)
	expect(approve(payment, invoice, ""), 401, "")
	expect(approve(payment, invoice, token(bUser, b)), 401, "")
	expect(approve(payment, invoice, token(bUser, a)), 403, "")
	expect(approve(payment, foreignInvoice, good), 400, "not found")
	expect(approve(foreignPayment, invoice, good), 400, "not found")
	expect(reverse(foreignPayment, good), 400, "not found")

	concurrent := func(first, second func() *httptest.ResponseRecorder) {
		t.Helper()
		start := make(chan struct{})
		results := make(chan *httptest.ResponseRecorder, 2)
		for _, fn := range []func() *httptest.ResponseRecorder{first, second} {
			go func() { <-start; results <- fn() }()
		}
		close(start)
		one, two := <-results, <-results
		if one.Code > two.Code {
			one, two = two, one
		}
		expect(one, 200, "")
		expect(two, 400, "")
	}
	// Different actors avoid hiding invoice/payment serialization behind the
	// transactional administrator lock.
	secondAdmin := query(`INSERT INTO users(business_id,user_id,email,password_hash,role)
		VALUES ($1,gen_random_uuid(),'recon-admin@example.test','unused','admin') RETURNING user_id::text`, a)
	other := token(secondAdmin, a)
	concurrent(func() *httptest.ResponseRecorder { return approve(payment, invoice, good) },
		func() *httptest.ResponseRecorder { return approve(payment, invoice, other) })
	state := func(biz, i, p string) string {
		t.Helper()
		return query(`SELECT concat_ws('/',i.status,i.balance_due,i.wht_booked,p.status,p.wht_amount,
			(SELECT count(*) FROM journal_entries j WHERE j.business_id=p.business_id AND j.source_id=p.payment_id),
			(SELECT count(*) FROM reconciliation_log r WHERE r.business_id=p.business_id AND r.payment_id=p.payment_id),
			(SELECT count(*) FROM hosting_restore_intents h WHERE h.business_id=i.business_id AND h.invoice_id=i.invoice_id))
			FROM invoices i JOIN payments p USING (business_id) WHERE i.business_id=$1 AND i.invoice_id=$2 AND p.payment_id=$3`, biz, i, p)
	}
	check := func(biz, i, p, want string) {
		t.Helper()
		if got := state(biz, i, p); got != want {
			t.Fatalf("state %s, want %s", got, want)
		}
	}
	check(a, invoice, payment, "PAID/0.0000/2.0000/CLEARED/2.0000/1/1/1")
	check(b, foreignInvoice, foreignPayment, "OPEN/10.0000/0.0000/PENDING_MANUAL_REVIEW/0.0000/0/0/0")
	exec(`UPDATE hosting_restore_intents SET lease_token=gen_random_uuid(),lease_until=clock_timestamp()-interval '1 minute'
		WHERE business_id=$1 AND invoice_id=$2`, a, invoice)
	expect(reverse(payment, good), 400, "claimed hosting")
	check(a, invoice, payment, "PAID/0.0000/2.0000/CLEARED/2.0000/1/1/1")
	exec(`UPDATE hosting_restore_intents SET lease_token=NULL,lease_until=NULL,next_attempt_at='2001-01-01' WHERE business_id=$1 AND invoice_id=$2`, a, invoice)
	exec(`UPDATE items SET hosting_status='SUSPENDED' WHERE business_id=$1 AND item_id=$2`, a, item)
	exec(`INSERT INTO cloudflare_targets(business_id,target_id,item_id,zone_id,record_id,record_name,record_type,live_content,maintenance_content,provider_status)
		VALUES ($1,gen_random_uuid(),$2,'recon-zone','recon-record','recon.example.test','A','192.0.2.1','192.0.2.2','SUSPENDED')`, a, item)
	scope, err := business.Parse(a)
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan platform.HostingOperation, 1)
	release := make(chan struct{})
	provider := reconciliationHostingFake(func(ctx context.Context, got business.ID, operation platform.HostingOperation) error {
		if got != scope || operation.RecordID != "recon-record" {
			t.Errorf("unexpected fake provider operation: %v %+v", got, operation)
			return errors.New("unexpected fake operation")
		}
		entered <- operation
		select {
		case <-release:
			return errors.New("uncertain fake restore")
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	finished := make(chan error, 1)
	go func() { finished <- platform.ConsumeScopedHostingRestoreIntents(ctx, pool, scope, provider) }()
	select {
	case op := <-entered:
		if op.Suspend {
			t.Fatal("expected restore operation")
		}
	case err := <-finished:
		t.Fatalf("hosting worker did not reach fake provider: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	expect(reverse(payment, good), 400, "unresolved hosting")
	close(release)
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if got := query(`SELECT concat_ws('/',d.lease_token IS NULL,l.repair_required)
		FROM hosting_restore_intents d JOIN hosting_operation_leases l USING (business_id,item_id)
		WHERE d.business_id=$1 AND d.invoice_id=$2 AND d.item_id=$3`, a, invoice, item); got != "t/t" {
		t.Fatalf("expected unclaimed intent and unresolved repair, got %s", got)
	}
	expect(reverse(payment, good), 400, "unresolved hosting")
	exec(`UPDATE hosting_operation_leases SET manual_review_required=true,repair_required=false WHERE business_id=$1 AND item_id=$2`, a, item)
	expect(reverse(payment, good), 400, "unresolved hosting")
	check(a, invoice, payment, "PAID/0.0000/2.0000/CLEARED/2.0000/1/1/1")
	// Simulate operator resolution of the provider uncertainty.
	exec(`DELETE FROM hosting_operation_leases WHERE business_id=$1 AND item_id=$2`, a, item)
	concurrent(func() *httptest.ResponseRecorder { return reverse(payment, good) },
		func() *httptest.ResponseRecorder { return reverse(payment, other) })
	check(a, invoice, payment, "OPEN/10.0000/0.0000/FLAGGED/2.0000/2/2/0")
	expect(approve(payment, invoice, good), 400, "FLAGGED")

	sameInvoice, samePayment := seed(a, "SGD", "8")
	expect(approve(samePayment, sameInvoice, good), 200, `"status":"CLEARED"`)
	// Reject the final write after the journal link and invoice update to
	// verify that reversal leaves the original transaction intact.
	exec(fmt.Sprintf(`ALTER TABLE reconciliation_log ADD CONSTRAINT reject_test_reversal CHECK (payment_id <> '%s' OR action <> 'REVERSED')`, samePayment))
	expect(reverse(samePayment, good), 500, "")
	check(a, sameInvoice, samePayment, "PAID/0.0000/2.0000/CLEARED/2.0000/1/1/0")
	if linked := query(`SELECT (reversed_by_entry_id IS NOT NULL)::text FROM journal_entries WHERE business_id=$1 AND source_id=$2`, a, samePayment); linked != "false" {
		t.Fatalf("failed reversal linked a journal: %s", linked)
	}
	exec(`ALTER TABLE reconciliation_log DROP CONSTRAINT reject_test_reversal`)
	expect(reverse(samePayment, good), 200, `"status":"FLAGGED"`)
	check(a, sameInvoice, samePayment, "OPEN/10.0000/0.0000/FLAGGED/2.0000/2/2/0")
	partialInvoice, partialPayment := seed(a, "SGD", "3.1234")
	expect(approve(partialPayment, partialInvoice, good), 200, "")
	check(a, partialInvoice, partialPayment, "PARTIAL/6.8766/0.0000/CLEARED/0.0000/1/1/0")
	exec(`UPDATE journal_entries SET memo=replace(memo,' [exact invoice relief]','') WHERE business_id=$1 AND source_id=$2`, a, partialPayment)
	expect(reverse(partialPayment, good), 400, "exact invoice relief")
	check(a, partialInvoice, partialPayment, "PARTIAL/6.8766/0.0000/CLEARED/0.0000/1/1/0")

	nearInvoice, nearPayment := seed(a, "SGD", "7.9999")
	expect(approve(nearPayment, nearInvoice, good), 200, "")
	check(a, nearInvoice, nearPayment, "PARTIAL/2.0001/0.0000/CLEARED/0.0000/1/1/0")
	expect(reverse(nearPayment, good), 200, "")
	check(a, nearInvoice, nearPayment, "OPEN/10.0000/0.0000/FLAGGED/0.0000/2/2/0")

	overInvoice, overPayment := seed(a, "SGD", "10.0001")
	expect(approve(overPayment, overInvoice, good), 400, "exceeds invoice balance")
	check(a, overInvoice, overPayment, "OPEN/10.0000/0.0000/PENDING_MANUAL_REVIEW/0.0000/0/0/0")
	fxInvoice, fxPayment := seed(a, "EUR", "8")
	expect(approve(fxPayment, fxInvoice, good), 500, "")
	check(a, fxInvoice, fxPayment, "OPEN/10.0000/0.0000/PENDING_MANUAL_REVIEW/0.0000/0/0/0")
	if count := query(`SELECT count(*)::text FROM reconciliation_log WHERE business_id=$1 AND payment_id=$2 AND actor_user_id IN ($3,$4)`, a, payment, aUser, secondAdmin); count != "2" {
		t.Fatalf("wrong audit ownership: %s", count)
	}
	t.Run("Python", func(t *testing.T) {
		_, reference := freshAlembic(t)
		for _, scenario := range []struct {
			name, currency, amount string
		}{
			{"same currency and withholding", "SGD", "8"},
			{"partial", "SGD", "3.1234"},
			{"cross currency", "USD", "7.1234"},
		} {
			i, p := seed(a, scenario.currency, scenario.amount)
			snapshot := func(w *httptest.ResponseRecorder) reconciliationSnapshot {
				t.Helper()
				expect(w, 200, "")
				var out reconciliationSnapshot
				if err := json.Unmarshal(w.Body.Bytes(), &out.Payment); err != nil {
					t.Fatal(err)
				}
				delete(out.Payment, "created_at")
				out.Invoice = query(`SELECT concat_ws('/',status,balance_due,wht_booked) FROM invoices WHERE business_id=$1 AND invoice_id=$2`, a, i)
				out.Journal = query(`SELECT coalesce(string_agg(line,',' ORDER BY line),'') FROM (
					SELECT concat_ws('/',j.reversed_by_entry_id IS NOT NULL,j.memo LIKE 'Reversal:%',
						ac.code,l.currency,l.debit,l.credit,l.base_amount_debit,l.base_amount_credit,l.fx_rate) AS line
					FROM journal_entries j JOIN journal_lines l USING (business_id,entry_id)
					JOIN chart_of_accounts ac USING (business_id,account_id)
					WHERE j.business_id=$1 AND j.source_type='PAYMENT' AND j.source_id=$2
				) lines`, a, p)
				out.Audit = query(`SELECT string_agg(concat_ws('/',action,array_to_string(reasons,','),actor_user_id),',' ORDER BY action)
					FROM reconciliation_log WHERE business_id=$1 AND payment_id=$2`, a, p)
				return out
			}
			goStates := []reconciliationSnapshot{snapshot(approve(p, i, good)), snapshot(reverse(p, good))}
			pythonStates := pythonReconciliation(t, reference, i, p, aUser, scenario.currency, scenario.amount)
			if scenario.currency == "USD" {
				if goStates[1].Invoice != "OPEN/10.0000/0.0000" || pythonStates[1].Invoice != "OPEN/9.1234/0.0000" {
					t.Fatalf("cross-currency reversal: Go %s, Python %s", goStates[1].Invoice, pythonStates[1].Invoice)
				}
				pythonStates[1].Invoice = goStates[1].Invoice
			}
			if !reflect.DeepEqual(goStates, pythonStates) {
				t.Fatalf("%s mismatch\nGo: %+v\nPython: %+v", scenario.name, goStates, pythonStates)
			}
		}
	})

}

type reconciliationSnapshot struct {
	Payment map[string]any
	Invoice string
	Journal string
	Audit   string
}

func pythonReconciliation(t *testing.T, url, invoice, payment, actor, currency, amount string) []reconciliationSnapshot {
	t.Helper()
	cmd := exec.Command("python", "-c", `
import json, os
from types import SimpleNamespace
from uuid import UUID
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine, text
from sqlalchemy.orm import Session
from app.api.deps import current_admin
from app.api.v1.routers.recon import router
from app.db.session import get_db

i, p, u, currency, amount = (os.environ[k] for k in ("INVOICE", "PAYMENT", "ACTOR", "PAYMENT_CCY", "PAYMENT_AMOUNT"))
engine = create_engine(os.environ["REF_DB"].replace("postgres://", "postgresql+psycopg://", 1))
args = dict(i=i, p=p, u=u, c=currency, a=amount)
with engine.begin() as c:
    c.execute(text("INSERT INTO users(user_id,email,password_hash,role) VALUES (:u,'recon@example.test','unused','admin') ON CONFLICT DO NOTHING"), args)
    c.execute(text("INSERT INTO invoices(invoice_id,invoice_type,currency,amount,balance_due,wht_amount,status) VALUES (:i,'MILESTONE','SGD',10,10,2,'OPEN')"), args)
    c.execute(text("""INSERT INTO payments(payment_id,amount,currency,payer_name,payment_date,intake_source,status)
        VALUES (:p,:a,:c,'Payer','2001-12-03','WEBHOOK','PENDING_MANUAL_REVIEW')"""), args)
    c.execute(text("""INSERT INTO fx_rates(from_currency,to_currency,rate,as_of_date,source)
        VALUES ('USD','IDR',2,'2001-12-01','test'), ('SGD','IDR',3,'2001-12-01','test') ON CONFLICT DO NOTHING"""))

app = FastAPI()
app.include_router(router, prefix="/api/v1")
app.dependency_overrides[current_admin] = lambda: SimpleNamespace(user_id=UUID(u))
def session():
    with Session(engine) as db:
        yield db
app.dependency_overrides[get_db] = session

def snapshot(response):
    response.raise_for_status()
    out = response.json()
    del out["created_at"]
    with engine.connect() as c:
        invoice = c.scalar(text("SELECT concat_ws('/',status,balance_due,wht_booked) FROM invoices WHERE invoice_id=:i"), args)
        journal = c.scalar(text("""SELECT coalesce(string_agg(line,',' ORDER BY line),'') FROM (
            SELECT concat_ws('/',j.reversed_by_entry_id IS NOT NULL,j.memo LIKE 'Reversal:%',
              ac.code,l.currency,l.debit,l.credit,l.base_amount_debit,l.base_amount_credit,l.fx_rate) AS line
            FROM journal_entries j JOIN journal_lines l USING (entry_id) JOIN chart_of_accounts ac USING (account_id)
            WHERE j.source_type='PAYMENT' AND j.source_id=:p
        ) lines"""), args)
        audit = c.scalar(text("""SELECT string_agg(concat_ws('/',action,array_to_string(reasons,','),actor_user_id),',' ORDER BY action)
            FROM reconciliation_log WHERE payment_id=:p"""), args)
    return dict(Payment=out, Invoice=invoice, Journal=journal, Audit=audit)

with TestClient(app) as client:
    root = "/api/v1/payments/" + p
    print(json.dumps([snapshot(client.post(root+"/manual-review", json={"invoice_id": i})),
                      snapshot(client.post(root+"/reverse", json={"reason": "operator reversal"}))]))
`)
	cmd.Dir = "../../backend"
	cmd.Env = append(os.Environ(), "REF_DB="+url, "INVOICE="+invoice, "PAYMENT="+payment, "ACTOR="+actor,
		"PAYMENT_CCY="+currency, "PAYMENT_AMOUNT="+amount, "BASE_CURRENCY=IDR", "SCHEDULER_ENABLED=false")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("Python reconciliation: %v\n%s", err, stderr.String())
	}
	var snapshots []reconciliationSnapshot
	if err := json.Unmarshal(out, &snapshots); err != nil || len(snapshots) != 2 {
		t.Fatalf("Python snapshots: %v\n%s", err, out)
	}
	return snapshots
}

type reconciliationHostingFake func(context.Context, business.ID, platform.HostingOperation) error

func (f reconciliationHostingFake) ApplyHosting(ctx context.Context, scope business.ID, operation platform.HostingOperation) error {
	return f(ctx, scope, operation)
}
