package platform

import "testing"

func TestParseVendorUpdate(t *testing.T) {
	for _, tc := range []struct {
		body   string
		errors int
	}{
		{`{}`, 0}, {`{"name":"","contact_email":null,"active":null}`, 0},
		{`{"default_currency":"US","payment_terms_days":366}`, 2},
		{`{"contact_email":"bad","active":"maybe"}`, 2},
	} {
		_, errs := parseVendorUpdate([]byte(tc.body))
		if len(errs) != tc.errors {
			t.Errorf("%s: got %v", tc.body, errs)
		}
	}
}
