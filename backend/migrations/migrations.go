// Package migrations holds the goose migrations, embedded in the binary.
package migrations

import "embed"

// FS is the migration files at their root, for db.Migrate.
//
//go:embed *.sql
var FS embed.FS
