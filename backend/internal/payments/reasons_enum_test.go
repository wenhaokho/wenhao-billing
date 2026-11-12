package payments_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/danielgtaylor/huma/v2"

	"github.com/wenhaokho/wenhao-billing/backend/internal/payments"
	"github.com/wenhaokho/wenhao-billing/backend/internal/stats"
)

// enumOf returns the enum the schema publishes for the reason_codes items of
// the struct type t.
func enumOf(t *testing.T, typ reflect.Type) []string {
	t.Helper()
	reg := huma.NewMapRegistry("#/components/schemas/", huma.DefaultSchemaNamer)
	s := reg.Schema(typ, false, "")
	if s.Ref != "" {
		s = reg.SchemaFromRef(s.Ref)
	}
	prop := s.Properties["reason_codes"]
	if prop == nil || prop.Items == nil {
		t.Fatalf("%s: reason_codes is not an array schema", typ)
	}
	var out []string
	for _, v := range prop.Items.Enum {
		str, ok := v.(string)
		if !ok {
			t.Fatalf("%s: enum value %v is not a string", typ, v)
		}
		out = append(out, str)
	}
	return out
}

// Adding a code without adding it to the enum tags (or the reverse) must fail.
func TestReasonCodesEnumInSchema(t *testing.T) {
	t.Parallel()
	for name, typ := range map[string]reflect.Type{
		"AuditRow":     reflect.TypeFor[payments.AuditRow](),
		"LogEntry":     reflect.TypeFor[payments.LogEntry](),
		"ActivityItem": reflect.TypeFor[stats.ActivityItem](),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := slices.Clone(enumOf(t, typ))
			want := slices.Clone(payments.AllReasonCodes)
			slices.Sort(got)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Fatalf("schema enum %v != payments.AllReasonCodes %v", got, want)
			}
		})
	}
}

// AllReasonCodes must cover every code ReasonCodes can return.
func TestAllReasonCodesCoversReasonCode(t *testing.T) {
	t.Parallel()
	for _, text := range []string{
		"no open invoice matches", "currency mismatch: x", "amount mismatch: x", "ambiguous match: x",
		"exact amount+currency", "manual-entry", "manual approval against invoice x", "alias match: x",
		"confidence 0.5 below threshold", "invoice 1 changed before clearing", "whatever",
	} {
		if c := payments.ReasonCode(text); !slices.Contains(payments.AllReasonCodes, c) {
			t.Errorf("%q gives %q, missing from AllReasonCodes", text, c)
		}
	}
}
