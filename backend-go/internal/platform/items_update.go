package platform

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
)

type itemUpdateSchema struct {
	Sku                      *string                 `json:"sku,omitempty"`
	Name                     *string                 `json:"name,omitempty"`
	ItemType                 *string                 `json:"item_type,omitempty" enum:"SERVICE,USAGE,FIXED_FEE"`
	Description              *string                 `json:"description,omitempty"`
	DefaultCurrency          *string                 `json:"default_currency,omitempty" minLength:"3" maxLength:"3"`
	DefaultUnitPrice         *string                 `json:"default_unit_price,omitempty"`
	DefaultPurchasePrice     *string                 `json:"default_purchase_price,omitempty"`
	RevenueAccountID         *int                    `json:"revenue_account_id,omitempty"`
	ExpenseAccountID         *int                    `json:"expense_account_id,omitempty"`
	IsSold                   *bool                   `json:"is_sold,omitempty"`
	IsPurchased              *bool                   `json:"is_purchased,omitempty"`
	Active                   *bool                   `json:"active,omitempty"`
	IsHosting                *bool                   `json:"is_hosting,omitempty"`
	HostingDomain            *string                 `json:"hosting_domain,omitempty" maxLength:"255"`
	HostingGraceDays         *int                    `json:"hosting_grace_days,omitempty" minimum:"0" maximum:"365"`
	HostingSuspensionEnabled *bool                   `json:"hosting_suspension_enabled,omitempty"`
	HostingStatus            *string                 `json:"hosting_status,omitempty" enum:"ACTIVE,SUSPEND_PENDING,SUSPENDED,CANCELLED"`
	CloudflareTarget         *itemCreateTargetSchema `json:"cloudflare_target,omitempty"`
}

func registerItemUpdate(api huma.API, s sessionHandler) {
	huma.Register(api, huma.Operation{Method: "PATCH", Path: "/api/v1/items/{item_id}", OperationID: "update-item", SkipValidateBody: true, Security: []map[string][]string{{"sessionCookie": {}}}, Middlewares: huma.Middlewares{sessionMiddleware(s.itemUpdate)}}, func(context.Context, *struct {
		ID   string `path:"item_id" format:"uuid"`
		Body itemUpdateSchema
	}) (*struct{ Body itemOutputSchema }, error) {
		panic("middleware")
	})
	schema := api.OpenAPI().Components.Schemas.Map()["ItemUpdateSchema"]
	for _, field := range []string{"default_unit_price", "default_purchase_price"} {
		*schema.Properties[field] = huma.Schema{AnyOf: []*huma.Schema{{Type: "number"}, {Type: "string"}, {Type: "null"}}}
	}
	for _, field := range []string{"sku", "name", "item_type", "description", "default_currency", "revenue_account_id", "expense_account_id", "is_sold", "is_purchased", "active", "is_hosting", "hosting_domain", "hosting_grace_days", "hosting_suspension_enabled", "hosting_status"} {
		schema.Properties[field].Nullable = true
	}
	target := schema.Properties["cloudflare_target"]
	schema.Properties["cloudflare_target"] = &huma.Schema{AnyOf: []*huma.Schema{target, {Type: "null"}}}
	for _, field := range []string{"item_type", "hosting_status"} {
		schema.Properties[field].Enum = append(schema.Properties[field].Enum, nil)
	}
}
func (s sessionHandler) itemUpdate(w http.ResponseWriter, r *http.Request) {
	body, ok := readBodyWithSyntax(w, r)
	if !ok {
		return
	}
	if _, ok := s.requireAdmin(w, r); !ok {
		return
	}
	u, errs := parseItemUpdate(body)
	// FastAPI decodes malformed JSON before validating path parameters.
	if len(errs) > 0 && errs[0].Type == "json_invalid" {
		validationResponse(w, errs)
		return
	}
	raw := chi.URLParam(r, "item_id")
	id, valid := parsePathUUID(raw)
	if !valid {
		detail := uuidErrorDetail(raw)
		errs = append([]validationError{{"uuid_parsing", []any{"path", "item_id"}, "Input should be a valid UUID, " + detail, raw, map[string]string{"error": detail}}}, errs...)
	}
	if len(errs) > 0 {
		validationResponse(w, errs)
		return
	}
	tx, err := s.db.Begin(r.Context())
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	defer tx.Rollback(r.Context())
	q := dbquery.New(tx)
	i, err := q.LockItem(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		authError(w, 404, "item not found")
		return
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	// SQL non-null columns reject explicit null in the Python implementation.
	for _, invalid := range []bool{u.Name.Present && u.Name.Null, u.ItemType.Present && u.ItemType.Null, u.DefaultCurrency.Present && u.DefaultCurrency.Null, u.IsSold.Present && u.IsSold.Null, u.IsPurchased.Present && u.IsPurchased.Null, u.Active.Present && u.Active.Null, u.IsHosting.Present && u.IsHosting.Null} {
		if invalid {
			authError(w, 500, "internal server error")
			return
		}
	}
	setText := func(f updateField[string], v *pgtype.Text) {
		if f.Present {
			*v = pgtype.Text{String: f.Value, Valid: !f.Null}
		}
	}
	setInt := func(f updateField[*big.Int], v *pgtype.Int4) bool {
		if !f.Present {
			return true
		}
		if f.Null {
			*v = pgtype.Int4{}
			return true
		}
		if !f.Value.IsInt64() || f.Value.Int64() < -2147483648 || f.Value.Int64() > 2147483647 {
			return false
		}
		*v = pgtype.Int4{Int32: int32(f.Value.Int64()), Valid: true}
		return true
	}
	setMoney := func(f updateField[string], v *pgtype.Numeric) {
		if f.Present {
			if f.Null {
				*v = pgtype.Numeric{}
			} else {
				v.Scan(f.Value)
			}
		}
	}
	setText(u.Sku, &i.Sku)
	setText(u.Description, &i.Description)
	setText(u.HostingDomain, &i.HostingDomain)
	setText(u.HostingStatus, &i.HostingStatus)
	if u.HostingDomain.Present && !u.HostingDomain.Null && u.HostingDomain.Value != "" {
		i.HostingDomain.String = strings.ToLower(strings.TrimSpace(u.HostingDomain.Value))
	}
	if u.Name.Present {
		i.Name = u.Name.Value
	}
	if u.ItemType.Present {
		i.ItemType = u.ItemType.Value
	}
	if u.DefaultCurrency.Present {
		i.DefaultCurrency = u.DefaultCurrency.Value
	}
	if u.IsSold.Present {
		i.IsSold = u.IsSold.Value
	}
	if u.IsPurchased.Present {
		i.IsPurchased = u.IsPurchased.Value
	}
	if u.Active.Present {
		i.Active = u.Active.Value
	}
	if u.IsHosting.Present {
		i.IsHosting = u.IsHosting.Value
	}
	if u.HostingSuspensionEnabled.Present {
		i.HostingSuspensionEnabled = pgtype.Bool{Bool: u.HostingSuspensionEnabled.Value, Valid: !u.HostingSuspensionEnabled.Null}
	}
	if !setInt(u.RevenueAccountID, &i.RevenueAccountID) || !setInt(u.ExpenseAccountID, &i.ExpenseAccountID) || !setInt(u.HostingGraceDays, &i.HostingGraceDays) {
		authError(w, 500, "internal server error")
		return
	}
	setMoney(u.DefaultUnitPrice, &i.DefaultUnitPrice)
	setMoney(u.DefaultPurchasePrice, &i.DefaultPurchasePrice)
	if i.IsHosting && !i.HostingStatus.Valid {
		i.HostingStatus = pgtype.Text{String: "ACTIVE", Valid: true}
	}
	// ItemOut requires a nonempty name. FastAPI discovers this after commit;
	// reject before writing so the response error cannot persist an invalid item.
	if i.Name == "" {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Internal Server Error"))
		return
	}
	i, err = q.UpdateItem(r.Context(), dbquery.UpdateItemParams{ItemID: id, Sku: i.Sku, Name: i.Name, ItemType: i.ItemType, Description: i.Description, DefaultCurrency: i.DefaultCurrency, DefaultUnitPrice: i.DefaultUnitPrice, DefaultPurchasePrice: i.DefaultPurchasePrice, RevenueAccountID: i.RevenueAccountID, ExpenseAccountID: i.ExpenseAccountID, IsSold: i.IsSold, IsPurchased: i.IsPurchased, Active: i.Active, IsHosting: i.IsHosting, HostingDomain: i.HostingDomain, HostingGraceDays: i.HostingGraceDays, HostingSuspensionEnabled: i.HostingSuspensionEnabled, HostingStatus: i.HostingStatus})
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	t, err := q.GetItemTarget(r.Context(), id)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		authError(w, 500, "internal server error")
		return
	}
	hasTarget := err == nil
	if u.CloudflareTarget.Present && !u.CloudflareTarget.Null {
		v := u.CloudflareTarget.Value
		proxied := true
		if hasTarget {
			proxied = t.Proxied
		}
		if v.Proxied.Present {
			proxied = v.Proxied.Value
		}
		tid, e := itemUUID()
		if e != nil {
			authError(w, 500, "internal server error")
			return
		}
		t, e = q.UpsertItemTarget(r.Context(), dbquery.UpsertItemTargetParams{TargetID: tid, ItemID: id, ZoneID: strings.TrimSpace(v.ZoneID), RecordID: strings.TrimSpace(v.RecordID), RecordName: strings.ToLower(strings.TrimSpace(v.RecordName)), RecordType: strings.ToUpper(strings.TrimSpace(v.RecordType)), LiveContent: strings.TrimSpace(v.LiveContent), MaintenanceContent: strings.TrimSpace(v.MaintenanceContent), Proxied: proxied})
		if e != nil {
			authError(w, 500, "internal server error")
			return
		}
		hasTarget = true
	}
	var target *dbquery.CloudflareTarget
	if hasTarget {
		target = &t
	}
	encoded, err := json.Marshal(itemJSON(i, target))
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	if err = tx.Commit(r.Context()); err != nil {
		authError(w, 500, "internal server error")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(append(encoded, '\n'))
}
