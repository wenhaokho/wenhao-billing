package items

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/LOQATO/gokit/apierr"
	"github.com/LOQATO/gokit/i18n"

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

// The sentences pydantic answers for a price outside
// `Field(ge=0, max_digits=19, decimal_places=4)`.
const (
	negativeSentence  = "Input should be greater than or equal to 0"
	placesSentence    = "Decimal input should have no more than 4 decimal places"
	wholeSentence     = "Decimal input should have no more than 15 digits before the decimal point"
	validationSummary = "Request validation failed"
)

// maxPrice is the smallest value numeric(19,4) cannot hold: 15 integer digits.
var maxPrice = decimal.New(1, 15)

// priceRefusal is why a price sent in f is refused, or "". The Dec4 type
// takes any decimal, so the column's scale and precision are checked here.
func priceRefusal(f platform.Optional[platform.Dec4]) string {
	if !f.Set || f.Null {
		return ""
	}
	d := f.Value.Decimal
	switch {
	case d.Sign() < 0:
		return negativeSentence
	case !d.Equal(d.Round(4)):
		return placesSentence
	case d.Cmp(maxPrice) >= 0:
		return wholeSentence
	}
	return ""
}

// checkPrices is the 422 for refused prices, each at its field, or nil.
func checkPrices(unit, purchase platform.Optional[platform.Dec4]) error {
	var details []apierr.Detail
	for _, f := range []struct {
		name  string
		value platform.Optional[platform.Dec4]
	}{{"default_unit_price", unit}, {"default_purchase_price", purchase}} {
		if s := priceRefusal(f.value); s != "" {
			details = append(details, apierr.Detail{Field: f.name, Message: i18n.T(s)})
		}
	}
	if details == nil {
		return nil
	}
	status := http.StatusUnprocessableEntity
	return apierr.New(status, apierr.Codes[status], i18n.T(validationSummary), details...)
}

// checkHosting is Python's ItemCreate rule: a hosting item names everything
// enforcement needs. The missing fields are listed in this order.
func checkHosting(b ItemCreate) error {
	if !b.IsHosting.Or(false) {
		return nil
	}
	var missing []string
	if d := nullable(b.HostingDomain, nil); d == nil || *d == "" {
		missing = append(missing, "hosting_domain")
	}
	if nullable(b.HostingGraceDays, nil) == nil {
		missing = append(missing, "hosting_grace_days")
	}
	if nullable(b.HostingSuspensionEnabled, nil) == nil {
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
// record_name lower-cased and record_type upper-cased.
func upsertTarget(ctx context.Context, q *itemsdb.Queries, itemID uuid.UUID, in CloudflareTargetIn) (*itemsdb.CloudflareTarget, error) {
	t, err := q.UpsertTarget(ctx, itemsdb.UpsertTargetParams{
		TargetID:           uuid.New(),
		ItemID:             itemID,
		ZoneID:             strings.TrimSpace(in.ZoneID),
		RecordID:           strings.TrimSpace(in.RecordID),
		RecordName:         strings.ToLower(strings.TrimSpace(in.RecordName)),
		RecordType:         strings.ToUpper(strings.TrimSpace(in.RecordType)),
		LiveContent:        strings.TrimSpace(in.LiveContent),
		MaintenanceContent: strings.TrimSpace(in.MaintenanceContent),
		Proxied:            in.Proxied.Or(true),
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
		Sku:                      nullable(b.Sku, nil),
		Name:                     b.Name,
		ItemType:                 b.ItemType,
		Description:              nullable(b.Description, nil),
		DefaultCurrency:          b.DefaultCurrency.Or("IDR"),
		DefaultUnitPrice:         nullDec(b.DefaultUnitPrice, decimal.NullDecimal{}),
		DefaultPurchasePrice:     nullDec(b.DefaultPurchasePrice, decimal.NullDecimal{}),
		RevenueAccountID:         nullable(b.RevenueAccountID, nil),
		ExpenseAccountID:         nullable(b.ExpenseAccountID, nil),
		IsSold:                   b.IsSold.Or(true),
		IsPurchased:              b.IsPurchased.Or(false),
		Active:                   b.Active.Or(true),
		IsHosting:                isHosting,
		HostingDomain:            domain(nullable(b.HostingDomain, nil)),
		HostingGraceDays:         nullable(b.HostingGraceDays, nil),
		HostingSuspensionEnabled: nullable(b.HostingSuspensionEnabled, nil),
		HostingStatus:            status,
	})
	if err != nil {
		return ItemOut{}, platform.MapConstraint(err, constraints)
	}
	var t *itemsdb.CloudflareTarget
	if b.CloudflareTarget != nil {
		if t, err = upsertTarget(ctx, q, it.ItemID, *b.CloudflareTarget); err != nil {
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
		Sku:                      nullable(b.Sku, cur.Sku),
		Name:                     b.Name.Or(cur.Name),
		ItemType:                 b.ItemType.Or(cur.ItemType),
		Description:              nullable(b.Description, cur.Description),
		DefaultCurrency:          b.DefaultCurrency.Or(cur.DefaultCurrency),
		DefaultUnitPrice:         nullDec(b.DefaultUnitPrice, cur.DefaultUnitPrice),
		DefaultPurchasePrice:     nullDec(b.DefaultPurchasePrice, cur.DefaultPurchasePrice),
		RevenueAccountID:         nullable(b.RevenueAccountID, cur.RevenueAccountID),
		ExpenseAccountID:         nullable(b.ExpenseAccountID, cur.ExpenseAccountID),
		IsSold:                   b.IsSold.Or(cur.IsSold),
		IsPurchased:              b.IsPurchased.Or(cur.IsPurchased),
		Active:                   b.Active.Or(cur.Active),
		IsHosting:                b.IsHosting.Or(cur.IsHosting),
		HostingDomain:            cur.HostingDomain,
		HostingGraceDays:         nullable(b.HostingGraceDays, cur.HostingGraceDays),
		HostingSuspensionEnabled: nullable(b.HostingSuspensionEnabled, cur.HostingSuspensionEnabled),
		HostingStatus:            nullable(b.HostingStatus, cur.HostingStatus),
	}
	if b.HostingDomain.Set {
		p.HostingDomain = domain(nullable(b.HostingDomain, nil))
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
		t, err = upsertTarget(ctx, q, it.ItemID, *b.CloudflareTarget)
	} else {
		t, err = target(ctx, q, it.ItemID)
	}
	if err != nil {
		return ItemOut{}, err
	}
	return itemOut(it, t), nil
}
