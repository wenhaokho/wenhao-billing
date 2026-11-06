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

type billLineOutput struct {
	LineItemID       pgtype.UUID `json:"line_item_id"`
	ItemID           pgtype.UUID `json:"item_id"`
	ExpenseAccountID pgtype.Int4 `json:"expense_account_id"`
	Description      string      `json:"description"`
	Quantity         string      `json:"quantity"`
	UnitPrice        string      `json:"unit_price"`
	Amount           string      `json:"amount"`
	Position         int32       `json:"position"`
}
type billOutput struct {
	BillID        pgtype.UUID      `json:"bill_id"`
	VendorID      pgtype.UUID      `json:"vendor_id"`
	ProjectID     pgtype.UUID      `json:"project_id"`
	BillNumber    pgtype.Text      `json:"bill_number"`
	PoNumber      pgtype.Text      `json:"po_number"`
	Currency      string           `json:"currency"`
	Subtotal      *string          `json:"subtotal"`
	DiscountType  pgtype.Text      `json:"discount_type"`
	DiscountValue *string          `json:"discount_value"`
	Amount        string           `json:"amount"`
	BalanceDue    string           `json:"balance_due"`
	Status        string           `json:"status"`
	IssueDate     pgtype.Date      `json:"issue_date"`
	DueDate       pgtype.Date      `json:"due_date"`
	PaymentTerms  pgtype.Text      `json:"payment_terms"`
	Notes         pgtype.Text      `json:"notes"`
	CreatedAt     pythonTimestamp  `json:"created_at"`
	LineItems     []billLineOutput `json:"line_items"`
}
type billLineSchema struct {
	LineItemID       string  `json:"line_item_id" format:"uuid"`
	ItemID           *string `json:"item_id" format:"uuid"`
	ExpenseAccountID *int32  `json:"expense_account_id"`
	Description      string  `json:"description"`
	Quantity         string  `json:"quantity"`
	UnitPrice        string  `json:"unit_price"`
	Amount           string  `json:"amount"`
	Position         int32   `json:"position"`
}
type billSchema struct {
	BillID        string           `json:"bill_id" format:"uuid"`
	VendorID      string           `json:"vendor_id" format:"uuid"`
	ProjectID     *string          `json:"project_id" format:"uuid"`
	BillNumber    *string          `json:"bill_number"`
	PoNumber      *string          `json:"po_number"`
	Currency      string           `json:"currency"`
	Subtotal      *string          `json:"subtotal"`
	DiscountType  *string          `json:"discount_type"`
	DiscountValue *string          `json:"discount_value"`
	Amount        string           `json:"amount"`
	BalanceDue    string           `json:"balance_due"`
	Status        string           `json:"status"`
	IssueDate     *string          `json:"issue_date" format:"date"`
	DueDate       *string          `json:"due_date" format:"date"`
	PaymentTerms  *string          `json:"payment_terms"`
	Notes         *string          `json:"notes"`
	CreatedAt     string           `json:"created_at" format:"date-time"`
	LineItems     []billLineSchema `json:"line_items" nullable:"false"`
}

func billNumeric(n pgtype.Numeric, optional bool) (*string, error) {
	if !n.Valid {
		if optional {
			return nil, nil
		}
		return nil, errors.New("missing numeric")
	}
	if n.NaN || n.InfinityModifier != pgtype.Finite || n.Int == nil {
		return nil, errors.New("invalid numeric")
	}
	v := decimal.NewFromBigInt(n.Int, n.Exp).StringFixed(4)
	return &v, nil
}
func billJSON(b dbquery.Bill, lines []dbquery.BillLineItem) (billOutput, error) {
	subtotal, e := billNumeric(b.Subtotal, true)
	if e != nil {
		return billOutput{}, e
	}
	discount, e := billNumeric(b.DiscountValue, true)
	if e != nil {
		return billOutput{}, e
	}
	amount, e := billNumeric(b.Amount, false)
	if e != nil {
		return billOutput{}, e
	}
	balance, e := billNumeric(b.BalanceDue, false)
	if e != nil {
		return billOutput{}, e
	}
	out := billOutput{b.BillID, b.VendorID, b.ProjectID, b.BillNumber, b.PoNumber, b.Currency, subtotal, b.DiscountType, discount, *amount, *balance, b.Status, b.IssueDate, b.DueDate, b.PaymentTerms, b.Notes, pythonTimestamp(b.CreatedAt), make([]billLineOutput, 0, len(lines))}
	for _, l := range lines {
		q, e := billNumeric(l.Quantity, false)
		if e != nil {
			return billOutput{}, e
		}
		p, e := billNumeric(l.UnitPrice, false)
		if e != nil {
			return billOutput{}, e
		}
		a, e := billNumeric(l.Amount, false)
		if e != nil {
			return billOutput{}, e
		}
		out.LineItems = append(out.LineItems, billLineOutput{l.LineItemID, l.ItemID, l.ExpenseAccountID, l.Description, *q, *p, *a, l.Position})
	}
	return out, nil
}

type billListInput struct {
	VendorID string   `query:"vendor_id" format:"uuid"`
	Status   []string `query:"status"`
}
type billGetInput struct {
	ID string `path:"bill_id" format:"uuid"`
}

func registerBillReads(api huma.API, s sessionHandler) {
	security := []map[string][]string{{"sessionCookie": {}}}
	m := huma.Middlewares{sessionMiddleware(s.billRead)}
	response := func(desc string, body any) *huma.Response {
		return &huma.Response{Description: desc, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(body), true, "")}}}
	}
	responses := func(detail bool) map[string]*huma.Response {
		r := map[string]*huma.Response{"401": response("Not authenticated", authErrorBody{}), "403": response("Admin required", authErrorBody{}), "422": response("Validation error", validationErrorBody{}), "500": response("Internal server error", authErrorBody{})}
		if detail {
			r["404"] = response("Bill not found", authErrorBody{})
		}
		return r
	}
	huma.Register(api, huma.Operation{Method: "GET", Path: "/api/v1/bills", OperationID: "list-bills", Security: security, Middlewares: m, Responses: responses(false)}, func(context.Context, *billListInput) (*struct {
		Body []billSchema `nullable:"false"`
	}, error) {
		panic("middleware")
	})
	huma.Register(api, huma.Operation{Method: "GET", Path: "/api/v1/bills/{bill_id}", OperationID: "get-bill", Security: security, Middlewares: m, Responses: responses(true)}, func(context.Context, *billGetInput) (*struct{ Body billSchema }, error) { panic("middleware") })
}
func (s sessionHandler) billRead(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	q := dbquery.New(s.db)
	raw := chi.URLParam(r, "bill_id")
	var rows []dbquery.Bill
	if raw != "" {
		id, ok := parsePathUUID(raw)
		if !ok {
			d := uuidErrorDetail(raw)
			validationResponse(w, []validationError{{"uuid_parsing", []any{"path", "bill_id"}, "Input should be a valid UUID, " + d, raw, map[string]string{"error": d}}})
			return
		}
		b, e := q.GetBill(r.Context(), id)
		if errors.Is(e, pgx.ErrNoRows) {
			authError(w, 404, "bill not found")
			return
		}
		if e != nil {
			authError(w, 500, "internal server error")
			return
		}
		rows = []dbquery.Bill{b}
	} else {
		values := r.URL.Query()
		var id pgtype.UUID
		if v, ok := values["vendor_id"]; ok {
			raw = v[len(v)-1]
			var valid bool
			id, valid = parsePathUUID(raw)
			if !valid {
				d := uuidErrorDetail(raw)
				validationResponse(w, []validationError{{"uuid_parsing", []any{"query", "vendor_id"}, "Input should be a valid UUID, " + d, raw, map[string]string{"error": d}}})
				return
			}
		}
		statuses, ok := values["status"]
		if !ok {
			statuses = []string{}
		}
		var e error
		rows, e = q.ListBills(r.Context(), dbquery.ListBillsParams{VendorID: id, AllStatus: !ok, Statuses: statuses})
		if e != nil {
			authError(w, 500, "internal server error")
			return
		}
	}
	ids := make([]pgtype.UUID, 0, len(rows))
	for _, b := range rows {
		ids = append(ids, b.BillID)
	}
	lines, e := q.BillLines(r.Context(), ids)
	if e != nil {
		authError(w, 500, "internal server error")
		return
	}
	grouped := map[pgtype.UUID][]dbquery.BillLineItem{}
	for _, l := range lines {
		grouped[l.BillID] = append(grouped[l.BillID], l)
	}
	out := make([]billOutput, 0, len(rows))
	for _, b := range rows {
		v, e := billJSON(b, grouped[b.BillID])
		if e != nil {
			authError(w, 500, "internal server error")
			return
		}
		out = append(out, v)
	}
	w.Header().Set("Content-Type", "application/json")
	if chi.URLParam(r, "bill_id") != "" {
		writeSafeJSON(w, out[0])
	} else {
		writeSafeJSON(w, out)
	}
}
