package platform

import (
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// UUIDPtr is u as a pointer, nil for NULL.
func UUIDPtr(u pgtype.UUID) *uuid.UUID {
	if !u.Valid {
		return nil
	}
	id := uuid.UUID(u.Bytes)
	return &id
}

// MergeUUID is the column value of a nullable uuid PATCH field: cur when the
// field was left out, NULL when it was sent as null, otherwise the value.
func MergeUUID(f Optional[uuid.UUID], cur pgtype.UUID) pgtype.UUID {
	switch {
	case !f.Set:
		return cur
	case f.Null:
		return pgtype.UUID{}
	}
	return pgtype.UUID{Bytes: f.Value, Valid: true}
}

// MergeDate is MergeUUID for a nullable date field.
func MergeDate(f Optional[Date], cur pgtype.Date) pgtype.Date {
	switch {
	case !f.Set:
		return cur
	case f.Null:
		return pgtype.Date{}
	}
	return pgtype.Date{Time: f.Value.Time, Valid: true}
}
