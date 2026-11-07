package migrations

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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

func testScopedPaymentIntake(t *testing.T, pool *pgxpool.Pool, a, b string) {
	t.Helper()
	const webhook = "/api/v1/payments/intake/webhook"
	const email = "/api/v1/payments/intake/email"
	const secret = "isolated-webhook-secret"
	const token = "isolated-email-secret"
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	config := platform.SessionConfig{
		Secret: []byte(strings.Repeat("s", 32)), Path: "/", SameSite: http.SameSiteLaxMode,
		PaymentIntake: platform.PaymentIntakeConfig{WebhookSecret: secret, EmailToken: token},
	}
	makeRouter := func(biz string, cfg platform.SessionConfig) http.Handler {
		t.Helper()
		scope, err := business.Parse(biz)
		if err != nil {
			t.Fatal(err)
		}
		router, err := platform.NewScopedProfileRouter(pool, cfg, scope)
		if err != nil {
			t.Fatal(err)
		}
		return router
	}
	left, right := makeRouter(a, config), makeRouter(b, config)
	sign := func(body string) string {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(body))
		return fmt.Sprintf("%x", mac.Sum(nil))
	}
	request := func(h http.Handler, path, body, credential string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Business-ID", b)
		if credential != "" {
			if path == webhook {
				r.Header.Set("X-Signature", credential)
			} else {
				r.Header.Set("X-Email-Token", credential)
			}
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	expect := func(w *httptest.ResponseRecorder, code int, body string) {
		t.Helper()
		if w.Code != code || !strings.Contains(w.Body.String(), body) {
			t.Fatalf("want %d %q, got %d %s", code, body, w.Code, w.Body)
		}
	}
	body := fmt.Sprintf(`{"external_ref":"intake-http-shared","amount":"20.1234","currency":"IDR","payer_name":"Customer","payment_date":"2026-01-01","business_id":%q,"invoice_id":"00000000-0000-0000-0000-000000000002","customer_id":"00000000-0000-0000-0000-000000000002"}`, b)
	expect(request(left, webhook, body, ""), 400, "missing")
	expect(request(left, webhook, body, "invalid"), 401, "signature")
	expect(request(left, webhook, "{", sign("{")), 400, "invalid")
	expect(request(left, webhook, body, sign(body+" ")), 401, "signature")
	expect(request(left, email, `{}`, ""), 401, "token")
	expect(request(left, email, `{"text":"Amount: USD 20.00","attachments":[{"content":"proof"}]}`, token), 400, "invalid")
	expect(request(left, email, `{"text":"Amount: USD 20.00","text":"Amount: USD 30.00"}`, token), 400, "invalid")
	expect(request(left, webhook, body, sign(body)), 201, `"invoice_id":null`)
	expect(request(right, webhook, body, sign(body)), 201, `"customer_id":null`)
	expect(request(left, webhook, body, sign(body)), 409, "duplicate")
	mail := `{"from":"payer@example.test","text":"Amount: USD 20.00","message_id":"intake-http-shared","payment_date":"2026-01-01"}`
	expect(request(left, email, mail, token), 201, `"intake_source":"EMAIL_PARSER"`)
	expect(request(left, email, mail, token), 409, "duplicate")
	disabledConfig := config
	disabledConfig.PaymentIntake = platform.PaymentIntakeConfig{}
	disabled := makeRouter(a, disabledConfig)
	expect(request(disabled, webhook, body, sign(body)), 401, "signature")
	expect(request(disabled, email, mail, token), 401, "token")
	big := strings.Repeat(" ", 1<<20+1)
	expect(request(left, webhook, big, sign(big)), 413, "too large")

	withProof := `{"from":"payer@example.test","attachments":[{"content":"encoded-proof"}],"message_id":"intake-http-ocr","payment_date":"2026-01-01"}`
	ocrConfig := config
	var ocrCalls int
	ocrConfig.PaymentIntake.OCR = func(ctx context.Context, input []byte, limit int) (string, error) {
		ocrCalls++
		if string(input) != "encoded-proof" || limit < 20 || limit >= 1<<20 || ctx.Err() != nil {
			t.Fatalf("wrong OCR input or bound: %q, %d, %v", input, limit, ctx.Err())
		}
		if ocrCalls == 1 {
			return "", errors.New("local fake failed")
		}
		return "Amount: USD 20.00", nil
	}
	ocrRouter := makeRouter(a, ocrConfig)
	expect(request(ocrRouter, email, withProof, "wrong"), 401, "token")
	if ocrCalls != 0 {
		t.Fatal("unauthenticated request reached OCR")
	}
	expect(request(ocrRouter, email, withProof, token), 503, "unavailable")
	expect(request(ocrRouter, email, withProof, token), 201, "PENDING_MANUAL_REVIEW")

	// Seed one plausible exact match, then introduce a second invoice.
	for i, scenario := range []struct{ amount, currency string }{
		{"20.1234", "IDR"}, {"20.1234", "IDR"}, {"20.1233", "IDR"}, {"20.1234", "USD"},
	} {
		if i < 2 {
			if _, err := pool.Exec(ctx, `INSERT INTO invoices(business_id,invoice_id,customer_id,invoice_type,currency,amount,balance_due,status,issue_date,due_date)
				SELECT business_id,gen_random_uuid(),customer_id,'MILESTONE','IDR',20.1234,20.1234,'OPEN','2026-01-01','2026-01-01'
				FROM customers WHERE business_id=$1 AND name='Customer'`, a); err != nil {
				t.Fatal(err)
			}
		}
		payload := fmt.Sprintf(`{"external_ref":"intake-match-%d","amount":%q,"currency":%q,"payer_name":"Customer","payment_date":"2026-01-01"}`, i, scenario.amount, scenario.currency)
		expect(request(left, webhook, payload, sign(payload)), 201, "PENDING_MANUAL_REVIEW")
	}

	raceBody := strings.ReplaceAll(body, "intake-http-shared", "intake-http-race")
	start := make(chan struct{})
	results := make(chan *httptest.ResponseRecorder, 2)
	for range 2 {
		go func() { <-start; results <- request(left, webhook, raceBody, sign(raceBody)) }()
	}
	close(start)
	first, second := <-results, <-results
	if first.Code > second.Code {
		first, second = second, first
	}
	expect(first, 201, "PENDING_MANUAL_REVIEW")
	expect(second, 409, "duplicate")
	check := func(query string, want int) {
		t.Helper()
		var got int
		if err := pool.QueryRow(ctx, query, a, b).Scan(&got); err != nil || got != want {
			t.Fatalf("want %d, got %d: %v", want, got, err)
		}
	}
	check(`SELECT count(*) FROM payments p JOIN reconciliation_log l USING(business_id,payment_id)
		WHERE p.business_id IN ($1,$2) AND p.external_ref LIKE 'intake-http-%'
		AND p.status='PENDING_MANUAL_REVIEW' AND p.invoice_id IS NULL AND p.customer_id IS NULL
		AND l.action='HELD' AND l.actor_user_id IS NULL`, 5)
	check(`SELECT count(*) FROM payments p JOIN journal_entries j ON j.business_id=p.business_id AND j.source_id=p.payment_id
		WHERE p.business_id IN ($1,$2) AND (p.external_ref LIKE 'intake-http-%' OR p.external_ref LIKE 'intake-match-%')`, 0)
	check(`SELECT count(*) FROM invoices WHERE business_id IN ($1,$2) AND issue_date='2026-01-01' AND currency='IDR' AND amount=20.1234 AND balance_due=20.1234 AND status='OPEN'`, 2)
	if _, err := pool.Exec(ctx, `ALTER TABLE reconciliation_log ADD CONSTRAINT intake_http_reject_held CHECK(action <> 'HELD') NOT VALID`); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := pool.Exec(ctx, `ALTER TABLE reconciliation_log DROP CONSTRAINT IF EXISTS intake_http_reject_held`); err != nil {
			t.Error(err)
		}
	}()
	late := strings.ReplaceAll(body, "intake-http-shared", "intake-http-late")
	expect(request(left, webhook, late, sign(late)), 500, "internal server error")
	check(`SELECT count(*) FROM payments WHERE business_id IN ($1,$2) AND external_ref='intake-http-late'`, 0)
	if _, err := pool.Exec(ctx, `ALTER TABLE reconciliation_log DROP CONSTRAINT intake_http_reject_held`); err != nil {
		t.Fatal(err)
	}
	expect(request(left, webhook, late, sign(late)), 201, "PENDING_MANUAL_REVIEW")

	t.Run("Python", func(t *testing.T) {
		_, reference := freshAlembic(t)
		plain := `{"external_ref":"intake-parity","amount":"42.5000","currency":"USD","payer_name":"No matching customer","payment_date":"2026-01-01","payer_reference":"1234"}`
		cmd := exec.Command("python", "-c", `
import hashlib, hmac, json, os
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine, text
from sqlalchemy.orm import Session
from app.api.v1.routers.payments import router
from app.db.session import get_db

engine = create_engine(os.environ["REF_DB"].replace("postgres://", "postgresql+psycopg://", 1))
app = FastAPI()
app.include_router(router, prefix="/api/v1")
def session():
    with Session(engine) as db:
        yield db
app.dependency_overrides[get_db] = session

def signed(body):
    return {"X-Signature": hmac.new(os.environ["WEBHOOK_HMAC_SECRET"].encode(),body.encode(),hashlib.sha256).hexdigest()}

body = os.environ["INTAKE_BODY"]
webhook = "/api/v1/payments/intake/webhook"
email = "/api/v1/payments/intake/email"
with TestClient(app) as client:
    responses = [
        client.post(webhook,content=body),
        client.post(webhook,content=body,headers={"X-Signature":"invalid"}),
        client.post(webhook,content="{",headers=signed("{")),
        client.post(email,json={}),
        client.post(webhook,content=body,headers=signed(body)),
        client.post(webhook,content=body,headers=signed(body)),
    ]
    payment = responses[4].json()
    with engine.connect() as c:
        audit = c.scalar(text("SELECT action FROM reconciliation_log WHERE payment_id=:payment"), {"payment":payment["payment_id"]})
    print(json.dumps(dict(Status=[r.status_code for r in responses], Payment=payment, Audit=audit)))
`)
		cmd.Dir = "../../backend"
		cmd.Env = append(os.Environ(), "REF_DB="+reference, "INTAKE_BODY="+plain, "WEBHOOK_HMAC_SECRET="+secret)
		raw, err := cmd.Output()
		if err != nil {
			t.Fatalf("Python payment intake: %v, %s", err, raw)
		}
		var python struct {
			Status  []int
			Payment map[string]any
			Audit   string
		}
		if err := json.Unmarshal(raw, &python); err != nil {
			t.Fatal(err)
		}
		goResponses := []*httptest.ResponseRecorder{
			request(left, webhook, plain, ""), request(left, webhook, plain, "invalid"),
			request(left, webhook, "{", sign("{")), request(left, email, `{}`, ""),
			request(left, webhook, plain, sign(plain)), request(left, webhook, plain, sign(plain)),
		}
		for i, got := range goResponses {
			expect(got, python.Status[i], "")
		}
		var goPayment map[string]any
		if err := json.Unmarshal(goResponses[4].Body.Bytes(), &goPayment); err != nil {
			t.Fatal(err)
		}
		var goAudit string
		if err := pool.QueryRow(ctx, `SELECT action FROM reconciliation_log WHERE business_id=$1 AND payment_id=$2`, a, goPayment["payment_id"]).Scan(&goAudit); err != nil {
			t.Fatal(err)
		}
		if goAudit != python.Audit || python.Audit != "HELD" {
			t.Fatalf("audit action Go=%q Python=%q", goAudit, python.Audit)
		}
		if goPayment["adjustment_type"] != "NONE" || goPayment["confidence_score"] != nil ||
			python.Payment["adjustment_type"] != "CREDIT_ON_ACCOUNT" || python.Payment["confidence_score"] != "0.00" {
			t.Fatalf("unexpected classification Go=%v Python=%v", goPayment, python.Payment)
		}
		for _, key := range []string{"payment_id", "created_at", "confidence_score", "adjustment_type"} {
			delete(goPayment, key)
			delete(python.Payment, key)
		}
		if !reflect.DeepEqual(goPayment, python.Payment) {
			t.Fatalf("Go=%v Python=%v", goPayment, python.Payment)
		}
	})

}
