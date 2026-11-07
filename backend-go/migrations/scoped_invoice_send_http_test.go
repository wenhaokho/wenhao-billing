package migrations

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wenhaokho/wenhao-billing/backend/internal/business"
	"github.com/wenhaokho/wenhao-billing/backend/internal/email"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

func testScopedInvoiceSend(t *testing.T, pool *pgxpool.Pool, owners []scopedDocumentOwner, ids []string, secret []byte, token func(string, string) string) {
	t.Helper()
	ctx, stop := context.WithTimeout(context.Background(), 15*time.Second)
	defer stop()
	newRouter := func(pool *pgxpool.Pool, o scopedDocumentOwner, sender scopedQuotationSender) http.Handler {
		t.Helper()
		scope, err := business.Parse(o.id)
		if err != nil {
			t.Fatal(err)
		}
		router, err := platform.NewScopedProfileRouter(pool, platform.SessionConfig{
			Secret: secret, Path: "/", SameSite: http.SameSiteLaxMode, Sender: sender,
		}, scope)
		if err != nil {
			t.Fatal(err)
		}
		return router
	}
	call := func(ctx context.Context, router http.Handler, id, session string, override ...string) *httptest.ResponseRecorder {
		body := "{}"
		if len(override) != 0 {
			body = override[0]
		}
		req := httptest.NewRequest(http.MethodPost, "/api/v1/invoices/"+id+"/send", strings.NewReader(body)).WithContext(ctx)
		req.Header.Set("Content-Type", "application/json")
		if session != "" {
			req.AddCookie(&http.Cookie{Name: "session", Value: session})
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	expect := func(w *httptest.ResponseRecorder, want int) {
		t.Helper()
		if w.Code != want {
			t.Fatalf("send response %d, want %d: %s", w.Code, want, w.Body)
		}
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	checkStatus := func(i int, want string) {
		t.Helper()
		var got string
		err := pool.QueryRow(ctx, "SELECT status FROM invoices WHERE business_id=$1 AND invoice_id=$2", owners[i].id, ids[i]).Scan(&got)
		if err != nil || got != want {
			t.Fatalf("invoice status %s, want %s: %v", got, want, err)
		}
	}
	for i, owner := range owners {
		other := owners[1-i]
		id := ids[i]
		session := token(owner.user, owner.id)
		var messages []email.Message
		providerErr := errors.New("fake provider failure")
		cancelRequest := func() {}
		router := newRouter(pool, owner, func(_ context.Context, msg email.Message) error {
			messages = append(messages, msg)
			cancelRequest()
			return providerErr
		})
		foreign := call(ctx, router, ids[1-i], session)
		missing := call(ctx, router, "ffffffff-ffff-ffff-ffff-ffffffffffff", session)
		expect(foreign, 404)
		expect(missing, 404)
		if foreign.Body.String() != missing.Body.String() {
			t.Fatal("foreign and missing invoices have different responses")
		}
		expect(call(ctx, router, id, ""), 401)
		expect(call(ctx, router, id, token(other.user, other.id)), 401)
		expect(call(ctx, router, id, token(other.user, owner.id)), 403)
		expect(call(ctx, router, "bad-id", session), 422)

		exec("UPDATE invoices SET due_date='infinity' WHERE business_id=$1 AND invoice_id=$2", owner.id, id)
		expect(call(ctx, router, id, session), 500)
		exec("UPDATE invoices SET due_date='2026-12-31',wht_type='PPH23',wht_amount=10 WHERE business_id=$1 AND invoice_id=$2", owner.id, id)
		exec("UPDATE invoice_line_items SET amount='NaN' WHERE business_id=$1 AND invoice_id=$2", owner.id, id)
		expect(call(ctx, router, id, session), 500)
		exec("UPDATE invoice_line_items SET amount=unit_price WHERE business_id=$1 AND invoice_id=$2", owner.id, id)
		if len(messages) != 0 {
			t.Fatal("rejected sends reached the provider")
		}
		checkStatus(i, "DRAFT")
		expect(call(ctx, router, id, session), 500)
		checkStatus(i, "DRAFT")
		if len(messages) != 1 {
			t.Fatalf("provider calls %d, want 1", len(messages))
		}

		providerErr = nil
		reqCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		cancelRequest = cancel
		w := call(reqCtx, router, id, session)
		expect(w, 200)
		checkStatus(i, "SENT")
		if len(messages) != 2 || !strings.Contains(w.Body.String(), `"status":"SENT"`) {
			t.Fatalf("unexpected response %s and %d deliveries", w.Body, len(messages))
		}
		msg := messages[1]
		cash := []string{"113.45", "668.90"}[i]
		for _, part := range []string{owner.name + " Customer", "after withholding tax): " + cash + " USD", "Due date: 2026-12-31"} {
			if !strings.Contains(msg.Text, part) {
				t.Fatalf("missing %q in message %q", part, msg.Text)
			}
		}
		if len(msg.To) != 1 || msg.To[0] != strings.ToLower(owner.name)+"@example.test" ||
			len(msg.Attachments) != 1 || msg.Attachments[0].Filename != "invoice-"+owner.name+"-PDF.pdf" ||
			msg.Attachments[0].ContentType != "application/pdf" {
			t.Fatalf("wrong recipient or attachment: %v", msg.To)
		}
		content := scopedPDFText(t, msg.Attachments[0].Content)
		for _, part := range []string{" Studio", " Customer", " Bank", " Service"} {
			if !strings.Contains(content, owner.name+part) || strings.Contains(content, other.name+part) {
				t.Fatalf("wrong PDF content: %q", content)
			}
		}
		checkStatus(1-i, []string{"DRAFT", "SENT"}[i])
	}

	owner := owners[0]
	id := ids[0]
	session := token(owner.user, owner.id)
	calls := make(chan email.Message, 2)
	sender := func(_ context.Context, msg email.Message) error {
		calls <- msg
		return nil
	}
	router := newRouter(pool, owner, sender)
	t.Run("InvalidCustomerReference", func(t *testing.T) {
		var definition string
		if err := pool.QueryRow(ctx, `SELECT pg_get_constraintdef(oid) FROM pg_constraint
			WHERE conrelid='invoices'::regclass AND conname='invoices_customer_id_fkey'`).Scan(&definition); err != nil {
			t.Fatal(err)
		}
		exec("ALTER TABLE invoices DROP CONSTRAINT invoices_customer_id_fkey")
		const update = "UPDATE invoices SET customer_id=$3 WHERE business_id=$1 AND invoice_id=$2"
		defer func() {
			exec(update, owner.id, id, owner.customer)
			exec("ALTER TABLE invoices ADD CONSTRAINT invoices_customer_id_fkey " + definition)
		}()
		var detail string
		for _, customer := range []string{owners[1].customer, "ffffffff-ffff-ffff-ffff-ffffffffffff"} {
			exec(update, owner.id, id, customer)
			w := call(ctx, router, id, session, `{"to_email":"override@example.test"}`)
			expect(w, 500)
			if detail != "" && detail != w.Body.String() {
				t.Fatal("foreign and absent customer references have different responses")
			}
			detail = w.Body.String()
		}
		checkStatus(0, "SENT")
		if len(calls) != 0 {
			t.Fatal("inconsistent customer reference reached the provider")
		}
	})
	for _, race := range []struct {
		update, target, query string
		status                int
	}{
		{"UPDATE users SET role='user' WHERE business_id=$1 AND user_id=$2", owner.user, "%LockSessionUserByID%", 403},
		{"UPDATE invoices SET status='VOID' WHERE business_id=$1 AND invoice_id=$2", id, "%LockInvoice :one%", 400},
	} {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if _, err := tx.Exec(ctx, race.update, owner.id, race.target); err != nil {
			t.Fatal(err)
		}
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() { done <- call(ctx, router, id, session) }()
		wait, cancel := context.WithTimeout(ctx, 3*time.Second)
		for {
			var blocked bool
			err = pool.QueryRow(wait, `SELECT EXISTS (SELECT FROM pg_stat_activity WHERE datname=current_database()
				AND wait_event_type='Lock' AND query LIKE $1)`, race.query).Scan(&blocked)
			if err != nil || blocked {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		cancel()
		if err != nil {
			t.Fatalf("did not see send waiting for %s: %v", race.query, err)
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		select {
		case w := <-done:
			expect(w, race.status)
		case <-ctx.Done():
			t.Fatal("send did not finish after concurrent mutation")
		}
		exec("UPDATE users SET role='admin' WHERE business_id=$1 AND user_id=$2", owner.id, owner.user)
	}
	checkStatus(0, "VOID")
	if len(calls) != 0 {
		t.Fatal("concurrently rejected sends reached the provider")
	}
	exec("UPDATE invoices SET status='OPEN' WHERE business_id=$1 AND invoice_id=$2", owner.id, id)
	exec(`CREATE FUNCTION scoped_invoice_send_bad_date() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN NEW.due_date := 'infinity'; RETURN NEW; END $$;
		CREATE TRIGGER scoped_invoice_send_bad_date BEFORE UPDATE OF status ON invoices
		FOR EACH ROW EXECUTE FUNCTION scoped_invoice_send_bad_date();`)
	expect(call(ctx, router, id, session), 500)
	exec("DROP TRIGGER scoped_invoice_send_bad_date ON invoices; DROP FUNCTION scoped_invoice_send_bad_date()")
	checkStatus(0, "OPEN")
	if len(calls) != 0 {
		t.Fatal("an invalid stored response reached the provider")
	}

	t.Run("SettingsSnapshot", func(t *testing.T) {
		gate := &invoiceLineGate{marker: "-- name: ListBusinessPaymentAccounts", reached: make(chan struct{}), release: make(chan struct{})}
		config := pool.Config()
		config.ConnConfig.Tracer = gate
		sendPool, err := pgxpool.NewWithConfig(ctx, config)
		if err != nil {
			t.Fatal(err)
		}
		defer sendPool.Close()
		defer func() {
			select {
			case <-gate.release:
			default:
				close(gate.release)
			}
		}()
		router := newRouter(sendPool, owner, sender)
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() { done <- call(ctx, router, id, session) }()
		select {
		case <-gate.reached:
		case w := <-done:
			t.Fatalf("send completed before loading accounts: %d %s", w.Code, w.Body)
		case <-ctx.Done():
			t.Fatal("send did not reach the account query")
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if _, err := tx.Exec(ctx, "UPDATE customers SET contact_email='new@example.test' WHERE business_id=$1 AND customer_id=$2", owner.id, owner.customer); err != nil {
			t.Fatal(err)
		}
		for _, sql := range []string{
			"UPDATE business_profile SET name='New Studio' WHERE business_id=$1",
			"UPDATE payment_account SET instructions='New Bank' WHERE business_id=$1 AND currency='USD'",
		} {
			if _, err := tx.Exec(ctx, sql, owner.id); err != nil {
				t.Fatal(err)
			}
		}
		if err := tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		close(gate.release)
		select {
		case w := <-done:
			expect(w, 200)
		case <-ctx.Done():
			t.Fatal("send did not finish after settings update")
		}
		checkMessage := func(wantTo, wantStudio, wantBank string) {
			t.Helper()
			select {
			case msg := <-calls:
				content := scopedPDFText(t, msg.Attachments[0].Content)
				if len(msg.To) != 1 || msg.To[0] != wantTo ||
					!strings.Contains(content, wantStudio) || !strings.Contains(content, wantBank) {
					t.Fatalf("mixed settings: %v / %q", msg.To, content)
				}
			default:
				t.Fatal("send never reached provider")
			}
		}
		checkMessage("alpha@example.test", "Alpha Studio", "Alpha Bank")
		checkStatus(0, "SENT")
		expect(call(ctx, router, id, session), 200)
		checkMessage("new@example.test", "New Studio", "New Bank")
	})
	exec("UPDATE business_profile SET name=$2 WHERE business_id=$1", owner.id, owner.name+" Studio")
	exec("UPDATE customers SET contact_email='alpha@example.test' WHERE business_id=$1 AND customer_id=$2", owner.id, owner.customer)
	exec("UPDATE payment_account SET instructions=$2 WHERE business_id=$1 AND currency='USD'", owner.id, owner.name+" Bank")
}
