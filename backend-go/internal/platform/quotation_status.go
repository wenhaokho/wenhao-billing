package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type quotationAcceptSchema struct {
	AcceptedBy *string `json:"accepted_by,omitempty"`
}

func registerQuotationStatus(api huma.API, s sessionHandler) {
	response := func(desc string, body any) *huma.Response {
		return &huma.Response{Description: desc, Content: map[string]*huma.MediaType{"application/json": {Schema: api.OpenAPI().Components.Schemas.Schema(reflect.TypeOf(body), true, "")}}}
	}
	for _, action := range []string{"mark-sent", "accept", "decline", "void"} {
		responses := map[string]*huma.Response{"400": response("Invalid quotation transition", authErrorBody{}), "401": response("Not authenticated", authErrorBody{}), "403": response("Admin required", authErrorBody{}), "422": response("Validation error", validationErrorBody{}), "500": response("Internal server error", authErrorBody{})}
		op := huma.Operation{Method: "POST", Path: "/api/v1/quotations/{quotation_id}/" + action, OperationID: action + "-quotation", Security: []map[string][]string{{"sessionCookie": {}}}, Middlewares: huma.Middlewares{sessionMiddleware(s.quotationWriter().status(action))}, Responses: responses, SkipValidateBody: true}
		if action == "accept" {
			responses["413"] = response("Body too large", authErrorBody{})
			responses["500"].Content["text/plain"] = &huma.MediaType{Schema: &huma.Schema{Type: "string"}}
			huma.Register(api, op, func(context.Context, *struct {
				ID   string `path:"quotation_id" format:"uuid"`
				Body quotationAcceptSchema
			}) (*struct{ Body quotationSchema }, error) {
				panic("middleware")
			})
		} else {
			huma.Register(api, op, func(context.Context, *struct {
				ID string `path:"quotation_id" format:"uuid"`
			}) (*struct{ Body quotationSchema }, error) {
				panic("middleware")
			})
		}
	}
	if schema := api.OpenAPI().Components.Schemas.Map()["QuotationAcceptSchema"]; schema != nil {
		schema.AdditionalProperties = true
	}
}

func (s quotationWriter) status(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body []byte
		var ok bool
		if action == "accept" {
			body, ok = readItemBody(w, r)
			if !ok {
				return
			}
			if !utf8.Valid(body) {
				authError(w, 400, "There was an error parsing the body")
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			body, ok = readBodyWithSyntax(w, r)
			if !ok {
				return
			}
		}
		current, ok := s.admin(w, r)
		if !ok {
			return
		}
		raw := chi.URLParam(r, "quotation_id")
		id, validID := parsePathUUID(raw)
		var pathErrors []validationError
		if !validID {
			d := uuidErrorDetail(raw)
			pathErrors = []validationError{{"uuid_parsing", []any{"path", "quotation_id"}, "Input should be a valid UUID, " + d, raw, map[string]string{"error": d}}}
		}
		respondValidation := func(errs []validationError) { validationResponse(w, append(pathErrors, errs...)) }
		if action != "accept" && !validID {
			respondValidation(nil)
			return
		}
		var acceptedBy *string
		invalidAcceptedBy := false
		if action == "accept" {
			if len(body) == 0 {
				respondValidation([]validationError{{"missing", []any{"body"}, "Field required", nil, nil}})
				return
			}
			if !requestJSONMediaType(r) {
				respondValidation([]validationError{{"model_attributes_type", []any{"body"}, "Input should be a valid dictionary or object to extract fields from", "[REDACTED]", nil}})
				return
			}
			dec := json.NewDecoder(bytes.NewReader(body))
			dec.UseNumber()
			var value any
			if err := dec.Decode(&value); err != nil {
				respondValidation(itemUpdateJSONError(body, err, true))
				return
			}
			var extra any
			if err := dec.Decode(&extra); err != io.EOF {
				respondValidation(itemUpdateJSONError(body, err, true))
				return
			}
			fields, valid := value.(map[string]any)
			if !valid {
				respondValidation([]validationError{{"model_attributes_type", []any{"body"}, "Input should be a valid dictionary or object to extract fields from", value, nil}})
				return
			}
			if v := fields["accepted_by"]; v != nil {
				str, valid := v.(string)
				if !valid {
					respondValidation([]validationError{{"string_type", []any{"body", "accepted_by"}, "Input should be a valid string", v, nil}})
					return
				}
				var rawFields map[string]json.RawMessage
				_ = json.Unmarshal(body, &rawFields)
				invalidAcceptedBy = strings.IndexByte(str, 0) >= 0 || quotationUnpairedSurrogate(rawFields["accepted_by"])
				acceptedBy = &str
			}
		}
		if !validID {
			respondValidation(nil)
			return
		}
		ctx := r.Context()
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			authError(w, 500, "internal server error")
			return
		}
		defer tx.Rollback(ctx)
		q := s.queries(tx)
		role, err := q.lockAdminRole(ctx, current)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && role != "admin" {
			authError(w, 403, "admin required")
			return
		}
		if err != nil {
			authError(w, 500, "internal server error")
			return
		}
		quotation, err := q.LockQuotation(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			s.quotationNotFound(w, id)
			return
		}
		if err != nil {
			authError(w, 500, "internal server error")
			return
		}
		if action == "void" && quotation.Status == "INVOICED" {
			authError(w, 400, "cannot void an already-invoiced quotation")
			return
		}
		if action != "void" && quotation.Status != "DRAFT" && quotation.Status != "SENT" {
			authError(w, 400, fmt.Sprintf("cannot %s from status %s", action, quotation.Status))
			return
		}
		if invalidAcceptedBy {
			quotationAcceptStorageError(w)
			return
		}
		err = q.changeQuotationStatus(ctx, id, action, acceptedBy)
		if err != nil {
			// Let PostgreSQL enforce varchar length in characters, including
			// its rule that permits truncation of excess trailing spaces.
			var pgErr *pgconn.PgError
			if action == "accept" && acceptedBy != nil && errors.As(err, &pgErr) && pgErr.Code == "22001" {
				quotationAcceptStorageError(w)
				return
			}
			authError(w, 500, "internal server error")
			return
		}
		quotation, err = q.GetQuotation(ctx, id)
		if err != nil {
			authError(w, 500, "internal server error")
			return
		}
		lines, err := q.QuotationLines(ctx, []pgtype.UUID{id})
		if err != nil {
			authError(w, 500, "internal server error")
			return
		}
		out, err := quotationJSON(quotation, lines)
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
		_, _ = w.Write(append(encoded, '\n'))
	}
}

func quotationAcceptStorageError(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = w.Write([]byte("Internal Server Error"))
}

// Python preserves lone UTF-16 surrogates when decoding JSON, but PostgreSQL's
// UTF-8 driver rejects them. Detect them before Go replaces them with U+FFFD.
func quotationUnpairedSurrogate(raw []byte) bool {
	for i := 1; i+1 < len(raw); i++ {
		if raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) {
			break
		}
		if raw[i] != 'u' || i+4 >= len(raw) {
			continue
		}
		n, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if err != nil {
			continue
		}
		i += 4
		if n >= 0xdc00 && n <= 0xdfff {
			return true
		}
		if n >= 0xd800 && n <= 0xdbff {
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return true
			}
			low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return true
			}
			i += 6
		}
	}
	return false
}
