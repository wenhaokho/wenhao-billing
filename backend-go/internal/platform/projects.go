package platform

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
)

type projectOutput struct {
	ProjectID     pgtype.UUID     `json:"project_id"`
	CustomerID    pgtype.UUID     `json:"customer_id"`
	Code          string          `json:"code"`
	Name          string          `json:"name"`
	Currency      string          `json:"currency"`
	ContractValue *string         `json:"contract_value"`
	Status        string          `json:"status"`
	StartDate     pgtype.Date     `json:"start_date"`
	EndDate       pgtype.Date     `json:"end_date"`
	Notes         pgtype.Text     `json:"notes"`
	Active        bool            `json:"active"`
	CreatedAt     pythonTimestamp `json:"created_at"`
}
type projectOutputSchema struct {
	ProjectID     string  `json:"project_id" format:"uuid"`
	CustomerID    string  `json:"customer_id" format:"uuid"`
	Code          string  `json:"code"`
	Name          string  `json:"name"`
	Currency      string  `json:"currency"`
	ContractValue *string `json:"contract_value"`
	Status        string  `json:"status"`
	StartDate     *string `json:"start_date" format:"date"`
	EndDate       *string `json:"end_date" format:"date"`
	Notes         *string `json:"notes"`
	Active        bool    `json:"active"`
	CreatedAt     string  `json:"created_at" format:"date-time"`
}

func projectJSON(p dbquery.Project) (projectOutput, error) {
	if p.ContractValue.Valid && (p.ContractValue.NaN || p.ContractValue.InfinityModifier != pgtype.Finite || p.ContractValue.Int == nil) {
		return projectOutput{}, errors.New("invalid contract value")
	}
	var contract *string
	if p.ContractValue.Valid {
		v := decimal.NewFromBigInt(p.ContractValue.Int, p.ContractValue.Exp).StringFixed(4)
		contract = &v
	}
	return projectOutput{p.ProjectID, p.CustomerID, p.Code, p.Name, p.Currency, contract, p.Status, p.StartDate, p.EndDate, p.Notes, p.Active, pythonTimestamp(p.CreatedAt)}, nil
}

type projectListInput struct {
	CustomerID string   `query:"customer_id" format:"uuid"`
	Status     []string `query:"status"`
	Active     bool     `query:"active"`
}
type projectGetInput struct {
	ID string `path:"project_id" format:"uuid"`
}

func registerProjectReads(api huma.API, s sessionHandler) {
	security := []map[string][]string{{"sessionCookie": {}}}
	m := huma.Middlewares{sessionMiddleware(s.projectRead)}
	response := func(desc string, body any) *huma.Response {
		return &huma.Response{Description: desc, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(body), true, "")}}}
	}
	responses := func(detail bool) map[string]*huma.Response {
		r := map[string]*huma.Response{"401": response("Not authenticated", authErrorBody{}), "403": response("Admin required", authErrorBody{}), "422": response("Validation error", validationErrorBody{}), "500": response("Internal server error", authErrorBody{})}
		if detail {
			r["404"] = response("Project not found", authErrorBody{})
		}
		return r
	}
	huma.Register(api, huma.Operation{Method: "GET", Path: "/api/v1/projects", OperationID: "list-projects", Security: security, Middlewares: m, Responses: responses(false)}, func(context.Context, *projectListInput) (*struct {
		Body []projectOutputSchema `nullable:"false"`
	}, error) {
		panic("middleware")
	})
	huma.Register(api, huma.Operation{Method: "GET", Path: "/api/v1/projects/{project_id}", OperationID: "get-project", Security: security, Middlewares: m, Responses: responses(true)}, func(context.Context, *projectGetInput) (*struct{ Body projectOutputSchema }, error) {
		panic("middleware")
	})
}
func (s sessionHandler) projectRead(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	q := dbquery.New(s.db)
	raw := chi.URLParam(r, "project_id")
	var rows []dbquery.Project
	if raw != "" {
		id, ok := parsePathUUID(raw)
		if !ok {
			d := uuidErrorDetail(raw)
			validationResponse(w, []validationError{{"uuid_parsing", []any{"path", "project_id"}, "Input should be a valid UUID, " + d, raw, map[string]string{"error": d}}})
			return
		}
		p, e := q.GetProject(r.Context(), id)
		if errors.Is(e, pgx.ErrNoRows) {
			authError(w, 404, "project not found")
			return
		}
		if e != nil {
			authError(w, 500, "internal server error")
			return
		}
		rows = []dbquery.Project{p}
	} else {
		var id pgtype.UUID
		var errs []validationError
		values := r.URL.Query()
		if v, ok := values["customer_id"]; ok {
			raw = v[len(v)-1]
			var valid bool
			id, valid = parsePathUUID(raw)
			if !valid {
				d := uuidErrorDetail(raw)
				errs = append(errs, validationError{"uuid_parsing", []any{"query", "customer_id"}, "Input should be a valid UUID, " + d, raw, map[string]string{"error": d}})
			}
		}
		var active pgtype.Bool
		if v, ok := values["active"]; ok {
			raw = v[len(v)-1]
			switch strings.ToLower(raw) {
			case "1", "true", "on", "yes", "y", "t":
				active = pgtype.Bool{Bool: true, Valid: true}
			case "0", "false", "off", "no", "n", "f":
				active = pgtype.Bool{Bool: false, Valid: true}
			default:
				errs = append(errs, validationError{"bool_parsing", []any{"query", "active"}, "Input should be a valid boolean, unable to interpret input", raw, nil})
			}
		}
		if len(errs) > 0 {
			validationResponse(w, errs)
			return
		}
		statuses, ok := values["status"]
		if !ok {
			statuses = []string{}
		}
		var err error
		rows, err = q.ListProjects(r.Context(), dbquery.ListProjectsParams{CustomerID: id, Active: active, AllStatus: !ok, Statuses: statuses})
		if err != nil {
			authError(w, 500, "internal server error")
			return
		}
	}
	out := make([]projectOutput, 0, len(rows))
	for _, p := range rows {
		v, e := projectJSON(p)
		if e != nil {
			authError(w, 500, "internal server error")
			return
		}
		out = append(out, v)
	}
	w.Header().Set("Content-Type", "application/json")
	if chi.URLParam(r, "project_id") != "" {
		writeSafeJSON(w, out[0])
	} else {
		writeSafeJSON(w, out)
	}
}
