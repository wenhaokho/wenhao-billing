package platform

import (
	"strings"
	"testing"
)

func TestVendorCreateValidation(t *testing.T) {
	for _, tc := range []struct {
		body   string
		errors int
	}{
		{`{"name":"Vendor"}`, 0},
		{`{"name":"","default_currency":null,"payment_terms_days":366,"active":null}`, 4},
		{`{"name":"Vendor","contact_email":"bad"}`, 1},
		{`{"name":"` + strings.Repeat("x", 256) + `"}`, 1},
		{`{"name":"Vendor","default_currency":"US","payment_terms_days":-1}`, 2},
		{`{"name":"Vendor","default_currency":"USDX"}`, 1},
		{`{"name":"Vendor","payment_terms_days":"30","active":"false"}`, 0},
	} {
		_, errs := parseVendorCreate([]byte(tc.body))
		if len(errs) != tc.errors {
			t.Errorf("%s: got %#v", tc.body, errs)
		}
	}
}
