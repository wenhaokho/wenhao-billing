// Package business serves the business profile (the singleton row 1 printed
// on invoices) and its payment accounts at /api/v1/business-profile, with the
// Python backend's contract.
package business

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/wenhaokho/wenhao-billing/backend/internal/business/businessdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// ProfileOut is the business profile, as both profile routes answer.
type ProfileOut struct {
	Name           *string       `json:"name"`
	Address        *string       `json:"address"`
	ContactEmail   *string       `json:"contact_email"`
	ContactPhone   *string       `json:"contact_phone"`
	InvoiceTitle   *string       `json:"invoice_title"`
	InvoiceSummary *string       `json:"invoice_summary"`
	LogoURL        *string       `json:"logo_url"`
	DefaultNotes   *string       `json:"default_notes"`
	Npwp           *string       `json:"npwp"`
	NpwpName       *string       `json:"npwp_name"`
	UpdatedAt      platform.Time `json:"updated_at"`
}

func profileOut(p businessdb.BusinessProfile) ProfileOut {
	return ProfileOut{
		Name: p.Name, Address: p.Address, ContactEmail: p.ContactEmail, ContactPhone: p.ContactPhone,
		InvoiceTitle: p.InvoiceTitle, InvoiceSummary: p.InvoiceSummary, LogoURL: p.LogoUrl,
		DefaultNotes: p.DefaultNotes, Npwp: p.Npwp, NpwpName: p.NpwpName,
		UpdatedAt: platform.Time{Time: p.UpdatedAt},
	}
}

// Profile reads the business profile. When row 1 is missing (it is seeded,
// so only after someone deleted it) it answers every field null with
// updated_at = now, and writes nothing: the next write creates the row.
func Profile(ctx context.Context, q *businessdb.Queries, now time.Time) (ProfileOut, error) {
	p, err := q.GetProfile(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProfileOut{UpdatedAt: platform.Time{Time: now}}, nil
	}
	if err != nil {
		return ProfileOut{}, err
	}
	return profileOut(p), nil
}

// ProfileUpdate changes the fields sent and leaves the rest; null clears a
// field. The length limits are the columns'.
type ProfileUpdate struct {
	Name           platform.Optional[string] `json:"name" maxLength:"255"`
	Address        platform.Optional[string] `json:"address"`
	ContactEmail   platform.Optional[string] `json:"contact_email" maxLength:"255"`
	ContactPhone   platform.Optional[string] `json:"contact_phone" maxLength:"50"`
	InvoiceTitle   platform.Optional[string] `json:"invoice_title" maxLength:"255"`
	InvoiceSummary platform.Optional[string] `json:"invoice_summary"`
	LogoURL        platform.Optional[string] `json:"logo_url"`
	DefaultNotes   platform.Optional[string] `json:"default_notes"`
	Npwp           platform.Optional[string] `json:"npwp" maxLength:"32"`
	NpwpName       platform.Optional[string] `json:"npwp_name" maxLength:"255"`
}

// anySet reports whether at least one field was sent.
func (u ProfileUpdate) anySet() bool {
	for _, f := range []platform.Optional[string]{
		u.Name, u.Address, u.ContactEmail, u.ContactPhone, u.InvoiceTitle,
		u.InvoiceSummary, u.LogoURL, u.DefaultNotes, u.Npwp, u.NpwpName,
	} {
		if f.Set {
			return true
		}
	}
	return false
}

// PaymentAccountOut is one account the invoices print for a currency.
type PaymentAccountOut struct {
	ID           int32         `json:"id"`
	Currency     string        `json:"currency"`
	Instructions string        `json:"instructions"`
	IsDefault    bool          `json:"is_default"`
	UpdatedAt    platform.Time `json:"updated_at"`
}

// PaymentAccounts lists the payment accounts by currency.
func PaymentAccounts(ctx context.Context, q *businessdb.Queries) ([]PaymentAccountOut, error) {
	rows, err := q.ListPaymentAccounts(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]PaymentAccountOut, 0, len(rows))
	for _, a := range rows {
		out = append(out, PaymentAccountOut{
			ID: a.ID, Currency: a.Currency, Instructions: a.Instructions, IsDefault: a.IsDefault,
			UpdatedAt: platform.Time{Time: a.UpdatedAt},
		})
	}
	return out, nil
}

// PaymentAccountIn is one account of the list a PUT replaces them with.
type PaymentAccountIn struct {
	Currency     string                 `json:"currency" doc:"IDR, SGD or USD, in any case; stored upper-case."`
	Instructions string                 `json:"instructions"`
	IsDefault    platform.NotNull[bool] `json:"is_default" doc:"Defaults to false."`
}
