package compare

import (
	"strings"
	"testing"
	"time"
)

func TestIgnoreNeedsNote(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name:    "an ignore entry without a note",
			yaml:    "- route: GET /api/v1/invoices\n  path: $.items\n  rule: ignore\n",
			wantErr: "has no note",
		},
		{
			name: "an entry with a note",
			yaml: "- route: GET /api/v1/invoices\n  path: $.items\n  rule: ignore\n  note: PN 78\n",
		},
		{
			name:    "an unknown rule",
			yaml:    "- route: GET /api/v1/invoices\n  path: $.items\n  rule: whatever\n  note: PN 1\n",
			wantErr: "unknown rule",
		},
		{
			name:    "a missing route",
			yaml:    "- path: $.items\n  rule: ignore\n  note: PN 1\n",
			wantErr: "has no route",
		},
		{
			name:    "a missing path",
			yaml:    "- route: GET /api/v1/invoices\n  rule: ignore\n  note: PN 1\n",
			wantErr: "has no path",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseAllowed([]byte(tc.yaml))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("ParseAllowed: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("ParseAllowed succeeded, want an error")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q, want it to hold %q", err, tc.wantErr)
			}
		})
	}
}

func TestLoadAllowedFile(t *testing.T) {
	t.Parallel()
	a, err := LoadAllowedFile("../../port/allowed-diffs.yaml")
	if err != nil {
		t.Fatalf("LoadAllowedFile: %v", err)
	}
	if len(a.Entries) == 0 {
		t.Fatal("allowed-diffs.yaml has no entries")
	}
	for _, e := range a.Entries {
		if !knownRules[e.Rule] {
			t.Fatalf("entry %+v has an unknown rule", e)
		}
		if strings.TrimSpace(e.Note) == "" {
			t.Fatalf("entry %+v has no note", e)
		}
	}
}

func TestRouteMatches(t *testing.T) {
	t.Parallel()
	cases := []struct {
		pattern, route string
		want           bool
	}{
		{"GET /api/v1/invoices", "GET /api/v1/invoices", true},
		{"GET /api/v1/invoices/{id}", "GET /api/v1/invoices/8f14e45f", true},
		{"GET /api/v1/invoices/{id}", "GET /api/v1/invoices/8f14e45f/pdf", false},
		{"GET /api/v1/invoices/{id}", "POST /api/v1/invoices/8f14e45f", false},
		{"GET /api/v1/invoices/{id}", "GET /api/v1/bills/8f14e45f", false},
		{"/api/v1/invoices", "GET /api/v1/invoices", true},
		{"GET /api/v1/invoices", "GET /api/v1/invoices?status=OPEN", true},
	}
	for _, tc := range cases {
		t.Run(tc.pattern+" against "+tc.route, func(t *testing.T) {
			t.Parallel()
			if got := routeMatches(tc.pattern, tc.route); got != tc.want {
				t.Fatalf("routeMatches(%q, %q) = %v, want %v", tc.pattern, tc.route, got, tc.want)
			}
		})
	}
}

func TestParseInstant(t *testing.T) {
	t.Parallel()
	zone := time.FixedZone("WIB", 7*3600)
	cases := []struct {
		in   string
		loc  *time.Location
		want time.Time
		ok   bool
	}{
		{"2026-01-01T00:00:00Z", time.UTC, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), true},
		{"2026-01-01T07:00:00+07:00", time.UTC, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), true},
		{"2026-01-01T07:00:00", zone, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), true},
		{"2026-01-01T00:00:00", time.UTC, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), true},
		{"2026-01-01", time.UTC, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), true},
		{"not a time", time.UTC, time.Time{}, false},
		{"1500.0000", time.UTC, time.Time{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got, ok := parseInstant(tc.in, tc.loc)
			if ok != tc.ok {
				t.Fatalf("parseInstant(%q) ok = %v, want %v", tc.in, ok, tc.ok)
			}
			if ok && !got.Equal(tc.want) {
				t.Fatalf("parseInstant(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
