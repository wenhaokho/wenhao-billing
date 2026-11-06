package platform

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
)

type vendorOutput struct {
	VendorID         pgtype.UUID     `json:"vendor_id"`
	Name             string          `json:"name"`
	ContactEmail     pgtype.Text     `json:"contact_email"`
	ContactName      pgtype.Text     `json:"contact_name"`
	DefaultCurrency  string          `json:"default_currency"`
	PaymentTermsDays int32           `json:"payment_terms_days"`
	TaxID            pgtype.Text     `json:"tax_id"`
	Notes            pgtype.Text     `json:"notes"`
	Active           bool            `json:"active"`
	CreatedAt        pythonTimestamp `json:"created_at"`
}
type vendorOutputSchema struct {
	VendorID         string  `json:"vendor_id" format:"uuid"`
	Name             string  `json:"name" minLength:"1" maxLength:"255"`
	ContactEmail     *string `json:"contact_email" format:"email"`
	ContactName      *string `json:"contact_name"`
	DefaultCurrency  string  `json:"default_currency" minLength:"3" maxLength:"3"`
	PaymentTermsDays int32   `json:"payment_terms_days" minimum:"0" maximum:"365"`
	TaxID            *string `json:"tax_id"`
	Notes            *string `json:"notes"`
	Active           bool    `json:"active"`
	CreatedAt        string  `json:"created_at" format:"date-time"`
}

func vendorJSON(v dbquery.Vendor) (vendorOutput, error) {
	if n := utf8.RuneCountInString(v.Name); n < 1 || n > 255 {
		return vendorOutput{}, errors.New("invalid vendor response")
	}
	if utf8.RuneCountInString(v.DefaultCurrency) != 3 || v.PaymentTermsDays < 0 || v.PaymentTermsDays > 365 {
		return vendorOutput{}, errors.New("invalid vendor response")
	}
	if v.ContactEmail.Valid {
		normalized, reason := normalizeEmail(v.ContactEmail.String)
		if reason != "" {
			return vendorOutput{}, errors.New("invalid vendor response")
		}
		v.ContactEmail.String = normalized
	}
	return vendorOutput{v.VendorID, v.Name, v.ContactEmail, v.ContactName, v.DefaultCurrency, v.PaymentTermsDays, v.TaxID, v.Notes, v.Active, pythonTimestamp(v.CreatedAt)}, nil
}
func (s sessionHandler) vendorRead(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	q := dbquery.New(s.db)
	raw := chi.URLParam(r, "vendor_id")
	if raw != "" {
		id, valid := parsePathUUID(raw)
		if !valid {
			d := uuidErrorDetail(raw)
			validationResponse(w, []validationError{{"uuid_parsing", []any{"path", "vendor_id"}, "Input should be a valid UUID, " + d, raw, map[string]string{"error": d}}})
			return
		}
		v, err := q.GetVendor(r.Context(), id)
		if errors.Is(err, pgx.ErrNoRows) {
			authError(w, 404, "vendor not found")
			return
		}
		if err != nil {
			authError(w, 500, "internal server error")
			return
		}
		out, err := vendorJSON(v)
		if err != nil {
			authError(w, 500, "internal server error")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		writeSafeJSON(w, out)
		return
	}
	active, ok := customerBoolFilter(w, r)
	if !ok {
		return
	}
	rows, err := q.ListVendors(r.Context(), active)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	out := make([]vendorOutput, 0, len(rows))
	for _, v := range rows {
		item, err := vendorJSON(v)
		if err != nil {
			authError(w, 500, "internal server error")
			return
		}
		out = append(out, item)
	}
	w.Header().Set("Content-Type", "application/json")
	writeSafeJSON(w, out)
}

type vendorListInput struct {
	Active bool `query:"active"`
}
type vendorGetInput struct {
	ID string `path:"vendor_id" format:"uuid"`
}

func registerVendorReads(api huma.API, s sessionHandler) {
	security := []map[string][]string{{"sessionCookie": {}}}
	m := huma.Middlewares{sessionMiddleware(s.vendorRead)}
	response := func(desc string, body any) *huma.Response {
		return &huma.Response{Description: desc, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(body), true, "")}}}
	}
	responses := func(detail bool) map[string]*huma.Response {
		r := map[string]*huma.Response{"401": response("Not authenticated", authErrorBody{}), "403": response("Admin required", authErrorBody{}), "422": response("Validation error", validationErrorBody{}), "500": response("Internal server error", authErrorBody{})}
		if detail {
			r["404"] = response("Vendor not found", authErrorBody{})
		}
		return r
	}
	huma.Register(api, huma.Operation{Method: "GET", Path: "/api/v1/vendors", OperationID: "list-vendors", Security: security, Middlewares: m, Responses: responses(false)}, func(context.Context, *vendorListInput) (*struct {
		Body []vendorOutputSchema `nullable:"false"`
	}, error) {
		panic("middleware")
	})
	huma.Register(api, huma.Operation{Method: "GET", Path: "/api/v1/vendors/{vendor_id}", OperationID: "get-vendor", Security: security, Middlewares: m, Responses: responses(true)}, func(context.Context, *vendorGetInput) (*struct{ Body vendorOutputSchema }, error) {
		panic("middleware")
	})
}
