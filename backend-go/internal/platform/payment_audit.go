package platform

import (
	"context"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
)

type auditInput struct {
	Action string `query:"action"`
	Payer  string `query:"payer"`
	From   string `query:"from_date" format:"date"`
	To     string `query:"to_date" format:"date"`
	Limit  int64  `query:"limit" default:"200" maximum:"500"`
}
type auditOutput struct {
	LogID        pgtype.UUID     `json:"log_id"`
	PaymentID    pgtype.UUID     `json:"payment_id"`
	Action       string          `json:"action"`
	Reasons      []string        `json:"reasons"`
	ActorUserID  pgtype.UUID     `json:"actor_user_id"`
	ActorEmail   pgtype.Text     `json:"actor_email"`
	CreatedAt    pythonTimestamp `json:"created_at"`
	PayerName    pgtype.Text     `json:"payer_name"`
	Amount       *string         `json:"amount"`
	Currency     pgtype.Text     `json:"currency"`
	InvoiceID    pgtype.UUID     `json:"invoice_id"`
	IntakeSource pgtype.Text     `json:"intake_source"`
}
type auditSchema struct {
	LogID        string   `json:"log_id" format:"uuid"`
	PaymentID    string   `json:"payment_id" format:"uuid"`
	Action       string   `json:"action"`
	Reasons      []string `json:"reasons" nullable:"false"`
	ActorUserID  *string  `json:"actor_user_id" format:"uuid"`
	ActorEmail   *string  `json:"actor_email"`
	CreatedAt    *string  `json:"created_at" format:"date-time"`
	PayerName    *string  `json:"payer_name"`
	Amount       *string  `json:"amount"`
	Currency     *string  `json:"currency"`
	InvoiceID    *string  `json:"invoice_id" format:"uuid"`
	IntakeSource *string  `json:"intake_source"`
}
type logSchema struct {
	LogID       string   `json:"log_id" format:"uuid"`
	Action      string   `json:"action"`
	Reasons     []string `json:"reasons" nullable:"false"`
	ActorUserID *string  `json:"actor_user_id" format:"uuid"`
	CreatedAt   *string  `json:"created_at" format:"date-time"`
}
type logOutput struct {
	LogID       pgtype.UUID     `json:"log_id"`
	Action      string          `json:"action"`
	Reasons     []string        `json:"reasons"`
	ActorUserID pgtype.UUID     `json:"actor_user_id"`
	CreatedAt   pythonTimestamp `json:"created_at"`
}

func registerPaymentAudit(api huma.API, s sessionHandler) {
	responses := map[string]*huma.Response{}
	for code, desc := range map[string]string{"401": "Not authenticated", "403": "Admin required", "422": "Validation error", "500": "Internal server error"} {
		typ := reflect.TypeOf(authErrorBody{})
		if code == "422" {
			typ = reflect.TypeOf(validationErrorBody{})
		}
		responses[code] = &huma.Response{Description: desc, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(typ, true, "")}}}
	}
	responses["500"].Content["text/plain"] = &huma.MediaType{Schema: &huma.Schema{Type: "string"}}
	op := func(path, id string) huma.Operation {
		return huma.Operation{Method: "GET", Path: path, OperationID: id, Security: []map[string][]string{{"sessionCookie": {}}}, Middlewares: huma.Middlewares{sessionMiddleware(s.paymentAudit)}, Responses: responses}
	}
	huma.Register(api, op("/api/v1/payments/audit-log", "payment-audit-log"), func(context.Context, *auditInput) (*struct {
		Body []auditSchema `nullable:"false"`
	}, error) {
		panic("middleware")
	})
	huma.Register(api, op("/api/v1/payments/{payment_id}/log", "payment-log"), func(context.Context, *paymentGetInput) (*struct {
		Body []logSchema `nullable:"false"`
	}, error) {
		panic("middleware")
	})
}
func (s sessionHandler) paymentAudit(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	q := dbquery.New(s.db)
	if raw := chi.URLParam(r, "payment_id"); raw != "" {
		id, ok := parsePathUUID(raw)
		if !ok {
			d := uuidErrorDetail(raw)
			validationResponse(w, []validationError{{"uuid_parsing", []any{"path", "payment_id"}, "Input should be a valid UUID, " + d, raw, map[string]string{"error": d}}})
			return
		}
		rows, e := q.PaymentLogs(r.Context(), id)
		if e != nil {
			authError(w, 500, "internal server error")
			return
		}
		out := make([]logOutput, 0, len(rows))
		for _, v := range rows {
			out = append(out, logOutput{v.LogID, v.Action, v.Reasons, v.ActorUserID, pythonTimestamp(v.CreatedAt)})
		}
		w.Header().Set("Content-Type", "application/json")
		writeSafeJSON(w, out)
		return
	}
	args, ok := parsePaymentAudit(w, r)
	if !ok {
		return
	}
	rows, e := q.PaymentAudit(r.Context(), args)
	if e != nil {
		authError(w, 500, "internal server error")
		return
	}
	out := make([]auditOutput, 0, len(rows))
	for _, v := range rows {
		amount, e := billNumeric(v.Amount, true)
		if e != nil {
			authError(w, 500, "internal server error")
			return
		}
		reasons := v.Reasons
		if reasons == nil {
			reasons = []string{}
		}
		out = append(out, auditOutput{v.LogID, v.PaymentID, v.Action, reasons, v.ActorUserID, v.ActorEmail, pythonTimestamp(v.CreatedAt), v.PayerName, amount, v.Currency, v.InvoiceID, v.IntakeSource})
	}
	w.Header().Set("Content-Type", "application/json")
	writeSafeJSON(w, out)
}

func parsePaymentAudit(w http.ResponseWriter, r *http.Request) (dbquery.PaymentAuditParams, bool) {
	values := r.URL.Query()
	args := dbquery.PaymentAuditParams{RowLimit: 200}
	var errs []validationError
	for _, entry := range []struct {
		name string
		dest *pgtype.Text
	}{{"action", &args.Action}, {"payer", &args.Payer}} {
		if v, ok := values[entry.name]; ok && v[len(v)-1] != "" {
			*entry.dest = pgtype.Text{String: v[len(v)-1], Valid: true}
		}
	}
	for _, entry := range []struct {
		name string
		dest *pgtype.Timestamp
		end  bool
	}{{"from_date", &args.FromDate, false}, {"to_date", &args.ToDate, true}} {
		if v, ok := values[entry.name]; ok {
			raw := v[len(v)-1]
			t, e := fxDate(raw)
			if e != nil {
				e.Loc = []any{"query", entry.name}
				errs = append(errs, *e)
			} else {
				if entry.end {
					t = t.AddDate(0, 0, 1)
				}
				*entry.dest = pgtype.Timestamp{Time: t, Valid: true}
			}
		}
	}
	if v, ok := values["limit"]; ok {
		raw := v[len(v)-1]
		n := strings.TrimSpace(raw)
		if !fxPathInteger.MatchString(n) {
			errs = append(errs, validationError{"int_parsing", []any{"query", "limit"}, "Input should be a valid integer, unable to parse string as an integer", raw, nil})
		} else {
			n = strings.ReplaceAll(n, "_", "")
			if dot := strings.IndexByte(n, '.'); dot >= 0 {
				n = n[:dot]
			}
			num, e := strconv.ParseInt(n, 10, 64)
			if e != nil {
				if strings.HasPrefix(n, "-") {
					num = -1
				} else {
					num = 501
				}
			}
			if num > 500 {
				errs = append(errs, validationError{"less_than_equal", []any{"query", "limit"}, "Input should be less than or equal to 500", raw, map[string]int{"le": 500}})
			} else {
				if num < 0 {
					args.RowLimit = -1
				} else {
					args.RowLimit = int32(num)
				}
			}
		}
	}
	if len(errs) > 0 {
		validationResponse(w, errs)
		return args, false
	}
	if args.RowLimit < 0 {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(500)
		w.Write([]byte("Internal Server Error"))
		return args, false
	}
	return args, true
}
