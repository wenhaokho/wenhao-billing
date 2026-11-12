package stats

import (
	"github.com/google/uuid"

	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// ActivityItem is one reconciliation_log row with the context of its payment
// (Python's ActivityItem). Everything the left joins can miss is null.
type ActivityItem struct {
	LogID           uuid.UUID      `json:"log_id"`
	PaymentID       uuid.UUID      `json:"payment_id"`
	Action          string         `json:"action"`
	Reasons         []string       `json:"reasons" nullable:"false"`
	ReasonCodes     []string       `json:"reason_codes" nullable:"false" enum:"NO_CANDIDATE,CURRENCY_MISMATCH,AMOUNT_MISMATCH,LOW_CONFIDENCE,AMBIGUOUS,CLEAR_FAILED,EXACT_MATCH,MANUAL_ENTRY,MANUAL_APPROVAL,ALIAS_MATCH,OTHER" doc:"The code of each reason, in the same order (payments.ReasonCode)."`
	ActorUserID     *uuid.UUID     `json:"actor_user_id"`
	CreatedAt       *platform.Time `json:"created_at"`
	PayerName       *string        `json:"payer_name"`
	Amount          *platform.Dec4 `json:"amount"`
	Currency        *string        `json:"currency"`
	PaymentDate     *platform.Date `json:"payment_date"`
	PayerReference  *string        `json:"payer_reference"`
	WhtAmount       *platform.Dec4 `json:"wht_amount"`
	ConfidenceScore *platform.Dec2 `json:"confidence_score"`
	IntakeSource    *string        `json:"intake_source"`
	InvoiceID       *uuid.UUID     `json:"invoice_id"`
	InvoiceNumber   *string        `json:"invoice_number"`
	CustomerName    *string        `json:"customer_name" doc:"The payment's customer, else its invoice's."`
	ActorName       *string        `json:"actor_name" doc:"The actor's display name, else their email."`
}

// UpcomingInvoiceItem is the next generation date of one recurring template
// or one DRAFT usage invoice (Python's UpcomingInvoiceItem).
type UpcomingInvoiceItem struct {
	InvoiceID        uuid.UUID     `json:"invoice_id"`
	CustomerID       *uuid.UUID    `json:"customer_id"`
	CustomerName     *string       `json:"customer_name"`
	Mode             string        `json:"mode" enum:"RECURRING,USAGE"`
	NextDate         platform.Date `json:"next_date"`
	Amount           platform.Dec4 `json:"amount"`
	Currency         string        `json:"currency"`
	AmountIsEstimate bool          `json:"amount_is_estimate" doc:"True for usage invoices, whose amount is still accruing."`
}

// DashboardStats is the dashboard response (Python's DashboardStats): the
// fields of DashboardCore with recent_activity after the quotation pipeline
// and upcoming_invoices last.
type DashboardStats struct {
	AwaitingReviewCount   int                       `json:"awaiting_review_count"`
	DraftCount            int                       `json:"draft_count" doc:"DRAFT invoices that are not templates."`
	SentCount             int                       `json:"sent_count" doc:"OPEN and SENT invoices (templates and PARTIAL as Python counts them)."`
	CustomerCount         int                       `json:"customer_count" doc:"Every customer, inactive ones included."`
	AutoClearedLast30d    int                       `json:"auto_cleared_last_30d" doc:"Every CLEARED payment created in the last 30 days, manual ones included."`
	OpenARByCurrency      []invoices.CurrencyAmount `json:"open_ar_by_currency" nullable:"false"`
	OpenQuotationCount    int                       `json:"open_quotation_count"`
	OpenQuotationPipeline []invoices.CurrencyAmount `json:"open_quotation_pipeline" nullable:"false" doc:"Largest total first."`
	RecentActivity        []ActivityItem            `json:"recent_activity" nullable:"false" doc:"The ten newest reconciliation log rows."`
	MRRByCurrency         []invoices.CurrencyAmount `json:"mrr_by_currency" nullable:"false"`
	BaseCurrency          string                    `json:"base_currency"`
	OpenARBaseByCurrency  []CurrencyBaseAmount      `json:"open_ar_base_by_currency" nullable:"false" doc:"Largest first."`
	OpenARUnconverted     []string                  `json:"open_ar_unconverted" nullable:"false" doc:"Currencies with no FX rate, left out of open_ar_base_by_currency."`
	UpcomingInvoices      []UpcomingInvoiceItem     `json:"upcoming_invoices" nullable:"false" doc:"Next generation dates within 90 days, by date."`
}

func newDashboardStats(c DashboardCore, activity []ActivityItem, upcoming []UpcomingInvoiceItem) DashboardStats {
	return DashboardStats{
		AwaitingReviewCount: c.AwaitingReviewCount, DraftCount: c.DraftCount, SentCount: c.SentCount,
		CustomerCount: c.CustomerCount, AutoClearedLast30d: c.AutoClearedLast30d,
		OpenARByCurrency: c.OpenARByCurrency, OpenQuotationCount: c.OpenQuotationCount,
		OpenQuotationPipeline: c.OpenQuotationPipeline, RecentActivity: activity,
		MRRByCurrency: c.MRRByCurrency, BaseCurrency: c.BaseCurrency,
		OpenARBaseByCurrency: c.OpenARBaseByCurrency, OpenARUnconverted: c.OpenARUnconverted,
		UpcomingInvoices: upcoming,
	}
}
