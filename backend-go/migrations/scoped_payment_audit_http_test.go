package migrations

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func testScopedPaymentAudit(t *testing.T, pool *pgxpool.Pool, a, b, actorA, actorB, paymentA, paymentB, invoiceB, customerB, missing, admin, foreign string, request func(string, string, string, string) *httptest.ResponseRecorder) {
	t.Helper()
	ctx := context.Background()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	const base = "/api/v1/payments/"
	const audit = base + "audit-log"
	const early = "2026-04-02T00:00:00"
	const late = "2026-04-02T23:59:59.999999"
	logs := func(id string) string { return base + id + "/log" }
	read := func(path, token string, status int) string {
		t.Helper()
		w := request("GET", path, token, "")
		if w.Code != status {
			t.Fatalf("%s: got %d %s, want %d", path, w.Code, w.Body, status)
		}
		return w.Body.String()
	}
	for _, path := range []string{audit, logs(paymentA)} {
		read(path, "", 401)
		read(path, foreign, 401)
	}
	read(logs("bad"), admin, 422)
	read(audit+"?limit=501", admin, 422)
	exec(`UPDATE payments SET payer_name='Scoped Audit' WHERE business_id=$1 AND payment_id=$2`, a, paymentA)
	exec(`INSERT INTO reconciliation_log(business_id,log_id,payment_id,actor_user_id,action,reasons,created_at) VALUES
		($1,gen_random_uuid(),$2,$3,'SCOPE_AUDIT',ARRAY['early'],$4),
		($1,gen_random_uuid(),$2,NULL,'SCOPE_AUDIT',ARRAY['late'],$5),
		($1,gen_random_uuid(),$2,NULL,'SCOPE_AUDIT',ARRAY['outside'],'2026-04-03'),
		($6,gen_random_uuid(),$7,$8,'SCOPE_AUDIT',ARRAY['foreign'],$5)`, a, paymentA, actorA, early, late, b, paymentB, actorB)
	filter := audit + "?action=SCOPE_AUDIT&payer=scoped%20audit&from_date=2026-04-02&to_date=2026-04-02"
	rows := func(path string) []map[string]any {
		t.Helper()
		var out []map[string]any
		if err := json.Unmarshal([]byte(read(path, admin, 200)), &out); err != nil || out == nil {
			t.Fatalf("%s: invalid response %v / %v", path, out, err)
		}
		return out
	}
	clean := rows(filter)
	if len(clean) != 2 || clean[0]["created_at"] != late || clean[0]["actor_user_id"] != nil || clean[0]["actor_email"] != nil ||
		clean[1]["created_at"] != early || clean[1]["actor_user_id"] != actorA || clean[1]["payment_id"] != paymentA ||
		clean[1]["payer_name"] != "Scoped Audit" || clean[1]["business_id"] != nil {
		t.Fatalf("audit rows: %v", clean)
	}
	var amount string
	if err := pool.QueryRow(ctx, `SELECT amount::text FROM payments WHERE business_id=$1 AND payment_id=$2`, a, paymentA).Scan(&amount); err != nil || clean[1]["amount"] != amount {
		t.Fatalf("amount: %v, want %s / %v", clean[1]["amount"], amount, err)
	}
	if got := rows(filter + "&limit=1"); !reflect.DeepEqual(got, clean[:1]) {
		t.Fatalf("limited audit: %v", got)
	}
	if got := rows(filter + "&limit=0"); len(got) != 0 {
		t.Fatalf("zero limit: %v", got)
	}
	var ownTimes []any
	for _, row := range rows(logs(paymentA)) {
		if row["action"] == "SCOPE_AUDIT" {
			ownTimes = append(ownTimes, row["created_at"])
		}
	}
	if !reflect.DeepEqual(ownTimes, []any{early, late, "2026-04-03T00:00:00"}) {
		t.Fatalf("ascending payment log: %v", ownTimes)
	}
	ownLogs := rows(logs(paymentA))
	for _, id := range []string{paymentB, missing} {
		if got := rows(logs(id)); len(got) != 0 {
			t.Fatalf("unavailable payment log: %v", got)
		}
	}
	const generic = "{\"detail\":\"internal server error\"}\n"
	snapshot := func() string {
		t.Helper()
		var state string
		if err := pool.QueryRow(ctx, `SELECT
			(SELECT jsonb_agg(to_jsonb(l) ORDER BY log_id)::text FROM reconciliation_log l WHERE business_id IN ($1,$2)) ||
			(SELECT jsonb_agg(to_jsonb(p) ORDER BY payment_id)::text FROM payments p WHERE business_id IN ($1,$2))`, a, b).Scan(&state); err != nil {
			t.Fatal(err)
		}
		return state
	}
	exec(`UPDATE users SET role='user' WHERE business_id=$1 AND user_id=$2`, a, actorA)
	read(audit, admin, 403)
	read(logs(paymentA), admin, 403)
	exec(`UPDATE users SET role='admin' WHERE business_id=$1 AND user_id=$2`, a, actorA)
	for _, tc := range []struct {
		table, column, key, row, foreign string
		logStatus                        int
	}{
		{"reconciliation_log", "actor_user_id", "log_id", clean[1]["log_id"].(string), actorB, 500},
		{"reconciliation_log", "payment_id", "log_id", clean[1]["log_id"].(string), paymentB, 200},
		{"payments", "invoice_id", "payment_id", paymentA, invoiceB, 500},
		{"payments", "customer_id", "payment_id", paymentA, customerB, 500},
	} {
		t.Run(tc.column, func(t *testing.T) {
			constraint := tc.table + "_" + tc.column + "_fkey"
			var definition string
			if err := pool.QueryRow(ctx, `SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid=$1::regclass AND conname=$2`, tc.table, constraint).Scan(&definition); err != nil {
				t.Fatal(err)
			}
			where := " WHERE business_id=$1 AND " + tc.key + "=$2"
			var original *string
			if err := pool.QueryRow(ctx, "SELECT "+tc.column+"::text FROM "+tc.table+where, a, tc.row).Scan(&original); err != nil {
				t.Fatal(err)
			}
			set := "UPDATE " + tc.table + " SET " + tc.column + "=$3" + where
			exec("ALTER TABLE " + tc.table + " DROP CONSTRAINT " + constraint)
			t.Cleanup(func() {
				exec(set, a, tc.row, original)
				exec("ALTER TABLE " + tc.table + " ADD CONSTRAINT " + constraint + " " + definition)
			})
			for _, id := range []string{tc.foreign, missing} {
				exec(set, a, tc.row, id)
				before := snapshot()
				if body := read(filter, admin, 500); body != generic {
					t.Fatalf("damaged audit response: %q", body)
				}
				if body := read(logs(paymentA), admin, tc.logStatus); tc.logStatus == 500 && body != generic {
					t.Fatalf("damaged log response: %q", body)
				}
				if got := rows(logs(id)); len(got) != 0 {
					t.Fatalf("foreign or missing payment disclosed logs: %v", got)
				}
				if after := snapshot(); before != after {
					t.Fatal("audit reads changed payments or reconciliation logs")
				}
			}
			if tc.column == "payment_id" {
				exec(set, a, tc.row, original)
				var foreignLog string
				if err := pool.QueryRow(ctx, `SELECT log_id::text FROM reconciliation_log
					WHERE business_id=$1 AND payment_id=$2 AND action='SCOPE_AUDIT'`, b, paymentB).Scan(&foreignLog); err != nil {
					t.Fatal(err)
				}
				exec(set, b, foreignLog, paymentA)
				defer exec(set, b, foreignLog, paymentB)
				if got := rows(logs(paymentA)); !reflect.DeepEqual(got, ownLogs) {
					t.Fatalf("foreign log attached to owned payment: %v", got)
				}
				if got := rows(filter); !reflect.DeepEqual(got, clean) {
					t.Fatalf("foreign log entered owned audit: %v", got)
				}
			}
		})
	}
	if got := rows(filter); !reflect.DeepEqual(got, clean) {
		t.Fatalf("audit after restoration: %v", got)
	}

}
