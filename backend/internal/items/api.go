// Package items serves the item catalog at /api/v1/items with the Python
// backend's contract: list (by active, item_type and is_hosting), create,
// read, partial update, and deactivate. A hosting item carries its domain,
// grace period and the one Cloudflare DNS record that hosting enforcement
// flips between live and maintenance content. Items are never deleted.
package items

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/items/itemsdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// ItemTypes are Python's ItemType (and ck_items_type). Every item_type
// field's enum tag lists the same values (TestItemEnumsMatch).
var ItemTypes = []string{"SERVICE", "USAGE", "FIXED_FEE"}

// HostingStatuses are Python's HostingStatus. Every hosting_status field's
// enum tag lists the same values (TestItemEnumsMatch).
var HostingStatuses = []string{"ACTIVE", "SUSPEND_PENDING", "SUSPENDED", "CANCELLED"}

// CloudflareTargetIn is the item's DNS record, as a create or PATCH sends it.
// Strings are trimmed; record_name is lower-cased and record_type upper-cased.
type CloudflareTargetIn struct {
	ZoneID             string                 `json:"zone_id" minLength:"1" maxLength:"100"`
	RecordID           string                 `json:"record_id" minLength:"1" maxLength:"100"`
	RecordName         string                 `json:"record_name" minLength:"1" maxLength:"255"`
	RecordType         string                 `json:"record_type" minLength:"1" maxLength:"20"`
	LiveContent        string                 `json:"live_content" minLength:"1"`
	MaintenanceContent string                 `json:"maintenance_content" minLength:"1"`
	Proxied            platform.NotNull[bool] `json:"proxied" doc:"Defaults to true; a PATCH that leaves it out keeps the stored value."`
}

// CloudflareTargetOut is the item's DNS record.
type CloudflareTargetOut struct {
	TargetID           uuid.UUID      `json:"target_id"`
	ItemID             uuid.UUID      `json:"item_id"`
	ZoneID             string         `json:"zone_id"`
	RecordID           string         `json:"record_id"`
	RecordName         string         `json:"record_name"`
	RecordType         string         `json:"record_type"`
	LiveContent        string         `json:"live_content"`
	MaintenanceContent string         `json:"maintenance_content"`
	Proxied            bool           `json:"proxied"`
	ProviderStatus     string         `json:"provider_status"`
	LastActionAt       *platform.Time `json:"last_action_at"`
	LastError          *string        `json:"last_error"`
	CreatedAt          platform.Time  `json:"created_at"`
	UpdatedAt          platform.Time  `json:"updated_at"`
}

// ItemOut is an item, as every route answers; cloudflare_target is null when
// the item has none.
type ItemOut struct {
	Sku                      *string              `json:"sku"`
	Name                     string               `json:"name"`
	ItemType                 string               `json:"item_type" enum:"SERVICE,USAGE,FIXED_FEE"`
	Description              *string              `json:"description"`
	DefaultCurrency          string               `json:"default_currency"`
	DefaultUnitPrice         *platform.Dec4       `json:"default_unit_price"`
	DefaultPurchasePrice     *platform.Dec4       `json:"default_purchase_price"`
	RevenueAccountID         *int32               `json:"revenue_account_id"`
	ExpenseAccountID         *int32               `json:"expense_account_id"`
	IsSold                   bool                 `json:"is_sold"`
	IsPurchased              bool                 `json:"is_purchased"`
	Active                   bool                 `json:"active"`
	IsHosting                bool                 `json:"is_hosting"`
	HostingDomain            *string              `json:"hosting_domain"`
	HostingGraceDays         *int32               `json:"hosting_grace_days"`
	HostingSuspensionEnabled *bool                `json:"hosting_suspension_enabled"`
	ItemID                   uuid.UUID            `json:"item_id"`
	CreatedAt                platform.Time        `json:"created_at"`
	HostingStatus            *string              `json:"hosting_status" enum:"ACTIVE,SUSPEND_PENDING,SUSPENDED,CANCELLED"`
	HostingLastPaidAt        *platform.Time       `json:"hosting_last_paid_at"`
	HostingLastActionAt      *platform.Time       `json:"hosting_last_action_at"`
	HostingLastError         *string              `json:"hosting_last_error"`
	CloudflareTarget         *CloudflareTargetOut `json:"cloudflare_target"`
}

func itemOut(it itemsdb.Item, t *itemsdb.CloudflareTarget) ItemOut {
	out := ItemOut{
		Sku: it.Sku, Name: it.Name, ItemType: it.ItemType, Description: it.Description,
		DefaultCurrency: it.DefaultCurrency, DefaultUnitPrice: dec4(it.DefaultUnitPrice),
		DefaultPurchasePrice: dec4(it.DefaultPurchasePrice), RevenueAccountID: it.RevenueAccountID,
		ExpenseAccountID: it.ExpenseAccountID, IsSold: it.IsSold, IsPurchased: it.IsPurchased, Active: it.Active,
		IsHosting: it.IsHosting, HostingDomain: it.HostingDomain, HostingGraceDays: it.HostingGraceDays,
		HostingSuspensionEnabled: it.HostingSuspensionEnabled, ItemID: it.ItemID,
		CreatedAt: platform.Time{Time: it.CreatedAt}, HostingStatus: it.HostingStatus,
		HostingLastPaidAt: timePtr(it.HostingLastPaidAt), HostingLastActionAt: timePtr(it.HostingLastActionAt),
		HostingLastError: it.HostingLastError,
	}
	if t != nil {
		out.CloudflareTarget = &CloudflareTargetOut{
			TargetID: t.TargetID, ItemID: t.ItemID, ZoneID: t.ZoneID, RecordID: t.RecordID,
			RecordName: t.RecordName, RecordType: t.RecordType, LiveContent: t.LiveContent,
			MaintenanceContent: t.MaintenanceContent, Proxied: t.Proxied, ProviderStatus: t.ProviderStatus,
			LastActionAt: timePtr(t.LastActionAt), LastError: t.LastError,
			CreatedAt: platform.Time{Time: t.CreatedAt}, UpdatedAt: platform.Time{Time: t.UpdatedAt},
		}
	}
	return out
}

func dec4(d decimal.NullDecimal) *platform.Dec4 {
	if !d.Valid {
		return nil
	}
	return &platform.Dec4{Decimal: d.Decimal}
}

func timePtr(t *time.Time) *platform.Time {
	if t == nil {
		return nil
	}
	return &platform.Time{Time: *t}
}

// ItemCreate adds an item. The NOT NULL columns with a default may be left
// out but not sent as null (Python's field defaults); the nullable ones take
// null as no value. A hosting item (is_hosting true) must send
// hosting_domain, hosting_grace_days, hosting_suspension_enabled and
// cloudflare_target.
type ItemCreate struct {
	Sku                      platform.Optional[string]        `json:"sku" maxLength:"50"`
	Name                     string                           `json:"name" minLength:"1" maxLength:"255"`
	ItemType                 string                           `json:"item_type" enum:"SERVICE,USAGE,FIXED_FEE"`
	Description              platform.Optional[string]        `json:"description"`
	DefaultCurrency          platform.NotNull[string]         `json:"default_currency" minLength:"3" maxLength:"3" doc:"Defaults to IDR."`
	DefaultUnitPrice         platform.Optional[platform.Dec4] `json:"default_unit_price" doc:"At least 0, at most 4 decimal places."`
	DefaultPurchasePrice     platform.Optional[platform.Dec4] `json:"default_purchase_price" doc:"At least 0, at most 4 decimal places."`
	RevenueAccountID         platform.Optional[int32]         `json:"revenue_account_id"`
	ExpenseAccountID         platform.Optional[int32]         `json:"expense_account_id"`
	IsSold                   platform.NotNull[bool]           `json:"is_sold" doc:"Defaults to true."`
	IsPurchased              platform.NotNull[bool]           `json:"is_purchased" doc:"Defaults to false."`
	Active                   platform.NotNull[bool]           `json:"active" doc:"Defaults to true."`
	IsHosting                platform.NotNull[bool]           `json:"is_hosting" doc:"Defaults to false."`
	HostingDomain            platform.Optional[string]        `json:"hosting_domain" maxLength:"255" doc:"Trimmed and lower-cased."`
	HostingGraceDays         platform.Optional[int32]         `json:"hosting_grace_days" minimum:"0" maximum:"365"`
	HostingSuspensionEnabled platform.Optional[bool]          `json:"hosting_suspension_enabled"`
	CloudflareTarget         *CloudflareTargetIn              `json:"cloudflare_target" doc:"May be null: no target."`
}

// ItemUpdate changes the fields sent and leaves the rest. A null clears a
// nullable column and is a 422 for the others. A cloudflare_target replaces
// the item's target, or adds one; null leaves it alone.
type ItemUpdate struct {
	Sku                      platform.Optional[string]        `json:"sku" maxLength:"50"`
	Name                     platform.NotNull[string]         `json:"name" minLength:"1" maxLength:"255"`
	ItemType                 platform.NotNull[string]         `json:"item_type" enum:"SERVICE,USAGE,FIXED_FEE"`
	Description              platform.Optional[string]        `json:"description"`
	DefaultCurrency          platform.NotNull[string]         `json:"default_currency" minLength:"3" maxLength:"3"`
	DefaultUnitPrice         platform.Optional[platform.Dec4] `json:"default_unit_price" doc:"At least 0, at most 4 decimal places."`
	DefaultPurchasePrice     platform.Optional[platform.Dec4] `json:"default_purchase_price" doc:"At least 0, at most 4 decimal places."`
	RevenueAccountID         platform.Optional[int32]         `json:"revenue_account_id"`
	ExpenseAccountID         platform.Optional[int32]         `json:"expense_account_id"`
	IsSold                   platform.NotNull[bool]           `json:"is_sold"`
	IsPurchased              platform.NotNull[bool]           `json:"is_purchased"`
	Active                   platform.NotNull[bool]           `json:"active"`
	IsHosting                platform.NotNull[bool]           `json:"is_hosting"`
	HostingDomain            platform.Optional[string]        `json:"hosting_domain" maxLength:"255" doc:"Trimmed and lower-cased."`
	HostingGraceDays         platform.Optional[int32]         `json:"hosting_grace_days" minimum:"0" maximum:"365"`
	HostingSuspensionEnabled platform.Optional[bool]          `json:"hosting_suspension_enabled"`
	HostingStatus            platform.Optional[string]        `json:"hosting_status" enum:"ACTIVE,SUSPEND_PENDING,SUSPENDED,CANCELLED" doc:"A hosting item left with none becomes ACTIVE."`
	CloudflareTarget         *CloudflareTargetIn              `json:"cloudflare_target" doc:"May be null: leaves the target as it is."`
}

// nullDec is Optional.Merge for a price, whose column type is NullDecimal.
func nullDec(f platform.Optional[platform.Dec4], cur decimal.NullDecimal) decimal.NullDecimal {
	if !f.Set {
		return cur
	}
	if f.Null {
		return decimal.NullDecimal{}
	}
	return decimal.NullDecimal{Decimal: f.Value.Decimal, Valid: true}
}
