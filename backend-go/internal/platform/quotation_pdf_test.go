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
	"os/exec"
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

func TestQuotationPDFPostgres(t *testing.T) {
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
	_, err = p.Exec(context.Background(), `INSERT INTO users(user_id,email,password_hash,role) VALUES ('00000000-0000-0000-0000-000000000001','pdf@example.com','x','admin'),('00000000-0000-0000-0000-000000000002','user@example.com','x','user'); INSERT INTO customers(customer_id,name) VALUES ('10000000-0000-0000-0000-000000000001','Café Customer'); INSERT INTO quotations(quotation_id,customer_id,quotation_number,currency,amount,status) VALUES ('20000000-0000-0000-0000-000000000001','10000000-0000-0000-0000-000000000001','Q-1','USD',10,'DRAFT'); UPDATE business_profile SET name='Studio Example',address='Business Address',contact_email='studio@example.com',default_notes='Default memo' WHERE id=1; INSERT INTO payment_account(business_profile_id,currency,instructions,is_default) VALUES (1,'USD','USD bank instructions',true),(1,'IDR','IDR bank instructions',false); UPDATE customers SET billing_address1='Customer Address',contact_email='client@example.com' WHERE customer_id='10000000-0000-0000-0000-000000000001'; UPDATE quotations SET subtotal=12.50,discount_type='PERCENT',discount_value=20,issue_date='2026-01-02',valid_until='2026-03-01',payment_terms='Net 30',notes='Quote memo' WHERE quotation_id='20000000-0000-0000-0000-000000000001'; INSERT INTO quotation_line_items(line_item_id,quotation_id,description,position,quantity,unit_price,amount) SELECT ('30000000-0000-0000-0000-' || lpad(i::text,12,'0'))::uuid,'20000000-0000-0000-0000-000000000001','Service line ' || i,i,2,5,10 FROM generate_series(1,75) i`)
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
	path := "/api/v1/quotations/20000000-0000-0000-0000-000000000001/pdf"
	for _, tc := range []struct {
		path string
		c    *http.Cookie
		want int
	}{{path, nil, 401}, {path, cookie("00000000-0000-0000-0000-000000000002"), 403}, {"/api/v1/quotations/bad/pdf", cookie("00000000-0000-0000-0000-000000000001"), 422}, {"/api/v1/quotations/20000000-0000-0000-0000-000000000099/pdf", cookie("00000000-0000-0000-0000-000000000001"), 404}, {path, cookie("00000000-0000-0000-0000-000000000001"), 200}} {
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
				if !strings.Contains(pageText, "QUOTATION") || !strings.Contains(pageText, "Description") {
					t.Fatalf("page %d missing repeated header: %s", i, pageText)
				}
			}
			if reader.NumPage() < 2 || !strings.Contains(string(b), "Service line 75") || !strings.Contains(string(b), "Customer") || !strings.Contains(string(b), "10.00") {
				t.Fatalf("missing PDF text: %q", b)
			}
		}
		if tc.want == 200 && (w.Header().Get("Content-Type") != "application/pdf" || w.Header().Get("Content-Disposition") != `inline; filename=quotation-Q-1.pdf` || !strings.HasPrefix(w.Body.String(), "%PDF-")) {
			t.Fatalf("bad PDF response: %v", w.Header())
		}
	}

	// Hold the line table after the handler reads the quotation, then commit a
	// concurrent quotation+line edit before letting the line query proceed.
	tx, err := p.Begin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(context.Background(), `LOCK TABLE quotation_line_items IN ACCESS EXCLUSIVE MODE`); err != nil {
		t.Fatal(err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- call(path, cookie("00000000-0000-0000-0000-000000000001")) }()
	deadline := time.Now().Add(10 * time.Second)
	blocked := false
	for time.Now().Before(deadline) {
		var n int
		if err = p.QueryRow(context.Background(), `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%FROM quotation_line_items WHERE quotation_id=ANY%'`).Scan(&n); err != nil {
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
	if _, err = tx.Exec(context.Background(), `UPDATE quotations SET amount=999 WHERE quotation_id='20000000-0000-0000-0000-000000000001'; UPDATE quotation_line_items SET description='Concurrent new line' WHERE position=1`); err != nil {
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
	if _, err = p.Exec(context.Background(), `UPDATE quotations SET amount=10 WHERE quotation_id='20000000-0000-0000-0000-000000000001'; UPDATE quotation_line_items SET description='Service line 1' WHERE position=1`); err != nil {
		t.Fatal(err)
	}

	if _, err = p.Exec(context.Background(), `UPDATE quotation_line_items SET unit_price=999999999999999.9999,amount=999999999999999.9999 WHERE position=1`); err != nil {
		t.Fatal(err)
	}
	large := call(path, cookie("00000000-0000-0000-0000-000000000001"))
	lr, err := pdf.NewReader(bytes.NewReader(large.Body.Bytes()), int64(large.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for i := 1; i <= lr.NumPage(); i++ {
		groups := map[[2]float64]string{}
		for _, run := range lr.Page(i).Content().Text {
			key := [2]float64{run.X, run.Y}
			groups[key] += run.S
		}
		for _, v := range groups {
			if v == "1,000,000,000,000,000.00" {
				found++
			}
		}
	}
	if found != 2 {
		t.Fatalf("large numbers split or missing: %d", found)
	}
	if _, err = p.Exec(context.Background(), `UPDATE quotation_line_items SET unit_price=5,amount=10 WHERE position=1`); err != nil {
		t.Fatal(err)
	}
	// Long descriptions and notes must flow across pages without losing content.
	longText := strings.Repeat("项目说明", 30) + "\n" + strings.Repeat("https://example.com/verylongunbrokenpath", 100) + "ENDMARKER"
	if _, err = p.Exec(context.Background(), `WITH updated AS (UPDATE quotation_line_items SET description=$1 WHERE position=1) UPDATE quotations SET notes=$1 WHERE quotation_id='20000000-0000-0000-0000-000000000001'`, longText); err != nil {
		t.Fatal(err)
	}
	longPDF := call(path, cookie("00000000-0000-0000-0000-000000000001"))
	if output := os.Getenv("QUOTATION_LONG_PDF_PREVIEW"); output != "" {
		if err := os.WriteFile(output, longPDF.Body.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
	}
	reader, err := pdf.NewReader(bytes.NewReader(longPDF.Body.Bytes()), int64(longPDF.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := reader.GetPlainText()
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(plain)
	if err != nil {
		t.Fatal(err)
	}
	notesOnly := false
	for i := 1; i <= reader.NumPage(); i++ {
		pt, e := reader.Page(i).GetPlainText(nil)
		if e != nil {
			t.Fatal(e)
		}
		if strings.Contains(pt, "https://") && !strings.Contains(pt, "Service line") && !strings.Contains(pt, "Description") {
			notesOnly = true
		}
	}
	if !notesOnly {
		t.Fatal("no notes-only page without table header")
	}
	if !strings.Contains(string(body), "项目说明") {
		t.Fatal("missing CJK text")
	}
	for i := 1; i <= reader.NumPage(); i++ {
		for _, run := range reader.Page(i).Content().Text {
			if run.X < 0 || run.X+run.W > 596 {
				t.Fatalf("text outside page: %+v", run)
			}
		}
	}
	if strings.Count(string(body), "ENDMARKER") != 2 {
		t.Fatalf("long content lost: %d markers", strings.Count(string(body), "ENDMARKER"))
	}
	if _, err = p.Exec(context.Background(), `UPDATE quotation_line_items SET description='Service line 1' WHERE position=1; UPDATE quotations SET notes='Quote memo' WHERE quotation_id='20000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	for _, currency := range []string{"USD", "IDR"} {
		if _, err = p.Exec(context.Background(), `UPDATE quotations SET currency=$1 WHERE quotation_id='20000000-0000-0000-0000-000000000001'`, currency); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("python", "-c", `import sys,os
from fastapi import FastAPI
from fastapi.testclient import TestClient
from sqlalchemy import create_engine
from sqlalchemy.orm import Session
from app.api.v1.routers.quotations import router
from app.api.deps import current_admin
from app.db.session import get_db
engine=create_engine(os.environ['DB'].replace('postgres://','postgresql+psycopg://',1))
a=FastAPI();a.include_router(router,prefix='/api/v1');a.dependency_overrides[current_admin]=lambda:object()
def session():
 with Session(engine) as db: yield db
a.dependency_overrides[get_db]=session
with TestClient(a) as c:
 r=c.get('/api/v1/quotations/20000000-0000-0000-0000-000000000001/pdf');r.raise_for_status();sys.stdout.buffer.write(r.content)`)
		cmd.Env = append(os.Environ(), "DB="+db.URL())
		py, err := cmd.Output()
		if err != nil {
			t.Fatalf("Python PDF: %v", err)
		}
		goPDF := call(path, cookie("00000000-0000-0000-0000-000000000001"))
		if output := os.Getenv("QUOTATION_PDF_PREVIEW"); output != "" && currency == "USD" {
			if err := os.WriteFile(output, goPDF.Body.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if goPDF.Code != 200 {
			t.Fatal(goPDF.Body)
		}
		extract := func(data []byte) string {
			r, e := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
			if e != nil {
				t.Fatal(e)
			}
			out, e := r.GetPlainText()
			if e != nil {
				t.Fatal(e)
			}
			b, e := io.ReadAll(out)
			if e != nil {
				t.Fatal(e)
			}
			return string(b)
		}
		goText, pyText := extract(goPDF.Body.Bytes()), extract(py)
		expected := []string{"Q-1", "Studio Example", "Business Address", "Customer Address", "Café Customer", "client@example.com", "Quote memo", "Net 30", "Service line 75", "20%", currency + " bank instructions"}
		if currency == "USD" {
			expected = append(expected, "12.50", "10.00")
		} else {
			expected = append(expected, "12", "10")
		}
		for _, v := range expected {
			if !strings.Contains(goText, v) || !strings.Contains(pyText, v) {
				t.Errorf("%s missing %q in Go=%t Python=%t", currency, v, strings.Contains(goText, v), strings.Contains(pyText, v))
			}
		}
	}

	if _, err = p.Exec(context.Background(), `UPDATE quotations SET quotation_number=NULL,notes=NULL,currency='EUR' WHERE quotation_id='20000000-0000-0000-0000-000000000001'`); err != nil {
		t.Fatal(err)
	}
	fallback := call(path, cookie("00000000-0000-0000-0000-000000000001"))
	if fallback.Header().Get("Content-Disposition") != `inline; filename=quotation-20000000.pdf` {
		t.Fatalf("fallback filename: %s", fallback.Header().Get("Content-Disposition"))
	}
	fr, err := pdf.NewReader(bytes.NewReader(fallback.Body.Bytes()), int64(fallback.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	ft, err := fr.GetPlainText()
	if err != nil {
		t.Fatal(err)
	}
	fb, err := io.ReadAll(ft)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{"Default memo", "USD bank instructions"} {
		if !strings.Contains(string(fb), v) {
			t.Errorf("fallback PDF missing %q", v)
		}
	}

	reference := `R";` + strings.Repeat("X", 47)
	if _, err = p.Exec(context.Background(), `UPDATE quotations SET quotation_number=$1 WHERE quotation_id='20000000-0000-0000-0000-000000000001'`, reference); err != nil {
		t.Fatal(err)
	}
	long := call(path, cookie("00000000-0000-0000-0000-000000000001"))
	disposition, params, err := mime.ParseMediaType(long.Header().Get("Content-Disposition"))
	if err != nil || disposition != "inline" || params["filename"] != "quotation-"+reference+".pdf" {
		t.Fatalf("unsafe filename: %q %v", long.Header().Get("Content-Disposition"), err)
	}
	lr, err = pdf.NewReader(bytes.NewReader(long.Body.Bytes()), int64(long.Body.Len()))
	if err != nil {
		t.Fatal(err)
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

}
