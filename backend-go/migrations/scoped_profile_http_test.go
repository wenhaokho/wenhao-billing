package migrations

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LOQATO/gokit/auth"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wenhaokho/wenhao-billing/backend/internal/business"
	"github.com/wenhaokho/wenhao-billing/backend/internal/email"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

type scopedTestSender struct{ messages []email.Message }

func (s *scopedTestSender) Send(_ context.Context, m email.Message) error {
	s.messages = append(s.messages, m)
	return nil
}

func testScopedProfileHTTP(t *testing.T, url string) {
	t.Helper()
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var a, b, aUser, bUser string
	var aID int
	if err = pool.QueryRow(ctx, `SELECT p.business_id::text,p.id,u.user_id::text FROM business_profile p JOIN users u USING(business_id) WHERE p.id<>1 LIMIT 1`).Scan(&a, &aID, &aUser); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT p.business_id::text,u.user_id::text FROM business_profile p JOIN users u USING(business_id) WHERE p.business_id<>$1 LIMIT 1`, a).Scan(&b, &bUser); err != nil {
		t.Fatal(err)
	}
	var bSnapshot string
	if err = pool.QueryRow(ctx, `SELECT jsonb_build_object('profile',(SELECT to_jsonb(p) FROM business_profile p WHERE business_id=$1),'accounts',(SELECT coalesce(jsonb_agg(to_jsonb(pa) ORDER BY id),'[]'::jsonb) FROM payment_account pa WHERE business_id=$1))::text`, b).Scan(&bSnapshot); err != nil {
		t.Fatal(err)
	}
	scope, err := business.Parse(a)
	if err != nil {
		t.Fatal(err)
	}
	secret := []byte("0123456789abcdef0123456789abcdef")
	if _, err = platform.NewScopedProfileRouter(pool, platform.SessionConfig{Secret: secret, Path: "/", SameSite: http.SameSiteLaxMode}, business.ID{}); err == nil {
		t.Fatal("zero business accepted")
	}
	sender := &scopedTestSender{}
	h, err := platform.NewScopedProfileRouter(pool, platform.SessionConfig{Secret: secret, Path: "/", SameSite: http.SameSiteLaxMode, Sender: sender}, scope)
	if err != nil {
		t.Fatal(err)
	}
	token := func(sub, company string) string {
		claims := jwt.MapClaims{"sub": sub}
		if company != "" {
			claims["business_id"] = company
		}
		value, e := (auth.Tokens{Secret: secret, AccessTTL: time.Hour}).Issue(claims)
		if e != nil {
			t.Fatal(e)
		}
		return value
	}
	request := func(method, path, tok, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, bytes.NewBufferString(body))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "session", Value: tok})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for _, bad := range []string{token(aUser, ""), token(aUser, b), token(bUser, a)} {
		if w := request("GET", "/api/v1/business-profile", bad, ""); w.Code != 401 && w.Code != 403 {
			t.Fatalf("foreign session accepted: %d %s", w.Code, w.Body.String())
		}
	}
	good := token(aUser, a)
	testScopedCustomerReports(t, pool, a, b, good, token(bUser, b), request)
	expired, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": aUser, "business_id": a, "exp": time.Now().Add(-time.Hour).Unix()}).SignedString(secret)
	if err != nil {
		t.Fatal(err)
	}
	badSignature, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": aUser, "business_id": a, "exp": time.Now().Add(time.Hour).Unix()}).SignedString([]byte("different-secret"))
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{expired, badSignature} {
		if w := request("GET", "/api/v1/business-profile", bad, ""); w.Code != 401 {
			t.Fatalf("bad token accepted: %d", w.Code)
		}
	}
	hashA, err := auth.Hash("alpha-password", auth.HashParams{Memory: 64, Iterations: 1, Parallelism: 1, SaltLen: 16, KeyLen: 32})
	if err != nil {
		t.Fatal(err)
	}
	hashB, err := auth.Hash("beta-password", auth.HashParams{Memory: 64, Iterations: 1, Parallelism: 1, SaltLen: 16, KeyLen: 32})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE users SET email='scope@example.com', password_hash=CASE WHEN business_id=$1 THEN $2 ELSE $3 END`, a, hashA, hashB); err != nil {
		t.Fatal(err)
	}
	if w := request("POST", "/api/v1/auth/login", "", `{"email":"scope@example.com","password":"beta-password"}`); w.Code != 401 {
		t.Fatalf("foreign password accepted: %d %s", w.Code, w.Body.String())
	}
	login := request("POST", "/api/v1/auth/login", "", `{"email":"scope@example.com","password":"alpha-password"}`)
	if login.Code != 200 || len(login.Result().Cookies()) == 0 {
		t.Fatalf("scoped login: %d %s", login.Code, login.Body.String())
	}
	if w := request("GET", "/api/v1/business-profile", login.Result().Cookies()[0].Value, ""); w.Code != 200 {
		t.Fatalf("issued session: %d %s", w.Code, w.Body.String())
	}
	if w := request("GET", "/api/v1/business-profile", good, ""); w.Code != 200 {
		t.Fatalf("profile GET: %d %s", w.Code, w.Body.String())
	}
	if w := request("GET", "/api/v1/business-profile/payment-accounts", good, ""); w.Code != 200 {
		t.Fatalf("accounts GET: %d %s", w.Code, w.Body.String())
	}
	var ownPayment, foreignPayment string
	if err = pool.QueryRow(ctx, `SELECT payment_id::text FROM payments WHERE business_id=$1 LIMIT 1`, a).Scan(&ownPayment); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT payment_id::text FROM payments WHERE business_id=$1 LIMIT 1`, b).Scan(&foreignPayment); err != nil {
		t.Fatal(err)
	}
	var invoiceA, invoiceB string
	if err = pool.QueryRow(ctx, `SELECT invoice_id::text FROM invoices WHERE business_id=$1 LIMIT 1`, a).Scan(&invoiceA); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT invoice_id::text FROM invoices WHERE business_id=$1 LIMIT 1`, b).Scan(&invoiceB); err != nil {
		t.Fatal(err)
	}
	if w := request("GET", "/api/v1/invoices/"+invoiceB, good, ""); w.Code != 404 {
		t.Fatalf("foreign invoice GET: %d %s", w.Code, w.Body.String())
	}
	invoiceDetail := request("GET", "/api/v1/invoices/"+invoiceA, good, "")
	var invoiceBody map[string]any
	if invoiceDetail.Code != 200 || json.Unmarshal(invoiceDetail.Body.Bytes(), &invoiceBody) != nil || strings.Contains(invoiceDetail.Body.String(), "business_id") {
		t.Fatalf("own invoice GET: %d %s", invoiceDetail.Code, invoiceDetail.Body.String())
	}
	var lineA, lineB string
	if err = pool.QueryRow(ctx, `SELECT line_item_id::text FROM invoice_line_items WHERE invoice_id=$1 LIMIT 1`, invoiceA).Scan(&lineA); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT line_item_id::text FROM invoice_line_items WHERE invoice_id=$1 LIMIT 1`, invoiceB).Scan(&lineB); err != nil {
		t.Fatal(err)
	}
	invoiceLines, ok := invoiceBody["line_items"].([]any)
	if !ok || len(invoiceLines) != 1 {
		t.Fatalf("invoice line shape: %v", invoiceBody["line_items"])
	}
	line, ok := invoiceLines[0].(map[string]any)
	if !ok || line["line_item_id"] != lineA || line["line_item_id"] == lineB || line["business_id"] != nil {
		t.Fatalf("invoice line isolation: %v", line)
	}
	invoiceList := request("GET", "/api/v1/invoices?limit=1", good, "")
	var invoiceRows []map[string]any
	if invoiceList.Code != 200 || json.Unmarshal(invoiceList.Body.Bytes(), &invoiceRows) != nil || len(invoiceRows) > 1 {
		t.Fatalf("invoice pagination: %d %s", invoiceList.Code, invoiceList.Body.String())
	}
	for _, i := range invoiceRows {
		if i["invoice_id"] == invoiceB {
			t.Fatal("foreign invoice in list")
		}
	}
	testDocumentSnapshot(t, url, a, aUser, invoiceA, "invoice")
	testDocumentSnapshot(t, url, a, aUser, invoiceA, "customer-summary")
	queueInvoices := request("GET", "/api/v1/invoices/awaiting-finalization", good, "")
	var queuedInvoices []map[string]any
	if queueInvoices.Code != 200 || json.Unmarshal(queueInvoices.Body.Bytes(), &queuedInvoices) != nil || len(queuedInvoices) != 1 || queuedInvoices[0]["invoice_id"] != invoiceA {
		t.Fatalf("invoice queue isolation: %d %s", queueInvoices.Code, queueInvoices.Body.String())
	}
	var templateA, templateB string
	if err = pool.QueryRow(ctx, `INSERT INTO invoices(business_id,invoice_id,customer_id,invoice_type,currency,amount,balance_due,status,is_template,invoice_number) VALUES($1,gen_random_uuid(),(SELECT customer_id FROM customers WHERE business_id=$1 LIMIT 1),'RECURRING','IDR',1,1,'DRAFT',true,'T-A') RETURNING invoice_id::text`, a).Scan(&templateA); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `INSERT INTO invoices(business_id,invoice_id,customer_id,invoice_type,currency,amount,balance_due,status,is_template,invoice_number) VALUES($1,gen_random_uuid(),(SELECT customer_id FROM customers WHERE business_id=$1 LIMIT 1),'RECURRING','IDR',1,1,'DRAFT',true,'T-B') RETURNING invoice_id::text`, b).Scan(&templateB); err != nil {
		t.Fatal(err)
	}
	templates := request("GET", "/api/v1/invoices/recurring-templates", good, "")
	var templateRows []map[string]any
	if templates.Code != 200 || json.Unmarshal(templates.Body.Bytes(), &templateRows) != nil || len(templateRows) != 1 || templateRows[0]["invoice_id"] != templateA {
		t.Fatalf("template isolation: %d %s", templates.Code, templates.Body.String())
	}
	if w := request("GET", "/api/v1/invoices/recurring-templates/"+templateB, good, ""); w.Code != 404 {
		t.Fatalf("foreign template detail: %d", w.Code)
	}
	testScopedRecurringTemplateRows(t, pool, a, b, templateA, good, token(bUser, b), token(bUser, a), request)
	var quotationA, quotationB string
	if err = pool.QueryRow(ctx, `SELECT quotation_id::text FROM quotations WHERE business_id=$1 LIMIT 1`, a).Scan(&quotationA); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT quotation_id::text FROM quotations WHERE business_id=$1 LIMIT 1`, b).Scan(&quotationB); err != nil {
		t.Fatal(err)
	}
	if w := request("GET", "/api/v1/quotations/"+quotationB, good, ""); w.Code != 404 {
		t.Fatalf("foreign quotation GET: %d %s", w.Code, w.Body.String())
	}
	testDocumentSnapshot(t, url, a, aUser, quotationA, "quotation")
	quote := request("GET", "/api/v1/quotations/"+quotationA, good, "")
	var quoteBody map[string]any
	if quote.Code != 200 || json.Unmarshal(quote.Body.Bytes(), &quoteBody) != nil || quoteBody["business_id"] != nil {
		t.Fatalf("own quotation GET: %d %s", quote.Code, quote.Body.String())
	}
	var quoteLineA, quoteLineB, quoteCustomerA, quoteCustomerB, quoteStatus string
	if err = pool.QueryRow(ctx, `SELECT line_item_id::text FROM quotation_line_items WHERE quotation_id=$1`, quotationA).Scan(&quoteLineA); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT line_item_id::text FROM quotation_line_items WHERE quotation_id=$1`, quotationB).Scan(&quoteLineB); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT customer_id::text,status FROM quotations WHERE quotation_id=$1`, quotationA).Scan(&quoteCustomerA, &quoteStatus); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT customer_id::text FROM quotations WHERE quotation_id=$1`, quotationB).Scan(&quoteCustomerB); err != nil {
		t.Fatal(err)
	}
	quoteLines, ok := quoteBody["line_items"].([]any)
	if !ok || len(quoteLines) != 1 {
		t.Fatalf("quotation line shape: %v", quoteBody["line_items"])
	}
	quoteLine, ok := quoteLines[0].(map[string]any)
	if !ok || quoteLine["line_item_id"] != quoteLineA || quoteLine["line_item_id"] == quoteLineB || quoteLine["business_id"] != nil {
		t.Fatalf("quotation line isolation: %v", quoteLine)
	}
	quoteList := request("GET", "/api/v1/quotations?limit=1", good, "")
	var quoteRows []map[string]any
	if quoteList.Code != 200 || json.Unmarshal(quoteList.Body.Bytes(), &quoteRows) != nil || len(quoteRows) != 1 || quoteRows[0]["quotation_id"] != quotationA {
		t.Fatalf("quotation pagination: %d %s", quoteList.Code, quoteList.Body.String())
	}
	quoteList = request("GET", "/api/v1/quotations?customer_id="+quoteCustomerA+"&status="+quoteStatus, good, "")
	if quoteList.Code != 200 || json.Unmarshal(quoteList.Body.Bytes(), &quoteRows) != nil || len(quoteRows) != 1 || quoteRows[0]["quotation_id"] != quotationA {
		t.Fatalf("quotation filters: %d %s", quoteList.Code, quoteList.Body.String())
	}
	quoteList = request("GET", "/api/v1/quotations?customer_id="+quoteCustomerB, good, "")
	if quoteList.Code != 200 || json.Unmarshal(quoteList.Body.Bytes(), &quoteRows) != nil || len(quoteRows) != 0 {
		t.Fatalf("foreign customer quotations: %d %s", quoteList.Code, quoteList.Body.String())
	}
	var billA, billB string
	if err = pool.QueryRow(ctx, `SELECT bill_id::text FROM bills WHERE business_id=$1 LIMIT 1`, a).Scan(&billA); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT bill_id::text FROM bills WHERE business_id=$1 LIMIT 1`, b).Scan(&billB); err != nil {
		t.Fatal(err)
	}
	if w := request("GET", "/api/v1/bills/"+billB, good, ""); w.Code != 404 {
		t.Fatalf("foreign bill GET: %d %s", w.Code, w.Body.String())
	}
	testDocumentSnapshot(t, url, a, aUser, billA, "bill")
	billDetail := request("GET", "/api/v1/bills/"+billA, good, "")
	var billBody map[string]any
	if billDetail.Code != 200 || json.Unmarshal(billDetail.Body.Bytes(), &billBody) != nil || billBody["business_id"] != nil {
		t.Fatalf("own bill GET: %d %s", billDetail.Code, billDetail.Body.String())
	}
	var billLineA, billLineB, billVendorA, billVendorB, billStatus string
	if err = pool.QueryRow(ctx, `SELECT line_item_id::text FROM bill_line_items WHERE bill_id=$1`, billA).Scan(&billLineA); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT line_item_id::text FROM bill_line_items WHERE bill_id=$1`, billB).Scan(&billLineB); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT vendor_id::text,status FROM bills WHERE bill_id=$1`, billA).Scan(&billVendorA, &billStatus); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT vendor_id::text FROM bills WHERE bill_id=$1`, billB).Scan(&billVendorB); err != nil {
		t.Fatal(err)
	}
	billLines, ok := billBody["line_items"].([]any)
	if !ok || len(billLines) != 1 {
		t.Fatalf("bill line shape: %v", billBody["line_items"])
	}
	billLine, ok := billLines[0].(map[string]any)
	if !ok || billLine["line_item_id"] != billLineA || billLine["line_item_id"] == billLineB || billLine["business_id"] != nil {
		t.Fatalf("bill line isolation: %v", billLine)
	}
	billList := request("GET", "/api/v1/bills", good, "")
	var billRows []map[string]any
	if billList.Code != 200 || json.Unmarshal(billList.Body.Bytes(), &billRows) != nil || len(billRows) != 1 || billRows[0]["bill_id"] != billA {
		t.Fatalf("bill list isolation: %d %s", billList.Code, billList.Body.String())
	}
	billList = request("GET", "/api/v1/bills?vendor_id="+billVendorA+"&status="+billStatus, good, "")
	if billList.Code != 200 || json.Unmarshal(billList.Body.Bytes(), &billRows) != nil || len(billRows) != 1 || billRows[0]["bill_id"] != billA {
		t.Fatalf("bill filters: %d %s", billList.Code, billList.Body.String())
	}
	billList = request("GET", "/api/v1/bills?vendor_id="+billVendorB, good, "")
	if billList.Code != 200 || json.Unmarshal(billList.Body.Bytes(), &billRows) != nil || len(billRows) != 0 {
		t.Fatalf("foreign vendor bills: %d %s", billList.Code, billList.Body.String())
	}
	billList = request("GET", "/api/v1/bills?status=NONMATCHING", good, "")
	if billList.Code != 200 || json.Unmarshal(billList.Body.Bytes(), &billRows) != nil || len(billRows) != 0 {
		t.Fatalf("nonmatching bill status: %d %s", billList.Code, billList.Body.String())
	}
	if w := request("GET", "/api/v1/payments/"+foreignPayment, good, ""); w.Code != 404 {
		t.Fatalf("foreign payment GET: %d %s", w.Code, w.Body.String())
	}
	foreignPay := request("GET", "/api/v1/payments/"+foreignPayment, good, "")
	missingPay := request("GET", "/api/v1/payments/00000000-0000-0000-0000-000000000099", good, "")
	if foreignPay.Code != 404 || foreignPay.Body.String() != missingPay.Body.String() {
		t.Fatalf("foreign/missing payment response mismatch: %s / %s", foreignPay.Body.String(), missingPay.Body.String())
	}
	pay := request("GET", "/api/v1/payments/"+ownPayment, good, "")
	var payBody map[string]any
	if pay.Code != 200 || json.Unmarshal(pay.Body.Bytes(), &payBody) != nil || payBody["business_id"] != nil {
		t.Fatalf("own payment GET: %d %s", pay.Code, pay.Body.String())
	}
	if payBody["amount"] != "20.0000" || payBody["wht_amount"] != "0.0000" {
		t.Fatalf("payment decimals: %v", payBody)
	}
	var fxB int64
	if err = pool.QueryRow(ctx, `INSERT INTO fx_rates(business_id,from_currency,to_currency,rate,as_of_date,source) VALUES($1,'EUR','IDR',123,'2024-02-01','MANUAL') RETURNING rate_id`, b).Scan(&fxB); err != nil {
		t.Fatal(err)
	}
	var fxBefore string
	if err = pool.QueryRow(ctx, `SELECT to_jsonb(f)::text FROM fx_rates f WHERE rate_id=$1`, fxB).Scan(&fxBefore); err != nil {
		t.Fatal(err)
	}
	if w := request("DELETE", fmt.Sprintf("/api/v1/accounting/fx-rates/%d", fxB), good, ""); w.Code != 404 {
		t.Fatalf("foreign FX delete: %d %s", w.Code, w.Body.String())
	}
	fxBody := `{"from_currency":"EUR","to_currency":"IDR","rate":"456.12345678","as_of_date":"2024-02-01","business_id":"` + b + `"}`
	fxCreate := request("POST", "/api/v1/accounting/fx-rates", good, fxBody)
	var fxCreated map[string]any
	if fxCreate.Code != 201 || json.Unmarshal(fxCreate.Body.Bytes(), &fxCreated) != nil || fxCreated["rate"] != "456.12345678" || fxCreated["business_id"] != nil {
		t.Fatalf("scoped FX create: %d %s", fxCreate.Code, fxCreate.Body.String())
	}
	if w := request("POST", "/api/v1/accounting/fx-rates", good, fxBody); w.Code != 409 {
		t.Fatalf("same-business FX duplicate: %d %s", w.Code, w.Body.String())
	}
	if w := request("POST", "/api/v1/accounting/fx-rates", good, `{"from_currency":"EUR","to_currency":"IDR","rate":"789.00000000","as_of_date":"2024-03-01"}`); w.Code != 201 {
		t.Fatalf("newer own FX: %d %s", w.Code, w.Body.String())
	}
	fxLatest := request("GET", "/api/v1/accounting/fx-rates/latest?from_currency=EUR&to_currency=IDR&on_date=2024-02-01", good, "")
	var fxLatestBody map[string]any
	if fxLatest.Code != 200 || json.Unmarshal(fxLatest.Body.Bytes(), &fxLatestBody) != nil || fxLatestBody["rate"] != "456.12345678" {
		t.Fatalf("scoped FX latest: %d %s", fxLatest.Code, fxLatest.Body.String())
	}
	fxLatest = request("GET", "/api/v1/accounting/fx-rates/latest?from_currency=EUR&to_currency=IDR", good, "")
	if fxLatest.Code != 200 || json.Unmarshal(fxLatest.Body.Bytes(), &fxLatestBody) != nil || fxLatestBody["rate"] != "789.00000000" {
		t.Fatalf("latest newer FX: %d %s", fxLatest.Code, fxLatest.Body.String())
	}
	var fxAfter string
	if err = pool.QueryRow(ctx, `SELECT to_jsonb(f)::text FROM fx_rates f WHERE rate_id=$1`, fxB).Scan(&fxAfter); err != nil || fxAfter != fxBefore {
		t.Fatalf("foreign FX changed: %v", err)
	}
	fxList := request("GET", "/api/v1/accounting/fx-rates?from_currency=EUR&to_currency=IDR", good, "")
	var fxRows []map[string]any
	if fxList.Code != 200 || json.Unmarshal(fxList.Body.Bytes(), &fxRows) != nil {
		t.Fatalf("scoped FX list: %d %s", fxList.Code, fxList.Body.String())
	}
	for _, row := range fxRows {
		if row["rate_id"] == float64(fxB) {
			t.Fatal("foreign FX row in list")
		}
	}
	fxID, ok := fxCreated["rate_id"].(float64)
	if !ok {
		t.Fatalf("missing FX rate ID: %v", fxCreated)
	}
	if w := request("DELETE", fmt.Sprintf("/api/v1/accounting/fx-rates/%.0f", fxID), good, ""); w.Code != 204 {
		t.Fatalf("own FX delete: %d %s", w.Code, w.Body.String())
	}
	var fxCount int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM fx_rates WHERE rate_id=$1`, int64(fxID)).Scan(&fxCount); err != nil || fxCount != 0 {
		t.Fatalf("FX delete not persisted: %d %v", fxCount, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE payments SET status='PENDING_MANUAL_REVIEW' WHERE payment_id=$1 OR payment_id=$2`, ownPayment, foreignPayment); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO payments(business_id,payment_id,amount,currency,payer_name,payment_date,intake_source,external_ref,status) VALUES($1,gen_random_uuid(),1,'IDR','other','2026-01-01','WEBHOOK','nonpending','PENDING')`, a); err != nil {
		t.Fatal(err)
	}
	queue := request("GET", "/api/v1/payments/awaiting-review", good, "")
	var queued []map[string]any
	if queue.Code != 200 || json.Unmarshal(queue.Body.Bytes(), &queued) != nil || len(queued) != 1 || queued[0]["payment_id"] != ownPayment {
		t.Fatalf("payment review isolation: %d %s", queue.Code, queue.Body.String())
	}
	var aAccount, bAccount int
	if err = pool.QueryRow(ctx, `SELECT account_id FROM chart_of_accounts WHERE business_id=$1 LIMIT 1`, a).Scan(&aAccount); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT account_id FROM chart_of_accounts WHERE business_id=$1 LIMIT 1`, b).Scan(&bAccount); err != nil {
		t.Fatal(err)
	}
	if w := request("GET", fmt.Sprintf("/api/v1/accounting/chart-of-accounts/%d", bAccount), good, ""); w.Code != 404 {
		t.Fatalf("foreign account GET: %d %s", w.Code, w.Body.String())
	}
	wAccount := request("GET", fmt.Sprintf("/api/v1/accounting/chart-of-accounts/%d", aAccount), good, "")
	var accountBody map[string]any
	if wAccount.Code != 200 || json.Unmarshal(wAccount.Body.Bytes(), &accountBody) != nil || accountBody["business_id"] != nil {
		t.Fatalf("own account GET: %d %s", wAccount.Code, wAccount.Body.String())
	}
	var distinctB int
	if err = pool.QueryRow(ctx, `INSERT INTO chart_of_accounts(business_id,code,name,type,active) VALUES($1,'ZZ123','B only','ASSET',true) RETURNING account_id`, b).Scan(&distinctB); err != nil {
		t.Fatal(err)
	}
	foreignPath := fmt.Sprintf("/api/v1/accounting/chart-of-accounts/%d", distinctB)
	if w := request("PATCH", foreignPath, good, `{"name":"foreign edit"}`); w.Code != 404 {
		t.Fatalf("foreign account PATCH: %d %s", w.Code, w.Body.String())
	}
	if w := request("DELETE", foreignPath, good, ""); w.Code != 404 {
		t.Fatalf("foreign account DELETE: %d %s", w.Code, w.Body.String())
	}
	createdAccount := request("POST", "/api/v1/accounting/chart-of-accounts", good, `{"code":"ZZ123","name":"A account","type":"ASSET"}`)
	if createdAccount.Code != 201 {
		t.Fatalf("cross-business duplicate account code: %d %s", createdAccount.Code, createdAccount.Body.String())
	}
	var createdBody struct {
		ID int `json:"account_id"`
	}
	if err = json.Unmarshal(createdAccount.Body.Bytes(), &createdBody); err != nil || createdBody.ID == 0 {
		t.Fatalf("created account: %v", err)
	}
	if w := request("POST", "/api/v1/accounting/chart-of-accounts", good, `{"code":"ZZ123","name":"duplicate","type":"ASSET"}`); w.Code != 409 {
		t.Fatalf("same-business duplicate: %d", w.Code)
	}
	if w := request("POST", "/api/v1/accounting/chart-of-accounts", good, `{"code":"ZZ124","name":"invalid","type":"ASSET|LIABILITY"}`); w.Code != 422 {
		t.Fatalf("invalid enum: %d", w.Code)
	}
	list := request("GET", "/api/v1/accounting/chart-of-accounts?active=true", good, "")
	var accounts []map[string]any
	if list.Code != 200 || json.Unmarshal(list.Body.Bytes(), &accounts) != nil {
		t.Fatalf("account list: %d %s", list.Code, list.Body.String())
	}
	found := false
	for _, account := range accounts {
		if int(account["account_id"].(float64)) == distinctB {
			t.Fatal("foreign account in list")
		}
		if int(account["account_id"].(float64)) == createdBody.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("created active account absent from list")
	}
	ownPath := fmt.Sprintf("/api/v1/accounting/chart-of-accounts/%d", createdBody.ID)
	if w := request("PATCH", ownPath, good, `{"name":"updated"}`); w.Code != 200 {
		t.Fatalf("own PATCH: %d %s", w.Code, w.Body.String())
	}
	updatedAccount := request("GET", ownPath, good, "")
	if json.Unmarshal(updatedAccount.Body.Bytes(), &accountBody) != nil || accountBody["name"] != "updated" {
		t.Fatalf("account patch not persisted: %s", updatedAccount.Body.String())
	}
	if w := request("DELETE", ownPath, good, ""); w.Code != 200 {
		t.Fatalf("own DELETE: %d %s", w.Code, w.Body.String())
	}
	deactivated := request("GET", ownPath, good, "")
	if json.Unmarshal(deactivated.Body.Bytes(), &accountBody) != nil || accountBody["active"] != false {
		t.Fatalf("account deactivation not persisted: %s", deactivated.Body.String())
	}
	list = request("GET", "/api/v1/accounting/chart-of-accounts?active=true", good, "")
	if list.Code != 200 || json.Unmarshal(list.Body.Bytes(), &accounts) != nil {
		t.Fatalf("active account list: %d %s", list.Code, list.Body.String())
	}
	for _, account := range accounts {
		if int(account["account_id"].(float64)) == createdBody.ID {
			t.Fatal("inactive account present in active filter")
		}
	}
	list = request("GET", "/api/v1/accounting/chart-of-accounts?active=false", good, "")
	if list.Code != 200 || json.Unmarshal(list.Body.Bytes(), &accounts) != nil {
		t.Fatalf("inactive account list: %d %s", list.Code, list.Body.String())
	}
	found = false
	for _, account := range accounts {
		if int(account["account_id"].(float64)) == createdBody.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("deactivated account absent from inactive filter")
	}
	var bName string
	var bActive bool
	if err = pool.QueryRow(ctx, `SELECT name,active FROM chart_of_accounts WHERE account_id=$1`, distinctB).Scan(&bName, &bActive); err != nil || bName != "B only" || !bActive {
		t.Fatalf("foreign account mutated: %q %v %v", bName, bActive, err)
	}
	if w := request("PUT", "/api/v1/business-profile", good, `{"name":"HTTP A","business_id":"`+b+`"}`); w.Code != 200 {
		t.Fatalf("profile PUT: %d %s", w.Code, w.Body.String())
	}
	var name string
	if err = pool.QueryRow(ctx, `SELECT name FROM business_profile WHERE business_id=$1`, a).Scan(&name); err != nil || name != "HTTP A" || aID == 1 {
		t.Fatalf("profile id=%d name=%q: %v", aID, name, err)
	}
	if w := request("PUT", "/api/v1/business-profile", good, `{"name":42}`); w.Code != 422 {
		t.Fatalf("invalid profile: %d %s", w.Code, w.Body.String())
	}
	if err = pool.QueryRow(ctx, `SELECT name FROM business_profile WHERE business_id=$1`, a).Scan(&name); err != nil || name != "HTTP A" {
		t.Fatalf("invalid profile changed data: %q %v", name, err)
	}
	var bCount int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM payment_account WHERE business_id=$1`, b).Scan(&bCount); err != nil {
		t.Fatal(err)
	}
	var before string
	if err = pool.QueryRow(ctx, `SELECT jsonb_agg(to_jsonb(p) ORDER BY id)::text FROM payment_account p WHERE business_id=$1`, a).Scan(&before); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{`[{"currency":"IDR"}]`, `[{"currency":"IDR","instructions":null}]`} {
		if w := request("PUT", "/api/v1/business-profile/payment-accounts", good, invalid); w.Code != 422 {
			t.Fatalf("invalid accounts: %d %s", w.Code, w.Body.String())
		}
		var afterInvalid string
		if err = pool.QueryRow(ctx, `SELECT jsonb_agg(to_jsonb(p) ORDER BY id)::text FROM payment_account p WHERE business_id=$1`, a).Scan(&afterInvalid); err != nil || afterInvalid != before {
			t.Fatalf("invalid accounts changed data: %v", err)
		}
	}
	if w := request("PUT", "/api/v1/business-profile/payment-accounts", good, `[{"currency":"IDR","instructions":"A bank","is_default":true}]`); w.Code != 200 {
		t.Fatalf("accounts PUT: %d %s", w.Code, w.Body.String())
	}
	var after int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM payment_account WHERE business_id=$1`, b).Scan(&after); err != nil || after != bCount {
		t.Fatalf("B accounts changed: %d -> %d: %v", bCount, after, err)
	}
	var bAfter string
	if err = pool.QueryRow(ctx, `SELECT jsonb_build_object('profile',(SELECT to_jsonb(p) FROM business_profile p WHERE business_id=$1),'accounts',(SELECT coalesce(jsonb_agg(to_jsonb(pa) ORDER BY id),'[]'::jsonb) FROM payment_account pa WHERE business_id=$1))::text`, b).Scan(&bAfter); err != nil || bAfter != bSnapshot {
		t.Fatalf("B profile or accounts changed: %v", err)
	}
	var vendorA, vendorB string
	if err = pool.QueryRow(ctx, `SELECT vendor_id::text FROM vendors WHERE business_id=$1 LIMIT 1`, a).Scan(&vendorA); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT vendor_id::text FROM vendors WHERE business_id=$1 LIMIT 1`, b).Scan(&vendorB); err != nil {
		t.Fatal(err)
	}
	var vendorBefore string
	if err = pool.QueryRow(ctx, `SELECT to_jsonb(v)::text FROM vendors v WHERE vendor_id=$1`, vendorB).Scan(&vendorBefore); err != nil {
		t.Fatal(err)
	}
	if w := request("GET", "/api/v1/vendors/"+vendorB, good, ""); w.Code != 404 {
		t.Fatalf("foreign vendor GET: %d %s", w.Code, w.Body.String())
	}
	if w := request("DELETE", "/api/v1/vendors/"+vendorB, good, ""); w.Code != 404 {
		t.Fatalf("foreign vendor DELETE: %d %s", w.Code, w.Body.String())
	}
	if w := request("PATCH", "/api/v1/vendors/"+vendorB, good, `{"name":"foreign mutation"}`); w.Code != 404 {
		t.Fatalf("foreign vendor PATCH: %d %s", w.Code, w.Body.String())
	}
	createdVendor := request("POST", "/api/v1/vendors", good, `{"name":"Created A","business_id":"`+b+`"}`)
	var vendorBody map[string]any
	if createdVendor.Code != 201 || json.Unmarshal(createdVendor.Body.Bytes(), &vendorBody) != nil || vendorBody["business_id"] != nil {
		t.Fatalf("create vendor: %d %s", createdVendor.Code, createdVendor.Body.String())
	}
	createdVendorID, ok := vendorBody["vendor_id"].(string)
	if !ok {
		t.Fatal("missing vendor id")
	}
	var vendorBusiness string
	if err = pool.QueryRow(ctx, `SELECT business_id::text FROM vendors WHERE vendor_id=$1`, createdVendorID).Scan(&vendorBusiness); err != nil || vendorBusiness != a {
		t.Fatalf("spoofed vendor scope: %q %v", vendorBusiness, err)
	}
	if w := request("PATCH", "/api/v1/vendors/"+createdVendorID, good, `{"name":"Updated A"}`); w.Code != 200 {
		t.Fatalf("own vendor PATCH: %d %s", w.Code, w.Body.String())
	}
	var vendorName string
	var vendorActive bool
	if err = pool.QueryRow(ctx, `SELECT name,active FROM vendors WHERE vendor_id=$1`, createdVendorID).Scan(&vendorName, &vendorActive); err != nil || vendorName != "Updated A" || !vendorActive {
		t.Fatalf("vendor PATCH not persisted: %q %v %v", vendorName, vendorActive, err)
	}
	vendorList := request("GET", "/api/v1/vendors?active=true", good, "")
	var vendorRows []map[string]any
	if vendorList.Code != 200 || json.Unmarshal(vendorList.Body.Bytes(), &vendorRows) != nil {
		t.Fatalf("vendor list: %d %s", vendorList.Code, vendorList.Body.String())
	}
	vendorFound := false
	for _, v := range vendorRows {
		if v["vendor_id"] == vendorB {
			t.Fatal("foreign vendor in list")
		}
		if v["vendor_id"] == createdVendorID {
			vendorFound = true
		}
	}
	if !vendorFound {
		t.Fatal("own active vendor absent from list")
	}
	if w := request("DELETE", "/api/v1/vendors/"+createdVendorID, good, ""); w.Code != 200 {
		t.Fatalf("own vendor DELETE: %d %s", w.Code, w.Body.String())
	}
	if err = pool.QueryRow(ctx, `SELECT active FROM vendors WHERE vendor_id=$1`, createdVendorID).Scan(&vendorActive); err != nil || vendorActive {
		t.Fatalf("vendor DELETE not persisted: %v %v", vendorActive, err)
	}
	vendorList = request("GET", "/api/v1/vendors?active=true", good, "")
	if vendorList.Code != 200 || json.Unmarshal(vendorList.Body.Bytes(), &vendorRows) != nil {
		t.Fatalf("active vendors: %d %s", vendorList.Code, vendorList.Body.String())
	}
	for _, v := range vendorRows {
		if v["vendor_id"] == createdVendorID {
			t.Fatal("deactivated vendor present in active filter")
		}
	}
	var vendorAfter string
	if err = pool.QueryRow(ctx, `SELECT to_jsonb(v)::text FROM vendors v WHERE vendor_id=$1`, vendorB).Scan(&vendorAfter); err != nil || vendorAfter != vendorBefore {
		t.Fatalf("foreign vendor changed: %v", err)
	}
	if w := request("GET", "/api/v1/vendors/"+vendorA, good, ""); w.Code != 200 || strings.Contains(w.Body.String(), "business_id") {
		t.Fatalf("own vendor GET: %d %s", w.Code, w.Body.String())
	}
	var itemA, itemB string
	if err = pool.QueryRow(ctx, `SELECT item_id::text FROM items WHERE business_id=$1 LIMIT 1`, a).Scan(&itemA); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT item_id::text FROM items WHERE business_id=$1 LIMIT 1`, b).Scan(&itemB); err != nil {
		t.Fatal(err)
	}
	foreignCreate := request("POST", "/api/v1/items", good, fmt.Sprintf(`{"name":"foreign account","item_type":"SERVICE","revenue_account_id":%d}`, bAccount))
	missingCreate := request("POST", "/api/v1/items", good, `{"name":"foreign account","item_type":"SERVICE","revenue_account_id":2147483647}`)
	if foreignCreate.Code != 400 || missingCreate.Code != 400 || foreignCreate.Body.String() != missingCreate.Body.String() {
		t.Fatalf("foreign/missing create account: %d %s / %d %s", foreignCreate.Code, foreignCreate.Body.String(), missingCreate.Code, missingCreate.Body.String())
	}
	createdItem := request("POST", "/api/v1/items", good, fmt.Sprintf(`{"name":"created item","item_type":"SERVICE","revenue_account_id":%d,"business_id":"%s"}`, aAccount, b))
	var createdItemBody map[string]any
	if createdItem.Code != 201 || json.Unmarshal(createdItem.Body.Bytes(), &createdItemBody) != nil || createdItemBody["business_id"] != nil {
		t.Fatalf("own item create: %d %s", createdItem.Code, createdItem.Body.String())
	}
	createdItemID, ok := createdItemBody["item_id"].(string)
	if !ok {
		t.Fatal("missing item ID")
	}
	var itemBusiness string
	if err = pool.QueryRow(ctx, `SELECT business_id::text FROM items WHERE item_id=$1`, createdItemID).Scan(&itemBusiness); err != nil || itemBusiness != a {
		t.Fatalf("spoofed item ownership: %q %v", itemBusiness, err)
	}
	var targetBefore string
	if err = pool.QueryRow(ctx, `SELECT to_jsonb(t)::text FROM cloudflare_targets t WHERE business_id=$1 AND item_id=$2`, b, itemB).Scan(&targetBefore); err != nil {
		t.Fatal(err)
	}
	hostedBody := `{"name":"hosted A","item_type":"SERVICE","is_hosting":true,"hosting_domain":"a.example.com","hosting_grace_days":0,"hosting_suspension_enabled":false,"cloudflare_target":{"business_id":"` + b + `","item_id":"` + itemB + `","zone_id":"zone-a","record_id":"record-a","record_name":"a.example.com","record_type":"A","live_content":"1","maintenance_content":"2"}}`
	hosted := request("POST", "/api/v1/items", good, hostedBody)
	var hostedBodyOut map[string]any
	if hosted.Code != 201 || json.Unmarshal(hosted.Body.Bytes(), &hostedBodyOut) != nil {
		t.Fatalf("hosted create: %d %s", hosted.Code, hosted.Body.String())
	}
	hostedID, ok := hostedBodyOut["item_id"].(string)
	if !ok {
		t.Fatal("missing hosted item ID")
	}
	var hostedBusiness, hostedZone string
	if err = pool.QueryRow(ctx, `SELECT business_id::text,zone_id FROM cloudflare_targets WHERE item_id=$1`, hostedID).Scan(&hostedBusiness, &hostedZone); err != nil || hostedBusiness != a || hostedZone != "zone-a" {
		t.Fatalf("hosted target scope: %q %q %v", hostedBusiness, hostedZone, err)
	}
	updateHosted := `{"cloudflare_target":{"business_id":"` + b + `","item_id":"` + itemB + `","zone_id":"zone-new","record_id":"record-a","record_name":"a.example.com","record_type":"A","live_content":"1","maintenance_content":"2"}}`
	if w := request("PATCH", "/api/v1/items/"+hostedID, good, updateHosted); w.Code != 200 {
		t.Fatalf("hosted update: %d %s", w.Code, w.Body.String())
	}
	if err = pool.QueryRow(ctx, `SELECT business_id::text,zone_id FROM cloudflare_targets WHERE item_id=$1`, hostedID).Scan(&hostedBusiness, &hostedZone); err != nil || hostedBusiness != a || hostedZone != "zone-new" {
		t.Fatalf("hosted target upsert: %q %q %v", hostedBusiness, hostedZone, err)
	}
	var targetAfter string
	if err = pool.QueryRow(ctx, `SELECT to_jsonb(t)::text FROM cloudflare_targets t WHERE business_id=$1 AND item_id=$2`, b, itemB).Scan(&targetAfter); err != nil || targetAfter != targetBefore {
		t.Fatalf("foreign target changed: %v", err)
	}
	var itemBefore string
	if err = pool.QueryRow(ctx, `SELECT to_jsonb(i)::text FROM items i WHERE item_id=$1`, itemB).Scan(&itemBefore); err != nil {
		t.Fatal(err)
	}
	if w := request("GET", "/api/v1/items/"+itemB, good, ""); w.Code != 404 {
		t.Fatalf("foreign item GET: %d %s", w.Code, w.Body.String())
	}
	if w := request("GET", "/api/v1/items/"+itemA, good, ""); w.Code != 200 || strings.Contains(w.Body.String(), "business_id") {
		t.Fatalf("own item GET: %d %s", w.Code, w.Body.String())
	}
	if w := request("PATCH", "/api/v1/items/"+itemB, good, `{"name":"foreign"}`); w.Code != 404 {
		t.Fatalf("foreign item PATCH: %d %s", w.Code, w.Body.String())
	}
	var itemABefore string
	if err = pool.QueryRow(ctx, `SELECT to_jsonb(i)::text FROM items i WHERE item_id=$1`, itemA).Scan(&itemABefore); err != nil {
		t.Fatal(err)
	}
	foreignAccountBody := fmt.Sprintf(`{"revenue_account_id":%d}`, bAccount)
	foreignRef := request("PATCH", "/api/v1/items/"+itemA, good, foreignAccountBody)
	missingRef := request("PATCH", "/api/v1/items/"+itemA, good, `{"revenue_account_id":2147483647}`)
	if foreignRef.Code != 400 || missingRef.Code != 400 || foreignRef.Body.String() != missingRef.Body.String() {
		t.Fatalf("foreign/missing item account: %d %s / %d %s", foreignRef.Code, foreignRef.Body.String(), missingRef.Code, missingRef.Body.String())
	}
	var itemAAfter string
	if err = pool.QueryRow(ctx, `SELECT to_jsonb(i)::text FROM items i WHERE item_id=$1`, itemA).Scan(&itemAAfter); err != nil || itemABefore != itemAAfter {
		t.Fatalf("foreign account changed item: %v", err)
	}
	if w := request("PATCH", "/api/v1/items/"+itemA, good, `{"name":"updated item"}`); w.Code != 200 {
		t.Fatalf("own item PATCH: %d %s", w.Code, w.Body.String())
	}
	if w := request("DELETE", "/api/v1/items/"+itemB, good, ""); w.Code != 404 {
		t.Fatalf("foreign item DELETE: %d %s", w.Code, w.Body.String())
	}
	if w := request("DELETE", "/api/v1/items/"+itemA, good, ""); w.Code != 200 {
		t.Fatalf("own item DELETE: %d %s", w.Code, w.Body.String())
	}
	var itemActive bool
	if err = pool.QueryRow(ctx, `SELECT active FROM items WHERE item_id=$1`, itemA).Scan(&itemActive); err != nil || itemActive {
		t.Fatalf("item deactivation: %v %v", itemActive, err)
	}
	var itemAfter string
	if err = pool.QueryRow(ctx, `SELECT to_jsonb(i)::text FROM items i WHERE item_id=$1`, itemB).Scan(&itemAfter); err != nil || itemAfter != itemBefore {
		t.Fatalf("foreign item changed: %v", err)
	}
	var projectA, projectB string
	if err = pool.QueryRow(ctx, `SELECT project_id::text FROM projects WHERE business_id=$1 LIMIT 1`, a).Scan(&projectA); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT project_id::text FROM projects WHERE business_id=$1 LIMIT 1`, b).Scan(&projectB); err != nil {
		t.Fatal(err)
	}
	if w := request("GET", "/api/v1/projects/"+projectB, good, ""); w.Code != 404 {
		t.Fatalf("foreign project GET: %d %s", w.Code, w.Body.String())
	}
	if w := request("GET", "/api/v1/projects/"+projectA, good, ""); w.Code != 200 || strings.Contains(w.Body.String(), "business_id") {
		t.Fatalf("own project GET: %d %s", w.Code, w.Body.String())
	}
	var projectCustomerA, projectCustomerB string
	if err = pool.QueryRow(ctx, `SELECT customer_id::text FROM customers WHERE business_id=$1 LIMIT 1`, a).Scan(&projectCustomerA); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT customer_id::text FROM customers WHERE business_id=$1 LIMIT 1`, b).Scan(&projectCustomerB); err != nil {
		t.Fatal(err)
	}
	projectList := request("GET", "/api/v1/projects?customer_id="+projectCustomerA, good, "")
	var projectRows []map[string]any
	if projectList.Code != 200 || json.Unmarshal(projectList.Body.Bytes(), &projectRows) != nil || len(projectRows) == 0 {
		t.Fatalf("own project list: %d %s", projectList.Code, projectList.Body.String())
	}
	for _, p := range projectRows {
		if p["project_id"] == projectB {
			t.Fatal("foreign project in list")
		}
	}
	projectList = request("GET", "/api/v1/projects?customer_id="+projectCustomerB, good, "")
	if projectList.Code != 200 || json.Unmarshal(projectList.Body.Bytes(), &projectRows) != nil || len(projectRows) != 0 {
		t.Fatalf("foreign customer project list: %d %s", projectList.Code, projectList.Body.String())
	}
	foreignProjectBefore := ""
	if err = pool.QueryRow(ctx, `SELECT to_jsonb(p)::text FROM projects p WHERE project_id=$1`, projectB).Scan(&foreignProjectBefore); err != nil {
		t.Fatal(err)
	}
	if w := request("POST", "/api/v1/projects", good, `{"customer_id":"`+projectCustomerB+`","code":"ISOLATE","name":"foreign","currency":"IDR"}`); w.Code != 400 {
		t.Fatalf("foreign customer project create: %d %s", w.Code, w.Body.String())
	}
	createdProject := request("POST", "/api/v1/projects", good, `{"customer_id":"`+projectCustomerA+`","code":"ISOLATE","name":"A project","currency":"IDR","business_id":"`+b+`"}`)
	var projectBody map[string]any
	if createdProject.Code != 201 || json.Unmarshal(createdProject.Body.Bytes(), &projectBody) != nil || projectBody["business_id"] != nil {
		t.Fatalf("project create: %d %s", createdProject.Code, createdProject.Body.String())
	}
	createdProjectID, ok := projectBody["project_id"].(string)
	if !ok {
		t.Fatal("missing project ID")
	}
	if w := request("PATCH", "/api/v1/projects/"+createdProjectID, good, `{"name":"updated project"}`); w.Code != 200 {
		t.Fatalf("own project PATCH: %d %s", w.Code, w.Body.String())
	}
	var projectName string
	if err = pool.QueryRow(ctx, `SELECT name FROM projects WHERE project_id=$1`, createdProjectID).Scan(&projectName); err != nil || projectName != "updated project" {
		t.Fatalf("project PATCH not persisted: %q %v", projectName, err)
	}
	if w := request("POST", "/api/v1/projects", good, `{"customer_id":"`+projectCustomerA+`","code":"ISOLATE","name":"duplicate","currency":"IDR"}`); w.Code != 400 {
		t.Fatalf("same-business project code duplicate: %d %s", w.Code, w.Body.String())
	}
	if _, err = pool.Exec(ctx, `INSERT INTO projects(business_id,project_id,customer_id,code,name,currency,status,active) VALUES($1,gen_random_uuid(),$2,'CROSSCODE','B project','IDR','ACTIVE',true)`, b, projectCustomerB); err != nil {
		t.Fatal(err)
	}
	if w := request("POST", "/api/v1/projects", good, `{"customer_id":"`+projectCustomerA+`","code":"CROSSCODE","name":"A project","currency":"IDR"}`); w.Code != 201 {
		t.Fatalf("cross-business project code: %d %s", w.Code, w.Body.String())
	}
	if w := request("DELETE", "/api/v1/projects/"+projectA, good, ""); w.Code != 400 {
		t.Fatalf("referenced project DELETE: %d %s", w.Code, w.Body.String())
	}
	var quoteOnlyProject string
	if err = pool.QueryRow(ctx, `INSERT INTO projects(business_id,project_id,customer_id,code,name,currency,status,active) VALUES($1,gen_random_uuid(),$2,'QUOTEONLY','quote-only','IDR','ACTIVE',true) RETURNING project_id::text`, a, projectCustomerA).Scan(&quoteOnlyProject); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE quotations SET project_id=$1 WHERE business_id=$2`, quoteOnlyProject, a); err != nil {
		t.Fatal(err)
	}
	if w := request("DELETE", "/api/v1/projects/"+quoteOnlyProject, good, ""); w.Code != 400 {
		t.Fatalf("quotation-only project DELETE: %d %s", w.Code, w.Body.String())
	}
	if w := request("PATCH", "/api/v1/projects/"+createdProjectID, good, `{"customer_id":"`+projectCustomerB+`"}`); w.Code != 400 {
		t.Fatalf("foreign customer project update: %d %s", w.Code, w.Body.String())
	}
	if w := request("PATCH", "/api/v1/projects/"+projectB, good, `{"name":"foreign"}`); w.Code != 404 {
		t.Fatalf("foreign project PATCH: %d %s", w.Code, w.Body.String())
	}
	if w := request("DELETE", "/api/v1/projects/"+projectB, good, ""); w.Code != 404 {
		t.Fatalf("foreign project DELETE: %d %s", w.Code, w.Body.String())
	}
	if w := request("DELETE", "/api/v1/projects/"+createdProjectID, good, ""); w.Code != 204 {
		t.Fatalf("own project DELETE: %d %s", w.Code, w.Body.String())
	}
	var removed int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM projects WHERE project_id=$1`, createdProjectID).Scan(&removed); err != nil || removed != 0 {
		t.Fatalf("project not removed: %d %v", removed, err)
	}
	var foreignProjectAfter string
	if err = pool.QueryRow(ctx, `SELECT to_jsonb(p)::text FROM projects p WHERE project_id=$1`, projectB).Scan(&foreignProjectAfter); err != nil || foreignProjectAfter != foreignProjectBefore {
		t.Fatalf("foreign project mutated: %v", err)
	}
	var beforeBills, beforeBillLines int
	if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM bills),(SELECT count(*) FROM bill_line_items)`).Scan(&beforeBills, &beforeBillLines); err != nil {
		t.Fatal(err)
	}
	missingID := "00000000-0000-0000-0000-000000000099"
	makeBill := func(vendor, project, item string, account int) string {
		return fmt.Sprintf(`{"vendor_id":"%s","project_id":"%s","currency":"IDR","line_items":[{"item_id":"%s","expense_account_id":%d,"description":"work","quantity":"2","unit_price":"3.2500"}]}`, vendor, project, item, account)
	}
	for _, pair := range [][2]string{{makeBill(vendorB, projectA, itemA, aAccount), makeBill(missingID, projectA, itemA, aAccount)}, {makeBill(vendorA, projectB, itemA, aAccount), makeBill(vendorA, missingID, itemA, aAccount)}, {makeBill(vendorA, projectA, itemB, aAccount), makeBill(vendorA, projectA, missingID, aAccount)}, {makeBill(vendorA, projectA, itemA, bAccount), makeBill(vendorA, projectA, itemA, 2147483647)}} {
		foreign := request("POST", "/api/v1/bills", good, pair[0])
		missing := request("POST", "/api/v1/bills", good, pair[1])
		if foreign.Code != 400 || missing.Code != 400 || foreign.Body.String() != missing.Body.String() {
			t.Fatalf("foreign/missing bill reference: %d %s / %d %s", foreign.Code, foreign.Body.String(), missing.Code, missing.Body.String())
		}
	}
	var afterBills, afterBillLines int
	if err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM bills),(SELECT count(*) FROM bill_line_items)`).Scan(&afterBills, &afterBillLines); err != nil || afterBills != beforeBills || afterBillLines != beforeBillLines {
		t.Fatalf("invalid bill wrote rows: %d/%d -> %d/%d: %v", beforeBills, beforeBillLines, afterBills, afterBillLines, err)
	}
	positiveBill := strings.TrimSuffix(makeBill(vendorA, projectA, itemA, aAccount), "}") + `,"business_id":"` + b + `","issue_date":"2026-01-01T00:00:00Z","due_date":1767312000}`
	createdBill := request("POST", "/api/v1/bills", good, positiveBill)
	var billCreated map[string]any
	if createdBill.Code != 201 || json.Unmarshal(createdBill.Body.Bytes(), &billCreated) != nil || billCreated["amount"] != "6.5000" || billCreated["subtotal"] != "6.5000" || billCreated["balance_due"] != "6.5000" || billCreated["status"] != "DRAFT" || billCreated["issue_date"] != "2026-01-01" || billCreated["due_date"] != "2026-01-02" || billCreated["business_id"] != nil {
		t.Fatalf("scoped bill create: %d %s", createdBill.Code, createdBill.Body.String())
	}
	createdBillID, ok := billCreated["bill_id"].(string)
	if !ok {
		t.Fatal("missing created bill ID")
	}
	var ownerBill, ownerLine, quantity, unitPrice, lineAmount string
	if err = pool.QueryRow(ctx, `SELECT b.business_id::text,l.business_id::text,l.quantity::text,l.unit_price::text,l.amount::text FROM bills b JOIN bill_line_items l ON l.bill_id=b.bill_id WHERE b.bill_id=$1`, createdBillID).Scan(&ownerBill, &ownerLine, &quantity, &unitPrice, &lineAmount); err != nil || ownerBill != a || ownerLine != a || quantity != "2.0000" || unitPrice != "3.2500" || lineAmount != "6.5000" {
		t.Fatalf("bill ownership/line values: %s %s %s %s %s: %v", ownerBill, ownerLine, quantity, unitPrice, lineAmount, err)
	}
	billSnapshot := func(id string) string {
		t.Helper()
		var snapshot string
		if err := pool.QueryRow(ctx, `SELECT jsonb_build_object('bill',(SELECT to_jsonb(b) FROM bills b WHERE bill_id=$1),'lines',(SELECT jsonb_agg(to_jsonb(l) ORDER BY line_item_id) FROM bill_line_items l WHERE bill_id=$1))::text`, id).Scan(&snapshot); err != nil {
			t.Fatal(err)
		}
		return snapshot
	}
	foreignBillBefore := billSnapshot(billB)
	if w := request("PATCH", "/api/v1/bills/"+billB, good, `{"notes":"foreign"}`); w.Code != 404 {
		t.Fatalf("foreign bill PATCH: %d %s", w.Code, w.Body.String())
	}
	createdBillBefore := billSnapshot(createdBillID)
	patchBill := func(field, id string) string { return fmt.Sprintf(`{"%s":"%s"}`, field, id) }
	for _, pair := range [][2]string{{patchBill("vendor_id", vendorB), patchBill("vendor_id", missingID)}, {patchBill("project_id", projectB), patchBill("project_id", missingID)}, {fmt.Sprintf(`{"line_items":[{"item_id":"%s","description":"work","quantity":"1","unit_price":"1"}]}`, itemB), fmt.Sprintf(`{"line_items":[{"item_id":"%s","description":"work","quantity":"1","unit_price":"1"}]}`, missingID)}, {fmt.Sprintf(`{"line_items":[{"expense_account_id":%d,"description":"work","quantity":"1","unit_price":"1"}]}`, bAccount), `{"line_items":[{"expense_account_id":2147483647,"description":"work","quantity":"1","unit_price":"1"}]}`}} {
		foreign := request("PATCH", "/api/v1/bills/"+createdBillID, good, pair[0])
		missing := request("PATCH", "/api/v1/bills/"+createdBillID, good, pair[1])
		if foreign.Code != 400 || missing.Code != 400 || foreign.Body.String() != missing.Body.String() {
			t.Fatalf("bill PATCH foreign/missing: %d %s / %d %s", foreign.Code, foreign.Body.String(), missing.Code, missing.Body.String())
		}
	}
	if billSnapshot(createdBillID) != createdBillBefore {
		t.Fatal("rejected bill patch changed header or lines")
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE bill_line_items ADD CONSTRAINT test_bill_line_failure CHECK (description <> 'reject this line')`); err != nil {
		t.Fatal(err)
	}
	failedPatch := `{"notes":"should roll back","line_items":[{"description":"insert first","quantity":"1","unit_price":"1"},{"description":"reject this line","quantity":"1","unit_price":"1"}]}`
	failedCreate := fmt.Sprintf(`{"vendor_id":"%s","currency":"IDR",%s`, vendorA, strings.TrimPrefix(failedPatch, "{"))
	if w := request("POST", "/api/v1/bills", good, failedCreate); w.Code != 500 {
		t.Fatalf("bill create line failure: %d %s", w.Code, w.Body.String())
	}
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM bills),(SELECT count(*) FROM bill_line_items)`).Scan(&afterBills, &afterBillLines); err != nil || afterBills != beforeBills+1 || afterBillLines != beforeBillLines+1 {
		t.Fatalf("failed create left a bill or line: %d/%d: %v", afterBills, afterBillLines, err)
	}
	if w := request("PATCH", "/api/v1/bills/"+createdBillID, good, failedPatch); w.Code != 500 {
		t.Fatalf("bill line failure: %d %s", w.Code, w.Body.String())
	}
	if billSnapshot(createdBillID) != createdBillBefore {
		t.Fatal("bill line failure did not roll back the original lines")
	}
	replacement := fmt.Sprintf(`{"business_id":"%s","notes":"updated","discount_type":"PERCENT","discount_value":"25","line_items":[{"item_id":"%s","expense_account_id":%d,"position":7,"description":"replacement","quantity":"3","unit_price":"2.5000"}]}`, b, itemA, aAccount)
	updatedBill := request("PATCH", "/api/v1/bills/"+createdBillID, good, replacement)
	var billUpdated map[string]any
	if updatedBill.Code != 200 || json.Unmarshal(updatedBill.Body.Bytes(), &billUpdated) != nil || billUpdated["subtotal"] != "7.5000" || billUpdated["amount"] != "5.6250" || billUpdated["balance_due"] != "5.6250" || billUpdated["status"] != "DRAFT" {
		t.Fatalf("replacement bill totals: %d %s", updatedBill.Code, updatedBill.Body.String())
	}
	var persistedLines int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM bill_line_items WHERE bill_id=$1`, createdBillID).Scan(&persistedLines); err != nil || persistedLines != 1 {
		t.Fatalf("replacement bill line count: %d: %v", persistedLines, err)
	}
	var persistedItem string
	var persistedAccount, persistedPosition int
	if err := pool.QueryRow(ctx, `SELECT business_id::text,item_id::text,expense_account_id,position,quantity::text,unit_price::text,amount::text FROM bill_line_items WHERE bill_id=$1`, createdBillID).Scan(&ownerLine, &persistedItem, &persistedAccount, &persistedPosition, &quantity, &unitPrice, &lineAmount); err != nil ||
		ownerLine != a || persistedItem != itemA || persistedAccount != aAccount || persistedPosition != 7 || quantity != "3.0000" || unitPrice != "2.5000" || lineAmount != "7.5000" {
		t.Fatalf("replacement bill line: %s %s %d %d %s %s %s: %v", ownerLine, persistedItem, persistedAccount, persistedPosition, quantity, unitPrice, lineAmount, err)
	}
	if w := request("PATCH", "/api/v1/bills/"+createdBillID, good, `{"status":"OPEN"}`); w.Code != 200 {
		t.Fatalf("open bill: %d %s", w.Code, w.Body.String())
	}
	// 0.0001 × 2.5 is exactly 0.00025, which rounds down to the even digit.
	tie := `{"due_date":"2026-02-01T00:00:00Z","discount_type":null,"discount_value":null,"line_items":[{"description":"rounding tie","quantity":"0.0001","unit_price":"2.5"}]}`
	openBill := request("PATCH", "/api/v1/bills/"+createdBillID, good, tie)
	var openBody map[string]any
	if openBill.Code != 200 || json.Unmarshal(openBill.Body.Bytes(), &openBody) != nil || openBody["subtotal"] != "0.0002" || openBody["amount"] != "0.0002" ||
		openBody["balance_due"] != "5.6250" || openBody["status"] != "OPEN" || openBody["due_date"] != "2026-02-01" {
		t.Fatalf("open bill edit: %d %s", openBill.Code, openBill.Body.String())
	}
	var persistedBalance, persistedDueDate string
	if err := pool.QueryRow(ctx, `SELECT b.balance_due::text,b.due_date::text,l.amount::text FROM bills b JOIN bill_line_items l USING (bill_id,business_id) WHERE b.bill_id=$1`, createdBillID).Scan(&persistedBalance, &persistedDueDate, &lineAmount); err != nil ||
		persistedBalance != "5.6250" || persistedDueDate != "2026-02-01" || lineAmount != "0.0002" {
		t.Fatalf("open bill persistence: %s %s %s: %v", persistedBalance, persistedDueDate, lineAmount, err)
	}
	if w := request("PATCH", "/api/v1/bills/"+createdBillID, good, `{"status":"PAID"}`); w.Code != 200 {
		t.Fatalf("bill status update: %d %s", w.Code, w.Body.String())
	}
	paidBill := billSnapshot(createdBillID)
	if w := request("PATCH", "/api/v1/bills/"+createdBillID, good, replacement); w.Code != 400 || !strings.Contains(w.Body.String(), "bill is PAID and cannot be edited") {
		t.Fatalf("paid bill edit: %d %s", w.Code, w.Body.String())
	}
	if billSnapshot(createdBillID) != paidBill || billSnapshot(billB) != foreignBillBefore {
		t.Fatal("scoped writes changed a paid or foreign bill")
	}
	t.Run("QuotationWrites", func(t *testing.T) {
		testScopedQuotationWrites(t, pool, a, b, bUser, quotationB, quoteCustomerA, quoteCustomerB, projectA, projectB, itemA, itemB, missingID, good, token, request)
	})
	t.Run("InvoiceWrites", func(t *testing.T) {
		testScopedInvoiceWrites(t, pool, a, b, aUser, bUser, invoiceB, quoteCustomerA, quoteCustomerB, projectA, projectB, itemA, itemB, missingID, good, token, request)
	})
	t.Run("InvoiceLifecycle", func(t *testing.T) {
		testScopedInvoiceLifecycle(t, pool, a, b, bUser, invoiceB, itemA, itemB, missingID, good, token, request)
	})
	t.Run("PaymentAudit", func(t *testing.T) {
		testScopedPaymentAudit(t, pool, a, b, aUser, bUser, ownPayment, foreignPayment, invoiceB, quoteCustomerB, missingID, good, token(bUser, b), request)
	})
	t.Run("InvoicePayments", func(t *testing.T) {
		testScopedInvoicePayments(t, pool, a, b, aUser, bUser, invoiceB, quoteCustomerA, quoteCustomerB, missingID, good, token, request)
	})
	t.Run("MilestoneWrites", func(t *testing.T) {
		testScopedMilestoneWrites(t, pool, a, b, aUser, bUser, quoteCustomerA, quoteCustomerB, projectA, projectB, missingID, good, token, request)
	})
	t.Run("RecurringTemplateWrites", func(t *testing.T) {
		testScopedRecurringTemplateWrites(t, pool, a, b, bUser, quoteCustomerA, quoteCustomerB, projectA, projectB, itemA, itemB, missingID, good, token, request)
	})
	t.Run("UsageLock", func(t *testing.T) {
		testScopedUsageLock(t, pool, a, b, bUser, quoteCustomerA, quoteCustomerB, missingID, good, token, request)
	})
	t.Run("BillLifecycle", func(t *testing.T) {
		testScopedBillLifecycle(t, pool, a, b, bUser, createdBillID, billB, missingID, good, token, request, billSnapshot)
	})
	if w := request("GET", "/api/v1/reports/trial-balance", good, ""); w.Code != 404 {
		t.Fatalf("unconverted route exposed: %d", w.Code)
	}
	var customerA, customerB string
	if err = pool.QueryRow(ctx, `SELECT customer_id::text FROM customers WHERE business_id=$1 LIMIT 1`, a).Scan(&customerA); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT customer_id::text FROM customers WHERE business_id=$1 LIMIT 1`, b).Scan(&customerB); err != nil {
		t.Fatal(err)
	}
	var customerBefore string
	if err = pool.QueryRow(ctx, `SELECT to_jsonb(c)::text FROM customers c WHERE customer_id=$1`, customerB).Scan(&customerBefore); err != nil {
		t.Fatal(err)
	}
	if w := request("GET", "/api/v1/customers/"+customerB, good, ""); w.Code != 404 {
		t.Fatalf("foreign customer GET: %d %s", w.Code, w.Body.String())
	}
	if w := request("DELETE", "/api/v1/customers/"+customerB, good, ""); w.Code != 404 {
		t.Fatalf("foreign customer DELETE: %d %s", w.Code, w.Body.String())
	}
	if w := request("PATCH", "/api/v1/customers/"+customerB, good, `{"name":"foreign change"}`); w.Code != 404 {
		t.Fatalf("foreign customer PATCH: %d %s", w.Code, w.Body.String())
	}
	createdCustomer := request("POST", "/api/v1/customers", good, `{"name":"Created A","contact_phone":"123","notes":"preserve","business_id":"`+b+`"}`)
	var customerBody map[string]any
	if createdCustomer.Code != 201 || json.Unmarshal(createdCustomer.Body.Bytes(), &customerBody) != nil || customerBody["business_id"] != nil {
		t.Fatalf("create customer: %d %s", createdCustomer.Code, createdCustomer.Body.String())
	}
	createdCustomerID, ok := customerBody["customer_id"].(string)
	if !ok {
		t.Fatal("missing customer ID")
	}
	var customerBusiness string
	if err = pool.QueryRow(ctx, `SELECT business_id::text FROM customers WHERE customer_id=$1`, createdCustomerID).Scan(&customerBusiness); err != nil || customerBusiness != a {
		t.Fatalf("spoofed customer scope: %q %v", customerBusiness, err)
	}
	if w := request("PATCH", "/api/v1/customers/"+createdCustomerID, good, `{"name":"Updated A"}`); w.Code != 200 {
		t.Fatalf("own customer PATCH: %d %s", w.Code, w.Body.String())
	}
	var createdCustomerName string
	if err = pool.QueryRow(ctx, `SELECT name FROM customers WHERE customer_id=$1`, createdCustomerID).Scan(&createdCustomerName); err != nil || createdCustomerName != "Updated A" {
		t.Fatalf("customer PATCH not persisted: %q %v", createdCustomerName, err)
	}
	if w := request("PATCH", "/api/v1/customers/"+createdCustomerID, good, `{"contact_phone":null,"business_id":"`+b+`","customer_id":"`+customerB+`"}`); w.Code != 200 {
		t.Fatalf("nullable customer PATCH: %d %s", w.Code, w.Body.String())
	}
	var phone *string
	var customerNotes string
	if err = pool.QueryRow(ctx, `SELECT contact_phone,notes,business_id::text FROM customers WHERE customer_id=$1`, createdCustomerID).Scan(&phone, &customerNotes, &customerBusiness); err != nil || phone != nil || customerNotes != "preserve" || customerBusiness != a {
		t.Fatalf("customer patch omitted/null/spoof: phone=%v notes=%q scope=%q err=%v", phone, customerNotes, customerBusiness, err)
	}
	var createdBefore string
	if err = pool.QueryRow(ctx, `SELECT to_jsonb(c)::text FROM customers c WHERE customer_id=$1`, createdCustomerID).Scan(&createdBefore); err != nil {
		t.Fatal(err)
	}
	if w := request("PATCH", "/api/v1/customers/"+createdCustomerID, good, `{"matching_aliases":null}`); w.Code != 500 {
		t.Fatalf("null customer array: %d %s", w.Code, w.Body.String())
	}
	var createdAfter string
	if err = pool.QueryRow(ctx, `SELECT to_jsonb(c)::text FROM customers c WHERE customer_id=$1`, createdCustomerID).Scan(&createdAfter); err != nil || createdAfter != createdBefore {
		t.Fatalf("null array mutated customer: %v", err)
	}
	if w := request("GET", "/api/v1/customers/"+customerA, good, ""); w.Code != 200 || strings.Contains(w.Body.String(), "business_id") {
		t.Fatalf("own customer GET: %d %s", w.Code, w.Body.String())
	}
	customers := request("GET", "/api/v1/customers?active=true", good, "")
	var customerRows []map[string]any
	if customers.Code != 200 || json.Unmarshal(customers.Body.Bytes(), &customerRows) != nil {
		t.Fatalf("customer list: %d %s", customers.Code, customers.Body.String())
	}
	customerFound := false
	for _, c := range customerRows {
		if c["customer_id"] == customerB {
			t.Fatal("foreign customer in list")
		}
		if c["customer_id"] == customerA {
			customerFound = true
		}
	}
	if !customerFound {
		t.Fatal("own active customer absent")
	}
	if w := request("DELETE", "/api/v1/customers/"+customerA, good, ""); w.Code != 200 {
		t.Fatalf("own customer DELETE: %d %s", w.Code, w.Body.String())
	}
	var customerActive bool
	if err = pool.QueryRow(ctx, `SELECT active FROM customers WHERE customer_id=$1`, customerA).Scan(&customerActive); err != nil || customerActive {
		t.Fatalf("customer deactivate not persisted: %v %v", customerActive, err)
	}
	customers = request("GET", "/api/v1/customers?active=true", good, "")
	if customers.Code != 200 || json.Unmarshal(customers.Body.Bytes(), &customerRows) != nil {
		t.Fatalf("active customers: %d %s", customers.Code, customers.Body.String())
	}
	for _, c := range customerRows {
		if c["customer_id"] == customerA {
			t.Fatal("deactivated customer in active list")
		}
	}
	var customerAfter string
	if err = pool.QueryRow(ctx, `SELECT to_jsonb(c)::text FROM customers c WHERE customer_id=$1`, customerB).Scan(&customerAfter); err != nil || customerAfter != customerBefore {
		t.Fatalf("foreign customer changed: %v", err)
	}
	userList := request("GET", "/api/v1/users", good, "")
	var listed []map[string]any
	if userList.Code != 200 || json.Unmarshal(userList.Body.Bytes(), &listed) != nil || len(listed) != 1 || listed[0]["user_id"] != aUser {
		t.Fatalf("scoped user list: %d %s", userList.Code, userList.Body.String())
	}
	if w := request("PATCH", "/api/v1/users/me", good, `{"display_name":"Scoped A","user_id":"`+bUser+`","business_id":"`+b+`","role":"viewer"}`); w.Code != 200 {
		t.Fatalf("user profile PATCH: %d %s", w.Code, w.Body.String())
	}
	var ownDisplay, ownRole string
	if err = pool.QueryRow(ctx, `SELECT display_name,role FROM users WHERE business_id=$1 AND user_id=$2`, a, aUser).Scan(&ownDisplay, &ownRole); err != nil || ownDisplay != "Scoped A" || ownRole != "admin" {
		t.Fatalf("self profile persistence: %q %q %v", ownDisplay, ownRole, err)
	}
	var foreignDisplay *string
	if err = pool.QueryRow(ctx, `SELECT display_name FROM users WHERE business_id=$1 AND user_id=$2`, b, bUser).Scan(&foreignDisplay); err != nil || foreignDisplay != nil {
		t.Fatalf("foreign user changed: %v %v", foreignDisplay, err)
	}
	var foreignHash string
	if err = pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE business_id=$1 AND user_id=$2`, b, bUser).Scan(&foreignHash); err != nil {
		t.Fatal(err)
	}
	if w := request("POST", "/api/v1/auth/change-password", good, `{"current_password":"alpha-password","new_password":"new-password"}`); w.Code != 200 {
		t.Fatalf("change password: %d %s", w.Code, w.Body.String())
	}
	var foreignHashAfter string
	if err = pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE business_id=$1 AND user_id=$2`, b, bUser).Scan(&foreignHashAfter); err != nil || foreignHashAfter != foreignHash {
		t.Fatalf("foreign password changed: %v", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE users SET password_reset_token=CASE WHEN business_id=$1 THEN 'reset-A' ELSE 'reset-B' END, password_reset_expires=now()+interval '1 hour'`, a); err != nil {
		t.Fatal(err)
	}
	if w := request("POST", "/api/v1/auth/reset-password", "", `{"token":"reset-B","new_password":"changed-password"}`); w.Code != 400 {
		t.Fatalf("foreign reset token: %d %s", w.Code, w.Body.String())
	}
	if w := request("POST", "/api/v1/auth/reset-password", "", `{"token":"reset-A","new_password":"changed-password"}`); w.Code != 200 {
		t.Fatalf("own reset token: %d %s", w.Code, w.Body.String())
	}
	var foreignToken string
	if err = pool.QueryRow(ctx, `SELECT password_hash,password_reset_token FROM users WHERE business_id=$1 AND user_id=$2`, b, bUser).Scan(&foreignHashAfter, &foreignToken); err != nil || foreignHashAfter != foreignHash || foreignToken != "reset-B" {
		t.Fatalf("foreign reset changed state: %v", err)
	}
	known := request("POST", "/api/v1/auth/forgot-password", "", `{"email":"scope@example.com"}`)
	unknown := request("POST", "/api/v1/auth/forgot-password", "", `{"email":"absent@example.com"}`)
	if known.Code != 200 || unknown.Code != 200 || known.Body.String() != unknown.Body.String() {
		t.Fatalf("recovery enumeration: known=%d %s unknown=%d %s", known.Code, known.Body.String(), unknown.Code, unknown.Body.String())
	}
	if len(sender.messages) != 1 || len(sender.messages[0].To) != 1 || sender.messages[0].To[0] != "scope@example.com" {
		t.Fatalf("unexpected fake recovery messages: %v", sender.messages)
	}
	resetLink, err := neturl.Parse(strings.TrimPrefix(sender.messages[0].Text, "Reset your password: "))
	if err != nil || resetLink.Query().Get("token") == "" {
		t.Fatalf("invalid fake recovery link: %v", err)
	}
	resetToken := resetLink.Query().Get("token")
	resetBody := `{"token":"` + resetToken + `","new_password":"recovered-password"}`
	if w := request("POST", "/api/v1/auth/reset-password", "", resetBody); w.Code != 200 {
		t.Fatalf("reset link: %d %s", w.Code, w.Body.String())
	}
	if w := request("POST", "/api/v1/auth/reset-password", "", resetBody); w.Code != 400 {
		t.Fatalf("reset replay: %d %s", w.Code, w.Body.String())
	}
	if w := request("POST", "/api/v1/auth/login", "", `{"email":"scope@example.com","password":"recovered-password"}`); w.Code != 200 {
		t.Fatalf("recovered login: %d %s", w.Code, w.Body.String())
	}
	if w := request("POST", "/api/v1/auth/forgot-password", "", `{"email":"scope@example.com"}`); w.Code != 200 || len(sender.messages) != 2 {
		t.Fatalf("second recovery: %d", w.Code)
	}
	expiring, err := neturl.Parse(strings.TrimPrefix(sender.messages[1].Text, "Reset your password: "))
	if err != nil || expiring.Query().Get("token") == "" {
		t.Fatalf("invalid second recovery link: %v", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE users SET password_reset_expires=now()-interval '1 minute' WHERE business_id=$1 AND user_id=$2`, a, aUser); err != nil {
		t.Fatal(err)
	}
	if w := request("POST", "/api/v1/auth/reset-password", "", `{"token":"`+expiring.Query().Get("token")+`","new_password":"ignored-password"}`); w.Code != 400 {
		t.Fatalf("expired reset: %d %s", w.Code, w.Body.String())
	}
	if _, err = pool.Exec(ctx, `UPDATE users SET email='foreign@example.com' WHERE business_id=$1 AND user_id=$2`, b, bUser); err != nil {
		t.Fatal(err)
	}
	foreignRecovery := request("POST", "/api/v1/auth/forgot-password", "", `{"email":"foreign@example.com"}`)
	if foreignRecovery.Code != 200 || foreignRecovery.Body.String() != unknown.Body.String() || len(sender.messages) != 2 {
		t.Fatalf("foreign-only recovery: %d %s, messages=%d", foreignRecovery.Code, foreignRecovery.Body.String(), len(sender.messages))
	}
	foreignReset := request("POST", "/api/v1/users/"+bUser+"/reset-password", good, `{"password":"foreign-change"}`)
	missingReset := request("POST", "/api/v1/users/00000000-0000-0000-0000-000000000099/reset-password", good, `{"password":"foreign-change"}`)
	if foreignReset.Code != 404 || missingReset.Code != 404 || foreignReset.Body.String() != missingReset.Body.String() {
		t.Fatalf("foreign/missing admin reset: %d %s / %d %s", foreignReset.Code, foreignReset.Body.String(), missingReset.Code, missingReset.Body.String())
	}
	if err = pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE business_id=$1 AND user_id=$2`, b, bUser).Scan(&foreignHashAfter); err != nil || foreignHashAfter != foreignHash {
		t.Fatalf("foreign admin reset changed hash: %v", err)
	}
	createBody := `{"email":"foreign@example.com","password":"new-user-password","role":"admin","business_id":"` + b + `"}`
	createdUser := request("POST", "/api/v1/users", good, createBody)
	if createdUser.Code != 201 {
		t.Fatalf("create user with B email: %d %s", createdUser.Code, createdUser.Body.String())
	}
	var createdUserBody map[string]any
	if json.Unmarshal(createdUser.Body.Bytes(), &createdUserBody) != nil || createdUserBody["password_hash"] != nil || createdUserBody["business_id"] != nil {
		t.Fatalf("user create leaked fields: %s", createdUser.Body.String())
	}
	createdUserID, ok := createdUserBody["user_id"].(string)
	if !ok {
		t.Fatalf("missing created user ID: %s", createdUser.Body.String())
	}
	var createdBusiness string
	if err = pool.QueryRow(ctx, `SELECT business_id::text FROM users WHERE user_id=$1`, createdUserID).Scan(&createdBusiness); err != nil || createdBusiness != a {
		t.Fatalf("spoofed user scope: %q %v", createdBusiness, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE users SET password_reset_token='temporary-token', password_reset_expires=now()+interval '1 hour' WHERE business_id=$1 AND user_id=$2`, a, createdUserID); err != nil {
		t.Fatal(err)
	}
	if w := request("POST", "/api/v1/users/"+createdUserID+"/reset-password", good, `{"password":"admin-new-password"}`); w.Code != 200 {
		t.Fatalf("own admin reset: %d %s", w.Code, w.Body.String())
	}
	var tokenCleared, expiryCleared bool
	if err = pool.QueryRow(ctx, `SELECT password_reset_token IS NULL,password_reset_expires IS NULL FROM users WHERE business_id=$1 AND user_id=$2`, a, createdUserID).Scan(&tokenCleared, &expiryCleared); err != nil || !tokenCleared || !expiryCleared {
		t.Fatalf("admin reset did not clear recovery: %v", err)
	}
	if w := request("POST", "/api/v1/auth/login", "", `{"email":"foreign@example.com","password":"new-user-password"}`); w.Code != 401 {
		t.Fatalf("old admin-reset password: %d", w.Code)
	}
	if w := request("POST", "/api/v1/auth/login", "", `{"email":"foreign@example.com","password":"admin-new-password"}`); w.Code != 200 {
		t.Fatalf("new admin-reset password: %d %s", w.Code, w.Body.String())
	}
	if w := request("POST", "/api/v1/users", good, createBody); w.Code != 409 {
		t.Fatalf("same-business duplicate user: %d %s", w.Code, w.Body.String())
	}
	if err = pool.QueryRow(ctx, `SELECT password_reset_token FROM users WHERE business_id=$1 AND user_id=$2`, b, bUser).Scan(&foreignToken); err != nil || foreignToken != "reset-B" {
		t.Fatalf("foreign recovery changed token: %v", err)
	}
	t.Run("QuotationConvert", func(t *testing.T) {
		testScopedQuotationConvert(t, pool, a, b, bUser, quotationB, quoteCustomerA, quoteCustomerB, projectA, projectB, itemA, itemB, missingID, good, token, request)
	})
	locking, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer locking.Rollback(ctx)
	if _, err = locking.Exec(ctx, `UPDATE users SET role='viewer' WHERE business_id=$1 AND user_id=$2`, a, aUser); err != nil {
		t.Fatal(err)
	}
	response := make(chan *httptest.ResponseRecorder, 1)
	go func() { response <- request("PUT", "/api/v1/business-profile", good, `{"name":"should not commit"}`) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var blocked int
		if err = pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query ILIKE '%users%'`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("profile mutation did not block on user lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = locking.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case w := <-response:
		if w.Code != 403 {
			t.Fatalf("demoted user mutation: %d %s", w.Code, w.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("profile mutation did not finish")
	}
	if err = pool.QueryRow(ctx, `SELECT name FROM business_profile WHERE business_id=$1`, a).Scan(&name); err != nil || name != "HTTP A" {
		t.Fatalf("demoted user changed profile: %q %v", name, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE users SET role='admin' WHERE business_id=$1 AND user_id=$2`, a, aUser); err != nil {
		t.Fatal(err)
	}
	t.Run("DocumentPDF", func(t *testing.T) {
		testScopedDocumentPDF(t, pool, a, b, aUser, bUser, secret, token)
	})
	t.Run("PaymentIntake", func(t *testing.T) {
		testScopedPaymentIntake(t, pool, a, b)
	})
	t.Run("Reconciliation", func(t *testing.T) {
		testScopedReconciliation(t, pool, a, b, aUser, bUser, good, token, request)
	})
	t.Run("CandidateFence", func(t *testing.T) {
		testScopedCandidateFence(t, pool, platform.SessionConfig{Secret: secret, Path: "/", SameSite: http.SameSiteLaxMode, Sender: sender}, a, b, aUser, bUser, token)
	})
}

// invoiceLineGate pauses before pgx sends the line query, after the header query has completed.
type invoiceLineGate struct {
	marker  string
	once    sync.Once
	reached chan struct{}
	release chan struct{}
}

func (g *invoiceLineGate) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	if strings.Contains(d.SQL, g.marker) {
		g.once.Do(func() {
			close(g.reached)
			select {
			case <-g.release:
			case <-ctx.Done():
			}
		})
	}
	return ctx
}
func (*invoiceLineGate) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

func testDocumentSnapshot(t *testing.T, url, company, user, invoice, kind string) {
	specs := map[string]struct{ table, lines, id, marker, path string }{
		"customer-summary": {"invoices", "invoice_line_items", "invoice_id", "-- name: CustomerInvoiceLines", "/api/v1/customers/"},
		"invoice":          {"invoices", "invoice_line_items", "invoice_id", "-- name: InvoiceLinesByIDs", "/api/v1/invoices/"},
		"quotation":        {"quotations", "quotation_line_items", "quotation_id", "-- name: QuotationLines", "/api/v1/quotations/"},
		"bill":             {"bills", "bill_line_items", "bill_id", "-- name: BillLines", "/api/v1/bills/"},
	}
	spec, ok := specs[kind]
	if !ok {
		t.Fatal("invalid snapshot kind")
	}
	t.Helper()
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var oldAmount, oldDescription string
	if err = pool.QueryRow(ctx, `SELECT amount::text FROM `+spec.table+` WHERE `+spec.id+`=$1`, invoice).Scan(&oldAmount); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT description FROM `+spec.lines+` WHERE `+spec.id+`=$1 LIMIT 1`, invoice).Scan(&oldDescription); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, e := pool.Exec(ctx, `UPDATE `+spec.table+` SET amount=$2::numeric WHERE `+spec.id+`=$1`, invoice, oldAmount); e != nil {
			t.Errorf("restore invoice: %v", e)
		}
		if _, e := pool.Exec(ctx, `UPDATE `+spec.lines+` SET description=$2 WHERE `+spec.id+`=$1`, invoice, oldDescription); e != nil {
			t.Errorf("restore line: %v", e)
		}
	}()
	path := spec.path + invoice
	if kind == "customer-summary" {
		var customer string
		if err = pool.QueryRow(ctx, `SELECT customer_id::text FROM invoices WHERE invoice_id=$1`, invoice).Scan(&customer); err != nil {
			t.Fatal(err)
		}
		path = spec.path + customer + "/billing-summary"
	}
	gate := &invoiceLineGate{marker: spec.marker, reached: make(chan struct{}), release: make(chan struct{})}
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.Tracer = gate
	routePool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer routePool.Close()
	var release sync.Once
	defer release.Do(func() { close(gate.release) })
	scope, err := business.Parse(company)
	if err != nil {
		t.Fatal(err)
	}
	secret := []byte("0123456789abcdef0123456789abcdef")
	handler, err := platform.NewScopedProfileRouter(routePool, platform.SessionConfig{Secret: secret, Path: "/", SameSite: http.SameSiteLaxMode}, scope)
	if err != nil {
		t.Fatal(err)
	}
	signed, err := (auth.Tokens{Secret: secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": user, "business_id": company})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		r := httptest.NewRequest("GET", path, nil)
		r.AddCookie(&http.Cookie{Name: "session", Value: signed})
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		done <- w
	}()
	select {
	case <-gate.reached:
	case <-time.After(5 * time.Second):
		t.Fatal("invoice lines did not reach query tracer")
	}
	update, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer update.Rollback(ctx)
	if _, err = update.Exec(ctx, `UPDATE `+spec.table+` SET amount=amount+1 WHERE `+spec.id+`=$1`, invoice); err != nil {
		t.Fatal(err)
	}
	if _, err = update.Exec(ctx, `UPDATE `+spec.lines+` SET description='new snapshot value' WHERE `+spec.id+`=$1`, invoice); err != nil {
		t.Fatal(err)
	}
	if err = update.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	release.Do(func() { close(gate.release) })
	select {
	case w := <-done:
		var payload map[string]any
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &payload) != nil {
			t.Fatalf("snapshot response: %d %s", w.Code, w.Body.String())
		}
		if kind == "customer-summary" {
			rows, ok := payload["unpaid_invoices"].([]any)
			if !ok || len(rows) != 1 {
				t.Fatalf("snapshot unpaid invoices: %v", payload)
			}
			payload, ok = rows[0].(map[string]any)
			if !ok {
				t.Fatalf("snapshot unpaid invoice: %v", rows[0])
			}
		}
		lines, ok := payload["line_items"].([]any)
		if !ok || len(lines) != 1 {
			t.Fatalf("snapshot lines: %v", payload["line_items"])
		}
		line, ok := lines[0].(map[string]any)
		if !ok || line["description"] != oldDescription || payload["amount"] != oldAmount {
			t.Fatalf("mixed invoice snapshot: %v", payload)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("invoice snapshot request did not finish")
	}
}
