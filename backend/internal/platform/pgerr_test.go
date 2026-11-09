package platform

import (
	"context"
	"errors"
	"testing"

	"github.com/LOQATO/gokit/apierr"
	"github.com/LOQATO/gokit/db"
	"github.com/LOQATO/gokit/testkit"

	"github.com/wenhaokho/wenhao-billing/backend/migrations"
)

func TestMapConstraint(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool, err := db.Open(ctx, db.PoolConfig{URL: testkit.PostgresURL(t, migrations.FS)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	insert := `INSERT INTO chart_of_accounts (code, name, type) VALUES ('T-1', 'Test', 'ASSET')`
	if _, err := pool.Exec(ctx, insert); err != nil {
		t.Fatal(err)
	}
	_, dup := pool.Exec(ctx, insert)
	if dup == nil {
		t.Fatal("duplicate code accepted")
	}

	known := map[string]Constraint{"chart_of_accounts_code_key": {409, "account_code_exists", "account code already exists"}}
	var e *apierr.Error
	if !errors.As(MapConstraint(dup, known), &e) || e.Status != 409 || e.Code != "account_code_exists" || e.Message.String() != "account code already exists" {
		t.Errorf("mapped: %#v", MapConstraint(dup, known))
	}

	other := map[string]Constraint{"vendors_email_key": {409, "x", "y"}}
	if got := MapConstraint(dup, other); got != dup { //nolint:errorlint // must be the very same error
		t.Errorf("unlisted constraint: %v", got)
	}
	plain := errors.New("boom")
	if got := MapConstraint(plain, known); got != plain { //nolint:errorlint // must be the very same error
		t.Errorf("non-pg error: %v", got)
	}
	if MapConstraint(nil, known) != nil {
		t.Error("nil mapped to an error")
	}
}
