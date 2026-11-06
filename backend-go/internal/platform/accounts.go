package platform

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
)

type accountOutput struct {
	Code      string `json:"code" minLength:"1" maxLength:"10"`
	Name      string `json:"name" minLength:"1" maxLength:"100"`
	Type      string `json:"type" enum:"ASSET,LIABILITY,EQUITY,INCOME,EXPENSE,COGS"`
	Active    bool   `json:"active"`
	AccountID int32  `json:"account_id"`
}

func accountJSON(a dbquery.ChartOfAccount) accountOutput {
	return accountOutput{a.Code, a.Name, a.Type, a.Active, a.AccountID}
}

type accountListInput struct {
	Active bool `query:"active"`
}
type accountGetInput struct {
	ID int `path:"account_id"`
}

func registerAccountReads(api huma.API, s sessionHandler) {
	security := []map[string][]string{{"sessionCookie": {}}}
	m := huma.Middlewares{sessionMiddleware(s.accountRead)}
	response := func(desc string, body any) *huma.Response {
		return &huma.Response{Description: desc, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(body), true, "")}}}
	}
	responses := func(detail bool) map[string]*huma.Response {
		result := map[string]*huma.Response{"401": response("Not authenticated", authErrorBody{}), "403": response("Admin required", authErrorBody{}), "422": response("Validation error", validationErrorBody{}), "500": response("Internal server error", authErrorBody{})}
		if detail {
			result["404"] = response("Account not found", authErrorBody{})
			result["500"].Content["text/plain"] = &huma.MediaType{Schema: &huma.Schema{Type: "string"}}
		}
		return result
	}
	huma.Register(api, huma.Operation{Method: "GET", Path: "/api/v1/accounting/chart-of-accounts", OperationID: "list-accounts", Security: security, Middlewares: m, Responses: responses(false)}, func(context.Context, *accountListInput) (*struct{ Body []accountOutput }, error) { panic("middleware") })
	huma.Register(api, huma.Operation{Method: "GET", Path: "/api/v1/accounting/chart-of-accounts/{account_id}", OperationID: "get-account", Security: security, Middlewares: m, Responses: responses(true)}, func(context.Context, *accountGetInput) (*struct{ Body accountOutput }, error) { panic("middleware") })
}

var accountIntegerPattern = regexp.MustCompile(`^[+-]?[0-9]+(?:_[0-9]+)*(?:\.0+)?$`)

func (s sessionHandler) accountRead(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	q := dbquery.New(s.db)
	raw := chi.URLParam(r, "account_id")
	if raw != "" {
		normalized := strings.TrimSpace(raw)
		if !accountIntegerPattern.MatchString(normalized) {
			validationResponse(w, []validationError{{"int_parsing", []any{"path", "account_id"}, "Input should be a valid integer, unable to parse string as an integer", raw, nil}})
			return
		}
		normalized = strings.ReplaceAll(normalized, "_", "")
		if dot := strings.IndexByte(normalized, '.'); dot >= 0 {
			normalized = normalized[:dot]
		}
		id, err := strconv.ParseInt(normalized, 10, 64)
		if errors.Is(err, strconv.ErrRange) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(500)
			w.Write([]byte("Internal Server Error"))
			return
		}
		if err != nil {
			validationResponse(w, []validationError{{"int_parsing", []any{"path", "account_id"}, "Input should be a valid integer, unable to parse string as an integer", raw, nil}})
			return
		}
		if id < -2147483648 || id > 2147483647 {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(500)
			w.Write([]byte("Internal Server Error"))
			return
		}
		a, err := q.GetAccount(r.Context(), int32(id))
		if errors.Is(err, pgx.ErrNoRows) {
			authError(w, 404, "account not found")
			return
		}
		if err != nil {
			authError(w, 500, "internal server error")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(accountJSON(a))
		return
	}
	active, ok := accountBoolFilter(w, r)
	if !ok {
		return
	}
	rows, err := q.ListAccounts(r.Context(), active)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	out := make([]accountOutput, 0, len(rows))
	for _, a := range rows {
		out = append(out, accountJSON(a))
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func accountBoolFilter(w http.ResponseWriter, r *http.Request) (pgtype.Bool, bool) {
	values, exists := r.URL.Query()["active"]
	if !exists {
		return pgtype.Bool{}, true
	}
	raw := values[len(values)-1]
	switch strings.ToLower(raw) {
	case "1", "true", "on", "yes", "y", "t":
		return pgtype.Bool{Bool: true, Valid: true}, true
	case "0", "false", "off", "no", "n", "f":
		return pgtype.Bool{Bool: false, Valid: true}, true
	}
	validationResponse(w, []validationError{{"bool_parsing", []any{"query", "active"}, "Input should be a valid boolean, unable to interpret input", raw, nil}})
	return pgtype.Bool{}, false
}
