package platform

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wenhaokho/wenhao-billing/backend/internal/business"
)

type transactionBeginner interface {
	Begin(context.Context) (pgx.Tx, error)
}

type transactionOptionsBeginner interface {
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}

// candidateTransactions starts a staged candidate-affecting write. Acquire the
// business fence before actor, invoice, customer, quote, or numbering locks.
// NO KEY UPDATE lets unrelated inserts take their business FK's KEY SHARE.
type candidateTransactions struct {
	db    transactionOptionsBeginner
	scope business.ID
}

func (b candidateTransactions) Begin(ctx context.Context) (pgx.Tx, error) {
	return b.BeginTx(ctx, pgx.TxOptions{})
}

func (b candidateTransactions) BeginTx(ctx context.Context, options pgx.TxOptions) (pgx.Tx, error) {
	if err := b.scope.Validate(); err != nil {
		return nil, err
	}
	wait, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := b.db.BeginTx(wait, options)
	if err != nil {
		return nil, err
	}
	var locked pgtype.UUID
	err = tx.QueryRow(wait, `SELECT business_id FROM businesses WHERE business_id=$1 FOR NO KEY UPDATE`, b.scope.UUID()).Scan(&locked)
	if err != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
		return nil, fmt.Errorf("acquire reconciliation candidate fence: %w", err)
	}
	return tx, nil
}
