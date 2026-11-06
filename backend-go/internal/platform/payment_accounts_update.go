package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

type paymentAccountInputSchema struct {
	Currency     string `json:"currency"`
	Instructions string `json:"instructions"`
	IsDefault    bool   `json:"is_default,omitempty"`
}

func registerPaymentAccountsUpdate(api huma.API, s sessionHandler) {
	response := func(desc string, body any) *huma.Response {
		return &huma.Response{Description: desc, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(body), true, "")}}}
	}
	errs := map[string]*huma.Response{"401": response("Not authenticated", authErrorBody{}), "403": response("Admin required", authErrorBody{}), "413": response("Request body too large", authErrorBody{}), "422": response("Validation error", validationErrorBody{}), "500": response("Internal server error", authErrorBody{})}
	huma.Register(api, huma.Operation{Method: "PUT", Path: "/api/v1/business-profile/payment-accounts", OperationID: "replace-payment-accounts", SkipValidateBody: true, Security: []map[string][]string{{"sessionCookie": {}}}, Middlewares: huma.Middlewares{sessionMiddleware(s.paymentAccountsUpdate)}, Responses: errs}, func(context.Context, *struct {
		Body []paymentAccountInputSchema `nullable:"false"`
	}) (*struct {
		Body []paymentAccountSchema `nullable:"false"`
	}, error) {
		panic("middleware")
	})
	schema := api.OpenAPI().Components.Schemas.Map()["PaymentAccountInputSchema"]
	schema.AdditionalProperties = true
	schema.Properties["is_default"].Default = false
}

func (s sessionHandler) paymentAccountsUpdate(w http.ResponseWriter, r *http.Request) {
	body, ok := readBodyWithSyntax(w, r)
	if !ok {
		return
	}
	current, ok := s.requireAdmin(w, r)
	if !ok {
		return
	}
	if len(body) == 0 {
		validationResponse(w, []validationError{{"missing", []any{"body"}, "Field required", nil, nil}})
		return
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	var raw any
	_ = d.Decode(&raw)
	list, ok := raw.([]any)
	if !ok {
		validationResponse(w, []validationError{{"list_type", []any{"body"}, "Input should be a valid list", "[REDACTED]", nil}})
		return
	}
	inputs := make([]paymentAccountInputSchema, 0, len(list))
	var errs []validationError
	for i, v := range list {
		obj, ok := v.(map[string]any)
		if !ok {
			errs = append(errs, validationError{"model_attributes_type", []any{"body", i}, "Input should be a valid dictionary or object to extract fields from", "[REDACTED]", nil})
			continue
		}
		var a paymentAccountInputSchema
		for _, f := range []string{"currency", "instructions"} {
			val, exists := obj[f]
			loc := []any{"body", i, f}
			if !exists {
				errs = append(errs, validationError{"missing", loc, "Field required", nil, nil})
				continue
			}
			x, ok := val.(string)
			if !ok {
				errs = append(errs, validationError{"string_type", loc, "Input should be a valid string", "[REDACTED]", nil})
				continue
			}
			if f == "currency" {
				a.Currency = x
			} else {
				a.Instructions = x
			}
		}
		if v, exists := obj["is_default"]; exists {
			loc := []any{"body", i, "is_default"}
			if e := itemBooleanError(v, loc); e != nil {
				e.Input = "[REDACTED]"
				errs = append(errs, *e)
			} else {
				switch x := v.(type) {
				case bool:
					a.IsDefault = x
				case string:
					switch strings.ToLower(x) {
					case "true", "1", "on", "yes", "y", "t":
						a.IsDefault = true
					}
				case json.Number:
					n, _ := x.Float64()
					a.IsDefault = n == 1
				}
			}
		}
		inputs = append(inputs, a)
	}
	if len(errs) > 0 {
		validationResponse(w, errs)
		return
	}
	seen := map[string]bool{}
	unsupported := map[string]bool{}
	defaults := 0
	for i := range inputs {
		c := cases.Upper(language.Und).String(inputs[i].Currency)
		inputs[i].Currency = c
		if seen[c] {
			authError(w, 422, "Duplicate currency in payment accounts")
			return
		}
		seen[c] = true
		if c != "USD" && c != "SGD" && c != "IDR" {
			unsupported[c] = true
		}
		if inputs[i].IsDefault {
			defaults++
		}
	}
	if len(unsupported) > 0 {
		authError(w, 422, "Unsupported currency")
		return
	}
	if defaults > 1 {
		authError(w, 422, "At most one account can be the default")
		return
	}
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
	if err = q.EnsureBusinessProfile(ctx); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if _, err = q.LockBusinessProfile(ctx); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if err = q.DeleteBusinessPaymentAccounts(ctx); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	for _, a := range inputs {
		if err = q.InsertBusinessPaymentAccount(ctx, dbquery.InsertBusinessPaymentAccountParams{Currency: a.Currency, Instructions: a.Instructions, IsDefault: a.IsDefault}); err != nil {
			authError(w, 500, "internal server error")
			return
		}
	}
	rows, err := q.ListBusinessPaymentAccounts(ctx)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	out := make([]paymentAccountOutput, 0, len(rows))
	for _, p := range rows {
		if !p.UpdatedAt.Valid {
			authError(w, 500, "internal server error")
			return
		}
		out = append(out, paymentAccountOutput{p.ID, p.Currency, p.Instructions, p.IsDefault, pythonTimestamp(p.UpdatedAt)})
	}
	encoded, err := json.Marshal(out)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if err = tx.Commit(ctx); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(append(encoded, '\n'))
}
