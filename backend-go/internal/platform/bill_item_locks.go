package platform

import (
	"bytes"
	"sort"

	"github.com/jackc/pgx/v5/pgtype"
)

// Lock each referenced item once, in the same order for every UUID spelling.
func orderedBillItemIDs(ids map[[16]byte]pgtype.UUID) []pgtype.UUID {
	ordered := make([]pgtype.UUID, 0, len(ids))
	for _, id := range ids {
		ordered = append(ordered, id)
	}
	sort.Slice(ordered, func(i, j int) bool { return bytes.Compare(ordered[i].Bytes[:], ordered[j].Bytes[:]) < 0 })
	return ordered
}
