package mcp

import (
	"encoding/hex"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/wenhaokho/wenhao-billing/backend/internal/bills"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// maxLimit is the most rows a list tool returns (Python: min(limit, 500)).
const maxLimit = 500

// clampLimit is Python's min(limit, 500) with the lower bound Python lacks:
// a negative limit is 0 (an empty list), not a database error (Go change
// 12). It is the page size passed to the list queries.
func clampLimit(n int) *int32 {
	n = max(0, min(n, maxLimit))
	l := int32(n)
	return &l
}

// parseID is Python's UUID(x): it removes every "urn:" and "uuid:", strips
// braces from both ends, removes every hyphen, and then needs exactly 32 hex
// digits, in either case. So the hyphenated form, braces, the urn:uuid:
// prefix and the bare digits all parse, and so does a hyphen anywhere, as in
// Python. Narrower than Python only where int(x, 16) is lenient (surrounding
// whitespace, a sign, a "0x" prefix, underscores): those are refused. name is
// the argument's name, for the sentence of the refusal.
func parseID(name, raw string) (uuid.UUID, error) {
	s := strings.ReplaceAll(strings.ReplaceAll(raw, "urn:", ""), "uuid:", "")
	s = strings.ReplaceAll(strings.Trim(s, "{}"), "-", "")
	var b [16]byte
	// len first: hex.Decode into b would run past it for a longer s.
	if len(s) != 32 {
		return uuid.Nil, errors.New("invalid " + name + ": " + raw + " is not a valid UUID")
	}
	if _, err := hex.Decode(b[:], []byte(s)); err != nil {
		return uuid.Nil, errors.New("invalid " + name + ": " + raw + " is not a valid UUID")
	}
	return uuid.UUID(b), nil
}

// optID is parseID of an argument that filters only when given: nil and ""
// are no filter (Python: `if vendor_id:`).
func optID(name string, raw *string) (*uuid.UUID, error) {
	if raw == nil || *raw == "" {
		return nil, nil
	}
	id, err := parseID(name, *raw)
	if err != nil {
		return nil, err
	}
	return &id, nil
}

// nameLike is a name-substring argument as a query's escaped LIKE operand:
// nil and "" are no filter (Python: `if query:`), and %, _ and \ in the text
// match themselves (Go change 12).
func nameLike(q *string) *string {
	if q == nil || *q == "" {
		return nil
	}
	s := platform.LikeEscape(*q)
	return &s
}

// optText is a text filter argument: nil and "" are no filter.
func optText(s *string) *string {
	if s == nil || *s == "" {
		return nil
	}
	return s
}

// noRows reports whether err is "no such row", which the get tools answer
// as {} (Python: db.get returned None). The service packages report it as
// pgx.ErrNoRows, and bills also wraps it in its own ErrNotFound.
func noRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows) || errors.Is(err, bills.ErrNotFound)
}
