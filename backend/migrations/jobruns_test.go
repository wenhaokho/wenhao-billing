package migrations_test

import (
	"strings"
	"testing"

	"github.com/LOQATO/gokit/testkit"

	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

// TestTheJobRunsMigrationIsAdditive: the Go-only migrations may only add
// (the Python backend runs beside them until the switch-over), so their Up
// sections create tables and add columns, never drop or alter what is
// there.
func TestTheJobRunsMigrationIsAdditive(t *testing.T) {
	t.Parallel()
	body, err := migrations.FS.ReadFile("00004_scheduled_job_runs.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, _, _ := strings.Cut(string(body), "-- +goose Down")
	for _, line := range strings.Split(up, "\n") {
		line = strings.TrimSpace(strings.ToUpper(line))
		if strings.HasPrefix(line, "--") || line == "" {
			continue
		}
		for _, forbidden := range []string{"DROP ", "ALTER ", "TRUNCATE ", "DELETE "} {
			if strings.HasPrefix(line, forbidden) {
				t.Errorf("the Up section is not additive: %s", line)
			}
		}
	}
}

// TestTheJobRunsTableShape: the table is the one the brief names: the slot
// a job ran in, the primary key (job_id, slot).
func TestTheJobRunsTableShape(t *testing.T) {
	t.Parallel()
	url := testkit.PostgresURL(t, migrations.FS)
	pool := openPool(t, url)
	columns := map[string]string{}
	rows, err := pool.Query(t.Context(), `SELECT column_name, data_type FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'scheduled_job_runs'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var name, typ string
		if err := rows.Scan(&name, &typ); err != nil {
			t.Fatal(err)
		}
		columns[name] = typ
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"job_id": "text", "slot": "timestamp with time zone",
		"started_at": "timestamp with time zone", "finished_at": "timestamp with time zone",
		"error": "text",
	}
	for name, typ := range want {
		got, ok := columns[name]
		if !ok {
			t.Errorf("scheduled_job_runs has no column %q", name)
			continue
		}
		if got != typ {
			t.Errorf("scheduled_job_runs.%s is %q, want %q", name, got, typ)
		}
	}
	// The primary key is (job_id, slot): that is what makes a slot run once.
	keys := scalar[string](t, pool, `SELECT string_agg(a.attname, ',' ORDER BY k.ord)
		FROM pg_constraint c
		JOIN pg_class r ON r.oid = c.conrelid
		JOIN unnest(c.conkey) WITH ORDINALITY AS k(attnum, ord) ON true
		JOIN pg_attribute a ON a.attrelid = r.oid AND a.attnum = k.attnum
		WHERE r.relname = 'scheduled_job_runs' AND c.contype = 'p'`)
	if keys != "job_id,slot" {
		t.Errorf("the primary key is %q, want %q", keys, "job_id,slot")
	}
}
