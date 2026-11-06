package platform

import (
	"context"
	"errors"
	"net/http"
	"reflect"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
)

type paymentOutput struct {
	PaymentID       pgtype.UUID     `json:"payment_id"`
	InvoiceID       pgtype.UUID     `json:"invoice_id"`
	CustomerID      pgtype.UUID     `json:"customer_id"`
	Amount          string          `json:"amount"`
	WhtAmount       string          `json:"wht_amount"`
	Currency        string          `json:"currency"`
	PayerName       string          `json:"payer_name"`
	PayerReference  pgtype.Text     `json:"payer_reference"`
	PaymentDate     pgtype.Date     `json:"payment_date"`
	IntakeSource    string          `json:"intake_source"`
	ExternalRef     pgtype.Text     `json:"external_ref"`
	Status          string          `json:"status"`
	AdjustmentType  string          `json:"adjustment_type"`
	ConfidenceScore *string         `json:"confidence_score"`
	CreatedAt       pythonTimestamp `json:"created_at"`
}
type paymentSchema struct {
	PaymentID       string  `json:"payment_id" format:"uuid"`
	InvoiceID       *string `json:"invoice_id" format:"uuid"`
	CustomerID      *string `json:"customer_id" format:"uuid"`
	Amount          string  `json:"amount"`
	WhtAmount       string  `json:"wht_amount"`
	Currency        string  `json:"currency"`
	PayerName       string  `json:"payer_name"`
	PayerReference  *string `json:"payer_reference"`
	PaymentDate     string  `json:"payment_date" format:"date"`
	IntakeSource    string  `json:"intake_source"`
	ExternalRef     *string `json:"external_ref"`
	Status          string  `json:"status"`
	AdjustmentType  string  `json:"adjustment_type"`
	ConfidenceScore *string `json:"confidence_score"`
	CreatedAt       string  `json:"created_at" format:"date-time"`
}

func paymentJSON(p dbquery.Payment) (paymentOutput, error) {
	amount, e := billNumeric(p.Amount, false)
	if e != nil {
		return paymentOutput{}, e
	}
	wht, e := billNumeric(p.WhtAmount, false)
	if e != nil {
		return paymentOutput{}, e
	}
	var confidence *string
	if p.ConfidenceScore.Valid {
		if p.ConfidenceScore.NaN || p.ConfidenceScore.InfinityModifier != pgtype.Finite || p.ConfidenceScore.Int == nil {
			return paymentOutput{}, errors.New("invalid numeric")
		}
		c := decimal.NewFromBigInt(p.ConfidenceScore.Int, p.ConfidenceScore.Exp).StringFixed(2)
		confidence = &c
	}
	return paymentOutput{p.PaymentID, p.InvoiceID, p.CustomerID, *amount, *wht, p.Currency, p.PayerName, p.PayerReference, p.PaymentDate, p.IntakeSource, p.ExternalRef, p.Status, p.AdjustmentType, confidence, pythonTimestamp(p.CreatedAt)}, nil
}

type paymentGetInput struct {
	ID string `path:"payment_id" format:"uuid"`
}

func registerPaymentReads(api huma.API, s sessionHandler) {
	security := []map[string][]string{{"sessionCookie": {}}}
	m := huma.Middlewares{sessionMiddleware(s.paymentRead)}
	response := func(desc string, body any) *huma.Response {
		return &huma.Response{Description: desc, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(body), true, "")}}}
	}
	responses := func(detail bool) map[string]*huma.Response {
		r := map[string]*huma.Response{"401": response("Not authenticated", authErrorBody{}), "403": response("Admin required", authErrorBody{}), "422": response("Validation error", validationErrorBody{}), "500": response("Internal server error", authErrorBody{})}
		if detail {
			r["404"] = response("Payment not found", authErrorBody{})
		}
		return r
	}
	huma.Register(api, huma.Operation{Method: "GET", Path: "/api/v1/payments/awaiting-review", OperationID: "payments-awaiting-review", Security: security, Middlewares: m, Responses: responses(false)}, func(context.Context, *struct{}) (*struct {
		Body []paymentSchema `nullable:"false"`
	}, error) {
		panic("middleware")
	})
	huma.Register(api, huma.Operation{Method: "GET", Path: "/api/v1/payments/{payment_id}", OperationID: "get-payment", Security: security, Middlewares: m, Responses: responses(true)}, func(context.Context, *paymentGetInput) (*struct{ Body paymentSchema }, error) { panic("middleware") })
}
func (s sessionHandler) paymentRead(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	q := dbquery.New(s.db)
	raw := chi.URLParam(r, "payment_id")
	var rows []dbquery.Payment
	if raw != "" {
		id, ok := parsePathUUID(raw)
		if !ok {
			d := uuidErrorDetail(raw)
			validationResponse(w, []validationError{{"uuid_parsing", []any{"path", "payment_id"}, "Input should be a valid UUID, " + d, raw, map[string]string{"error": d}}})
			return
		}
		p, e := q.GetPayment(r.Context(), id)
		if errors.Is(e, pgx.ErrNoRows) {
			authError(w, 404, "payment not found")
			return
		}
		if e != nil {
			authError(w, 500, "internal server error")
			return
		}
		rows = []dbquery.Payment{p}
	} else {
		var e error
		rows, e = q.ListPaymentsAwaitingReview(r.Context())
		if e != nil {
			authError(w, 500, "internal server error")
			return
		}
	}
	out := make([]paymentOutput, 0, len(rows))
	for _, p := range rows {
		v, e := paymentJSON(p)
		if e != nil {
			authError(w, 500, "internal server error")
			return
		}
		out = append(out, v)
	}
	w.Header().Set("Content-Type", "application/json")
	if raw != "" {
		writeSafeJSON(w, out[0])
	} else {
		writeSafeJSON(w, out)
	}
}
