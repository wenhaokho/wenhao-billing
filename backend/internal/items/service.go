package items

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/items/itemsdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// Get is the item with its Cloudflare target, read through q (a pool or a
// transaction). An unknown id is pgx.ErrNoRows, unwrapped: test it with
// errors.Is(err, pgx.ErrNoRows).
func Get(ctx context.Context, q *itemsdb.Queries, id uuid.UUID) (ItemOut, error) {
	it, err := q.GetItem(ctx, id)
	if err != nil {
		return ItemOut{}, err
	}
	t, err := target(ctx, q, id)
	if err != nil {
		return ItemOut{}, err
	}
	return itemOut(it, t), nil
}

// target is the item's Cloudflare target, nil when it has none.
func target(ctx context.Context, q *itemsdb.Queries, itemID uuid.UUID) (*itemsdb.CloudflareTarget, error) {
	t, err := q.GetTarget(ctx, itemID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// list is the items the filters select, each with its target.
func list(ctx context.Context, q *itemsdb.Queries, p itemsdb.ListItemsParams) ([]ItemOut, error) {
	rows, err := q.ListItems(ctx, p)
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, len(rows))
	for i, it := range rows {
		ids[i] = it.ItemID
	}
	targets, err := q.ListTargets(ctx, ids)
	if err != nil {
		return nil, err
	}
	byItem := make(map[uuid.UUID]*itemsdb.CloudflareTarget, len(targets))
	for i := range targets {
		byItem[targets[i].ItemID] = &targets[i]
	}
	out := make([]ItemOut, 0, len(rows))
	for _, it := range rows {
		out = append(out, itemOut(it, byItem[it.ItemID]))
	}
	return out, nil
}

// List is the items matching p, exported for the MCP list_items tool
// (inventory §4): p's active, item_type and is_hosting filters, its
// NameLike name substring and its RowLimit cap (nil is unlimited).
func List(ctx context.Context, q *itemsdb.Queries, p itemsdb.ListItemsParams) ([]ItemOut, error) {
	return list(ctx, q, p)
}

// Create is the create route's item: the price and hosting checks, then
// create on q (a transaction). Exported for the MCP create_item tool
// (inventory §4).
func Create(ctx context.Context, q *itemsdb.Queries, b ItemCreate) (ItemOut, error) {
	if err := checkPrices(b.DefaultUnitPrice, b.DefaultPurchasePrice); err != nil {
		return ItemOut{}, err
	}
	if err := checkHosting(b); err != nil {
		return ItemOut{}, err
	}
	return create(ctx, q, b)
}

// Update is the update route's item: the price check, then update on q (a
// transaction). Exported for the MCP update_item tool (inventory §4).
func Update(ctx context.Context, q *itemsdb.Queries, id uuid.UUID, b ItemUpdate) (ItemOut, error) {
	if err := checkPrices(b.DefaultUnitPrice, b.DefaultPurchasePrice); err != nil {
		return ItemOut{}, err
	}
	return update(ctx, q, id, b)
}

// negativeSentence is pydantic's refusal of a price below `Field(ge=0)`.
// Dec4 itself refuses more than 4 places or 15 digits before the point.
const negativeSentence = "Input should be greater than or equal to 0"

// priceRefusal is why a price sent in f is refused, or "".
func priceRefusal(f platform.Optional[platform.Dec4]) string {
	if f.Set && !f.Null && f.Value.Decimal().Sign() < 0 {
		return negativeSentence
	}
	return ""
}

// checkPrices is the 422 for the first refused price, at its field, or nil.
func checkPrices(unit, purchase platform.Optional[platform.Dec4]) error {
	if s := priceRefusal(unit); s != "" {
		return platform.FieldError("default_unit_price", s)
	}
	if s := priceRefusal(purchase); s != "" {
		return platform.FieldError("default_purchase_price", s)
	}
	return nil
}

// checkHosting is Python's ItemCreate rule: a hosting item names everything
// enforcement needs. The missing fields are listed in this order.
func checkHosting(b ItemCreate) error {
	if !b.IsHosting.Or(false) {
		return nil
	}
	var missing []string
	if d := b.HostingDomain.Merge(nil); d == nil || *d == "" {
		missing = append(missing, "hosting_domain")
	}
	if b.HostingGraceDays.Merge(nil) == nil {
		missing = append(missing, "hosting_grace_days")
	}
	if b.HostingSuspensionEnabled.Merge(nil) == nil {
		missing = append(missing, "hosting_suspension_enabled")
	}
	if b.CloudflareTarget == nil {
		missing = append(missing, "cloudflare_target")
	}
	if missing == nil {
		return nil
	}
	return platform.Err(http.StatusUnprocessableEntity, "hosting_item_incomplete", "hosting item requires: "+strings.Join(missing, ", "))
}

// domain is the hosting domain as stored: trimmed and lower-cased. Python
// normalises only a non-empty value, so "" is kept as sent.
func domain(d *string) *string {
	if d == nil || *d == "" {
		return d
	}
	s := strings.ToLower(strings.TrimSpace(*d))
	return &s
}

// upsertTarget writes the item's target from in, every string trimmed,
// record_name lower-cased and record_type upper-cased. A nil proxied keeps
// the stored value (true for a new target), as a PATCH that leaves it out.
func upsertTarget(ctx context.Context, q *itemsdb.Queries, itemID uuid.UUID, in CloudflareTargetIn, proxied *bool) (*itemsdb.CloudflareTarget, error) {
	t, err := q.UpsertTarget(ctx, itemsdb.UpsertTargetParams{
		TargetID:           uuid.New(),
		ItemID:             itemID,
		ZoneID:             strings.TrimSpace(in.ZoneID),
		RecordID:           strings.TrimSpace(in.RecordID),
		RecordName:         strings.ToLower(strings.TrimSpace(in.RecordName)),
		RecordType:         strings.ToUpper(strings.TrimSpace(in.RecordType)),
		LiveContent:        strings.TrimSpace(in.LiveContent),
		MaintenanceContent: strings.TrimSpace(in.MaintenanceContent),
		Proxied:            proxied,
	})
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// statusActive is the hosting status of a new or newly hosting item.
const statusActive = "ACTIVE"

// create writes the item, then its target if one was sent, on q (a
// transaction).
func create(ctx context.Context, q *itemsdb.Queries, b ItemCreate) (ItemOut, error) {
	isHosting := b.IsHosting.Or(false)
	var status *string
	if isHosting {
		s := statusActive
		status = &s
	}
	it, err := q.CreateItem(ctx, itemsdb.CreateItemParams{
		ItemID:                   uuid.New(),
		Sku:                      b.Sku.Merge(nil),
		Name:                     b.Name,
		ItemType:                 b.ItemType,
		Description:              b.Description.Merge(nil),
		DefaultCurrency:          b.DefaultCurrency.Or("IDR"),
		DefaultUnitPrice:         nullDec(b.DefaultUnitPrice, decimal.NullDecimal{}),
		DefaultPurchasePrice:     nullDec(b.DefaultPurchasePrice, decimal.NullDecimal{}),
		RevenueAccountID:         b.RevenueAccountID.Merge(nil),
		ExpenseAccountID:         b.ExpenseAccountID.Merge(nil),
		IsSold:                   b.IsSold.Or(true),
		IsPurchased:              b.IsPurchased.Or(false),
		Active:                   b.Active.Or(true),
		IsHosting:                isHosting,
		HostingDomain:            domain(b.HostingDomain.Merge(nil)),
		HostingGraceDays:         b.HostingGraceDays.Merge(nil),
		HostingSuspensionEnabled: b.HostingSuspensionEnabled.Merge(nil),
		HostingStatus:            status,
	})
	if err != nil {
		return ItemOut{}, platform.MapConstraint(err, constraints)
	}
	var t *itemsdb.CloudflareTarget
	if b.CloudflareTarget != nil {
		proxied := b.CloudflareTarget.Proxied.Or(true)
		if t, err = upsertTarget(ctx, q, it.ItemID, *b.CloudflareTarget, &proxied); err != nil {
			return ItemOut{}, err
		}
	}
	return itemOut(it, t), nil
}

// update is the PATCH pattern on q, a transaction: read the row locked,
// overlay the fields sent, write every column back, then replace or add the
// target if one was sent. An unknown id is pgx.ErrNoRows.
func update(ctx context.Context, q *itemsdb.Queries, id uuid.UUID, b ItemUpdate) (ItemOut, error) {
	cur, err := q.GetItemForUpdate(ctx, id)
	if err != nil {
		return ItemOut{}, err
	}
	p := itemsdb.UpdateItemParams{
		ItemID:                   cur.ItemID,
		Sku:                      b.Sku.Merge(cur.Sku),
		Name:                     b.Name.Or(cur.Name),
		ItemType:                 b.ItemType.Or(cur.ItemType),
		Description:              b.Description.Merge(cur.Description),
		DefaultCurrency:          b.DefaultCurrency.Or(cur.DefaultCurrency),
		DefaultUnitPrice:         nullDec(b.DefaultUnitPrice, cur.DefaultUnitPrice),
		DefaultPurchasePrice:     nullDec(b.DefaultPurchasePrice, cur.DefaultPurchasePrice),
		RevenueAccountID:         b.RevenueAccountID.Merge(cur.RevenueAccountID),
		ExpenseAccountID:         b.ExpenseAccountID.Merge(cur.ExpenseAccountID),
		IsSold:                   b.IsSold.Or(cur.IsSold),
		IsPurchased:              b.IsPurchased.Or(cur.IsPurchased),
		Active:                   b.Active.Or(cur.Active),
		IsHosting:                b.IsHosting.Or(cur.IsHosting),
		HostingDomain:            cur.HostingDomain,
		HostingGraceDays:         b.HostingGraceDays.Merge(cur.HostingGraceDays),
		HostingSuspensionEnabled: b.HostingSuspensionEnabled.Merge(cur.HostingSuspensionEnabled),
		HostingStatus:            b.HostingStatus.Merge(cur.HostingStatus),
	}
	if b.HostingDomain.Set {
		p.HostingDomain = domain(b.HostingDomain.Merge(nil))
	}
	if p.IsHosting && p.HostingStatus == nil {
		s := statusActive
		p.HostingStatus = &s
	}
	it, err := q.UpdateItem(ctx, p)
	if err != nil {
		return ItemOut{}, platform.MapConstraint(err, constraints)
	}
	var t *itemsdb.CloudflareTarget
	if b.CloudflareTarget != nil {
		var proxied *bool
		if p := b.CloudflareTarget.Proxied; p.Set {
			proxied = &p.Value
		}
		t, err = upsertTarget(ctx, q, it.ItemID, *b.CloudflareTarget, proxied)
	} else {
		t, err = target(ctx, q, it.ItemID)
	}
	if err != nil {
		return ItemOut{}, err
	}
	return itemOut(it, t), nil
}
