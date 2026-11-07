package migrations

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wenhaokho/wenhao-billing/backend/internal/business"
	"github.com/wenhaokho/wenhao-billing/backend/internal/email"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

type scopedQuotationSender func(context.Context, email.Message) error

func (send scopedQuotationSender) Send(ctx context.Context, message email.Message) error {
	return send(ctx, message)
}

func testScopedQuotationSend(t *testing.T, pool *pgxpool.Pool, owners []scopedDocumentOwner, ids []string, secret []byte, token func(string, string) string) {
	t.Helper()
	ctx, stop := context.WithTimeout(context.Background(), 10*time.Second)
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
	call := func(ctx context.Context, router http.Handler, id, session string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/quotations/"+id+"/send", strings.NewReader("{}")).WithContext(ctx)
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
	checkState := func(o scopedDocumentOwner, id, wantStatus string, wantSent bool) {
		t.Helper()
		var status string
		var sent bool
		err := pool.QueryRow(ctx, "SELECT status,last_sent_at IS NOT NULL FROM quotations WHERE business_id=$1 AND quotation_id=$2", o.id, id).Scan(&status, &sent)
		if err != nil || status != wantStatus || sent != wantSent {
			t.Fatalf("quotation state %s/%v, want %s/%v: %v", status, sent, wantStatus, wantSent, err)
		}
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}

	for _, o := range owners {
		var previous *string
		if err := pool.QueryRow(ctx, "SELECT default_notes FROM business_profile WHERE business_id=$1", o.id).Scan(&previous); err != nil {
			t.Fatal(err)
		}
		exec("UPDATE business_profile SET default_notes=$2 WHERE business_id=$1", o.id, o.name+" Default Notes")
		defer exec("UPDATE business_profile SET default_notes=$2 WHERE business_id=$1", o.id, previous)
	}
	for i, o := range owners {
		other := owners[1-i]
		var messages []email.Message
		var providerErr error
		cancelRequest := func() {}
		router := newRouter(pool, o, func(_ context.Context, message email.Message) error {
			messages = append(messages, message)
			cancelRequest()
			return providerErr
		})
		session := token(o.user, o.id)
		foreign := call(ctx, router, ids[1-i], session)
		missing := call(ctx, router, "ffffffff-ffff-ffff-ffff-ffffffffffff", session)
		expect(foreign, 404)
		expect(missing, 404)
		if foreign.Body.String() != missing.Body.String() {
			t.Fatalf("foreign/missing responses differ: %s / %s", foreign.Body, missing.Body)
		}
		expect(call(ctx, router, ids[i], ""), 401)
		expect(call(ctx, router, ids[i], token(other.user, other.id)), 401)
		expect(call(ctx, router, ids[i], token(other.user, o.id)), 403)
		expect(call(ctx, router, "malformed", session), 422)
		exec("UPDATE quotations SET valid_until='infinity' WHERE business_id=$1 AND quotation_id=$2", o.id, ids[i])
		expect(call(ctx, router, ids[i], session), 500)
		exec("UPDATE quotations SET valid_until=NULL WHERE business_id=$1 AND quotation_id=$2", o.id, ids[i])
		exec("UPDATE quotation_line_items SET amount='NaN' WHERE business_id=$1 AND quotation_id=$2", o.id, ids[i])
		expect(call(ctx, router, ids[i], session), 500)
		exec("UPDATE quotation_line_items SET amount=unit_price WHERE business_id=$1 AND quotation_id=$2", o.id, ids[i])
		if len(messages) != 0 {
			t.Fatal("rejected sends reached the provider")
		}
		checkState(o, ids[i], "DRAFT", false)

		providerErr = errors.New("fake ambiguous delivery")
		expect(call(ctx, router, ids[i], session), 500)
		checkState(o, ids[i], "DRAFT", false)
		if len(messages) != 1 {
			t.Fatalf("provider attempts %d, want 1", len(messages))
		}

		providerErr = nil
		reqCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		cancelRequest = cancel
		w := call(reqCtx, router, ids[i], session)
		expect(w, 200)
		if len(messages) != 2 || !strings.Contains(w.Body.String(), `"status":"SENT"`) || strings.Contains(w.Body.String(), `"last_sent_at":null`) {
			t.Fatalf("delivery response %s with %d provider attempts", w.Body, len(messages))
		}
		checkState(o, ids[i], "SENT", true)
		message := messages[1]
		if len(message.To) != 1 || message.To[0] != strings.ToLower(o.name)+"@example.test" || !strings.Contains(message.Text, o.name+" Customer") || strings.Contains(message.Text, other.name) {
			t.Fatalf("wrong recipient or message text: %+v / %q", message.To, message.Text)
		}
		if len(message.Attachments) != 1 || message.Attachments[0].Filename != "quotation-"+o.name+"-PDF.pdf" || message.Attachments[0].ContentType != "application/pdf" {
			t.Fatal("missing quotation PDF attachment")
		}
		content := scopedPDFText(t, message.Attachments[0].Content)
		for _, suffix := range []string{" Studio", " Customer", " Bank", " Service", " Default Notes"} {
			if !strings.Contains(content, o.name+suffix) || strings.Contains(content, other.name+suffix) {
				t.Fatalf("wrong business content in attachment: %q", content)
			}
		}
	}

	o := owners[0]
	calls := make(chan email.Message, 1)
	router := newRouter(pool, o, func(_ context.Context, message email.Message) error {
		calls <- message
		return nil
	})
	session := token(o.user, o.id)
	waitFor := func(query string) {
		t.Helper()
		wait, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		for {
			var blocked bool
			err := pool.QueryRow(wait, `SELECT EXISTS (SELECT FROM pg_stat_activity WHERE datname=current_database()
				AND wait_event_type='Lock' AND query LIKE $1)`, query).Scan(&blocked)
			if err != nil {
				t.Fatalf("did not observe send waiting for %s: %v", query, err)
			}
			if blocked {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	race := func(update, blockedQuery string, want int, targetID string) {
		t.Helper()
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if _, err = tx.Exec(ctx, update, o.id, targetID); err != nil {
			t.Fatal(err)
		}
		response := make(chan *httptest.ResponseRecorder, 1)
		go func() { response <- call(ctx, router, ids[0], session) }()
		waitFor(blockedQuery)
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		select {
		case w := <-response:
			expect(w, want)
		case <-ctx.Done():
			t.Fatal("send did not finish after concurrent mutation")
		}
		if len(calls) != 0 {
			t.Fatal("send reached provider after concurrent mutation")
		}
	}
	race("UPDATE users SET role='user' WHERE business_id=$1 AND user_id=$2",
		"%FROM users WHERE business_id=%FOR UPDATE%", 403, o.user)
	exec("UPDATE users SET role='admin' WHERE business_id=$1 AND user_id=$2", o.id, o.user)
	race("UPDATE quotations SET status='VOID' WHERE business_id=$1 AND quotation_id=$2",
		"%FROM quotations WHERE business_id=%FOR UPDATE%", 400, ids[0])
	checkState(o, ids[0], "VOID", true)

	exec("UPDATE quotations SET status='DRAFT', last_sent_at=NULL WHERE business_id=$1 AND quotation_id=$2", o.id, ids[0])
	t.Run("SupportingSnapshot", func(t *testing.T) {
		gate := &quotationSendDetailsGate{invoiceLineGate{
			marker: "-- name: QuotationSendDetails", reached: make(chan struct{}), release: make(chan struct{}),
		}}
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
		router := newRouter(readPool, o, func(_ context.Context, message email.Message) error {
			calls <- message
			return nil
		})
		response := make(chan *httptest.ResponseRecorder, 1)
		go func() { response <- call(ctx, router, ids[0], session) }()
		select {
		case <-gate.reached:
		case w := <-response:
			t.Fatalf("send completed before the settings query: %d %s", w.Code, w.Body)
		case <-ctx.Done():
			t.Fatal("send did not reach the settings query")
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		for _, update := range []struct {
			sql  string
			args []any
		}{
			{"UPDATE business_profile SET name='New Studio' WHERE business_id=$1", []any{o.id}},
			{"UPDATE payment_account SET instructions='New Bank' WHERE business_id=$1 AND currency='USD'", []any{o.id}},
			{"UPDATE customers SET contact_email='new@example.test' WHERE business_id=$1 AND customer_id=$2", []any{o.id, o.customer}},
		} {
			if _, err = tx.Exec(ctx, update.sql, update.args...); err != nil {
				t.Fatal(err)
			}
		}
		if err = tx.Commit(ctx); err != nil {
			t.Fatal(err)
		}
		close(gate.release)
		select {
		case w := <-response:
			expect(w, 200)
		case <-ctx.Done():
			t.Fatal("send did not finish after the settings commit")
		}
		select {
		case message := <-calls:
			content := scopedPDFText(t, message.Attachments[0].Content)
			if !strings.Contains(content, "New Studio") || !strings.Contains(content, "New Bank") ||
				strings.Contains(content, o.name+" Studio") || strings.Contains(content, o.name+" Bank") ||
				len(message.To) != 1 || message.To[0] != "new@example.test" {
				t.Fatalf("send mixed settings snapshots: %v / %q", message.To, content)
			}
		default:
			t.Fatal("send never reached provider")
		}
	})

	exec("UPDATE business_profile SET name=$2 WHERE business_id=$1", o.id, o.name+" Studio")
	exec("UPDATE payment_account SET instructions=$2 WHERE business_id=$1 AND currency='USD'", o.id, o.name+" Bank")
	exec("UPDATE customers SET contact_email=$3 WHERE business_id=$1 AND customer_id=$2", o.id, o.customer, strings.ToLower(o.name)+"@example.test")
	exec("UPDATE quotations SET status='DRAFT', last_sent_at=NULL WHERE business_id=$1 AND quotation_id=$2", o.id, ids[0])
	exec(`CREATE FUNCTION scoped_send_bad_timestamp() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN NEW.accepted_at := 'infinity'; RETURN NEW; END $$;
		CREATE TRIGGER scoped_send_bad_timestamp BEFORE UPDATE OF last_sent_at ON quotations
		FOR EACH ROW EXECUTE FUNCTION scoped_send_bad_timestamp();`)
	defer exec("DROP TRIGGER scoped_send_bad_timestamp ON quotations; DROP FUNCTION scoped_send_bad_timestamp();")
	expect(call(ctx, router, ids[0], session), 500)
	if len(calls) != 1 {
		t.Fatal("expected provider acceptance before response serialization failed")
	}
	checkState(o, ids[0], "DRAFT", false)
}

// Pause before the combined snapshot, or before payment accounts when running
// the prior split-query implementation as a negative control.
type quotationSendDetailsGate struct {
	invoiceLineGate
}

func (g *quotationSendDetailsGate) TraceQueryStart(ctx context.Context, conn *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if strings.Contains(data.SQL, "-- name: ListBusinessPaymentAccounts") {
		data.SQL = g.marker
	}
	return g.invoiceLineGate.TraceQueryStart(ctx, conn, data)
}
