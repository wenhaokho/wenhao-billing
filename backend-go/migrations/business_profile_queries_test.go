package migrations

import (
	"database/sql"
	"os"
	"strconv"
	"strings"
	"testing"
)

// profileQuery executes the staged v8 query text, substituting sqlc's named
// argument notation with PostgreSQL placeholders for this integration test.
func profileQuery(t *testing.T, name string, args ...string) string {
	t.Helper()
	data, err := os.ReadFile("../queries_v8/business_profile.sql")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(data), "-- name: "+name+" ")
	if start < 0 {
		t.Fatalf("missing query %s", name)
	}
	text := string(data)[start:]
	text = text[strings.IndexByte(text, '\n')+1:]
	if end := strings.Index(text, "\n-- name:"); end >= 0 {
		text = text[:end]
	}
	for i, name := range args {
		text = strings.ReplaceAll(text, "sqlc.arg("+name+")", "$"+strconv.Itoa(i+1))
	}
	if strings.Contains(text, "sqlc.arg(") {
		t.Fatalf("unbound argument in %s", name)
	}
	return strings.TrimSpace(text)
}

func testScopedProfileQueries(t *testing.T, db *sql.DB) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var a, b string
	if err = tx.QueryRow(`SELECT business_id::text FROM businesses LIMIT 1`).Scan(&a); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(`INSERT INTO businesses (business_id) VALUES (gen_random_uuid()) RETURNING business_id::text`).Scan(&b); err != nil {
		t.Fatal(err)
	}
	ensure := profileQuery(t, "EnsureBusinessProfile", "business_id")
	if _, err = tx.Exec(ensure, b); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ensure, b); err != nil {
		t.Fatal(err)
	}
	var idA, idB int
	lock := profileQuery(t, "LockBusinessProfile", "business_id")
	if err = tx.QueryRow(lock, a).Scan(&idA); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(lock, b).Scan(&idB); err != nil {
		t.Fatal(err)
	}
	if idA == idB || idB == 1 {
		t.Fatalf("unexpected profile IDs A=%d B=%d", idA, idB)
	}
	insert := profileQuery(t, "InsertBusinessPaymentAccount", "business_id", "currency", "instructions", "is_default")
	if _, err = tx.Exec(insert, b, "IDR", "B bank", true); err != nil {
		t.Fatal(err)
	}
	var owner string
	var profileID int
	if err = tx.QueryRow(`SELECT business_id::text, business_profile_id FROM payment_account WHERE business_id=$1`, b).Scan(&owner, &profileID); err != nil || owner != b || profileID != idB {
		t.Fatalf("B account owner=%q profile=%d, want %q profile=%d: %v", owner, profileID, b, idB, err)
	}
	if _, err = tx.Exec(profileQuery(t, "DeleteBusinessPaymentAccounts", "business_id"), b); err != nil {
		t.Fatal(err)
	}
	var countA, countB int
	if err = tx.QueryRow(`SELECT count(*) FILTER (WHERE business_id=$1), count(*) FILTER (WHERE business_id=$2) FROM payment_account`, a, b).Scan(&countA, &countB); err != nil {
		t.Fatal(err)
	}
	if countA != 1 || countB != 0 {
		t.Fatalf("payment accounts after B delete: A=%d B=%d", countA, countB)
	}
}
