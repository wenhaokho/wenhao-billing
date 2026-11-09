package stats

import (
	"context"

	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/internal/stats/statsdb"
)

// RecentActivity is the ten newest reconciliation log rows, newest first (the
// log id breaking ties), each with its payment, invoice, customer and actor.
// Never nil.
func RecentActivity(ctx context.Context, db statsdb.DBTX) ([]ActivityItem, error) {
	rows, err := statsdb.New(db).RecentActivity(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ActivityItem, 0, len(rows))
	for _, r := range rows {
		item := ActivityItem{
			LogID: r.LogID, PaymentID: r.PaymentID, Action: r.Action, Reasons: r.Reasons,
			ActorUserID: platform.UUIDPtr(r.ActorUserID),
			PayerName:   r.PayerName, Currency: r.Currency, PayerReference: r.PayerReference,
			IntakeSource: r.IntakeSource, InvoiceID: platform.UUIDPtr(r.InvoiceID),
			InvoiceNumber: r.InvoiceNumber, CustomerName: r.CustomerName,
			// coalesce(display_name, email): an empty display name is kept.
			ActorName: r.ActorDisplayName,
		}
		if item.Reasons == nil {
			item.Reasons = []string{}
		}
		if item.ActorName == nil {
			item.ActorName = r.ActorEmail
		}
		if r.CreatedAt != nil {
			item.CreatedAt = &platform.Time{Time: *r.CreatedAt}
		}
		if r.PaymentDate.Valid {
			item.PaymentDate = &platform.Date{Time: r.PaymentDate.Time}
		}
		item.Amount = dec4(r.Amount)
		item.WhtAmount = dec4(r.WhtAmount)
		if r.ConfidenceScore.Valid {
			c := platform.NewDec2(r.ConfidenceScore.Decimal)
			item.ConfidenceScore = &c
		}
		out = append(out, item)
	}
	return out, nil
}

func dec4(d decimal.NullDecimal) *platform.Dec4 {
	if !d.Valid {
		return nil
	}
	v := platform.NewDec4(d.Decimal)
	return &v
}
