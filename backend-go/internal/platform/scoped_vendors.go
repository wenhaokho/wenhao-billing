package platform

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopeduser"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopedvendor"
	"io"
	"net/http"
)

func legacyVendor(v dbqueryv8.Vendor) dbquery.Vendor {
	return dbquery.Vendor{VendorID: v.VendorID, Name: v.Name, ContactEmail: v.ContactEmail, ContactName: v.ContactName, DefaultCurrency: v.DefaultCurrency, PaymentTermsDays: v.PaymentTermsDays, TaxID: v.TaxID, Notes: v.Notes, Active: v.Active, CreatedAt: v.CreatedAt}
}

func (h scopedProfileRouter) vendorRead(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.admin(w, r); !ok {
		return
	}
	raw := chi.URLParam(r, "vendor_id")
	if raw != "" {
		id, valid := parsePathUUID(raw)
		if !valid {
			d := uuidErrorDetail(raw)
			validationResponse(w, []validationError{{"uuid_parsing", []any{"path", "vendor_id"}, "Input should be a valid UUID, " + d, raw, map[string]string{"error": d}}})
			return
		}
		v, err := scopedvendor.Get(r.Context(), h.pool, h.scope, id)
		if errors.Is(err, pgx.ErrNoRows) {
			authError(w, 404, "vendor not found")
			return
		}
		if err != nil {
			authError(w, 500, "internal server error")
			return
		}
		out, err := vendorJSON(legacyVendor(v))
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
	rows, err := scopedvendor.List(r.Context(), h.pool, h.scope, active)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	out := make([]vendorOutput, 0, len(rows))
	for _, v := range rows {
		item, err := vendorJSON(legacyVendor(v))
		if err != nil {
			authError(w, 500, "internal server error")
			return
		}
		out = append(out, item)
	}
	w.Header().Set("Content-Type", "application/json")
	writeSafeJSON(w, out)
}

func (h scopedProfileRouter) vendorDelete(w http.ResponseWriter, r *http.Request) {
	current, ok := h.admin(w, r)
	if !ok {
		return
	}
	raw := chi.URLParam(r, "vendor_id")
	id, valid := parsePathUUID(raw)
	if !valid {
		d := uuidErrorDetail(raw)
		validationResponse(w, []validationError{{"uuid_parsing", []any{"path", "vendor_id"}, "Input should be a valid UUID, " + d, raw, map[string]string{"error": d}}})
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
	v, err := scopedvendor.Deactivate(ctx, tx, h.scope, id)
	if errors.Is(err, pgx.ErrNoRows) {
		authError(w, 404, "vendor not found")
		return
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	out, err := vendorJSON(legacyVendor(v))
	if err != nil {
		authError(w, 500, "internal server error")
		return
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
	w.Write(append(encoded, '\n'))
}

func (h scopedProfileRouter) vendorCreate(w http.ResponseWriter, r *http.Request) {
	body, ok := readItemBody(w, r)
	if !ok {
		return
	}
	// FastAPI parses JSON only for application/json and application/*+json.
	isJSON := requestJSONMediaType(r)
	if len(body) > 0 && isJSON {
		d := json.NewDecoder(bytes.NewReader(body))
		d.UseNumber()
		var v any
		if err := d.Decode(&v); err != nil {
			validationResponse(w, itemUpdateJSONError(body, err, false))
			return
		}
		if err := d.Decode(&v); err != io.EOF {
			validationResponse(w, itemUpdateJSONError(body, err, true))
			return
		}
	}
	current, ok := h.admin(w, r)
	if !ok {
		return
	}
	if len(body) > 0 && !isJSON {
		// Unparsed content may contain credentials in an arbitrary encoding.
		validationResponse(w, []validationError{{"model_attributes_type", []any{"body"}, "Input should be a valid dictionary or object to extract fields from", "[REDACTED]", nil}})
		return
	}
	p, errs := parseVendorCreate(body)
	if len(errs) > 0 {
		validationResponse(w, errs)
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
	v, err := scopedvendor.Create(ctx, tx, h.scope, scopedvendor.Fields{Name: p.Name, ContactEmail: p.ContactEmail, ContactName: p.ContactName, DefaultCurrency: p.DefaultCurrency, PaymentTermsDays: p.PaymentTermsDays, TaxID: p.TaxID, Notes: p.Notes, Active: p.Active})
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	out, err := vendorJSON(legacyVendor(v))
	if err != nil {
		authError(w, 500, "internal server error")
		return
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
	w.WriteHeader(201)
	w.Write(append(encoded, '\n'))
}

func (h scopedProfileRouter) vendorUpdate(w http.ResponseWriter, r *http.Request) {
	body, ok := readItemBody(w, r)
	if !ok {
		return
	}
	isJSON := requestJSONMediaType(r)
	if len(body) > 0 && isJSON {
		d := json.NewDecoder(bytes.NewReader(body))
		d.UseNumber()
		var v any
		if err := d.Decode(&v); err != nil {
			vendorUpdateValidationResponse(w, itemUpdateJSONError(body, err, false))
			return
		}
		if err := d.Decode(&v); err != io.EOF {
			vendorUpdateValidationResponse(w, itemUpdateJSONError(body, err, true))
			return
		}
	}
	current, ok := h.admin(w, r)
	if !ok {
		return
	}
	var data map[string]any
	var errs []validationError
	if len(body) > 0 && !isJSON {
		errs = []validationError{{"model_attributes_type", []any{"body"}, "Input should be a valid dictionary or object to extract fields from", "[REDACTED]", nil}}
	} else {
		data, errs = parseVendorUpdate(body)
	}
	raw := chi.URLParam(r, "vendor_id")
	id, valid := parsePathUUID(raw)
	if !valid {
		d := uuidErrorDetail(raw)
		errs = append([]validationError{{"uuid_parsing", []any{"path", "vendor_id"}, "Input should be a valid UUID, " + d, raw, map[string]string{"error": d}}}, errs...)
	}
	if len(errs) > 0 {
		vendorUpdateValidationResponse(w, errs)
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
	_, err = scopedvendor.Lock(ctx, tx, h.scope, id)
	if errors.Is(err, pgx.ErrNoRows) {
		authError(w, 404, "vendor not found")
		return
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	b, err := json.Marshal(data)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	v, err := scopedvendor.Update(ctx, tx, h.scope, id, b)
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	out, err := vendorJSON(legacyVendor(v))
	if err != nil {
		authError(w, 500, "internal server error")
		return
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
	w.Write(append(encoded, '\n'))
}
