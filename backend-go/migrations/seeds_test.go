package migrations

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/peterldowns/pgtestdb"
	"github.com/peterldowns/pgtestdb/migrators/goosemigrator"
)

func TestFreshBaselineSeedsMatchAlembic(t *testing.T) {
	raw := os.Getenv("PGTESTDB_URL")
	u, err := url.Parse(raw)
	if err != nil || u.User == nil || u.Hostname() == "" || u.Path == "" {
		t.Fatal("PGTESTDB_URL must identify the isolated Alembic test database")
	}
	source, err := sql.Open("pgx", raw)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	ctx := context.Background()
	var rev string
	if err := source.QueryRowContext(ctx, "SELECT version_num FROM alembic_version").Scan(&rev); err != nil {
		t.Fatal(err)
	}
	if rev != "0028_business_npwp" {
		t.Fatalf("reference database revision = %q", rev)
	}
	password, _ := u.User.Password()
	config := pgtestdb.Config{DriverName: "pgx", Host: u.Hostname(), Port: u.Port(), User: u.User.Username(), Password: password, Database: strings.TrimPrefix(u.Path, "/"), Options: u.RawQuery}
	target := pgtestdb.Custom(t, config, goosemigrator.New("."))
	db, err := sql.Open("pgx", target.URL())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	read := func(db *sql.DB, query string) []string {
		t.Helper()
		rows, err := db.QueryContext(ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var row string
			if err := rows.Scan(&row); err != nil {
				t.Fatal(err)
			}
			out = append(out, row)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return out
	}
	for _, tc := range []struct {
		name, sourceQuery, targetQuery string
		count                          int
	}{
		{"chart_of_accounts", `SELECT row_to_json(c)::text FROM (SELECT * FROM chart_of_accounts ORDER BY account_id) c`, "", 14},
		{"identity_rate", `SELECT row_to_json(c)::text FROM (SELECT rate_id, from_currency, to_currency, rate, source FROM fx_rates WHERE from_currency='IDR' AND to_currency='IDR' AND source='SEED' ORDER BY rate_id) c`, `SELECT row_to_json(c)::text FROM (SELECT rate_id, from_currency, to_currency, rate, source FROM fx_rates ORDER BY rate_id) c`, 1},
		{"business_profile", `SELECT (to_jsonb(b) - 'updated_at')::text FROM business_profile b ORDER BY id`, "", 1},
		{"payment_account", `SELECT (to_jsonb(p) - 'updated_at')::text FROM payment_account p ORDER BY id`, "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			targetQuery := tc.targetQuery
			if targetQuery == "" {
				targetQuery = tc.sourceQuery
			}
			want, got := read(source, tc.sourceQuery), read(db, targetQuery)
			if len(want) != tc.count {
				t.Fatalf("Alembic reference %s: expected %d rows, got %d: %v", tc.name, tc.count, len(want), want)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s seed mismatch: Alembic %v; goose %v", tc.name, want, got)
			}
		})
	}
	t.Run("identity_rate_date", func(t *testing.T) {
		// Both defaults use the migration transaction's timestamp. This remains
		// valid if the template was created yesterday or a test spans midnight.
		for name, database := range map[string]*sql.DB{"Alembic": source, "goose": db} {
			var valid bool
			err := database.QueryRowContext(ctx, `SELECT as_of_date = created_at::date AND created_at <= LOCALTIMESTAMP FROM fx_rates WHERE from_currency='IDR' AND to_currency='IDR' AND source='SEED'`).Scan(&valid)
			if err != nil {
				t.Fatal(err)
			}
			if !valid {
				t.Errorf("%s identity rate date must equal its creation date", name)
			}
		}
	})

	for _, tc := range []struct{ table, column string }{{"chart_of_accounts", "account_id"}, {"fx_rates", "rate_id"}, {"business_profile", "id"}} {
		t.Run("sequence_"+tc.table, func(t *testing.T) {
			var next, max int64
			query := fmt.Sprintf(`SELECT nextval(pg_get_serial_sequence('%s','%s')), (SELECT max(%s) FROM %s)`, tc.table, tc.column, tc.column, tc.table)
			if err := db.QueryRowContext(ctx, query).Scan(&next, &max); err != nil {
				t.Fatal(err)
			}
			if next <= max {
				t.Fatalf("%s sequence next value %d collides with seeded IDs through %d", tc.table, next, max)
			}
		})
	}
}
