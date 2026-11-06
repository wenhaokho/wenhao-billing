package platform

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
)

// pythonTimestamp mirrors Pydantic's formatting for naive PostgreSQL timestamps.
type pythonTimestamp pgtype.Timestamp

func (t pythonTimestamp) MarshalJSON() ([]byte, error) {
	original := pgtype.Timestamp(t)
	if !original.Valid {
		return original.MarshalJSON()
	}
	if original.InfinityModifier != pgtype.Finite || original.Time.Year() < 1 || original.Time.Year() > 9999 {
		return nil, errors.New("invalid timestamp")
	}
	layout := "2006-01-02T15:04:05"
	if original.Time.Nanosecond() != 0 {
		layout += ".000000"
	}
	return json.Marshal(original.Time.Format(layout))
}

// writeSafeJSON marshals the complete response before writing headers so a bad
// stored timestamp cannot produce a partial successful response.
func writeSafeJSON(w http.ResponseWriter, value any) {
	b, err := json.Marshal(value)
	if err != nil {
		authError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(append(b, '\n'))
}

type itemTarget struct {
	TargetID           pgtype.UUID     `json:"target_id"`
	ItemID             pgtype.UUID     `json:"item_id"`
	ZoneID             string          `json:"zone_id"`
	RecordID           string          `json:"record_id"`
	RecordName         string          `json:"record_name"`
	RecordType         string          `json:"record_type"`
	LiveContent        string          `json:"live_content"`
	MaintenanceContent string          `json:"maintenance_content"`
	Proxied            bool            `json:"proxied"`
	ProviderStatus     string          `json:"provider_status"`
	LastActionAt       pythonTimestamp `json:"last_action_at"`
	LastError          pgtype.Text     `json:"last_error"`
	CreatedAt          pythonTimestamp `json:"created_at"`
	UpdatedAt          pythonTimestamp `json:"updated_at"`
}
type itemOutput struct {
	ItemID                   pgtype.UUID     `json:"item_id"`
	Sku                      pgtype.Text     `json:"sku"`
	Name                     string          `json:"name"`
	ItemType                 string          `json:"item_type"`
	Description              pgtype.Text     `json:"description"`
	DefaultCurrency          string          `json:"default_currency"`
	DefaultUnitPrice         *string         `json:"default_unit_price"`
	DefaultPurchasePrice     *string         `json:"default_purchase_price"`
	RevenueAccountID         pgtype.Int4     `json:"revenue_account_id"`
	ExpenseAccountID         pgtype.Int4     `json:"expense_account_id"`
	IsSold                   bool            `json:"is_sold"`
	IsPurchased              bool            `json:"is_purchased"`
	Active                   bool            `json:"active"`
	IsHosting                bool            `json:"is_hosting"`
	HostingDomain            pgtype.Text     `json:"hosting_domain"`
	HostingGraceDays         pgtype.Int4     `json:"hosting_grace_days"`
	HostingSuspensionEnabled pgtype.Bool     `json:"hosting_suspension_enabled"`
	HostingStatus            pgtype.Text     `json:"hosting_status"`
	HostingLastPaidAt        pythonTimestamp `json:"hosting_last_paid_at"`
	HostingLastActionAt      pythonTimestamp `json:"hosting_last_action_at"`
	HostingLastError         pgtype.Text     `json:"hosting_last_error"`
	CreatedAt                pythonTimestamp `json:"created_at"`
	CloudflareTarget         *itemTarget     `json:"cloudflare_target"`
}

type itemTargetSchema struct {
	TargetID           string  `json:"target_id" format:"uuid"`
	ItemID             string  `json:"item_id" format:"uuid"`
	ZoneID             string  `json:"zone_id"`
	RecordID           string  `json:"record_id"`
	RecordName         string  `json:"record_name"`
	RecordType         string  `json:"record_type"`
	LiveContent        string  `json:"live_content"`
	MaintenanceContent string  `json:"maintenance_content"`
	Proxied            bool    `json:"proxied"`
	ProviderStatus     string  `json:"provider_status"`
	LastActionAt       *string `json:"last_action_at" format:"date-time"`
	LastError          *string `json:"last_error"`
	CreatedAt          string  `json:"created_at" format:"date-time"`
	UpdatedAt          string  `json:"updated_at" format:"date-time"`
}
type itemOutputSchema struct {
	ItemID                   string            `json:"item_id" format:"uuid"`
	Sku                      *string           `json:"sku"`
	Name                     string            `json:"name"`
	ItemType                 string            `json:"item_type" enum:"SERVICE,USAGE,FIXED_FEE"`
	Description              *string           `json:"description"`
	DefaultCurrency          string            `json:"default_currency"`
	DefaultUnitPrice         *string           `json:"default_unit_price"`
	DefaultPurchasePrice     *string           `json:"default_purchase_price"`
	RevenueAccountID         *int              `json:"revenue_account_id"`
	ExpenseAccountID         *int              `json:"expense_account_id"`
	IsSold                   bool              `json:"is_sold"`
	IsPurchased              bool              `json:"is_purchased"`
	Active                   bool              `json:"active"`
	IsHosting                bool              `json:"is_hosting"`
	HostingDomain            *string           `json:"hosting_domain"`
	HostingGraceDays         *int              `json:"hosting_grace_days"`
	HostingSuspensionEnabled *bool             `json:"hosting_suspension_enabled"`
	HostingStatus            *string           `json:"hosting_status" enum:"ACTIVE,SUSPEND_PENDING,SUSPENDED,CANCELLED"`
	HostingLastPaidAt        *string           `json:"hosting_last_paid_at" format:"date-time"`
	HostingLastActionAt      *string           `json:"hosting_last_action_at" format:"date-time"`
	HostingLastError         *string           `json:"hosting_last_error"`
	CreatedAt                string            `json:"created_at" format:"date-time"`
	CloudflareTarget         *itemTargetSchema `json:"cloudflare_target"`
}

func numericString(n pgtype.Numeric) *string {
	if !n.Valid {
		return nil
	}
	v, err := n.Value()
	if err != nil {
		return nil
	}
	s := v.(string)
	return &s
}
func itemJSON(i dbquery.Item, t *dbquery.CloudflareTarget) itemOutput {
	o := itemOutput{i.ItemID, i.Sku, i.Name, i.ItemType, i.Description, i.DefaultCurrency, numericString(i.DefaultUnitPrice), numericString(i.DefaultPurchasePrice), i.RevenueAccountID, i.ExpenseAccountID, i.IsSold, i.IsPurchased, i.Active, i.IsHosting, i.HostingDomain, i.HostingGraceDays, i.HostingSuspensionEnabled, i.HostingStatus, pythonTimestamp(i.HostingLastPaidAt), pythonTimestamp(i.HostingLastActionAt), i.HostingLastError, pythonTimestamp(i.CreatedAt), nil}
	if t != nil {
		o.CloudflareTarget = &itemTarget{t.TargetID, t.ItemID, t.ZoneID, t.RecordID, t.RecordName, t.RecordType, t.LiveContent, t.MaintenanceContent, t.Proxied, t.ProviderStatus, pythonTimestamp(t.LastActionAt), t.LastError, pythonTimestamp(t.CreatedAt), pythonTimestamp(t.UpdatedAt)}
	}
	return o
}
func boolFilter(w http.ResponseWriter, r *http.Request, key string) (pgtype.Bool, bool) {
	v, exists := r.URL.Query()[key]
	if !exists {
		return pgtype.Bool{}, true
	}
	raw := strings.ToLower(v[len(v)-1])
	switch raw {
	case "1", "true", "on", "yes", "y", "t":
		return pgtype.Bool{Bool: true, Valid: true}, true
	case "0", "false", "off", "no", "n", "f":
		return pgtype.Bool{Bool: false, Valid: true}, true
	}
	validationResponse(w, []validationError{{"bool_parsing", []any{"query", key}, "Input should be a valid boolean", v[len(v)-1], nil}})
	return pgtype.Bool{}, false
}

// parsePathUUID accepts the UUID string forms understood by Pydantic while
// enforcing separator positions before handing the canonical value to pgx.
func parsePathUUID(raw string) (pgtype.UUID, bool) {
	var id pgtype.UUID
	value := raw
	wrapped := false
	if strings.HasPrefix(value, "urn:uuid:") {
		value = value[len("urn:uuid:"):]
		wrapped = true
	} else if strings.HasPrefix(value, "{") && strings.HasSuffix(value, "}") {
		value = value[1 : len(value)-1]
		wrapped = true
	}
	if len(value) != 32 && len(value) != 36 {
		return id, false
	}
	if wrapped && len(value) != 36 {
		return id, false
	}
	for i, c := range value {
		if len(value) == 36 && (i == 8 || i == 13 || i == 18 || i == 23) {
			if c != '-' {
				return id, false
			}
			continue
		}
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return id, false
		}
	}
	if id.Scan(value) != nil || !id.Valid {
		return pgtype.UUID{}, false
	}
	return id, true
}
func uuidErrorDetail(raw string) string {
	// Bound validation work for unusually large URL path segments.
	if len(raw) > 4096 {
		return fmt.Sprintf("invalid length: found %d", len(raw))
	}
	wrapped := false
	offset := 0
	if strings.HasPrefix(raw, "urn:uuid:") {
		raw = strings.TrimPrefix(raw, "urn:uuid:")
		offset = len("urn:uuid:")
		wrapped = true
	} else if strings.HasPrefix(raw, "{") && strings.HasSuffix(raw, "}") {
		raw = raw[1 : len(raw)-1]
		offset = 1
		wrapped = true
	}
	for pos, c := range raw {
		if c != '-' && !(c >= '0' && c <= '9') && !(c >= 'a' && c <= 'f') && !(c >= 'A' && c <= 'F') {
			return fmt.Sprintf("invalid character: found `%c` at %d", c, pos+offset)
		}
	}
	if wrapped || strings.Contains(raw, "-") {
		groups := strings.Split(raw, "-")
		if len(groups) != 5 {
			return fmt.Sprintf("invalid group count: expected 5, found %d", len(groups))
		}
		for i, expected := range []int{8, 4, 4, 4, 12} {
			if len(groups[i]) != expected {
				return fmt.Sprintf("invalid group length in group %d: expected %d, found %d", i, expected, len(groups[i]))
			}
		}
	}
	return fmt.Sprintf("invalid length: found %d", len(raw))
}
func (s sessionHandler) itemRead(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	q := dbquery.New(s.db)
	ctx := r.Context()
	var items []dbquery.Item
	if idRaw := chi.URLParam(r, "item_id"); idRaw != "" {
		id, valid := parsePathUUID(idRaw)
		if !valid {
			detail := uuidErrorDetail(idRaw)
			validationResponse(w, []validationError{{"uuid_parsing", []any{"path", "item_id"}, "Input should be a valid UUID, " + detail, idRaw, map[string]string{"error": detail}}})
			return
		}
		i, err := q.GetItem(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			authError(w, 404, "item not found")
			return
		}
		if err != nil {
			authError(w, 500, "internal server error")
			return
		}
		items = []dbquery.Item{i}
	} else {
		a, ok := boolFilter(w, r, "active")
		if !ok {
			return
		}
		h, ok := boolFilter(w, r, "is_hosting")
		if !ok {
			return
		}
		var typ string
		if values := r.URL.Query()["item_type"]; len(values) > 0 {
			typ = values[len(values)-1]
		}
		var err error
		items, err = q.ListItems(ctx, dbquery.ListItemsParams{Active: a, ItemType: pgtype.Text{String: typ, Valid: typ != ""}, IsHosting: h})
		if err != nil {
			authError(w, 500, "internal server error")
			return
		}
	}
	out := make([]itemOutput, 0, len(items))
	for _, i := range items {
		t, err := q.GetItemTarget(ctx, i.ItemID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			authError(w, 500, "internal server error")
			return
		}
		var target *dbquery.CloudflareTarget
		if err == nil {
			target = &t
		}
		out = append(out, itemJSON(i, target))
	}
	w.Header().Set("Content-Type", "application/json")
	if chi.URLParam(r, "item_id") != "" {
		writeSafeJSON(w, out[0])
	} else {
		writeSafeJSON(w, out)
	}
}

type itemListInput struct {
	Active    bool   `query:"active"`
	ItemType  string `query:"item_type"`
	IsHosting bool   `query:"is_hosting"`
}
type itemGetInput struct {
	ID string `path:"item_id" format:"uuid"`
}

func registerItemReads(api huma.API, s sessionHandler) {
	security := []map[string][]string{{"sessionCookie": {}}}
	m := huma.Middlewares{sessionMiddleware(s.itemRead)}
	huma.Register(api, huma.Operation{Method: "GET", Path: "/api/v1/items", OperationID: "list-items", Security: security, Middlewares: m}, func(context.Context, *itemListInput) (*struct{ Body []itemOutputSchema }, error) { panic("middleware") })
	huma.Register(api, huma.Operation{Method: "GET", Path: "/api/v1/items/{item_id}", OperationID: "get-item", Security: security, Middlewares: m}, func(context.Context, *itemGetInput) (*struct{ Body itemOutputSchema }, error) { panic("middleware") })
	huma.Register(api, huma.Operation{Method: "DELETE", Path: "/api/v1/items/{item_id}", OperationID: "deactivate-item", Security: security, Middlewares: huma.Middlewares{sessionMiddleware(s.itemDeactivate)}}, func(context.Context, *itemGetInput) (*struct{ Body itemOutputSchema }, error) { panic("middleware") })
	// JSON Schema enums must explicitly include null for nullable fields.
	status := api.OpenAPI().Components.Schemas.Map()["ItemOutputSchema"].Properties["hosting_status"]
	status.Enum = append(status.Enum, nil)
}

func (s sessionHandler) itemDeactivate(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	raw := chi.URLParam(r, "item_id")
	id, valid := parsePathUUID(raw)
	if !valid {
		detail := uuidErrorDetail(raw)
		validationResponse(w, []validationError{{"uuid_parsing", []any{"path", "item_id"}, "Input should be a valid UUID, " + detail, raw, map[string]string{"error": detail}}})
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	defer tx.Rollback(r.Context())
	q := dbquery.New(tx)
	i, err := q.DeactivateItem(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		authError(w, 404, "item not found")
		return
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	t, err := q.GetItemTarget(r.Context(), id)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		authError(w, 500, "internal server error")
		return
	}
	var target *dbquery.CloudflareTarget
	if err == nil {
		target = &t
	}
	encoded, err := json.Marshal(itemJSON(i, target))
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(append(encoded, '\n'))
}

// readItemBody bounds memory use for both item write endpoints, including chunked requests.
func readItemBody(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			authError(w, http.StatusRequestEntityTooLarge, "request body too large")
		} else {
			authError(w, http.StatusInternalServerError, "internal server error")
		}
		return nil, false
	}
	if len(body) > 0 && !requestJSONMediaType(r) {
		// Never include unparsed bytes in a validation response.
		return []byte(`"[REDACTED]"`), true
	}
	return body, true
}

// FastAPI uses Python email.message, which treats types with multiple slashes as text/plain.
// Its parameter parsing is permissive, so mime.ParseMediaType would reject valid cases.
func requestJSONMediaType(r *http.Request) bool {
	media, _, _ := strings.Cut(r.Header.Get("Content-Type"), ";")
	media = strings.ToLower(strings.TrimSpace(media))
	major, subtype, found := strings.Cut(media, "/")
	if !found || strings.Contains(subtype, "/") || major != "application" {
		return false
	}
	return subtype == "json" || strings.HasSuffix(subtype, "+json")
}

// readBodyWithSyntax performs FastAPI's JSON syntax check before dependencies.
func readBodyWithSyntax(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	body, ok := readItemBody(w, r)
	if !ok || len(body) == 0 {
		return body, ok
	}
	d := json.NewDecoder(bytes.NewReader(body))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		validationResponse(w, itemUpdateJSONError(body, err, false))
		return nil, false
	}
	if err := d.Decode(&v); err != io.EOF {
		validationResponse(w, itemUpdateJSONError(body, err, true))
		return nil, false
	}
	return body, true
}
