package migrations

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func testScopedMilestoneWrites(t *testing.T, pool *pgxpool.Pool, a, b, aUser, bUser, customerA, customerB, projectA, projectB, missing, good string,
	token func(string, string) string, request func(string, string, string, string) *httptest.ResponseRecorder,
) {
	t.Helper()
	const path = "/api/v1/invoices/milestone"
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := func(business string) string {
		t.Helper()
		var state string
		if err := pool.QueryRow(ctx, `SELECT jsonb_build_array(
			(SELECT jsonb_agg(to_jsonb(i) ORDER BY invoice_id) FROM invoices i WHERE business_id=$1),
			(SELECT jsonb_agg(to_jsonb(l) ORDER BY line_item_id) FROM invoice_line_items l WHERE business_id=$1),
			(SELECT jsonb_agg(to_jsonb(j) ORDER BY entry_id) FROM journal_entries j WHERE business_id=$1))::text`, business).Scan(&state); err != nil {
			t.Fatal(err)
		}
		return state
	}
	post := func(cookie, body string) *httptest.ResponseRecorder { return request("POST", path, cookie, body) }
	expect := func(w *httptest.ResponseRecorder, code int) map[string]any {
		t.Helper()
		var obj map[string]any
		if w.Code != code || json.Unmarshal(w.Body.Bytes(), &obj) != nil {
			t.Fatalf("got %d %s, want %d with JSON", w.Code, w.Body, code)
		}
		return obj
	}
	body := func(customer, project, ref string) string {
		return fmt.Sprintf(`{"customer_id":%q,"project_id":%s,"milestone_ref":%q,"currency":"USD","amount":99999999999999.1234,"issue_date":"2026-02-15","due_in_days":-2,"business_id":%q,"invoice_number":"ignored","status":"PAID"}`, customer, project, ref, b)
	}
	owned := body(customerA, fmt.Sprintf("%q", projectA), "scoped milestone")
	originalA, originalB := snapshot(a), snapshot(b)
	expect(post("", owned), 401)
	expect(post(token(bUser, b), owned), 401)
	expect(post(token(bUser, a), owned), 403)
	expect(post(good, `{"customer_id":"invalid","amount":-1,"due_in_days":1.5}`), 422)
	expect(post(good, owned+"{}"), 422)
	for _, pair := range [][2]string{
		{body(customerB, "null", "bad"), body(missing, "null", "bad")},
		{body(customerA, fmt.Sprintf("%q", projectB), "bad"), body(customerA, fmt.Sprintf("%q", missing), "bad")},
	} {
		foreign, missing := post(good, pair[0]), post(good, pair[1])
		expect(foreign, 400)
		expect(missing, 400)
		if foreign.Body.String() != missing.Body.String() {
			t.Fatalf("foreign and missing responses differ: %s / %s", foreign.Body, missing.Body)
		}
	}

	exec(`ALTER TABLE invoices ADD CONSTRAINT test_milestone_insert CHECK (billing_cycle_ref->>'milestone_ref' <> 'reject milestone')`)
	expect(post(good, body(customerA, "null", "reject milestone")), 500)
	exec(`ALTER TABLE invoices DROP CONSTRAINT test_milestone_insert`)
	exec(`CREATE FUNCTION test_corrupt_milestone() RETURNS trigger LANGUAGE plpgsql AS $$
		BEGIN NEW.amount := 'NaN'; RETURN NEW; END $$`)
	exec(`CREATE TRIGGER test_corrupt_milestone BEFORE INSERT ON invoices FOR EACH ROW
		WHEN (NEW.billing_cycle_ref->>'milestone_ref' = 'bad response') EXECUTE FUNCTION test_corrupt_milestone()`)
	expect(post(good, body(customerA, "null", "bad response")), 500)
	exec(`DROP TRIGGER test_corrupt_milestone ON invoices`)
	exec(`DROP FUNCTION test_corrupt_milestone()`)
	if snapshot(a) != originalA || snapshot(b) != originalB {
		t.Fatal("failed milestone requests persisted changes")
	}

	var oldType string
	var oldRate *string
	var oldGross bool
	if err := pool.QueryRow(ctx, `SELECT default_wht_type,default_wht_rate::text,default_wht_gross_up
		FROM customers WHERE business_id=$1 AND customer_id=$2`, a, customerA).Scan(&oldType, &oldRate, &oldGross); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		exec(`UPDATE customers SET default_wht_type=$3,default_wht_rate=$4::text::numeric,default_wht_gross_up=$5
			WHERE business_id=$1 AND customer_id=$2`, a, customerA, oldType, oldRate, oldGross)
	})
	exec(`UPDATE customers SET default_wht_type='NONE',default_wht_rate=NULL,default_wht_gross_up=false WHERE business_id=$1 AND customer_id=$2`, a, customerA)
	first, second := expect(post(good, owned), 201), expect(post(good, owned), 201)
	for _, row := range []map[string]any{first, second} {
		if row["customer_id"] != customerA || row["project_id"] != projectA || row["status"] != "DRAFT" ||
			row["invoice_type"] != "MILESTONE" || row["invoice_number"] != nil || row["business_id"] != nil ||
			row["amount"] != "99999999999999.1234" || row["balance_due"] != row["amount"] ||
			row["issue_date"] != "2026-02-15" || row["due_date"] != "2026-02-13" ||
			len(row["line_items"].([]any)) != 0 || row["billing_cycle_ref"].(map[string]any)["milestone_ref"] != "scoped milestone" {
			t.Fatalf("unexpected milestone: %v", row)
		}
	}
	if first["invoice_id"] == second["invoice_id"] {
		t.Fatal("repeated milestone references should create distinct drafts")
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM invoices WHERE business_id=$1 AND customer_id=$2 AND project_id=$3
		AND invoice_number IS NULL AND invoice_id IN ($4,$5)`, a, customerA, projectA, first["invoice_id"], second["invoice_id"]).Scan(&count); err != nil || count != 2 {
		t.Fatalf("persisted milestones: %d, %v", count, err)
	}

	race := func(query string, args []any, pattern, cookie, payload string) *httptest.ResponseRecorder {
		t.Helper()
		wait, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		tx, err := pool.Begin(wait)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if _, err := tx.Exec(wait, query, args...); err != nil {
			t.Fatal(err)
		}
		result := make(chan *httptest.ResponseRecorder, 1)
		go func() { result <- post(cookie, payload) }()
		for {
			var blocked bool
			if err := pool.QueryRow(wait, `SELECT EXISTS(SELECT FROM pg_stat_activity WHERE datname=current_database()
				AND wait_event_type='Lock' AND query LIKE $1)`, pattern).Scan(&blocked); err != nil {
				t.Fatal(err)
			}
			if blocked {
				break
			}
			select {
			case w := <-result:
				t.Fatalf("request skipped row lock: %d %s", w.Code, w.Body)
			case <-time.After(10 * time.Millisecond):
			}
		}
		if err := tx.Commit(wait); err != nil {
			t.Fatal(err)
		}
		select {
		case w := <-result:
			return w
		case <-wait.Done():
			t.Fatal(wait.Err())
			return nil
		}
	}
	withholding := fmt.Sprintf(`{"customer_id":%q,"milestone_ref":"new defaults","currency":"USD","amount":"10.0001"}`, customerA)
	updated := expect(race(`UPDATE customers SET default_wht_type='PPH23',default_wht_rate=NULL,default_wht_gross_up=true
		WHERE business_id=$1 AND customer_id=$2`, []any{a, customerA}, "%LockMilestoneCustomer%", good, withholding), 201)
	if updated["project_id"] != nil || updated["wht_type"] != "PPH23" || updated["wht_rate"] != "2.0000" ||
		updated["wht_base"] != "10.0001" || updated["wht_amount"] != "0.1999" || updated["amount"] != "10.2000" {
		t.Fatalf("unexpected new withholding defaults: %v", updated)
	}
	var disposableProject string
	if err := pool.QueryRow(ctx, `INSERT INTO projects(project_id,business_id,customer_id,code,name,currency)
		VALUES(gen_random_uuid(),$1,$2,'scoped-milestone-race','scoped milestone race','USD') RETURNING project_id::text`, a, customerA).Scan(&disposableProject); err != nil {
		t.Fatal(err)
	}
	beforeRace := snapshot(a)
	expect(race(`DELETE FROM projects WHERE business_id=$1 AND project_id=$2`, []any{a, disposableProject},
		"%LockMilestoneProject%", good, body(customerA, fmt.Sprintf("%q", disposableProject), "deleted project")), 400)
	expect(race(`UPDATE users SET role='viewer' WHERE business_id=$1 AND user_id=$2`, []any{a, aUser},
		"%LockSessionUserByID%", good, owned), 403)
	exec(`UPDATE users SET role='admin' WHERE business_id=$1 AND user_id=$2`, a, aUser)
	if snapshot(a) != beforeRace || snapshot(b) != originalB {
		t.Fatal("concurrent denial changed owned or foreign invoices")
	}
}
