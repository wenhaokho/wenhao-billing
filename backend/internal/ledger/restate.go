package ledger

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/wenhaokho/wenhao-billing/backend/internal/ledger/ledgerdb"
)

// SetEntryMemo replaces entry id's memo: the back-post (PORT-NOTES 166)
// marks a restated payment entry with the original's memo plus a suffix.
// Nothing else about the entry changes.
func SetEntryMemo(ctx context.Context, tx pgx.Tx, id uuid.UUID, memo string) error {
	if err := ledgerdb.New(tx).SetEntryMemo(ctx, ledgerdb.SetEntryMemoParams{Memo: memo, EntryID: id}); err != nil {
		return fmt.Errorf("ledger: set entry memo: %w", err)
	}
	return nil
}
