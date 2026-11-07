package platform

import (
	"bytes"
	"context"
	pdf "github.com/ledongthuc/pdf"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/LOQATO/gokit/auth"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/goosemigrator"
	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

func TestInvoicePDFPostgres(t *testing.T) {
	u, err := url.Parse(os.Getenv("PGTESTDB_URL"))
	if err != nil || u.User == nil {
		t.Fatal("PGTESTDB_URL required")
	}
	pw, _ := u.User.Password()
	db := pgtestdb.Custom(t, pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: pw, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}, goosemigrator.New(".", goosemigrator.WithFS(migrations.Files)))
	p, err := pgxpool.New(context.Background(), db.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	_, err = p.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','pdf@example.com','x','admin'),('00000000-0000-0000-0000-000000000002','user@example.com','x','user'); INSERT INTO customers(customer_id,name) VALUES ('10000000-0000-0000-0000-000000000001','Café Customer'); INSERT INTO invoices(invoice_id,customer_id,invoice_number,currency,amount,balance_due,invoice_type,status) VALUES ('20000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','I-1','USD',10,10,'MILESTONE','DRAFT'); UPDATE business_profile SET name='Studio Example',address='Business Address',contact_email='studio@example.com',default_notes='Default memo' WHERE id=1; INSERT INTO payment_account(business_profile_id,currency,instructions,is_default) VALUES (1,'USD','USD bank instructions',true),(1,'IDR','IDR bank instructions',false); UPDATE customers SET billing_address1='Customer Address',contact_email='client@example.com' WHERE customer_id='10000000-0000-0000-0000-000000000001'; UPDATE invoices SET subtotal=12.50,discount_type='PERCENT',discount_value=20,issue_date='2026-01-02',due_date='2026-03-01',payment_terms='Net 30',notes='Invoice memo' WHERE invoice_id='20000000-0000-0000-0000-000000000001'; INSERT INTO invoice_line_items(line_item_id,invoice_id,description,position,quantity,unit_price,amount) SELECT ('30000000-0000-0000-0000-' || lpad(i::text,12,'0'))::uuid,'20000000-0000-0000-0000-000000000001','Service line ' || i,i,2,5,10 FROM generate_series(1,75) i`)
	if err != nil {
		t.Fatal(err)
	}
	cfg := SessionConfig{Secret: []byte("0123456789abcdef0123456789abcdef"), Path: "/", SameSite: http.SameSiteLaxMode}
	h, err := NewHandlerWithSessions(p, nil, cfg)
	if err != nil {
		t.Fatal(err)
	}
	cookie := func(id string) *http.Cookie {
		v, e := (auth.Tokens{Secret: cfg.Secret, AccessTTL: time.Hour}).Issue(jwt.MapClaims{"sub": id})
		if e != nil {
			t.Fatal(e)
		}
		return &http.Cookie{Name: "session", Value: v}
	}
	call := func(path string, c *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		if c != nil {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	path := "/api/v1/invoices/20000000-0000-0000-0000-000000000001/pdf"
	for _, tc := range []struct {
		path string
		c    *http.Cookie
		want int
	}{{path, nil, 401}, {path, cookie("00000000-0000-0000-0000-000000000002"), 403}, {"/api/v1/invoices/bad/pdf", cookie("00000000-0000-0000-0000-000000000001"), 422}, {"/api/v1/invoices/20000000-0000-0000-0000-000000000099/pdf", cookie("00000000-0000-0000-0000-000000000001"), 404}, {path, cookie("00000000-0000-0000-0000-000000000001"), 200}} {
		w := call(tc.path, tc.c)
		if w.Code != tc.want {
			t.Fatalf("%s: got %d, want %d: %s", tc.path, w.Code, tc.want, w.Body.String())
		}
		if tc.want == 200 {
			reader, err := pdf.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
			if err != nil {
				t.Fatal(err)
			}
			text, err := reader.GetPlainText()
			if err != nil {
				t.Fatal(err)
			}
			b, err := io.ReadAll(text)
			if err != nil {
				t.Fatal(err)
			}
			for i := 1; i <= reader.NumPage(); i++ {
				pageText, e := reader.Page(i).GetPlainText(nil)
				if e != nil {
					t.Fatal(e)
				}
				if i == 1 && (strings.Index(pageText, "Bill To") < 0 || strings.Index(pageText, "Description") < strings.Index(pageText, "Bill To")) {
					t.Fatalf("table header precedes customer details: %s", pageText)
				}
				if !strings.Contains(pageText, "INVOICE") || !strings.Contains(pageText, "Description") {
					t.Fatalf("page %d missing repeated header: %s", i, pageText)
				}
			}
			if reader.NumPage() < 2 || !strings.Contains(string(b), "Service line 75") || !strings.Contains(string(b), "Customer") || !strings.Contains(string(b), "10.00") {
				t.Fatalf("missing PDF text: %q", b)
			}
		}
		if tc.want == 200 && (w.Header().Get("Content-Type") != "application/pdf" || w.Header().Get("Content-Disposition") != `inline; filename=invoice-I-1.pdf` || !strings.HasPrefix(w.Body.String(), "%PDF-")) {
			t.Fatalf("bad PDF response: %v", w.Header())
		}
	}

	// Hold the line table after the handler reads the invoice, then commit a
	// concurrent invoice+line edit before letting the line query proceed.
	tx, err := p.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(context.Background(), `LOCK TABLE invoice_line_items IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- call(path, cookie("00000000-0000-0000-0000-000000000001")) }()
	deadline := time.Now().Add(10 * time.Second)
	blocked := false
	for time.Now().Before(deadline) {
		var n int
		if err = p.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%FROM invoice_line_items WHERE invoice_id = ANY%'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			blocked = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !blocked {
		tx.Rollback(context.Background())
		t.Fatal("PDF line query did not block")
	}
	if _, err = tx.Exec(context.Background(), `UPDATE invoices SET amount=999 WHERE invoice_id='20000000-0000-0000-0000-000000000001'; UPDATE invoice_line_items SET description='Concurrent new line' WHERE position=1`); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	snapshot := <-done
	sr, err := pdf.NewReader(bytes.NewReader(snapshot.Body.Bytes()), int64(snapshot.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	st, err := sr.GetPlainText()
	if err != nil {
		t.Fatal(err)
	}
	sb, err := io.ReadAll(st)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(sb), "Concurrent new line") || strings.Contains(string(sb), "999.00") || !strings.Contains(string(sb), "Service line 1") {
		t.Fatal("PDF mixed data from different commits")
	}
	if _, err = p.Exec(context.Background(), `UPDATE invoices SET amount=10 WHERE invoice_id='20000000-0000-0000-0000-000000000001'; UPDATE invoice_line_items SET description='Service line 1' WHERE position=1`); err != nil {
		t.Fatal(err)
	}

	_, err = p.Exec(context.Background(), `UPDATE invoices SET currency='IDR', subtotal=100000, amount=102040.82, balance_due=72040.82, wht_type='PPH23', wht_rate=2, wht_gross_up=true, wht_base=100000, wht_amount=2040.82, wht_booked=1000 WHERE invoice_id='20000000-0000-0000-0000-000000000001'; UPDATE business_profile SET npwp='12345',npwp_name='Tax Name' WHERE id=1`)
	if err != nil {
		t.Fatal(err)
	}
	w := call(path, cookie("00000000-0000-0000-0000-000000000001"))

	r, err := pdf.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	stream, err := r.GetPlainText()
	if err != nil {
		t.Fatal(err)
	}
	content, err := io.ReadAll(stream)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"PPh 23 (2%)", "2,041", "102,041", "PPh 23 withholding (2%)", "Paid", "29,000", "Amount to Transfer", "71,000", "NPWP 12345 a.n. Tax Name", "IDR bank instructions"} {
		if !strings.Contains(string(content), value) {
			t.Errorf("missing %q in %s", value, content)
		}
	}

	// Long mastheads must end above all body text, including repeated headers.
	longTitle := strings.Repeat("A\n", 122) + "ENDTITLE"
	reference := `R";` + strings.Repeat("X", 47)
	_, err = p.Exec(context.Background(), `UPDATE business_profile SET invoice_title=$1,invoice_summary='Summary marker' WHERE id=1`, longTitle)
	if err != nil {
		t.Fatal(err)
	}
	_, err = p.Exec(context.Background(), `UPDATE invoices SET invoice_number=$1,po_so_number='PO marker',payment_terms='Net 30',footer='Footer marker' WHERE invoice_id='20000000-0000-0000-0000-000000000001'`, reference)
	if err != nil {
		t.Fatal(err)
	}
	long := call(path, cookie("00000000-0000-0000-0000-000000000001"))
	disposition, params, err := mime.ParseMediaType(long.Header().Get("Content-Disposition"))
	if err != nil || disposition != "inline" || params["filename"] != "invoice-"+reference+".pdf" {
		t.Fatalf("unsafe filename: %q %v", long.Header().Get("Content-Disposition"), err)
	}
	lr, err := pdf.NewReader(bytes.NewReader(long.Body.Bytes()), int64(long.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if lr.NumPage() < 2 || lr.NumPage() > 8 {
		t.Fatalf("unexpected page count: %d", lr.NumPage())
	}
	for i := 1; i <= lr.NumPage(); i++ {
		minTitle, maxBody := 10000.0, 0.0
		for _, part := range lr.Page(i).Content().Text {
			if part.FontSize > 17 {
				if part.Y < minTitle {
					minTitle = part.Y
				}
			} else if part.FontSize >= 8 && part.Y > maxBody {
				maxBody = part.Y
			}
		}
		if minTitle == 10000 || minTitle-maxBody < 10 {
			t.Fatalf("page %d masthead overlaps body: title y=%f body y=%f", i, minTitle, maxBody)
		}
	}
	plain, err := lr.GetPlainText()
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(plain)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "Net 30") {
		t.Fatal("default invoice must suppress payment terms")
	}
	for _, marker := range []string{"ENDTITLE", "Summary marker", "PO marker", "Footer marker", "Invoice No.", "Issue Date"} {
		if !strings.Contains(string(data), marker) {
			t.Errorf("missing %q", marker)
		}
	}

}
