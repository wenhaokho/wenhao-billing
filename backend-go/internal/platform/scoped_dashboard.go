package platform

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
	"github.com/wenhaokho/wenhao-billing/backend/internal/recurringschedule"
)

type dashboardCurrencyAmount struct {
	Currency string `json:"currency"`
	Amount   string `json:"amount"`
}

type dashboardBaseAmount struct {
	Currency   string `json:"currency"`
	BaseAmount string `json:"base_amount"`
}

type dashboardActivity struct {
	LogID           pgtype.UUID     `json:"log_id"`
	PaymentID       pgtype.UUID     `json:"payment_id"`
	Action          string          `json:"action"`
	Reasons         []string        `json:"reasons"`
	ActorUserID     pgtype.UUID     `json:"actor_user_id"`
	CreatedAt       pythonTimestamp `json:"created_at"`
	PayerName       string          `json:"payer_name"`
	Amount          string          `json:"amount"`
	Currency        string          `json:"currency"`
	PaymentDate     pgtype.Date     `json:"payment_date"`
	PayerReference  pgtype.Text     `json:"payer_reference"`
	WhtAmount       string          `json:"wht_amount"`
	ConfidenceScore *string         `json:"confidence_score"`
	IntakeSource    string          `json:"intake_source"`
	InvoiceID       pgtype.UUID     `json:"invoice_id"`
	InvoiceNumber   pgtype.Text     `json:"invoice_number"`
	CustomerName    pgtype.Text     `json:"customer_name"`
	ActorName       pgtype.Text     `json:"actor_name"`
}

type dashboardUpcoming struct {
	InvoiceID        pgtype.UUID `json:"invoice_id"`
	CustomerID       pgtype.UUID `json:"customer_id"`
	CustomerName     pgtype.Text `json:"customer_name"`
	Mode             string      `json:"mode"`
	NextDate         pgtype.Date `json:"next_date"`
	Amount           string      `json:"amount"`
	Currency         string      `json:"currency"`
	AmountIsEstimate bool        `json:"amount_is_estimate"`
}

type dashboardOutput struct {
	AwaitingReviewCount   int64                     `json:"awaiting_review_count"`
	DraftCount            int64                     `json:"draft_count"`
	SentCount             int64                     `json:"sent_count"`
	CustomerCount         int64                     `json:"customer_count"`
	AutoClearedLast30d    int64                     `json:"auto_cleared_last_30d"`
	OpenQuotationCount    int64                     `json:"open_quotation_count"`
	OpenARByCurrency      []dashboardCurrencyAmount `json:"open_ar_by_currency"`
	OpenQuotationPipeline []dashboardCurrencyAmount `json:"open_quotation_pipeline"`
	RecentActivity        []dashboardActivity       `json:"recent_activity"`
	MRRByCurrency         []dashboardCurrencyAmount `json:"mrr_by_currency"`
	BaseCurrency          string                    `json:"base_currency"`
	OpenARBaseByCurrency  []dashboardBaseAmount     `json:"open_ar_base_by_currency"`
	OpenARUnconverted     []string                  `json:"open_ar_unconverted"`
	UpcomingInvoices      []dashboardUpcoming       `json:"upcoming_invoices"`
}

func (h scopedProfileRouter) dashboard(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.admin(w, r); !ok {
		return
	}
	out, err := h.readDashboard(r.Context(), time.Now())
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	writeSafeJSON(w, out)
}

func (h scopedProfileRouter) readDashboard(ctx context.Context, now time.Time) (dashboardOutput, error) {
	base := h.cfg.BaseCurrency
	if base == "" {
		base = "IDR"
	}
	out := dashboardOutput{
		BaseCurrency: base, OpenARByCurrency: []dashboardCurrencyAmount{},
		OpenQuotationPipeline: []dashboardCurrencyAmount{}, RecentActivity: []dashboardActivity{},
		MRRByCurrency: []dashboardCurrencyAmount{}, OpenARBaseByCurrency: []dashboardBaseAmount{},
		OpenARUnconverted: []string{}, UpcomingInvoices: []dashboardUpcoming{},
	}
	y, m, d := now.Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	tx, err := h.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	q := dbqueryv8.New(tx)
	businessID := h.scope.UUID()
	counts, err := q.GetDashboardCounts(ctx, dbqueryv8.GetDashboardCountsParams{
		BusinessID:   businessID,
		ClearedSince: pgtype.Timestamp{Time: now.UTC().AddDate(0, 0, -30), Valid: true},
	})
	if err != nil {
		return out, err
	}
	out.AwaitingReviewCount = counts.AwaitingReviewCount
	out.DraftCount = counts.DraftCount
	out.SentCount = counts.SentCount
	out.CustomerCount = counts.CustomerCount
	out.AutoClearedLast30d = counts.AutoClearedLast30d
	out.OpenQuotationCount = counts.OpenQuotationCount

	ar, err := q.GetDashboardOpenAR(ctx, businessID)
	if err != nil {
		return out, err
	}
	target := strings.ToUpper(base)
	convertedTotals := make(map[string]decimal.Decimal, len(ar))
	for _, row := range ar {
		amount, _, err := projectNumeric(row.Amount)
		if err != nil {
			return out, err
		}
		out.OpenARByCurrency = append(out.OpenARByCurrency, dashboardCurrencyAmount{row.Currency, dashboardNativeAmount(amount)})
		rate := decimal.NewFromInt(1)
		if source := strings.ToUpper(row.Currency); source != target {
			stored, err := q.GetFXRate(ctx, dbqueryv8.GetFXRateParams{
				BusinessID: businessID, FromCurrency: source, ToCurrency: target,
				AsOfDate: pgtype.Date{Time: today, Valid: true},
			})
			if errors.Is(err, pgx.ErrNoRows) {
				out.OpenARUnconverted = append(out.OpenARUnconverted, row.Currency)
				continue
			}
			if err != nil {
				return out, err
			}
			rate, _, err = projectNumeric(stored)
			if err != nil || !rate.IsPositive() {
				return out, errors.New("invalid dashboard FX rate")
			}
		}
		convertedTotals[row.Currency] = amount.Mul(rate).RoundBank(4)
		out.OpenARBaseByCurrency = append(out.OpenARBaseByCurrency, dashboardBaseAmount{
			row.Currency, monthlyRevenueConverted(amount, rate),
		})
	}
	sort.Strings(out.OpenARUnconverted)
	sort.Slice(out.OpenARBaseByCurrency, func(i, j int) bool {
		a, b := out.OpenARBaseByCurrency[i].Currency, out.OpenARBaseByCurrency[j].Currency
		if comparison := convertedTotals[a].Cmp(convertedTotals[b]); comparison != 0 {
			return comparison > 0
		}
		return a < b
	})

	quotations, err := q.GetDashboardOpenQuotations(ctx, businessID)
	if err != nil {
		return out, err
	}
	for _, row := range quotations {
		amount, _, err := projectNumeric(row.Amount)
		if err != nil {
			return out, err
		}
		out.OpenQuotationPipeline = append(out.OpenQuotationPipeline, dashboardCurrencyAmount{row.Currency, dashboardNativeAmount(amount)})
	}

	activity, err := q.GetDashboardActivity(ctx, businessID)
	if err != nil {
		return out, err
	}
	for _, row := range activity {
		item, err := dashboardActivityJSON(row)
		if err != nil {
			return out, err
		}
		out.RecentActivity = append(out.RecentActivity, item)
	}

	sources, err := q.GetDashboardUpcomingSources(ctx, businessID)
	if err != nil {
		return out, err
	}
	if err := out.addUpcoming(sources, today); err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

func dashboardNativeAmount(amount decimal.Decimal) string {
	if amount.IsZero() {
		return "0"
	}
	return amount.StringFixed(4)
}

func dashboardActivityJSON(row dbqueryv8.GetDashboardActivityRow) (dashboardActivity, error) {
	if row.InvalidReference {
		return dashboardActivity{}, errors.New("invalid dashboard activity reference")
	}
	if !row.CreatedAt.Valid || !row.PaymentDate.Valid || row.PaymentDate.InfinityModifier != pgtype.Finite ||
		row.PaymentDate.Time.Year() < 1 || row.PaymentDate.Time.Year() > 9999 {
		return dashboardActivity{}, errors.New("invalid dashboard activity date")
	}
	amount, err := billNumeric(row.Amount, false)
	if err != nil {
		return dashboardActivity{}, err
	}
	wht, err := billNumeric(row.WhtAmount, false)
	if err != nil {
		return dashboardActivity{}, err
	}
	var confidence *string
	if row.ConfidenceScore.Valid {
		value, _, err := projectNumeric(row.ConfidenceScore)
		if err != nil {
			return dashboardActivity{}, err
		}
		formatted := value.StringFixed(2)
		confidence = &formatted
	}
	reasons := row.Reasons
	if reasons == nil {
		reasons = []string{}
	}
	actorName := row.DisplayName
	if !actorName.Valid {
		actorName = row.ActorEmail
	}
	return dashboardActivity{
		LogID: row.LogID, PaymentID: row.PaymentID, Action: row.Action, Reasons: reasons,
		ActorUserID: row.ActorUserID, CreatedAt: pythonTimestamp(row.CreatedAt), PayerName: row.PayerName,
		Amount: *amount, Currency: row.Currency, PaymentDate: row.PaymentDate, PayerReference: row.PayerReference,
		WhtAmount: *wht, ConfidenceScore: confidence, IntakeSource: row.IntakeSource, InvoiceID: row.InvoiceID,
		InvoiceNumber: row.InvoiceNumber, CustomerName: row.CustomerName, ActorName: actorName,
	}, nil
}

func (out *dashboardOutput) addUpcoming(sources []dbqueryv8.GetDashboardUpcomingSourcesRow, today time.Time) error {
	horizon := today.AddDate(0, 0, 90)
	monthly := make(map[string]*big.Rat)
	for _, row := range sources {
		var next *time.Time
		isUsage := row.InvoiceType == "USAGE"
		if isUsage {
			next = dashboardUsageDate(row.BillingCycleRef, today)
			if next == nil {
				continue
			}
		} else {
			if recurringschedule.IsPaused(row.BillingCycleRef) {
				continue
			}
			schedule, err := recurringschedule.Parse(row.BillingCycleRef)
			if err != nil {
				continue
			}
			current, err := recurringschedule.CurrentCycle(schedule, today)
			if err != nil {
				continue
			}
			next, err = recurringschedule.NextCycleAfter(schedule, today)
			if err != nil {
				next = nil
			}
			if current == nil && next == nil {
				continue
			}
			amount, _, err := projectNumeric(row.Amount)
			if err != nil {
				return err
			}
			if monthly[row.Currency] == nil {
				monthly[row.Currency] = new(big.Rat)
			}
			monthly[row.Currency].Add(monthly[row.Currency], dashboardMonthlyEquivalent(amount, schedule))
		}

		if next == nil || next.After(horizon) {
			continue
		}
		if row.InvalidReference {
			return errors.New("invalid dashboard upcoming customer reference")
		}
		amount, err := billNumeric(row.Amount, false)
		if err != nil {
			return err
		}
		out.UpcomingInvoices = append(out.UpcomingInvoices, dashboardUpcoming{
			InvoiceID: row.InvoiceID, CustomerID: row.CustomerID, CustomerName: row.CustomerName,
			Mode: row.InvoiceType, NextDate: pgtype.Date{Time: *next, Valid: true},
			Amount: *amount, Currency: row.Currency, AmountIsEstimate: isUsage,
		})
	}
	// The input is sorted by UUID, which supplies Python's date tie-breaker.
	sort.SliceStable(out.UpcomingInvoices, func(i, j int) bool {
		return out.UpcomingInvoices[i].NextDate.Time.Before(out.UpcomingInvoices[j].NextDate.Time)
	})

	// Round the summed rational once to a four-place decimal, with ties to even.
	rounded := make(map[string]decimal.Decimal, len(monthly))
	for currency, total := range monthly {
		scaled := new(big.Rat).Mul(total, big.NewRat(10000, 1))
		value, remainder := new(big.Int), new(big.Int)
		value.QuoRem(scaled.Num(), scaled.Denom(), remainder)
		twiceRemainder := new(big.Int).Lsh(remainder.Abs(remainder), 1)
		comparison := twiceRemainder.Cmp(scaled.Denom())
		if comparison > 0 || (comparison == 0 && value.Bit(0) == 1) {
			value.Add(value, big.NewInt(int64(total.Sign())))
		}
		rounded[currency] = decimal.NewFromBigInt(value, -4)
		formatted := rounded[currency].StringFixed(4)
		if value.Sign() == 0 && total.Sign() < 0 {
			formatted = "-0.0000"
		}
		out.MRRByCurrency = append(out.MRRByCurrency, dashboardCurrencyAmount{currency, formatted})
	}
	sort.Slice(out.MRRByCurrency, func(i, j int) bool {
		a, b := out.MRRByCurrency[i].Currency, out.MRRByCurrency[j].Currency
		if comparison := rounded[a].Cmp(rounded[b]); comparison != 0 {
			return comparison > 0
		}
		return a < b
	})
	return nil
}

func dashboardMonthlyEquivalent(amount decimal.Decimal, schedule recurringschedule.Schedule) *big.Rat {
	cyclesPerYear := int64(12)
	switch schedule.Frequency {
	case "DAILY":
		cyclesPerYear = 365
	case "WEEKLY":
		cyclesPerYear = 52
	case "YEARLY":
		cyclesPerYear = 1
	}
	ratio := big.NewRat(cyclesPerYear, 12)
	ratio.Quo(ratio, big.NewRat(int64(schedule.Interval), 1))
	return ratio.Mul(ratio, amount.Rat())
}

func dashboardUsageDate(config []byte, today time.Time) *time.Time {
	var fields map[string]json.RawMessage
	if json.Unmarshal(config, &fields) != nil {
		return nil
	}
	var day int
	cutoff := fields["cut_off_day"]
	if string(cutoff) == "true" {
		day = 1
	} else if json.Unmarshal(cutoff, &day) != nil || day < 1 || day > 31 {
		return nil
	}
	month := today.AddDate(0, 0, 1-today.Day())
	next := month.AddDate(0, 0, min(day, month.AddDate(0, 1, -1).Day())-1)
	if next.Before(today) {
		month = month.AddDate(0, 1, 0)
		next = month.AddDate(0, 0, min(day, month.AddDate(0, 1, -1).Day())-1)
	}
	return &next
}
