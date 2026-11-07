package platform

import (
	"encoding/json"
	"errors"
	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbquery"
	"github.com/wenhaokho/wenhao-billing/backend/internal/dbqueryv8"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopeditem"
	"github.com/wenhaokho/wenhao-billing/backend/internal/scopeduser"
	"net/http"
)

func legacyItem(v dbqueryv8.Item) dbquery.Item {
	return dbquery.Item{ItemID: v.ItemID, Sku: v.Sku, Name: v.Name, ItemType: v.ItemType, Description: v.Description, DefaultCurrency: v.DefaultCurrency, DefaultUnitPrice: v.DefaultUnitPrice, RevenueAccountID: v.RevenueAccountID, Active: v.Active, CreatedAt: v.CreatedAt, IsSold: v.IsSold, IsPurchased: v.IsPurchased, ExpenseAccountID: v.ExpenseAccountID, DefaultPurchasePrice: v.DefaultPurchasePrice, IsHosting: v.IsHosting, HostingDomain: v.HostingDomain, HostingGraceDays: v.HostingGraceDays, HostingSuspensionEnabled: v.HostingSuspensionEnabled, HostingStatus: v.HostingStatus, HostingLastPaidAt: v.HostingLastPaidAt, HostingLastActionAt: v.HostingLastActionAt, HostingLastError: v.HostingLastError}
}
func legacyItemTarget(v dbqueryv8.CloudflareTarget) dbquery.CloudflareTarget {
	return dbquery.CloudflareTarget{TargetID: v.TargetID, ItemID: v.ItemID, ZoneID: v.ZoneID, RecordID: v.RecordID, RecordName: v.RecordName, RecordType: v.RecordType, LiveContent: v.LiveContent, MaintenanceContent: v.MaintenanceContent, Proxied: v.Proxied, ProviderStatus: v.ProviderStatus, LastActionAt: v.LastActionAt, LastError: v.LastError, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
}
func (h scopedProfileRouter) itemRead(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.admin(w, r); !ok {
		return
	}
	ctx := r.Context()
	var items []dbqueryv8.Item
	if idRaw := chi.URLParam(r, "item_id"); idRaw != "" {
		id, valid := parsePathUUID(idRaw)
		if !valid {
			detail := uuidErrorDetail(idRaw)
			validationResponse(w, []validationError{{"uuid_parsing", []any{"path", "item_id"}, "Input should be a valid UUID, " + detail, idRaw, map[string]string{"error": detail}}})
			return
		}
		i, err := scopeditem.GetItem(ctx, h.pool, h.scope, dbqueryv8.GetItemParams{ItemID: id})
		if errors.Is(err, pgx.ErrNoRows) {
			authError(w, 404, "item not found")
			return
		}
		if err != nil {
			authError(w, 500, "internal server error")
			return
		}
		items = []dbqueryv8.Item{i}
	} else {
		a, ok := boolFilter(w, r, "active")
		if !ok {
			return
		}
		hosting, ok := boolFilter(w, r, "is_hosting")
		if !ok {
			return
		}
		var typ string
		if values := r.URL.Query()["item_type"]; len(values) > 0 {
			typ = values[len(values)-1]
		}
		var err error
		items, err = scopeditem.ListItems(ctx, h.pool, h.scope, dbqueryv8.ListItemsParams{Active: a, ItemType: pgtype.Text{String: typ, Valid: typ != ""}, IsHosting: hosting})
		if err != nil {
			authError(w, 500, "internal server error")
			return
		}
	}
	out := make([]itemOutput, 0, len(items))
	for _, i := range items {
		t, err := scopeditem.GetItemTarget(ctx, h.pool, h.scope, dbqueryv8.GetItemTargetParams{ItemID: i.ItemID})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			authError(w, 500, "internal server error")
			return
		}
		var target *dbquery.CloudflareTarget
		if err == nil {
			legacy := legacyItemTarget(t)
			target = &legacy
		}
		out = append(out, itemJSON(legacyItem(i), target))
	}
	w.Header().Set("Content-Type", "application/json")
	if chi.URLParam(r, "item_id") != "" {
		writeSafeJSON(w, out[0])
	} else {
		writeSafeJSON(w, out)
	}
}

func (h scopedProfileRouter) itemDeactivate(w http.ResponseWriter, r *http.Request) {
	current, ok := h.admin(w, r)
	if !ok {
		return
	}
	raw := chi.URLParam(r, "item_id")
	id, valid := parsePathUUID(raw)
	if !valid {
		detail := uuidErrorDetail(raw)
		validationResponse(w, []validationError{{"uuid_parsing", []any{"path", "item_id"}, "Input should be a valid UUID, " + detail, raw, map[string]string{"error": detail}}})
		return
	}
	tx, err := h.pool.Begin(r.Context())
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	defer tx.Rollback(r.Context())
	locked, err := scopeduser.LockSessionUserByID(r.Context(), tx, h.scope, dbqueryv8.LockSessionUserByIDParams{UserID: current.UserID})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && locked.Role != "admin" {
		authError(w, 403, "admin required")
		return
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	i, err := scopeditem.DeactivateItem(r.Context(), tx, h.scope, dbqueryv8.DeactivateItemParams{ItemID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		authError(w, 404, "item not found")
		return
	}
	if err != nil {
		authError(w, 500, "internal server error")
		return
	}
	t, err := scopeditem.GetItemTarget(r.Context(), tx, h.scope, dbqueryv8.GetItemTargetParams{ItemID: id})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		authError(w, 500, "internal server error")
		return
	}
	var target *dbquery.CloudflareTarget
	if err == nil {
		legacy := legacyItemTarget(t)
		target = &legacy
	}
	encoded, err := json.Marshal(itemJSON(legacyItem(i), target))
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
