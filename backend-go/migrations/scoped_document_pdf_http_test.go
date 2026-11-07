package migrations

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	pdf "github.com/ledongthuc/pdf"
	"github.com/wenhaokho/wenhao-billing/backend/internal/business"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

func testScopedDocumentPDF(t *testing.T, pool *pgxpool.Pool, a, b, aUser, bUser string, secret []byte, token func(string, string) string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	insert := func(sql string, args ...any) string {
		t.Helper()
		var id string
		if err := pool.QueryRow(ctx, sql, args...).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	owners := []scopedDocumentOwner{{name: "Alpha", id: a, user: aUser}, {name: "Bravo", id: b, user: bUser}}
	for i := range owners {
		o := &owners[i]
		scope, err := business.Parse(o.id)
		if err != nil {
			t.Fatal(err)
		}
		o.router, err = platform.NewScopedProfileRouter(pool, platform.SessionConfig{Secret: secret, Path: "/", SameSite: http.SameSiteLaxMode}, scope)
		if err != nil {
			t.Fatal(err)
		}
		exec("UPDATE business_profile SET name=$2, logo_url=NULL WHERE business_id=$1", o.id, o.name+" Studio")
		exec("DELETE FROM payment_account WHERE business_id=$1 AND currency='USD'", o.id)
		exec("INSERT INTO payment_account(business_id,business_profile_id,currency,instructions,is_default) SELECT business_id,id,'USD',$2,false FROM business_profile WHERE business_id=$1", o.id, o.name+" Bank")
		o.customer = insert("INSERT INTO customers(customer_id,business_id,name,billing_address1,contact_email) VALUES(gen_random_uuid(),$1,$2,$3,$4) RETURNING customer_id::text", o.id, o.name+" Customer", o.name+" Address", strings.ToLower(o.name)+"@example.test")
	}
	request := func(o scopedDocumentOwner, path, session string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
		if session != "" {
			req.AddCookie(&http.Cookie{Name: "session", Value: session})
		}
		w := httptest.NewRecorder()
		o.router.ServeHTTP(w, req)
		return w
	}
	parse := func(t *testing.T, w *httptest.ResponseRecorder, filename string) string {
		t.Helper()
		if w.Code != 200 || w.Header().Get("Content-Type") != "application/pdf" ||
			w.Header().Get("Content-Disposition") != "inline; filename="+filename {
			t.Fatalf("PDF response %d %v", w.Code, w.Header())
		}
		return scopedPDFText(t, w.Body.Bytes())
	}
	for _, kind := range []string{"quotation", "invoice"} {
		t.Run(kind, func(t *testing.T) {
			var ids []string
			for i, o := range owners {
				amount := []string{"123.45", "678.90"}[i]
				var id string
				if kind == "quotation" {
					id = insert("INSERT INTO quotations(quotation_id,business_id,customer_id,quotation_number,currency,subtotal,amount,status) VALUES(gen_random_uuid(),$1,$2,$3,'USD',$4::numeric,$4::numeric,'DRAFT') RETURNING quotation_id::text", o.id, o.customer, o.name+"-PDF", amount)
				} else {
					id = insert("INSERT INTO invoices(invoice_id,business_id,customer_id,invoice_number,invoice_type,currency,subtotal,amount,balance_due,status) VALUES(gen_random_uuid(),$1,$2,$3,'MILESTONE','USD',$4::numeric,$4::numeric,$4::numeric,'DRAFT') RETURNING invoice_id::text", o.id, o.customer, o.name+"-PDF", amount)
				}
				exec(fmt.Sprintf("INSERT INTO %[1]s_line_items(line_item_id,business_id,%[1]s_id,description,position,quantity,unit_price,amount) VALUES(gen_random_uuid(),$1,$2,$3,1,1,$4::numeric,$4::numeric)", kind), o.id, id, o.name+" Service", amount)
				ids = append(ids, id)
			}
			path := func(id string) string { return "/api/v1/" + kind + "s/" + id + "/pdf" }
			for i, o := range owners {
				other := owners[1-i]
				session := token(o.user, o.id)
				foreign := request(o, path(ids[1-i]), session)
				missing := request(o, path("ffffffff-ffff-ffff-ffff-ffffffffffff"), session)
				if foreign.Code != 404 || missing.Code != 404 || foreign.Body.String() != missing.Body.String() {
					t.Fatalf("foreign and missing responses differ: %d %s / %d %s", foreign.Code, foreign.Body, missing.Code, missing.Body)
				}
				for _, check := range []struct {
					session string
					status  int
				}{
					{"", 401}, {token(other.user, other.id), 401}, {token(other.user, o.id), 403},
				} {
					if w := request(o, path(ids[i]), check.session); w.Code != check.status {
						t.Fatalf("authentication response %d, want %d", w.Code, check.status)
					}
				}
				if w := request(o, path("malformed"), session); w.Code != 422 {
					t.Fatalf("invalid ID response %d: %s", w.Code, w.Body)
				}
				content := parse(t, request(o, path(ids[i]), session), kind+"-"+o.name+"-PDF.pdf")
				for _, expected := range []string{o.name + " Studio", o.name + " Customer", o.name + " Address", o.name + " Bank", o.name + " Service", []string{"123.45", "678.90"}[i]} {
					if !strings.Contains(content, expected) {
						t.Errorf("missing %q in PDF: %q", expected, content)
					}
				}
				if strings.Contains(content, other.name) || strings.Contains(content, []string{"123.45", "678.90"}[1-i]) {
					t.Fatalf("foreign data in PDF: %q", content)
				}
			}
			if kind == "quotation" {
				t.Run("Send", func(t *testing.T) {
					testScopedQuotationSend(t, pool, owners, ids, secret, token)
				})
			} else {
				t.Run("Send", func(t *testing.T) {
					testScopedInvoiceSend(t, pool, owners, ids, secret, token)
				})
				t.Run("Receipt", func(t *testing.T) {
					testScopedReceiptSend(t, pool, owners, ids, secret, token)
				})
			}
			t.Run("Snapshot", func(t *testing.T) {
				o := owners[0]
				marker := "-- name: QuotationLines"
				if kind == "invoice" {
					marker = "-- name: InvoiceLinesByIDs"
				}
				gate := &invoiceLineGate{marker: marker, reached: make(chan struct{}), release: make(chan struct{})}
				config := pool.Config()
				config.ConnConfig.Tracer = gate
				readPool, err := pgxpool.NewWithConfig(ctx, config)
				if err != nil {
					t.Fatal(err)
				}
				defer readPool.Close()
				defer func() {
					select {
					case <-gate.release:
					default:
						close(gate.release)
					}
				}()
				scope, err := business.Parse(o.id)
				if err != nil {
					t.Fatal(err)
				}
				o.router, err = platform.NewScopedProfileRouter(readPool, platform.SessionConfig{Secret: secret, Path: "/", SameSite: http.SameSiteLaxMode}, scope)
				if err != nil {
					t.Fatal(err)
				}
				session := token(o.user, o.id)
				done := make(chan *httptest.ResponseRecorder, 1)
				go func() { done <- request(o, path(ids[0]), session) }()
				select {
				case <-gate.reached:
				case <-ctx.Done():
					t.Fatal("PDF line query did not reach the tracer")
				}

				tx, err := pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback(ctx)
				for _, sql := range []string{
					fmt.Sprintf("UPDATE %[1]ss SET amount=999.99 WHERE business_id=$1 AND %[1]s_id=$2", kind),
					fmt.Sprintf("UPDATE %[1]s_line_items SET description='New Service' WHERE business_id=$1 AND %[1]s_id=$2", kind),
				} {
					if _, err = tx.Exec(ctx, sql, o.id, ids[0]); err != nil {
						t.Fatal(err)
					}
				}
				if err = tx.Commit(ctx); err != nil {
					t.Fatal(err)
				}
				close(gate.release)

				filename := kind + "-" + o.name + "-PDF.pdf"
				select {
				case w := <-done:
					content := parse(t, w, filename)
					if !strings.Contains(content, "Alpha Service") || !strings.Contains(content, "123.45") ||
						strings.Contains(content, "New Service") || strings.Contains(content, "999.99") {
						t.Fatalf("PDF mixed snapshots: %q", content)
					}
				case <-ctx.Done():
					t.Fatal("PDF request did not finish")
				}
				content := parse(t, request(o, path(ids[0]), session), filename)
				if !strings.Contains(content, "New Service") || !strings.Contains(content, "999.99") {
					t.Fatalf("fresh PDF missed the committed edit: %q", content)
				}
			})

		})
	}
}

type scopedDocumentOwner struct {
	name, id, user, customer string
	router                   http.Handler
}

func scopedPDFText(t *testing.T, data []byte) string {
	t.Helper()
	doc, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	stream, err := doc.GetPlainText()
	if err != nil {
		t.Fatal(err)
	}
	data, err = io.ReadAll(stream)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
