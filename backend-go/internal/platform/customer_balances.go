package platform

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
)

type customerBalanceOutput struct {
	CustomerID pgtype.UUID `json:"customer_id"`
	Currency   string      `json:"currency"`
	Balance    string      `json:"balance"`
	Overdue    string      `json:"overdue"`
}
type customerBalanceSchema struct {
	CustomerID string `json:"customer_id" format:"uuid"`
	Currency   string `json:"currency"`
	Balance    string `json:"balance"`
	Overdue    string `json:"overdue"`
}

func (s sessionHandler) customerBalances(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	today := time.Now()
	rows, err := dbquery.New(s.db).CustomerBalances(r.Context(), pgtype.Date{Time: today, Valid: true})
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	out := make([]customerBalanceOutput, 0, len(rows))
	for _, row := range rows {
		if !row.CustomerID.Valid || !row.Balance.Valid || !row.Overdue.Valid || row.Balance.NaN || row.Overdue.NaN || row.Balance.InfinityModifier != pgtype.Finite || row.Overdue.InfinityModifier != pgtype.Finite {
			authError(w, 500, "internal server error")
			return
		}
		balance, overdue := numericString(row.Balance), numericString(row.Overdue)
		if balance == nil || overdue == nil {
			authError(w, 500, "internal server error")
			return
		}
		out = append(out, customerBalanceOutput{row.CustomerID, row.Currency, *balance, *overdue})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}
func registerCustomerBalances(api huma.API, s sessionHandler) {
	response := func(desc string) *huma.Response {
		return &huma.Response{Description: desc, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(authErrorBody{}), true, "")}}}
	}
	huma.Register(api, huma.Operation{Method: "GET", Path: "/api/v1/customers/balances", OperationID: "customer-balances", Responses: map[string]*huma.Response{"401": response("Not authenticated"), "403": response("Admin required"), "500": response("Internal server error")}, Security: []map[string][]string{{"sessionCookie": {}}}, Middlewares: huma.Middlewares{sessionMiddleware(s.customerBalances)}}, func(context.Context, *struct{}) (*struct {
		Body []customerBalanceSchema `nullable:"false"`
	}, error) {
		panic("middleware")
	})
}
