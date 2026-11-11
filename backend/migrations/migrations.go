// Package migrations holds the goose migrations, embedded in the binary.
package migrations

import "embed"

// FS is the migration files at their root, for db.Migrate.
//
//go:embed *.sql
var FS embed.FS

// Testdata is the reference descriptions of the Alembic database at the frozen
// head and the queries that produce them. They were written by
// port/make-baseline.sh from the Python backend's Alembic migrations; both
// were removed on 2026-11-11 (see git history before that), so these files
// are now fixed: adopt still checks an Alembic-built database against them.
//
//go:embed testdata/*
var Testdata embed.FS
