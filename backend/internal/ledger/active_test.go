package ledger

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func active(t *testing.T, pool *pgxpool.Pool, sourceType string, sourceID uuid.UUID) (uuid.UUID, bool) {
	t.Helper()
	tx := begin(t, pool)
	id, ok, err := ActiveEntryFor(context.Background(), tx, sourceType, sourceID)
	if err != nil {
		t.Fatal(err)
	}
	return id, ok
}

func reverse(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) Entry {
	t.Helper()
	tx := begin(t, pool)
	rev, err := PostReversal(context.Background(), tx, base, id, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	return rev
}

func TestActiveEntryNone(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	if id, ok := active(t, pool, "PAYMENT", uuid.New()); ok {
		t.Errorf("found %s for an unposted source", id)
	}
}

func TestActiveEntryAfterPost(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	orig := post(t, pool, cleared("100", "IDR"), invoice("IDR", "100"), "0")
	id, ok := active(t, pool, orig.SourceType, orig.SourceID)
	if !ok || id != orig.ID {
		t.Errorf("active = %s %v, want %s", id, ok, orig.ID)
	}
	if _, ok := active(t, pool, "INVOICE", orig.SourceID); ok {
		t.Error("a different source type matched")
	}
}

func TestActiveEntryAfterReversalIsNone(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	orig := post(t, pool, cleared("100", "IDR"), invoice("IDR", "100"), "0")
	reverse(t, pool, orig.ID)
	if id, ok := active(t, pool, orig.SourceType, orig.SourceID); ok {
		t.Errorf("found %s after the only entry was reversed", id)
	}
}

func TestActiveEntryAfterRepost(t *testing.T) {
	t.Parallel()
	pool := newPool(t)
	p := cleared("100", "IDR")
	first := post(t, pool, p, invoice("IDR", "100"), "0")
	reverse(t, pool, first.ID)
	second := post(t, pool, p, invoice("IDR", "100"), "0")
	id, ok := active(t, pool, second.SourceType, second.SourceID)
	if !ok || id != second.ID {
		t.Errorf("active = %s %v, want the second post %s", id, ok, second.ID)
	}
	reverse(t, pool, second.ID)
	if id, ok := active(t, pool, second.SourceType, second.SourceID); ok {
		t.Errorf("found %s after both posts were reversed", id)
	}
}
