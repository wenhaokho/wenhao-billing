package platform

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedproject"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopeduser"
	"io"
	"net/http"
	"strings"
)

func legacyProject(v dbqueryv8.Project) dbquery.Project {
	return dbquery.Project{ProjectID: v.ProjectID, CustomerID: v.CustomerID, Code: v.Code, Name: v.Name, Currency: v.Currency, ContractValue: v.ContractValue, Status: v.Status, StartDate: v.StartDate, EndDate: v.EndDate, Notes: v.Notes, Active: v.Active, CreatedAt: v.CreatedAt}
}
func (h scopedProfileRouter) projectRead(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.admin(w, r); !ok {
		return
	}
	raw := chi.URLParam(r, "project_id")
	var rows []dbqueryv8.Project
	if raw != "" {
		id, ok := parsePathUUID(raw)
		if !ok {
			d := uuidErrorDetail(raw)
			validationResponse(w, []validationError{{"uuid_parsing", []any{"path", "project_id"}, "Input should be a valid UUID, " + d, raw, map[string]string{"error": d}}})
			return
		}
		p, e := scopedproject.GetProject(r.Context(), h.pool, h.scope, dbqueryv8.GetProjectParams{ProjectID: id})
		if errors.Is(e, pgx.ErrNoRows) {
			authError(w, 404, "project not found")
			return
		}
		if e != nil {
			authError(w, 500, "internal server error")
			return
		}
		rows = []dbqueryv8.Project{p}
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
		rows, err = scopedproject.ListProjects(r.Context(), h.pool, h.scope, dbqueryv8.ListProjectsParams{CustomerID: id, Active: active, AllStatus: !ok, Statuses: statuses})
		if err != nil {
			authError(w, 500, "internal server error")
			return
		}
	}
	out := make([]projectOutput, 0, len(rows))
	for _, p := range rows {
		v, e := projectJSON(legacyProject(p))
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

func (h scopedProfileRouter) projectCreate(w http.ResponseWriter, r *http.Request) {
	body, ok := readItemBody(w, r)
	if !ok {
		return
	}
	isJSON := requestJSONMediaType(r)
	if len(body) > 0 && isJSON {
		d := json.NewDecoder(bytes.NewReader(body))
		d.UseNumber()
		var v any
		if e := d.Decode(&v); e != nil {
			vendorUpdateValidationResponse(w, itemUpdateJSONError(body, e, false))
			return
		}
		if e := d.Decode(&v); e != io.EOF {
			vendorUpdateValidationResponse(w, itemUpdateJSONError(body, e, true))
			return
		}
	}
	current, ok := h.admin(w, r)
	if !ok {
		return
	}
	if len(body) > 0 && !isJSON {
		vendorUpdateValidationResponse(w, []validationError{{"model_attributes_type", []any{"body"}, "Input should be a valid dictionary or object to extract fields from", nil, nil}})
		return
	}
	p, errs := parseProjectCreate(body)
	if len(errs) > 0 {
		vendorUpdateValidationResponse(w, errs)
		return
	}
	ctx := r.Context()
	tx, e := h.pool.Begin(ctx)
	if e != nil {
		authError(w, 500, "internal server error")
		return
	}
	defer tx.Rollback(ctx)
	user, e := scopeduser.LockSessionUserByID(ctx, tx, h.scope, dbqueryv8.LockSessionUserByIDParams{UserID: current.UserID})
	if errors.Is(e, pgx.ErrNoRows) || e == nil && user.Role != "admin" {
		authError(w, 403, "admin required")
		return
	}
	if e != nil {
		authError(w, 500, "internal server error")
		return
	}
	row, e := scopedproject.CreateProject(ctx, tx, h.scope, dbqueryv8.CreateProjectParams{CustomerID: p.CustomerID, Code: p.Code, Name: p.Name, Currency: p.Currency, ContractValue: p.ContractValue, Status: p.Status, StartDate: p.StartDate, EndDate: p.EndDate, Notes: p.Notes})
	if errors.Is(e, pgx.ErrNoRows) {
		authError(w, 400, "customer not found")
		return
	}
	if e != nil {
		var pe *pgconn.PgError
		if errors.As(e, &pe) && strings.HasPrefix(pe.Code, "23") {
			authError(w, 400, fmt.Sprintf("project code %s already exists", pythonRepr(p.Code)))
		} else {
			authError(w, 500, "internal server error")
		}
		return
	}
	out, e := projectJSON(legacyProject(row))
	if e != nil {
		authError(w, 500, "internal server error")
		return
	}
	encoded, e := json.Marshal(out)
	if e != nil {
		authError(w, 500, "internal server error")
		return
	}
	if e = tx.Commit(ctx); e != nil {
		authError(w, 500, "internal server error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(201)
	w.Write(append(encoded, '\n'))
}

func (h scopedProfileRouter) projectUpdate(w http.ResponseWriter, r *http.Request) {
	body, ok := readItemBody(w, r)
	if !ok {
		return
	}
	isJSON := requestJSONMediaType(r)
	if len(body) > 0 && isJSON {
		d := json.NewDecoder(bytes.NewReader(body))
		d.UseNumber()
		var v any
		if e := d.Decode(&v); e != nil {
			vendorUpdateValidationResponse(w, itemUpdateJSONError(body, e, false))
			return
		}
		if e := d.Decode(&v); e != io.EOF {
			vendorUpdateValidationResponse(w, itemUpdateJSONError(body, e, true))
			return
		}
	}
	current, ok := h.admin(w, r)
	if !ok {
		return
	}
	var data map[string]any
	var p dbquery.CreateProjectParams
	var errs []validationError
	if len(body) > 0 && !isJSON {
		errs = []validationError{{"model_attributes_type", []any{"body"}, "Input should be a valid dictionary or object to extract fields from", nil, nil}}
	} else {
		data, p, errs = parseProjectUpdate(body)
	}
	raw := chi.URLParam(r, "project_id")
	id, valid := parsePathUUID(raw)
	if !valid {
		detail := uuidErrorDetail(raw)
		errs = append([]validationError{{"uuid_parsing", []any{"path", "project_id"}, "Input should be a valid UUID, " + detail, raw, map[string]string{"error": detail}}}, errs...)
	}
	if len(errs) > 0 {
		vendorUpdateValidationResponse(w, errs)
		return
	}
	ctx := r.Context()
	tx, e := h.pool.Begin(ctx)
	if e != nil {
		authError(w, 500, "internal server error")
		return
	}
	defer tx.Rollback(ctx)
	user, e := scopeduser.LockSessionUserByID(ctx, tx, h.scope, dbqueryv8.LockSessionUserByIDParams{UserID: current.UserID})
	if errors.Is(e, pgx.ErrNoRows) || e == nil && user.Role != "admin" {
		authError(w, 403, "admin required")
		return
	}
	if e != nil {
		authError(w, 500, "internal server error")
		return
	}
	row, e := scopedproject.LockProject(ctx, tx, h.scope, dbqueryv8.LockProjectParams{ProjectID: id})
	if errors.Is(e, pgx.ErrNoRows) {
		authError(w, 404, "project not found")
		return
	}
	if e != nil {
		authError(w, 500, "internal server error")
		return
	}
	for _, k := range []string{"customer_id", "code", "name", "currency", "status", "active"} {
		if v, exists := data[k]; exists && v == nil {
			authError(w, 400, "project code already exists")
			return
		}
	}
	u := dbquery.UpdateProjectParams{ProjectID: id, CustomerID: row.CustomerID, Code: row.Code, Name: row.Name, Currency: row.Currency, ContractValue: row.ContractValue, Status: row.Status, StartDate: row.StartDate, EndDate: row.EndDate, Notes: row.Notes, Active: row.Active}
	if _, ok := data["customer_id"]; ok {
		u.CustomerID = p.CustomerID
	}
	if _, ok := data["code"]; ok {
		u.Code = p.Code
	}
	if v, ok := data["name"]; ok {
		u.Name = v.(string)
	}
	if _, ok := data["currency"]; ok {
		u.Currency = p.Currency
	}
	if _, ok := data["status"]; ok {
		u.Status = p.Status
	}
	if _, ok := data["contract_value"]; ok {
		u.ContractValue = p.ContractValue
	}
	if _, ok := data["start_date"]; ok {
		u.StartDate = p.StartDate
	}
	if _, ok := data["end_date"]; ok {
		u.EndDate = p.EndDate
	}
	if _, ok := data["notes"]; ok {
		u.Notes = p.Notes
	}
	if a, ok := data["active"]; ok {
		switch v := a.(type) {
		case bool:
			u.Active = v
		case string:
			switch strings.ToLower(v) {
			case "true", "1", "on", "yes", "y", "t":
				u.Active = true
			default:
				u.Active = false
			}
		case json.Number:
			n, _ := boundedItemRat(string(v))
			u.Active = n != nil && n.Sign() != 0
		}
	}
	updated, e := scopedproject.UpdateProject(ctx, tx, h.scope, dbqueryv8.UpdateProjectParams{ProjectID: u.ProjectID, CustomerID: u.CustomerID, Code: u.Code, Name: u.Name, Currency: u.Currency, ContractValue: u.ContractValue, Status: u.Status, StartDate: u.StartDate, EndDate: u.EndDate, Notes: u.Notes, Active: u.Active})
	if errors.Is(e, pgx.ErrNoRows) {
		authError(w, 400, "customer not found")
		return
	}
	if e != nil {
		var pe *pgconn.PgError
		if errors.As(e, &pe) && strings.HasPrefix(pe.Code, "23") {
			authError(w, 400, "project code already exists")
		} else {
			authError(w, 500, "internal server error")
		}
		return
	}
	out, e := projectJSON(legacyProject(updated))
	if e != nil {
		authError(w, 500, "internal server error")
		return
	}
	encoded, e := json.Marshal(out)
	if e != nil {
		authError(w, 500, "internal server error")
		return
	}
	if e = tx.Commit(ctx); e != nil {
		authError(w, 500, "internal server error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(200)
	w.Write(append(encoded, '\n'))
}

func (h scopedProfileRouter) projectDelete(w http.ResponseWriter, r *http.Request) {
	current, ok := h.admin(w, r)
	if !ok {
		return
	}
	raw := chi.URLParam(r, "project_id")
	id, valid := parsePathUUID(raw)
	if !valid {
		d := uuidErrorDetail(raw)
		validationResponse(w, []validationError{{"uuid_parsing", []any{"path", "project_id"}, "Input should be a valid UUID, " + d, raw, map[string]string{"error": d}}})
		return
	}
	ctx := r.Context()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	defer tx.Rollback(ctx)
	user, err := scopeduser.LockSessionUserByID(ctx, tx, h.scope, dbqueryv8.LockSessionUserByIDParams{UserID: current.UserID})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && user.Role != "admin" {
		authError(w, 403, "admin required")
		return
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	_, err = scopedproject.LockProject(ctx, tx, h.scope, dbqueryv8.LockProjectParams{ProjectID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		authError(w, 404, "project not found")
		return
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	count, err := scopedproject.CountProjectReferences(ctx, tx, h.scope, dbqueryv8.CountProjectReferencesParams{ProjectID: id})
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if count > 0 {
		authError(w, 400, fmt.Sprintf("cannot delete: project is referenced by %d invoice(s)/bill(s)/quotation(s)", count))
		return
	}
	if err = scopedproject.DeleteProject(ctx, tx, h.scope, dbqueryv8.DeleteProjectParams{ProjectID: id}); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if err = tx.Commit(ctx); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
