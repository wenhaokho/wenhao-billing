// Package vendors serves the vendor master data at /api/v1/vendors with the
// Python backend's contract: list (optionally by active), create, read,
// partial update, and deactivate. Vendors are never deleted.
package vendors

import (
	"github.com/google/uuid"

	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
	"github.com/wenhaokho/wenhao-billing/backend/internal/vendors/vendorsdb"
)

// VendorOut is a vendor, as every route answers.
type VendorOut struct {
	Name             string        `json:"name"`
	ContactEmail     *string       `json:"contact_email"`
	ContactName      *string       `json:"contact_name"`
	DefaultCurrency  string        `json:"default_currency"`
	PaymentTermsDays int32         `json:"payment_terms_days"`
	TaxID            *string       `json:"tax_id"`
	Notes            *string       `json:"notes"`
	Active           bool          `json:"active"`
	VendorID         uuid.UUID     `json:"vendor_id"`
	CreatedAt        platform.Time `json:"created_at"`
}

func vendorOut(v vendorsdb.Vendor) VendorOut {
	return VendorOut{
		Name: v.Name, ContactEmail: v.ContactEmail, ContactName: v.ContactName,
		DefaultCurrency: v.DefaultCurrency, PaymentTermsDays: v.PaymentTermsDays,
		TaxID: v.TaxID, Notes: v.Notes, Active: v.Active, VendorID: v.VendorID,
		CreatedAt: platform.Time{Time: v.CreatedAt},
	}
}

// VendorCreate adds a vendor. The NOT NULL columns with a default may be left
// out but not sent as null (Python's field defaults); the nullable ones take
// null as no value.
type VendorCreate struct {
	Name             string                            `json:"name" minLength:"1" maxLength:"255"`
	ContactEmail     platform.Optional[platform.Email] `json:"contact_email" maxLength:"255"`
	ContactName      platform.Optional[string]         `json:"contact_name" maxLength:"255"`
	DefaultCurrency  platform.NotNull[string]          `json:"default_currency" minLength:"3" maxLength:"3" doc:"Defaults to IDR."`
	PaymentTermsDays platform.NotNull[int32]           `json:"payment_terms_days" minimum:"0" maximum:"365" doc:"Defaults to 30."`
	TaxID            platform.Optional[string]         `json:"tax_id" maxLength:"100"`
	Notes            platform.Optional[string]         `json:"notes"`
	Active           platform.NotNull[bool]            `json:"active" doc:"Defaults to true."`
}

// VendorUpdate changes the fields sent and leaves the rest. A null clears a
// nullable column and is a 422 for the others.
type VendorUpdate struct {
	Name             platform.NotNull[string]          `json:"name" minLength:"1" maxLength:"255"`
	ContactEmail     platform.Optional[platform.Email] `json:"contact_email" maxLength:"255"`
	ContactName      platform.Optional[string]         `json:"contact_name" maxLength:"255"`
	DefaultCurrency  platform.NotNull[string]          `json:"default_currency" minLength:"3" maxLength:"3"`
	PaymentTermsDays platform.NotNull[int32]           `json:"payment_terms_days" minimum:"0" maximum:"365"`
	TaxID            platform.Optional[string]         `json:"tax_id" maxLength:"100"`
	Notes            platform.Optional[string]         `json:"notes"`
	Active           platform.NotNull[bool]            `json:"active"`
}

// nullable is the column value for a nullable request field: the value when
// sent, nil when sent as null, otherwise cur.
func nullable[T any](f platform.Optional[T], cur *T) *T {
	if !f.Set {
		return cur
	}
	if f.Null {
		return nil
	}
	v := f.Value
	return &v
}

// emailPtr is nullable for the email field, whose request type is Email and
// whose column is text.
func emailPtr(f platform.Optional[platform.Email], cur *string) *string {
	if !f.Set {
		return cur
	}
	if f.Null {
		return nil
	}
	s := string(f.Value)
	return &s
}
