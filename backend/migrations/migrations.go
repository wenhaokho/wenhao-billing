// Package migrations holds the goose migrations, embedded in the binary.
package migrations

import "embed"

// FS is the migration files at their root, for db.Migrate.
//
//go:embed *.sql
var FS embed.FS

// Testdata is the reference descriptions of the Alembic database at the frozen
// head (written by port/make-baseline.sh) and the queries that produce them.
//
//go:embed testdata/*
var Testdata embed.FS
