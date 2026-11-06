package platform

import (
	"testing"
)

func TestPythonUUIDPathForms(t *testing.T) {
	valid := []string{"10000000-0000-0000-0000-000000000001", "10000000000000000000000000000001", "{10000000-0000-0000-0000-000000000001}", "urn:uuid:10000000-0000-0000-0000-000000000001"}
	for _, s := range valid {
		if _, ok := parsePathUUID(s); !ok {
			t.Errorf("valid %s", s)
		}
	}
	for _, s := range []string{"10000000x0000x0000x0000x000000000001", "{10000000000000000000000000000001}"} {
		if _, ok := parsePathUUID(s); ok {
			t.Errorf("invalid accepted %s", s)
		}
	}
}
