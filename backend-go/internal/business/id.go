// Package business defines the validated identity of a deployed business.
package business

import (
	"errors"
	"github.com/jackc/pgx/v5/pgtype"
)

type ID struct{ value pgtype.UUID }

func Parse(s string) (ID, error) {
	// Require the canonical hyphenated form; compact 32-hex UUIDs are rejected.
	if len(s) != 36 || s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return ID{}, errors.New("business ID must be a canonical UUID")
	}
	var v pgtype.UUID
	if err := v.Scan(s); err != nil {
		return ID{}, err
	}
	id := ID{value: v}
	if err := id.Validate(); err != nil {
		return ID{}, err
	}
	return id, nil
}
func (id ID) Validate() error {
	if !id.value.Valid || id.value.Bytes == [16]byte{} {
		return errors.New("business ID must be a nonzero UUID")
	}
	return nil
}
func (id ID) String() string {
	if id.Validate() != nil {
		return ""
	}
	return id.value.String()
}
func (id ID) UUID() pgtype.UUID { return id.value }
