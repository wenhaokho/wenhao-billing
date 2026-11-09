package invoices

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/wenhaokho/wenhao-billing/backend/internal/invoices/invoicesdb"
	"github.com/wenhaokho/wenhao-billing/backend/internal/platform"
)

// wantWHT checks a resolved or merged withholding.
func wantWHT(t *testing.T, gotType string, gotRate *decimal.Decimal, gotGrossUp bool, err error, wantType string, wantRate *string, wantGrossUp bool) {
	t.Helper()
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if gotType != wantType || gotGrossUp != wantGrossUp {
		t.Errorf("type, gross_up = %s, %v; want %s, %v", gotType, gotGrossUp, wantType, wantGrossUp)
	}
	switch {
	case wantRate == nil && gotRate != nil:
		t.Errorf("rate = %s, want nil", gotRate)
	case wantRate != nil && (gotRate == nil || !gotRate.Equal(dec(*wantRate))):
		t.Errorf("rate = %v, want %s", gotRate, *wantRate)
	}
}

func newCustomerWithWHT(t *testing.T, whtType string, rate *string, grossUp bool) (*invoicesdb.Queries, uuid.UUID) {
	t.Helper()
	a := newApp(t)
	id := uuid.New()
	if _, err := a.Pool.Exec(t.Context(), `INSERT INTO customers (customer_id, name, default_wht_type, default_wht_rate, default_wht_gross_up)
VALUES ($1, 'Acme', $2, $3::numeric, $4)`, id, whtType, rate, grossUp); err != nil {
		t.Fatal(err)
	}
	return invoicesdb.New(a.Pool), id
}

func TestResolveWHTPayloadWins(t *testing.T) {
	t.Parallel()
	q, id := newCustomerWithWHT(t, "PPH23", strPtr("3"), true)
	// An explicit NONE beats the customer's default.
	typ, rate, gross, err := ResolveWHT(t.Context(), q, &id, strPtr("NONE"), nil, nil)
	wantWHT(t, typ, rate, gross, err, "NONE", nil, false)
	// A payload type takes its own default rate; a nil gross-up is false.
	typ, rate, gross, err = ResolveWHT(t.Context(), q, &id, strPtr("PPH21"), nil, nil)
	wantWHT(t, typ, rate, gross, err, "PPH21", strPtr("2.5"), false)
	// A refused payload is an ErrInvoicing with withholding's sentence.
	_, _, _, err = ResolveWHT(t.Context(), q, &id, strPtr("PPH23"), decPtr("100"), nil)
	if !errors.Is(err, ErrInvoicing) || err.Error() != "Withholding tax rate must be greater than 0 and less than 100" {
		t.Errorf("err = %v, want ErrInvoicing with the rate sentence", err)
	}
	_, _, _, err = ResolveWHT(t.Context(), q, &id, strPtr("VAT"), nil, nil)
	if !errors.Is(err, ErrInvoicing) || err.Error() != "Unknown withholding tax type: VAT" {
		t.Errorf("err = %v, want ErrInvoicing with the type sentence", err)
	}
}

func TestResolveWHTCustomerDefault(t *testing.T) {
	t.Parallel()
	q, id := newCustomerWithWHT(t, "PPH23", strPtr("3"), true)
	typ, rate, gross, err := ResolveWHT(t.Context(), q, &id, nil, decPtr("9"), nil)
	wantWHT(t, typ, rate, gross, err, "PPH23", strPtr("3"), true)

	// A stored default without a rate is normalized to the type's default.
	q, id = newCustomerWithWHT(t, "PPH21", nil, false)
	typ, rate, gross, err = ResolveWHT(t.Context(), q, &id, nil, nil, nil)
	wantWHT(t, typ, rate, gross, err, "PPH21", strPtr("2.5"), false)
}

func TestResolveWHTNoCustomer(t *testing.T) {
	t.Parallel()
	q, _ := newCustomerWithWHT(t, "PPH23", strPtr("3"), true)
	typ, rate, gross, err := ResolveWHT(t.Context(), q, nil, nil, nil, nil)
	wantWHT(t, typ, rate, gross, err, "NONE", nil, false)
	unknown := uuid.New()
	typ, rate, gross, err = ResolveWHT(t.Context(), q, &unknown, nil, nil, nil)
	wantWHT(t, typ, rate, gross, err, "NONE", nil, false)
}

func set[T any](v T) platform.Optional[T] { return platform.Optional[T]{Set: true, Value: v} }

func null[T any]() platform.Optional[T] { return platform.Optional[T]{Set: true, Null: true} }

func rateIn(s string) platform.Optional[platform.Dec4] { return set(platform.NewDec4(dec(s))) }

func TestMergeWHTUntouchedKeepsStoredWithoutNormalizing(t *testing.T) {
	t.Parallel()
	// NONE with a rate and gross-up would normalize to (NONE, nil, false).
	typ, rate, gross, err := MergeWHT("NONE", decPtr("7"), true,
		platform.Optional[string]{}, platform.Optional[platform.Dec4]{}, platform.Optional[bool]{})
	wantWHT(t, typ, rate, gross, err, "NONE", strPtr("7"), true)
	// Even a stored value Normalize would refuse comes back unchanged.
	typ, rate, gross, err = MergeWHT("PPH23", decPtr("150"), false,
		platform.Optional[string]{}, platform.Optional[platform.Dec4]{}, platform.Optional[bool]{})
	wantWHT(t, typ, rate, gross, err, "PPH23", strPtr("150"), false)
}

func TestMergeWHTTypeSwitchTakesNewDefault(t *testing.T) {
	t.Parallel()
	typ, rate, gross, err := MergeWHT("PPH23", decPtr("3"), true, set("PPH21"), platform.Optional[platform.Dec4]{}, platform.Optional[bool]{})
	wantWHT(t, typ, rate, gross, err, "PPH21", strPtr("2.5"), true)
	// An explicit null rate is the same as leaving it out.
	typ, rate, gross, err = MergeWHT("PPH23", decPtr("3"), false, set("PPH21"), null[platform.Dec4](), platform.Optional[bool]{})
	wantWHT(t, typ, rate, gross, err, "PPH21", strPtr("2.5"), false)
	// A sent rate wins.
	typ, rate, gross, err = MergeWHT("PPH23", decPtr("3"), false, set("PPH21"), rateIn("4"), platform.Optional[bool]{})
	wantWHT(t, typ, rate, gross, err, "PPH21", strPtr("4"), false)
	// Switching to NONE clears the rate and gross-up.
	typ, rate, gross, err = MergeWHT("PPH23", decPtr("3"), true, set("NONE"), platform.Optional[platform.Dec4]{}, platform.Optional[bool]{})
	wantWHT(t, typ, rate, gross, err, "NONE", nil, false)
}

func TestMergeWHTGrossUpOnlyKeepsTypeAndRate(t *testing.T) {
	t.Parallel()
	typ, rate, gross, err := MergeWHT("PPH21", decPtr("3"), false, platform.Optional[string]{}, platform.Optional[platform.Dec4]{}, set(true))
	wantWHT(t, typ, rate, gross, err, "PPH21", strPtr("3"), true)
	// A null or empty type falls back to the stored one (Python's `or`), and
	// so keeps the stored rate.
	typ, rate, gross, err = MergeWHT("PPH21", decPtr("3"), true, null[string](), platform.Optional[platform.Dec4]{}, null[bool]())
	wantWHT(t, typ, rate, gross, err, "PPH21", strPtr("3"), true)
	typ, rate, gross, err = MergeWHT("PPH21", decPtr("3"), true, set(""), platform.Optional[platform.Dec4]{}, set(false))
	wantWHT(t, typ, rate, gross, err, "PPH21", strPtr("3"), false)
	// Once a field is sent the result is normalized, so a bad rate is refused.
	_, _, _, err = MergeWHT("PPH21", decPtr("3"), false, platform.Optional[string]{}, rateIn("0"), platform.Optional[bool]{})
	if !errors.Is(err, ErrInvoicing) || err.Error() != "Withholding tax rate must be greater than 0 and less than 100" {
		t.Errorf("err = %v, want ErrInvoicing with the rate sentence", err)
	}
}
