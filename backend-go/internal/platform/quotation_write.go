package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
)

// The v7 and staged v8 routes share request validation, money calculations and
// transaction ordering. Each supplies the queries for its own schema.
type quotationWriter struct {
	pool    *pgxpool.Pool
	admin   func(http.ResponseWriter, *http.Request) (pgtype.UUID, bool)
	queries func(pgx.Tx) quotationWriteQueries
	scoped  bool
}

type quotationWriteQueries interface {
	sendDetails(context.Context, pgtype.UUID) (quotationSendDetails, error)
	recordQuotationSend(context.Context, pgtype.UUID) error
	lockAdminRole(context.Context, pgtype.UUID) (string, error)
	lockCustomer(context.Context, pgtype.UUID) error
	lockProject(context.Context, pgtype.UUID) error
	lockItem(context.Context, pgtype.UUID) error
	LockQuotationNumberAllocation(context.Context) error
	QuotationNumbersForPrefix(context.Context, pgtype.Text) ([]pgtype.Text, error)
	InsertQuotation(context.Context, dbquery.InsertQuotationParams) error
	InsertQuotationLine(context.Context, dbquery.InsertQuotationLineParams) error
	LockQuotation(context.Context, pgtype.UUID) (dbquery.Quotation, error)
	GetQuotation(context.Context, pgtype.UUID) (dbquery.Quotation, error)
	QuotationLines(context.Context, []pgtype.UUID) ([]dbquery.QuotationLineItem, error)
	DeleteQuotationLines(context.Context, pgtype.UUID) error
	UpdateQuotation(context.Context, dbquery.UpdateQuotationParams) error
	deleteQuotation(context.Context, pgtype.UUID) error
	changeQuotationStatus(context.Context, pgtype.UUID, string, *string) error
}

func (s sessionHandler) quotationWriter() quotationWriter {
	return quotationWriter{
		pool: s.db,
		admin: func(w http.ResponseWriter, r *http.Request) (pgtype.UUID, bool) {
			u, ok := s.requireAdmin(w, r)
			return u.UserID, ok
		},
		queries: func(tx pgx.Tx) quotationWriteQueries {
			return legacyQuotationWriteQueries{Queries: dbquery.New(tx), tx: tx}
		},
	}
}

func (s quotationWriter) quotationNotFound(w http.ResponseWriter, id pgtype.UUID) {
	if s.scoped {
		authError(w, 400, "quotation not found")
		return
	}
	authError(w, 400, fmt.Sprintf("quotation %s not found", id.String()))
}

func (s quotationWriter) acceptReference(w http.ResponseWriter, err error) bool {
	if errors.Is(err, pgx.ErrNoRows) {
		if !s.scoped {
			// The v7 contract leaves missing references to its existing FK handling.
			return true
		}
		authError(w, 400, "referenced record not found")
		return false
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return false
	}
	return true
}

type legacyQuotationWriteQueries struct {
	*dbquery.Queries
	tx pgx.Tx
}

func (q legacyQuotationWriteQueries) lockAdminRole(ctx context.Context, id pgtype.UUID) (string, error) {
	user, err := q.LockSessionUserByID(ctx, id)
	return user.Role, err
}

func (q legacyQuotationWriteQueries) lockCustomer(ctx context.Context, id pgtype.UUID) error {
	_, err := q.LockCustomer(ctx, id)
	return err
}

func (q legacyQuotationWriteQueries) lockProject(ctx context.Context, id pgtype.UUID) error {
	_, err := q.LockProject(ctx, id)
	return err
}

func (q legacyQuotationWriteQueries) lockItem(ctx context.Context, id pgtype.UUID) error {
	_, err := q.LockItem(ctx, id)
	return err
}

// Quotation positions accept integer strings, numbers with an exact integer
// value, and JSON booleans. String exponents are invalid even when integral.
func quotationPosition(value any) (*big.Int, bool) {
	var text string
	switch v := value.(type) {
	case bool:
		if v {
			return big.NewInt(1), true
		}
		return big.NewInt(0), true
	case json.Number:
		text = v.String()
	case string:
		text = strings.TrimSpace(v)
		if !billIntegerString.MatchString(text) {
			return nil, false
		}
	default:
		return nil, false
	}
	rat, ok := boundedItemRat(text)
	if !ok || !rat.IsInt() {
		return nil, false
	}
	return rat.Num(), true
}

func (q legacyQuotationWriteQueries) deleteQuotation(ctx context.Context, id pgtype.UUID) error {
	_, err := q.tx.Exec(ctx, `DELETE FROM quotations WHERE quotation_id=$1`, id)
	return err
}

func (q legacyQuotationWriteQueries) changeQuotationStatus(ctx context.Context, id pgtype.UUID, action string, acceptedBy *string) error {
	var err error
	switch action {
	case "mark-sent":
		_, err = q.tx.Exec(ctx, `UPDATE quotations SET status='SENT', last_sent_at=timezone('UTC', clock_timestamp()) WHERE quotation_id=$1`, id)
	case "accept":
		_, err = q.tx.Exec(ctx, `UPDATE quotations SET status='ACCEPTED', accepted_at=timezone('UTC', clock_timestamp()), accepted_by=$2 WHERE quotation_id=$1`, id, acceptedBy)
	case "decline":
		_, err = q.tx.Exec(ctx, `UPDATE quotations SET status='DECLINED' WHERE quotation_id=$1`, id)
	case "void":
		_, err = q.tx.Exec(ctx, `UPDATE quotations SET status='VOID' WHERE quotation_id=$1`, id)
	}
	return err
}
