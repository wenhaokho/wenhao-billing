package platform

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
	"github.com/wenhaokho/wenhao-billing/backend/internal/recurringschedule"
)

type recurringRowSchema struct {
	TemplateID          string  `json:"template_id" format:"uuid"`
	CustomerID          *string `json:"customer_id" format:"uuid"`
	CustomerName        *string `json:"customer_name"`
	Currency            string  `json:"currency"`
	Amount              string  `json:"amount"`
	PaymentTerms        *string `json:"payment_terms"`
	ScheduleDescription string  `json:"schedule_description"`
	Frequency           *string `json:"frequency"`
	StartDate           *string `json:"start_date" format:"date"`
	EndMode             *string `json:"end_mode"`
	NextRunDate         *string `json:"next_run_date" format:"date"`
	PreviousIssueDate   *string `json:"previous_issue_date" format:"date"`
	GeneratedCount      int64   `json:"generated_count"`
	Status              string  `json:"status"`
}
type recurringRow struct {
	TemplateID          pgtype.UUID `json:"template_id"`
	CustomerID          pgtype.UUID `json:"customer_id"`
	CustomerName        pgtype.Text `json:"customer_name"`
	Currency            string      `json:"currency"`
	Amount              *string     `json:"amount"`
	PaymentTerms        pgtype.Text `json:"payment_terms"`
	ScheduleDescription string      `json:"schedule_description"`
	Frequency           *string     `json:"frequency"`
	StartDate           *string     `json:"start_date"`
	EndMode             *string     `json:"end_mode"`
	NextRunDate         *string     `json:"next_run_date"`
	PreviousIssueDate   *string     `json:"previous_issue_date"`
	GeneratedCount      int64       `json:"generated_count"`
	Status              string      `json:"status"`
}

func registerRecurringRows(api huma.API, s sessionHandler, responses map[string]*huma.Response) {
	huma.Register(api, huma.Operation{Method: "GET", Path: "/api/v1/invoices/recurring-templates/rows", OperationID: "recurring-template-rows", Responses: responses, Security: []map[string][]string{{"sessionCookie": {}}}, Middlewares: huma.Middlewares{sessionMiddleware(s.recurringRows)}}, func(context.Context, *struct{}) (*struct {
		Body []recurringRowSchema `nullable:"false"`
	}, error) {
		panic("middleware")
	})
}
func (s sessionHandler) recurringRows(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	rows, err := dbquery.New(s.db).RecurringTemplateRows(r.Context())
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	writeRecurringRows(w, rows)
}

func writeRecurringRows(w http.ResponseWriter, rows []dbquery.RecurringTemplateRowsRow) {
	out := make([]recurringRow, 0, len(rows))
	for _, v := range rows {
		amount, e := checkedNum(v.Amount, true)
		if e != nil || !v.CreatedAt.Valid || v.CreatedAt.InfinityModifier != pgtype.Finite || v.CreatedAt.Time.Year() < 1 || v.CreatedAt.Time.Year() > 9999 {
			authError(w, 500, "internal server error")
			return
		}
		var prev *time.Time
		if v.PreviousIssueDate.Valid {
			d := v.PreviousIssueDate.Time
			if v.PreviousIssueDate.InfinityModifier != pgtype.Finite || d.Year() < 1 || d.Year() > 9999 {
				authError(w, 500, "internal server error")
				return
			}
			prev = &d
		}
		var ref map[string]json.RawMessage
		if json.Unmarshal(v.PreviousRef, &ref) == nil {
			var key string
			if json.Unmarshal(ref["cycle_key"], &key) == nil && key != "" {
				if d, e := parseCycleDate(key); e == nil && d.Year() >= 1 && d.Year() <= 9999 {
					prev = &d
				}
			}
		}
		row := recurringRow{TemplateID: v.InvoiceID, CustomerID: v.CustomerID, CustomerName: v.CustomerName, Currency: v.Currency, Amount: amount, PaymentTerms: v.PaymentTerms, GeneratedCount: v.GeneratedCount, ScheduleDescription: "Schedule not configured", Status: "MISCONFIGURED"}
		if prev != nil {
			str := prev.Format("2006-01-02")
			row.PreviousIssueDate = &str
		}
		if schedule, e := recurringschedule.Parse(v.BillingCycleRef); e == nil {
			row.ScheduleDescription = recurringschedule.Describe(schedule)
			row.Frequency = &schedule.Frequency
			row.EndMode = &schedule.EndMode
			start := schedule.StartDate.Format("2006-01-02")
			row.StartDate = &start
			anchor := schedule.StartDate.AddDate(0, 0, -1)
			if prev != nil {
				anchor = *prev
			}
			next, e := recurringschedule.NextCycleAfter(schedule, anchor)
			if e != nil {
				authError(w, 500, "internal server error")
				return
			}
			if next == nil {
				row.Status = "ENDED"
			} else {
				str := next.Format("2006-01-02")
				row.NextRunDate = &str
				row.Status = "ACTIVE"
				if recurringschedule.IsPaused(v.BillingCycleRef) {
					row.Status = "PAUSED"
				}
			}
		}
		out = append(out, row)
	}
	w.Header().Set("Content-Type", "application/json")
	writeSafeJSON(w, out)
}

func parseCycleDate(key string) (time.Time, error) { return recurringschedule.ParseDate(key) }
