package platform

import (
	"context"
	"errors"
	"net/http"
	"reflect"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
)

type businessProfileOutput struct {
	Name           pgtype.Text     `json:"name"`
	Address        pgtype.Text     `json:"address"`
	ContactEmail   pgtype.Text     `json:"contact_email"`
	ContactPhone   pgtype.Text     `json:"contact_phone"`
	InvoiceTitle   pgtype.Text     `json:"invoice_title"`
	InvoiceSummary pgtype.Text     `json:"invoice_summary"`
	LogoURL        pgtype.Text     `json:"logo_url"`
	DefaultNotes   pgtype.Text     `json:"default_notes"`
	NPWP           pgtype.Text     `json:"npwp"`
	NPWPName       pgtype.Text     `json:"npwp_name"`
	UpdatedAt      pythonTimestamp `json:"updated_at"`
}
type businessProfileSchema struct {
	Name           *string `json:"name"`
	Address        *string `json:"address"`
	ContactEmail   *string `json:"contact_email"`
	ContactPhone   *string `json:"contact_phone"`
	InvoiceTitle   *string `json:"invoice_title"`
	InvoiceSummary *string `json:"invoice_summary"`
	LogoURL        *string `json:"logo_url"`
	DefaultNotes   *string `json:"default_notes"`
	NPWP           *string `json:"npwp"`
	NPWPName       *string `json:"npwp_name"`
	UpdatedAt      string  `json:"updated_at" format:"date-time"`
}
type paymentAccountOutput struct {
	ID           int32           `json:"id"`
	Currency     string          `json:"currency"`
	Instructions string          `json:"instructions"`
	IsDefault    bool            `json:"is_default"`
	UpdatedAt    pythonTimestamp `json:"updated_at"`
}
type paymentAccountSchema struct {
	ID           int32  `json:"id"`
	Currency     string `json:"currency"`
	Instructions string `json:"instructions"`
	IsDefault    bool   `json:"is_default"`
	UpdatedAt    string `json:"updated_at" format:"date-time"`
}

func registerProfileReads(api huma.API, s sessionHandler) {
	security := []map[string][]string{{"sessionCookie": {}}}
	response := func(desc string) *huma.Response {
		return &huma.Response{Description: desc, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(authErrorBody{}), true, "")}}}
	}
	errs := map[string]*huma.Response{"401": response("Not authenticated"), "403": response("Admin required"), "500": response("Internal server error")}
	huma.Register(api, huma.Operation{Method: "GET", Path: "/api/v1/business-profile", OperationID: "get-business-profile", Security: security, Middlewares: huma.Middlewares{sessionMiddleware(s.profileRead)}, Responses: errs}, func(context.Context, *struct{}) (*struct{ Body businessProfileSchema }, error) { panic("middleware") })
	huma.Register(api, huma.Operation{Method: "GET", Path: "/api/v1/business-profile/payment-accounts", OperationID: "list-payment-accounts", Security: security, Middlewares: huma.Middlewares{sessionMiddleware(s.profileAccountsRead)}, Responses: errs}, func(context.Context, *struct{}) (*struct {
		Body []paymentAccountSchema `nullable:"false"`
	}, error) {
		panic("middleware")
	})
}
func (s sessionHandler) profileRead(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	q := dbquery.New(s.db)
	p, e := q.GetBusinessProfile(r.Context())
	if errors.Is(e, pgx.ErrNoRows) {
		p.UpdatedAt, e = q.GetBusinessProfileDefaultTime(r.Context())
	}
	if e != nil || !p.UpdatedAt.Valid {
		authError(w, 500, "internal server error")
		return
	}
	out := businessProfileOutput{p.Name, p.Address, p.ContactEmail, p.ContactPhone, p.InvoiceTitle, p.InvoiceSummary, p.LogoUrl, p.DefaultNotes, p.Npwp, p.NpwpName, pythonTimestamp(p.UpdatedAt)}
	w.Header().Set("Content-Type", "application/json")
	writeSafeJSON(w, out)
}
func (s sessionHandler) profileAccountsRead(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	rows, e := dbquery.New(s.db).ListBusinessPaymentAccounts(r.Context())
	if e != nil {
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
	w.Header().Set("Content-Type", "application/json")
	writeSafeJSON(w, out)
}
