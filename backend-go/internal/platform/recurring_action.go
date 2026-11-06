package platform

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
	"github.com/wenhaokho/wenhao-billing/backend/internal/recurringschedule"
)

type recurringActionInput struct {
	ID   string `path:"template_id" format:"uuid"`
	Body struct {
		Action string `json:"action"`
	}
}

func registerRecurringAction(api huma.API, s sessionHandler) {
	responses := map[string]*huma.Response{}
	for _, code := range []string{"400", "401", "403", "404", "409", "413", "422", "500"} {
		responses[code] = &huma.Response{Description: "Request failed", Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(authErrorBody{}), true, "")}}}
	}
	huma.Register(api, huma.Operation{Method: "PATCH", Path: "/api/v1/invoices/recurring-templates/{template_id}", OperationID: "patch-recurring-template", SkipValidateBody: true, Responses: responses, Security: []map[string][]string{{"sessionCookie": {}}}, Middlewares: huma.Middlewares{sessionMiddleware(s.recurringAction)}}, func(context.Context, *recurringActionInput) (*struct{ Body invoiceOutputSchema }, error) {
		panic("middleware")
	})
}
func (s sessionHandler) recurringAction(w http.ResponseWriter, r *http.Request) {
	body, ok := readItemBody(w, r)
	if !ok {
		return
	}
	current, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	raw := chi.URLParam(r, "template_id")
	id, ok := parsePathUUID(raw)
	if !ok {
		validationResponse(w, []validationError{{"uuid_parsing", []any{"path", "template_id"}, "Input should be a valid UUID", nil, nil}})
		return
	}
	var data map[string]json.RawMessage
	if len(body) == 0 {
		validationResponse(w, []validationError{{"missing", []any{"body"}, "Field required", nil, nil}})
		return
	}
	if json.Unmarshal(body, &data) != nil || data == nil {
		validationResponse(w, []validationError{{"model_attributes_type", []any{"body"}, "Input should be a valid dictionary or object to extract fields from", nil, nil}})
		return
	}
	v, exists := data["action"]
	if !exists {
		validationResponse(w, []validationError{{"missing", []any{"body", "action"}, "Field required", nil, nil}})
		return
	}
	var action string
	if json.Unmarshal(v, &action) != nil || string(v) == "null" {
		validationResponse(w, []validationError{{"string_type", []any{"body", "action"}, "Input should be a valid string", nil, nil}})
		return
	}
	action = strings.ToUpper(action)
	ctx := r.Context()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	defer tx.Rollback(ctx)
	q := dbquery.New(tx)
	user, err := q.LockSessionUserByID(ctx, current.UserID)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && user.Role != "admin" {
		authError(w, 403, "admin required")
		return
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	row, err := q.LockInvoice(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !row.IsTemplate {
		authError(w, 404, "recurring template not found")
		return
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	var cfg map[string]json.RawMessage
	if len(row.BillingCycleRef) == 0 || string(row.BillingCycleRef) == "null" {
		cfg = map[string]json.RawMessage{}
	} else if json.Unmarshal(row.BillingCycleRef, &cfg) != nil || cfg == nil {
		authError(w, 500, "internal server error")
		return
	}
	schedule, scheduleErr := recurringschedule.Parse(row.BillingCycleRef)
	var next *time.Time
	if scheduleErr == nil && (action == "PAUSE" || action == "RESUME" || action == "END_NOW") {
		anchor := schedule.StartDate.AddDate(0, 0, -1)
		child, e := q.LatestRecurringChild(ctx, id)
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			authError(w, 500, "internal server error")
			return
		}
		if e == nil {
			if child.IssueDate.Valid {
				anchor = child.IssueDate.Time
			}
			var ref map[string]json.RawMessage
			var key string
			if json.Unmarshal(child.BillingCycleRef, &ref) == nil && json.Unmarshal(ref["cycle_key"], &key) == nil && key != "" {
				if d, e := parseCycleDate(key); e == nil {
					anchor = d
				}
			}
		}
		next, err = recurringschedule.NextCycleAfter(schedule, anchor)
		if err != nil {
			authError(w, 500, "internal server error")
			return
		}
	}
	ended := scheduleErr == nil && next == nil
	switch action {
	case "PAUSE", "RESUME":
		if ended {
			authError(w, 409, "recurring template has ended and is view-only")
			return
		}
		if action == "PAUSE" {
			cfg["paused"] = json.RawMessage("true")
		} else {
			delete(cfg, "paused")
		}
	case "END_NOW":
		if !ended {
			now := time.Now()
			end := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
			if next != nil && next.AddDate(0, 0, -1).Before(end) {
				end = next.AddDate(0, 0, -1)
			}
			cfg["end_mode"] = json.RawMessage(`"ON_DATE"`)
			b, _ := json.Marshal(end.Format("2006-01-02"))
			cfg["end_date"] = b
			delete(cfg, "end_after_cycles")
		}
		delete(cfg, "paused")
	default:
		authError(w, 400, "unknown action")
		return
	}
	row.BillingCycleRef, err = json.Marshal(cfg)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if err = q.UpdateTemplateSchedule(ctx, dbquery.UpdateTemplateScheduleParams{InvoiceID: id, BillingCycleRef: row.BillingCycleRef}); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	lines, err := q.InvoiceLinesByIDs(ctx, []pgtype.UUID{id})
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	out, err := invoiceJSON(row, lines)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if tx.Commit(ctx) != nil {
		authError(w, 500, "internal server error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(append(encoded, '\n'))
}
