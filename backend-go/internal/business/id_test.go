package business

import "testing"

func TestParse(t *testing.T) {
	const canonical = "123e4567-e89b-12d3-a456-426614174000"
	for _, s := range []string{"", "invalid", "123e4567Xe89bX12d3Xa456X426614174000", "123e4567-e89b-12d3-a456-42661417400-", "123e4567e89b12d3a456426614174000", "00000000-0000-0000-0000-000000000000"} {
		if _, err := Parse(s); err == nil {
			t.Errorf("Parse(%q) succeeded", s)
		}
	}
	id, err := Parse(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if err := id.Validate(); err != nil {
		t.Fatal(err)
	}
	if id.String() != canonical || !id.UUID().Valid {
		t.Fatalf("bad round trip: %q %#v", id.String(), id.UUID())
	}
	again, err := Parse(id.String())
	if err != nil || again != id {
		t.Fatalf("round trip: %v", err)
	}
	if err := (ID{}).Validate(); err == nil {
		t.Fatal("zero value validated")
	}
}
