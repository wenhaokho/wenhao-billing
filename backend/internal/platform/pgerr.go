package platform

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// Constraint is the client error a database constraint's violation becomes.
type Constraint struct {
	Status       int
	Code, Detail string
}

// MapConstraint turns the violation of a constraint known names (a unique
// key, say) into that entry's Err; any other error, nil included, comes back
// unchanged.
func MapConstraint(err error, known map[string]Constraint) error {
	var pg *pgconn.PgError
	if !errors.As(err, &pg) {
		return err
	}
	c, ok := known[pg.ConstraintName]
	if !ok {
		return err
	}
	return Err(c.Status, c.Code, c.Detail)
}
