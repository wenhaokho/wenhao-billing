package platform

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wenhaokho/wenhao-billing/backend/internal/business"
)

func TestCandidateTransactionsRejectInvalidScopeBeforeConnecting(t *testing.T) {
	tx, err := (candidateTransactions{}).Begin(context.Background())
	if tx != nil || err == nil {
		t.Fatalf("invalid scope: %v %v", tx, err)
	}
}

func testCandidateTransactionsRejectAbsentBusiness(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	scope, err := business.Parse("99999999-1111-4444-8888-999999999999")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := (candidateTransactions{db: pool, scope: scope}).Begin(ctx)
	if tx != nil || !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("absent business: %v %v", tx, err)
	}
}
