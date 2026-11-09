// Package db opens Postgres pools, runs transactions and migrations.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"time"

	pgxdecimal "github.com/jackc/pgx-shopspring-decimal"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// PoolConfig is what Open needs. A zero StatementTimeout leaves the server's
// default; a zero MaxConns leaves pgxpool's.
type PoolConfig struct {
	URL              string
	StatementTimeout time.Duration
	MaxConns         int32
}

// Open connects a pool. Every connection reads and writes numeric as
// decimal.Decimal, runs with jit=off (JIT compilation made TAGARA's
// correlated subqueries ten times slower than the queries themselves) and
// with the statement timeout set.
func Open(ctx context.Context, c PoolConfig) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(c.URL)
	if err != nil {
		return nil, fmt.Errorf("db: %w", err)
	}
	cfg.ConnConfig.RuntimeParams["jit"] = "off"
	if c.StatementTimeout > 0 {
		cfg.ConnConfig.RuntimeParams["statement_timeout"] = strconv.FormatInt(c.StatementTimeout.Milliseconds(), 10)
	}
	if c.MaxConns > 0 {
		cfg.MaxConns = c.MaxConns
	}
	cfg.AfterConnect = func(_ context.Context, conn *pgx.Conn) error {
		pgxdecimal.Register(conn.TypeMap())
		return nil
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("db: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("db: %w", err)
	}
	return pool, nil
}

// InTx runs fn in a transaction. It commits when fn returns nil and rolls
// back when fn returns an error or panics; a panic continues after the
// rollback. Code inside fn never commits or rolls back itself.
func InTx(ctx context.Context, pool *pgxpool.Pool, fn func(pgx.Tx) error) (err error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("db: begin: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
			panic(p)
		}
	}()
	if err := fn(tx); err != nil {
		if rbErr := tx.Rollback(context.WithoutCancel(ctx)); rbErr != nil && !errors.Is(rbErr, pgx.ErrTxClosed) {
			return errors.Join(err, fmt.Errorf("db: rollback: %w", rbErr))
		}
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("db: commit: %w", err)
	}
	return nil
}

// Migrate applies every pending goose migration in migrations (SQL files at
// its root) under a Postgres session lock, so several processes starting at
// once run them one at a time and the later ones find nothing to do.
func Migrate(ctx context.Context, url string, migrations fs.FS) error {
	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		return fmt.Errorf("db: migrate: %w", err)
	}
	conn := stdlib.OpenDB(*cfg)
	defer func(c *sql.DB) { _ = c.Close() }(conn)
	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return fmt.Errorf("db: migrate: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, conn, migrations, goose.WithSessionLocker(locker))
	if err != nil {
		return fmt.Errorf("db: migrate: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("db: migrate: %w", err)
	}
	return nil
}
